# Redpanda as the broker binary

ADR-0002 kept the Kafka-API event backbone and deferred the broker *binary* to the install-design ticket. The owner has now decided: **Redpanda**, not Apache Kafka KRaft. Redpanda speaks the Kafka protocol (franz-go and every consumer pattern from ADR-0002 carry over unchanged) while being a single C++ binary with no JVM and no ZooKeeper/KRaft controller to size — the lightest plausible footprint for the one-script, single-host install.

## Considered options

- **Apache Kafka (KRaft)**: the default; heavier container (JVM), controller quorum to configure. Rejected on install weight alone — protocol-level behavior is equivalent for our use.
- **NATS JetStream / others**: already rejected in ADR-0002 (different semantics); unchanged.

## Consequences

- Compose stack gets one `redpanda` container (Community Edition) instead of a Kafka KRaft container; optional Redpanda Console container for a broker UI.
- franz-go remains the client; topic design, outbox relay, and idempotent-consumer patterns from ADR-0002 are untouched.
- **License note**: Redpanda source is BSL 1.1, free to self-host as Community Edition; tiered storage and some enterprise features are paid. Single-host v1 uses none of those. If that ever becomes unacceptable, switching back to Apache Kafka is a container swap, not a code change.
- Verify transactional-producer behavior against Redpanda during the matching-engine prototype ([prototype ticket]) — idempotent producer is confirmed-supported; transactions have had historical caveats.
