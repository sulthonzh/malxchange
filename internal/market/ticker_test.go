package market

import (
	"encoding/json"
	"strings"
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

// TestTickerWireShape pins the wire contract shared by the ticker topic
// and the internal REST endpoints (they must match byte for byte): exactly
// market/last/high/low/turnover, every money value a decimal string
// (ADR-0005), no float and no Member/Order identifier anywhere.
func TestTickerWireShape(t *testing.T) {
	t.Parallel()
	b, err := json.Marshal(events.Ticker{
		Market: "BTC/USDT", Last: "50250.75", High: "51500.5",
		Low: "49500.001", Turnover: "101.55085",
	})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatal(err)
	}
	want := []string{"market", "last", "high", "low", "turnover"}
	if len(fields) != len(want) {
		t.Errorf("Ticker wire fields = %v, want exactly %v", keys(fields), want)
	}
	for _, k := range want {
		if _, ok := fields[k]; !ok {
			t.Errorf("Ticker wire missing field %q", k)
		}
	}
	for _, banned := range []string{"member", "order", "e+", "e-", "volume", "notional", "thumb"} {
		if strings.Contains(string(b), banned) {
			t.Errorf("Ticker wire carries %q:\n%s", banned, b)
		}
	}
	// The empty window serializes honestly: empty strings and "0", never
	// null, never a float zero. The empty form is produced by the fold —
	// the only path that builds a Ticker — so this pins the built payload,
	// not a zero struct.
	b, err = json.Marshal(FoldTicker("ETH/USDT", time.Now(), nil))
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"market":"ETH/USDT","last":"","high":"","low":"","turnover":"0"}`; string(b) != want {
		t.Errorf("empty Ticker wire = %s, want %s", b, want)
	}
}

// TestTickerPassGate: one publish pass emits a Market only when its Ticker
// differs from the last one actually published for it — the in-memory
// cadence state that keeps the loop at most one message per Market per
// pass (the pass itself is paced at most once per second). The pass output
// is ordered by Market, so it is deterministic end to end.
func TestTickerPassGate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	btc := events.Ticker{Market: "BTC/USDT", Last: "50000.1", High: "50000.1", Low: "50000.1", Turnover: "10.00002"}
	eth := events.Ticker{Market: "ETH/USDT", Turnover: "0"}
	btcNext := events.Ticker{Market: "BTC/USDT", Last: "51000", High: "51000", Low: "50000.1", Turnover: "20.00002"}

	// First pass: every known Market publishes, Markets sorted.
	published := map[string]string{}
	got := passKeys(tickerPass(now, map[string]events.Ticker{"ETH/USDT": eth, "BTC/USDT": btc}, published))
	if len(got) != 2 || got[0].Key != "BTC/USDT" || got[1].Key != "ETH/USDT" {
		t.Fatalf("first pass = [%s %s], want BTC/USDT then ETH/USDT", got[0].Key, got[1].Key)
	}
	for _, env := range got {
		var payload events.Ticker
		if err := json.Unmarshal(env.Data, &payload); err != nil {
			t.Fatalf("pass envelope %s: %v", env.Key, err)
		}
		switch env.Key {
		case "BTC/USDT":
			if payload != btc {
				t.Errorf("pass payload = %+v, want %+v", payload, btc)
			}
		case "ETH/USDT":
			if payload != eth {
				t.Errorf("pass payload = %+v, want %+v", payload, eth)
			}
		}
		if env.Type != events.TopicTicker {
			t.Errorf("pass envelope type = %q, want %q", env.Type, events.TopicTicker)
		}
	}

	// Second pass, nothing changed: nothing publishes — the cadence gate.
	got = passKeys(tickerPass(now.Add(time.Second), map[string]events.Ticker{"BTC/USDT": btc, "ETH/USDT": eth}, published))
	if len(got) != 0 {
		t.Errorf("unchanged pass published %d envelopes, want 0", len(got))
	}

	// BTC moves: only BTC publishes, carrying the new state.
	emits := tickerPass(now.Add(2*time.Second), map[string]events.Ticker{"BTC/USDT": btcNext, "ETH/USDT": eth}, published)
	got = passKeys(emits)
	if len(got) != 1 || got[0].Key != "BTC/USDT" {
		t.Fatalf("changed pass = %+v, want exactly BTC/USDT", got)
	}
	var payload events.Ticker
	if err := json.Unmarshal(emits[0].env.Data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload != btcNext {
		t.Errorf("changed pass payload = %+v, want %+v", payload, btcNext)
	}

	// A market rolling back to an earlier state still publishes: the gate
	// compares against the last PUBLISHED state, not any history.
	got = passKeys(tickerPass(now.Add(3*time.Second), map[string]events.Ticker{"BTC/USDT": btc, "ETH/USDT": eth}, published))
	if len(got) != 1 || got[0].Key != "BTC/USDT" {
		t.Fatalf("rollback pass = %+v, want exactly BTC/USDT", got)
	}

	// Envelope ids are deterministic per (Market, second) — a restart or
	// produce retry within the same second can only re-send a byte-identical
	// message (SweepTTL's deterministic-id precedent).
	a := tickerPass(now, map[string]events.Ticker{"BTC/USDT": btc}, map[string]string{})
	b := tickerPass(now, map[string]events.Ticker{"BTC/USDT": btc}, map[string]string{})
	if a[0].env.ID != b[0].env.ID {
		t.Errorf("same-second envelope ids differ: %q vs %q", a[0].env.ID, b[0].env.ID)
	}
	if next := tickerPass(now.Add(time.Second), map[string]events.Ticker{"BTC/USDT": btc}, map[string]string{}); next[0].env.ID == a[0].env.ID {
		t.Errorf("next-second envelope id %q reused", next[0].env.ID)
	}
}

// passKeys flattens a pass to its envelope key order (the sorted-Market
// order the pass promises) so assertions read plainly.
func passKeys(emits []tickerEmit) []events.Envelope {
	out := make([]events.Envelope, len(emits))
	for i, e := range emits {
		out[i] = e.env
	}
	return out
}
