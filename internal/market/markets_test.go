package market

import (
	"testing"
	"time"

	"github.com/syaikhipin/malxchange/internal/events"
)

// cfgFixture is a lawful market.updated payload the way exchange publishes
// it (ticket #29): full self-contained Market row, decimal-string money,
// "" = unbounded on the collar and limit fields.
func cfgFixture() events.MarketUpdated {
	return events.MarketUpdated{
		Market: "BTC/USDT", BaseCoin: "BTC", QuoteCoin: "USDT",
		BaseScale: 8, QuoteScale: 2,
		TickSize: "0.1", LotSize: "0.0001",
		MakerFeeRate: "0.001", TakerFeeRate: "0.001",
		MinPrice: "", MaxPrice: "", // unbounded
		MinAmount: "0", MaxAmount: "", MinTurnover: "10",
		MarketOrdersEnabled: true,
		Enabled:             true,
		Visible:             true,
		Sort:                1, Zone: "main", Featured: true,
		MaxOpenOrders: nil, TTLSeconds: nil,
	}
}

func mustCfgEnvelope(t *testing.T, id string, m events.MarketUpdated) events.Envelope {
	t.Helper()
	_, body, err := events.New(id, m.Market, events.TopicMarketUpdated,
		time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC), m)
	if err != nil {
		t.Fatalf("test envelope invalid: %v", err)
	}
	return mustEnvelope(t, body)
}

// TestPayloadOfRoutesMarketUpdated: the market.updated envelope routes to
// the Market-config applier, keyed in the inbox by config + Market, so
// per-Market config ordering is total on the symbol record key.
func TestPayloadOfRoutesMarketUpdated(t *testing.T) {
	t.Parallel()
	d, err := payloadOf(mustCfgEnvelope(t, "cfg-1", cfgFixture()))
	if err != nil {
		t.Fatalf("market.updated rejected: %v", err)
	}
	if d.updated == nil || d.fill != nil || d.plate != nil {
		t.Errorf("market.updated routed to updated=%v fill=%v plate=%v",
			d.updated != nil, d.fill != nil, d.plate != nil)
	}
	if d.inboxKey() != "cfg:BTC/USDT" {
		t.Errorf("inbox key = %q, want cfg:BTC/USDT", d.inboxKey())
	}
	if d.updated.Market != "BTC/USDT" || d.updated.Zone != "main" {
		t.Errorf("payload mismatch: %+v", d.updated)
	}
}

// TestMarketListingOrder pins the visible listing's determinism promise:
// ordered by zone (lexicographic), then sort, then the Market as the
// tie-break — never map or database order (review checklist: keyed reads,
// stable publish order).
func TestMarketListingOrder(t *testing.T) {
	t.Parallel()
	got := []MarketSummary{
		{Market: "ETH/USDT", Zone: "main", Sort: 2},
		{Market: "SOL/USDT", Zone: "innovation", Sort: 0},
		{Market: "AAVE/USDT", Zone: "main", Sort: 1},
		{Market: "BTC/USDT", Zone: "main", Sort: 1},
		{Market: "DOGE/USDT", Zone: "main", Sort: 1},
	}
	want := []string{
		"SOL/USDT",                           // zone innovation — lexicographically before main
		"AAVE/USDT", "BTC/USDT", "DOGE/USDT", // zone main, sort 1 — symbol tie-break
		"ETH/USDT", // zone main, sort 2
	}
	sortMarkets(got)
	for i, m := range got {
		if m.Market != want[i] {
			t.Fatalf("position %d = %s, want %s (full order %v)", i, m.Market, want[i], markets(got))
		}
	}
}

func markets(ms []MarketSummary) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Market)
	}
	return out
}

// TestValidateMarketUpdated: payload-level contract enforcement keeps
// poisoned messages parked at the consumer, out of the transactions.
func TestValidateMarketUpdated(t *testing.T) {
	t.Parallel()
	if err := validateMarketUpdated(cfgFixture()); err != nil {
		t.Errorf("valid market.updated rejected: %v", err)
	}

	cases := []struct {
		name string
		mut  func(*events.MarketUpdated)
	}{
		{"empty market", func(m *events.MarketUpdated) { m.Market = "" }},
		{"empty base coin", func(m *events.MarketUpdated) { m.BaseCoin = "" }},
		{"empty quote coin", func(m *events.MarketUpdated) { m.QuoteCoin = "" }},
		{"empty zone", func(m *events.MarketUpdated) { m.Zone = "" }},
		{"negative base scale", func(m *events.MarketUpdated) { m.BaseScale = -1 }},
		{"non-decimal tick size", func(m *events.MarketUpdated) { m.TickSize = "0,1" }},
		{"non-decimal lot size", func(m *events.MarketUpdated) { m.LotSize = "lots" }},
		{"non-decimal maker fee", func(m *events.MarketUpdated) { m.MakerFeeRate = "free" }},
		{"non-decimal max price", func(m *events.MarketUpdated) { m.MaxPrice = "1e999" }},
		{"non-decimal min amount", func(m *events.MarketUpdated) { m.MinAmount = "-" }},
	}
	for _, tc := range cases {
		m := cfgFixture()
		tc.mut(&m)
		if err := validateMarketUpdated(m); err == nil {
			t.Errorf("%s: accepted", tc.name)
		}
	}
}
