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
	"slices"
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
// Market symbol like every Market-ordered event. The instant rides the
// envelope: the copy's updated_at is a pure function of the event stream
// (ticket #34's rebuild determinism), never the consumer's wall clock.
func (s *itStack) produceCfg(t *testing.T, id string, at time.Time, m events.MarketUpdated) {
	t.Helper()
	_, body, err := events.New(id, m.Market, events.TopicMarketUpdated, at, m)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.producer.ProduceSync(context.Background(), &kgo.Record{
		Topic: events.TopicMarketUpdated, Key: []byte(m.Market), Value: body,
	}).FirstErr(); err != nil {
		t.Fatal(err)
	}
}

// wantSummary converts one payload into the listing row the visible
// listing must serve for it (spec #23: tests assert what members/admins
// observe through the api-proxied endpoints, never the derived tables'
// internal shape).
func wantSummary(m events.MarketUpdated) MarketSummary {
	return MarketSummary{
		Market: m.Market, BaseCoin: m.BaseCoin, QuoteCoin: m.QuoteCoin,
		Zone: m.Zone, Sort: m.Sort, Featured: m.Featured,
		BaseScale: m.BaseScale, QuoteScale: m.QuoteScale,
		TickSize: m.TickSize, LotSize: m.LotSize,
	}
}

// assertListing asserts the visible listing equals want, row for row in
// the served order (zone, then sort, then the Market tie-break) — extra
// or missing Markets fail by name, no ghost rows in between.
func assertListing(t *testing.T, s *itStack, want []MarketSummary) {
	t.Helper()
	got := getListing(t, s)
	if !slices.Equal(got, want) {
		t.Fatalf("listing:\n got %+v\nwant %+v", got, want)
	}
}

// rowUpdatedAt reads one Market's copy timestamp, so redelivery can prove
// the inbox skipped the write entirely. Rebuild tripwire: updated_at is
// invisible on every REST surface, yet only this read distinguishes a
// deduped redelivery from a silent rewrite.
func rowUpdatedAt(t *testing.T, s *itStack, market string) time.Time {
	t.Helper()
	var ts time.Time
	if err := s.pool.QueryRow(context.Background(),
		`SELECT updated_at FROM markets WHERE market = $1`, market).Scan(&ts); err != nil {
		t.Fatal(err)
	}
	return ts
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

	s.produceCfg(t, "cfg-burst-btc", time.Now().UTC(), btc)
	s.produceCfg(t, "cfg-burst-eth", time.Now().UTC(), eth)
	s.produceCfg(t, "cfg-burst-sol", time.Now().UTC(), sol)
	s.drain(t)

	// The visible listing serves the copy exactly: zone lexicographic
	// ("innovation" < "main"), then sort, then symbol — SOL first despite
	// sort 0 — every row equal to its payload (spec #23: assert through
	// the api-proxied endpoint, never the derived tables).
	assertListing(t, s, []MarketSummary{wantSummary(sol), wantSummary(btc), wantSummary(eth)})

	// Redelivery: identical envelopes, same IDs. The inbox drops them, so
	// the listing neither grows nor even rewrites its rows.
	before := rowUpdatedAt(t, s, "BTC/USDT")
	s.produceCfg(t, "cfg-burst-btc", time.Now().UTC(), btc)
	s.produceCfg(t, "cfg-burst-eth", time.Now().UTC(), eth)
	s.produceCfg(t, "cfg-burst-sol", time.Now().UTC(), sol)
	s.drain(t)
	assertListing(t, s, []MarketSummary{wantSummary(sol), wantSummary(btc), wantSummary(eth)})
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
	s.produceCfg(t, "cfg-edit-1", time.Now().UTC(), btc)
	s.drain(t)
	assertListing(t, s, []MarketSummary{wantSummary(btc)})

	// The edit: tick size, sort, and featured all change; unbounded
	// min_price gains a collar. Fresh envelope ID, same Market key.
	btc.TickSize = "0.2"
	btc.Sort = 7
	btc.Featured = true
	btc.MinPrice = "1000"
	s.produceCfg(t, "cfg-edit-2", time.Now().UTC(), btc)
	s.drain(t) // same process, same group — nothing restarted

	// The listing row carries the edit, whole — no restart, no duplicate
	// or ghost row beside it.
	assertListing(t, s, []MarketSummary{wantSummary(btc)})
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
	s.produceCfg(t, "cfg-flip-btc", time.Now().UTC(), btc)
	s.produceCfg(t, "cfg-flip-eth", time.Now().UTC(), eth)

	// ETH trades and publishes Depth before the flip, with real funded
	// holds behind both orders (review checklist). ETH and USDT are seeded
	// Coins in the wallet schema, so the holds are real rows.
	t0 := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	s.fundAndFreeze(t, "alice", "bob", "ETH", "USDT", "2", "20")
	s.produceFill(t, "fill-eth-1", t0, events.TradeFill{
		Market: "ETH/USDT", Seq: 1,
		BuyOrderID: "ord-b", SellOrderID: "ord-a",
		BuyMemberID: "bob", SellMemberID: "alice",
		TakerSide: "buy",
		Price:     "10.01", Amount: "2", Turnover: "20.02",
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
	s.produceCfg(t, "cfg-flip-eth-2", time.Now().UTC(), eth)
	s.drain(t)

	// Out of the listing — its row left the api-proxied surface while
	// everything else about the Market keeps answering…
	assertListing(t, s, []MarketSummary{wantSummary(btc)})

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
