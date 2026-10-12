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
    make test-profile-probe # health-check the chain nodes (run before chain debugging)

Services run against dev infra with `go run ./cmd/api` (and friends),
passing the same `MAL_*` env the containers get — read them from
`deploy/.env`:

    set -a; . deploy/.env; set +a
    MAL_PG_HOST=127.0.0.1 MAL_KAFKA_SEEDS=127.0.0.1:9092 MAL_PG_PASSWORD=$MAL_PG_PASSWORD_API go run ./cmd/api

### Chain test-profile gotchas

Each of these cost real debugging time; check them before theorising:

- **Recreate, then probe.** A panicked ltcd (e.g. the v0.23.6 versionbits
  crash on `generate`) latches: every later RPC returns garbage like
  "Server is already CPU mining" even though nothing is mining. Only a
  container recreate clears it — `make test-profile-probe` against a
  freshly recreated node is the only trustworthy reading.
- **Trust `docker compose ps`, not host listeners.** On macOS/colima the
  loopback ports (5432, 9092, …) are held by the colima `ssh` mux, so
  `lsof` shows an `ssh` listener even when the container is down or
  replaced — a stale forward serves connection resets that look like a
  dead Postgres.
- **Pinned node misbehaves → check upstream tags first.** Before local
  theories about a chain-node bug, list the upstream releases
  (`api.github.com/repos/<owner>/<repo>/tags`) and diff the pinned source
  in the Go module cache — the ltcd panic was upstream, and no newer tag
  existed to bump to.

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
`order.completed`, `cancel.rejected`, `market.plate`, `market.updated`;
market → `ticker`.
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

## Exchange engine (issue #21)

`cmd/exchange` now runs the real fold: order.command in; trade.fill /
order.completed / cancel.rejected / market.plate / market.updated out;
per-Market Books recovered from Postgres at boot with the initial Depth
published.
Internal status (admin view lands with #25):

    docker run --rm --network malxchange_default curlimages/curl:latest \
        -s http://exchange:8080/internal/engine/status

## Market data projection (issues #29–#34)

`cmd/market` consumes `trade.fill` / `market.plate` into the derived
market schema: sanitized trades, last-known Depth, and the rolling 24h
Ticker published on the `ticker` topic at most once per Market per
second (zero-Fill Markets included, honestly empty). Internal REST
answers the same values api will proxy — `GET /internal/trades/{market}`,
`GET /internal/depth/{market}`, `GET /internal/ticker/{market}`, and the
all-markets map `GET /internal/tickers`. Market config arrives on
`market.updated` (ticket #33) and everything in the schema is rebuildable
by replay (ticket #34).

A corrupted projection is a routine operation, never surgery:
`POST /internal/rebuild` (from inside the compose network, like every
internal endpoint) wipes the derived tables and the inbox, rewinds the
consumer group to the earliest retained offset, and the ordinary consume
loop replays `trade.fill` / `market.plate` / `market.updated` back to an
identical state — the drill the integration tests run verbatim. The
endpoint is idempotent: a retry after a partial failure is always safe.

Two deliberate edges of the read surface: the trades and Kline listings
accept an explicit `limit` of 1–1000 only (absent = their defaults, 50
trades / 500 bars), and `GET /internal/ticker/{market}` answers an empty
summary — 200, honestly empty last/range, zero turnover — for a Market
nobody knows yet, never a 404.
