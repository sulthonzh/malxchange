//go:build integration

// The finance funds surface over the REAL seam (spec #59): the admin
// surface with its full registry gate chain (session → permission →
// audit) talking to the wallet's internal RPC mounted in-process —
// one throwaway database, admin and wallet schemas side by side, the
// production mux wiring on both. Assertions go through the served
// HTTP surfaces; the audit_log queries are the black-box read of what
// the middleware must have written.
package admin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/syaikhipin/malxchange/internal/migrations"
	"github.com/syaikhipin/malxchange/internal/money"
	"github.com/syaikhipin/malxchange/internal/pgmig"
	"github.com/syaikhipin/malxchange/internal/wallet"
)

type fundsStack struct {
	pool    *pgxpool.Pool // search_path=admin
	walPool *pgxpool.Pool // search_path=wallet
	walSrv  *httptest.Server
	rest    *httptest.Server
	t       *testing.T
}

func newFundsStack(t *testing.T) *fundsStack {
	t.Helper()
	ctx := context.Background()

	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	dbName := "malxchange_itf_" + hex.EncodeToString(suffix)

	adminPool, err := pgxpool.New(ctx, fmt.Sprintf("postgres://%s:%s@%s:5432/postgres",
		os.Getenv("MAL_TEST_PG_SUPERUSER"), itPGPW, itPGHost))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adminPool.Exec(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, dbName)); err != nil {
		t.Fatal(err)
	}
	if _, err := adminPool.Exec(ctx, `CREATE DATABASE `+dbName); err != nil {
		t.Fatal(err)
	}
	adminPool.Close()
	t.Cleanup(func() {
		adminPool, err := pgxpool.New(ctx, fmt.Sprintf("postgres://%s:%s@%s:5432/postgres",
			os.Getenv("MAL_TEST_PG_SUPERUSER"), itPGPW, itPGHost))
		if err != nil {
			return
		}
		defer adminPool.Close()
		_, _ = adminPool.Exec(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, dbName))
	})

	mkPool := func(schema string) *pgxpool.Pool {
		t.Helper()
		u := url.URL{
			Scheme:   "postgres",
			User:     url.UserPassword(os.Getenv("MAL_TEST_PG_SUPERUSER"), itPGPW),
			Host:     itPGHost + ":5432",
			Path:     "/" + dbName,
			RawQuery: "sslmode=disable&search_path=" + schema,
		}
		pool, err := pgxpool.New(ctx, u.String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
		if _, err := pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS `+schema); err != nil {
			t.Fatal(err)
		}
		if err := pgmig.Run(u.String(), migrations.FS, schema); err != nil {
			t.Fatalf("%s migrations: %v", schema, err)
		}
		return pool
	}
	pool := mkPool("admin")
	walPool := mkPool("wallet")

	// The wallet internal RPC at its production paths (/internal/* via
	// StripPrefix), pointed at the admin stack through env exactly the
	// way compose points admin-api at the wallet service.
	walRPC := http.NewServeMux()
	wallet.RegisterRPC(walRPC, &wallet.RPC{DB: walPool, Log: nil})
	walRoot := http.NewServeMux()
	walRoot.Handle("/internal/", http.StripPrefix("/internal", walRPC))
	walSrv := httptest.NewServer(walRoot)
	t.Cleanup(walSrv.Close)

	// Test-tuned env, as admin_it_test.go's newStack; the wallet leg
	// rides the in-process server.
	env := &AdminEnv{
		SessionAbsolute:  12 * time.Hour,
		SessionIdle:      30 * time.Minute,
		LockoutThreshold: LockoutThreshold,
		LockoutWindow:    1500 * time.Millisecond,
		WalletBaseURL:    walSrv.URL + "/internal",
		WalletTimeout:    5 * time.Second,
	}

	root := http.NewServeMux()
	internal := http.NewServeMux()
	srv := NewServer(pool, env, nil)
	if err := srv.SeedRBAC(ctx); err != nil {
		t.Fatalf("seed rbac: %v", err)
	}
	srv.RegisterPublic(root)
	srv.RegisterInternal(internal)
	root.Handle("/internal/", http.StripPrefix("/internal", internal))
	rest := httptest.NewServer(root)
	t.Cleanup(rest.Close)

	return &fundsStack{pool: pool, walPool: walPool, walSrv: walSrv, rest: rest, t: t}
}

// authStack re-frames the served admin surface as the auth-core suite's
// itStack so the Employee helpers (bootstrap, fullyProvisioned, employee,
// roleIDOf, auditRows) run unchanged against the same server and pool.
func (s *fundsStack) authStack() *itStack {
	return &itStack{pool: s.pool, rest: s.rest, t: s.t}
}

// ---- wallet seeding (through the wallet package's own seams) ----

// faucet credits a member so withdrawals can be requested.
func (s *fundsStack) faucet(t *testing.T, member, coin, amount string) {
	t.Helper()
	if err := wallet.Credit(context.Background(), s.walPool, member, coin, wallet.KindAdjustment,
		"test faucet", "it-faucet:"+member+":"+coin,
		moneyParse(t, amount), "", "test"); err != nil {
		t.Fatal(err)
	}
}

// requestWithdrawal creates one withdrawal in state audit.
func (s *fundsStack) requestWithdrawal(t *testing.T, member, address, amount string) wallet.Withdrawal {
	t.Helper()
	coin, err := wallet.GetCoin(context.Background(), s.walPool, "USDT")
	if err != nil {
		t.Fatal(err)
	}
	wd, err := wallet.RequestWithdrawal(context.Background(), s.walPool, coin, member, address, moneyParse(t, amount))
	if err != nil {
		t.Fatal(err)
	}
	return wd
}

// failWithdrawal walks a withdrawal into state failed (approve → the
// executor marks the broadcast failed).
func (s *fundsStack) failWithdrawal(t *testing.T, id string) {
	t.Helper()
	if err := wallet.AuditWithdrawal(context.Background(), s.walPool, id, true, "executor"); err != nil {
		t.Fatal(err)
	}
	if err := wallet.MarkWithdrawalFailed(context.Background(), s.walPool, id, "broadcast: chain unreachable"); err != nil {
		t.Fatal(err)
	}
}

// available reads one balance through the wallet's accounts RPC (the
// served surface, not a table read).
func (s *fundsStack) available(t *testing.T, member, coin string) string {
	t.Helper()
	resp, err := http.Get(s.walSrv.URL + "/internal/accounts/" + member)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("accounts %s: status %d", member, resp.StatusCode)
	}
	var rows []struct {
		Coin      string `json:"coin"`
		Available string `json:"available"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Coin == coin {
			return row.Available
		}
	}
	return "0"
}

// ---- HTTP helpers (the admin surface) ----

// freshCookie signs p in with a fresh code and returns the live
// session cookie.
func (p provisioned) fundsCookie(t *testing.T, s *fundsStack) *http.Cookie {
	return p.freshCookie(t, s.authStack())
}

// fundsAvailable asserts the member's available balance equals want at
// money equality (the accounts RPC formats at the Coin's scale, so
// 100 arrives as "100.000000").
func (s *fundsStack) fundsAvailable(t *testing.T, member, coin, want string) {
	t.Helper()
	got := s.available(t, member, coin)
	if !money.MustParse(got).Equal(money.MustParse(want)) {
		t.Fatalf("%s %s available = %s, want %s", member, coin, got, want)
	}
}

func moneyParse(t *testing.T, raw string) decimal.Decimal {
	t.Helper()
	d, err := money.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return d
}

// ---- acceptance journeys ----

// fundsActors provisions the whole cast: a super-admin root, a finance
// auditor, and a support Employee (no funds permissions at all).
func fundsActors(t *testing.T, s *fundsStack) (root, fin, sup provisioned, rootCookie, finCookie, supCookie *http.Cookie) {
	t.Helper()
	auth := s.authStack()
	root = auth.fullyProvisioned(t, "funds-root@it.test")
	rootCookie = root.freshCookie(t, auth)
	fin = auth.employee(t, root, rootCookie, "funds-finance@it.test", RoleFinance)
	finCookie = fin.freshCookie(t, auth)
	sup = auth.employee(t, root, rootCookie, "funds-support@it.test", RoleSupport)
	supCookie = sup.freshCookie(t, auth)
	return root, fin, sup, rootCookie, finCookie, supCookie
}

func TestWithdrawalQueueDetailAndFilters(t *testing.T) {
	s := newFundsStack(t)
	_, _, _, rootCookie, finCookie, supCookie := fundsActors(t, s)

	s.faucet(t, "iris", "USDT", "500")
	s.faucet(t, "jack", "USDT", "500")
	wIris := s.requestWithdrawal(t, "iris", "0xiris", "20")
	wJack := s.requestWithdrawal(t, "jack", "0xjack", "30")

	// The queue: newest first, both records, decimal strings at the
	// Coin's scale.
	status, _, raw := s.authStack().do(t, "GET", "/api/withdrawals", "", finCookie)
	if status != http.StatusOK {
		t.Fatalf("queue: %d %s", status, raw)
	}
	var list []map[string]any
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("queue len = %d, want 2 (%s)", len(list), raw)
	}
	if list[0]["id"] != wJack.ID || list[0]["state"] != wallet.WAudit {
		t.Fatalf("queue[0] = %v, want jack's audit row", list[0])
	}

	// Member and state filters.
	status, _, raw = s.authStack().do(t, "GET", "/api/withdrawals?member=iris", "", finCookie)
	if status != http.StatusOK {
		t.Fatalf("member filter: %d %s", status, raw)
	}
	list = nil
	if err := json.Unmarshal([]byte(raw), &list); err != nil || len(list) != 1 || list[0]["member"] != "iris" {
		t.Fatalf("member filter = %d %s err %v", status, raw, err)
	}

	// Detail: the full record, updatedAt included.
	status, _, raw = s.authStack().do(t, "GET", "/api/withdrawals/"+wIris.ID, "", finCookie)
	if status != http.StatusOK {
		t.Fatalf("detail: %d %s", status, raw)
	}
	var detail map[string]any
	if err := json.Unmarshal([]byte(raw), &detail); err != nil {
		t.Fatal(err)
	}
	if detail["id"] != wIris.ID || detail["amount"] != "20" || detail["fee"] != "2" || detail["net"] != "18" {
		t.Fatalf("detail body wrong: %s", raw)
	}
	if updatedAt, _ := detail["updatedAt"].(string); updatedAt == "" {
		t.Fatalf("detail missing updatedAt: %s", raw)
	}

	// Unknown id → 404, never a 500.
	if status, _, _ = s.authStack().do(t, "GET", "/api/withdrawals/00000000-0000-0000-0000-000000000000", "", finCookie); status != http.StatusNotFound {
		t.Fatalf("unknown detail = %d, want 404", status)
	}

	// Read side: super-admin reads (read-side everything), support is
	// refused outright.
	if status, _, _ := s.authStack().do(t, "GET", "/api/withdrawals", "", supCookie); status != http.StatusForbidden {
		t.Fatalf("support queue = %d, want 403", status)
	}
	if status, _, _ := s.authStack().do(t, "GET", "/api/withdrawals/"+wIris.ID, "", supCookie); status != http.StatusForbidden {
		t.Fatalf("support detail = %d, want 403", status)
	}
	if status, _, _ := s.authStack().do(t, "GET", "/api/withdrawals", "", rootCookie); status != http.StatusOK {
		t.Fatalf("super-admin queue = %d, want 200 (read side)", status)
	}
	if status, _, _ := s.authStack().do(t, "GET", "/api/withdrawals/"+wJack.ID, "", rootCookie); status != http.StatusOK {
		t.Fatalf("super-admin detail = %d, want 200 (read side)", status)
	}
}

func TestWithdrawalApproveDemandsStepUpAndProceeds(t *testing.T) {
	s := newFundsStack(t)
	_, fin, _, _, finCookie, supCookie := fundsActors(t, s)
	auth := s.authStack()

	s.faucet(t, "kate", "USDT", "500")
	w := s.requestWithdrawal(t, "kate", "0xkate", "25")

	// Step-up is enforced server-side: no code → 401 before the wallet
	// is touched (the withdrawal stays in audit).
	status, _, raw := auth.post(t, "/api/withdrawals/"+w.ID+"/approve", "{}", finCookie)
	if status != http.StatusUnauthorized || !strings.Contains(raw, "fresh authenticator code") {
		t.Fatalf("approve without code = %d %s, want 401", status, raw)
	}
	// A well-formed but wrong code → 401 as well.
	code := wrongCodeOf(t, fin.TOTPSecr)
	status, _, raw = auth.post(t, "/api/withdrawals/"+w.ID+"/approve",
		fmt.Sprintf(`{"code":%q}`, code), finCookie)
	if status != http.StatusUnauthorized || !strings.Contains(raw, "not valid") {
		t.Fatalf("approve wrong code = %d %s, want 401", status, raw)
	}

	// The matrix: super-admin may read but not approve; support
	// neither.
	status, _, raw = auth.post(t, "/api/withdrawals/"+w.ID+"/approve",
		fmt.Sprintf(`{"code":%q}`, code), supCookie)
	if status != http.StatusForbidden {
		t.Fatalf("support approve = %d %s, want 403", status, raw)
	}

	// Fresh code → the withdrawal proceeds to the signer.
	fresh, err := totpNow(fin.TOTPSecr)
	if err != nil {
		t.Fatal(err)
	}
	status, _, raw = auth.post(t, "/api/withdrawals/"+w.ID+"/approve",
		fmt.Sprintf(`{"code":%q}`, fresh), finCookie)
	if status != http.StatusOK {
		t.Fatalf("approve with fresh code = %d %s, want 200", status, raw)
	}
	if got := s.available(t, "kate", "USDT"); !money.MustParse(got).Equal(money.MustParse("475")) {
		t.Fatalf("kate available = %s, want 475 (500 − 25 debit)", got)
	}
	if status, _, raw = auth.do(t, "GET", "/api/withdrawals/"+w.ID, "", finCookie); status != http.StatusOK || !strings.Contains(raw, `"state":"approved"`) {
		t.Fatalf("detail after approve = %d %s", status, raw)
	}

	// The audit row: action == permission string, target = the
	// withdrawal (never the caller), actor = the finance Employee.
	rows := auth.auditRows(t, PermApproveWithdrawal)
	if len(rows) != 1 {
		t.Fatalf("want exactly 1 %s row, got %d", PermApproveWithdrawal, len(rows))
	}
	if rows[0].Actor != fin.Email || rows[0].Target != w.ID {
		t.Fatalf("audit row actor=%q target=%q", rows[0].Actor, rows[0].Target)
	}
}

func TestWithdrawalRejectRetryRefund(t *testing.T) {
	s := newFundsStack(t)
	_, fin, _, _, finCookie, _ := fundsActors(t, s)
	auth := s.authStack()

	s.faucet(t, "liam", "USDT", "300")
	s.faucet(t, "mia", "USDT", "300")
	s.faucet(t, "noah", "USDT", "300")

	// Reject: funds return through the Journal, no Step-up demanded
	// (not in ADR-0012's step-up list), still audit-logged.
	wReject := s.requestWithdrawal(t, "liam", "0xliam", "30")
	s.fundsAvailable(t, "liam", "USDT", "270")
	status, _, raw := auth.post(t, "/api/withdrawals/"+wReject.ID+"/reject", "{}", finCookie)
	if status != http.StatusOK {
		t.Fatalf("reject = %d %s, want 200", status, raw)
	}
	s.fundsAvailable(t, "liam", "USDT", "300")

	// Retry: a failed broadcast re-arms — Step-up mandatory.
	wRetry := s.requestWithdrawal(t, "mia", "0xmia", "12")
	s.failWithdrawal(t, wRetry.ID)
	code := wrongCodeOf(t, fin.TOTPSecr)
	if status, _, raw = auth.post(t, "/api/withdrawals/"+wRetry.ID+"/retry",
		fmt.Sprintf(`{"code":%q}`, code), finCookie); status != http.StatusUnauthorized {
		t.Fatalf("retry wrong code = %d %s, want 401", status, raw)
	}
	fresh, err := totpNow(fin.TOTPSecr)
	if err != nil {
		t.Fatal(err)
	}
	if status, _, raw = auth.post(t, "/api/withdrawals/"+wRetry.ID+"/retry",
		fmt.Sprintf(`{"code":%q}`, fresh), finCookie); status != http.StatusOK {
		t.Fatalf("retry = %d %s, want 200", status, raw)
	}
	if status, _, raw = auth.do(t, "GET", "/api/withdrawals/"+wRetry.ID, "", finCookie); status != http.StatusOK || !strings.Contains(raw, `"state":"approved"`) {
		t.Fatalf("detail after retry = %d %s, want approved", status, raw)
	}

	// Refund: a failed withdrawal the Employee decides not to retry
	// returns the funds.
	wRefund := s.requestWithdrawal(t, "noah", "0xnoah", "44")
	s.failWithdrawal(t, wRefund.ID)
	if status, _, raw = auth.post(t, "/api/withdrawals/"+wRefund.ID+"/refund", "{}", finCookie); status != http.StatusOK {
		t.Fatalf("refund = %d %s, want 200", status, raw)
	}
	s.fundsAvailable(t, "noah", "USDT", "300")

	// Every decision left its audit row, target = the withdrawal id.
	for _, action := range []string{PermRejectWithdrawal, PermRetryWithdrawal, PermRefundWithdrawal} {
		rows := auth.auditRows(t, action)
		if len(rows) != 1 || rows[0].Actor != fin.Email {
			t.Fatalf("%s rows = %+v, want exactly one by %s", action, rows, fin.Email)
		}
	}
}

func TestManualAdjustmentJourney(t *testing.T) {
	s := newFundsStack(t)
	_, fin, _, rootCookie, finCookie, _ := fundsActors(t, s)
	auth := s.authStack()

	s.faucet(t, "petra", "USDT", "10")

	call := func(body string) (int, string) {
		status, _, raw := auth.post(t, "/api/adjustments", body, finCookie)
		return status, raw
	}
	body := fmt.Sprintf(`{"member":"petra","coin":"USDT","amount":"90","reason":"support case #4182","key":"case-4182","code":%q}`, mustCode(t, fin))

	// Step-up first: without the code the adjustment is refused.
	if status, _, raw := auth.post(t, "/api/adjustments",
		`{"member":"petra","coin":"USDT","amount":"90","reason":"support case #4182","key":"case-4182"}`,
		finCookie); status != http.StatusUnauthorized {
		t.Fatalf("adjust without code = %d %s, want 401", status, raw)
	}

	status, raw := call(body)
	if status != http.StatusOK {
		t.Fatalf("adjust = %d %s, want 200", status, raw)
	}
	s.fundsAvailable(t, "petra", "USDT", "100")

	// The Journal entry carries the reason and the acting Employee; the
	// Ledger moved through it (fold holds by construction).
	var reason, actor string
	if err := s.walPool.QueryRow(context.Background(),
		`SELECT reason, COALESCE(actor,'') FROM journal WHERE idem_key = $1`,
		"adjust:case-4182").Scan(&reason, &actor); err != nil {
		t.Fatal(err)
	}
	if reason != "adjustment: support case #4182" || actor != fin.Email {
		t.Fatalf("journal reason=%q actor=%q", reason, actor)
	}

	// Replaying the same key is a no-op: applied=false, no movement.
	// The handler is idempotent, the step-up code is not (#103): clear
	// the consumed step so the replayed request passes the gate again.
	auth.clearConsumedStep(t, fin.Email)
	status, raw = call(body)
	if status != http.StatusOK || !strings.Contains(raw, `"applied":false`) {
		t.Fatalf("replay = %d %s, want 200 applied:false", status, raw)
	}
	s.fundsAvailable(t, "petra", "USDT", "100")

	// The audit rows: the apply AND the replay (200, moved nothing)
	// each write one row, target = the member; the refused attempts
	// (401/403/400) drop per the middleware's error rule.
	rows := auth.auditRows(t, PermAdjustBalance)
	if len(rows) != 2 {
		t.Fatalf("want 2 %s rows, got %d", PermAdjustBalance, len(rows))
	}
	row := rows[0]
	if row.Actor != fin.Email || row.Target != "petra" {
		t.Fatalf("adjust audit actor=%q target=%q", row.Actor, row.Target)
	}
	var before, after []map[string]any
	if err := json.Unmarshal(row.Before, &before); err != nil {
		t.Fatalf("before: %v (%s)", err, row.Before)
	}
	if err := json.Unmarshal(row.After, &after); err != nil {
		t.Fatalf("after: %v (%s)", err, row.After)
	}
	availOf := func(rows []map[string]any) string {
		for _, r := range rows {
			if r["coin"] == "USDT" {
				s, _ := r["available"].(string)
				return s
			}
		}
		return ""
	}
	if !money.MustParse(availOf(before)).Equal(money.MustParse("10")) ||
		!money.MustParse(availOf(after)).Equal(money.MustParse("100")) {
		t.Fatalf("audit balances before=%v after=%v", before, after)
	}

	// Refusals: mandatory reason, key, and a non-zero signed amount.
	noReason := `{"member":"petra","coin":"USDT","amount":"1","key":"k1","code":"000000"}`
	if status, _ = call(noReason); status != http.StatusBadRequest {
		t.Fatalf("missing reason = %d, want 400", status)
	}
	if status, _ = call(`{"member":"petra","coin":"USDT","amount":"1","reason":"r","code":"000000"}`); status != http.StatusBadRequest {
		t.Fatalf("missing key = %d, want 400", status)
	}
	if status, _ = call(fmt.Sprintf(`{"member":"petra","coin":"USDT","amount":"0","reason":"r","key":"k2","code":%q}`, mustCode(t, fin))); status != http.StatusBadRequest {
		t.Fatalf("zero amount = %d, want 400", status)
	}

	// A negative adjustment moves the balance down — support cases
	// debit as well as credit. Earlier journeys consumed their steps
	// (#103); clear so this one passes the gate.
	auth.clearConsumedStep(t, fin.Email)
	neg := fmt.Sprintf(`{"member":"petra","coin":"USDT","amount":"-7","reason":"fee correction","key":"case-4183","code":%q}`, mustCode(t, fin))
	if status, raw = call(neg); status != http.StatusOK {
		t.Fatalf("negative adjust = %d %s, want 200", status, raw)
	}
	s.fundsAvailable(t, "petra", "USDT", "93")

	// The matrix: super-admin cannot adjust — money-moving writes are
	// finance's alone (the permission middleware refuses before the
	// handler, so no Step-up dance happens either).
	if status, _, raw := auth.post(t, "/api/adjustments",
		`{"member":"petra","coin":"USDT","amount":"1","reason":"r","key":"k9","code":"000000"}`,
		rootCookie); status != http.StatusForbidden {
		t.Fatalf("super-admin adjust = %d %s, want 403", status, raw)
	}
}

func mustCode(t *testing.T, p provisioned) string {
	t.Helper()
	code, err := totpNow(p.TOTPSecr)
	if err != nil {
		t.Fatal(err)
	}
	return code
}
