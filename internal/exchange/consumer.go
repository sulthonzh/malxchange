package exchange

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/syaikhipin/malxchange/internal/engine"
	"github.com/syaikhipin/malxchange/internal/events"
)

// ConsumerGroup is the engine's consumer group name (one engine replica,
// ADR-0010).
const ConsumerGroup = "exchange-engine"

// ConsumeCommandTopic is the production loop: poll → decode → one tx
// (inbox dedupe + fold + persistence) → commit offsets. Any apply error
// is fatal by design: the process restarts, Postgres hands back the fold
// state, and Redpanda re-delivers from the committed offset.
func ConsumeCommandTopic(ctx context.Context, e *Engine, cl *kgo.Client) error {
	for {
		fetches := cl.PollRecords(ctx, 500)
		if fetches.IsClientClosed() || ctx.Err() != nil {
			return nil
		}
		if err := applyFetches(ctx, e, cl, fetches); err != nil {
			return err
		}
	}
}

func applyFetches(ctx context.Context, e *Engine, cl *kgo.Client, fetches kgo.Fetches) error {
	var batch []decoded
	var recs []*kgo.Record
	for rec := range fetches.RecordsAll() {
		recs = append(recs, rec)
		env, err := events.Decode(rec.Value)
		if err != nil {
			// Malformed producer output is parked (offset commits) and
			// logged; retrying a bad envelope forever would wedge the fold.
			e.Log.Error("order.command: dropping malformed envelope", "err", err)
			continue
		}
		var cmd events.OrderCommand
		if err := json.Unmarshal(env.Data, &cmd); err != nil {
			e.Log.Error("order.command: dropping undecodable command", "err", err, "id", env.ID)
			continue
		}
		batch = append(batch, decoded{envID: env.ID, cmd: cmd})
	}
	if len(recs) == 0 {
		return nil
	}

	// ONE transaction: inbox rows (dedupe), submission validation, the
	// fold through the books, order/fill rows, outbox events.
	e.Log.Info("APPLYDEBUG begin tx", "batch", len(batch), "recs", len(recs))
	err := pgx.BeginFunc(ctx, e.Pool, func(tx pgx.Tx) error {
		fresh, err := recordDeduped(ctx, tx, batch)
		if err != nil {
			return err
		}
		if len(fresh) == 0 {
			return nil
		}
		markets, cmds := toPollCommands(fresh)
		return e.applyBatchTx(ctx, tx, markets, cmds)
	})
	if err != nil {
		return fmt.Errorf("engine batch: %w (fatal — restarting to rebuild from Postgres)", err)
	}

	// Offsets commit only after the transaction (ADR-0010).
	cl.MarkCommitRecords(recs...)
	return cl.CommitMarkedOffsets(ctx)
}

// toPollCommands converts fresh wire commands to book commands, keeping
// first-seen market order. Unknown ops and unknown-cancel metadata ride
// along as pre-book rejections.
func toPollCommands(fresh []decoded) ([]string, []pollCommand) {
	var markets []string
	seen := map[string]bool{}
	var cmds []pollCommand
	for _, d := range fresh {
		c := d.cmd
		if !seen[c.Market] {
			seen[c.Market] = true
			markets = append(markets, c.Market)
		}
		pc := pollCommand{market: c.Market, member: c.Member, orderID: c.OrderID}
		switch c.Op {
		case "place":
			pc.cmd = engine.Command{Place: &engine.PlaceIn{
				ID: c.OrderID, Member: c.Member,
				Side: sideOf(c.Side), Kind: kindOf(c.Kind),
				Price: c.Price, Amount: c.Amount,
			}}
		case "cancel", "ttl-cancel":
			pc.cmd = engine.Command{CancelID: c.OrderID}
		default:
			pc.reject = fmt.Sprintf("unknown op %q", c.Op)
		}
		cmds = append(cmds, pc)
	}
	return markets, cmds
}

func sideOf(s string) engine.Side {
	if s == "sell" {
		return engine.Sell
	}
	return engine.Buy
}

func kindOf(k string) engine.Kind {
	if k == "market" {
		return engine.Market
	}
	return engine.Limit
}
