package market

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/syaikhipin/malxchange/internal/events"
)

// Topics is everything the projection consumes — its whole input, in the
// order the service subscribes to it. The rebuild rewinds exactly these.
var Topics = []string{events.TopicTradeFill, events.TopicMarketPlate, events.TopicMarketUpdated}

// Projector couples the ordinary consume loop with the internal rebuild
// seam (ticket #34), so replay is NEVER a second write implementation:
// Consume is the same poll → applyFetches loop the service always ran,
// and Rebuild only decides where that loop consumes from. One replica
// owns this projector (the single-writer stance every consumer here
// takes), which is what makes rewinding its consumer group safe.
type Projector struct {
	db  Pool
	log *slog.Logger
	cl  consumerClient

	// mu serializes the whole consume cycle (poll → apply → commit)
	// against Rebuild's rewind+wipe. The franz-go SetOffsets guidance is
	// to never run it concurrent with polling or committing, and a batch
	// polled before a rewind but committed after it would re-commit head
	// offsets over the wiped tables — a crash mid-replay then resumes at
	// the head and permanently skips the replay. Polling runs inside the
	// lock on a short deadline (pollWait), so an idle loop cannot starve
	// the rebuild endpoint.
	mu sync.Mutex
}

// consumerClient is the franz-go surface the projector drives; *kgo.Client
// satisfies it. The seam exists so the poll/rewind/commit ordering
// contract is pinnable with a scripted client (rebuild_test.go).
type consumerClient interface {
	PollRecords(ctx context.Context, maxPollRecords int) kgo.Fetches
	SetOffsets(offsets map[string]map[int32]kgo.EpochOffset)
	MarkCommitRecords(recs ...*kgo.Record)
	CommitMarkedOffsets(ctx context.Context) error
}

// NewProjector wires one projector around the consumer the run harness
// started; the same client must be the one Consume polls.
func NewProjector(db Pool, log *slog.Logger, cl consumerClient) *Projector {
	return &Projector{db: db, log: log, cl: cl}
}

// Consume is the projection loop: poll → decode → one transaction
// (inbox dedupe + derived rows) → commit offsets. Events for a Market ride
// the Market symbol as record key, so per-Market order is total on the
// single-partition topics. Any apply error is fatal by design (same
// contract as the exchange engine and the wallet): the process restarts
// and Redpanda re-delivers from the committed offset.
//
// The WHOLE cycle runs under p.mu, shared with Rebuild's rewind+wipe:
// a poll can never be in flight while the group is rewound, and a batch
// polled before a rewind always commits before it — otherwise that batch
// would re-commit head offsets over the wiped tables and a crash mid-
// replay would permanently skip the replay. pollWait bounds the idle
// poll so a quiet loop releases the mutex and the rebuild endpoint
// stays responsive (the single-replica stance permits the full
// serialization; one replica owns this projector).
func (p *Projector) Consume(ctx context.Context) error {
	for {
		p.mu.Lock()
		pollCtx, cancel := context.WithTimeout(ctx, pollWait)
		fetches := p.cl.PollRecords(pollCtx, 500)
		cancel()
		if fetches.IsClientClosed() || ctx.Err() != nil {
			p.mu.Unlock()
			return nil
		}
		err := applyFetches(ctx, p.db, p.log, p.cl, fetches)
		p.mu.Unlock()
		if err != nil {
			return err
		}
	}
}

// pollWait bounds one in-loop poll: an idle PollRecords blocks until
// records arrive, and the poll now runs under p.mu, so without a deadline
// a quiet topic would hold the lock forever and starve the rebuild
// endpoint. A timed-out empty poll is just another quiet loop turn (the
// drain precedent in the integration harness).
const pollWait = time.Second

// Rebuild is the drill (ticket #34): rewind the consumer group to the
// earliest retained offset, then wipe every derived row AND the inbox —
// the inbox must go too, or the replay would dedupe every event into a
// no-op. The ordinary Consume loop then replays trade.fill,
// market.plate and market.updated through the very same handlers, and
// the natural keys (one trade per fill id, one Kline row per bucket,
// Depth by side replacement, the Market copy upsert) make the replay
// converge to the identical state.
//
// Ordering and safety:
//   - The rewind runs BEFORE the wipe. A rewind without a wipe is a
//     harmless no-op (the inbox dedupes the whole replay); a wipe without
//     a rewind would strand an empty projection with offsets at the head.
//     Crash between the two, or a failed wipe: retry the endpoint, it is
//     idempotent.
//   - A batch in flight when the rewind arrives COMMITS before it: the
//     loop's whole cycle (poll → apply → commit) and the rewind+wipe
//     share one mutex (see Consume), so SetOffsets can never land
//     between a batch's poll and its commit and no poll runs
//     concurrently with the rewind — a pre-rewind batch can never
//     re-commit head offsets over the wiped tables.
//   - The Ticker carries no rebuild state: it is a pure fold over the
//     trades (spec decision, ticket #32), so the next publish pass and
//     the REST endpoints re-derive it from the replayed history.
func (p *Projector) Rebuild(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	start := kgo.NewOffset().AtStart().WithEpoch(-1).EpochOffset()
	rewind := make(map[string]map[int32]kgo.EpochOffset, len(Topics))
	for _, topic := range Topics {
		// Partition count is 1 everywhere (v1 single-replica stance,
		// per-topic total ordering) — partition 0 is the whole topic.
		rewind[topic] = map[int32]kgo.EpochOffset{0: start}
	}
	// Partitions the group has not consumed yet are skipped by the
	// client; those begin at ConsumeStartOffset, which defaults to the
	// earliest offset — so every topic ends up replaying from the start.
	p.cl.SetOffsets(rewind)
	p.log.Info("market: rebuild rewound consumer group to the earliest offset",
		"topics", len(Topics))

	// The wipe: every derived table plus the inbox, in one transaction.
	// TRUNCATE is transactional in Postgres like any statement, so this
	// either lands whole or not at all.
	err := pgx.BeginFunc(ctx, p.db, func(tx pgx.Tx) error {
		for _, table := range []string{"trades", "depth_levels", "klines", "markets", "inbox"} {
			if _, err := tx.Exec(ctx, "TRUNCATE TABLE "+table); err != nil {
				return fmt.Errorf("wipe %s: %w", table, err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("market rebuild: %w (safe to retry — the rewind alone replays into the pre-wipe state)", err)
	}
	p.log.Info("market: rebuild wiped the derived state and the inbox")
	return nil
}
