//go:build integration

// Market consumer integration tests (spec #23 ticket #30): real Postgres +
// the in-process Kafka broker (kfake), modeled on the exchange consumer IT.
// Covers the projection discipline end to end: inbox-deduped consumption of
// trade.fill / market.plate in per-Market order, sanitized trade history and
// last-known Depth served at internal REST.
package market

import (
	"context"
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
	"github.com/syaikhipin/malxchange/internal/pgmig"
)

var (
	itPGHost = os.Getenv("MAL_TEST_PG_HOST")
	itPGPW   = os.Getenv("MAL_TEST_PG_SUPERUSER_PASSWORD")
)

func TestMain(m *testing.M) {
	if itPGHost == "" || itPGPW == "" {
		fmt.Println("skipping market integration tests: set MAL_TEST_PG_* (make test-integration)")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// itStack: throwaway database (market + wallet schemas) + in-process broker
// + consumer wired for manual batch driving (applyFetches is called
// directly, so "crash points" are expressible). The wallet schema exists so
// fixtures can fund and freeze both sides of every fill they produce
// (review checklist: unfunded fills distort balances silently).
type itStack struct {
	pool     *pgxpool.Pool // search_path=market
	wallet   *pgxpool.Pool // search_path=wallet, fixture only
	cfg      config.Kafka
	producer *kgo.Client
	broker   *kfake.Cluster
	consumer *kgo.Client
	rest     *httptest.Server
	t        *testing.T
}

func newStack(t *testing.T) *itStack {
	t.Helper()
	ctx := context.Background()

	admin, err := pgxpool.New(ctx, fmt.Sprintf("postgres://%s:%s@%s:5432/postgres",
		os.Getenv("MAL_TEST_PG_SUPERUSER"), itPGPW, itPGHost))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = admin.Exec(ctx, `DROP DATABASE IF EXISTS malxchange_itm WITH (FORCE)`)
	_, _ = admin.Exec(ctx, `CREATE DATABASE malxchange_itm`)
	admin.Close()

	mktURL := fmt.Sprintf("postgres://%s:%s@%s:5432/malxchange_itm?sslmode=disable&search_path=market",
		os.Getenv("MAL_TEST_PG_SUPERUSER"), itPGPW, itPGHost)
	pool, err := pgxpool.New(ctx, mktURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	_, _ = pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS market`)
	if err := pgmig.Run(pgURL(t, "market"), migrations.FS, "market"); err != nil {
		t.Fatalf("market migrations: %v", err)
	}

	// Wallet schema: fixture-only, so fills can carry a real funded hold
	// behind both orders. Market itself never reads it (cross-schema
	// access is denied by design).
	walletPool, err := pgxpool.New(ctx, fmt.Sprintf("postgres://%s:%s@%s:5432/malxchange_itm?sslmode=disable&search_path=wallet",
		os.Getenv("MAL_TEST_PG_SUPERUSER"), itPGPW, itPGHost))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(walletPool.Close)
	_, _ = walletPool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS wallet`)
	if err := pgmig.Run(pgURL(t, "wallet"), migrations.FS, "wallet"); err != nil {
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
	for _, topic := range []string{events.TopicTradeFill, events.TopicMarketPlate} {
		if err := broker.CreateTopic(topic, 1, nil); err != nil {
			t.Fatal(err)
		}
	}

	s := &itStack{pool: pool, wallet: walletPool, cfg: cfg, producer: producer, broker: broker, t: t}
	s.startREST()
	return s
}

func pgURL(t *testing.T, schema string) string {
	t.Helper()
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(os.Getenv("MAL_TEST_PG_SUPERUSER"), itPGPW),
		Host:     itPGHost + ":5432",
		Path:     "/malxchange_itm",
		RawQuery: "sslmode=disable&search_path=" + schema,
	}
	return u.String()
}

// startREST mounts the RPC exactly like the boot harness does: a root mux
// stripping /internal onto the service-internal mux (service.go wiring), so
// the tests exercise the production URL shape /internal/trades/{market...}.
func (s *itStack) startREST() {
	s.t.Helper()
	root := http.NewServeMux()
	internal := http.NewServeMux()
	root.Handle("/internal/", http.StripPrefix("/internal", internal))
	RegisterRPC(internal, &RPC{DB: s.pool})
	s.rest = httptest.NewServer(root)
	s.t.Cleanup(s.rest.Close)
}

func (s *itStack) newConsumer(group string) {
	s.t.Helper()
	c, err := kafka.NewConsumer(s.cfg, group, []string{events.TopicTradeFill, events.TopicMarketPlate},
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		s.t.Fatal(err)
	}
	s.consumer = c
	s.t.Cleanup(c.Close)
}

// drain applies polled batches until quiet (no records for a moment).
func (s *itStack) drain(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	quiet := 0
	for quiet < 3 {
		fetches := s.consumer.PollRecords(ctx, 100)
		n := countRecords(fetches)
		if n == 0 {
			quiet++
			continue
		}
		quiet = 0
		if err := applyFetches(ctx, s.pool, slog.New(slog.NewTextHandler(os.Stderr, nil)), s.consumer, fetches); err != nil {
			t.Fatalf("apply: %v", err)
		}
	}
}

// crashAfter: applies one poll batch but SKIPS the offset commit — the
// crash window between the Postgres commit and the offset commit.
func (s *itStack) crashAfter(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		fetches := s.consumer.PollRecords(ctx, 100)
		if countRecords(fetches) == 0 {
			return
		}
		if err := applyFetches(ctx, s.pool, slog.New(slog.NewTextHandler(os.Stderr, nil)), s.consumer, fetches); err != nil {
			t.Fatalf("apply: %v", err)
		}
		// offsets NOT committed: simulate process death right after the tx
	}
}

// countRecords counts without consuming the one-shot RecordsAll iterator.
func countRecords(fs kgo.Fetches) int {
	n := 0
	for _, f := range fs {
		for _, tp := range f.Topics {
			n += len(tp.Partitions[0].Records)
		}
	}
	return n
}

// fundAndFreeze puts a real hold behind both orders of a fill (review
// checklist): credit each member open-at-zero, then freeze what that side
// spends — the seller the base amount, the buyer the quote Turnover.
func (s *itStack) fundAndFreeze(t *testing.T, seller, buyer, base, quote, amount, turnover string) {
	t.Helper()
	ctx := context.Background()
	for _, mv := range []struct {
		member, coin, credit, freeze string
	}{
		{seller, base, "1000", amount},
		{buyer, quote, "1000000", turnover},
	} {
		if _, err := s.wallet.Exec(ctx,
			`INSERT INTO ledger_accounts (member_id, coin) VALUES ($1, $2) ON CONFLICT (member_id, coin) DO NOTHING`,
			mv.member, mv.coin); err != nil {
			t.Fatal(err)
		}
		if _, err := s.wallet.Exec(ctx,
			`UPDATE ledger_accounts SET available = available + $3 WHERE member_id = $1 AND coin = $2`,
			mv.member, mv.coin, mv.credit); err != nil {
			t.Fatal(err)
		}
		if _, err := s.wallet.Exec(ctx,
			`UPDATE ledger_accounts
			 SET available = available - $3, frozen = frozen + $3
			 WHERE member_id = $1 AND coin = $2`,
			mv.member, mv.coin, mv.freeze); err != nil {
			t.Fatal(err)
		}
	}
}

// produceFill builds and produces one trade.fill envelope. The envelope ID
// doubles as the fill ID clients dedupe on.
func (s *itStack) produceFill(t *testing.T, id string, at time.Time, f events.TradeFill) {
	t.Helper()
	_, body, err := events.New(id, f.Market, events.TopicTradeFill, at, f)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.producer.ProduceSync(context.Background(), &kgo.Record{
		Topic: events.TopicTradeFill, Key: []byte(f.Market), Value: body,
	}).FirstErr(); err != nil {
		t.Fatal(err)
	}
}

// getREST performs one internal REST GET and decodes the response body.
func (s *itStack) getREST(t *testing.T, path string) (int, []byte) {
	t.Helper()
	resp, err := http.Get(s.rest.URL + path)
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

// TestFillSequenceServesSanitizedTrades: a deterministic Fill sequence on
// trade.fold lands as a sanitized trade history at internal REST — fill ID,
// price, amount, turnover, taker side, time, and no Member/Order identifier
// anywhere on the wire or in the derived schema.
func TestFillSequenceServesSanitizedTrades(t *testing.T) {
	s := newStack(t)
	s.newConsumer("trades-1")

	// Fills on BTC/USDT: a resting maker sell crossed by two taker buys.
	t0 := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Second)
	fills := []events.TradeFill{
		{
			Market: "BTC/USDT", Seq: 1,
			BuyOrderID: "ord-taker-1", SellOrderID: "ord-maker-1",
			BuyMemberID: "bob", SellMemberID: "alice",
			TakerSide: "buy",
			Price:     "50000.10", Amount: "0.0002", Turnover: "10.00002",
			BuyTurnover: "10.00002", BuyFee: "0.0000002", SellFee: "0.01000002",
		},
		{
			Market: "BTC/USDT", Seq: 2,
			BuyOrderID: "ord-taker-1", SellOrderID: "ord-maker-1",
			BuyMemberID: "bob", SellMemberID: "alice",
			TakerSide: "buy",
			Price:     "50000.10", Amount: "0.0003", Turnover: "15.00003",
			BuyTurnover: "15.00003", BuyFee: "0.0000003", SellFee: "0.01500003",
		},
	}
	// Real holds behind both orders before each fill is produced.
	s.fundAndFreeze(t, "alice", "bob", "BTC", "USDT", "0.0002", "10.00002")
	s.produceFill(t, "fill-0001", t0, fills[0])
	s.fundAndFreeze(t, "alice", "bob", "BTC", "USDT", "0.0003", "15.00003")
	s.produceFill(t, "fill-0002", t1, fills[1])
	s.drain(t)

	// ... assertions below land with the handler (kept in this test).
	status, body := s.getREST(t, "/internal/trades/BTC/USDT")
	if status != http.StatusOK {
		t.Fatalf("GET /internal/trades/BTC/USDT = %d, want 200", status)
	}

	var trades []map[string]any
	if err := json.Unmarshal(body, &trades); err != nil {
		t.Fatalf("response is not a JSON array: %v\n%s", err, body)
	}
	if len(trades) != 2 {
		t.Fatalf("trades = %d entries, want 2\n%s", len(trades), body)
	}

	// Sanitized wire shape: exactly these keys, latest Fill first.
	wantKeys := []string{"id", "price", "amount", "turnover", "takerSide", "time"}
	want := []map[string]string{
		{"id": "fill-0002", "price": "50000.1", "amount": "0.0003", "turnover": "15.00003", "takerSide": "buy"},
		{"id": "fill-0001", "price": "50000.1", "amount": "0.0002", "turnover": "10.00002", "takerSide": "buy"},
	}
	wantTime := []string{t1.Format(time.RFC3339Nano), t0.Format(time.RFC3339Nano)}
	for i, tr := range trades {
		if len(tr) != len(wantKeys) {
			t.Errorf("trade %d has keys %v, want exactly %v", i, keysOf(tr), wantKeys)
		}
		for k, v := range want[i] {
			if got := tr[k]; got != v {
				t.Errorf("trade %d %s = %v, want %s", i, k, got, v)
			}
		}
		gotAt, err := time.Parse(time.RFC3339Nano, tr["time"].(string))
		if err != nil {
			t.Errorf("trade %d time %v is not RFC3339: %v", i, tr["time"], err)
		} else if !gotAt.Equal(mustTime(t, wantTime[i])) {
			t.Errorf("trade %d time = %v, want %v", i, tr["time"], wantTime[i])
		}
	}

	// No Member/Order identifier anywhere on the wire...
	for _, banned := range []string{"alice", "bob", "ord-maker-1", "ord-taker-1", "member", "orderId"} {
		if strings.Contains(string(body), banned) {
			t.Errorf("response body leaks %q:\n%s", banned, body)
		}
	}
	// ...nor in the derived schema.
	cols := schemaColumns(t, s.pool, "trades")
	for _, c := range cols {
		low := strings.ToLower(c)
		if strings.Contains(low, "member") || strings.Contains(low, "order") {
			t.Errorf("trades table carries identifier column %q", c)
		}
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("bad test time %q: %v", s, err)
	}
	return at
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func schemaColumns(t *testing.T, pool *pgxpool.Pool, table string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT column_name FROM information_schema.columns
		 WHERE table_schema = 'market' AND table_name = $1 ORDER BY column_name`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
	}
	return out
}
