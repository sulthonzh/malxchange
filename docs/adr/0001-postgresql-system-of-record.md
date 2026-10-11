# PostgreSQL as the system of record

The Java/MySQL monolith is being rewritten in Go. We decided the rewrite targets **plain, self-hosted PostgreSQL** (one container in the install compose stack) instead of carrying MySQL over, and rejected Supabase as the platform despite it also being Postgres. Plain Postgres keeps the fund-handling paths vendor-neutral, fits the one-script self-hosted install, and — because Supabase is the same engine underneath — leaves the door open to layer Supabase (auth, realtime) later without a data migration.

## Considered options

- **Keep MySQL**: viable (scale comes from topology, not engine), but the rewrite is the one cheap moment to move, and Postgres buys transactional DDL, stricter constraints, and a single `NUMERIC` type we trust for balances.
- **Supabase**: rejected for now. Managed tiers put KYC-bearing user data and exchange metadata behind a third-party control plane with connection/pooling limits across many Go services; adopting its Auth would mean rewriting ucenter's flows (JWT, 2FA, KYC, SMS) anyway; self-hosting Supabase is a heavy multi-container stack that works against the one-script install goal.

## Consequences

- Storage topology, not engine, carries scalability: per-service schemas (ucenter / exchange / otc / market each own their tables), no cross-service JOINs, Kafka for cross-context events — the shared-`bizzan`-database pattern from the Java codebase does not carry over.
- Money representation is a follow-up decision: Postgres `NUMERIC` columns, with Go-side choice between `int64` minor units and `shopspring/decimal` still open.
- Go data layer conventions follow from this: `pgx` driver, migrations via `golang-migrate`/`goose`, pgbouncer once service count grows.
- The full MySQL DDL is not in this repo drop (`00_framework/sql/` holds only a patch and a trigger); the PostgreSQL schema must be recovered from the Hibernate entities / upstream dump during planning.
