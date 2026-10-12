package market

import (
	"testing"
	"time"

	"github.com/syaikhipin/malxchange/internal/events"
	"github.com/syaikhipin/malxchange/internal/money"
)

// TestFoldTickerEmptyWindow: a Market whose 24h window holds no Fills
// publishes an honest empty summary — no invented last price, no invented
// range, zero Turnover (ticket #32: the front never waits for a first tick).
func TestFoldTickerEmptyWindow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	got := FoldTicker("BTC/USDT", now, nil)
	want := events.Ticker{
		Market: "BTC/USDT", Last: "", High: "", Low: "", Turnover: "0",
	}
	if got != want {
		t.Errorf("empty window fold = %+v, want %+v", got, want)
	}
}

// TestFoldTickerWindowRoll: the 24h roll arithmetic is decimal-exact and
// honest at the boundaries (ticket #32). A Fill exactly at now-24h is
// inside the window; the same Fill one nanosecond older has rolled out.
// High/Low compare as decimals, Turnover sums exactly, Last is the newest
// in-window Fill — expected values are hand-computed, never recomputed by
// the code under test.
func TestFoldTickerWindowRoll(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	boundary := now.Add(-TickerWindow) // exactly 24h ago: inside
	rolledOut := boundary.Add(-time.Nanosecond)

	fills := []WindowFill{
		{ID: "f-rolled", Price: money.MustParse("99000"), Turnover: money.MustParse("99"), At: rolledOut},
		{ID: "f-boundary", Price: money.MustParse("50000.1"), Turnover: money.MustParse("10.00002"), At: boundary},
		{ID: "f-mid", Price: money.MustParse("51000.25"), Turnover: money.MustParse("15.00003"), At: now.Add(-2 * time.Hour)},
		{ID: "f-low", Price: money.MustParse("49500.001"), Turnover: money.MustParse("4.95"), At: now.Add(-time.Hour)},
		{ID: "f-high", Price: money.MustParse("51500.5"), Turnover: money.MustParse("51.5005"), At: now.Add(-3 * time.Hour)},
		{ID: "f-last", Price: money.MustParse("50250.75"), Turnover: money.MustParse("20.1003"), At: now.Add(-time.Second)},
	}
	got := FoldTicker("BTC/USDT", now, fills)
	want := events.Ticker{
		Market:   "BTC/USDT",
		Last:     "50250.75",  // newest in-window Fill
		High:     "51500.5",   // f-high; f-rolled's 99000 stayed out
		Low:      "49500.001", // f-low; f-boundary's 50000.1 is above it
		Turnover: "101.55085", // 10.00002+15.00003+4.95+51.5005+20.1003, exact
	}
	if got != want {
		t.Errorf("window fold = %+v, want %+v", got, want)
	}

	// The same sequence one nanosecond later: the boundary Fill rolled out,
	// and every value it contributed drops honestly.
	got = FoldTicker("BTC/USDT", now.Add(time.Nanosecond), fills)
	want = events.Ticker{
		Market:   "BTC/USDT",
		Last:     "50250.75",
		High:     "51500.5",
		Low:      "49500.001",
		Turnover: "91.55083", // 15.00003+4.95+51.5005+20.1003 — the boundary Fill's 10.00002 left
	}
	if got != want {
		t.Errorf("roll past boundary = %+v, want %+v", got, want)
	}
}

// TestFoldTickerDeterministic: the Ticker is a pure function of trade
// history (spec decision, ticket #32) — the same window folds to the same
// answer regardless of the order Fills arrive in, and same-instant Fills
// break the Last tie on fill ID so the newest is stable. This pins the
// determinism promise with the feature (review checklist: determinism
// guarantees ship pinned, TestDeterministicFold precedent).
func TestFoldTickerDeterministic(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	fills := []WindowFill{
		{ID: "fill-0001", Price: money.MustParse("50000.1"), Turnover: money.MustParse("10.00002"), At: now.Add(-3 * time.Hour)},
		{ID: "fill-0002", Price: money.MustParse("51000.25"), Turnover: money.MustParse("15.00003"), At: now.Add(-time.Second)},
		{ID: "fill-0003", Price: money.MustParse("49500.001"), Turnover: money.MustParse("4.95"), At: now.Add(-3 * time.Hour)},
		{ID: "fill-0004", Price: money.MustParse("51500.5"), Turnover: money.MustParse("51.5005"), At: now.Add(-time.Minute)},
	}
	want := events.Ticker{
		Market: "BTC/USDT", Last: "51000.25", High: "51500.5",
		Low: "49500.001", Turnover: "81.45055",
	}
	if got := FoldTicker("BTC/USDT", now, fills); got != want {
		t.Fatalf("fold = %+v, want %+v", got, want)
	}

	// Every rotation of the same history folds to the identical answer.
	for i := 1; i < len(fills); i++ {
		rotated := append(append([]WindowFill{}, fills[i:]...), fills[:i]...)
		if got := FoldTicker("BTC/USDT", now, rotated); got != want {
			t.Errorf("rotation %d folded to %+v, want the identical %+v", i, got, want)
		}
	}

	// Same instant, two Fills: the greater fill ID is the newest, so Last
	// does not depend on which arrived first in the fold.
	sameInstant := now.Add(-time.Hour)
	tie := []WindowFill{
		{ID: "fill-0009", Price: money.MustParse("50001"), Turnover: money.MustParse("1"), At: sameInstant},
		{ID: "fill-0010", Price: money.MustParse("50002"), Turnover: money.MustParse("2"), At: sameInstant},
	}
	for _, order := range [][]WindowFill{tie, {tie[1], tie[0]}} {
		if got := FoldTicker("BTC/USDT", now, order); got.Last != "50002" {
			t.Errorf("same-instant fold Last = %q, want the greater fill ID's price 50002", got.Last)
		}
	}
}
