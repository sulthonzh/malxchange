package market

import (
	"errors"
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
