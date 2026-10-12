package market

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/syaikhipin/malxchange/internal/events"
	"github.com/syaikhipin/malxchange/internal/inbox"
)

// ConsumerGroup is the market service's consumer group (one market replica:
// the sole writer of the derived schema).
const ConsumerGroup = "market-projection"

// applyFetches is one projection batch: decode → one transaction (inbox
// dedupe + derived rows) → commit offsets. The projector's Consume loop
// and the integration drills drive this same function — the rebuild's
// replay path is exactly the ordinary one (ticket #34).

func applyFetches(ctx context.Context, db Pool, log *slog.Logger, cl *kgo.Client, fetches kgo.Fetches) error {
	var batch []decoded
	var recs []*kgo.Record
	for rec := range fetches.RecordsAll() {
		recs = append(recs, rec)
		env, err := events.Decode(rec.Value)
		if err != nil {
			// Malformed producer output is parked (offset commits) and
			// logged; retrying a bad envelope forever would wedge the
			// group.
			log.Error("market: dropping malformed envelope", "topic", rec.Topic, "err", err)
			continue
		}
		d, err := payloadOf(env)
		if err != nil {
			// Undecodable or contract-violating payload: park it the same
			// way, so one poisoned message cannot stop the projection.
			log.Error("market: dropping undecodable event", "topic", rec.Topic, "id", env.ID, "err", err)
			continue
		}
		batch = append(batch, d)
	}
	if len(recs) == 0 {
		return nil
	}

	// ONE transaction: inbox rows (dedupe) + derived writes. An event that
	// fails to apply aborts the batch and is fatal — restart + idempotent
	// redelivery (ADR-0010 discipline).
	err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		for _, d := range batch {
			if err := inbox.Apply(ctx, tx, d.inboxKey(), d.env.ID, func(tx pgx.Tx) error {
				return applyEnvelope(ctx, tx, d)
			}); err != nil {
				return fmt.Errorf("apply %s %s: %w", d.env.Type, d.env.ID, err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("market batch: %w (fatal — restarting, redelivery is idempotent)", err)
	}

	// Offsets commit only after the transaction (ADR-0010).
	cl.MarkCommitRecords(recs...)
	return cl.CommitMarkedOffsets(ctx)
}
