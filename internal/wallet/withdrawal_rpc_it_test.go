//go:build integration

// The admin-facing withdrawal seams over real HTTP (spec #59): the
// single-withdrawal detail RPC the audit queue drills through, and the
// manual balance adjustment with caller-owned idempotency — a retried
// POST with the same key must move nothing. The production mux wiring
// (root → /internal/ → StripPrefix → RegisterRPC) serves both, and
// assertions read the Ledger through the accounts RPC, not raw tables.
package wallet

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/syaikhipin/malxchange/internal/money"
)

// serveAdminRPC mounts RegisterRPC behind the boot harness's
// StripPrefix and returns the base URL + the mux server.
func serveAdminRPC(t *testing.T, s *itStack) *httptest.Server {
	t.Helper()
	internal := http.NewServeMux()
	RegisterRPC(internal, &RPC{DB: s.pool, Log: s.log})
	root := http.NewServeMux()
	root.Handle("/internal/", http.StripPrefix("/internal", internal))
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)
	return srv
}

func rpcCall(t *testing.T, srv *httptest.Server, method, path, body, actor string) (int, map[string]any) {
	t.Helper()
	status, raw := rawRPCCall(t, srv, method, path, body, actor)
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("%s %s: undecodable body %q: %v", method, path, raw, err)
	}
	return status, out
}

func rawRPCCall(t *testing.T, srv *httptest.Server, method, path, body, actor string) (int, string) {
	t.Helper()
	var rdr *bytes.Reader
	if body == "" {
		rdr = bytes.NewReader(nil)
	} else {
		rdr = bytes.NewReader([]byte(body))
	}
	req, err := http.NewRequest(method, srv.URL+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if actor != "" {
		req.Header.Set("X-Mal-Actor", actor)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(raw)
}

func TestWithdrawalDetailRPC(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	srv := serveAdminRPC(t, s)

	if err := Credit(ctx, s.pool, "greta", "ETH", KindAdjustment,
		"test faucet", "it-faucet:greta", money.MustParse("10"), "", "test"); err != nil {
		t.Fatal(err)
	}
	coin, err := GetCoin(ctx, s.pool, "ETH")
	if err != nil {
		t.Fatal(err)
	}
	w, err := RequestWithdrawal(ctx, s.pool, coin, "greta", "0xdEaD", money.MustParse("2"))
	if err != nil {
		t.Fatal(err)
	}

	status, body := rpcCall(t, srv, "GET", "/internal/withdrawals/"+w.ID, "", "")
	if status != http.StatusOK {
		t.Fatalf("detail = %d %v, want 200", status, body)
	}
	for key, want := range map[string]any{
		"id": w.ID, "coin": "ETH", "member": "greta", "address": "0xdEaD",
		"amount": "2", "fee": "0.004", "net": "1.996", "state": WAudit,
	} {
		if body[key] != want {
			t.Errorf("detail[%q] = %v, want %v", key, body[key], want)
		}
	}
	for _, key := range []string{"createdAt", "updatedAt"} {
		if s, _ := body[key].(string); s == "" {
			t.Errorf("detail[%q] missing", key)
		}
	}
	if _, ok := body["txid"]; ok {
		t.Errorf("detail carries empty txid: %v", body)
	}

	status, body = rpcCall(t, srv, "GET", "/internal/withdrawals/not-a-uuid", "", "")
	if status != http.StatusNotFound {
		t.Fatalf("unknown id = %d %v, want 404", status, body)
	}
}

func TestAdjustmentIdempotency(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	srv := serveAdminRPC(t, s)

	if err := Credit(ctx, s.pool, "hugo", "USDT", KindAdjustment,
		"test faucet", "it-faucet:hugo", money.MustParse("100"), "", "test"); err != nil {
		t.Fatal(err)
	}
	balance := func() string {
		avail, _ := s.balance(t, "hugo", "USDT")
		return avail
	}

	adj := func(body string) (int, map[string]any) {
		return rpcCall(t, srv, "POST", "/internal/adjustments", body, "fin@it.test")
	}

	status, body := adj(`{"member":"hugo","coin":"USDT","amount":"-2.5","reason":"support case #4182","key":"case-4182"}`)
	if status != http.StatusOK || body["applied"] != true {
		t.Fatalf("first apply = %d %v, want 200 applied", status, body)
	}
	s.mustEqual(t, "balance after -2.5", balance(), "97.5")

	// The same key again: a no-op, not a second movement.
	status, body = adj(`{"member":"hugo","coin":"USDT","amount":"-2.5","reason":"support case #4182","key":"case-4182"}`)
	if status != http.StatusOK || body["applied"] != false {
		t.Fatalf("replay = %d %v, want 200 not applied", status, body)
	}
	s.mustEqual(t, "balance after replay", balance(), "97.5")

	// The journal carries the reason verbatim (prefixed by the kind)
	// and the acting Employee.
	var reason, actor string
	if err := s.pool.QueryRow(ctx,
		`SELECT reason, COALESCE(actor,'') FROM journal WHERE idem_key = $1`,
		"adjust:case-4182").Scan(&reason, &actor); err != nil {
		t.Fatal(err)
	}
	if reason != "adjustment: support case #4182" || actor != "fin@it.test" {
		t.Fatalf("journal reason=%q actor=%q", reason, actor)
	}

	// A different key IS a different adjustment, even with the same reason.
	status, body = adj(`{"member":"hugo","coin":"USDT","amount":"1","reason":"support case #4182","key":"case-4182-followup"}`)
	if status != http.StatusOK || body["applied"] != true {
		t.Fatalf("second key = %d %v, want 200 applied", status, body)
	}
	s.mustEqual(t, "balance after second key", balance(), "98.5")

	// Refusals: missing key, zero amount (plain-text 400 answers).
	if status, _ = rawRPCCall(t, srv, "POST", "/internal/adjustments",
		`{"member":"hugo","coin":"USDT","amount":"1","reason":"r"}`, "fin@it.test"); status != http.StatusBadRequest {
		t.Fatalf("missing key = %d, want 400", status)
	}
	if status, _ = rawRPCCall(t, srv, "POST", "/internal/adjustments",
		`{"member":"hugo","coin":"USDT","amount":"0","reason":"r","key":"k1"}`, "fin@it.test"); status != http.StatusBadRequest {
		t.Fatalf("zero amount = %d, want 400", status)
	}

	// A negative adjustment past zero is refused without movement.
	if status, _ = rawRPCCall(t, srv, "POST", "/internal/adjustments",
		`{"member":"hugo","coin":"USDT","amount":"-1000","reason":"oops","key":"k2"}`, "fin@it.test"); status != http.StatusConflict {
		t.Fatalf("overdraw = %d, want 409", status)
	}
	s.mustEqual(t, "balance after refused overdraw", balance(), "98.5")

	folds, err := VerifyFold(ctx, s.pool, "USDT")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range folds {
		if !f.OK {
			t.Fatalf("fold broken for %s", f.Coin)
		}
	}
}
