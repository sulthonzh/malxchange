package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The wallet upstream knobs (ticket #60): the base URL carries the
// /internal suffix by default (the boot harness serves wallet's RPC
// mux there — api's funds default without the suffix is the trap this
// default avoids), and a garbage timeout falls back to 5s instead of
// bricking the surface.
func TestLoadAdminEnvWalletUpstream(t *testing.T) {
	env := loadAdminEnv() // pure reader: no env vars set in the test process
	if env.WalletBaseURL != "http://wallet:8080/internal" {
		t.Fatalf("WalletBaseURL default = %q, want http://wallet:8080/internal", env.WalletBaseURL)
	}
	if env.WalletTimeout != 5*time.Second {
		t.Fatalf("WalletTimeout default = %v, want 5s", env.WalletTimeout)
	}

	t.Setenv("MAL_ADMIN_WALLET_BASE_URL", "http://127.0.0.1:1/internal/")
	t.Setenv("MAL_ADMIN_WALLET_TIMEOUT", "750ms")
	env = loadAdminEnv()
	if env.WalletBaseURL != "http://127.0.0.1:1/internal/" {
		t.Fatalf("WalletBaseURL override = %q, want the env value", env.WalletBaseURL)
	}
	if env.WalletTimeout != 750*time.Millisecond {
		t.Fatalf("WalletTimeout override = %v, want 750ms", env.WalletTimeout)
	}

	t.Setenv("MAL_ADMIN_WALLET_TIMEOUT", "not-a-duration")
	if env := loadAdminEnv(); env.WalletTimeout != 5*time.Second {
		t.Fatalf("garbage WalletTimeout = %v, want the 5s default", env.WalletTimeout)
	}
}

// writeWalletErr is the mapping contract: the wallet's own 4xx
// refusals pass through with their message (they are honest sentences
// the UI renders), a wallet-side breakdown is one 502, and an
// unreachable wallet is one 503 — never the raw upstream text.
func TestWriteWalletErrMapping(t *testing.T) {
	s := &Server{} // log nil on purpose: the mapper must guard it

	pass := func(err error) (int, string) {
		t.Helper()
		w := httptest.NewRecorder()
		s.writeWalletErr(w, err)
		var body struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		return w.Code, body.Message
	}

	if status, msg := pass(&walletUpstreamError{Status: http.StatusBadRequest, Message: "BTC has no cold address"}); status != http.StatusBadRequest || msg != "BTC has no cold address" {
		t.Fatalf("400 mapping = %d %q", status, msg)
	}
	if status, _ := pass(&walletUpstreamError{Status: http.StatusNotFound, Message: "no such coin"}); status != http.StatusNotFound {
		t.Fatalf("404 mapping = %d, want 404", status)
	}
	if status, _ := pass(&walletUpstreamError{Status: http.StatusConflict, Message: "illegal state"}); status != http.StatusConflict {
		t.Fatalf("409 mapping = %d, want 409", status)
	}
	status, msg := pass(&walletUpstreamError{Status: http.StatusInternalServerError, Message: "pq: boom"})
	if status != http.StatusBadGateway || msg == "pq: boom" {
		t.Fatalf("500 mapping = %d %q, want 502 without leaking the upstream text", status, msg)
	}
	if status, _ := pass(errWalletDown); status != http.StatusServiceUnavailable {
		t.Fatalf("transport mapping = %d, want 503", status)
	}
	if status, _ := pass(errors.New("context canceled")); status != http.StatusServiceUnavailable {
		t.Fatalf("unknown-error mapping = %d, want 503", status)
	}
}

// listPath encodes only the non-empty filters, in a stable shape the
// wallet list RPCs parse (the coin/state/limit allowlist).
func TestListPathFilters(t *testing.T) {
	if got := listPath("/sweeps", map[string]string{"coin": "", "state": "", "limit": ""}); got != "/sweeps" {
		t.Fatalf("empty filters = %q, want the bare path", got)
	}
	if got := listPath("/sweeps", map[string]string{"coin": "BTC", "state": "failed", "limit": ""}); got != "/sweeps?coin=BTC&state=failed" {
		t.Fatalf("filters = %q, want /sweeps?coin=BTC&state=failed", got)
	}
}
