// The Market-config edit RPC's validation core (ticket #61): a wire
// edit folds onto the stored row and the merged result must be a whole
// valid config before anything touches the table. These are the pure
// fold rules — the served seam (persistence, freeze guard, live-apply)
// is the integration test's.
package exchange

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func seedCfg() MarketCfg {
	return MarketCfg{
		Symbol: "BTC/USDT", BaseCoin: "BTC", QuoteCoin: "USDT",
		BaseScale: 8, QuoteScale: 2,
		TickSize: "0.1", LotSize: "0.0001", TickRaw: 10, LotRaw: 10000,
		MakerFee: "0.001", TakerFee: "0.001",
		MinAmount: "0", MinTurnover: "5",
		MarketOrders: true, Enabled: true, Visible: true,
		Sort: 1, Zone: "main",
	}
}

func mustApply(t *testing.T, m MarketCfg, raw string) MarketCfg {
	t.Helper()
	var in marketUpdate
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
	next, err := m.applyUpdate(in)
	if err != nil {
		t.Fatalf("applyUpdate(%s): unexpected refusal %v", raw, err)
	}
	return next
}

func mustRefuse(t *testing.T, m MarketCfg, raw, wantFragment string) {
	t.Helper()
	var in marketUpdate
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
	_, err := m.applyUpdate(in)
	if !errors.Is(err, errBadMarketCfg) {
		t.Fatalf("applyUpdate(%s): want errBadMarketCfg, got %v", raw, err)
	}
	if wantFragment != "" && !strings.Contains(err.Error(), wantFragment) {
		t.Fatalf("applyUpdate(%s): error %q misses %q", raw, err, wantFragment)
	}
}

func TestApplyUpdateAcceptedFields(t *testing.T) {
	m := seedCfg()
	next := mustApply(t, m, `{"makerFeeRate":"0.002","takerFeeRate":"0.0035","enabled":false,"visible":false,
		"marketOrdersEnabled":false,"sort":7,"zone":"innovation","featured":true}`)
	if next.MakerFee != "0.002" || next.TakerFee != "0.0035" {
		t.Fatalf("fees not applied: %s/%s", next.MakerFee, next.TakerFee)
	}
	if next.Enabled || next.Visible || next.MarketOrders || !next.Featured {
		t.Fatalf("flags not applied: %+v", next)
	}
	if next.Sort != 7 || next.Zone != "innovation" {
		t.Fatalf("visibility fields not applied: sort=%d zone=%s", next.Sort, next.Zone)
	}
	// Untouched fields ride along unchanged.
	if next.TickSize != "0.1" || next.LotSize != "0.0001" || next.BaseCoin != "BTC" {
		t.Fatalf("unrelated fields moved: %+v", next)
	}
}

func TestApplyUpdateCollarSemantics(t *testing.T) {
	m := seedCfg()

	// A value sets the collar.
	next := mustApply(t, m, `{"minPrice":"0.01","maxPrice":"1000000","minAmount":"0.001","maxAmount":"50","minTurnover":"10"}`)
	if next.MinPrice != "0.01" || next.MaxPrice != "1000000" {
		t.Fatalf("price collar not applied: %s..%s", next.MinPrice, next.MaxPrice)
	}
	if next.MinAmount != "0.001" || next.MaxAmount != "50" || next.MinTurnover != "10" {
		t.Fatalf("amount fields not applied: %s %s %s", next.MinAmount, next.MaxAmount, next.MinTurnover)
	}

	// JSON null clears it (unbounded); absent keeps it.
	next = mustApply(t, next, `{"maxPrice":null}`)
	if next.MaxPrice != "" || next.MinPrice != "0.01" {
		t.Fatalf("null collar: got min=%q max=%q", next.MinPrice, next.MaxPrice)
	}

	// Cross-field ordering: min above max refuses, on the merged row.
	mustRefuse(t, m, `{"minPrice":"10","maxPrice":"5"}`, "min price exceeds max")
	mustRefuse(t, m, `{"minAmount":"100","maxAmount":"50"}`, "min amount exceeds max")

	// Collars must be positive decimals.
	mustRefuse(t, m, `{"minPrice":"0"}`, "minPrice must be positive")
	mustRefuse(t, m, `{"maxPrice":"abc"}`, "decimal string")
}

func TestApplyUpdateFeeShape(t *testing.T) {
	m := seedCfg()
	mustRefuse(t, m, `{"makerFeeRate":"1"}`, "at least 0 and less than 1")
	mustRefuse(t, m, `{"takerFeeRate":"-0.1"}`, "at least 0 and less than 1")
	mustRefuse(t, m, `{"makerFeeRate":"0.00001"}`, "4 decimal places")
	next := mustApply(t, m, `{"takerFeeRate":"0"}`) // zero fees are legal
	if next.TakerFee != "0" {
		t.Fatalf("zero fee not applied: %s", next.TakerFee)
	}
}

func TestApplyUpdateQuantizationValidatedAtMergedScales(t *testing.T) {
	m := seedCfg()

	// Tick at the merged quote scale must resolve exactly.
	next := mustApply(t, m, `{"tickSize":"0.05"}`)
	if next.TickRaw != 5 {
		t.Fatalf("tick raw not recomputed: %d", next.TickRaw)
	}
	mustRefuse(t, m, `{"tickSize":"0.001"}`, "quote scale") // finer than 2dp quote
	mustRefuse(t, m, `{"tickSize":"0"}`, "must be positive")

	// A scale change re-anchors the validation: quote scale 4 admits 0.001.
	next = mustApply(t, m, `{"quoteScale":4,"tickSize":"0.001"}`)
	if next.TickRaw != 10 || next.QuoteScale != 4 {
		t.Fatalf("merged scales wrong: tickRaw=%d quoteScale=%d", next.TickRaw, next.QuoteScale)
	}

	// Lot finer than the (merged) base scale is unrepresentable.
	mustRefuse(t, m, `{"lotSize":"0.000000001"}`, "base scale")
	mustRefuse(t, m, `{"baseScale":-1}`, "between 0 and 18")
}

func TestApplyUpdateVisibilityFields(t *testing.T) {
	m := seedCfg()
	mustRefuse(t, m, `{"sort":-3}`, "non-negative")
	mustRefuse(t, m, `{"zone":"   "}`, "1-64 characters")
	next := mustApply(t, m, `{"zone":"main"}`)
	if next.Zone != "main" {
		t.Fatalf("zone not applied: %s", next.Zone)
	}
}
