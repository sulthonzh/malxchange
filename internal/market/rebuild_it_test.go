//go:build integration

// Rebuild drill integration tests (spec #23 ticket #34): a corrupted
// projection becomes a routine operation, never surgery. The drill IS the
// test (spec testing decision): derive from a deterministic event soup
// (Fills incl. out-of-order instants, side-wise partial Depth publishes
// with a side emptied, market.updated boot burst plus churn), capture the
// full market-data answer surface at internal REST, wipe mid-run through
// the internal endpoint while the ordinary consume loop stays live, and
// assert byte-identical answers after convergence — no dupes, no loss.
package market

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/syaikhipin/malxchange/internal/events"
	"github.com/syaikhipin/malxchange/internal/migrations"
	"github.com/syaikhipin/malxchange/internal/pgmig"
)

// drillCfg builds a lawful market.updated payload; ETH/BTC is the
// invisible Market — the listing drops it while its other endpoints keep
// answering (visibility gates the listing only, ticket #33). Its quote
// Coin is BTC, the one deviation from itCfg's USDT default (both Coins
// exist in the wallet fixture, so the Fills behind it stay fundable).
func drillCfg(market, zone string, sort int, visible bool) events.MarketUpdated {
	c := itCfg(market, zone, sort)
	c.Visible = visible
	if market == "ETH/BTC" {
		c.QuoteCoin = "BTC"
	}
	return c
}

// produceDrillSoup produces the deterministic event sequence every drill
// derives from and returns the exact counts the rebuild must reproduce:
// the Fills (trades may neither dupe nor lose one) and the total envelopes
// (the inbox at rest holds exactly one row per applied event). Instants
// ride a now-anchored base on whole-minute boundaries well inside the
// Ticker window, and are deliberately out of event order — Seq stays
// monotonic while OccurredAt jumps back and forth, the shape late Fills
// and replay share.
func (s *itStack) produceDrillSoup(t *testing.T) (fills, envelopes int) {
	t.Helper()
	base := time.Now().UTC().Truncate(time.Minute).Add(-3 * time.Hour)

	// market.updated: the exchange boot burst, then churn on BTC/USDT —
	// the copy must converge to the churned row.
	s.produceCfg(t, "drill-cfg-btc", base.Add(-30*time.Minute), drillCfg("BTC/USDT", "main", 1, true))
	s.produceCfg(t, "drill-cfg-eth", base.Add(-30*time.Minute), drillCfg("ETH/USDT", "innovation", 1, true))
	s.produceCfg(t, "drill-cfg-ethbtc", base.Add(-30*time.Minute), drillCfg("ETH/BTC", "main", 2, false))
	churned := drillCfg("BTC/USDT", "main", 5, true)
	churned.TickSize = "0.01"
	churned.Featured = true
	s.produceCfg(t, "drill-cfg-btc-2", base.Add(-2*time.Minute), churned)

	// Depth publishes: the full book, a one-side refresh, then a side
	// emptied — side-wise partial merges, never whole-book replaces.
	s.producePlate(t, "drill-plate-btc-1", base.Add(-10*time.Minute), events.Plate{
		Market: "BTC/USDT",
		Bids:   []events.PlateLevel{{Price: "50100", Amount: "0.001"}, {Price: "50000", Amount: "0.002"}},
		Asks:   []events.PlateLevel{{Price: "50200", Amount: "0.003"}, {Price: "50300", Amount: "0.004"}},
	})
	s.producePlate(t, "drill-plate-btc-2", base.Add(time.Hour), events.Plate{
		Market: "BTC/USDT",
		Asks:   []events.PlateLevel{{Price: "50250", Amount: "0.005"}}, // bids untouched
	})
	s.produceRawPlate(t, "drill-plate-btc-3", base.Add(2*time.Hour),
		`{"market":"BTC/USDT","bids":[]}`) // bids emptied, asks untouched
	s.producePlate(t, "drill-plate-eth-1", base.Add(-10*time.Minute), events.Plate{
		Market: "ETH/USDT",
		Bids:   []events.PlateLevel{{Price: "2990", Amount: "3"}},
		Asks:   []events.PlateLevel{{Price: "3010", Amount: "2"}},
	})
	s.producePlate(t, "drill-plate-ethbtc-1", base.Add(-5*time.Minute), events.Plate{
		Market: "ETH/BTC",
		Bids:   []events.PlateLevel{{Price: "0.049", Amount: "50"}},
		Asks:   []events.PlateLevel{{Price: "0.051", Amount: "60"}},
	})

	// Fills — every one funded and frozen on both sides (review
	// checklist). BTC/USDT carries the out-of-order instants.
	type drillFill struct {
		id, seller, buyer, side, price, amount, turnover string
		seq                                              uint64
		at                                               time.Duration // offset from base
	}
	btc := []drillFill{
		{"drill-btc-1", "alice", "bob", "buy", "50000.10", "0.0005", "25.00005", 1, 0},
		{"drill-btc-2", "alice", "bob", "sell", "49990", "0.0004", "19.996", 2, 90 * time.Second},
		{"drill-btc-3", "alice", "bob", "buy", "49500", "0.0006", "29.7", 3, -120 * time.Second},
		{"drill-btc-4", "alice", "bob", "buy", "50100", "0.0003", "15.03", 4, 3700 * time.Second},
		{"drill-btc-5", "alice", "bob", "sell", "50150", "0.0002", "10.03", 5, 3700 * time.Second},
		{"drill-btc-6", "alice", "bob", "buy", "50200", "0.0001", "5.02", 6, 7300 * time.Second},
		{"drill-btc-7", "alice", "bob", "sell", "49800", "0.0007", "34.86", 7, 45 * time.Second},
	}
	eth := []drillFill{
		{"drill-eth-1", "carol", "dan", "buy", "3000", "1", "3000", 1, 10 * time.Second},
		{"drill-eth-2", "carol", "dan", "sell", "3010", "2", "6020", 2, time.Hour},
	}
	ethbtc := []drillFill{
		{"drill-ethbtc-1", "erin", "frank", "buy", "0.05", "10", "0.5", 1, 20 * time.Second},
	}
	for _, set := range []struct {
		market, baseCoin, quoteCoin string
		fills                       []drillFill
	}{
		{"BTC/USDT", "BTC", "USDT", btc},
		{"ETH/USDT", "ETH", "USDT", eth},
		{"ETH/BTC", "ETH", "BTC", ethbtc},
	} {
		for _, f := range set.fills {
			s.fundAndFreeze(t, f.seller, f.buyer, set.baseCoin, set.quoteCoin, f.amount, f.turnover)
			s.produceFill(t, f.id, base.Add(f.at), events.TradeFill{
				Market: set.market, Seq: f.seq,
				BuyOrderID: "ord-taker", SellOrderID: "ord-maker",
				BuyMemberID: f.buyer, SellMemberID: f.seller,
				TakerSide: f.side,
				Price:     f.price, Amount: f.amount, Turnover: f.turnover,
			})
			fills++
		}
	}

	envelopes = fills + 5 /*plates*/ + 4 /*market.updated*/
	return fills, envelopes
}

// drillPaths enumerates the full market-data answer surface: the listing,
// the all-markets Tickers, and per Market (visible and invisible alike)
// the trades, Depth, Ticker and every stored Kline interval.
func drillPaths() []string {
	paths := []string{"/internal/markets", "/internal/tickers"}
	for _, m := range []string{"BTC/USDT", "ETH/USDT", "ETH/BTC"} {
		paths = append(paths,
			"/internal/trades/"+m+"?limit=1000",
			"/internal/depth/"+m,
			"/internal/ticker/"+m)
		for _, iv := range StoredIntervals {
			paths = append(paths, "/internal/kline/"+m+"?interval="+string(iv))
		}
	}
	return paths
}

// captureAnswers takes one full snapshot of the answer surface, raw
// bodies, so two snapshots compare byte for byte.
func (s *itStack) captureAnswers(t *testing.T) map[string][]byte {
	t.Helper()
	out := make(map[string][]byte, len(drillPaths()))
	for _, p := range drillPaths() {
		status, body := s.getREST(t, p)
		if status != http.StatusOK {
			t.Fatalf("GET %s: status %d, want 200 (body %s)", p, status, body)
		}
		out[p] = body
	}
	return out
}

// assertAnswersIdentical compares two snapshots byte for byte and names
// every diverging path — the drill's verdict.
func assertAnswersIdentical(t *testing.T, before, after map[string][]byte) {
	t.Helper()
	for _, p := range drillPaths() {
		b, a := before[p], after[p]
		if string(b) != string(a) {
			t.Errorf("answers diverged after rebuild: %s\nbefore=%s\n after=%s", p, b, a)
		}
	}
}

// waitAnswersStable polls until every path answers its captured body
// again (the replay converged) or the deadline passes.
func (s *itStack) waitAnswersStable(t *testing.T, want map[string][]byte, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		diverged := false
		for _, p := range drillPaths() {
			status, body := s.getREST(t, p)
			if status != http.StatusOK || string(body) != string(want[p]) {
				diverged = true
				break
			}
		}
		if !diverged {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("answers did not converge back to the captured snapshot within " + within.String())
}

// pinSoupAnswers hand-checks the FIRST snapshot against independently
// derived expectations — the drill's comparison is snapshot-vs-snapshot,
// so these pins are what prove the snapshot itself is lawful (a test that
// compared two corrupt answers would pass vacuously).
func (s *itStack) pinSoupAnswers(t *testing.T) {
	t.Helper()

	// Listing: visible Markets in zone/sort/Market order — innovation
	// before main; the invisible ETH/BTC is absent.
	var listing []MarketSummary
	s.decodeREST(t, "/internal/markets", &listing)
	got := make([]string, 0, len(listing))
	for _, m := range listing {
		got = append(got, m.Market)
	}
	assertOrder(t, got, []string{"ETH/USDT", "BTC/USDT"})
	for _, m := range listing {
		if m.Market == "BTC/USDT" && (m.Sort != 5 || !m.Featured || m.TickSize != "0.01") {
			t.Errorf("listing carries stale BTC/USDT config: %+v", m)
		}
	}

	// BTC/USDT Ticker, hand-folded from the soup: newest Fill 50200, high
	// 50200, low 49500, Turnover 139.63605.
	var tk events.Ticker
	s.decodeREST(t, "/internal/ticker/BTC/USDT", &tk)
	want := events.Ticker{Market: "BTC/USDT", Last: "50200", High: "50200", Low: "49500", Turnover: "139.63605"}
	if tk != want {
		t.Errorf("BTC/USDT ticker:\n got %+v\nwant %+v", tk, want)
	}

	// BTC/USDT Depth: the last publish emptied the bids and left the one
	// refreshed ask level.
	var d Depth
	s.decodeREST(t, "/internal/depth/BTC/USDT", &d)
	if len(d.Bids) != 0 || len(d.Asks) != 1 || d.Asks[0].Price != "50250" || d.Asks[0].Amount != "0.005" {
		t.Errorf("BTC/USDT depth:\n got %+v\nwant bids empty, asks [50250×0.005]", d)
	}

	// Trades answer newest first by (traded_at, fill id): the same-instant
	// pair orders by fill id, and the out-of-order old Fill sits last.
	var trades []Trade
	s.decodeREST(t, "/internal/trades/BTC/USDT?limit=1000", &trades)
	gotIDs := make([]string, 0, len(trades))
	for _, tr := range trades {
		gotIDs = append(gotIDs, tr.ID)
	}
	assertOrder(t, gotIDs, []string{
		"drill-btc-6", "drill-btc-5", "drill-btc-4", "drill-btc-2",
		"drill-btc-7", "drill-btc-1", "drill-btc-3",
	})

	// Kline 1m bars: the soup's instants span five distinct minute
	// buckets (the +0s and +45s Fills share one).
	var bars []Kline
	s.decodeREST(t, "/internal/kline/BTC/USDT?interval=1m", &bars)
	if len(bars) != 5 {
		t.Errorf("BTC/USDT 1m bars: got %d, want 5 (%+v)", len(bars), bars)
	}
}

// decodeREST GETs one path and decodes the body into v.
func (s *itStack) decodeREST(t *testing.T, path string, v any) {
	t.Helper()
	status, body := s.getREST(t, path)
	if status != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200 (body %s)", path, status, body)
	}
	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("GET %s: decode %s: %v", path, body, err)
	}
}

// postREST performs one internal POST and returns the status and body.
func (s *itStack) postREST(t *testing.T, path string) (int, []byte) {
	t.Helper()
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post(s.rest.URL+path, "", nil)
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

// countRows counts one whole table — the no-dupes/no-loss probe.
func (s *itStack) countRows(t *testing.T, table string) int {
	t.Helper()
	var n int
	if err := s.pool.QueryRow(context.Background(),
		fmt.Sprintf(`SELECT count(*) FROM %s`, table)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// waitTradesCount polls until one Market's trade list reaches want Fills
// (batch transactions write trades and every other derived row together,
// so this gate means the whole soup has landed).
func (s *itStack) waitTradesCount(t *testing.T, market string, want int) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		var trades []Trade
		s.decodeREST(t, "/internal/trades/"+market+"?limit=1000", &trades)
		if len(trades) == want {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("%s trades did not reach %d within 30s", market, want)
}

// startLiveLoop mounts the rebuild seam and runs the ordinary consume
// loop in the background — the drill's service-under-load. stop is
// run-once and verifies the loop exited cleanly.
func (s *itStack) startLiveLoop(t *testing.T) (stop func()) {
	t.Helper()
	proj := NewProjector(s.pool, slog.New(slog.NewTextHandler(os.Stderr, nil)), s.consumer)
	s.mountRPC(&RPC{DB: s.pool, Rebuild: proj})

	loopCtx, cancel := context.WithCancel(context.Background())
	loopDone := make(chan error, 1)
	go func() { loopDone <- proj.Consume(loopCtx) }()
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			if err := <-loopDone; err != nil {
				t.Errorf("consume loop: %v", err)
			}
		})
	}
}

// TestRebuildDrillRestoresIdenticalAnswers: the ticket's drill, end to
// end. The ordinary consume loop runs live; the internal endpoint wipes
// every derived row plus the inbox mid-run and rewinds the consumer group
// to the earliest offset; the SAME loop replays the retained events; the
// full answer surface comes back byte-identical; trades hold exactly one
// row per Fill and the inbox exactly one row per event (no dupes, no
// loss); and the service keeps answering fresh events afterwards — the
// rebuild never wedged it.
func TestRebuildDrillRestoresIdenticalAnswers(t *testing.T) {
	s := newStack(t)
	s.newConsumer("rebuild-drill-1")
	fills, envelopes := s.produceDrillSoup(t)

	stop := s.startLiveLoop(t)
	defer stop()

	// Converge on the soup, then pin the snapshot against hand-derived
	// expectations before trusting snapshot-vs-snapshot comparison.
	s.waitTradesCount(t, "BTC/USDT", 7)
	s.waitTradesCount(t, "ETH/USDT", 2)
	s.waitTradesCount(t, "ETH/BTC", 1)
	s.pinSoupAnswers(t)
	before := s.captureAnswers(t)
	if n := s.countRows(t, "trades"); n != fills {
		t.Fatalf("trades hold %d rows before the drill, want %d (one per Fill)", n, fills)
	}
	if n := s.countRows(t, "inbox"); n != envelopes {
		t.Fatalf("inbox holds %d rows before the drill, want %d (one per event)", n, envelopes)
	}

	// The wipe, mid-run, while the loop stays live.
	if status, body := s.postREST(t, "/internal/rebuild"); status != http.StatusNoContent {
		t.Fatalf("POST /internal/rebuild: status %d, want 204 (body %s)", status, body)
	}

	// The same ordinary loop replays the retained events to convergence.
	s.waitAnswersStable(t, before, 30*time.Second)
	after := s.captureAnswers(t)
	assertAnswersIdentical(t, before, after)
	if n := s.countRows(t, "trades"); n != fills {
		t.Errorf("trades hold %d rows after the rebuild, want %d — replayed Fills duped or lost", n, fills)
	}
	if n := s.countRows(t, "inbox"); n != envelopes {
		t.Errorf("inbox holds %d rows after the rebuild, want %d — replay double-applied", n, envelopes)
	}

	// Still live: a fresh funded Fill lands through the ordinary loop and
	// the Ticker moves with it.
	s.fundAndFreeze(t, "carol", "dan", "ETH", "USDT", "0.5", "1510")
	s.produceFill(t, "drill-live-1", time.Now().UTC(), events.TradeFill{
		Market: "ETH/USDT", Seq: 3,
		BuyOrderID: "ord-taker", SellOrderID: "ord-maker",
		BuyMemberID: "dan", SellMemberID: "carol",
		TakerSide: "buy", Price: "3020", Amount: "0.5", Turnover: "1510",
	})
	s.waitTradesCount(t, "ETH/USDT", 3)
	var tk events.Ticker
	s.decodeREST(t, "/internal/ticker/ETH/USDT", &tk)
	if tk.Last != "3020" {
		t.Errorf("ETH/USDT ticker last after the drill: %s, want 3020 (the loop must keep serving)", tk.Last)
	}
}

// migrateMarketDown rewinds the market schema's derived migrations (the
// three droppable ones — platform init 0001 stays), through the same
// embedded FS and pinned search_path the boot harness uses. Re-running
// them up afterwards is pgmig.Run — literally the production boot path,
// no manual steps.
func migrateMarketDown(t *testing.T, s *itStack, steps int) {
	t.Helper()
	src, err := iofs.New(migrations.FS, "market")
	if err != nil {
		t.Fatal(err)
	}
	dbURL := strings.Replace(pgURL(t, s.db, "market"), "postgres://", "pgx5://", 1)
	m, err := migrate.NewWithSourceInstance("iofs", src, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.Steps(steps); err != nil {
		t.Fatalf("migrate market down %d steps: %v", steps, err)
	}
}

// TestDroppedDerivedSchemaRebuildsIdentical: the spec's stronger drill —
// drop the derived schema from the harness (migrations down), boot it
// back (migrations up, the production runner), trigger the rebuild, and
// the answers come back identical. The migrations are droppable and
// re-runnable from scratch without manual steps.
func TestDroppedDerivedSchemaRebuildsIdentical(t *testing.T) {
	s := newStack(t)
	s.newConsumer("rebuild-drop-1")
	fills, envelopes := s.produceDrillSoup(t)
	s.drain(t)

	proj := NewProjector(s.pool, slog.New(slog.NewTextHandler(os.Stderr, nil)), s.consumer)
	s.mountRPC(&RPC{DB: s.pool, Rebuild: proj})

	s.pinSoupAnswers(t)
	before := s.captureAnswers(t)

	// Drop the derived schema: the endpoints fail honestly (500) while
	// the tables are gone — never stale answers.
	migrateMarketDown(t, s, -3)
	if status, _ := s.getREST(t, "/internal/trades/BTC/USDT"); status != http.StatusInternalServerError {
		t.Fatalf("GET trades with the derived schema dropped: status %d, want 500", status)
	}

	// Re-run from scratch through the production migration runner, then
	// rebuild: identical answers, exact counts.
	if err := pgmig.Run(pgURL(t, s.db, "market"), migrations.FS, "market"); err != nil {
		t.Fatalf("re-run market migrations up: %v", err)
	}
	if status, body := s.postREST(t, "/internal/rebuild"); status != http.StatusNoContent {
		t.Fatalf("POST /internal/rebuild: status %d, want 204 (body %s)", status, body)
	}
	s.drain(t)

	assertAnswersIdentical(t, before, s.captureAnswers(t))
	if n := s.countRows(t, "trades"); n != fills {
		t.Errorf("trades hold %d rows after drop+rebuild, want %d", n, fills)
	}
	if n := s.countRows(t, "inbox"); n != envelopes {
		t.Errorf("inbox holds %d rows after drop+rebuild, want %d", n, envelopes)
	}
}
