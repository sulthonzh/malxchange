# ADR-0010: Engine startup and recovery — rehydrate from Postgres, engine owns its writes

The matching engine is a deterministic fold over `order.command` (validated by the proto/engine round-trip), but the legacy system kept its engine write-free — persistence happened downstream in the market service, leaving a crash window (engine matched, DB not yet updated) that legacy papered over by recomputing from detail sums and auto-refunding placements that raced warmup. We decided the `exchange` service wraps the pure engine module in a consumer loop that **persists each command batch transactionally** — order-row updates, fill rows, and outbox events in one Postgres transaction — and commits Kafka offsets only after that transaction. Postgres is then exactly the fold state at the last committed offset: nothing mid-flight is ever ambiguous, no Order can be "lost", and no deterministic-fill-id or reconciliation discipline is needed anywhere downstream.

Recovery: load each Market's open Orders from Postgres (price-time sequence from placement order), rebuild the Book, publish the initial Depth, then start consuming `order.command` at the committed offset — commands that arrived while the engine was down buffered in Redpanda and simply drain. A Halted Market still rebuilds its Book and consumes cancels; only placements are rejected. The consumer-starts-last ordering removes the race that legacy's unready gate patched, so the auto-refund of warmup placements, the silent drop of warmup cancels, and the admin-visible `ready` flag are cut (an internal per-Market status RPC — book ready, open Orders, consumer lag — replaces the legacy monitor endpoints).

TTL expiry runs as an ordinary cancel command from a sweeper in the exchange service; expired Orders land in CANCELED and there is no DB-direct cancellation path. Cancels naming an Order the engine doesn't know get an explicit `cancel.rejected` event so the api can answer the Member. v1 declares exactly one engine replica.

## Considered options

- **Write-free engine + deterministic fill IDs + inbox dedupe everywhere** (legacy shape made correct): rejected — every consumer must dedupe on fill identity and recovery needs a reconciliation pass; exactly the machinery legacy lacks, where its double-settle races live.
- **Snapshot + Kafka replay as Book truth** (event-sourced book): rejected — Redpanda would become the source of truth for the book, contradicting ADR-0001, and snapshot/offset versioning buys nothing at this scale: rebuilding a Book from open Orders is sub-second at prototype speed.

## Consequences

- Per-batch Postgres transaction caps engine throughput at roughly batch-size ÷ tx latency (~10k commands/sec at 5 ms for a 50-command batch) — far above realistic load for this exchange; larger batches or group-commit are the escape valve.
- Legacy items that die here: `OVERTIMED` (never had a writer), the `stopTrading()` stub, the unready auto-cancel, the `forceCancelOrder` DB bypass, and the `@Transient engineStatus` monitor surface.
- The WebSocket push ticket inherits: initial Depth publish after rebuild, the `market.plate` topic, and what clients see during Warmup.
- Scaling beyond one replica would need per-Market partition assignment plus the same tx/offset discipline per partition — deliberately not specced for v1.
