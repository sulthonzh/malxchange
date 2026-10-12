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
	default:
		return decoded{}, fmt.Errorf("unexpected topic %s", env.Type)
	}
}

type decoded struct {
	env   events.Envelope
	fill  *events.TradeFill
	plate *events.Plate
}

// inboxKey namespaces the dedupe pair per event kind, matching the wallet's
// convention ("fill:"+market on trade.fill).
func (d decoded) inboxKey() string {
	if d.plate != nil {
		return "plate:" + d.plate.Market
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
	default:
		return nil
	}
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
