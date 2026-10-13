package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/syaikhipin/malxchange/internal/config"
)

// MarketProxyConfig configures the anonymous market-data proxy
// (ticket #43): where market's internal REST surface lives and how long
// one proxied request may take.
type MarketProxyConfig struct {
	// MarketBaseURL is the base URL of market's internal REST surface —
	// scheme, host, and the /internal prefix the boot harness strips
	// onto market's mux (compose default http://market:8080/internal).
	// Public paths map onto it one to one: /api/market/markets →
	// <MarketBaseURL>/markets.
	MarketBaseURL string
	// Timeout bounds one proxied request end to end — connect, headers,
	// and body. A market that is down, unreachable, or slow answers 503,
	// never a hanging connection. Non-positive falls back to the default.
	Timeout time.Duration
	// Log receives one warn line per failed upstream request (nil = the
	// default logger). No retries: a down market surfaces as 503s.
	Log *slog.Logger
}

const (
	defaultMarketBaseURL      = "http://market:8080/internal"
	defaultMarketProxyTimeout = 5 * time.Second
)

// errMarketDown is the public 503 the proxy answers when market's
// internal REST cannot be reached within budget — readiness probes and
// the front see an explicit unavailable, not a hang or a masked 500.
var errMarketDown = errors.New("market data is temporarily unavailable")

var (
	marketProxyEnvOnce sync.Once
	marketProxyEnvVal  MarketProxyConfig
)

// LoadMarketProxyConfig reads the proxy's MAL_API_MARKET_* environment
// once at boot (idempotent; the LoadAPIEnv pattern).
func LoadMarketProxyConfig() MarketProxyConfig {
	marketProxyEnvOnce.Do(func() { marketProxyEnvVal = readMarketProxyEnv() })
	return marketProxyEnvVal
}

// readMarketProxyEnv is the pure reader behind LoadMarketProxyConfig.
// An unparsable or non-positive MAL_API_MARKET_TIMEOUT falls back to
// the default (the proxy never boots broken over a typo).
func readMarketProxyEnv() MarketProxyConfig {
	return MarketProxyConfig{
		MarketBaseURL: config.EnvDefault("MAL_API_MARKET_BASE_URL", defaultMarketBaseURL),
		Timeout:       envDuration("MAL_API_MARKET_TIMEOUT", defaultMarketProxyTimeout),
	}
}

// RegisterMarketProxy mounts the public market-data routes on the api's
// public mux (ticket #43; spec #24 story 19 — the exchange is explorable
// before signup, so these carry no session and no auth). Every route
// proxies one of market's internal REST endpoints one to one: the
// public /api/market segment maps onto market's own path, the raw query
// rides along untouched, and the JSON body streams back verbatim so the
// public wire cannot drift from the internal one (golden-compared by
// the integration test). The /api/market namespace is the prefix the
// web scaffold's market seams already speak (apps/web/src/pages/
// index.vue: MARKETS_PATH '/market/markets', TICKERS_PATH
// '/market/tickers' against the api base) and keeps the data routes
// apart from the sibling member surfaces (#37's /api/uc/…, #45's
// /api/announcements…). Market names contain a slash (BTC/USDT), so
// the wildcards are greedy — exactly like market's own patterns.
//
// Answers:
//   - success: market's status, Content-Type, and body bytes as served;
//   - error: market's status and message kept verbatim, re-emitted in
//     the public envelope {"message": …} (the internal {"error": …}
//     shape never crosses to the public surface);
//   - market unreachable or slow past the budget: 503
//     {"message":"market data is temporarily unavailable"}.
func RegisterMarketProxy(mux *http.ServeMux, cfg MarketProxyConfig) {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultMarketProxyTimeout
	}
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	p := &marketProxy{
		base:   strings.TrimSuffix(cfg.MarketBaseURL, "/"),
		client: &http.Client{Timeout: timeout},
		log:    log,
	}
	mux.HandleFunc("GET /api/market/markets", p.proxy)
	mux.HandleFunc("GET /api/market/trades/{market...}", p.proxy)
	mux.HandleFunc("GET /api/market/depth/{market...}", p.proxy)
	mux.HandleFunc("GET /api/market/kline/{market...}", p.proxy)
	mux.HandleFunc("GET /api/market/ticker/{market...}", p.proxy)
	mux.HandleFunc("GET /api/market/tickers", p.proxy)
}

// marketProxy is the mounted proxy: one HTTP client with the bounded
// budget, one upstream base.
type marketProxy struct {
	base   string
	client *http.Client
	log    *slog.Logger
}

// proxy forwards one request to market's internal REST. The path drops
// only the public /api/market prefix; the raw query rides along
// untouched.
func (p *marketProxy) proxy(w http.ResponseWriter, req *http.Request) {
	target := p.base + strings.TrimPrefix(req.URL.Path, "/api/market")
	if req.URL.RawQuery != "" {
		target += "?" + req.URL.RawQuery
	}
	upreq, err := newUpstreamGet(req, target)
	if err != nil {
		p.writeDown(w, err)
		return
	}
	up, err := p.client.Do(upreq)
	if err != nil {
		p.writeDown(w, err)
		return
	}
	defer up.Body.Close()
	body, err := io.ReadAll(up.Body)
	if err != nil {
		p.writeDown(w, err)
		return
	}
	writeUpstream(w, up.StatusCode, up.Header.Get("Content-Type"), body)
}

// newUpstreamGet builds the upstream GET bound to the caller's context,
// so a client disconnect cancels the upstream request too. A target the
// http package cannot even parse (a malformed configured base) is
// unreachability like any other — the error flows to the same bounded
// 503, never a panic.
func newUpstreamGet(req *http.Request, target string) (*http.Request, error) {
	return http.NewRequestWithContext(req.Context(), http.MethodGet, target, nil)
}

// upstreamError is the internal {"error": …} body market's named
// errors arrive in.
type upstreamError struct {
	Error string `json:"error"`
}

// writeUpstream relays one upstream answer. Errors (status >= 400) are
// re-emitted in the shared public envelope (errors.go's WriteError)
// with market's status and message verbatim — same names, public shape
// (the wire contract useApi.ts reads). Anything that is not market's
// error JSON (a non-JSON body from a proxy in between) passes through
// untouched rather than being mangled. Successes stream back exactly
// as served.
func writeUpstream(w http.ResponseWriter, status int, contentType string, body []byte) {
	if status < http.StatusBadRequest {
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(status)
		_, _ = w.Write(body)
		return
	}
	var ue upstreamError
	if err := json.Unmarshal(body, &ue); err != nil || ue.Error == "" {
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(status)
		_, _ = w.Write(body)
		return
	}
	WriteError(w, status, ue.Error)
}

// writeDown answers the 503 for an unreachable or slow market.
func (p *marketProxy) writeDown(w http.ResponseWriter, cause error) {
	p.log.Warn("market data proxy: market unavailable", "base", p.base, "error", cause)
	WriteError(w, http.StatusServiceUnavailable, errMarketDown.Error())
}
