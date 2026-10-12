package market

import (
	"errors"
	"testing"
	"time"
)

// mustUTC builds a UTC wall-clock instant for table tests.
func mustUTC(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	return t
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
