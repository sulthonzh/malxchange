# Research: Go Kafka Client & Transactional Outbox Relay Pattern

Issue: syaikhipin/cryptoexchange#7 · Input to ADR-0002 (Go + Nuxt 3 spot-exchange rewrite).
Date: 2026-10-11. No secrets contained.

---

## 1. Go Kafka client comparison

### franz-go (`twmb/franz-go`)

- **Maintenance:** Actively developed; targets Kafka 0.8.0 through v4.4+, states "If a client KIP exists, this library aims to support it" (README, https://github.com/twmb/franz-go).
- **Idempotent & transactional producer:** Full Exactly-Once Semantics (EOS), "Idempotent & transactional producers" listed as first-class features (README Features). Dedicated transaction docs: https://github.com/twmb/franz-go/blob/master/docs/transactions.md.
- **Consumer groups / rebalance:** Eager (roundrobin, range, sticky) **and cooperative-sticky** balancers, rack-aware assignment (KIP-881), and server-side balancing via the new consumer protocol (KIP-848); also share groups (KIP-932) (README Features).
- **Batching:** Records are internally batched; tuning via `kgo.ProducerBatchMaxBytes`, `kgo.ProducerLinger`, `kgo.MaxBufferedRecords`, etc. (pkg docs https://pkg.go.dev/github.com/twmb/franz-go/pkg/kgo).
- **Performance reputation:** "consistently among the fastest and most CPU and memory efficient Kafka clients in Go", avoids channels/goroutines where unnecessary; ships a benchmark harness with cross-client comparisons (README Performance; `examples/bench/compare`). Used in production by Alpaca, Benthos, Redpanda, Unity, Zomato, etc.
- **Pure Go** — no cgo, trivial cross-compilation and Alpine/static builds.

### Sarama (`IBM/sarama`, formerly `Shopify/sarama`)

- **Maintenance:** Moved to IBM/stewarded (https://github.com/IBM/sarama); still released regularly (v1.61.0, Sep 2026 per pkg.go.dev https://pkg.go.dev/github.com/IBM/sarama) with "2 releases + 2 months" Kafka/Go compatibility guarantee (README). The Shopify module path is frozen at v1.38.1 (Jan 2023).
- **Idempotent/transactional producer:** Idempotent producer and transactional producer exist (`config.Producer.Idempotent`, `config.Producer.Transaction.*`), but historically buggy/finicky — e.g. `syncProducer hangs forever when Idempotent is enabled` (https://github.com/Shopify/sarama/issues/1878), `Transactional Producer send msg always got ...` (https://github.com/IBM/sarama/issues/2611). EOS support is not considered production-grade the way franz-go's is.
- **Consumer groups / rebalance:** Eager consumer groups only; no cooperative-sticky assignment (the consumer-group protocol implementation lags modern KIPs; protocol support like TxnOffsetCommit v3 is still landing — CHANGELOG https://github.com/IBM/sarama/blob/main/CHANGELOG.md). Long processing can trigger rebalance storms (https://github.com/IBM/sarama/issues/3200).
- **Batching:** Producer batching exists but the channel/goroutine-heavy design (`async_producer.go`) is known to be less CPU/memory efficient than franz-go's.
- **Performance reputation:** Mature and battle-tested, but widely regarded as the slowest of the three and heaviest on allocations.

### confluent-kafka-go

- **Maintenance:** Actively maintained by Confluent with **commercial support**; currently requires Go 1.26+ and bundles librdkafka 2.15.0+ (README https://github.com/confluentinc/confluent-kafka-go).
- **Idempotent/transactional producer:** Inherits librdkafka's full idempotent + transactional producer (librdkafka supports EOS since v1.x); the Go side is a thin wrapper.
- **Consumer groups:** Balanced consumer groups with eager rebalance via librdkafka; librdkafka also has incremental/cooperative rebalance support ("partition.assignment.strategy=cooperative-sticky").
- **Batching:** librdkafka is heavily tuned (batching, compression, congestion control) — see performance discussion https://github.com/confluentinc/confluent-kafka-go/issues/214.
- **Performance reputation:** Typically the highest raw throughput/lowest latency of the three (it is the C library all Confluent clients share).
- **Key drawback for this repo:** **cgo**. `CGO_ENABLED` must not be 0 (README, librdkafka section); cross-compilation, Alpine (needs `-tags musl`), and glibc-static-linking all become friction points. CGO also interacts poorly with Go's scheduler (thread pinning on blocking C calls).

### Summary

| Dimension | franz-go | Sarama | confluent-kafka-go |
|---|---|---|---|
| Maintenance | Very active, single lead (twmb) + community | Active (IBM), slower feature pace | Active, commercial backing |
| Language | Pure Go | Pure Go | cgo (librdkafka) |
| Idempotent producer | Yes, mature | Yes, but buggy history (#1878) | Yes (librdkafka) |
| Transactional producer / EOS | Yes, first-class, documented | Present but unreliable (#2611) | Yes (librdkafka) |
| Cooperative rebalance | Yes (cooperative-sticky) + KIP-848 | No (eager only) | Yes (cooperative-sticky via librdkafka) |
| Batching controls | Rich, idiomatic options | Adequate | Rich (C tuning knobs) |
| Raw performance | Excellent | Good | Best |
| Deploy friction (docker, cross-compile) | None | None | cgo, musl tags, static-link pain |

**Recommendation: franz-go** for the Go rewrite — pure Go (clean Docker/K8s deploys), modern rebalancing, reliable EOS/transactions, excellent performance, active maintenance.

---

## 2. Transactional outbox relay for Postgres

All variants share the write side: the outbox row is INSERTed in the **same Postgres transaction** as the domain write (order fill, ledger entry), eliminating the dual-write anomaly (classic write-up: https://microservices.io/patterns/data/transactional-outbox.html).

### A. Polling publisher (SELECT … FOR UPDATE SKIP LOCKED)

The relay periodically runs something like:

```sql
UPDATE outbox SET status = 'processing', claimed_at = now()
WHERE id IN (
  SELECT id FROM outbox
  WHERE status = 'pending' AND next_attempt_at <= now()
  ORDER BY created_at
  LIMIT 500
  FOR UPDATE SKIP LOCKED
)
RETURNING *;
```

`FOR UPDATE` locks claimed rows; `SKIP LOCKED` lets multiple relay replicas work without blocking each other (Postgres docs: https://www.postgresql.org/docs/current/sql-select.html#SQL-FOR-UPDATE-SHARE; pattern write-up: https://www.glukhov.org/app-architecture/integration-patterns/transactional-outbox-pattern-go).

- **Ops cost:** Near zero — a goroutine in the existing Go service or a single small sidecar. No new infrastructure.
- **Latency:** Bounded by poll interval; 50–200 ms polls give acceptable p99 for a spot-exchange event fan-out. Can be reduced with a Postgres `LISTEN/NOTIFY` trigger on INSERT for near-realtime wakeup while keeping polling as the safety net.
- **Failure modes:** Relay crash mid-publish → message was possibly already sent to Kafka → **at-least-once**; mark row published only after broker ack (idempotent producer makes redelivery gap-free on the Kafka side, consumers still dedupe). Long-running publish inside the claim transaction holds row locks — best practice: claim-and-mark in one short transaction, publish outside, then update status. Polling query needs a partial index on `(status, created_at)`; table must be pruned (delete/park rows after publish).

### B. Logical replication / Debezium (+ outbox event router)

Debezium's Postgres connector streams WAL via logical decoding and the **outbox event router** SMT converts outbox-table changes directly into Kafka records with the right key/headers (docs: https://debezium.io/documentation/reference/stable/transformations/outbox-event-router.html; CDC-over-polling positioning: https://debezium.io/documentation/reference/stable/features.html).

- **Ops cost:** High — Kafka Connect cluster (JVM), connector config & monitoring, Postgres replication slot management (a stale slot can bloat the WAL and eventually break the primary), `wal_level=logical`, identity/REPLICA IDENTITY tuning. Debezium explicitly warns about slot/data-loss operational concerns.
- **Latency:** Low — typically tens of milliseconds end-to-end, no polling interval.
- **Failure modes:** Slot lag/consumption stalls; connector restarts; schema of the outbox table is coupled to connector config; an entirely new class of infrastructure to run for what is one table. Overkill unless CDC is wanted for other purposes too.

### C. Lightweight Go relay libraries

- **watermill** (https://github.com/ThreeDotsLabs/watermill, ~9.9k stars) + **watermill-kafka** (https://github.com/ThreeDotsLabs/watermill-kafka): a full message-driven framework (Pub/Sub, middlewares, CQRS helpers, forwarder for exactly this pattern — https://watermill.io/advanced/forwarder/). Powerful, but it imposes its Message envelope/router abstractions across your codebase — a large commitment for one relay.
- **outboxer** (https://github.com/italolelis/outboxer): small library specifically for the pattern (event stored + relay with in-memory/Postgres DataStore and AMQP/Exec dispatcher backends). Lighter, but low activity/stars (~164) — treat as reference code rather than a dependency.
- **Plain hand-rolled relay (~150–300 lines):** SKIP LOCKED claim loop + franz-go transactional producer + retry/backoff + metrics. Full control, no framework lock-in, no third-party maintenance risk.

**Recommendation for this repo: hand-rolled polling relay (option A) inside the Go service**, with `LISTEN/NOTIFY` wakeup and a partial index. Ops cost ≈ 0, latency sub-100 ms is achievable, failure modes are well understood and testable. Reserve Debezium for a later stage if CDC is needed beyond the outbox.

---

## 3. Idempotent consumer patterns

Because the outbox relay guarantees only **at-least-once** delivery, every consumer must be idempotent. Standard pattern:

1. **Key messages by business key** (e.g. `trade_id`, `order_id`, `ledger_entry_id`) so all events for one entity land on one Kafka partition → per-key ordering is preserved.
2. Consumer opens a **Postgres transaction** that does both:
   ```sql
   INSERT INTO processed_message (message_key, message_id)
   VALUES ($1, $2);           -- e.g. (trade:12345, outbox-uuid)
   -- UNIQUE(message_key, message_id) violation => duplicate, abort/rollback
   ```
   and applies the domain mutation. If the inbox insert violates the unique constraint, the whole transaction — including any partial domain writes — rolls back and the offset is committed as "handled".
3. Alternatively, apply the domain change keyed by the natural ID with `INSERT ... ON CONFLICT DO NOTHING` and check `xmax = 0` / affected-rows to detect a replay.

Key properties:
- Dedupe + domain write are **atomic** — no window where a crash between "recorded as processed" and "applied" creates a lost or double-applied event.
- Works for any consumer, including non-Kafka-transactional consumers (i.e. you are not relying on Kafka EOS consumer semantics, which only work with `read_committed` + transactional producers anyway).
- The inbox table needs pruning (e.g. keep 30 days) and an index on the dedupe key.

References: the at-least-once ⇒ idempotent-consumer pairing is standard microservices guidance (https://microservices.io/patterns/data/transactional-outbox.html, https://microservices.io/patterns/consumers/idempotent-consumer.html).

---

## 4. Recommendation table (for ADR-0002 / spec to cite)

| Decision | Recommendation | Runner-up / when to change | Rationale (sources above) |
|---|---|---|---|
| Kafka client library | **franz-go (`twmb/franz-go/pkg/kgo`)** | confluent-kafka-go if absolute max throughput needed and cgo is acceptable | Pure Go, full EOS + transactional producer, cooperative-sticky + KIP-848 rebalancing, top-tier perf, active maintenance (README: https://github.com/twmb/franz-go) |
| Avoid | Sarama for new EOS workloads | — | Eager-only rebalancing, idempotent/transactional producer has repeated bug history (Shopify/sarama#1878, IBM/sarama#2611) |
| Outbox relay | **Hand-rolled polling publisher, `FOR UPDATE SKIP LOCKED`, in-service goroutine, LISTEN/NOTIFY wakeup** | Debezium + outbox event router if CDC needed for other tables; watermill if adopting a full messaging framework | ~Zero new infra, ≤100 ms latency with notify, at-least-once semantics are fine given idempotent consumers (https://debezium.io/documentation/reference/stable/transformations/outbox-event-router.html, https://www.glukhov.org/app-architecture/integration-patterns/transactional-outbox-pattern-go) |
| Relay guarantees | At-least-once; franz-go idempotent producer on the relay (optionally transactional for multi-partition atomic batches) | Kafka EOS only where multi-partition atomicity is truly required | Outbox redelivery + consumer dedupe already covers duplicates (franz-go docs: https://github.com/twmb/franz-go/blob/master/docs/transactions.md) |
| Producer config | `kgo.RecordKey` = business ID; `ProducerBatchCompression` = lz4 or zstd; acks=all (default requires ISR) | — | Per-key ordering; low overhead, good ratio (README/kgo docs) |
| Consumer idempotency | **Inbox/dedupe table with `UNIQUE(message_key, message_id)` inside the same Postgres tx as the domain write** | Natural-key `ON CONFLICT DO NOTHING` for append-only state | Atomic dedupe+apply, crash-safe (https://microservices.io/patterns/consumers/idempotent-consumer.html) |
| Outbox table ops | Partial index on `(status, created_at)`; periodic prune of published rows; `next_attempt_at` backoff with DLQ status after N retries | — | Keeps claim query cheap; bounded growth |
