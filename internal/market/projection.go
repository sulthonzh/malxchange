package market

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/syaikhipin/malxchange/internal/events"
	"github.com/syaikhipin/malxchange/internal/money"
)

// The projection: consumed trade.fill / market.plate envelopes land in the
// derived schema, all inside the caller's transaction and behind the inbox
// dedupe (ADR-0002). Everything here is rebuildable state — dropping the
// schema and replaying the topics regenerates it.

// payloadOf decodes and validates one envelope's payload. A payload that
// cannot be decoded or violates its contract is a parked message (the
// consumer logs it and commits the offset); it never enters a transaction.
func payloadOf(env events.Envelope) (decoded, error) {
	switch env.Type {
	case events.TopicTradeFill:
		var f events.TradeFill
		if err := json.Unmarshal(env.Data, &f); err != nil {
			return decoded{}, fmt.Errorf("trade.fill payload: %w", err)
		}
		if err := validateFill(f); err != nil {
			return decoded{}, err
		}
		return decoded{env: env, fill: &f}, nil
	case events.TopicMarketPlate:
		var p events.Plate
		if err := json.Unmarshal(env.Data, &p); err != nil {
			return decoded{}, fmt.Errorf("market.plate payload: %w", err)
		}
		if err := validatePlate(p); err != nil {
			return decoded{}, err
		}
		return decoded{env: env, plate: &p}, nil
	case events.TopicMarketUpdated:
		var m events.MarketUpdated
		if err := json.Unmarshal(env.Data, &m); err != nil {
			return decoded{}, fmt.Errorf("market.updated payload: %w", err)
		}
		if err := validateMarketUpdated(m); err != nil {
			return decoded{}, err
		}
		return decoded{env: env, updated: &m}, nil
	default:
		return decoded{}, fmt.Errorf("unexpected topic %s", env.Type)
	}
}

type decoded struct {
	env     events.Envelope
	fill    *events.TradeFill
	plate   *events.Plate
	updated *events.MarketUpdated
}

// inboxKey namespaces the dedupe pair per event kind, matching the wallet's
// convention ("fill:"+market on trade.fill). Market config rides
// "cfg:"+market, so per-Market config order is total on the symbol key.
func (d decoded) inboxKey() string {
	if d.plate != nil {
		return "plate:" + d.plate.Market
	}
	if d.updated != nil {
		return "cfg:" + d.updated.Market
	}
	return "fill:" + d.fill.Market
}

// validateFill checks the trade.fill payload contract: a Market, a known
// taker side, and ADR-0005 decimal strings for the money values. Member and
// Order identifiers are ignored here by design — they never reach the
// derived schema.
func validateFill(f events.TradeFill) error {
	if f.Market == "" {
		return fmt.Errorf("trade.fill payload: empty market")
	}
	if f.TakerSide != "buy" && f.TakerSide != "sell" {
		return fmt.Errorf("trade.fill payload: unknown taker side %q", f.TakerSide)
	}
	for name, v := range map[string]string{
		"price": f.Price, "amount": f.Amount, "turnover": f.Turnover,
	} {
		if _, err := money.Parse(v); err != nil {
			return fmt.Errorf("trade.fill payload: %s: %w", name, err)
		}
	}
	return nil
}

// validatePlate checks the market.plate payload contract (a side-wise
// partial snapshot: whichever sides changed, best first).
func validatePlate(p events.Plate) error {
	if p.Market == "" {
		return fmt.Errorf("market.plate payload: empty market")
	}
	for _, side := range []struct {
		name   string
		levels []events.PlateLevel
	}{
		{"bids", p.Bids}, {"asks", p.Asks},
	} {
		for _, lv := range side.levels {
			if _, err := money.Parse(lv.Price); err != nil {
				return fmt.Errorf("market.plate payload: %s price: %w", side.name, err)
			}
			if _, err := money.Parse(lv.Amount); err != nil {
				return fmt.Errorf("market.plate payload: %s amount: %w", side.name, err)
			}
		}
	}
	return nil
}

// applyEnvelope applies one fresh (inbox-deduped) envelope to the derived
// schema inside the caller's transaction.
func applyEnvelope(ctx context.Context, tx pgx.Tx, d decoded) error {
	switch {
	case d.fill != nil:
		return applyFill(ctx, tx, d.env, *d.fill)
	case d.plate != nil:
		return applyPlate(ctx, tx, d.env, *d.plate)
	case d.updated != nil:
		return applyMarketUpdated(ctx, tx, d.env, *d.updated)
	default:
		return nil
	}
}

// validateMarketUpdated checks the market.updated payload contract
// (ticket #29): a Market with its coins and zone, non-negative scales, and
// ADR-0005 decimal strings for the money values — "" means unbounded for
// the collar and limit fields and is legal only there.
func validateMarketUpdated(m events.MarketUpdated) error {
	if m.Market == "" {
		return fmt.Errorf("market.updated payload: empty market")
	}
	for name, v := range map[string]string{
		"baseCoin": m.BaseCoin, "quoteCoin": m.QuoteCoin, "zone": m.Zone,
	} {
		if v == "" {
			return fmt.Errorf("market.updated payload: empty %s", name)
		}
	}
	if m.BaseScale < 0 {
		return fmt.Errorf("market.updated payload: negative baseScale")
	}
	if m.QuoteScale < 0 {
		return fmt.Errorf("market.updated payload: negative quoteScale")
	}
	for name, v := range map[string]string{
		"tickSize": m.TickSize, "lotSize": m.LotSize,
		"makerFeeRate": m.MakerFeeRate, "takerFeeRate": m.TakerFeeRate,
		"minPrice": m.MinPrice, "maxPrice": m.MaxPrice,
		"minAmount": m.MinAmount, "maxAmount": m.MaxAmount,
		"minTurnover": m.MinTurnover,
	} {
		if v == "" {
			continue // unbounded (collar and limit fields)
		}
		if _, err := money.Parse(v); err != nil {
			return fmt.Errorf("market.updated payload: %s: %w", name, err)
		}
	}
	return nil
}

// applyFill projects one Fill into a sanitized Trade row and folds it into
// every stored interval's Kline bar (ticket #31). A Trade is the
// market-wide record of a Fill pair (GLOSSARY): the Fill's Member and Order
// identifiers stay on trade.fill and are dropped here; the fill ID (the
// envelope ID) is kept so clients can dedupe on reconnect (ADR-0011). The
// Kline bars carry the same discipline — market-wide OHLCV only.
func applyFill(ctx context.Context, tx pgx.Tx, env events.Envelope, f events.TradeFill) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO trades (fill_id, market, taker_side, price, amount, turnover, traded_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		env.ID, f.Market, f.TakerSide, f.Price, f.Amount, f.Turnover, env.OccurredAt)
	if err != nil {
		return fmt.Errorf("insert trade %s: %w", env.ID, err)
	}
	return foldKlines(ctx, tx, env.OccurredAt, f)
}

// applyPlate merges a side-wise partial Depth publish into the Market's
// last-known full Depth: a side present in the publish replaces that side's
// levels; an absent side keeps its last-known levels, so a one-side publish
// never wipes the other side.
func applyPlate(ctx context.Context, tx pgx.Tx, env events.Envelope, p events.Plate) error {
	for _, side := range []struct {
		name   string
		levels []events.PlateLevel
	}{
		{"buy", p.Bids}, {"sell", p.Asks},
	} {
		if side.levels == nil {
			continue
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM depth_levels WHERE market = $1 AND side = $2`, p.Market, side.name); err != nil {
			return fmt.Errorf("clear depth %s %s: %w", p.Market, side.name, err)
		}
		for _, lv := range side.levels {
			if _, err := tx.Exec(ctx,
				`INSERT INTO depth_levels (market, side, price, amount) VALUES ($1, $2, $3, $4)`,
				p.Market, side.name, lv.Price, lv.Amount); err != nil {
				return fmt.Errorf("insert depth %s %s@%s: %w", p.Market, side.name, lv.Price, err)
			}
		}
	}
	return nil
}

// applyMarketUpdated projects one full Market row (ticket #29) into market's
// own copy of the Market config. The event is self-contained, so the copy
// converges by replacement: an idempotent upsert keyed on the Market —
// redelivery is dropped by the inbox, config churn (a fresh envelope for a
// known Market) replaces the row in place. updated_at carries the event's
// own instant, never the consumer's wall clock, so the copy stays a pure
// function of the event stream and a rebuild converges to the identical
// row (ticket #34). No ghost rows: a Market flipped invisible stays in the
// copy, it only leaves the visible listing (the other market-data
// endpoints never gate on visibility).
func applyMarketUpdated(ctx context.Context, tx pgx.Tx, env events.Envelope, m events.MarketUpdated) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO markets (market, base_coin, quote_coin, base_scale, quote_scale,
		                     tick_size, lot_size, maker_fee_rate, taker_fee_rate,
		                     min_price, max_price, min_amount, max_amount, min_turnover,
		                     market_orders_enabled, enabled, visible, sort, zone, featured,
		                     max_open_orders, ttl_seconds, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14,
		        $15, $16, $17, $18, $19, $20, $21, $22, $23)
		ON CONFLICT (market) DO UPDATE SET
			base_coin             = EXCLUDED.base_coin,
			quote_coin            = EXCLUDED.quote_coin,
			base_scale            = EXCLUDED.base_scale,
			quote_scale           = EXCLUDED.quote_scale,
			tick_size             = EXCLUDED.tick_size,
			lot_size              = EXCLUDED.lot_size,
			maker_fee_rate        = EXCLUDED.maker_fee_rate,
			taker_fee_rate        = EXCLUDED.taker_fee_rate,
			min_price             = EXCLUDED.min_price,
			max_price             = EXCLUDED.max_price,
			min_amount            = EXCLUDED.min_amount,
			max_amount            = EXCLUDED.max_amount,
			min_turnover          = EXCLUDED.min_turnover,
			market_orders_enabled = EXCLUDED.market_orders_enabled,
			enabled               = EXCLUDED.enabled,
			visible               = EXCLUDED.visible,
			sort                  = EXCLUDED.sort,
			zone                  = EXCLUDED.zone,
			featured              = EXCLUDED.featured,
			max_open_orders       = EXCLUDED.max_open_orders,
			ttl_seconds           = EXCLUDED.ttl_seconds,
			updated_at            = EXCLUDED.updated_at`,
		m.Market, m.BaseCoin, m.QuoteCoin, m.BaseScale, m.QuoteScale,
		m.TickSize, m.LotSize, nullableNum(m.MakerFeeRate), nullableNum(m.TakerFeeRate),
		nullableNum(m.MinPrice), nullableNum(m.MaxPrice),
		nullableNum(m.MinAmount), nullableNum(m.MaxAmount), nullableNum(m.MinTurnover),
		m.MarketOrdersEnabled, m.Enabled, m.Visible, m.Sort, m.Zone, m.Featured,
		m.MaxOpenOrders, m.TTLSeconds, env.OccurredAt)
	if err != nil {
		return fmt.Errorf("upsert market %s: %w", m.Market, err)
	}
	return nil
}

// nullableNum maps the wire's "" = unbounded (ticket #29) onto the copy's
// NULL; anything else passes through as a decimal string (ADR-0005).
func nullableNum(s string) any {
	if s == "" {
		return nil
	}
	return s
}
