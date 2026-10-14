//go:build integration

// Wallet Ops over the real seam (ticket #60): HTTP against the
// production registry wiring with a wallet-internal-RPC stub standing
// in for the wallet service (recorded calls assert what admin-api
// actually sent — path, body, and the X-Mal-Actor attribution).
// Assertions go through the served surface; the audit_log queries are
// the black-box read of what the middleware must have written. The
// permission matrix refusals for non-matrix Roles grow automatically
// out of TestRoleMatrixRefusesNonMatrixRoles — the journeys here are
// the positive finance paths plus the explicit support-side refusal.
package admin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- the wallet stub: one handler recording every proxied call ----

type stubCall struct {
	Method, Path, Query, Actor, Body string
}

type walletStub struct {
	mu    sync.Mutex
	calls []stubCall
	srv   *httptest.Server
}

func (ws *walletStub) saw(t *testing.T, method, path string) stubCall {
	t.Helper()
	ws.mu.Lock()
	defer ws.mu.Unlock()
	for _, c := range ws.calls {
		if c.Method == method && c.Path == path {
			return c
		}
	}
	t.Fatalf("wallet stub never saw %s %s; calls: %v", method, path, ws.calls)
	return stubCall{}
}

func (ws *walletStub) callsTo(path string) int {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	n := 0
	for _, c := range ws.calls {
		if c.Path == path {
			n++
		}
	}
	return n
}

func newWalletStub(t *testing.T) *walletStub {
	t.Helper()
	ws := &walletStub{}
	mux := http.NewServeMux()
	mux.HandleFunc("/internal/", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		ws.mu.Lock()
		ws.calls = append(ws.calls, stubCall{
			Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery,
			Actor: r.Header.Get("X-Mal-Actor"), Body: string(raw),
		})
		ws.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		const (
			summary = `{"alerts":[{"coin":"BTC","kind":"hot_ceiling","detail":"hot balance 11 above ceiling 10 — move excess to cold"}],` +
				`"hotBalances":{"BTC":"11"},"failed":{"withdrawals":1,"sweeps":0,"hotTransfers":2},` +
				`"openIncidents":3,"sweepsPaused":["LTC"],"foldOK":true}`
			reconcile = `{"ok":true,` +
				`"fold":[{"coin":"BTC","ledgerAvail":"9","ledgerFrozen":"1","journalAvail":"9","journalFrozen":"1","ok":true}],` +
				`"coins":[{"coin":"BTC","ledgerSum":"10","feesCollected":"0.0004","coldOut":"0","coldIn":"0","inFlight":"0",` +
				`"expectedHot":"10.0004","chainHot":"10.0004","unswept":"0","delta":"0","ok":true}]}`
			incidents = `[{"id":"inc-1","coin":"BTC","kind":"reorg","detail":"depth 5 > window 3","status":"open",` +
				`"createdAt":"2026-01-02 03:04:05+00"}]`
			sweeps = `[{"id":"sw-1","coin":"BTC","address":"bc1q-example","amount":"0.5","fee":"0.00001","fundingTxid":"",` +
				`"txid":"","state":"failed","error":"broadcast: eof","createdAt":"2026-01-02 03:04:05+00"}]`
			hotTransfers = `[{"id":"ht-1","coin":"BTC","amount":"1.5","address":"cold-btc-custody","state":"failed",` +
				`"txid":"","error":"broadcast: timeout","actor":"ops@it.test","createdAt":"2026-01-02 03:04:05+00"}]`
			coldTransfers = `[{"id":"ct-1","coin":"BTC","amount":"2","txid":"abc123","actor":"ops@it.test",` +
				`"notedAt":"2026-01-02 03:04:05+00"}]`
		)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/internal/ops/summary":
			_, _ = io.WriteString(w, summary)
		case r.Method == http.MethodGet && r.URL.Path == "/internal/reconcile":
			_, _ = io.WriteString(w, reconcile)
		case r.Method == http.MethodGet && r.URL.Path == "/internal/incidents":
			_, _ = io.WriteString(w, incidents)
		case r.Method == http.MethodGet && r.URL.Path == "/internal/sweeps":
			_, _ = io.WriteString(w, sweeps)
		case r.Method == http.MethodGet && r.URL.Path == "/internal/hot-transfers":
			_, _ = io.WriteString(w, hotTransfers)
		case r.Method == http.MethodGet && r.URL.Path == "/internal/cold-transfers":
			_, _ = io.WriteString(w, coldTransfers)
		case r.Method == http.MethodPost && r.URL.Path == "/internal/hot-transfers":
			_, _ = io.WriteString(w, `{"id":"ht-new","coin":"BTC","state":"pending"}`)
		default:
			_, _ = io.WriteString(w, `{"ok":true}`)
		}
	})
	ws.srv = httptest.NewServer(mux)
	t.Cleanup(ws.srv.Close)
	return ws
}

// ---- the ops stack: the ticket's surface over the stub, sessions
// ---- shared with the provisioning stack's DB ----

type opsStack struct {
	st   *itStack
	ws   *walletStub
	ops  *httptest.Server
	root provisioned
}

func newOpsStack(t *testing.T) *opsStack {
	t.Helper()
	o := &opsStack{st: newStack(t), ws: newWalletStub(t)}
	env := &AdminEnv{
		SessionAbsolute:  12 * time.Hour,
		SessionIdle:      30 * time.Minute,
		LockoutThreshold: LockoutThreshold,
		LockoutWindow:    1500 * time.Millisecond,
		WalletBaseURL:    o.ws.srv.URL + "/internal",
		WalletTimeout:    2 * time.Second,
	}
	srv := NewServer(o.st.pool, env, nil)
	root := http.NewServeMux()
	srv.RegisterEndpoints(root)
	o.ops = httptest.NewServer(root)
	t.Cleanup(o.ops.Close)

	o.root = o.st.fullyProvisioned(t, "ops-root@it.test")
	return o
}

func (o *opsStack) do(t *testing.T, method, path, body string, cookie *http.Cookie) (int, string) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, o.ops.URL+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// financeEmployee provisions a finance Employee and returns a live cookie.
func (o *opsStack) financeEmployee(t *testing.T, email string) (*http.Cookie, provisioned) {
	t.Helper()
	rootCookie := o.root.freshCookie(t, o.st)
	p := o.st.staff(t, o.root, rootCookie, email, "finance")
	return p.freshCookie(t, o.st), p
}

func (o *opsStack) supportCookie(t *testing.T, email string) *http.Cookie {
	t.Helper()
	rootCookie := o.root.freshCookie(t, o.st)
	p := o.st.staff(t, o.root, rootCookie, email, "support")
	return p.freshCookie(t, o.st)
}

// ---- the dashboard ----

func TestOpsDashboardSummaryAndReconcile(t *testing.T) {
	o := newOpsStack(t)
	finance, _ := o.financeEmployee(t, "finance-view@it.test")

	// Outside the matrix: support reaches neither the dashboard nor
	// reconcile (the wallet surface is finance/super-admin only).
	support := o.supportCookie(t, "support-ops@it.test")
	if status, _ := o.do(t, http.MethodGet, "/api/ops", "", support); status != http.StatusForbidden {
		t.Fatalf("support GET /api/ops = %d, want 403", status)
	}
	if status, _ := o.do(t, http.MethodGet, "/api/ops/reconcile", "", support); status != http.StatusForbidden {
		t.Fatalf("support GET /api/ops/reconcile = %d, want 403", status)
	}

	// The summary is the dashboard feed, passed through verbatim.
	status, raw := o.do(t, http.MethodGet, "/api/ops", "", finance)
	if status != http.StatusOK {
		t.Fatalf("finance GET /api/ops = %d %s, want 200", status, raw)
	}
	var summary struct {
		Failed struct {
			Withdrawals  int `json:"withdrawals"`
			Sweeps       int `json:"sweeps"`
			HotTransfers int `json:"hotTransfers"`
		} `json:"failed"`
		Alerts []struct {
			Coin   string `json:"coin"`
			Kind   string `json:"kind"`
			Detail string `json:"detail"`
		} `json:"alerts"`
		HotBalances  map[string]string `json:"hotBalances"`
		SweepsPaused []string          `json:"sweepsPaused"`
	}
	if err := json.Unmarshal([]byte(raw), &summary); err != nil {
		t.Fatalf("summary body: %v (%s)", err, raw)
	}
	if summary.Failed.Withdrawals != 1 || summary.Failed.Sweeps != 0 || summary.Failed.HotTransfers != 2 {
		t.Fatalf("failed counts = %+v, want 1/0/2", summary.Failed)
	}
	if len(summary.Alerts) != 1 || summary.Alerts[0].Kind != "hot_ceiling" || summary.Alerts[0].Coin != "BTC" {
		t.Fatalf("alerts = %+v, want one BTC hot_ceiling", summary.Alerts)
	}
	if summary.HotBalances["BTC"] != "11" {
		t.Fatalf("hotBalances = %v, want BTC=11", summary.HotBalances)
	}
	if len(summary.SweepsPaused) != 1 || summary.SweepsPaused[0] != "LTC" {
		t.Fatalf("sweepsPaused = %v, want [LTC]", summary.SweepsPaused)
	}
	o.ws.saw(t, http.MethodGet, "/internal/ops/summary")

	// Reconcile: fold invariant + per-Coin expected-vs-chain figures.
	status, raw = o.do(t, http.MethodGet, "/api/ops/reconcile", "", finance)
	if status != http.StatusOK {
		t.Fatalf("GET /api/ops/reconcile = %d %s, want 200", status, raw)
	}
	var recon struct {
		OK    bool `json:"ok"`
		Coins []struct {
			Coin        string `json:"coin"`
			LedgerSum   string `json:"ledgerSum"`
			ExpectedHot string `json:"expectedHot"`
			ChainHot    string `json:"chainHot"`
			Delta       string `json:"delta"`
		} `json:"coins"`
	}
	if err := json.Unmarshal([]byte(raw), &recon); err != nil {
		t.Fatalf("reconcile body: %v (%s)", err, raw)
	}
	if !recon.OK || len(recon.Coins) != 1 || recon.Coins[0].Delta != "0" {
		t.Fatalf("reconcile = %+v, want ok with one zero-delta Coin", recon)
	}

	// The lists render: transfers, sweeps, incidents — filters ride along.
	if status, raw = o.do(t, http.MethodGet, "/api/incidents?status=open", "", finance); status != http.StatusOK {
		t.Fatalf("GET /api/incidents = %d %s, want 200", status, raw)
	}
	for _, path := range []string{"/api/hot-transfers", "/api/cold-transfers?coin=BTC", "/api/sweeps?state=failed"} {
		if status, raw = o.do(t, http.MethodGet, path, "", finance); status != http.StatusOK {
			t.Fatalf("GET %s = %d %s, want 200", path, status, raw)
		}
	}
	if got := o.ws.saw(t, http.MethodGet, "/internal/incidents").Query; got != "status=open" {
		t.Fatalf("incidents filter query = %q, want status=open", got)
	}
	if got := o.ws.saw(t, http.MethodGet, "/internal/sweeps").Query; got != "state=failed" {
		t.Fatalf("sweeps filter query = %q, want state=failed", got)
	}
	if got := o.ws.saw(t, http.MethodGet, "/internal/cold-transfers").Query; got != "coin=BTC" {
		t.Fatalf("cold-transfers filter query = %q, want coin=BTC", got)
	}

	// Read-only proxying carries no attribution header.
	if got := o.ws.saw(t, http.MethodGet, "/internal/ops/summary").Actor; got != "" {
		t.Fatalf("read-only proxy set X-Mal-Actor %q, want none", got)
	}
}

// ---- hot→cold + cold→hot + retries: Step-up journeys ----

func TestOpsTransfersWithStepUp(t *testing.T) {
	o := newOpsStack(t)
	cookie, p := o.financeEmployee(t, "finance-move@it.test")

	fresh := func() string {
		t.Helper()
		code, err := totpNow(p.TOTPSecr)
		if err != nil {
			t.Fatal(err)
		}
		return code
	}

	// Hot→cold creation demands Step-up: no code, the password in the
	// code field, and a well-formed wrong code are all refused before
	// wallet sees anything.
	body := `{"coin":"BTC","amount":"1.5"}`
	if status, _ := o.do(t, http.MethodPost, "/api/hot-transfers", body, cookie); status != http.StatusUnauthorized {
		t.Fatalf("create without code = %d, want 401", status)
	}
	if status, _ := o.do(t, http.MethodPost, "/api/hot-transfers",
		`{"coin":"BTC","amount":"1.5","code":"rotated-password-2"}`, cookie); status != http.StatusUnauthorized {
		t.Fatalf("create with password as code = %d, want 401", status)
	}
	if status, _ := o.do(t, http.MethodPost, "/api/hot-transfers",
		fmt.Sprintf(`{"coin":"BTC","amount":"1.5","code":%q}`, wrongCodeOf(t, p.TOTPSecr)), cookie); status != http.StatusUnauthorized {
		t.Fatalf("create with wrong code = %d, want 401", status)
	}
	if o.ws.callsTo("/internal/hot-transfers") != 0 {
		t.Fatal("refused creations reached wallet")
	}

	status, raw := o.do(t, http.MethodPost, "/api/hot-transfers",
		fmt.Sprintf(`{"coin":"BTC","amount":"1.5","code":%q}`, fresh()), cookie)
	if status != http.StatusOK {
		t.Fatalf("create with fresh code = %d %s, want 200", status, raw)
	}
	var created struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(raw), &created); err != nil || created.ID != "ht-new" || created.State != "pending" {
		t.Fatalf("created = %s (%v), want ht-new pending", raw, err)
	}
	call := o.ws.saw(t, http.MethodPost, "/internal/hot-transfers")
	if call.Actor != p.Email {
		t.Fatalf("X-Mal-Actor = %q, want %q", call.Actor, p.Email)
	}
	if !strings.Contains(call.Body, `"coin":"BTC"`) || !strings.Contains(call.Body, `"amount":"1.5"`) {
		t.Fatalf("create body = %s, want coin+amount", call.Body)
	}
	rows := o.st.auditRows(t, PermCreateHotTransfer)
	if len(rows) != 1 || rows[0].Target != "ht-new" || rows[0].Actor != p.Email {
		t.Fatalf("audit rows for %s = %+v, want one targeting ht-new", PermCreateHotTransfer, rows)
	}

	// A garbage or non-positive amount is refused after Step-up, with
	// wallet untouched.
	before := o.ws.callsTo("/internal/hot-transfers")
	status, raw = o.do(t, http.MethodPost, "/api/hot-transfers",
		fmt.Sprintf(`{"coin":"BTC","amount":"not-a-number","code":%q}`, fresh()), cookie)
	if status != http.StatusBadRequest || !strings.Contains(raw, "amount") {
		t.Fatalf("garbage amount = %d %s, want 400 naming the amount", status, raw)
	}
	if status, _ = o.do(t, http.MethodPost, "/api/hot-transfers",
		fmt.Sprintf(`{"coin":"BTC","amount":"-1","code":%q}`, fresh()), cookie); status != http.StatusBadRequest {
		t.Fatalf("negative amount = %d, want 400", status)
	}
	if o.ws.callsTo("/internal/hot-transfers") != before {
		t.Fatal("bad amounts reached wallet")
	}

	// Retry of a failed hot transfer demands Step-up and fronts the
	// previously unrouted wallet retry.
	retry := func(code string) (int, string) {
		t.Helper()
		return o.do(t, http.MethodPost, "/api/hot-transfers/ht-1/retry",
			fmt.Sprintf(`{"code":%q}`, code), cookie)
	}
	if status, _ = retry(""); status != http.StatusUnauthorized {
		t.Fatalf("hot-transfer retry without code = %d, want 401", status)
	}
	if status, raw = retry(fresh()); status != http.StatusOK {
		t.Fatalf("hot-transfer retry with code = %d %s, want 200", status, raw)
	}
	o.ws.saw(t, http.MethodPost, "/internal/hot-transfers/ht-1/retry")
	rows = o.st.auditRows(t, PermRetryHotTransfer)
	if len(rows) != 1 || rows[0].Target != "ht-1" {
		t.Fatalf("audit rows for %s = %+v, want one targeting ht-1", PermRetryHotTransfer, rows)
	}

	// Retry of a failed sweep: same contract.
	if status, _ = o.do(t, http.MethodPost, "/api/sweeps/sw-1/retry", `{}`, cookie); status != http.StatusUnauthorized {
		t.Fatalf("sweep retry without code = %d, want 401", status)
	}
	if status, raw = o.do(t, http.MethodPost, "/api/sweeps/sw-1/retry",
		fmt.Sprintf(`{"code":%q}`, fresh()), cookie); status != http.StatusOK {
		t.Fatalf("sweep retry with code = %d %s, want 200", status, raw)
	}
	o.ws.saw(t, http.MethodPost, "/internal/sweeps/sw-1/retry")
	rows = o.st.auditRows(t, PermRetrySweep)
	if len(rows) != 1 || rows[0].Target != "sw-1" {
		t.Fatalf("audit rows for %s = %+v, want one targeting sw-1", PermRetrySweep, rows)
	}

	// Cold→hot recording: the offline procedure's paper trail — txid
	// mandatory, no Step-up in the ADR-0012 list, still audited.
	if status, raw = o.do(t, http.MethodPost, "/api/cold-transfers",
		`{"coin":"BTC","amount":"2"}`, cookie); status != http.StatusBadRequest {
		t.Fatalf("cold record without txid = %d %s, want 400", status, raw)
	}
	if o.ws.callsTo("/internal/cold-transfers") != 0 {
		t.Fatal("txid-less record reached wallet")
	}
	if status, raw = o.do(t, http.MethodPost, "/api/cold-transfers",
		`{"coin":"BTC","amount":"2","txid":"abc123"}`, cookie); status != http.StatusOK {
		t.Fatalf("cold record = %d %s, want 200", status, raw)
	}
	call = o.ws.saw(t, http.MethodPost, "/internal/cold-transfers")
	if call.Actor != p.Email || !strings.Contains(call.Body, `"txid":"abc123"`) {
		t.Fatalf("cold record call = %+v, want actor + txid", call)
	}
	rows = o.st.auditRows(t, PermRecordColdTransfer)
	if len(rows) != 1 || rows[0].Target != "abc123" {
		t.Fatalf("audit rows for %s = %+v, want one targeting the txid", PermRecordColdTransfer, rows)
	}
}

// ---- incident resolve + sweep pause: audited, no Step-up ----

func TestOpsIncidentsResolveAndSweepPauseAudited(t *testing.T) {
	o := newOpsStack(t)
	cookie, p := o.financeEmployee(t, "finance-incident@it.test")

	// Resolve carries no Step-up (not in ADR-0012's list) but is
	// audit-logged with the incident as target.
	status, raw := o.do(t, http.MethodPost, "/api/incidents/inc-1/resolve", `{}`, cookie)
	if status != http.StatusOK {
		t.Fatalf("resolve = %d %s, want 200", status, raw)
	}
	call := o.ws.saw(t, http.MethodPost, "/internal/incidents/inc-1/resolve")
	if call.Actor != p.Email {
		t.Fatalf("resolve X-Mal-Actor = %q, want %q", call.Actor, p.Email)
	}
	rows := o.st.auditRows(t, PermResolveIncident)
	if len(rows) != 1 || rows[0].Target != "inc-1" {
		t.Fatalf("audit rows for %s = %+v, want one targeting inc-1", PermResolveIncident, rows)
	}

	// Sweep pause and resume, per Coin, audited with the Coin as target.
	if status, raw = o.do(t, http.MethodPost, "/api/coins/LTC/sweep-pause", `{"paused":true}`, cookie); status != http.StatusOK {
		t.Fatalf("pause = %d %s, want 200", status, raw)
	}
	call = o.ws.saw(t, http.MethodPost, "/internal/coins/LTC/sweep-pause")
	if call.Body != `{"paused":true}` {
		t.Fatalf("pause body = %s, want the wallet wire shape", call.Body)
	}
	if status, _ = o.do(t, http.MethodPost, "/api/coins/LTC/sweep-pause", `{"paused":false}`, cookie); status != http.StatusOK {
		t.Fatalf("resume, want 200")
	}
	rows = o.st.auditRows(t, PermPauseSweep)
	if len(rows) != 2 || rows[0].Target != "LTC" || rows[1].Target != "LTC" {
		t.Fatalf("audit rows for %s = %+v, want two targeting LTC", PermPauseSweep, rows)
	}
	if status, raw = o.do(t, http.MethodPost, "/api/coins/LTC/sweep-pause", `{}`, cookie); status != http.StatusBadRequest {
		t.Fatalf("pause without a value = %d %s, want 400", status, raw)
	}

	// Wallet down: one honest 503, never a hang or a leaked error.
	o.ws.srv.Close()
	if status, raw = o.do(t, http.MethodGet, "/api/ops", "", cookie); status != http.StatusServiceUnavailable {
		t.Fatalf("wallet down = %d %s, want 503", status, raw)
	}
}
