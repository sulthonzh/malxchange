package market

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"time"

	"github.com/shopspring/decimal"
	"github.com/twmb/franz-go/pkg/kgo"

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
// config (the market.updated copy from #33), trade history, or a
// last-known Depth publish (the derived state this service owns). The
// Ticker enumerates from here so a zero-Fill Market publishes its empty
// summary too — the front never waits for a first tick. Sorted by Market
// so callers never depend on query order.
func knownMarkets(ctx context.Context, db DB) ([]string, error) {
	rows, err := db.Query(ctx, `
		SELECT market FROM markets
		UNION
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

// tickerEmit is one decided publish: the envelope and its full wire form,
// built once by the pass and produced by the loop.
type tickerEmit struct {
	env  events.Envelope
	body []byte
}

// tickerPass decides what one publish pass emits: a Market publishes only
// when its Ticker differs from the last one actually published for it —
// the in-memory cadence state (single-replica stance) that keeps the loop
// at most one message per Market per pass. Markets are visited in sorted
// order and each carries the Market as record key, so no caller can
// observe map order. Envelope ids are deterministic per (Market, second)
// — a restart or produce retry within the same second can only re-send a
// byte-identical message (the SweepTTL deterministic-id precedent).
func tickerPass(now time.Time, tickers map[string]events.Ticker, published map[string]string) []tickerEmit {
	markets := make([]string, 0, len(tickers))
	for m := range tickers {
		markets = append(markets, m)
	}
	sort.Strings(markets)
	out := make([]tickerEmit, 0, len(markets))
	for _, m := range markets {
		body, err := json.Marshal(tickers[m])
		if err != nil {
			// events.Ticker is all strings; marshaling cannot fail.
			panic(fmt.Sprintf("marshal ticker %s: %v", m, err))
		}
		if string(body) == published[m] {
			continue
		}
		id := "ticker:" + m + ":" + strconv.FormatInt(now.Unix(), 10)
		env, raw, err := events.New(id, m, events.TopicTicker, now, tickers[m])
		if err != nil {
			panic(fmt.Sprintf("build ticker envelope %s: %v", m, err))
		}
		out = append(out, tickerEmit{env: env, body: raw})
		published[m] = string(body)
	}
	return out
}

// tickerPublishEvery is the publish cadence: one pass per second, so a
// Market publishes at most one message per second no matter how fast
// Fills land (ticket #32).
const tickerPublishEvery = time.Second

// PublishTickers is the Ticker publish loop: once per cadence, enumerate
// the known Markets, fold each one's Ticker from its own trade history,
// and publish what changed onto the ticker topic. A failed pass is logged
// and retried on the next tick (the SweepTTL precedent for paced loops):
// a missed sample is harmless because the next pass republishes anything
// still different from the last published state.
func PublishTickers(ctx context.Context, db Pool, log *slog.Logger, cl *kgo.Client) error {
	t := time.NewTicker(tickerPublishEvery)
	defer t.Stop()
	published := make(map[string]string)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := publishPass(ctx, db, log, cl, published); err != nil {
				log.Warn("market: ticker publish pass", "err", err)
			}
		}
	}
}

// publishPass computes and produces one pass. The gate (tickerPass) picks
// what to send; this produces each envelope synchronously and records the
// published state only after the broker acked it, so a failed produce is
// retried wholesale on the next pass.
func publishPass(ctx context.Context, db Pool, log *slog.Logger, cl *kgo.Client, published map[string]string) error {
	now := time.Now().UTC()
	markets, err := knownMarkets(ctx, db)
	if err != nil {
		return err
	}
	tickers := make(map[string]events.Ticker, len(markets))
	for _, m := range markets {
		t, err := ComputeTicker(ctx, db, m, now)
		if err != nil {
			return err
		}
		tickers[m] = t
	}
	for _, emit := range tickerPass(now, tickers, published) {
		// ProduceSync's promise wait does not unblock on a canceled
		// context, so each produce carries its own deadline: a wedged
		// broker fails this pass (warned, retried next tick) and the
		// loop keeps answering ctx.Done within the bound.
		pctx, cancel := context.WithTimeout(ctx, produceDeadline)
		err := cl.ProduceSync(pctx, &kgo.Record{
			Topic: events.TopicTicker, Key: []byte(emit.env.Key), Value: emit.body,
		}).FirstErr()
		cancel()
		if err != nil {
			return fmt.Errorf("publish ticker %s: %w", emit.env.Key, err)
		}
	}
	return nil
}

// produceDeadline bounds one ProduceSync wait; the pass retries on the
// next tick, so a timed-out publish only delays that sample.
const produceDeadline = 10 * time.Second
