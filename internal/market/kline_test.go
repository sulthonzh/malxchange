package market

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/syaikhipin/malxchange/internal/money"
)

// mustUTC builds a UTC wall-clock instant for table tests.
func mustUTC(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	return t
}

// foldIntoBars folds points exactly the way the projection's write path
// does: bucket by interval, then fold each point into its bar in the given
// (Fill event) order. Test-local mirror of foldKlines' loop, so the
// aggregation semantics are pinned without Postgres.
func foldIntoBars(t *testing.T, iv Interval, pts ...fillPoint) map[time.Time]barState {
	t.Helper()
	bars := make(map[time.Time]barState)
	for _, p := range pts {
		open := bucketOpen(p.At, iv)
		b := bars[open]
		bars[open] = foldBar(b, p)
	}
	return bars
}

// point builds one Fill contribution to a bar.
func point(at time.Time, seq int64, price, amount, turnover string) fillPoint {
	return fillPoint{
		At: at, Seq: seq,
		Price:    money.MustParse(price),
		Amount:   money.MustParse(amount),
		Turnover: money.MustParse(turnover),
	}
}

// TestFoldOutOfOrderUpdatesOlderBarOnly: a Fill landing in an older bucket
// updates that bar's open/high/low/close/turnover correctly and leaves the
// neighboring bar untouched (ticket #31). Within a bucket, open/close
// follow the Fills' trade instants (ties by Fill Seq), never arrival order.
func TestFoldOutOfOrderUpdatesOlderBarOnly(t *testing.T) {
	t.Parallel()
	// Event order: f1 (09:01), f2 (09:06), f3 (09:03 — out-of-order, an
	// older bucket).
	pts := []fillPoint{
		point(mustUTC("2026-10-12T09:01:00Z"), 1, "50000.10", "0.0002", "10.00002"),
		point(mustUTC("2026-10-12T09:06:00Z"), 2, "50001.00", "0.0003", "15.00003"),
		point(mustUTC("2026-10-12T09:03:00Z"), 3, "50002.50", "0.0001", "5.000025"),
	}
	bars := foldIntoBars(t, Interval5m, pts...)

	// The older bar: opened by f1 (earliest instant), closed by f3 (latest
	// instant despite arriving last), high/low over both.
	b := bars[mustUTC("2026-10-12T09:00:00Z")]
	wantBar(t, b, "50000.10", "50002.50", "50000.10", "50002.50", "0.0003", "15.000045", 2,
		"2026-10-12T09:01:00Z", 1, "2026-10-12T09:03:00Z", 3)

	// The neighbor bar: f3's late landing must not touch it.
	n := bars[mustUTC("2026-10-12T09:05:00Z")]
	wantBar(t, n, "50001.00", "50001.00", "50001.00", "50001.00", "0.0003", "15.00003", 1,
		"2026-10-12T09:06:00Z", 2, "2026-10-12T09:06:00Z", 2)

	// The same Fills folded in a different arrival order produce the
	// identical bars: aggregation depends on trade instants, not delivery
	// order (rebuild-replay determinism).
	reordered := foldIntoBars(t, Interval5m, pts[2], pts[0], pts[1])
	wantBar(t, reordered[mustUTC("2026-10-12T09:00:00Z")], "50000.10", "50002.50", "50000.10", "50002.50", "0.0003", "15.000045", 2,
		"2026-10-12T09:01:00Z", 1, "2026-10-12T09:03:00Z", 3)
	wantBar(t, reordered[mustUTC("2026-10-12T09:05:00Z")], "50001.00", "50001.00", "50001.00", "50001.00", "0.0003", "15.00003", 1,
		"2026-10-12T09:06:00Z", 2, "2026-10-12T09:06:00Z", 2)
}

// TestFoldSameInstantTieBreaksBySeq: two Fills at the identical trade
// instant break the open/close tie by Fill Seq — the earlier Seq opens the
// bar, the later Seq closes it (deterministic under redelivery).
func TestFoldSameInstantTieBreaksBySeq(t *testing.T) {
	t.Parallel()
	pts := []fillPoint{
		point(mustUTC("2026-10-12T09:01:00Z"), 7, "100.50", "1", "100.50"),
		point(mustUTC("2026-10-12T09:01:00Z"), 9, "101.00", "2", "202.00"),
	}
	bars := foldIntoBars(t, Interval1m, pts...)
	wantBar(t, bars[mustUTC("2026-10-12T09:01:00Z")], "100.50", "101.00", "100.50", "101.00", "3", "302.50", 2,
		"2026-10-12T09:01:00Z", 7, "2026-10-12T09:01:00Z", 9)
}

// wantBar asserts one folded bar against hand-worked literals (independent
// source of truth, not a re-run of the fold).
func wantBar(t *testing.T, b barState, open, high, low, close, amount, turnover string, fills int64,
	firstAt string, firstSeq int64, lastAt string, lastSeq int64) {
	t.Helper()
	checks := []struct {
		name string
		got  decimal.Decimal
		want string
	}{
		{"open", b.Open, open},
		{"high", b.High, high},
		{"low", b.Low, low},
		{"close", b.Close, close},
		{"amount", b.Amount, amount},
		{"turnover", b.Turnover, turnover},
	}
	for _, c := range checks {
		if !c.got.Equal(money.MustParse(c.want)) {
			t.Errorf("bar %s = %s, want %s", c.name, c.got, c.want)
		}
	}
	if b.Fills != fills {
		t.Errorf("bar fills = %d, want %d", b.Fills, fills)
	}
	if !b.FirstAt.Equal(mustUTC(firstAt)) || b.FirstSeq != firstSeq {
		t.Errorf("bar first = %s/%d, want %s/%d", b.FirstAt.Format(time.RFC3339Nano), b.FirstSeq, firstAt, firstSeq)
	}
	if !b.LastAt.Equal(mustUTC(lastAt)) || b.LastSeq != lastSeq {
		t.Errorf("bar last = %s/%d, want %s/%d", b.LastAt.Format(time.RFC3339Nano), b.LastSeq, lastAt, lastSeq)
	}
}

// TestBucketOpenIntervalEdges pins that every bar opens at an exact UTC
// interval boundary: a Fill at the boundary itself opens the new bar, a
// Fill one instant earlier belongs to the bar before it, and sub-second
// remainders never leak into the bucket open (ticket #31).
func TestBucketOpenIntervalEdges(t *testing.T) {
	t.Parallel()
	cases := []struct {
		iv       Interval
		at       string
		wantOpen string
	}{
		// The edge itself belongs to the bucket that opens there.
		{iv: Interval1m, at: "2026-10-12T09:00:00Z", wantOpen: "2026-10-12T09:00:00Z"},
		{iv: Interval1m, at: "2026-10-12T09:00:00.999999999Z", wantOpen: "2026-10-12T09:00:00Z"},
		{iv: Interval5m, at: "2026-10-12T09:04:59Z", wantOpen: "2026-10-12T09:00:00Z"},
		{iv: Interval5m, at: "2026-10-12T09:05:00Z", wantOpen: "2026-10-12T09:05:00Z"},
		{iv: Interval15m, at: "2026-10-12T09:14:59Z", wantOpen: "2026-10-12T09:00:00Z"},
		{iv: Interval15m, at: "2026-10-12T09:15:00Z", wantOpen: "2026-10-12T09:15:00Z"},
		{iv: Interval30m, at: "2026-10-12T09:29:59Z", wantOpen: "2026-10-12T09:00:00Z"},
		{iv: Interval30m, at: "2026-10-12T09:30:00Z", wantOpen: "2026-10-12T09:30:00Z"},
		{iv: Interval1h, at: "2026-10-12T09:59:59Z", wantOpen: "2026-10-12T09:00:00Z"},
		{iv: Interval1h, at: "2026-10-12T10:00:00Z", wantOpen: "2026-10-12T10:00:00Z"},
		{iv: Interval4h, at: "2026-10-12T11:59:59Z", wantOpen: "2026-10-12T08:00:00Z"},
		{iv: Interval4h, at: "2026-10-12T12:00:00Z", wantOpen: "2026-10-12T12:00:00Z"},
		{iv: Interval1d, at: "2026-10-12T23:59:59Z", wantOpen: "2026-10-12T00:00:00Z"},
		{iv: Interval1d, at: "2026-10-13T00:00:00Z", wantOpen: "2026-10-13T00:00:00Z"},
		// 1w weeks start Monday 00:00 UTC (pinned choice — see bucketOpen):
		// 2026-10-12 is a Monday, 2026-10-11 its Sunday, so the Sunday Fill
		// lands in the week that opened Monday 2026-10-05.
		{iv: Interval1w, at: "2026-10-12T00:00:00Z", wantOpen: "2026-10-12T00:00:00Z"},
		{iv: Interval1w, at: "2026-10-11T23:59:59Z", wantOpen: "2026-10-05T00:00:00Z"},
		{iv: Interval1w, at: "2026-10-14T18:27:05Z", wantOpen: "2026-10-12T00:00:00Z"},
		// Bucketing is by UTC wall clock regardless of the location the
		// instant carries: 09:30+01:30 is 08:00 UTC.
		{iv: Interval1h, at: "2026-10-12T09:30:00+01:30", wantOpen: "2026-10-12T08:00:00Z"},
	}
	for _, tc := range cases {
		got := bucketOpen(mustUTC(tc.at), tc.iv)
		if want := mustUTC(tc.wantOpen); !got.Equal(want) {
			t.Errorf("bucketOpen(%s, %s) = %s, want %s", tc.at, tc.iv, got.Format(time.RFC3339Nano), tc.wantOpen)
		}
		if got.Location() != time.UTC {
			t.Errorf("bucketOpen(%s, %s) not UTC: %v", tc.at, tc.iv, got.Location())
		}
	}
}

// TestParseInterval pins the stored-interval vocabulary: exactly the eight
// stored intervals parse, anything else (including legacy 1m-only aliases
// and empty) is the named errBadInterval, so REST answers 400 instead of an
// empty success (ticket #31).
func TestParseInterval(t *testing.T) {
	t.Parallel()
	for _, iv := range StoredIntervals {
		got, err := ParseInterval(string(iv))
		if err != nil {
			t.Errorf("ParseInterval(%q): %v", iv, err)
			continue
		}
		if got != iv {
			t.Errorf("ParseInterval(%q) = %q", iv, got)
		}
	}
	for _, raw := range []string{"", "2m", "1M", "60m", "24h", "7d", "week", "1mm"} {
		if _, err := ParseInterval(raw); !errors.Is(err, errBadInterval) {
			t.Errorf("ParseInterval(%q) err = %v, want errBadInterval", raw, err)
		}
	}
}
