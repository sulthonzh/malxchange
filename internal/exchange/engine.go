package exchange

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/syaikhipin/malxchange/internal/engine"
	"github.com/syaikhipin/malxchange/internal/events"
	"github.com/syaikhipin/malxchange/internal/inbox"
	"github.com/syaikhipin/malxchange/internal/money"
	"github.com/syaikhipin/malxchange/internal/outbox"
)

// Engine owns the per-Market Books and the fold discipline. All Book
// access happens under mu — the consumer applies batches, the config
// watcher flips halt state, and the status endpoint reads state.
type Engine struct {
	Pool *pgxpool.Pool
	Log  *slog.Logger
	OB   *outbox.Outbox

	mu    sync.Mutex
	cfgs  map[string]MarketCfg
	books map[string]*engine.Book
	ready map[string]bool
}

func NewEngine(pool *pgxpool.Pool, log *slog.Logger, ob *outbox.Outbox) *Engine {
	return &Engine{
		Pool: pool, Log: log, OB: ob,
		cfgs: map[string]MarketCfg{}, books: map[string]*engine.Book{}, ready: map[string]bool{},
	}
}

// pollCommand is one decoded command destined for a market. reject != ""
// means the submission rules refused it before the book.
type pollCommand struct {
	market  string
	cmd     engine.Command
	member  string
	orderID string
	reject  string
}

// applyBatchTx is the fold for one polled batch inside the caller's
// transaction (which also carries the inbox dedupe rows): submission
// validation, the fold through the in-memory Books, and persistence of
// order rows + fill rows + outbox events — all committing atomically.
//
// If the transaction fails, the in-memory books have already moved — the
// caller MUST treat any error as fatal and restart (recovery rebuilds
// from Postgres; buffered commands drain from Redpanda).
func (e *Engine) applyBatchTx(ctx context.Context, tx pgx.Tx, orderedMarkets []string, cmds []pollCommand) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	{
		for _, market := range orderedMarkets {
			bucket := takeCommands(cmds, market)
			cfg, ok := e.cfgs[market]
			book := e.books[market]
			if !ok || book == nil {
				continue // unknown market: recorded in inbox by the caller, never reprocessed
			}

			var valid []engine.Command
			var evs []engine.Event
			var rejections []events.CancelRejected
			reject := func(pc pollCommand, reason string) {
				rejections = append(rejections, events.CancelRejected{
					Market: market, OrderID: pc.orderID, Member: pc.member, Reason: reason,
				})
			}
			placedHere := map[string]bool{}
			for _, pc := range bucket {
				if pc.reject != "" {
					reject(pc, pc.reject)
					continue
				}
				if pc.cmd.Place != nil {
					if reason := e.validatePlace(ctx, tx, cfg, *pc.cmd.Place); reason != "" {
						reject(pc, reason)
						continue
					}
					placedHere[pc.cmd.Place.ID] = true
				}
				if pc.cmd.CancelID != "" && !book.Has(pc.cmd.CancelID) && !placedHere[pc.cmd.CancelID] {
					// unknown order → explicit cancel.rejected so api can
					// answer the Member instead of hanging
					rejections = append(rejections, events.CancelRejected{
						Market: market, OrderID: pc.cmd.CancelID, Member: pc.member,
						Reason: "order is not on the book",
					})
					continue
				}
				valid = append(valid, pc.cmd)
			}
			evs = append(evs, book.Batch(valid)...)
			if err := applyEvents(ctx, tx, cfg, evs, e.OB); err != nil {
				return fmt.Errorf("market %s: %w", market, err)
			}
			for _, r := range rejections {
				if err := enqueueRejected(ctx, tx, e.OB, r); err != nil {
					return err
				}
			}
		}
		return nil
	}
}

// takeCommands filters cmds to a market, preserving order.
func takeCommands(cmds []pollCommand, market string) []pollCommand {
	var out []pollCommand
	for _, c := range cmds {
		if c.market == market {
			out = append(out, c)
		}
	}
	return out
}

// validatePlace enforces the unified submission rules (ADR-0009): collars
// on the limit price, min/max amount everywhere, min_turnover where
// turnover is known at submission (limit at limit price, market-buy on
// budget), the market-order toggle, and the per-Member open-order cap.
// Engine quantization rules (tick/lot/scale) are enforced inside the book.
func (e *Engine) validatePlace(ctx context.Context, tx pgx.Tx, cfg MarketCfg, in engine.PlaceIn) string {
	// place info rides on the wire command; rebuild the wire view from
	// the engine input for the rule checks.
	c := placeToWire(cfg, in)
	if c == nil {
		return "" // market-buy budget paths checked below
	}
	if c.Kind == "market" && !cfg.MarketOrders {
		return "market orders are disabled on this market"
	}
	if cfg.MinAmount != "" && less(c.Amount, cfg.MinAmount) {
		return fmt.Sprintf("amount %s is below the minimum %s", c.Amount, cfg.MinAmount)
	}
	if cfg.MaxAmount != "" && less(cfg.MaxAmount, c.Amount) {
		return fmt.Sprintf("amount %s is above the maximum %s", c.Amount, cfg.MaxAmount)
	}
	if c.Kind == "limit" {
		if cfg.MinPrice != "" && less(c.Price, cfg.MinPrice) {
			return fmt.Sprintf("price %s is below the collar minimum %s", c.Price, cfg.MinPrice)
		}
		if cfg.MaxPrice != "" && less(cfg.MaxPrice, c.Price) {
			return fmt.Sprintf("price %s is above the collar maximum %s", c.Price, cfg.MaxPrice)
		}
		if cfg.MinTurnover != "" && less(mul(c.Price, c.Amount), cfg.MinTurnover) {
			return fmt.Sprintf("turnover %s × %s is below the minimum %s", c.Price, c.Amount, cfg.MinTurnover)
		}
	}
	if c.Kind == "market" && c.Side == "buy" && cfg.MinTurnover != "" && less(c.Amount, cfg.MinTurnover) {
		return fmt.Sprintf("budget %s is below the minimum turnover %s", c.Amount, cfg.MinTurnover)
	}
	if cfg.MaxOpen != nil {
		var open int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM orders WHERE market=$1 AND member_id=$2 AND status='open'`,
			cfg.Market, c.Member).Scan(&open); err == nil && open >= int(*cfg.MaxOpen) {
			return fmt.Sprintf("open order cap (%d) reached on %s", *cfg.MaxOpen, cfg.Market)
		}
	}
	return ""
}

// placeToWire reconstructs the wire command from engine input for the
// rule checks (amount/budget semantics differ per kind and side).
func placeToWire(cfg MarketCfg, in engine.PlaceIn) *events.OrderCommand {
	c := &events.OrderCommand{OrderID: in.ID, Member: in.Member, Market: cfg.Market, Side: in.Side.String(), Kind: in.Kind.String()}
	switch {
	case in.Kind == engine.Limit:
		c.Price, c.Amount = in.Price, in.Amount
	case in.Kind == engine.Market && in.Side == engine.Sell:
		c.Amount = in.Amount
	default: // market buy: Amount is the quote budget
		c.Amount = in.Amount
	}
	return c
}

// ---- decimal-string comparisons on the rule layer ----

func mustD(s string) decimal.Decimal { return money.MustParse(s) }

func less(a, b string) bool { return mustD(a).LessThan(mustD(b)) }

func mul(a, b string) string { return mustD(a).Mul(mustD(b)).String() }

// decoded is a wire command paired with its envelope id (dedupe key).
type decoded struct {
	envID string
	cmd   events.OrderCommand
}

// recordDeduped writes the inbox rows for every command in the batch (the
// caller commits them together with the domain writes) and returns only
// the fresh ones.
func recordDeduped(ctx context.Context, tx pgx.Tx, batch []decoded) ([]decoded, error) {
	var fresh []decoded
	for _, d := range batch {
		ok, err := inbox.Ensure(ctx, tx, "cmd:"+d.cmd.Market, d.envID)
		if err != nil {
			return nil, err
		}
		if ok {
			fresh = append(fresh, d)
		}
	}
	return fresh, nil
}
