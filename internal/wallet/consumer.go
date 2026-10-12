package wallet

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/syaikhipin/malxchange/internal/events"
)

// ConsumerGroup is the wallet's consumer group (one wallet replica:
// the sole Ledger writer).
const ConsumerGroup = "wallet-ledger"

// ConsumeLedgerTopics is the settlement loop: poll → decode → one
// transaction (inbox dedupe + Journal + Ledger) → commit offsets. Any
// apply error is fatal by design (same contract as the exchange
// engine): the process restarts and Redpanda re-delivers.
func ConsumeLedgerTopics(ctx context.Context, db pgxBeginTx, log *slog.Logger, cl *kgo.Client) error {
	for {
		fetches := cl.PollRecords(ctx, 500)
		if fetches.IsClientClosed() || ctx.Err() != nil {
			return nil
		}
		if err := applyLedgerFetches(ctx, db, log, cl, fetches); err != nil {
			return err
		}
	}
}

// pgxBeginTx is the subset needed to open settlement transactions
// (*pgxpool.Pool satisfies it).
type pgxBeginTx interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

func applyLedgerFetches(ctx context.Context, db pgxBeginTx, log *slog.Logger, cl *kgo.Client, fetches kgo.Fetches) error {
	var envs []events.Envelope
	var recs []*kgo.Record
	for rec := range fetches.RecordsAll() {
		recs = append(recs, rec)
		env, err := events.Decode(rec.Value)
		if err != nil {
			// Malformed producer output parks (offset commits, logged).
			log.Error("wallet: dropping malformed envelope", "topic", rec.Topic, "err", err)
			continue
		}
		envs = append(envs, env)
	}
	if len(recs) == 0 {
		return nil
	}

	err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		for _, env := range envs {
			if err := SettleEnvelope(ctx, tx, env); err != nil {
				return fmt.Errorf("settle %s %s: %w", env.Type, env.ID, err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("wallet ledger batch: %w (fatal — restarting, redelivery is idempotent)", err)
	}

	cl.MarkCommitRecords(recs...)
	return cl.CommitMarkedOffsets(ctx)
}

// jsonUnmarshal is json.Unmarshal (kept for symmetry with the exchange
// consumer's decode errors).
func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }
