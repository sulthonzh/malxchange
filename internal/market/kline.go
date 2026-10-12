package market

import (
	"errors"
	"time"

	"github.com/shopspring/decimal"
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

// fillPoint is one Fill's contribution to a Kline bar: its trade instant
// (the envelope's OccurredAt), per-Market Fill Seq (tie-break), and the
// money values (ADR-0005 decimal strings parsed exact).
type fillPoint struct {
	At       time.Time
	Seq      int64
	Price    decimal.Decimal
	Amount   decimal.Decimal
	Turnover decimal.Decimal
}

// barState is the accumulated fold state of one (Market, interval, bucket
// open) bar. Open follows the earliest Fill instant, Close the latest —
// instants, not arrival order, so an out-of-order Fill lands in its own
// older bar without corrupting its neighbors. First/Last (instant + Seq)
// are the fold's comparison keys, persisted so replayed and late Fills
// recompute the same bar; Seq breaks ties between Fills at the same
// instant (per-Market event order == Seq order, so the tie-break is
// deterministic under redelivery and replay).
type barState struct {
	Exists   bool
	Open     decimal.Decimal
	High     decimal.Decimal
	Low      decimal.Decimal
	Close    decimal.Decimal
	Amount   decimal.Decimal
	Turnover decimal.Decimal
	Fills    int64
	FirstAt  time.Time
	FirstSeq int64
	LastAt   time.Time
	LastSeq  int64
}

// foldBar folds one Fill into a bar: a fresh bar opens/highs/lows/closes at
// the Fill's price; an existing bar widens high/low, adds amount and
// turnover, counts the Fill, and moves open/close only when the Fill's
// instant (ties by Seq) precedes the current first or follows the current
// last.
func foldBar(b barState, p fillPoint) barState {
	if !b.Exists {
		return barState{
			Exists:   true,
			Open:     p.Price,
			High:     p.Price,
			Low:      p.Price,
			Close:    p.Price,
			Amount:   p.Amount,
			Turnover: p.Turnover,
			Fills:    1,
			FirstAt:  p.At, FirstSeq: p.Seq,
			LastAt: p.At, LastSeq: p.Seq,
		}
	}
	if p.At.Before(b.FirstAt) || (p.At.Equal(b.FirstAt) && p.Seq < b.FirstSeq) {
		b.Open = p.Price
		b.FirstAt, b.FirstSeq = p.At, p.Seq
	}
	if p.At.After(b.LastAt) || (p.At.Equal(b.LastAt) && p.Seq > b.LastSeq) {
		b.Close = p.Price
		b.LastAt, b.LastSeq = p.At, p.Seq
	}
	if p.Price.Cmp(b.High) > 0 {
		b.High = p.Price
	}
	if p.Price.Cmp(b.Low) < 0 {
		b.Low = p.Price
	}
	b.Amount = b.Amount.Add(p.Amount)
	b.Turnover = b.Turnover.Add(p.Turnover)
	b.Fills++
	return b
}

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
