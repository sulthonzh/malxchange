// Upstream internal-RPC clients for the surfaces admin-api fronts but
// does not own (ticket #61): the wallet's Coin config RPC and the
// exchange's Market-config + engine-status RPCs. The typed-client
// pattern is internal/api/funds.go's; the compose network boundary is
// the auth, and admin-api sets X-Mal-Actor on every mutating call so
// the upstream service log attributes the edit. The env names converge
// across the spec's tickets: MAL_ADMIN_<SVC>_BASE_URL (with the
// /internal suffix) and an optional MAL_ADMIN_<SVC>_TIMEOUT.
package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/syaikhipin/malxchange/internal/config"
)

// upstreamEnv is the operator wiring for the two upstreams, read once
// per process. Defaults are the compose service names with the /internal
// prefix the boot harness strips server-side.
type upstreamEnv struct {
	WalletBaseURL   string
	WalletTimeout   time.Duration
	ExchangeBaseURL string
	ExchangeTimeout time.Duration
}

func loadUpstreamEnv() upstreamEnv {
	return upstreamEnv{
		WalletBaseURL:   config.EnvDefault("MAL_ADMIN_WALLET_BASE_URL", "http://wallet:8080/internal"),
		WalletTimeout:   envDuration("MAL_ADMIN_WALLET_TIMEOUT", 5*time.Second),
		ExchangeBaseURL: config.EnvDefault("MAL_ADMIN_EXCHANGE_BASE_URL", "http://exchange:8080/internal"),
		ExchangeTimeout: envDuration("MAL_ADMIN_EXCHANGE_TIMEOUT", 5*time.Second),
	}
}

// upstreamSet is the Server's handle on its upstreams. Stubs replace
// the clients per-test by re-pointing the base URLs.
type upstreamSet struct {
	wallet   *walletUpstream
	exchange *exchangeUpstream
}

func loadUpstreams(env upstreamEnv, log *slog.Logger) *upstreamSet {
	return &upstreamSet{
		wallet:   &walletUpstream{base: strings.TrimRight(env.WalletBaseURL, "/"), http: clientFor(env.WalletTimeout), log: log},
		exchange: &exchangeUpstream{base: strings.TrimRight(env.ExchangeBaseURL, "/"), http: clientFor(env.ExchangeTimeout), log: log},
	}
}

func clientFor(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &http.Client{Timeout: timeout}
}

// upstreamError is one upstream error answer: its HTTP status and the
// sentence from the {"error": ...} body (service convention) — or a
// plain-text body.
type upstreamError struct {
	Service string
	Status  int
	Message string
}

func (e *upstreamError) Error() string {
	return fmt.Sprintf("%s %d: %s", e.Service, e.Status, e.Message)
}

// isUpstreamDown reports whether err is a transport failure (service
// unreachable or slow) rather than an upstream error answer.
func isUpstreamDown(err error) bool {
	var ue *upstreamError
	return !errors.As(err, &ue)
}

// call is the shared one-JSON-call primitive: non-2xx answers come
// back as *upstreamError, transport failures as a plain error.
func upstreamCall(ctx context.Context, client *http.Client, base, service, method, path string, body []byte, actor string) (int, []byte, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, rdr)
	if err != nil {
		return 0, nil, fmt.Errorf("%s: %w", service, err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		if actor != "" {
			req.Header.Set("X-Mal-Actor", actor)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("%s rpc unreachable: %w", service, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, nil, fmt.Errorf("%s rpc unreadable: %w", service, err)
	}
	if resp.StatusCode >= 400 {
		return resp.StatusCode, raw, &upstreamError{Service: service, Status: resp.StatusCode, Message: upstreamSentence(raw)}
	}
	return resp.StatusCode, raw, nil
}

// upstreamSentence digs the human sentence out of an upstream error
// body: the {"error": ...} envelope, else the trimmed body text.
func upstreamSentence(raw []byte) string {
	var env struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err == nil && env.Error != "" {
		return env.Error
	}
	return strings.TrimSpace(string(raw))
}

// ---- wallet (Coin config) ----

type walletUpstream struct {
	base string
	http *http.Client
	log  *slog.Logger
}

func (u *walletUpstream) call(ctx context.Context, method, path string, body []byte, actor string) (int, []byte, error) {
	return upstreamCall(ctx, u.http, u.base, "wallet", method, path, body, actor)
}

// ---- exchange (Market config + engine status) ----

type exchangeUpstream struct {
	base string
	http *http.Client
	log  *slog.Logger
}

func (u *exchangeUpstream) call(ctx context.Context, method, path string, body []byte, actor string) (int, []byte, error) {
	return upstreamCall(ctx, u.http, u.base, "exchange", method, path, body, actor)
}

// envOnce-style lazy load: the upstream wiring is process-global like
// AdminEnv's, read on first use.
var (
	upstreamOnce   sync.Once
	upstreamEnvVal upstreamEnv
)

func sharedUpstreamEnv() upstreamEnv {
	upstreamOnce.Do(func() { upstreamEnvVal = loadUpstreamEnv() })
	return upstreamEnvVal
}
