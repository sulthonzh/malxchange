//go:build integration

// Market-copy integration tests (spec #23 ticket #33): market.updated
// converges market's own Market-config copy, and the internal visible
// listing GET /internal/markets serves it — through the production URL
// shape, real Postgres, in-process Kafka. Covers the ticket's acceptance
// seams: boot burst populates the copy, an operator config edit surfaces
// without a restart, redelivery and churn apply without duplicates or
// ghost rows, and a Market flipped invisible leaves the listing while its
// other endpoints keep answering (visibility gates the listing only).
package market

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/syaikhipin/malxchange/internal/events"
)

// itCfg builds a lawful market.updated payload with exchange-seed-like
// defaults; each test mutates the fields under test.
func itCfg(market, zone string, sort int) events.MarketUpdated {
	return events.MarketUpdated{
		Market:   market,
		BaseCoin: market[:3], QuoteCoin: "USDT",
		BaseScale: 8, QuoteScale: 2,
		TickSize: "0.1", LotSize: "0.0001",
		MakerFeeRate: "0.001", TakerFeeRate: "0.001",
		MinPrice: "", MaxPrice: "", // unbounded
		MinAmount: "0", MaxAmount: "", MinTurnover: "10",
		MarketOrdersEnabled: true,
		Enabled:             true,
		Visible:             true,
		Sort:                sort, Zone: zone, Featured: false,
	}
}

// produceCfg builds and produces one market.updated envelope, keyed by the
// Market symbol like every Market-ordered event.
func (s *itStack) produceCfg(t *testing.T, id string, m events.MarketUpdated) {
	t.Helper()
	_, body, err := events.New(id, m.Market, events.TopicMarketUpdated, time.Now().UTC(), m)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.producer.ProduceSync(context.Background(), &kgo.Record{
		Topic: events.TopicMarketUpdated, Key: []byte(m.Market), Value: body,
	}).FirstErr(); err != nil {
		t.Fatal(err)
	}
}

// marketRow is the full copy as it lands in market's own schema — the
// convergence seam: at rest, one row per Market, equal to its last
// consumed market.updated event ("" = unbounded, mirroring the wire).
type marketRow struct {
	Market     string
	BaseCoin   string
	QuoteCoin  string
	BaseScale  int
	QuoteScale int
	TickSize   string
	LotSize    string
	MakerRate  string
	TakerRate  string
	MinPrice   string
	MaxPrice   string
	MinAmount  string
	MaxAmount  string
	MinTurnov  string
	MktOrders  bool
	Enabled    bool
	Visible    bool
	Sort       int
	Zone       string
	Featured   bool
	MaxOpen    *int32
	TTL        *int32
}

// readMarkets reads the whole copy keyed by Market (review checklist:
// keyed reads, never positional).
func readMarkets(t *testing.T, s *itStack) map[string]marketRow {
	t.Helper()
	rows, err := s.pool.Query(context.Background(), `
		SELECT market, base_coin, quote_coin, base_scale, quote_scale,
		       tick_size::text, lot_size::text,
		       maker_fee_rate::text, taker_fee_rate::text,
		       COALESCE(min_price::text, ''), COALESCE(max_price::text, ''),
		       COALESCE(min_amount::text, ''), COALESCE(max_amount::text, ''),
		       COALESCE(min_turnover::text, ''),
		       market_orders_enabled, enabled, visible, sort, zone, featured,
		       max_open_orders, ttl_seconds
		FROM markets`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := make(map[string]marketRow)
	for rows.Next() {
		var r marketRow
		if err := rows.Scan(&r.Market, &r.BaseCoin, &r.QuoteCoin, &r.BaseScale, &r.QuoteScale,
			&r.TickSize, &r.LotSize, &r.MakerRate, &r.TakerRate,
			&r.MinPrice, &r.MaxPrice, &r.MinAmount, &r.MaxAmount, &r.MinTurnov,
			&r.MktOrders, &r.Enabled, &r.Visible, &r.Sort, &r.Zone, &r.Featured,
			&r.MaxOpen, &r.TTL); err != nil {
			t.Fatal(err)
		}
		out[r.Market] = r
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// wantRow converts a payload into the row the copy must hold at rest.
func wantRow(m events.MarketUpdated) marketRow {
	return marketRow{
		Market: m.Market, BaseCoin: m.BaseCoin, QuoteCoin: m.QuoteCoin,
		BaseScale: m.BaseScale, QuoteScale: m.QuoteScale,
		TickSize: m.TickSize, LotSize: m.LotSize,
		MakerRate: m.MakerFeeRate, TakerRate: m.TakerFeeRate,
		MinPrice: m.MinPrice, MaxPrice: m.MaxPrice,
		MinAmount: m.MinAmount, MaxAmount: m.MaxAmount, MinTurnov: m.MinTurnover,
		MktOrders: m.MarketOrdersEnabled, Enabled: m.Enabled, Visible: m.Visible,
		Sort: m.Sort, Zone: m.Zone, Featured: m.Featured,
		MaxOpen: m.MaxOpenOrders, TTL: m.TTLSeconds,
	}
}

// rowUpdatedAt reads one Market's copy timestamp, so redelivery can prove
// the inbox skipped the write entirely.
func rowUpdatedAt(t *testing.T, s *itStack, market string) time.Time {
	t.Helper()
	var ts time.Time
	if err := s.pool.QueryRow(context.Background(),
		`SELECT updated_at FROM markets WHERE market = $1`, market).Scan(&ts); err != nil {
		t.Fatal(err)
	}
	return ts
}

// assertCopy asserts the copy at rest equals the last consumed event per
// Market, with nothing extra (no ghost rows).
func assertCopy(t *testing.T, s *itStack, want map[string]events.MarketUpdated) {
	t.Helper()
	got := readMarkets(t, s)
	if len(got) != len(want) {
		t.Fatalf("copy holds %d rows (%v), want %d", len(got), rowKeys(got), len(want))
	}
	for market, m := range want {
		g, ok := got[market]
		if !ok {
			t.Fatalf("copy missing Market %s (has %v)", market, rowKeys(got))
		}
		if w := wantRow(m); g != w {
			t.Errorf("copy of %s:\n got %+v\nwant %+v", market, g, w)
		}
	}
}

func rowKeys(m map[string]marketRow) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// getListing fetches and decodes the visible listing.
func getListing(t *testing.T, s *itStack) []MarketSummary {
	t.Helper()
	status, body := s.getREST(t, "/internal/markets")
	if status != http.StatusOK {
		t.Fatalf("GET /internal/markets = %d, want 200\n%s", status, body)
	}
	var listing []MarketSummary
	if err := json.Unmarshal(body, &listing); err != nil {
		t.Fatalf("listing is not a JSON array: %v\n%s", err, body)
	}
	return listing
}

// listingMarkets extracts the listing's Market order.
func listingMarkets(listing []MarketSummary) []string {
	out := make([]string, 0, len(listing))
	for _, m := range listing {
		out = append(out, m.Market)
	}
	return out
}

func assertOrder(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("listing = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("listing = %v, want %v", got, want)
		}
	}
}

// TestBootBurstConvergesCopyAndListing: the exchange boot burst (one
// market.updated per known Market) populates the copy exactly, and the
// visible listing serves it in zone/sort order with the Market as
// tie-break. Redelivery of the same envelopes (the at-least-once
// contract) changes nothing — inbox dedupe skips the write.
func TestBootBurstConvergesCopyAndListing(t *testing.T) {
	s := newStack(t)
	s.newConsumer("cfg-burst")

	btc := itCfg("BTC/USDT", "main", 1)
	btc.BaseCoin, btc.Featured = "BTC", true
	eth := itCfg("ETH/USDT", "main", 2)
	eth.BaseCoin, eth.TickSize, eth.LotSize = "ETH", "0.01", "0.001"
	sol := itCfg("SOL/USDT", "innovation", 0)
	sol.BaseCoin = "SOL"

	s.produceCfg(t, "cfg-burst-btc", btc)
	s.produceCfg(t, "cfg-burst-eth", eth)
	s.produceCfg(t, "cfg-burst-sol", sol)
	s.drain(t)

	want := map[string]events.MarketUpdated{"BTC/USDT": btc, "ETH/USDT": eth, "SOL/USDT": sol}
	assertCopy(t, s, want)

	// The visible listing: zone lexicographic ("innovation" < "main"),
	// then sort, then symbol — SOL first despite sort 0.
	listing := getListing(t, s)
	assertOrder(t, listingMarkets(listing), []string{"SOL/USDT", "BTC/USDT", "ETH/USDT"})
	btcRow := listing[1]
	if btcRow.TickSize != "0.1" || btcRow.LotSize != "0.0001" ||
		btcRow.BaseScale != 8 || btcRow.QuoteScale != 2 ||
		btcRow.Zone != "main" || btcRow.Sort != 1 || !btcRow.Featured {
		t.Errorf("BTC/USDT listing row = %+v", btcRow)
	}

	// Redelivery: identical envelopes, same IDs. The inbox drops them, so
	// the copy neither grows nor even rewrites its rows.
	before := rowUpdatedAt(t, s, "BTC/USDT")
	s.produceCfg(t, "cfg-burst-btc", btc)
	s.produceCfg(t, "cfg-burst-eth", eth)
	s.produceCfg(t, "cfg-burst-sol", sol)
	s.drain(t)
	assertCopy(t, s, want)
	if after := rowUpdatedAt(t, s, "BTC/USDT"); !after.Equal(before) {
		t.Errorf("redelivery rewrote the copy: updated_at %v -> %v", before, after)
	}
}

// TestConfigEditSurfacesWithoutRestart: one consumer process, running —
// an operator's config edit (exchange republishes the full row within its
// config-watch cadence) lands in the copy and the listing with no restart
// and no duplicate or ghost row.
func TestConfigEditSurfacesWithoutRestart(t *testing.T) {
	s := newStack(t)
	s.newConsumer("cfg-edit")

	btc := itCfg("BTC/USDT", "main", 1)
	btc.BaseCoin = "BTC"
	s.produceCfg(t, "cfg-edit-1", btc)
	s.drain(t)
	if listing := getListing(t, s); listing[0].TickSize != "0.1" || listing[0].Sort != 1 {
		t.Fatalf("pre-edit listing row = %+v", listing[0])
	}

	// The edit: tick size, sort, and featured all change; unbounded
	// min_price gains a collar. Fresh envelope ID, same Market key.
	btc.TickSize = "0.2"
	btc.Sort = 7
	btc.Featured = true
	btc.MinPrice = "1000"
	s.produceCfg(t, "cfg-edit-2", btc)
	s.drain(t) // same process, same group — nothing restarted

	listing := getListing(t, s)
	assertOrder(t, listingMarkets(listing), []string{"BTC/USDT"})
	row := listing[0]
	if row.TickSize != "0.2" || row.Sort != 7 || !row.Featured {
		t.Errorf("post-edit listing row = %+v", row)
	}
	assertCopy(t, s, map[string]events.MarketUpdated{"BTC/USDT": btc})
}

// TestInvisibleMarketDropsFromListingKeepsServing: a Market flipped
// invisible leaves the visible listing but its row stays in the copy and
// its trades and Depth keep answering — no endpoint gates on visibility.
func TestInvisibleMarketDropsFromListingKeepsServing(t *testing.T) {
	s := newStack(t)
	s.newConsumer("cfg-flip")

	btc := itCfg("BTC/USDT", "main", 1)
	btc.BaseCoin = "BTC"
	eth := itCfg("ETH/USDT", "innovation", 0)
	eth.BaseCoin, eth.TickSize, eth.LotSize = "ETH", "0.01", "0.001"
	s.produceCfg(t, "cfg-flip-btc", btc)
	s.produceCfg(t, "cfg-flip-eth", eth)

	// ETH trades and publishes Depth before the flip, with real funded
	// holds behind both orders (review checklist). ETH and USDT are seeded
	// Coins in the wallet schema, so the holds are real rows.
	t0 := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	s.fundAndFreeze(t, "alice", "bob", "ETH", "USDT", "2", "20")
	s.produceFill(t, "fill-eth-1", t0, events.TradeFill{
		Market: "ETH/USDT", Seq: 1,
		BuyOrderID: "ord-b", SellOrderID: "ord-a",
		BuyMemberID: "bob", SellMemberID: "alice",
		TakerSide:   "buy",
		Price:       "10.01", Amount: "2", Turnover: "20.02",
		BuyTurnover: "20.02", BuyFee: "0.000002", SellFee: "0.0200002",
	})
	s.producePlate(t, "plate-eth-1", t0, events.Plate{
		Market: "ETH/USDT",
		Bids:   []events.PlateLevel{{Price: "10.00", Amount: "5"}},
		Asks:   []events.PlateLevel{{Price: "10.02", Amount: "3"}},
	})
	s.drain(t)

	assertOrder(t, listingMarkets(getListing(t, s)), []string{"ETH/USDT", "BTC/USDT"})

	// The flip: ETH goes invisible (fresh envelope, same Market key).
	eth.Visible = false
	s.produceCfg(t, "cfg-flip-eth-2", eth)
	s.drain(t)

	// Out of the listing, row kept (no ghost deletion)…
	listing := getListing(t, s)
	assertOrder(t, listingMarkets(listing), []string{"BTC/USDT"})
	got := readMarkets(t, s)
	if len(got) != 2 || got["ETH/USDT"].Visible {
		t.Errorf("copy after flip = %d rows, ETH visible = %v", len(got), got["ETH/USDT"].Visible)
	}

	// …while its trades and Depth keep answering.
	status, body := s.getREST(t, "/internal/trades/ETH/USDT")
	if status != http.StatusOK {
		t.Fatalf("GET /internal/trades/ETH/USDT = %d, want 200\n%s", status, body)
	}
	var trades []Trade
	if err := json.Unmarshal(body, &trades); err != nil {
		t.Fatalf("trades not a JSON array: %v\n%s", err, body)
	}
	if len(trades) != 1 || trades[0].ID != "fill-eth-1" {
		t.Errorf("invisible-Market trades = %+v", trades)
	}
	status, body = s.getREST(t, "/internal/depth/ETH/USDT")
	if status != http.StatusOK {
		t.Fatalf("GET /internal/depth/ETH/USDT = %d, want 200\n%s", status, body)
	}
	var depth Depth
	if err := json.Unmarshal(body, &depth); err != nil {
		t.Fatalf("depth not JSON: %v\n%s", err, body)
	}
	if len(depth.Bids) != 1 || len(depth.Asks) != 1 {
		t.Errorf("invisible-Market depth = %+v", depth)
	}
}
