package market

import (
	"context"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/syaikhipin/malxchange/internal/events"
	"github.com/syaikhipin/malxchange/internal/money"
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

// knownMarkets lists every Market the projection knows: any Market with
// trade history or a last-known Depth publish (the derived state this
// service owns). The Ticker enumerates from here so a zero-Fill Market
// publishes its empty summary too — the front never waits for a first
// tick. Sorted by Market so callers never depend on query order.
func knownMarkets(ctx context.Context, db DB) ([]string, error) {
	rows, err := db.Query(ctx, `
		SELECT market FROM trades
		UNION
		SELECT market FROM depth_levels
		ORDER BY market`)
	if err != nil {
		return nil, fmt.Errorf("known markets: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, fmt.Errorf("known markets: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// windowFills reads one Market's in-window trade history oldest first —
// the same [now-24h, now] boundary FoldTicker applies, so the query and
// the fold can never disagree about what is inside. Money values come
// back as NUMERIC text and parse at the ADR-0005 boundary.
func windowFills(ctx context.Context, db DB, market string, now time.Time) ([]WindowFill, error) {
	rows, err := db.Query(ctx, `
		SELECT fill_id, price::text, turnover::text, traded_at
		FROM trades
		WHERE market = $1 AND traded_at >= $2
		ORDER BY traded_at ASC, fill_id ASC`, market, now.Add(-TickerWindow))
	if err != nil {
		return nil, fmt.Errorf("window fills %s: %w", market, err)
	}
	defer rows.Close()
	var out []WindowFill
	for rows.Next() {
		var f WindowFill
		var price, turnover string
		if err := rows.Scan(&f.ID, &price, &turnover, &f.At); err != nil {
			return nil, fmt.Errorf("window fills %s: %w", market, err)
		}
		if f.Price, err = money.Parse(price); err != nil {
			return nil, fmt.Errorf("window fills %s: price %q: %w", market, price, err)
		}
		if f.Turnover, err = money.Parse(turnover); err != nil {
			return nil, fmt.Errorf("window fills %s: turnover %q: %w", market, turnover, err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ComputeTicker answers the Market's current Ticker: the pure fold over
// its own trade history, the same computation the publish loop puts on
// the ticker topic — REST and the stream cannot disagree.
func ComputeTicker(ctx context.Context, db DB, market string, now time.Time) (events.Ticker, error) {
	fills, err := windowFills(ctx, db, market, now)
	if err != nil {
		return events.Ticker{}, err
	}
	return FoldTicker(market, now, fills), nil
}
