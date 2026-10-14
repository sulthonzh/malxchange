// The wallet internal-RPC client for the funds surface (spec #59): a
// typed client over the same seam internal/api/funds.go uses for the
// member side — prefix-free paths on the boot harness's internal mux,
// so the base URL carries /internal (compose serves it; Caddy never
// proxies /internal, the network boundary is the auth). Money values
// are decimal strings end to end (ADR-0005); every mutating call
// carries X-Mal-Actor so the wallet's Journal names the acting
// Employee.
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
	"net/url"
	"strconv"
	"strings"
)

// errWalletDown marks an unreachable or slow wallet; Employees get one
// honest sentence, operators get the cause in the log.
var errWalletDown = errors.New("wallet rpc unreachable")

// walletUpstreamError is one wallet error answer: its HTTP status and
// the message from the {"error": …} body (or plain text).
type walletUpstreamError struct {
	Status  int
	Message string
}

func (e *walletUpstreamError) Error() string {
	return fmt.Sprintf("wallet %d: %s", e.Status, e.Message)
}

// walletClient is the typed client for the admin seams of the wallet's
// internal RPC (withdrawal audit queue + manual balance adjustment).
type walletClient struct {
	base string
	http *http.Client
	log  *slog.Logger
}

func newWalletClient(env *AdminEnv, log *slog.Logger) *walletClient {
	if log == nil {
		log = slog.Default()
	}
	return &walletClient{
		base: strings.TrimRight(env.WalletBaseURL, "/"),
		http: &http.Client{Timeout: env.WalletTimeout},
		log:  log,
	}
}

// call performs one JSON call, sending X-Mal-Actor when actor is
// non-empty. Non-2xx answers come back as *walletUpstreamError,
// transport failures as errWalletDown.
func (c *walletClient) call(ctx context.Context, method, path, actor string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return errWalletDown
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if actor != "" {
		req.Header.Set("X-Mal-Actor", actor)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.log.Error("admin: wallet rpc failed", "path", path, "err", err)
		return errWalletDown
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return errWalletDown
	}
	if resp.StatusCode >= 400 {
		msg := strings.TrimSpace(string(raw))
		var env struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &env) == nil && env.Error != "" {
			msg = env.Error
		}
		return &walletUpstreamError{Status: resp.StatusCode, Message: msg}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// walletWithdrawal mirrors the wallet's WithdrawalView wire shape.
type walletWithdrawal struct {
	ID        string `json:"id"`
	Coin      string `json:"coin"`
	Member    string `json:"member"`
	Address   string `json:"address"`
	Amount    string `json:"amount"`
	Fee       string `json:"fee"`
	Net       string `json:"net"`
	State     string `json:"state"`
	TxID      string `json:"txid"`
	Error     string `json:"error"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// walletAccount is one Ledger row (the wallet's accounts RPC shape).
type walletAccount struct {
	Coin      string `json:"coin"`
	Available string `json:"available"`
	Frozen    string `json:"frozen"`
}

func (c *walletClient) withdrawals(ctx context.Context, member, state string, limit int) ([]walletWithdrawal, error) {
	q := url.Values{}
	if member != "" {
		q.Set("member", member)
	}
	if state != "" {
		q.Set("state", state)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var out []walletWithdrawal
	if err := c.call(ctx, http.MethodGet, "/withdrawals?"+q.Encode(), "", nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []walletWithdrawal{}
	}
	return out, nil
}

func (c *walletClient) withdrawal(ctx context.Context, id string) (walletWithdrawal, error) {
	var out walletWithdrawal
	err := c.call(ctx, http.MethodGet, "/withdrawals/"+id, "", nil, &out)
	return out, err
}

func (c *walletClient) approveWithdrawal(ctx context.Context, id, actor string) error {
	return c.call(ctx, http.MethodPost, "/withdrawals/"+id+"/approve", actor, map[string]string{}, nil)
}

func (c *walletClient) rejectWithdrawal(ctx context.Context, id, actor string) error {
	return c.call(ctx, http.MethodPost, "/withdrawals/"+id+"/reject", actor, map[string]string{}, nil)
}

func (c *walletClient) retryWithdrawal(ctx context.Context, id, actor string) error {
	return c.call(ctx, http.MethodPost, "/withdrawals/"+id+"/retry", actor, map[string]string{}, nil)
}

func (c *walletClient) refundWithdrawal(ctx context.Context, id, actor string) error {
	return c.call(ctx, http.MethodPost, "/withdrawals/"+id+"/refund", actor, map[string]string{}, nil)
}

func (c *walletClient) accounts(ctx context.Context, member string) ([]walletAccount, error) {
	var out []walletAccount
	if err := c.call(ctx, http.MethodGet, "/accounts/"+member, "", nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []walletAccount{}
	}
	return out, nil
}

// adjustBalance posts one manual movement; applied is false when the
// idempotency key was already applied (the replay moved nothing).
func (c *walletClient) adjustBalance(ctx context.Context, member, coin, amount, reason, key, actor string) (applied bool, err error) {
	var out struct {
		OK      bool `json:"ok"`
		Applied bool `json:"applied"`
	}
	in := map[string]string{
		"member": member, "coin": coin, "amount": amount,
		"reason": reason, "key": key,
	}
	if err := c.call(ctx, http.MethodPost, "/adjustments", actor, in, &out); err != nil {
		return false, err
	}
	return out.Applied, nil
}
