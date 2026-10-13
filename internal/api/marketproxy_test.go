package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// Unit seams of the public market-data proxy (ticket #43): request-line
// passthrough, verbatim success bodies, the public error envelope, the
// bounded 503 on a down or slow market, and the env group. The
// golden-compare against the real market RPC lives in
// marketproxy_it_test.go; these stub the upstream.

// stubAnswer is one canned upstream response, keyed by request URI.
type stubAnswer struct {
	status      int
	contentType string
	body        string
}

// stubUpstream serves canned answers; unset URIs fail the test loudly
// so a route drift cannot pass silently.
func stubUpstream(t *testing.T, answers map[string]stubAnswer) *httptest.Server {
	t.Helper()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ans, ok := answers[r.URL.RequestURI()]
		if !ok {
			t.Errorf("stub upstream: no fixture for %s", r.URL.RequestURI())
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if ans.contentType != "" {
			w.Header().Set("Content-Type", ans.contentType)
		}
		w.WriteHeader(ans.status)
		_, _ = w.Write([]byte(ans.body))
	}))
	t.Cleanup(up.Close)
	return up
}

// mountProxy mounts the registrar on a fresh public mux, the way the
// api boot wires it, and serves it.
func mountProxy(t *testing.T, cfg MarketProxyConfig) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	RegisterMarketProxy(mux, cfg)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// proxyGet fetches one URL through a client with its own budget, so a
// hung proxy fails the test instead of hanging it.
func proxyGet(t *testing.T, url string) (status int, contentType string, body string) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	res, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("GET %s: read body: %v", url, err)
	}
	return res.StatusCode, res.Header.Get("Content-Type"), string(b)
}

// TestMarketProxyForwardsRequestLineVerbatim pins param passthrough
// (ticket #43): the public /api/market prefix is the only thing the
// proxy strips — method, path, and the raw query reach market's
// internal surface untouched, unknown parameters included.
func TestMarketProxyForwardsRequestLineVerbatim(t *testing.T) {
	var mu sync.Mutex
	var got []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]\n"))
	}))
	t.Cleanup(up.Close)

	srv := mountProxy(t, MarketProxyConfig{MarketBaseURL: up.URL + "/internal", Timeout: 2 * time.Second})

	paths := []string{
		"/api/market/markets",
		"/api/market/trades/BTC/USDT?limit=7",
		"/api/market/trades/BTC%2FUSDT?limit=1000&unknownParam=rides-along",
		"/api/market/depth/BTC/USDT",
		"/api/market/kline/ETH/USDT?interval=1h&limit=10",
		"/api/market/ticker/BTC/USDT",
		"/api/market/tickers",
	}
	for _, p := range paths {
		if status, _, _ := proxyGet(t, srv.URL+p); status != http.StatusOK {
			t.Fatalf("GET %s: status = %d, want 200", p, status)
		}
	}

	want := []string{
		"GET /internal/markets?",
		"GET /internal/trades/BTC/USDT?limit=7",
		"GET /internal/trades/BTC/USDT?limit=1000&unknownParam=rides-along",
		"GET /internal/depth/BTC/USDT?",
		"GET /internal/kline/ETH/USDT?interval=1h&limit=10",
		"GET /internal/ticker/BTC/USDT?",
		"GET /internal/tickers?",
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != len(want) {
		t.Fatalf("upstream saw %d requests, want %d: %q", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("upstream request %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestMarketProxyPassesSuccessBodiesThroughVerbatim(t *testing.T) {
	answers := map[string]stubAnswer{
		"/internal/markets": {
			status:      http.StatusOK,
			contentType: "application/json",
			body: `{"market":"BTC/USDT","baseCoin":"BTC","quoteCoin":"USDT","zone":"main","sort":1,` +
				`"featured":true,"baseScale":8,"quoteScale":2,"tickSize":"0.10","lotSize":"0.0001"}` + "\n",
		},
		"/internal/trades/BTC/USDT": {
			status:      http.StatusOK,
			contentType: "application/json",
			body: `[{"id":"fill-0001","price":"50000.10","amount":"0.0002","turnover":"10.00002",` +
				`"takerSide":"buy","time":"2026-10-12T09:00:00Z"}]` + "\n",
		},
		"/internal/depth/BTC/USDT": {
			status:      http.StatusOK,
			contentType: "application/json",
			body: `{"market":"BTC/USDT","bids":[{"price":"50000.10","amount":"0.0002"}],` +
				`"asks":[]}` + "\n",
		},
		"/internal/kline/BTC/USDT?interval=1m": {
			status:      http.StatusOK,
			contentType: "application/json",
			body: `[{"interval":"1m","openTime":"2026-10-12T09:00:00Z","open":"50000.10",` +
				`"high":"50010.20","low":"49999.00","close":"50005.00","amount":"1.2345",` +
				`"turnover":"61728.39","fillCount":7}]` + "\n",
		},
		"/internal/ticker/BTC/USDT": {
			status:      http.StatusOK,
			contentType: "application/json",
			body: `{"market":"BTC/USDT","last":"50000.10","high":"50100.00","low":"49900.00",` +
				`"turnover":"12345.67"}` + "\n",
		},
		"/internal/tickers": {
			status:      http.StatusOK,
			contentType: "application/json",
			body:        `{"BTC/USDT":{"market":"BTC/USDT","last":"","high":"","low":"","turnover":"0"}}` + "\n",
		},
	}
	up := stubUpstream(t, answers)
	srv := mountProxy(t, MarketProxyConfig{MarketBaseURL: up.URL + "/internal", Timeout: 2 * time.Second})

	for _, publicPath := range []string{
		"/api/market/markets",
		"/api/market/trades/BTC/USDT",
		"/api/market/depth/BTC/USDT",
		"/api/market/kline/BTC/USDT?interval=1m",
		"/api/market/ticker/BTC/USDT",
		"/api/market/tickers",
	} {
		ans, ok := answers[internalPath(t, publicPath)]
		if !ok {
			t.Fatalf("fixture missing for %s", publicPath)
		}
		status, contentType, body := proxyGet(t, srv.URL+publicPath)
		if status != ans.status {
			t.Errorf("GET %s: status = %d, want %d", publicPath, status, ans.status)
		}
		if contentType != ans.contentType {
			t.Errorf("GET %s: content-type = %q, want %q", publicPath, contentType, ans.contentType)
		}
		if body != ans.body {
			t.Errorf("GET %s: body = %q, want %q", publicPath, body, ans.body)
		}
	}
}

// TestMarketProxyRewritesNamedErrorsToPublicEnvelope pins the error
// contract (ticket #43): market's named errors keep their status and
// message verbatim, but the body is re-emitted in the api's public
// envelope {"message": …} — the internal {"error": …} shape never
// crosses to the public surface.
func TestMarketProxyRewritesNamedErrorsToPublicEnvelope(t *testing.T) {
	cases := []struct {
		name         string
		path         string
		status       int
		upstreamBody string
		wantMessage  string
	}{
		{
			name:         "unknown interval keeps its name",
			path:         "/api/market/kline/BTC/USDT",
			status:       http.StatusBadRequest,
			upstreamBody: `{"error":"unknown interval (must be one of 1m, 5m, 15m, 30m, 1h, 4h, 1d, 1w)"}` + "\n",
			wantMessage:  "unknown interval (must be one of 1m, 5m, 15m, 30m, 1h, 4h, 1d, 1w)",
		},
		{
			name:         "bad limit keeps its name",
			path:         "/api/market/trades/BTC/USDT?limit=0",
			status:       http.StatusBadRequest,
			upstreamBody: `{"error":"limit must be an integer between 1 and 1000"}` + "\n",
			wantMessage:  "limit must be an integer between 1 and 1000",
		},
		{
			name:         "unexpected upstream failure keeps its status",
			path:         "/api/market/markets",
			status:       http.StatusInternalServerError,
			upstreamBody: `{"error":"connection reset by peer"}` + "\n",
			wantMessage:  "connection reset by peer",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			answers := map[string]stubAnswer{internalPath(t, tc.path): {
				status: tc.status, body: tc.upstreamBody,
			}}
			up := stubUpstream(t, answers)
			srv := mountProxy(t, MarketProxyConfig{MarketBaseURL: up.URL + "/internal", Timeout: 2 * time.Second})

			status, contentType, body := proxyGet(t, srv.URL+tc.path)
			if status != tc.status {
				t.Fatalf("status = %d, want %d", status, tc.status)
			}
			if contentType != "application/json" {
				t.Errorf("content-type = %q, want application/json", contentType)
			}
			wantBody := `{"message":"` + tc.wantMessage + `"}` + "\n"
			if body != wantBody {
				t.Errorf("body = %q, want %q", body, wantBody)
			}
		})
	}
}

// internalPath derives the fixture key — the upstream request URI,
// /internal plus the public path minus /api/market — so the assertion
// and the stub share one source.
func internalPath(t *testing.T, public string) string {
	t.Helper()
	const prefix = "/api/market"
	if len(public) < len(prefix) || public[:len(prefix)] != prefix {
		t.Fatalf("public path %q lacks the %s prefix", public, prefix)
	}
	return "/internal" + public[len(prefix):]
}

// TestMarketProxyDownstreamDownAnswers503: a market that cannot be
// reached answers 503 with the public envelope — an explicit
// unavailable, never a hang or a masked 500.
func TestMarketProxyDownstreamDownAnswers503(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close() // the port is gone: every dial fails

	srv := mountProxy(t, MarketProxyConfig{MarketBaseURL: deadURL + "/internal", Timeout: 500 * time.Millisecond})

	status, contentType, body := proxyGet(t, srv.URL+"/api/market/markets")
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", status)
	}
	if contentType != "application/json" {
		t.Errorf("content-type = %q, want application/json", contentType)
	}
	wantBody := `{"message":"market data is temporarily unavailable"}` + "\n"
	if body != wantBody {
		t.Errorf("body = %q, want %q", body, wantBody)
	}
}

// TestMarketProxySlowDownstreamIsBoundedNotHung: a market that accepts
// the connection but never answers is cut off at the configured budget
// and answered 503 — the caller is never left waiting on the upstream.
func TestMarketProxySlowDownstreamIsBoundedNotHung(t *testing.T) {
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	t.Cleanup(up.Close)                  // registered first → runs last:
	t.Cleanup(func() { close(release) }) // Close waits for the handler, so release first

	srv := mountProxy(t, MarketProxyConfig{MarketBaseURL: up.URL + "/internal", Timeout: 60 * time.Millisecond})

	start := time.Now()
	status, _, body := proxyGet(t, srv.URL+"/api/market/tickers")
	elapsed := time.Since(start)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", status)
	}
	wantBody := `{"message":"market data is temporarily unavailable"}` + "\n"
	if body != wantBody {
		t.Errorf("body = %q, want %q", body, wantBody)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("slow upstream answered after %s; the 60ms budget was not enforced", elapsed)
	}
}

// TestMarketProxyDoesNotShadowSiblingRoutes: the registrar mounts exact
// public paths only — a sibling member route on the same mux keeps
// answering, and an unknown /api path stays the mux's own 404.
func TestMarketProxyDoesNotShadowSiblingRoutes(t *testing.T) {
	up := stubUpstream(t, map[string]stubAnswer{
		"/internal/markets": {status: http.StatusOK, contentType: "application/json", body: "[]\n"},
	})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/uc/me", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"email":"member@example.com"}` + "\n"))
	})
	RegisterMarketProxy(mux, MarketProxyConfig{MarketBaseURL: up.URL + "/internal", Timeout: 2 * time.Second})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	if status, _, body := proxyGet(t, srv.URL+"/api/uc/me"); status != http.StatusOK ||
		body != `{"email":"member@example.com"}`+"\n" {
		t.Errorf("sibling route /api/uc/me: status = %d, body = %q", status, body)
	}
	if status, _, _ := proxyGet(t, srv.URL+"/api/nosuchroute"); status != http.StatusNotFound {
		t.Errorf("unknown /api path: status = %d, want 404", status)
	}
}

// TestReadMarketProxyEnv pins the env group: compose-shaped defaults,
// overrides, and the fallback for a garbage or non-positive timeout.
func TestReadMarketProxyEnv(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		t.Setenv("MAL_API_MARKET_BASE_URL", "")
		t.Setenv("MAL_API_MARKET_TIMEOUT", "")
		cfg := readMarketProxyEnv()
		if cfg.MarketBaseURL != defaultMarketBaseURL {
			t.Errorf("MarketBaseURL = %q, want %q", cfg.MarketBaseURL, defaultMarketBaseURL)
		}
		if cfg.Timeout != defaultMarketProxyTimeout {
			t.Errorf("Timeout = %s, want %s", cfg.Timeout, defaultMarketProxyTimeout)
		}
	})
	t.Run("overrides", func(t *testing.T) {
		t.Setenv("MAL_API_MARKET_BASE_URL", "http://elsewhere:9999/internal")
		t.Setenv("MAL_API_MARKET_TIMEOUT", "250ms")
		cfg := readMarketProxyEnv()
		if cfg.MarketBaseURL != "http://elsewhere:9999/internal" {
			t.Errorf("MarketBaseURL = %q, want the override", cfg.MarketBaseURL)
		}
		if cfg.Timeout != 250*time.Millisecond {
			t.Errorf("Timeout = %s, want 250ms", cfg.Timeout)
		}
	})
	t.Run("garbage timeout falls back to the default", func(t *testing.T) {
		t.Setenv("MAL_API_MARKET_TIMEOUT", "not-a-duration")
		if cfg := readMarketProxyEnv(); cfg.Timeout != defaultMarketProxyTimeout {
			t.Errorf("Timeout = %s, want the default %s", cfg.Timeout, defaultMarketProxyTimeout)
		}
	})
	t.Run("non-positive timeout falls back to the default", func(t *testing.T) {
		t.Setenv("MAL_API_MARKET_TIMEOUT", "0s")
		if cfg := readMarketProxyEnv(); cfg.Timeout != defaultMarketProxyTimeout {
			t.Errorf("Timeout = %s, want the default %s", cfg.Timeout, defaultMarketProxyTimeout)
		}
	})
}
