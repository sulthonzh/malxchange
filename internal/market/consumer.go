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

// parkedEnvelope is one malformed or contract-violating record. Parking
// RETAINS the poison: the row in the inbox (under its park key) is the
// record of what was dropped and why, deduped on redelivery exactly like
// an applied event — never silently lost, never retried forever.
type parkedEnvelope struct {
	key    string
	id     string
	reason string
}

func applyFetches(ctx context.Context, db Pool, log *slog.Logger, cl *kgo.Client, fetches kgo.Fetches) error {
	var batch []decoded
	var parks []parkedEnvelope
	var recs []*kgo.Record
	for rec := range fetches.RecordsAll() {
		recs = append(recs, rec)
		env, err := events.Decode(rec.Value)
		if err != nil {
			// Malformed producer output parks: retrying a bad envelope
			// forever would wedge the group, but dropping it silently is
			// unrequested data loss — so it is retained in the inbox.
			parks = append(parks, parkedEnvelope{
				key:    "park:" + rec.Topic,
				id:     fmt.Sprintf("%d/%d", rec.Partition, rec.Offset),
				reason: err.Error(),
			})
			continue
		}
		d, err := payloadOf(env)
		if err != nil {
			// Undecodable or contract-violating payload: parked the same
			// way, so one poisoned message can neither stop the projection
			// nor vanish.
			parks = append(parks, parkedEnvelope{
				key:    "park:" + env.Type,
				id:     env.ID,
				reason: err.Error(),
			})
			continue
		}
		batch = append(batch, d)
	}
	if len(recs) == 0 {
		return nil
	}

	// ONE transaction: inbox rows (dedupe) + derived writes. An event that
	// fails to apply aborts the batch and is fatal — restart + idempotent
	// redelivery (ADR-0010 discipline). Parked envelopes ride the same
	// transaction: their inbox row IS the retention record.
	err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		for _, p := range parks {
			if _, err := inbox.Ensure(ctx, tx, p.key, p.id); err != nil {
				return fmt.Errorf("park %s %s: %w", p.key, p.id, err)
			}
		}
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
	for _, p := range parks {
		log.Error("market: parked malformed envelope", "key", p.key, "id", p.id, "reason", p.reason)
	}

	// Offsets commit only after the transaction (ADR-0010).
	cl.MarkCommitRecords(recs...)
	return cl.CommitMarkedOffsets(ctx)
}
