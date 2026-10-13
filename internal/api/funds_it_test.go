//go:build integration

// Funds surface integration tests (spec #24 ticket #40): the member
// REST routes proxying the REAL wallet internal RPC — Ledger accounts,
// deposit addresses, deposits with confirmation counts, the Journal,
// fee preview, and the gated withdrawal — over the production mux
// wiring in one throwaway database (unique name: sibling IT suites
// share the dev Postgres). Assertions go through the served surface;
// the one direct wallet-table read (auto-opened accounts) is a
// tripwire with a comment, per docs/agents/review-checklist.md.
package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/syaikhipin/malxchange/internal/config"
	"github.com/syaikhipin/malxchange/internal/events"
	"github.com/syaikhipin/malxchange/internal/kafka"
	"github.com/syaikhipin/malxchange/internal/migrations"
	"github.com/syaikhipin/malxchange/internal/outbox"
	"github.com/syaikhipin/malxchange/internal/pgmig"
	"github.com/syaikhipin/malxchange/internal/wallet"
	"github.com/syaikhipin/malxchange/internal/wallet/keys"
)

// fundsStack is the ticket's two halves against one throwaway
// database: the api (member identity + funds routes, outbox relay on an
// in-process kfake) and the wallet (Ledger consumer + internal RPC).
type fundsStack struct {
	t       *testing.T
	apiPool *pgxpool.Pool // search_path=api
	walPool *pgxpool.Pool // search_path=wallet
	db      string
	mailer  *CaptureMailer
	gate    *fundsStubGate
	rest    *httptest.Server // the api's public mux
	wallet  *httptest.Server // the wallet's internal RPC
}

func newFundsStack(t *testing.T) *fundsStack {
	t.Helper()
	ctx := context.Background()

	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	dbName := "malxchange_ita_" + hex.EncodeToString(suffix)

	admin, err := pgxpool.New(ctx, fmt.Sprintf("postgres://%s:%s@%s:5432/postgres",
		os.Getenv("MAL_TEST_PG_SUPERUSER"), itPGPW, itPGHost))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, dbName)); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %s`, dbName)); err != nil {
		t.Fatal(err)
	}
	admin.Close()
	t.Cleanup(func() {
		admin, err := pgxpool.New(ctx, fmt.Sprintf("postgres://%s:%s@%s:5432/postgres",
			os.Getenv("MAL_TEST_PG_SUPERUSER"), itPGPW, itPGHost))
		if err != nil {
			return
		}
		defer admin.Close()
		_, _ = admin.Exec(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, dbName))
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
	apiPool := mkPool("api")
	walPool := mkPool("wallet")

	// member.register flows through the real outbox relay to the real
	// wallet settlement consumer, so Ledger accounts auto-open exactly
	// as they will in production.
	broker, err := kfake.NewCluster(kfake.NumBrokers(1))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(broker.Close)
	cfg := config.Kafka{Seeds: broker.ListenAddrs()}
	producer, err := kafka.NewProducer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(producer.Close)
	if err := broker.CreateTopic(events.TopicMemberRegister, 1, nil); err != nil {
		t.Fatal(err)
	}
	relayCtx, relayCancel := context.WithCancel(ctx)
	t.Cleanup(relayCancel)
	go func() { _ = outbox.NewRelay(apiPool, kafka.Publisher{Client: producer}).Run(relayCtx) }()

	consumer, err := kafka.NewConsumer(cfg, wallet.ConsumerGroup, []string{events.TopicMemberRegister},
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(consumer.Close)
	consumeCtx, consumeCancel := context.WithCancel(ctx)
	t.Cleanup(consumeCancel)
	go func() { _ = wallet.ConsumeLedgerTopics(consumeCtx, walPool, testLogger(), consumer) }()

	// The wallet internal RPC at its INTENDED /internal/* paths.
	// wallet.RegisterRPC pre-decorates every pattern with /internal, so
	// mounting the mux at the server root (no StripPrefix) is what
	// serves the documented contract; the boot harness's StripPrefix
	// currently doubles the prefix (backend-wires.md §0) — the api
	// client speaks the documented paths either way.
	seedXPub(t, walPool)
	internal := http.NewServeMux()
	wallet.RegisterRPC(internal, &wallet.RPC{
		DB:  walPool,
		Log: testLogger(),
		WatchFor: func(ctx context.Context, coin string) (*keys.Watch, error) {
			var family, xpub string
			if err := walPool.QueryRow(ctx,
				`SELECT family, xpub FROM coins WHERE name = $1`, coin).Scan(&family, &xpub); err != nil {
				return nil, err
			}
			if xpub == "" {
				return nil, fmt.Errorf("coin %s has no xpub", coin)
			}
			return keys.ImportXPub(family, keys.MainNet, xpub)
		},
	})
	walletSrv := httptest.NewServer(internal)
	t.Cleanup(walletSrv.Close)

	env := &APIEnv{
		CodeTTL:          time.Minute,
		CodeCooldown:     200 * time.Millisecond,
		MaxCodeAttempts:  2,
		LockoutThreshold: 3,
		LockoutWindow:    1500 * time.Millisecond,
		SessionTTL:       time.Hour,
	}
	mailer := &CaptureMailer{}
	gate := &fundsStubGate{codes: map[string]string{}}

	// The funds logger mirrors the wallet IT harness: failures carry
	// their evidence on stderr, the quiet logger is for the identity
	// surface #37 already covers.
	fundsLog := slog.New(slog.NewTextHandler(os.Stderr, nil))
	pub := http.NewServeMux()
	member := &memberAPI{pool: apiPool, env: env, mailer: mailer,
		ob: outbox.New(apiPool), log: testLogger()}
	member.RegisterMember(pub)
	funds := newFundsAPI(apiPool,
		newWalletClient(FundsConfig{WalletBaseURL: walletSrv.URL, Timeout: 10 * time.Second}, fundsLog),
		gate, member.withSession, fundsLog)
	funds.Register(pub)
	rest := httptest.NewServer(pub)
	t.Cleanup(rest.Close)

	return &fundsStack{t: t, apiPool: apiPool, walPool: walPool, db: dbName,
		mailer: mailer, gate: gate, rest: rest, wallet: walletSrv}
}

// seedXPub registers a BTC watch-only xpub so deposit-address
// derivation works (production does this in the key ceremony).
func seedXPub(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	mnemonic, err := keys.NewMnemonic(128)
	if err != nil {
		t.Fatal(err)
	}
	seed, err := keys.Seed(mnemonic, "")
	if err != nil {
		t.Fatal(err)
	}
	account, err := keys.DeriveAccount(seed, "btc", keys.MainNet)
	if err != nil {
		t.Fatal(err)
	}
	xpub, err := account.XPub()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(),
		`UPDATE coins SET xpub = $1 WHERE name = 'BTC'`, xpub); err != nil {
		t.Fatal(err)
	}
}

// fundsStubGate stands in for ticket #39's KYC/TOTP machinery: the
// state flips per member, exactly the matrix the gate must enforce.
type fundsStubGate struct {
	kyc   map[string]bool
	totp  map[string]bool
	codes map[string]string // memberID -> the one accepted code
}

func (g *fundsStubGate) KYCVerified(_ context.Context, memberID string) (bool, error) {
	return g.kyc[memberID], nil
}

func (g *fundsStubGate) AuthenticatorBound(_ context.Context, memberID string) (bool, error) {
	return g.totp[memberID], nil
}

func (g *fundsStubGate) VerifyAuthenticatorCode(_ context.Context, memberID, code string) (bool, error) {
	return g.codes[memberID] == code && code != "", nil
}

func (g *fundsStubGate) allow(memberID, code string) {
	g.kyc[memberID] = true
	g.totp[memberID] = true
	g.codes[memberID] = code
}

// ---- HTTP helpers (the itStack ones are that stack's; these are ours) ----

// doRaw performs one request and decodes the JSON body generically
// (objects and arrays alike).
func (s *fundsStack) doRaw(method, path string, body any, token string) (int, any) {
	s.t.Helper()
	var reader *strings.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			s.t.Fatal(err)
		}
		reader = strings.NewReader(string(raw))
	} else {
		reader = strings.NewReader("")
	}
	req, err := http.NewRequest(method, s.rest.URL+path, reader)
	if err != nil {
		s.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("access-auth-token", token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// do is doRaw for object bodies; the map is empty on a non-object answer.
func (s *fundsStack) do(method, path string, body any, token string) (int, map[string]any) {
	s.t.Helper()
	status, raw := s.doRaw(method, path, body, token)
	obj, _ := raw.(map[string]any)
	if obj == nil {
		obj = map[string]any{}
	}
	return status, obj
}

func (s *fundsStack) post(path string, body any, token string) (int, map[string]any) {
	return s.do(http.MethodPost, path, body, token)
}

func (s *fundsStack) get(path, token string) (int, map[string]any) {
	return s.do(http.MethodGet, path, nil, token)
}

// registerMember drives the email-code registration and returns the
// session token plus the member id (the wallet keys accounts on it).
func (s *fundsStack) registerMember(email string) (token, memberID string) {
	s.t.Helper()
	var mail []CapturedMail
	deadline := time.Now().Add(6 * time.Second)
	var code string
	for time.Now().Before(deadline) {
		before := len(s.mailer.Mail())
		status, body := s.post("/api/uc/register/code", map[string]string{"email": email}, "")
		if status != http.StatusOK && status != http.StatusTooManyRequests {
			s.t.Fatalf("request code: status %d body %v", status, body)
		}
		for wait := time.Now().Add(500 * time.Millisecond); time.Now().Before(wait); {
			mail = s.mailer.Mail()
			if len(mail) > before {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		for i := len(mail) - 1; i >= 0; i-- {
			if mail[i].To == email {
				if match := codeRe.FindStringSubmatch(mail[i].Body); match != nil {
					code = match[1]
				}
			}
		}
		if code != "" {
			break
		}
	}
	if code == "" {
		s.t.Fatalf("no captured mail carrying a code for %s", email)
	}
	status, body := s.post("/api/uc/register", map[string]string{
		"email": email, "code": code, "password": "correct horse battery"}, "")
	if status != http.StatusOK {
		s.t.Fatalf("register: status %d body %v", status, body)
	}
	member, _ := body["member"].(map[string]any)
	id, _ := member["id"].(string)
	if id == "" {
		s.t.Fatalf("register: no member id in body %v", body)
	}
	return body["token"].(string), id
}

// fund credits a member through the wallet's real adjustment RPC (the
// admin seam, on the wallet server), so every balance assertion rests
// on the real Ledger path. The Journal stores NUMERIC plain text, so a
// funded amount reads back normalized (25.500000 → 25.5); records tests
// that need scale-exact strings assert the balance view, which formats
// at the Coin's scale.
func (s *fundsStack) fund(memberID, coin, amount string) {
	s.t.Helper()
	raw, err := json.Marshal(map[string]string{
		"member": memberID, "coin": coin, "amount": amount, "reason": "it fixture"})
	if err != nil {
		s.t.Fatal(err)
	}
	resp, err := http.Post(s.wallet.URL+"/adjustments", "application/json",
		bytes.NewReader(raw))
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		s.t.Fatalf("fund %s %s: status %d body %s", memberID, amount, resp.StatusCode, body)
	}
}

// awaitAccountsOpened waits for the wallet's member.register consumer
// to open the member's Ledger rows. Tripwire read (review-checklist):
// the accounts endpoint LEFT-JOINs every Coin, so opened rows are not
// distinguishable through any served surface.
func (s *fundsStack) awaitAccountsOpened(memberID string) {
	s.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var opened, coins int
		if err := s.walPool.QueryRow(context.Background(),
			`SELECT (SELECT count(*) FROM ledger_accounts WHERE member_id = $1),
			        (SELECT count(*) FROM coins)`, memberID).Scan(&opened, &coins); err != nil {
			s.t.Fatal(err)
		}
		if opened == coins && coins > 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	s.t.Fatalf("wallet never opened accounts for %s (member.register consumer)", memberID)
}

// accountsOf fetches the member's balances keyed by Coin (keyed read,
// never positional — review-checklist).
func (s *fundsStack) accountsOf(token string) map[string]map[string]string {
	s.t.Helper()
	status, raw := s.doRaw(http.MethodGet, "/api/uc/wallet/accounts", nil, token)
	if status != http.StatusOK {
		s.t.Fatalf("accounts: status %d body %v", status, raw)
	}
	rows, _ := raw.([]any)
	out := map[string]map[string]string{}
	for _, row := range rows {
		m, _ := row.(map[string]any)
		coin, _ := m["coin"].(string)
		entry := map[string]string{}
		for k, v := range m {
			if str, ok := v.(string); ok {
				entry[k] = str
			}
		}
		out[coin] = entry
	}
	return out
}

// ---- the seam ----

// TestFundsJourney walks the acceptance path: register → balances
// visible (accounts auto-opened) → deposit address per Coin → deposits
// with state and confirmation counts.
func TestFundsJourney(t *testing.T) {
	s := newFundsStack(t)
	token, memberID := s.registerMember("journey@example.com")
	s.awaitAccountsOpened(memberID)

	accounts := s.accountsOf(token)
	// The seeded Coin set, all zero, all exact decimal strings at the
	// Coin's scale — string in, string out (ADR-0005).
	for coin, want := range map[string]string{
		"BTC": "0.00000000", "LTC": "0.00000000", "ETH": "0.000000000000000000",
		"TRX": "0.000000", "USDT": "0.000000", "USDT-TRC": "0.000000",
	} {
		got, ok := accounts[coin]
		if !ok {
			t.Fatalf("balances: %s missing from %v", coin, accounts)
		}
		if got["available"] != want || got["frozen"] != want {
			t.Errorf("balances %s: available=%q frozen=%q, want %q", coin, got["available"], got["frozen"], want)
		}
	}

	// One deposit address per Coin, stable across requests.
	status, body := s.post("/api/uc/wallet/deposit-address", map[string]string{"coin": "BTC"}, token)
	if status != http.StatusOK {
		t.Fatalf("deposit address: status %d body %v", status, body)
	}
	addr, _ := body["address"].(string)
	if body["coin"] != "BTC" || !strings.HasPrefix(addr, "bc1") {
		t.Fatalf("deposit address: body %v", body)
	}
	status, again := s.post("/api/uc/wallet/deposit-address", map[string]string{"coin": "BTC"}, token)
	if status != http.StatusOK || again["address"] != addr {
		t.Errorf("deposit address re-request: status %d body %v (want same address %q)", status, again, addr)
	}

	// Deposits: detected with live confirmations, credited, exact
	// decimal amounts (rows seeded against the member's own address;
	// the list is the served surface).
	_, err := s.walPool.Exec(context.Background(), `
		INSERT INTO deposits (id, coin, member_id, address, txid, vout, amount,
		                      detected_height, confirmations, status, created_at)
		VALUES
		  ($1, 'BTC', $2, $3, 'journey-tx-1', 0, '0.00020000', 100, 1, 'detected', $4),
		  ($5, 'USDT', $2, '0xdummy', 'journey-tx-2', 0, '12.340000', 200, 12, 'credited', $6)`,
		uuid.NewString(), memberID, addr, time.Now().UTC().Add(-2*time.Minute),
		uuid.NewString(), time.Now().UTC().Add(-time.Minute))
	if err != nil {
		t.Fatalf("seed deposits: %v", err)
	}

	status, raw := s.doRaw(http.MethodGet, "/api/uc/wallet/deposits", nil, token)
	if status != http.StatusOK {
		t.Fatalf("deposits: status %d body %v", status, raw)
	}
	rows, _ := raw.([]any)
	byTx := map[string]map[string]any{}
	for _, row := range rows {
		m, _ := row.(map[string]any)
		txid, _ := m["txid"].(string)
		byTx[txid] = m
	}
	detected := byTx["journey-tx-1"]
	if detected == nil {
		t.Fatalf("deposits: journey-tx-1 missing from %v", raw)
	}
	if detected["status"] != "detected" || detected["confirmations"] != float64(1) || detected["needed"] != float64(3) {
		t.Errorf("detected deposit: %v (want status=detected confirmations=1 needed=3)", detected)
	}
	if detected["amount"] != "0.00020000" {
		t.Errorf("detected deposit amount: %v, want the exact decimal string 0.00020000", detected["amount"])
	}
	credited := byTx["journey-tx-2"]
	if credited == nil || credited["status"] != "credited" {
		t.Fatalf("credited deposit: %v", credited)
	}
}

// TestRecordsList answers the member center's records page: every
// Journal movement in member-facing keys, money verbatim, optional
// Coin filter, [] when empty.
func TestRecordsList(t *testing.T) {
	s := newFundsStack(t)
	token, memberID := s.registerMember("records@example.com")
	s.awaitAccountsOpened(memberID)
	s.fund(memberID, "USDT", "25.500000")

	status, raw := s.doRaw(http.MethodGet, "/api/uc/wallet/records", nil, token)
	if status != http.StatusOK {
		t.Fatalf("records: status %d body %v", status, raw)
	}
	rows, _ := raw.([]any)
	if len(rows) == 0 {
		t.Fatalf("records: empty body %v", raw)
	}
	var adjustment map[string]any
	for _, row := range rows {
		m, _ := row.(map[string]any)
		if m["kind"] == "adjustment" && m["coin"] == "USDT" {
			adjustment = m
		}
	}
	if adjustment == nil {
		t.Fatalf("records: no USDT adjustment entry in %v", raw)
	}
	if adjustment["amount"] != "25.5" || adjustment["availDelta"] != "25.5" {
		t.Errorf("adjustment entry money: amount=%v availDelta=%v, want the exact journal values 25.5",
			adjustment["amount"], adjustment["availDelta"])
	}
	if _, ok := adjustment["reason"].(string); !ok {
		t.Errorf("adjustment entry reason missing: %v", adjustment)
	}
	at, _ := adjustment["at"].(string)
	if _, err := time.Parse(time.RFC3339, at); err != nil {
		t.Errorf("adjustment entry at=%q is not RFC3339: %v", at, err)
	}

	// Coin filter: a Coin with no movements answers [], never null.
	status, raw = s.doRaw(http.MethodGet, "/api/uc/wallet/records?coin=BTC", nil, token)
	if status != http.StatusOK {
		t.Fatalf("records filtered: status %d body %v", status, raw)
	}
	if fmt.Sprint(raw) != "[]" {
		t.Errorf("records filtered by an unmoved Coin: %v, want []", raw)
	}
}

// TestWithdrawAddressBookCRUD walks the api-owned withdrawal address
// book: add, list, relabel, delete — plus the guards (unknown Coin,
// duplicate, another member's entry invisible and undeletable).
func TestWithdrawAddressBookCRUD(t *testing.T) {
	s := newFundsStack(t)
	token, _ := s.registerMember("book@example.com")
	otherToken, _ := s.registerMember("book-other@example.com")

	// Unknown Coins cannot enter the book.
	status, body := s.post("/api/uc/withdraw-addresses", map[string]string{
		"coin": "DOGE", "address": "0xdead", "label": "nope"}, token)
	if status != http.StatusBadRequest {
		t.Fatalf("add unknown coin: status %d body %v", status, body)
	}

	status, body = s.post("/api/uc/withdraw-addresses", map[string]string{
		"coin": "USDT", "address": "0xsavings", "label": "Savings"}, token)
	if status != http.StatusOK {
		t.Fatalf("add: status %d body %v", status, body)
	}
	id, _ := body["id"].(string)
	if id == "" || body["coin"] != "USDT" || body["address"] != "0xsavings" ||
		body["label"] != "Savings" || body["createdAt"] == nil {
		t.Fatalf("add: body %v", body)
	}

	// Duplicate (same Coin + address) is a conflict.
	status, body = s.post("/api/uc/withdraw-addresses", map[string]string{
		"coin": "USDT", "address": "0xsavings", "label": "Again"}, token)
	if status != http.StatusConflict {
		t.Fatalf("duplicate add: status %d body %v", status, body)
	}

	// List shows the entry; another member sees none of it.
	listEntries := func(tok string) map[string]map[string]any {
		t.Helper()
		status, raw := s.doRaw(http.MethodGet, "/api/uc/withdraw-addresses", nil, tok)
		if status != http.StatusOK {
			t.Fatalf("list: status %d body %v", status, raw)
		}
		rows, _ := raw.([]any)
		out := map[string]map[string]any{}
		for _, row := range rows {
			m, _ := row.(map[string]any)
			if entryID, _ := m["id"].(string); entryID != "" {
				out[entryID] = m
			}
		}
		return out
	}
	if _, ok := listEntries(token)[id]; !ok {
		t.Fatal("list: saved entry missing")
	}
	if entries := listEntries(otherToken); len(entries) != 0 {
		t.Fatalf("list: another member sees %v", entries)
	}

	// Relabel.
	status, body = s.do(http.MethodPut, "/api/uc/withdraw-addresses/"+id,
		map[string]string{"label": "Cold storage"}, token)
	if status != http.StatusOK {
		t.Fatalf("relabel: status %d body %v", status, body)
	}
	if entry := listEntries(token)[id]; entry["label"] != "Cold storage" {
		t.Fatalf("relabel: entry %v", entry)
	}

	// Another member cannot touch it.
	status, _ = s.do(http.MethodDelete, "/api/uc/withdraw-addresses/"+id, nil, otherToken)
	if status != http.StatusNotFound {
		t.Fatalf("foreign delete: status %d, want 404", status)
	}

	// Delete, then the book is empty ([]).
	status, body = s.do(http.MethodDelete, "/api/uc/withdraw-addresses/"+id, nil, token)
	if status != http.StatusOK {
		t.Fatalf("delete: status %d body %v", status, body)
	}
	status, raw := s.doRaw(http.MethodGet, "/api/uc/withdraw-addresses", nil, token)
	if status != http.StatusOK || fmt.Sprint(raw) != "[]" {
		t.Fatalf("list after delete: status %d body %v, want []", status, raw)
	}
}
