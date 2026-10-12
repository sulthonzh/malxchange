package exchange

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/syaikhipin/malxchange/internal/engine"
	"github.com/syaikhipin/malxchange/internal/events"
	"github.com/syaikhipin/malxchange/internal/outbox"
)

// applyEvents persists one market's engine events inside the caller's
// batch transaction: order rows upsert, fill rows, and the outbox events
// (trade.fill, order.completed, cancel.rejected, market.plate). All of it
// commits or none of it does — Postgres is the fold state (ADR-0010).
func applyEvents(ctx context.Context, tx pgx.Tx, cfg MarketCfg, evs []engine.Event, ob *outbox.Outbox) error {
	spec := cfg.Spec()
	for _, ev := range evs {
		if ev.Placed != nil {
			if err := insertOrder(ctx, tx, cfg, ev.Placed); err != nil {
				return err
			}
		}
		if ev.Canceled != nil {
			if err := setOrderStatus(ctx, tx, cfg.Symbol, ev.Canceled.ID, "canceled"); err != nil {
				return err
			}
			if err := enqueueCompleted(ctx, tx, ob, cfg, ev.Canceled, "canceled"); err != nil {
				return err
			}
		}
		for _, f := range ev.Fills {
			fees := feesForFill(cfg, f)
			if err := insertFill(ctx, tx, cfg, f, fees); err != nil {
				return err
			}
			// Progress accrues on BOTH rows immediately — an open order's
			// filled_amount must never go stale (recovery reloads it).
			if err := accrueFill(ctx, tx, cfg.Symbol, f, spec); err != nil {
				return err
			}
			if err := enqueueFill(ctx, tx, ob, cfg, f, fees); err != nil {
				return err
			}
		}
		for _, done := range ev.Completed {
			if err := setOrderStatus(ctx, tx, cfg.Symbol, done.ID, "filled"); err != nil {
				return err
			}
			if err := enqueueCompleted(ctx, tx, ob, cfg, done, "filled"); err != nil {
				return err
			}
		}
		if ev.PlateBuy != nil || ev.PlateSell != nil {
			if err := enqueuePlate(ctx, tx, ob, cfg, ev.PlateBuy, ev.PlateSell); err != nil {
				return err
			}
		}
	}
	return nil
}

// accrueFill adds one fill's amount/turnover to both order rows. The buy
// side's turnover includes burned dust (BuyTurnoverRaw).
func accrueFill(ctx context.Context, tx pgx.Tx, market string, f engine.Fill, spec engine.MarketSpec) error {
	amt := spec.AmountString(f.AmountLots)
	_, err := tx.Exec(ctx, `
		UPDATE orders SET filled_amount = filled_amount + $3::numeric, turnover = turnover + $4::numeric
		WHERE market=$1 AND id=$2 AND status='open'`,
		market, f.BuyOrderID, amt, spec.MoneyString(f.BuyTurnoverRaw))
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE orders SET filled_amount = filled_amount + $3::numeric, turnover = turnover + $4::numeric
		WHERE market=$1 AND id=$2 AND status='open'`,
		market, f.SellOrderID, amt, spec.MoneyString(f.TurnoverRaw))
	return err
}

func insertOrder(ctx context.Context, tx pgx.Tx, cfg MarketCfg, o *engine.PlacedOrder) error {
	spec := cfg.Spec()
	var price *string
	if o.Kind == engine.Limit {
		s := spec.PriceString(o.PriceTicks)
		price = &s
	}
	// amount column: base amount, or the quote budget for market buys
	amt := spec.AmountString(o.AmountLots)
	if o.Kind == engine.Market && o.Side == engine.Buy {
		amt = spec.MoneyString(o.BudgetRaw)
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO orders (id, market, member_id, side, kind, price, amount, filled_amount, turnover, status, seq)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'open',$10)
		ON CONFLICT (id) DO NOTHING`,
		o.ID, cfg.Symbol, o.MemberID, o.Side.String(), o.Kind.String(), price, amt,
		spec.AmountString(o.TradedLots), spec.MoneyString(o.TurnoverRaw), int64(o.Seq))
	return err
}

// setOrderStatus flips an open order to its terminal state. Progress was
// already accrued by the fills; hitting a non-open row means the command
// stream replayed (dedupe should have prevented it).
func setOrderStatus(ctx context.Context, tx pgx.Tx, market, id, status string) error {
	tag, err := tx.Exec(ctx, `
		UPDATE orders SET status=$3, canceled_at=CASE WHEN $3='canceled' THEN now() ELSE canceled_at END
		WHERE id=$1 AND market=$2 AND status='open'`,
		id, market, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("exchange: set %s %s: order not open (duplicate command?)", status, id)
	}
	return nil
}

func insertFill(ctx context.Context, tx pgx.Tx, cfg MarketCfg, f engine.Fill, fees fillFees) error {
	spec := cfg.Spec()
	_, err := tx.Exec(ctx, `
		INSERT INTO fills (market, seq, buy_order_id, sell_order_id, buy_member_id, sell_member_id,
		                   taker_side, price, amount, turnover, buy_turnover, buy_fee, sell_fee)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT (market, seq) DO NOTHING`,
		cfg.Symbol, int64(f.Seq), f.BuyOrderID, f.SellOrderID, f.BuyMemberID, f.SellMemberID,
		f.TakerSide.String(), spec.PriceString(f.PriceTicks), spec.AmountString(f.AmountLots),
		spec.MoneyString(f.TurnoverRaw), spec.MoneyString(f.BuyTurnoverRaw),
		spec.BaseMoneyString(fees.BuyFeeRaw), spec.MoneyString(fees.SellFeeRaw))
	return err
}

func enqueueFill(ctx context.Context, tx pgx.Tx, ob *outbox.Outbox, cfg MarketCfg, f engine.Fill, fees fillFees) error {
	spec := cfg.Spec()
	data := events.TradeFill{
		Market: cfg.Symbol, Seq: f.Seq,
		BuyOrderID: f.BuyOrderID, SellOrderID: f.SellOrderID,
		BuyMemberID: f.BuyMemberID, SellMemberID: f.SellMemberID,
		TakerSide:   f.TakerSide.String(),
		Price:       spec.PriceString(f.PriceTicks),
		Amount:      spec.AmountString(f.AmountLots),
		Turnover:    spec.MoneyString(f.TurnoverRaw),
		BuyTurnover: spec.MoneyString(f.BuyTurnoverRaw),
		BuyFee:      spec.BaseMoneyString(fees.BuyFeeRaw),
		SellFee:     spec.MoneyString(fees.SellFeeRaw),
	}
	_, body, err := events.New(uuid.NewString(), cfg.Symbol, events.TopicTradeFill, time.Now().UTC(), data)
	if err != nil {
		return err
	}
	return ob.Enqueue(ctx, tx, events.TopicTradeFill, cfg.Symbol, body)
}

func enqueueCompleted(ctx context.Context, tx pgx.Tx, ob *outbox.Outbox, cfg MarketCfg, o *engine.PlacedOrder, status string) error {
	spec := cfg.Spec()
	data := events.OrderCompleted{
		Market: cfg.Symbol, OrderID: o.ID, MemberID: o.MemberID,
		Side: o.Side.String(), Kind: o.Kind.String(), Status: status,
		FilledAmount: spec.AmountString(o.TradedLots),
		Turnover:     spec.MoneyString(o.TurnoverRaw),
	}
	_, body, err := events.New(uuid.NewString(), cfg.Symbol, events.TopicOrderCompleted, time.Now().UTC(), data)
	if err != nil {
		return err
	}
	return ob.Enqueue(ctx, tx, events.TopicOrderCompleted, cfg.Symbol, body)
}

func enqueuePlate(ctx context.Context, tx pgx.Tx, ob *outbox.Outbox, cfg MarketCfg, bids, asks *engine.Plate) error {
	spec := cfg.Spec()
	data := events.Plate{Market: cfg.Symbol}
	if bids != nil {
		data.Bids = plateLevels(spec, *bids)
	}
	if asks != nil {
		data.Asks = plateLevels(spec, *asks)
	}
	_, body, err := events.New(uuid.NewString(), cfg.Symbol, events.TopicMarketPlate, time.Now().UTC(), data)
	if err != nil {
		return err
	}
	return ob.Enqueue(ctx, tx, events.TopicMarketPlate, cfg.Symbol, body)
}

func plateLevels(spec engine.MarketSpec, p engine.Plate) []events.PlateLevel {
	out := make([]events.PlateLevel, 0, len(p.Items))
	for _, it := range p.Items {
		out = append(out, events.PlateLevel{Price: spec.PriceString(it.PriceTicks), Amount: spec.AmountString(it.AmountLots)})
	}
	return out
}

// enqueueMarketUpdated publishes the full Market row on market.updated
// (ticket #29): the lawful source for market's Market copy and api's
// listings, so no consumer needs a cross-schema read. Keyed by Market
// symbol for per-Market ordering, like trade.fill.
func enqueueMarketUpdated(ctx context.Context, tx pgx.Tx, ob *outbox.Outbox, cfg MarketCfg) error {
	data := events.MarketUpdated{
		Market: cfg.Symbol, BaseCoin: cfg.BaseCoin, QuoteCoin: cfg.QuoteCoin,
		BaseScale: cfg.BaseScale, QuoteScale: cfg.QuoteScale,
		TickSize: cfg.TickSize, LotSize: cfg.LotSize,
		MakerFeeRate: cfg.MakerFee, TakerFeeRate: cfg.TakerFee,
		MinPrice: cfg.MinPrice, MaxPrice: cfg.MaxPrice,
		MinAmount: cfg.MinAmount, MaxAmount: cfg.MaxAmount,
		MinTurnover:         cfg.MinTurnover,
		MarketOrdersEnabled: cfg.MarketOrders,
		Enabled:             cfg.Enabled,
		Visible:             cfg.Visible,
		Sort:                cfg.Sort, Zone: cfg.Zone, Featured: cfg.Featured,
		MaxOpenOrders: cfg.MaxOpen, TTLSeconds: cfg.TTLSeconds,
	}
	_, body, err := events.New(uuid.NewString(), cfg.Symbol, events.TopicMarketUpdated, time.Now().UTC(), data)
	if err != nil {
		return err
	}
	return ob.Enqueue(ctx, tx, events.TopicMarketUpdated, cfg.Symbol, body)
}

// enqueueRejected publishes a placement rejection or unknown-cancel
// answer on cancel.rejected (the api service answers the Member from it).
func enqueueRejected(ctx context.Context, tx pgx.Tx, ob *outbox.Outbox, r events.CancelRejected) error {
	_, body, err := events.New(uuid.NewString(), r.Market, events.TopicCancelRejected, time.Now().UTC(), r)
	if err != nil {
		return err
	}
	return ob.Enqueue(ctx, tx, events.TopicCancelRejected, r.Market, body)
}
