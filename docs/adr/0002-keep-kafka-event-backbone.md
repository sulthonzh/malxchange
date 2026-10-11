# Keep Kafka as the event backbone

The Go rewrite inherits the Java system's Kafka usage — order flow into the matching engine, trade/depth fanout to market data, and wallet operations — and we decided to keep Kafka rather than swap brokers. The event backbone needs ordered, replayable, multi-consumer streams and Go's client ecosystem (`franz-go`) is mature; replacing the broker would re-solve delivery semantics and ordering for no gain. Kafka runs in **KRaft mode** (no ZooKeeper container). Kafka vs Redpanda as the broker *binary* is deferred to an install-design ticket — the code targets the Kafka API either way.

## Considered options

- **NATS JetStream / Redis Streams**: lighter install, but different delivery and ordering semantics — a rewrite of solved problems, rejected.
- **Postgres-only queue**: viable at toy scale, weak for market-data fanout with many consumers, rejected.

## Consequences

- Fund-moving events (`withdraw`, `reset-member-address`) are bare at-most-once sends in the Java code. The Go rewrite adds a **transactional outbox** in Postgres (event written in the same transaction as the state change, relay publishes) and **idempotent consumers**; the old fire-and-forget pattern does not carry over.
- Topics are redesigned per bounded context instead of the ~10 ad-hoc JSON-string topics.
- One `kafka` (KRaft) container joins the install compose stack; ZooKeeper does not.
