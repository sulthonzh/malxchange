# malxchange platform (issue #20)

The ground every other slice builds on: the compose stack, the installer,
and the shared Go infrastructure — money (ADR-0005), transactional outbox
and idempotent inbox (ADR-0002), the franz-go client wiring (ADR-0004),
embedded migrations, and the per-service Postgres schema/role isolation
(ADR-0001). No Redis anywhere.

## Everyday commands (from the repo root)

    make dev-up             # postgres + redpanda on loopback, with .env seeded
    make test               # unit tests (hermetic)
    make test-integration   # migrations/isolation/outbox-roundtrip/inbox vs dev infra
    make lint               # go vet + gofmt
    make build-images       # build the five service images
    make test-profile-up    # simnet btcd/ltcd + anvil (the E2E seam substrate)

Services run against dev infra with `go run ./cmd/api` (and friends),
passing the same `MAL_*` env the containers get — read them from
`deploy/.env`:

    set -a; . deploy/.env; set +a
    MAL_PG_HOST=127.0.0.1 MAL_KAFKA_SEEDS=127.0.0.1:9092 MAL_PG_PASSWORD=$MAL_PG_PASSWORD_API go run ./cmd/api

## Layout

| Path | What |
|---|---|
| `cmd/{api,exchange,market,wallet,admin-api}` | the five service binaries (thin: `service.Main(name, extra)`) |
| `internal/money` | decimal strings, floor rounding, scale/tick/lot conformity, int64 ticks |
| `internal/outbox` | enqueue-in-tx + relay (SKIP LOCKED, LISTEN/NOTIFY, backoff→failed, prune) |
| `internal/inbox` | `UNIQUE(message_key, message_id)` dedupe in the consumer's tx |
| `internal/kafka` | franz-go producer/consumer/admin + topic creation |
| `internal/events` | topic names, ownership map, the JSON envelope |
| `internal/pgmig` + `internal/migrations` | embedded golang-migrate per service schema |
| `deploy/` | compose files (base/dev/test), Caddyfile, postgres init, pg-backup |
| `install/install.sh` | the one-script, idempotent single-host installer |

## The environment contract

Every service reads the same `MAL_*` variables (see `internal/config`).
`deploy/.env` is generated — by the installer on a host, by `make dev-up`
locally — and never committed. Postgres runs one database with a schema +
login role per service; grants never cross schemas, so a cross-service
JOIN fails at the database, not in review.

## Topic ownership

Each service creates the topics it produces at boot (auto-create is off):
api → `order.command`, `member.register`; exchange → `trade.fill`,
`order.completed`, `cancel.rejected`, `market.plate`; market → `ticker`.
Money values inside event payloads are decimal strings, always.

## Installer (production, single host)

    sudo install/install.sh --domain exchange.example.com [--without-chains]

Preflight (docker, compose, disk, ports 80/443) → generate/preserve
`.env` secrets (root-only copy under `/root/malxchange/credentials.txt`)
→ `docker compose up -d --wait` → per-service verification → summary.
Business bootstrap (first Employee, seed Coins/Markets) is appended by
slices #21/#22/#25 as their tables land; today the phase verifies health.

The Redpanda Console is bound to `127.0.0.1:8090` only — reach it over
SSH, never through Caddy. btcd/ltcd (chains profile, default on) need
~100GB+ of disk; `--without-chains` installs without them.
