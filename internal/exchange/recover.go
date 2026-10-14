package exchange

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/syaikhipin/malxchange/internal/engine"
)

// Recover implements ADR-0010 startup: rebuild each Market's Book from
// open Orders in price-time (seq) order, seed the shared seq counter past
// every seq the market ever issued, apply Halted from config, and publish
// the initial Depth. The consumer starts only after this returns.
func (e *Engine) Recover(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	cfgs, err := LoadMarkets(ctx, e.Pool)
	if err != nil {
		return err
	}
	e.Log.Info("RECOVER-DEBUG", "markets", len(cfgs))
	for market, cfg := range cfgs {
		restored, lastSeq, err := loadOpenOrders(ctx, e.Pool, cfg)
		if err != nil {
			return fmt.Errorf("recover %s: %w", market, err)
		}
		book := engine.NewBook(cfg.Spec())
		if err := book.Restore(restored, lastSeq); err != nil {
			return fmt.Errorf("recover %s: %w", market, err)
		}
		if !cfg.Enabled {
			book.Batch([]engine.Command{{Halt: true}}) // cancels still flow
		}
		e.books[market] = book
		e.cfgs[market] = cfg
		e.ready[market] = true

		// Initial publishes (ADR-0010/0011, ticket #29): market's
		// last-known plate refreshes from the Depth publish (a true cold
		// start publishes an empty book), and one market.updated per
		// known Market lands right after its Book recovery — the lawful
		// source for market's Market copy and api's listings.
		st := book.State()
		if err := pgx.BeginFunc(ctx, e.Pool, func(tx pgx.Tx) error {
			if err := enqueuePlate(ctx, tx, e.OB, cfg, st.PlateBuy, st.PlateSell); err != nil {
				return err
			}
			return enqueueMarketUpdated(ctx, tx, e.OB, cfg)
		}); err != nil {
			return fmt.Errorf("recover %s: initial publish: %w", market, err)
		}
	}
	return nil
}

func loadOpenOrders(ctx context.Context, pool *pgxpool.Pool, cfg MarketCfg) ([]engine.RestoredOrder, uint64, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, member_id, side, kind,
		       COALESCE(price::text,''),
		       amount::text, filled_amount::text, turnover::text, seq
		FROM orders WHERE market=$1 AND status='open' ORDER BY seq`, cfg.Market)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []engine.RestoredOrder
	for rows.Next() {
		var r engine.RestoredOrder
		var side, kind, price, amount, filled, turnover string
		var seq int64
		if err := rows.Scan(&r.ID, &r.MemberID, &side, &kind, &price, &amount, &filled, &turnover, &seq); err != nil {
			return nil, 0, err
		}
		r.Side, r.Kind = sideOf(side), kindOf(kind)
		r.Seq = uint64(seq)
		switch {
		case r.Kind == engine.Limit:
			raw, ok := engine.ParseRaw(price, cfg.QuoteScale)
			if !ok || raw <= 0 {
				return nil, 0, fmt.Errorf("order %s: bad persisted price %q", r.ID, price)
			}
			r.PriceTicks = raw / cfg.TickRaw
			rawAmt, ok := engine.ParseRaw(amount, cfg.BaseScale)
			if !ok {
				return nil, 0, fmt.Errorf("order %s: bad persisted amount %q", r.ID, amount)
			}
			r.AmountLots = rawAmt / cfg.LotRaw
		case r.Kind == engine.Market && r.Side == engine.Buy:
			raw, ok := engine.ParseRaw(amount, cfg.QuoteScale)
			if !ok {
				return nil, 0, fmt.Errorf("order %s: bad persisted budget %q", r.ID, amount)
			}
			r.BudgetRaw = raw
		default: // market sell
			raw, ok := engine.ParseRaw(amount, cfg.BaseScale)
			if !ok {
				return nil, 0, fmt.Errorf("order %s: bad persisted amount %q", r.ID, amount)
			}
			r.AmountLots = raw / cfg.LotRaw
		}
		if raw, ok := engine.ParseRaw(filled, cfg.BaseScale); ok {
			r.TradedLots = raw / cfg.LotRaw
		}
		if raw, ok := engine.ParseRaw(turnover, cfg.QuoteScale); ok {
			r.TurnoverRaw = raw
		}
		if r.AmountLots <= 0 && r.BudgetRaw <= 0 {
			return nil, 0, fmt.Errorf("order %s: no quantity to restore", r.ID)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	var lastOrderSeq, lastFillSeq int64
	_ = pool.QueryRow(ctx, `SELECT COALESCE(max(seq),0) FROM orders WHERE market=$1`, cfg.Market).Scan(&lastOrderSeq)
	_ = pool.QueryRow(ctx, `SELECT COALESCE(max(seq),0) FROM fills WHERE market=$1`, cfg.Market).Scan(&lastFillSeq)
	last := lastOrderSeq
	if lastFillSeq > last {
		last = lastFillSeq
	}
	return out, uint64(last), nil
}
