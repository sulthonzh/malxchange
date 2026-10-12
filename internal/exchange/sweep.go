package exchange

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/syaikhipin/malxchange/internal/engine"
	"github.com/syaikhipin/malxchange/internal/events"
)

// SweepTTL expires resting Orders past their Market's TTL as ordinary
// cancel commands on order.command (ADR-0010: no DB-direct cancellation
// path exists). Envelope ids are deterministic per order ("ttl:<id>") so
// a restart or redelivery can never double-cancel.
func SweepTTL(ctx context.Context, e *Engine, cl *kgo.Client) error {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			rows, err := e.Pool.Query(ctx, `
				SELECT o.id, o.market, o.member_id
				FROM orders o JOIN markets m ON m.symbol = o.market
				WHERE o.status='open'
				  AND m.ttl_seconds IS NOT NULL
				  AND o.created_at + (m.ttl_seconds * interval '1 second') < now()`)
			if err != nil {
				e.Log.Warn("ttl sweep query", "err", err)
				continue
			}
			type exp struct{ id, market, member string }
			var due []exp
			for rows.Next() {
				var x exp
				if err := rows.Scan(&x.id, &x.market, &x.member); err != nil {
					break
				}
				due = append(due, x)
			}
			rows.Close()
			for _, x := range due {
				cmd := events.OrderCommand{Op: "ttl-cancel", OrderID: x.id, Member: x.member, Market: x.market}
				_, body, err := events.New("ttl:"+x.id, x.market, events.TopicOrderCommand, time.Now().UTC(), cmd)
				if err == nil {
					err = cl.ProduceSync(ctx, &kgo.Record{
						Topic: events.TopicOrderCommand, Key: []byte(x.market), Value: body,
					}).FirstErr()
				}
				if err != nil {
					e.Log.Warn("ttl cancel produce", "order", x.id, "err", err) // retried next tick? no —
					// deterministic id means a later retry is deduped safe; keep going
				}
			}
		}
	}
}

// WatchConfig live-applies market config every few seconds (ADR-0009):
// halt/resume follows `enabled`; fees, collars, and minimums take effect
// at fill/validation time straight from the cfg map; tick/lot/scale
// changes are REFUSED while orders rest (the freeze guard — admin edits
// land with #25, this is the engine-side backstop). New markets get a
// fresh book. Every accepted change is published as market.updated
// (ticket #29); a failed publish leaves memory untouched and the next
// tick retries.
func WatchConfig(ctx context.Context, e *Engine, every time.Duration) error {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			cfgs, err := LoadMarkets(ctx, e.Pool)
			if err != nil {
				e.Log.Warn("config reload", "err", err)
				continue
			}
			if err := e.applyConfig(ctx, cfgs); err != nil {
				e.Log.Warn("config apply", "err", err) // retried next tick
			}
		}
	}
}

// applyConfig diffs the reloaded configs against the live ones and
// applies accepted changes: unknown Markets get a fresh book, quantization
// moves are refused while orders rest, halt/resume follows enabled, and
// every accepted change is published as one market.updated through the
// outbox (ticket #29). The events are inserted BEFORE the in-memory
// switch, inside the same lock hold, so the durable event exists exactly
// when the change applies; a failed insert applies nothing and the next
// watch tick retries.
func (e *Engine) applyConfig(ctx context.Context, cfgs map[string]MarketCfg) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Pass 1: decide which changes are accepted, in stable Market order
	// (map iteration is random; publish order must be deterministic).
	symbols := make([]string, 0, len(cfgs))
	for symbol := range cfgs {
		symbols = append(symbols, symbol)
	}
	slices.Sort(symbols)

	var accepted []MarketCfg
	for _, symbol := range symbols {
		cfg := cfgs[symbol]
		old, known := e.cfgs[symbol]
		if !known || e.books[symbol] == nil {
			accepted = append(accepted, cfg) // new Market: full row surfaces
			continue
		}
		if old.quant() != cfg.quant() && e.openCountLocked(symbol) > 0 {
			e.Log.Error("market config edit REFUSED: quantization change with resting orders",
				"market", symbol, "open", e.openCountLocked(symbol),
				"old", old.quant(), "new", cfg.quant())
			continue // keep the old, working spec; publish nothing
		}
		if old.changed(cfg) {
			accepted = append(accepted, cfg)
		}
	}
	if len(accepted) == 0 {
		return nil
	}

	if err := pgx.BeginFunc(ctx, e.Pool, func(tx pgx.Tx) error {
		for _, cfg := range accepted {
			if err := enqueueMarketUpdated(ctx, tx, e.OB, cfg); err != nil {
				return fmt.Errorf("publish market.updated for %s: %w", cfg.Symbol, err)
			}
		}
		return nil
	}); err != nil {
		return err
	}

	// Pass 2: apply what was published.
	for _, cfg := range accepted {
		symbol := cfg.Symbol
		old, known := e.cfgs[symbol]
		if !known || e.books[symbol] == nil {
			e.books[symbol] = engine.NewBook(cfg.Spec())
			e.cfgs[symbol] = cfg
			e.ready[symbol] = true
			e.Log.Info("market loaded", "market", symbol, "enabled", cfg.Enabled)
			continue
		}
		if old.quant() != cfg.quant() {
			// allowed with an empty book: swap in a fresh book at the new spec
			e.books[symbol] = engine.NewBook(cfg.Spec())
		}
		if old.Enabled != cfg.Enabled {
			cmd := engine.Command{Halt: true}
			if cfg.Enabled {
				cmd = engine.Command{Resume: true}
			}
			e.books[symbol].Batch([]engine.Command{cmd})
			e.Log.Info("market halt state changed", "market", symbol, "enabled", cfg.Enabled)
		}
		e.cfgs[symbol] = cfg
	}
	return nil
}

func (e *Engine) openCountLocked(symbol string) int {
	st := e.books[symbol].State()
	return len(st.OpenLimit) + len(st.OpenMkt)
}

// StatusSnapshot is the internal engine status view (ADR-0010: replaces
// the legacy monitor endpoints; consumed by admin-api in #25).
type StatusSnapshot struct {
	Market     string `json:"market"`
	Ready      bool   `json:"ready"`
	Enabled    bool   `json:"enabled"`
	Halted     bool   `json:"halted"`
	OpenOrders int    `json:"openOrders"`
	LastSeq    uint64 `json:"lastSeq"`
}

func (e *Engine) Status() []StatusSnapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]StatusSnapshot, 0, len(e.books))
	for symbol, book := range e.books {
		st := book.State()
		out = append(out, StatusSnapshot{
			Market: symbol, Ready: e.ready[symbol], Enabled: e.cfgs[symbol].Enabled,
			Halted: st.Halted, OpenOrders: len(st.OpenLimit) + len(st.OpenMkt), LastSeq: st.Seq,
		})
	}
	// Map iteration order is random; callers index or diff snapshots by
	// market, so publish a stable market order.
	slices.SortFunc(out, func(a, b StatusSnapshot) int { return strings.Compare(a.Market, b.Market) })
	return out
}
