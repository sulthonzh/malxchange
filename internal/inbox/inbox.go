// Package inbox implements the idempotent-consumer dedupe (ADR-0002, per
// research/kafka-outbox.md): the inbox row and the domain write happen in
// the SAME Postgres transaction, so an at-least-once redelivery either
// applies exactly once or not at all — never partially.
package inbox

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Ensure records (messageKey, messageID) as consumed inside tx. It returns
// true when the pair was new — i.e. this delivery should apply the domain
// write — and false when the pair was already consumed (a duplicate
// delivery; skip the write and commit nothing).
func Ensure(ctx context.Context, tx pgx.Tx, messageKey, messageID string) (bool, error) {
	tag, err := tx.Exec(ctx,
		`INSERT INTO inbox (message_key, message_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		messageKey, messageID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// Apply runs fn inside tx only if (messageKey, messageID) has not been
// consumed; on duplicate it is a no-op. Either way the transaction commits
// (the dedupe insert itself must persist even when fn does nothing).
func Apply(ctx context.Context, tx pgx.Tx, messageKey, messageID string, fn func(pgx.Tx) error) error {
	fresh, err := Ensure(ctx, tx, messageKey, messageID)
	if err != nil {
		return err
	}
	if !fresh {
		return nil
	}
	return fn(tx)
}
