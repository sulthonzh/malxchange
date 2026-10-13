// Package outbox is the transactional-outbox relay (ADR-0002; the
// hand-rolled pattern from research/kafka-outbox.md):
//
//   - Enqueue inserts the event row in the caller's domain transaction
//   - the relay claims due rows with FOR UPDATE SKIP LOCKED, publishes them
//     to Redpanda, and marks them published only after broker ack
//   - LISTEN/NOTIFY wakes the relay on insert; a slow poll is the safety net
//   - rows stuck in 'publishing' (relay crash) are reclaimed after a timeout
//   - exhausted retries land in 'failed' (the DLQ surface) for inspection
//   - published rows are pruned on a schedule
//
// Delivery is at-least-once; consumers dedupe via internal/inbox.
package outbox

import (
	"context"
	"log/slog"
	"math/rand"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/syaikhipin/malxchange/internal/events"
)

// Publisher publishes one event payload; the Kafka implementation lives in
// internal/kafka (franz-go, acks=all, idempotent producer).
type Publisher interface {
	Publish(ctx context.Context, topic, key string, payload []byte) error
}

// Outbox is the enqueue side, used inside domain transactions.
type Outbox struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Outbox { return &Outbox{db: db} }

// Enqueue writes an event row inside the caller's transaction; the payload
// must already be a marshaled events.Envelope (events.New). The outbox row
// id equals the envelope id — that pair is what consumers dedupe on — so a
// payload that is not a valid envelope fails the enqueue loudly.
func (o *Outbox) Enqueue(ctx context.Context, tx pgx.Tx, topic, key string, payload []byte) error {
	e, err := events.Decode(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO outbox (id, topic, key, payload) VALUES ($1, $2, $3, $4)`,
		e.ID, topic, key, payload)
	return err
}

// Relay is the publish side. Run it once per process, next to the pool.
type Relay struct {
	DB             *pgxpool.Pool
	Pub            Publisher
	BatchSize      int           // rows claimed per wake (default 50)
	MaxAttempts    int           // before status=failed (default 10)
	RetryBase      time.Duration // first retry delay (default 1s)
	StaleAfter     time.Duration // publishing rows older than this get reclaimed (default 2m)
	PruneAge       time.Duration // published rows older than this get deleted (default 7d)
	PollInterval   time.Duration // safety-net poll (default 1s); NOTIFY is the fast path
	ProduceTimeout time.Duration // per-publish bound (default 30s); retries then flow through the outbox backoff
	Log            *slog.Logger
}

func NewRelay(db *pgxpool.Pool, pub Publisher) *Relay {
	return &Relay{
		DB: db, Pub: pub,
		BatchSize:      50,
		MaxAttempts:    10,
		RetryBase:      time.Second,
		StaleAfter:     2 * time.Minute,
		PruneAge:       7 * 24 * time.Hour,
		PollInterval:   time.Second,
		ProduceTimeout: 30 * time.Second,
		Log:            slog.Default(),
	}
}

// Run blocks until ctx is done, relaying events. A listener failure (or a
// listener-less environment) degrades to pure polling.
func (r *Relay) Run(ctx context.Context) error {
	notify := r.listen(ctx)
	poll := time.NewTicker(r.PollInterval)
	prune := time.NewTicker(time.Hour)
	defer poll.Stop()
	defer prune.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-notify:
		case <-poll.C:
		case <-prune.C:
			if err := r.prune(ctx); err != nil {
				r.Log.Warn("outbox: prune", "err", err)
			}
			continue
		}
		if err := r.reclaim(ctx); err != nil {
			r.Log.Warn("outbox: reclaim", "err", err)
		}
		for {
			n, err := r.drainBatch(ctx)
			if err != nil {
				r.Log.Warn("outbox: batch", "err", err)
				break
			}
			if n < r.BatchSize {
				break // queue drained; wait for the next wake
			}
		}
	}
}

// listen watches the outbox NOTIFY channel; a nil channel on failure.
func (r *Relay) listen(ctx context.Context) <-chan struct{} {
	out := make(chan struct{}, 1)
	conn, err := r.DB.Acquire(ctx)
	if err != nil {
		r.Log.Warn("outbox: listener disabled (acquire)", "err", err)
		return out
	}
	go func(acq *pgxpool.Conn) {
		defer acq.Release()
		if _, err := acq.Exec(ctx, `LISTEN outbox`); err != nil {
			r.Log.Warn("outbox: listener disabled (listen)", "err", err)
			return
		}
		for {
			ntf, err := acq.Conn().WaitForNotification(ctx)
			if err != nil {
				if ctx.Err() == nil {
					r.Log.Warn("outbox: listener stopped", "err", err)
				}
				return
			}
			_ = ntf
			select {
			case out <- struct{}{}:
			default:
			}
		}
	}(conn)
	return out
}

// reclaim resets rows stuck publishing (relay died between claim and mark).
func (r *Relay) reclaim(ctx context.Context) error {
	tag, err := r.DB.Exec(ctx,
		`UPDATE outbox SET status='pending', claimed_at=NULL
		 WHERE status='publishing' AND claimed_at < now() - $1::interval`,
		r.StaleAfter)
	if err != nil {
		return err
	}
	if n := tag.RowsAffected(); n > 0 {
		r.Log.Warn("outbox: reclaimed stale publishing rows", "count", n)
	}
	return nil
}

type claimed struct {
	ID        string    `db:"id"`
	Topic     string    `db:"topic"`
	Key       string    `db:"key"`
	Payload   []byte    `db:"payload"`
	Attempts  int       `db:"attempts"`
	CreatedAt time.Time `db:"created_at"`
}

func (r *Relay) drainBatch(ctx context.Context) (int, error) {
	rows, err := r.DB.Query(ctx,
		`UPDATE outbox SET status='publishing', claimed_at=now()
		 WHERE id IN (
		     SELECT id FROM outbox
		     WHERE status='pending' AND next_attempt_at <= now()
		     ORDER BY created_at
		     LIMIT $1
		     FOR UPDATE SKIP LOCKED
		 )
		 RETURNING id, topic, key, payload, attempts, created_at`, r.BatchSize)
	if err != nil {
		return 0, err
	}
	batch, err := pgx.CollectRows(rows, pgx.RowToStructByPos[claimed])
	if err != nil {
		return 0, err
	}
	// UPDATE ... RETURNING does not promise the subquery's ORDER BY, but
	// same-key events must publish in enqueue order (consumers rely on
	// per-key sequencing), so restore it before publishing.
	sort.Slice(batch, func(i, j int) bool { return batch[i].CreatedAt.Before(batch[j].CreatedAt) })
	if err != nil {
		return 0, err
	}
	for _, row := range batch {
		if err := r.publish(ctx, row); err != nil {
			r.Log.Warn("outbox: publish", "id", row.ID, "err", err)
		}
	}
	return len(batch), nil
}

func (r *Relay) publish(ctx context.Context, row claimed) error {
	pctx, cancel := context.WithTimeout(ctx, r.ProduceTimeout)
	defer cancel()
	err := r.Pub.Publish(pctx, row.Topic, row.Key, row.Payload)
	if err == nil {
		_, err = r.DB.Exec(ctx,
			`UPDATE outbox SET status='published', published_at=now(), last_error=NULL WHERE id=$1`, row.ID)
		return err
	}
	// Failure: back off, or park in failed (the DLQ surface) after
	// MaxAttempts. row.Attempts is the count before this try.
	delay := Backoff(r.RetryBase, r.MaxAttempts, time.Now().UnixNano(), int64(row.Attempts))
	_, uerr := r.DB.Exec(ctx,
		`UPDATE outbox
		    SET attempts = attempts + 1,
		        status = CASE WHEN attempts + 1 >= $2 THEN 'failed' ELSE 'pending' END,
		        next_attempt_at = now() + $3::interval,
		        claimed_at = NULL,
		        last_error = $4
		 WHERE id = $1`,
		row.ID, r.MaxAttempts, delay, truncate(err.Error(), 512))
	if uerr != nil {
		r.Log.Error("outbox: mark failure", "id", row.ID, "err", uerr)
	}
	return err
}

func (r *Relay) prune(ctx context.Context) error {
	tag, err := r.DB.Exec(ctx,
		`DELETE FROM outbox WHERE status='published' AND published_at < now() - $1::interval`, r.PruneAge)
	if err != nil {
		return err
	}
	if n := tag.RowsAffected(); n > 0 {
		r.Log.Info("outbox: pruned published rows", "count", n)
	}
	return nil
}

// Backoff returns the retry delay before attempt n+1: base * 2^n with
// jitter, capped at 5 minutes.
func Backoff(base time.Duration, maxAttempts int, seed, attempts int64) time.Duration {
	if attempts < 0 {
		attempts = 0
	}
	d := base
	for i := int64(0); i < attempts && d < backoffCap; i++ {
		d *= 2
	}
	// jitter ±25%, then a hard cap so redelivery stays bounded.
	rnd := rand.New(rand.NewSource(seed ^ attempts*2654435761))
	jitter := 0.75 + 0.5*rnd.Float64()
	d = time.Duration(float64(d) * jitter)
	if d > backoffCap {
		d = backoffCap
	}
	return d
}

const backoffCap = 5 * time.Minute

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
