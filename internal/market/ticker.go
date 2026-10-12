package market

import (
	"time"

	"github.com/shopspring/decimal"

	"github.com/syaikhipin/malxchange/internal/events"
)

// TickerWindow is the rolling window every Ticker summarizes (ticket #32:
// the 24h range and Turnover).
const TickerWindow = 24 * time.Hour

// WindowFill is one Fill inside the rolling window, as read from the
// Market's own trade history (the sanitized trades table from #30). A
// Trade is the market-wide record of a Fill pair (GLOSSARY); price and
// Turnover are ADR-0005 decimals parsed from the NUMERIC columns.
type WindowFill struct {
	ID       string          // fill ID (the trade.fill envelope ID)
	Price    decimal.Decimal // quote price per base unit
	Turnover decimal.Decimal // quote-side value of the Fill
	At       time.Time       // traded_at
}

// FoldTicker folds one Market's window Fills into its Ticker: last price,
// 24h high, 24h low, 24h Turnover. The fold is a pure function of (now,
// fills) — same history, same answer, in any fold order — so the Ticker
// carries no rebuild state (spec decision, ticket #32). The window is
// [now-24h, now]: a Fill exactly at the lower boundary is inside the
// window, a Fill one nanosecond older has rolled out. All arithmetic is
// decimal-exact (ADR-0005) — high/low compare as decimals, Turnover sums
// exactly, and Last is the newest Fill's price with newest decided by
// (traded_at, fill ID) so same-instant Fills are deterministic. A window
// with no Fills answers empty (see events.Ticker).
func FoldTicker(market string, now time.Time, fills []WindowFill) events.Ticker {
	cutoff := now.Add(-TickerWindow)
	have := false
	var high, low, turnover, last decimal.Decimal
	var lastID string
	var lastAt time.Time
	for _, f := range fills {
		if f.At.Before(cutoff) {
			continue // rolled out of the window
		}
		if !have {
			high, low, turnover, have = f.Price, f.Price, f.Turnover, true
		} else {
			if f.Price.Cmp(high) > 0 {
				high = f.Price
			}
			if f.Price.Cmp(low) < 0 {
				low = f.Price
			}
			turnover = turnover.Add(f.Turnover) // exact decimal sum
		}
		if !have || f.At.After(lastAt) || (f.At.Equal(lastAt) && f.ID > lastID) {
			have = true
			last, lastID, lastAt = f.Price, f.ID, f.At
		}
	}
	if !have {
		return events.Ticker{Market: market, Turnover: decimal.Zero.String()}
	}
	return events.Ticker{
		Market: market, Last: last.String(), High: high.String(),
		Low: low.String(), Turnover: turnover.String(),
	}
}
