//go:build integration

// Member order current/history RPC integration tests (ticket #41): the
// seams api fronts for the member REST (spec #24 story 30 — api holds no
// order-table copy to drift). The stack mints its own throwaway database
// (review checklist: a fixed name lets sibling IT suites pull the floor
// out from under each other on the shared dev Postgres), seeds orders
// through the real order.command path, funds and freezes both sides of
// every fill through a wallet-schema fixture (review checklist), and
// asserts only through the served /internal surface — never handler
// internals or table shapes.
package exchange

import (
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

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/syaikhipin/malxchange/internal/config"
	"github.com/syaikhipin/malxchange/internal/events"
	"github.com/syaikhipin/malxchange/internal/kafka"
	"github.com/syaikhipin/malxchange/internal/migrations"
	"github.com/syaikhipin/malxchange/internal/outbox"
	"github.com/syaikhipin/malxchange/internal/pgmig"
)

// ordersStack: throwaway database (exchange + wallet schemas) +
// in-process broker + engine wired for manual batch driving, with the
// member-order RPC mounted exactly like the boot harness mounts it. The
// wallet schema is fixture-only — the exchange engine never reads it
// (cross-schema access is denied by design); it exists so every fill
// rides a real funded hold behind both orders.
type ordersStack struct {
	pool     *pgxpool.Pool // search_path=exchange
	wallet   *pgxpool.Pool // search_path=wallet, fixture only
	cfg      config.Kafka
	producer *kgo.Client
	engine   *Engine
	consumer *kgo.Client
	rest     *httptest.Server
	t        *testing.T
}

func newOrdersStack(t *testing.T) *ordersStack {
	t.Helper()
	ctx := context.Background()

	// Unique throwaway DB per binary run, minted by the shared harness
	// helper: sibling tickets' IT suites share the dev Postgres (#35).
	dbName := mintITDB(t, "x")

	exURL := fmt.Sprintf("postgres://%s:%s@%s:5432/%s?sslmode=disable&search_path=exchange",
		os.Getenv("MAL_TEST_PG_SUPERUSER"), itPGPW, itPGHost, dbName)
	pool, err := pgxpool.New(ctx, exURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	_, _ = pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS exchange`)
	if err := pgmig.Run(ordersPGURL(t, dbName, "exchange"), migrations.FS, "exchange"); err != nil {
		t.Fatalf("exchange migrations: %v", err)
	}

	walletPool, err := pgxpool.New(ctx, fmt.Sprintf("postgres://%s:%s@%s:5432/%s?sslmode=disable&search_path=wallet",
		os.Getenv("MAL_TEST_PG_SUPERUSER"), itPGPW, itPGHost, dbName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(walletPool.Close)
	_, _ = walletPool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS wallet`)
	if err := pgmig.Run(ordersPGURL(t, dbName, "wallet"), migrations.FS, "wallet"); err != nil {
		t.Fatalf("wallet migrations: %v", err)
	}

	broker, err := kfake.NewCluster(kfake.NumBrokers(1))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(broker.Close)
	addrs := broker.ListenAddrs()
	if len(addrs) == 0 {
		t.Fatal("kfake gave no listen addresses")
	}
	cfg := config.Kafka{Seeds: []string{addrs[0]}} // no SASL against kfake
	producer, err := kafka.NewProducer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(producer.Close)
	if err := broker.CreateTopic(events.TopicOrderCommand, 1, nil); err != nil {
		t.Fatal(err)
	}

	s := &ordersStack{pool: pool, wallet: walletPool, cfg: cfg, producer: producer, t: t}
	s.engine = NewEngine(s.pool, slog.New(slog.NewTextHandler(os.Stderr, nil)), outbox.New(s.pool))
	if err := s.engine.Recover(ctx); err != nil {
		t.Fatalf("recover: %v", err)
	}
	s.newOrdersConsumer("orders-rpc")
	s.mountOrdersRPC()
	return s
}

func ordersPGURL(t *testing.T, db, schema string) string {
	t.Helper()
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(os.Getenv("MAL_TEST_PG_SUPERUSER"), itPGPW),
		Host:     itPGHost + ":5432",
		Path:     "/" + db,
		RawQuery: "sslmode=disable&search_path=" + schema,
	}
	return u.String()
}

// mountOrdersRPC mounts the member-order RPC exactly like the boot
// harness: a root mux stripping /internal onto the service-internal mux,
// so the tests exercise the production URL shape /internal/orders/….
func (s *ordersStack) mountOrdersRPC() {
	s.t.Helper()
	root := http.NewServeMux()
	internal := http.NewServeMux()
	root.Handle("/internal/", http.StripPrefix("/internal", internal))
	RegisterRPC(internal, &RPC{DB: s.pool})
	s.rest = httptest.NewServer(root)
	s.t.Cleanup(s.rest.Close)
}

func (s *ordersStack) newOrdersConsumer(group string) {
	s.t.Helper()
	c, err := kafka.NewConsumer(s.cfg, group, []string{events.TopicOrderCommand},
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		s.t.Fatal(err)
	}
	s.consumer = c
	s.t.Cleanup(c.Close)
}

func (s *ordersStack) ordersProduce(cmds ...events.OrderCommand) {
	s.t.Helper()
	ctx := context.Background()
	for i, c := range cmds {
		// Unique across calls: the inbox dedupes on (key, envelope id),
		// so a cancel of an order placed earlier must never reuse the
		// place's id.
		suffix := make([]byte, 4)
		if _, err := rand.Read(suffix); err != nil {
			s.t.Fatal(err)
		}
		envID := fmt.Sprintf("orpc-%s-%d-%s", c.OrderID, i, hex.EncodeToString(suffix))
		_, body, err := events.New(envID, c.Market, events.TopicOrderCommand, time.Now().UTC(), c)
		if err != nil {
			s.t.Fatal(err)
		}
		if err := s.producer.ProduceSync(ctx, &kgo.Record{
			Topic: events.TopicOrderCommand, Key: []byte(c.Market), Value: body,
		}).FirstErr(); err != nil {
			s.t.Fatal(err)
		}
	}
}

// ordersDrain applies polled batches until quiet (no records for a
// moment). Each poll gets its own short deadline: an idle PollRecords
// blocks until its context expires.
func (s *ordersStack) ordersDrain(t *testing.T) {
	t.Helper()
	quiet := 0
	for quiet < 3 {
		pollCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		fetches := s.consumer.PollRecords(pollCtx, 100)
		cancel()
		if countRecords(fetches) == 0 {
			quiet++
			continue
		}
		quiet = 0
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := applyFetches(ctx, s.engine, s.consumer, fetches)
		cancel()
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
	}
}

// creditAndFreeze puts a real hold behind a member's order side (review
// checklist): credit the Ledger account open-at-zero, then freeze what
// that side spends — the sold base amount for a sell, the quote Turnover
// (or budget) for a buy. Done before the commands that produce the fill.
func (s *ordersStack) creditAndFreeze(t *testing.T, member, coin, freeze string) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.wallet.Exec(ctx,
		`INSERT INTO ledger_accounts (member_id, coin) VALUES ($1, $2) ON CONFLICT (member_id, coin) DO NOTHING`,
		member, coin); err != nil {
		t.Fatal(err)
	}
	if _, err := s.wallet.Exec(ctx,
		`UPDATE ledger_accounts SET available = available + $3 WHERE member_id = $1 AND coin = $2`,
		member, coin, "1000000"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.wallet.Exec(ctx,
		`UPDATE ledger_accounts
		 SET available = available - $3, frozen = frozen + $3
		 WHERE member_id = $1 AND coin = $2`,
		member, coin, freeze); err != nil {
		t.Fatal(err)
	}
}

// ordersGet performs one internal REST GET and returns status + body.
func (s *ordersStack) ordersGet(t *testing.T, path string) (int, []byte) {
	t.Helper()
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(s.rest.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, body
}

func rpcPlace(id, member, side, kind, price, amount string) events.OrderCommand {
	return events.OrderCommand{Op: "place", OrderID: id, Member: member, Market: "BTC/USDT",
		Side: side, Kind: kind, Price: price, Amount: amount}
}

// nonEmpty is the sentinel for "key present, a non-empty string" (times).
const nonEmpty = "<nonempty>"

func asOrders(t *testing.T, status int, body []byte) []map[string]any {
	t.Helper()
	if status != http.StatusOK {
		t.Fatalf("GET = %d, want 200: %s", status, body)
	}
	var orders []map[string]any
	if err := json.Unmarshal(body, &orders); err != nil {
		t.Fatalf("response is not a JSON array: %v\n%s", err, body)
	}
	return orders
}

// checkOrder compares exactly the wanted keys; a nonEmpty want demands a
// non-empty string value (timestamps), anything else must match verbatim.
func checkOrder(t *testing.T, got map[string]any, want map[string]any) {
	t.Helper()
	for k, v := range want {
		gv, ok := got[k]
		if !ok {
			t.Errorf("order %v: key %q missing; got %v", got["orderId"], k, got)
			continue
		}
		if v == nonEmpty {
			s, isStr := gv.(string)
			if !isStr || s == "" {
				t.Errorf("order %v %s = %v, want a non-empty string", got["orderId"], k, gv)
			}
			continue
		}
		if gv != v {
			t.Errorf("order %v %s = %v, want %v", got["orderId"], k, gv, v)
		}
	}
}

func absent(t *testing.T, got map[string]any, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if _, ok := got[k]; ok {
			t.Errorf("order %v: key %q present, want absent: %v", got["orderId"], k, got)
		}
	}
}

// checkFills asserts the order's fills array, field by field, plus a
// positive fill id (seq) on every entry.
func checkFills(t *testing.T, order map[string]any, want []map[string]any) {
	t.Helper()
	raw, ok := order["fills"].([]any)
	if !ok {
		t.Fatalf("order %v fills = %v, want an array", order["orderId"], order["fills"])
	}
	if len(raw) != len(want) {
		t.Fatalf("order %v has %d fills, want %d: %v", order["orderId"], len(raw), len(want), raw)
	}
	for i, w := range want {
		f, ok := raw[i].(map[string]any)
		if !ok {
			t.Fatalf("fill %d = %v, want an object", i, raw[i])
		}
		checkOrder(t, f, w)
		if seq, ok := f["seq"].(float64); !ok || seq <= 0 {
			t.Errorf("fill %d seq = %v, want a positive id", i, f["seq"])
		}
	}
}

// TestMemberOrderCurrentHistorySplit: a resting maker sell crossed by a
// taker buy splits correctly — the partial sell stays current with its
// progress, the filled buy lands in history with its per-fill fee (the
// base-Coin buy fee), the later canceled sell lands in history with its
// own fee (the quote-Coin sell fee) — all decimal-exact strings.
func TestMemberOrderCurrentHistorySplit(t *testing.T) {
	s := newOrdersStack(t)

	// Real holds behind both sides of the fill (review checklist).
	s.creditAndFreeze(t, "alice", "BTC", "0.0002")
	s.creditAndFreeze(t, "bob", "USDT", "10.00002")
	s.ordersProduce(
		rpcPlace("m1", "alice", "sell", "limit", "50000.10", "0.0005"),
		rpcPlace("t1", "bob", "buy", "limit", "50000.10", "0.0002"),
	)
	s.ordersDrain(t)

	// Current: only the partially-filled maker sell rests, with its
	// accrued progress and no fills key (fills ride history only).
	code, body := s.ordersGet(t, "/internal/orders/current?member=alice")
	orders := asOrders(t, code, body)
	if len(orders) != 1 {
		t.Fatalf("alice current = %d orders, want 1: %s", len(orders), body)
	}
	checkOrder(t, orders[0], map[string]any{
		"orderId": "m1", "market": "BTC/USDT", "side": "sell", "kind": "limit",
		"price": "50000.1", "amount": "0.0005", "filledAmount": "0.0002",
		"turnover": "10", "status": "open", "createdAt": nonEmpty,
	})
	absent(t, orders[0], "fills", "canceledAt")

	// The fully-filled taker buy left current entirely.
	code, body = s.ordersGet(t, "/internal/orders/current?member=bob")
	if got := asOrders(t, code, body); len(got) != 0 {
		t.Fatalf("bob current = %d orders, want 0: %s", len(got), body)
	}

	// History: bob's filled buy carries its fill with HIS fee — the
	// base-Coin buy fee, exact: 0.0002 × 0.001 (taker).
	code, body = s.ordersGet(t, "/internal/orders/history?member=bob")
	orders = asOrders(t, code, body)
	if len(orders) != 1 {
		t.Fatalf("bob history = %d orders, want 1: %s", len(orders), body)
	}
	checkOrder(t, orders[0], map[string]any{
		"orderId": "t1", "market": "BTC/USDT", "side": "buy", "kind": "limit",
		"price": "50000.1", "amount": "0.0002", "filledAmount": "0.0002",
		"turnover": "10", "status": "filled", "createdAt": nonEmpty,
	})
	absent(t, orders[0], "canceledAt")
	checkFills(t, orders[0], []map[string]any{
		{"price": "50000.1", "amount": "0.0002", "turnover": "10",
			"fee": "0.0000002", "time": nonEmpty},
	})

	// alice has no history yet — an open order is not terminal.
	code, body = s.ordersGet(t, "/internal/orders/history?member=alice")
	if got := asOrders(t, code, body); len(got) != 0 {
		t.Fatalf("alice history = %d orders, want 0: %s", len(got), body)
	}

	// Canceling the partially-filled sell moves it to history: canceled,
	// with its fill and alice's fee — the quote-Coin sell fee
	// (10 × 0.001 maker).
	s.ordersProduce(events.OrderCommand{Op: "cancel", OrderID: "m1", Member: "alice", Market: "BTC/USDT"})
	s.ordersDrain(t)

	code, body = s.ordersGet(t, "/internal/orders/history?member=alice")
	orders = asOrders(t, code, body)
	if len(orders) != 1 {
		t.Fatalf("alice history = %d orders, want 1: %s", len(orders), body)
	}
	checkOrder(t, orders[0], map[string]any{
		"orderId": "m1", "market": "BTC/USDT", "side": "sell", "kind": "limit",
		"price": "50000.1", "amount": "0.0005", "filledAmount": "0.0002",
		"turnover": "10", "status": "canceled", "createdAt": nonEmpty,
		"canceledAt": nonEmpty,
	})
	checkFills(t, orders[0], []map[string]any{
		{"price": "50000.1", "amount": "0.0002", "turnover": "10",
			"fee": "0.01", "time": nonEmpty},
	})

	code, body = s.ordersGet(t, "/internal/orders/current?member=alice")
	if got := asOrders(t, code, body); len(got) != 0 {
		t.Fatalf("alice current after cancel = %d orders, want 0: %s", len(got), body)
	}

	// Two resting orders across markets: the pinned order (market, then
	// newest placement inside the Market) answers BTC/USDT before
	// ETH/USDT regardless of how the consumer grouped the placements —
	// deterministic, never map order.
	s.creditAndFreeze(t, "alice", "BTC", "0.0003")
	s.creditAndFreeze(t, "alice", "ETH", "0.05")
	s.ordersProduce(
		rpcPlace("m2", "alice", "sell", "limit", "50010.20", "0.0003"),
		events.OrderCommand{Op: "place", OrderID: "e1", Member: "alice", Market: "ETH/USDT",
			Side: "sell", Kind: "limit", Price: "300.00", Amount: "0.05"},
	)
	s.ordersDrain(t)

	code, body = s.ordersGet(t, "/internal/orders/current?member=alice")
	orders = asOrders(t, code, body)
	if len(orders) != 2 {
		t.Fatalf("alice current = %d orders, want 2: %s", len(orders), body)
	}
	checkOrder(t, orders[0], map[string]any{
		"orderId": "m2", "market": "BTC/USDT", "side": "sell", "kind": "limit",
		"price": "50010.2", "amount": "0.0003", "filledAmount": "0",
		"turnover": "0", "status": "open", "createdAt": nonEmpty,
	})
	checkOrder(t, orders[1], map[string]any{
		"orderId": "e1", "market": "ETH/USDT", "side": "sell", "kind": "limit",
		"price": "300", "amount": "0.05", "filledAmount": "0",
		"turnover": "0", "status": "open", "createdAt": nonEmpty,
	})
}

// TestMemberOrderHistoryPagingAndErrors: history pages through the
// terminal orders newest-placed first with limit/offset, an unfilled
// canceled order still carries its (empty) fills array, and bad member /
// limit / offset input gets the named errors on the wire.
func TestMemberOrderHistoryPagingAndErrors(t *testing.T) {
	s := newOrdersStack(t)

	// Five resting sells, canceled unfilled in the same command batch —
	// terminal history with no fills. Prices sit far from any buy.
	var cmds []events.OrderCommand
	for i := 1; i <= 5; i++ {
		cmds = append(cmds, rpcPlace(fmt.Sprintf("h%d", i), "carol", "sell",
			"limit", fmt.Sprintf("60000.%02d", (i-1)*10), "0.01"))
	}
	for i := 1; i <= 5; i++ {
		cmds = append(cmds, events.OrderCommand{Op: "cancel",
			OrderID: fmt.Sprintf("h%d", i), Member: "carol", Market: "BTC/USDT"})
	}
	s.ordersProduce(cmds...)
	s.ordersDrain(t)

	for _, tc := range []struct {
		path    string
		wantIDs string
	}{
		{"/internal/orders/history?member=carol", "h5,h4,h3,h2,h1"},
		{"/internal/orders/history?member=carol&limit=2", "h5,h4"},
		{"/internal/orders/history?member=carol&limit=2&offset=2", "h3,h2"},
		{"/internal/orders/history?member=carol&offset=4", "h1"},
		{"/internal/orders/history?member=carol&limit=1000", "h5,h4,h3,h2,h1"},
		{"/internal/orders/history?member=carol&offset=6", ""},
	} {
		code, body := s.ordersGet(t, tc.path)
		orders := asOrders(t, code, body)
		ids := make([]string, 0, len(orders))
		for _, o := range orders {
			fills, ok := o["fills"].([]any)
			if !ok {
				t.Errorf("%s: fills = %v, want an array (empty — nothing filled)", tc.path, o["fills"])
			} else if len(fills) != 0 {
				t.Errorf("%s: order %v fills = %v, want none", tc.path, o["orderId"], fills)
			}
			if o["status"] != "canceled" {
				t.Errorf("%s: order %v status = %v, want canceled", tc.path, o["orderId"], o["status"])
			}
			ids = append(ids, fmt.Sprint(o["orderId"]))
		}
		if got := strings.Join(ids, ","); got != tc.wantIDs {
			t.Errorf("%s ids = %q, want %q", tc.path, got, tc.wantIDs)
		}
	}

	// Bad input gets the named errors, JSON on the wire.
	for _, tc := range []struct {
		path    string
		wantErr string
	}{
		{"/internal/orders/current", "need member"},
		{"/internal/orders/current?member=", "need member"},
		{"/internal/orders/history", "need member"},
		{"/internal/orders/history?member=carol&limit=0", "limit must be an integer between 1 and 1000"},
		{"/internal/orders/history?member=carol&limit=1001", "limit must be an integer between 1 and 1000"},
		{"/internal/orders/history?member=carol&limit=many", "limit must be an integer between 1 and 1000"},
		{"/internal/orders/history?member=carol&offset=-1", "offset must be a non-negative integer"},
		{"/internal/orders/history?member=carol&offset=nope", "offset must be a non-negative integer"},
	} {
		code, body := s.ordersGet(t, tc.path)
		if code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400: %s", tc.path, code, body)
		}
		var e map[string]string
		if err := json.Unmarshal(body, &e); err != nil || e["error"] != tc.wantErr {
			t.Errorf("%s body = %s, want {\"error\": %q}", tc.path, body, tc.wantErr)
		}
	}
}
