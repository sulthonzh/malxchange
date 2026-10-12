package market

import (
	"errors"
	"time"
)

// Interval is a stored Kline bar width (GLOSSARY: a Kline is an OHLCV bar
// at a stored interval). The wire spelling is the public chart-channel
// identifier (ADR-0011: kline.{market}.{interval}).
type Interval string

// The stored intervals, oldest vocabulary first. This slice is the single
// source of truth: the klines CHECK constraint, REST parsing, and the
// fold all enumerate it — and it is a slice, never a map iteration, so
// enumeration order is stable everywhere it appears.
const (
	Interval1m  Interval = "1m"
	Interval5m  Interval = "5m"
	Interval15m Interval = "15m"
	Interval30m Interval = "30m"
	Interval1h  Interval = "1h"
	Interval4h  Interval = "4h"
	Interval1d  Interval = "1d"
	Interval1w  Interval = "1w"
)

// StoredIntervals lists every interval the projection folds Fills into.
var StoredIntervals = []Interval{Interval1m, Interval5m, Interval15m, Interval30m, Interval1h, Interval4h, Interval1d, Interval1w}

// errBadInterval is the named error for any interval parameter that is
// absent or not a stored interval; callers test it with errors.Is.
var errBadInterval = errors.New("unknown interval (must be one of 1m, 5m, 15m, 30m, 1h, 4h, 1d, 1w)")

// ParseInterval accepts exactly the stored intervals. The legacy
// 1-minute-only restriction is dead: every stored interval is first-class
// on the wire (ticket #31, ADR-0011).
func ParseInterval(raw string) (Interval, error) {
	for _, iv := range StoredIntervals {
		if raw == string(iv) {
			return iv, nil
		}
	}
	return "", errBadInterval
}

// Week starts Monday 00:00 UTC (ISO 8601) — the pinned choice for the 1w
// bucket edge, asserted by TestBucketOpenIntervalEdges. Unix day 0
// (1970-01-01) was a Thursday, so Monday weeks offset by +4 days: Unix day
// 4 (1970-01-05) opened the first week bucket.
const weekStartOffsetDays = 4

// bucketOpen floors a Fill instant to the open of its Kline bar: the exact
// UTC interval boundary the Fill falls in (every sub-interval remainder —
// including sub-second — is dropped). All boundaries are UTC regardless of
// the location the instant carries.
func bucketOpen(at time.Time, iv Interval) time.Time {
	u := at.Unix() // seconds; UTC by definition
	var open int64
	switch iv {
	case Interval1w:
		days := floorDiv(u, secondsPerDay)
		weeks := floorDiv(days-weekStartOffsetDays, 7)
		open = (weeks*7 + weekStartOffsetDays) * secondsPerDay
	case Interval1d:
		open = floorDiv(u, secondsPerDay) * secondsPerDay
	default:
		// 1m/5m/15m/30m/1h/4h all divide the UTC day evenly, so second
		// arithmetic lands on exact UTC wall-clock boundaries.
		open = u - floorMod(u, iv.seconds())
	}
	return time.Unix(open, 0).UTC()
}

func (iv Interval) seconds() int64 {
	switch iv {
	case Interval1m:
		return 60
	case Interval5m:
		return 5 * 60
	case Interval15m:
		return 15 * 60
	case Interval30m:
		return 30 * 60
	case Interval1h:
		return 60 * 60
	case Interval4h:
		return 4 * 60 * 60
	default:
		panic("market: " + string(iv) + " has no second width")
	}
}

const secondsPerDay = 24 * 60 * 60

// floorDiv is Euclidean integer division (rounds toward -infinity), so
// pre-epoch instants bucket the same way post-epoch ones do.
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// floorMod is the remainder paired with floorDiv (never negative for a
// positive divisor).
func floorMod(a, b int64) int64 {
	return a - floorDiv(a, b)*b
}
