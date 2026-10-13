//go:build integration

// The member order seam over the real stack (ticket #42): api REST +
// the real wallet service (freeze through the production mux wiring) +
// an in-process kfake broker carrying order.command out of the real
// outbox relay + market and exchange stubs. Assertions go through the
// served surfaces only — api REST, the wallet's internal RPC, and the
// broker topic. Throwaway database with a unique name per run (sibling
// IT suites share the dev Postgres).
package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
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
	"github.com/syaikhipin/malxchange/internal/money"
	"github.com/syaikhipin/malxchange/internal/outbox"
	"github.com/syaikhipin/malxchange/internal/pgmig"
	"github.com/syaikhipin/malxchange/internal/wallet"
)

// ordersStack is one test's world: the api service (member + order
// routes on the public mux), the real wallet service behind the
// production /internal wiring, a kfake broker with the outbox relay,
// and the market/exchange stubs the order path calls.
type ordersStack struct {
	t        *testing.T
	pool     *pgxpool.Pool // search_path=api
	wpool    *pgxpool.Pool // search_path=wallet (the real wallet seam)
	cfg      config.Kafka
	broker   *kfake.Cluster
	consumer *kgo.Client // order.command, reset to start
	relay    *outbox.Relay
	mailer   *CaptureMailer
	market   *httptest.Server
	exchange *exchangeStub
	wallet   *httptest.Server
	rest     *httptest.Server
}

func newOrdersStack(t *testing.T) *ordersStack {
	t.Helper()
	ctx := context.Background()

	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	dbName := "malxchange_itao_" + hex.EncodeToString(suffix)
	dsn := fmt.Sprintf("postgres://%s:%s@%s:5432/",
		os.Getenv("MAL_TEST_PG_SUPERUSER"), itPGPW, itPGHost)
	admin, err := pgxpool.New(ctx, dsn+"postgres")
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
		admin, err := pgxpool.New(ctx, dsn+"postgres")
		if err != nil {
			return
		}
		defer admin.Close()
		_, _ = admin.Exec(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, dbName))
	})

	newPool := func(schema string) *pgxpool.Pool {
		t.Helper()
		pool, err := pgxpool.New(ctx, fmt.Sprintf("%s%s?sslmode=disable&search_path=%s", dsn, dbName, schema))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
		if _, err := pool.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s`, schema)); err != nil {
			t.Fatal(err)
		}
		u := url.URL{
			Scheme:   "postgres",
			User:     url.UserPassword(os.Getenv("MAL_TEST_PG_SUPERUSER"), itPGPW),
			Host:     itPGHost + ":5432",
			Path:     dbName,
			RawQuery: "sslmode=disable&search_path=" + schema,
		}
		if err := pgmig.Run(u.String(), migrations.FS, schema); err != nil {
			t.Fatalf("%s migrations: %v", schema, err)
		}
		return pool
	}
	pool := newPool("api")
	wpool := newPool("wallet")

	broker, err := kfake.NewCluster(kfake.NumBrokers(1))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(broker.Close)
	cfg := config.Kafka{Seeds: broker.ListenAddrs()}
	for _, topic := range []string{events.TopicMemberRegister, events.TopicOrderCommand} {
		if err := broker.CreateTopic(topic, 1, nil); err != nil {
			t.Fatal(err)
		}
	}
	relayCtx, relayCancel := context.WithCancel(ctx)
	t.Cleanup(relayCancel)
	relay := outbox.NewRelay(pool, kafka.Publisher{Client: mustProducer(t, cfg)})
	go func() { _ = relay.Run(relayCtx) }()

	consumer, err := kafka.NewConsumer(cfg, "orders-it",
		[]string{events.TopicOrderCommand}, kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(consumer.Close)

	// The real wallet service surface, wired exactly like the boot
	// harness mounts it (root → /internal/ → StripPrefix → RegisterRPC).
	winternal := http.NewServeMux()
	wallet.RegisterRPC(winternal, &wallet.RPC{DB: wpool, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	wroot := http.NewServeMux()
	wroot.Handle("/internal/", http.StripPrefix("/internal", winternal))
	wsrv := httptest.NewServer(wroot)
	t.Cleanup(wsrv.Close)

	market := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"market":"BTC/USDT","baseCoin":"BTC","quoteCoin":"USDT",
"zone":"main","sort":1,"featured":true,"baseScale":8,"quoteScale":2,
"tickSize":"0.1","lotSize":"0.0001"}]`))
	}))
	t.Cleanup(market.Close)

	exchange := &exchangeStub{current: []byte(`[]`), history: []byte(`[]`)}
	exsrv := httptest.NewServer(exchange)
	t.Cleanup(exsrv.Close)

	env := &APIEnv{
		CodeTTL: time.Minute, CodeCooldown: 100 * time.Millisecond,
		MaxCodeAttempts: 2, LockoutThreshold: 3, LockoutWindow: time.Minute,
		SessionTTL: time.Hour,
	}
	mailer := &CaptureMailer{}

	root := http.NewServeMux()
	member := &memberAPI{pool: pool, env: env, mailer: mailer,
		ob: outbox.New(pool), log: testLogger()}
	member.RegisterMember(root)
	orders := newOrderAPI(pool, env, outbox.New(pool), testLogger(), OrderConfig{
		WalletBaseURL: wsrv.URL + "/internal", MarketBaseURL: market.URL + "/internal",
		ExchangeBaseURL: exsrv.URL + "/internal",
		MarketCacheTTL:  time.Second,
	})
	orders.RegisterOrders(root)
	rest := httptest.NewServer(root)
	t.Cleanup(rest.Close)

	return &ordersStack{
		t: t, pool: pool, wpool: wpool, cfg: cfg, broker: broker, consumer: consumer,
		relay: relay, mailer: mailer, market: market, exchange: exchange,
		wallet: wsrv, rest: rest,
	}
}

func mustProducer(t *testing.T, cfg config.Kafka) *kgo.Client {
	t.Helper()
	producer, err := kafka.NewProducer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(producer.Close)
	return producer
}

// exchangeStub stands in for the exchange service's member-order RPC
// (ticket #41): canned bodies served verbatim so the proxy's
// byte-for-byte promise is assertable, with the received query
// captured and a failure knob.
type exchangeStub struct {
	mu       sync.Mutex
	current  []byte
	history  []byte
	fail     bool
	gotQuery url.Values
}

func (e *exchangeStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	e.mu.Lock()
	e.gotQuery = r.URL.Query()
	fail, cur, hist := e.fail, e.current, e.history
	e.mu.Unlock()
	if fail {
		http.Error(w, "boom", http.StatusInternalServerError)
		return
	}
	var body []byte
	switch r.URL.Path {
	case "/internal/orders/current":
		body = cur
	case "/internal/orders/history":
		body = hist
	default:
		http.Error(w, "unknown path", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// ---- stack helpers ----

func (s *ordersStack) post(path string, body any, token string) (int, map[string]any) {
	s.t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		s.t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, s.rest.URL+path, bytes.NewReader(raw))
	if err != nil {
		s.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("access-auth-token", token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func (s *ordersStack) getRaw(path, token string) (int, string, http.Header) {
	s.t.Helper()
	req, err := http.NewRequest(http.MethodGet, s.rest.URL+path, nil)
	if err != nil {
		s.t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("access-auth-token", token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.t.Fatal(err)
	}
	return resp.StatusCode, string(body), resp.Header
}

// register drives request-code → capture → register and returns the
// session token and the member id.
func (s *ordersStack) register(email string) (token, memberID string) {
	s.t.Helper()
	if status, body := s.post("/api/uc/register/code", map[string]string{"email": email}, ""); status != http.StatusOK {
		s.t.Fatalf("request code: status %d body %v", status, body)
	}
	var code string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, m := range s.mailer.Mail() {
			if m.To != email {
				continue
			}
			if match := codeRe.FindStringSubmatch(m.Body); match != nil {
				code = match[1]
			}
		}
		if code != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if code == "" {
		s.t.Fatal("no captured registration code")
	}
	status, body := s.post("/api/uc/register", map[string]string{
		"email": email, "code": code, "password": "correct horse battery staple"}, "")
	if status != http.StatusOK {
		s.t.Fatalf("register: status %d body %v", status, body)
	}
	member, _ := body["member"].(map[string]any)
	id, _ := member["id"].(string)
	return body["token"].(string), id
}

// fund credits a member's Ledger account through the wallet's own
// faucet (the Ledger-only adjustment, spec #22's test pattern).
func (s *ordersStack) fund(memberID, coin, amount string) {
	s.t.Helper()
	if err := wallet.Credit(context.Background(), s.wpool, memberID, coin,
		wallet.KindAdjustment, "test faucet", "it-faucet:"+uuid.NewString(),
		money.MustParse(amount), "", "it"); err != nil {
		s.t.Fatal(err)
	}
}

// walletAccount reads one coin's balances through the wallet's
// accounts RPC — the served surface, never the table.
func (s *ordersStack) walletAccount(memberID, coin string) (available, frozen string) {
	s.t.Helper()
	resp, err := http.Get(s.wallet.URL + "/internal/accounts/" + memberID)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	var accounts []map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&accounts); err != nil {
		s.t.Fatal(err)
	}
	for _, a := range accounts {
		if a["coin"] == coin {
			return a["available"], a["frozen"]
		}
	}
	s.t.Fatalf("no %s account for %s", coin, memberID)
	return "", ""
}

// journalAmount returns the freeze journal entry's exact amount for the
// order (the wallet Journal, read through its RPC).
func (s *ordersStack) journalFreezeAmount(memberID, orderID string) string {
	s.t.Helper()
	resp, err := http.Get(s.wallet.URL + "/internal/journal/" + memberID)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	var entries []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		s.t.Fatal(err)
	}
	for _, e := range entries {
		if e["Kind"] == "freeze" && e["Ref"] == orderID {
			amount, _ := e["Amount"].(string)
			return amount
		}
	}
	s.t.Fatalf("no freeze journal entry for order %s", orderID)
	return ""
}

// commands consumes order.command until n commands arrived (the relay
// publishes after the api commit).
func (s *ordersStack) commands(n int) []events.OrderCommand {
	s.t.Helper()
	var out []events.OrderCommand
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && len(out) < n {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		fetches := s.consumer.PollRecords(ctx, 10)
		fetches.EachError(func(_ string, _ int32, err error) {
			if !errors.Is(err, context.DeadlineExceeded) {
				s.t.Fatal(err)
			}
		})
		for _, rec := range fetches.Records() {
			env, err := events.Decode(rec.Value)
			if err != nil {
				s.t.Fatalf("order.command envelope: %v", err)
			}
			var cmd events.OrderCommand
			if err := json.Unmarshal(env.Data, &cmd); err != nil {
				s.t.Fatalf("order.command payload: %v", err)
			}
			out = append(out, cmd)
		}
		cancel()
	}
	if len(out) < n {
		s.t.Fatalf("only %d order.command messages, want %d", len(out), n)
	}
	return out
}

// assertNoCommand fails if any order.command arrives within the wait.
func (s *ordersStack) assertNoCommand(wait time.Duration) {
	s.t.Helper()
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		fetches := s.consumer.PollRecords(ctx, 10)
		fetches.EachError(func(_ string, _ int32, err error) {
			if !errors.Is(err, context.DeadlineExceeded) {
				s.t.Fatal(err)
			}
		})
		if got := len(fetches.Records()); got != 0 {
			cancel()
			s.t.Fatalf("%d order.command messages, want none", got)
		}
		cancel()
	}
}

func (s *ordersStack) place(token string, order map[string]string) (int, map[string]any) {
	s.t.Helper()
	return s.post("/api/uc/orders", order, token)
}

// ---- the seam ----

// A valid limit buy is accepted (202), freezes the exact quote cost
// (price × amount, never floored), and carries the member's strings
// verbatim on order.command — trailing zeros preserved.
func TestPlaceLimitOrderFreezesQuoteCostAndPublishesCommand(t *testing.T) {
	s := newOrdersStack(t)
	token, memberID := s.register("taker@example.com")
	s.fund(memberID, "USDT", "10000")

	status, body := s.place(token, map[string]string{
		"market": "BTC/USDT", "side": "buy", "kind": "limit",
		"price": "50000.10", "amount": "0.0002",
	})
	if status != http.StatusAccepted {
		t.Fatalf("place = %d %v, want 202", status, body)
	}
	orderID, _ := body["orderId"].(string)
	if orderID == "" {
		t.Fatalf("place answer carries no orderId: %v", body)
	}

	cmd := s.commands(1)[0]
	want := events.OrderCommand{
		Op: "place", OrderID: orderID, Member: memberID, Market: "BTC/USDT",
		Side: "buy", Kind: "limit", Price: "50000.10", Amount: "0.0002",
	}
	if cmd != want {
		t.Fatalf("order.command = %+v, want %+v", cmd, want)
	}

	// The wallet holds the exact product 50000.10 × 0.0002 = 10.000020
	// (USDT renders at its scale 6), and the Journal records the same
	// amount on the freeze entry.
	avail, frozen := s.walletAccount(memberID, "USDT")
	if frozen != "10.000020" || avail != "9989.999980" {
		t.Fatalf("USDT = available %s frozen %s, want 9989.999980 / 10.000020", avail, frozen)
	}
	if got := s.journalFreezeAmount(memberID, orderID); got != "10.00002" {
		t.Fatalf("freeze journal amount = %s, want 10.00002", got)
	}
}

// A market sell freezes the base amount; a market buy freezes the
// amount itself, because the amount IS the quote budget (wire notes
// §3.4) — and the command carries the same budget string.
func TestPlaceMarketSellFreezesBaseAndMarketBuyFreezesBudget(t *testing.T) {
	s := newOrdersStack(t)
	token, memberID := s.register("both@example.com")
	s.fund(memberID, "BTC", "1")
	s.fund(memberID, "USDT", "100")

	status, body := s.place(token, map[string]string{
		"market": "BTC/USDT", "side": "sell", "kind": "market", "amount": "0.0002",
	})
	if status != http.StatusAccepted {
		t.Fatalf("market sell = %d %v, want 202", status, body)
	}
	sellID := body["orderId"].(string)
	_, frozen := s.walletAccount(memberID, "BTC")
	if frozen != "0.00020000" {
		t.Fatalf("BTC frozen after market sell = %s, want 0.00020000", frozen)
	}

	status, body = s.place(token, map[string]string{
		"market": "BTC/USDT", "side": "buy", "kind": "market", "amount": "10.50",
	})
	if status != http.StatusAccepted {
		t.Fatalf("market buy = %d %v, want 202", status, body)
	}

	cmds := s.commands(2)
	if cmds[0].Op != "place" || cmds[0].OrderID != sellID ||
		cmds[0].Side != "sell" || cmds[0].Kind != "market" ||
		cmds[0].Price != "" || cmds[0].Amount != "0.0002" {
		t.Fatalf("market sell command = %+v", cmds[0])
	}
	if cmds[1].Op != "place" || cmds[1].Side != "buy" || cmds[1].Kind != "market" ||
		cmds[1].Price != "" || cmds[1].Amount != "10.50" {
		t.Fatalf("market buy command = %+v", cmds[1])
	}
	avail, frozen := s.walletAccount(memberID, "USDT")
	if frozen != "10.500000" || avail != "89.500000" {
		t.Fatalf("USDT = available %s frozen %s, want 89.500000 / 10.500000", avail, frozen)
	}
}

// Bad input answers named 400s before anything is frozen or published.
func TestPlaceRejectsBadNumbersWithNothingFrozenOrPublished(t *testing.T) {
	s := newOrdersStack(t)
	token, memberID := s.register("bad@example.com")
	s.fund(memberID, "USDT", "10000")

	cases := []struct {
		name    string
		order   map[string]string
		status  int
		message string
	}{
		{"unknown market", map[string]string{
			"market": "DOGE/BTC", "side": "buy", "kind": "limit",
			"price": "1.0", "amount": "1"}, http.StatusBadRequest,
			"This market is not available for trading."},
		{"bad side", map[string]string{
			"market": "BTC/USDT", "side": "hold", "kind": "limit",
			"price": "1.0", "amount": "0.0002"}, http.StatusBadRequest,
			"The side must be buy or sell."},
		{"bad kind", map[string]string{
			"market": "BTC/USDT", "side": "buy", "kind": "stop",
			"price": "1.0", "amount": "0.0002"}, http.StatusBadRequest,
			"The order kind must be limit or market."},
		{"missing price", map[string]string{
			"market": "BTC/USDT", "side": "buy", "kind": "limit",
			"amount": "0.0002"}, http.StatusBadRequest,
			"A limit order needs a price."},
		{"price not a decimal", map[string]string{
			"market": "BTC/USDT", "side": "buy", "kind": "limit",
			"price": "50e3", "amount": "0.0002"}, http.StatusBadRequest,
			"The price must be a decimal number like 50000.10."},
		{"price zero", map[string]string{
			"market": "BTC/USDT", "side": "buy", "kind": "limit",
			"price": "0", "amount": "0.0002"}, http.StatusBadRequest,
			"The price must be greater than zero."},
		{"price off tick", map[string]string{
			"market": "BTC/USDT", "side": "buy", "kind": "limit",
			"price": "50000.11", "amount": "0.0002"}, http.StatusBadRequest,
			"The price 50000.11 must be a multiple of the tick size 0.1."},
		{"price over quote scale", map[string]string{
			"market": "BTC/USDT", "side": "buy", "kind": "limit",
			"price": "50000.111", "amount": "0.0002"}, http.StatusBadRequest,
			"The price 50000.111 has more decimal places than the quote scale 2."},
		{"amount not a decimal", map[string]string{
			"market": "BTC/USDT", "side": "buy", "kind": "limit",
			"price": "1.0", "amount": "1/2"}, http.StatusBadRequest,
			"The amount must be a decimal number like 0.0001."},
		{"amount off lot", map[string]string{
			"market": "BTC/USDT", "side": "buy", "kind": "limit",
			"price": "1.0", "amount": "0.00015"}, http.StatusBadRequest,
			"The amount 0.00015 must be a multiple of the lot size 0.0001."},
		{"amount over base scale", map[string]string{
			"market": "BTC/USDT", "side": "buy", "kind": "limit",
			"price": "1.0", "amount": "0.000000001"}, http.StatusBadRequest,
			"The amount 0.000000001 has more decimal places than the base scale 8."},
		{"market-buy budget over quote scale", map[string]string{
			"market": "BTC/USDT", "side": "buy", "kind": "market",
			"amount": "10.001"}, http.StatusBadRequest,
			"The budget 10.001 has more decimal places than the quote scale 2."},
		{"amount zero", map[string]string{
			"market": "BTC/USDT", "side": "sell", "kind": "market",
			"amount": "0"}, http.StatusBadRequest,
			"The amount must be greater than zero."},
	}
	for _, tc := range cases {
		status, body := s.place(token, tc.order)
		message, _ := body["message"].(string)
		if status != tc.status || message != tc.message {
			t.Errorf("%s: place = %d %q, want %d %q", tc.name, status, message, tc.status, tc.message)
		}
	}

	s.assertNoCommand(1200 * time.Millisecond)
	_, frozen := s.walletAccount(memberID, "USDT")
	if frozen != "0.000000" {
		t.Fatalf("USDT frozen after rejected orders = %s, want 0.000000", frozen)
	}
}

// Insufficient funds answers 409 by name, with nothing frozen beyond
// the balance and nothing published.
func TestPlaceInsufficientFundsIsRejectedWithNothingPublished(t *testing.T) {
	s := newOrdersStack(t)
	token, memberID := s.register("poor@example.com")
	s.fund(memberID, "USDT", "5") // freeze would need 10.00002

	status, body := s.place(token, map[string]string{
		"market": "BTC/USDT", "side": "buy", "kind": "limit",
		"price": "50000.10", "amount": "0.0002",
	})
	if status != http.StatusConflict {
		t.Fatalf("place = %d %v, want 409", status, body)
	}
	if message, _ := body["message"].(string); message != "You do not have enough available funds to place this order." {
		t.Fatalf("409 message = %q", message)
	}
	s.assertNoCommand(1200 * time.Millisecond)
	avail, frozen := s.walletAccount(memberID, "USDT")
	if avail != "5.000000" || frozen != "0.000000" {
		t.Fatalf("USDT = available %s frozen %s, want 5.000000 / 0.000000", avail, frozen)
	}
}

// An unreachable funds service is a named 503 and nothing is published
// (fail closed — never enqueue on an unverified freeze).
func TestPlaceFailsClosedWithoutFundsService(t *testing.T) {
	s := newOrdersStack(t)
	token, memberID := s.register("nowallet@example.com")
	s.fund(memberID, "USDT", "10000")
	s.wallet.Close()

	status, body := s.place(token, map[string]string{
		"market": "BTC/USDT", "side": "buy", "kind": "limit",
		"price": "50000.10", "amount": "0.0002",
	})
	if status != http.StatusServiceUnavailable {
		t.Fatalf("place = %d %v, want 503", status, body)
	}
	if message, _ := body["message"].(string); message != "The funds service is unavailable. Try again in a moment." {
		t.Fatalf("503 message = %q", message)
	}
	s.assertNoCommand(1200 * time.Millisecond)
}

// Market info is required for validation; without it placement fails
// closed rather than guessing (503, nothing published, nothing frozen).
func TestPlaceFailsClosedWithoutMarketInfo(t *testing.T) {
	s := newOrdersStack(t)
	token, memberID := s.register("nomarket@example.com")
	s.fund(memberID, "USDT", "10000")
	s.market.Close()

	status, body := s.place(token, map[string]string{
		"market": "BTC/USDT", "side": "buy", "kind": "limit",
		"price": "50000.10", "amount": "0.0002",
	})
	if status != http.StatusServiceUnavailable {
		t.Fatalf("place = %d %v, want 503", status, body)
	}
	if message, _ := body["message"].(string); message != "Market information is unavailable. Try again in a moment." {
		t.Fatalf("503 message = %q", message)
	}
	s.assertNoCommand(1200 * time.Millisecond)
	_, frozen := s.walletAccount(memberID, "USDT")
	if frozen != "0.000000" {
		t.Fatalf("USDT frozen = %s, want 0.000000 (validation runs before the freeze)", frozen)
	}
}

// Current and history proxy the exchange's answer byte-for-byte, the
// member identity comes from the session, paging rides along verbatim,
// and api's own bounds answer named 400s. An unhappy upstream is one
// named 503.
func TestOrdersListsProxyExchangeByteForByte(t *testing.T) {
	s := newOrdersStack(t)
	token, memberID := s.register("list@example.com")

	current := []byte(`[{"orderId":"o1","market":"BTC/USDT","side":"buy","kind":"limit",` +
		`"price":"50000.1","amount":"0.0002","filledAmount":"0.0001","turnover":"5.00001",` +
		`"status":"open","createdAt":"2026-10-13T10:00:00Z"}]`)
	history := []byte(`[{"orderId":"o2","market":"BTC/USDT","side":"sell","kind":"market",` +
		`"amount":"0.0002","filledAmount":"0.0002","turnover":"10.00002","status":"filled",` +
		`"createdAt":"2026-10-12T09:00:00Z","canceledAt":"",` +
		`"fills":[{"seq":3,"price":"50000.1","amount":"0.0002","turnover":"10.00002",` +
		`"fee":"0.01000002","time":"2026-10-12T09:00:01Z"}]}]`)
	s.exchange.mu.Lock()
	s.exchange.current, s.exchange.history = current, history
	s.exchange.mu.Unlock()

	status, body, header := s.getRaw("/api/uc/orders/current", token)
	if status != http.StatusOK {
		t.Fatalf("current = %d %s, want 200", status, body)
	}
	if body != string(current) {
		t.Fatalf("current body = %s, want the exchange's bytes %s", body, current)
	}
	if ct := header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("current content type = %q", ct)
	}

	status, body, _ = s.getRaw("/api/uc/orders/history?limit=7&offset=3", token)
	if status != http.StatusOK || body != string(history) {
		t.Fatalf("history = %d %s, want 200 and the exchange's bytes", status, body)
	}
	s.exchange.mu.Lock()
	got := s.exchange.gotQuery.Get("member")
	s.exchange.mu.Unlock()
	if got != memberID {
		t.Fatalf("exchange saw member %q, want the session's %q", got, memberID)
	}

	// api's named bounds, before anything reaches the exchange.
	for _, q := range []string{
		"?limit=0", "?limit=1001", "?limit=abc", "?offset=-1", "?offset=x",
	} {
		status, body, _ = s.getRaw("/api/uc/orders/history"+q, token)
		if status != http.StatusBadRequest {
			t.Errorf("history %s = %d %s, want 400", q, status, body)
		}
		if !strings.Contains(body, `"message"`) {
			t.Errorf("history %s body = %s, want api's message envelope", q, body)
		}
	}

	// Anonymous callers never reach the exchange.
	if status, _, _ = s.getRaw("/api/uc/orders/current", ""); status != http.StatusUnauthorized {
		t.Fatalf("anonymous current = %d, want 401", status)
	}

	// A failing upstream is one named 503.
	s.exchange.mu.Lock()
	s.exchange.fail = true
	s.exchange.mu.Unlock()
	if status, body, _ = s.getRaw("/api/uc/orders/current", token); status != http.StatusServiceUnavailable ||
		!strings.Contains(body, "Order information is unavailable") {
		t.Fatalf("current with failed exchange = %d %s, want the named 503", status, body)
	}
}

// Cancel publishes the cancel command for the member's own order only;
// unknown ids and other members' orders answer 404 with nothing
// published.
func TestCancelPublishesCommandAndOnlyForOnesOwnOrders(t *testing.T) {
	s := newOrdersStack(t)
	token, memberID := s.register("owner@example.com")
	s.fund(memberID, "USDT", "10000")

	status, body := s.place(token, map[string]string{
		"market": "BTC/USDT", "side": "buy", "kind": "limit",
		"price": "50000.10", "amount": "0.0002",
	})
	if status != http.StatusAccepted {
		t.Fatalf("place = %d %v, want 202", status, body)
	}
	orderID := body["orderId"].(string)

	status, body = s.post("/api/uc/orders/"+orderID+"/cancel", nil, token)
	if status != http.StatusAccepted || body["orderId"] != orderID {
		t.Fatalf("cancel = %d %v, want 202 with the orderId", status, body)
	}

	cmds := s.commands(2)
	if cmds[0].Op != "place" {
		t.Fatalf("first command = %+v, want place", cmds[0])
	}
	if cmds[1] != (events.OrderCommand{
		Op: "cancel", OrderID: orderID, Member: memberID, Market: "BTC/USDT",
	}) {
		t.Fatalf("cancel command = %+v", cmds[1])
	}

	// Unknown id: 404, nothing published.
	status, body = s.post("/api/uc/orders/"+uuid.NewString()+"/cancel", nil, token)
	if status != http.StatusNotFound {
		t.Fatalf("cancel unknown = %d %v, want 404", status, body)
	}
	if message, _ := body["message"].(string); message != "Order not found." {
		t.Fatalf("404 message = %q", message)
	}

	// Another member's order: also 404, and no third command.
	otherToken, _ := s.register("other@example.com")
	status, _ = s.post("/api/uc/orders/"+orderID+"/cancel", nil, otherToken)
	if status != http.StatusNotFound {
		t.Fatalf("cancel someone else's order = %d, want 404", status)
	}
	s.assertNoCommand(1200 * time.Millisecond)
}
