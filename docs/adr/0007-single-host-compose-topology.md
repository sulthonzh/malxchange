# ADR-0007: Single-host Docker Compose topology and the one-script installer

The install target is one host (map destination), and the legacy deploy was a 19-step manual sequence. We fix the container topology the spec builds against: **one Postgres, one Redpanda, Caddy at the edge, per-service containers, and no Redis** — with an idempotent bash installer that collapses the legacy's manual order into healthcheck-gated `depends_on`.

## Considered options

- **Redis for codes/sessions/rate-limits** (as legacy): rejected — ADR-0001 already made Postgres the system of record; verification codes, sessions, and rate-limit counters are tables, and the outbox wakeup is Postgres LISTEN/NOTIFY (per the Kafka/outbox research). One less stateful container to back up.
- **Nginx+certbot / Traefik at the edge**: rejected — Caddy gives auto-HTTPS and WebSocket proxying from one small Caddyfile.
- **Static SPA builds for the Nuxt apps**: rejected — ADR-0003 assumed SSR; both apps run as SSR Node containers.
- **Separate migrate job**: rejected — services run embedded golang-migrate at boot; single replica per service on one host means no migration race.
- **Chain nodes out of the install**: rejected — deposit watching must not depend on third-party RPC rate limits; btcd + ltcd ship in the same compose behind a `--with-chains` profile, default-on, with external RPC as pure config fallback.

## Consequences

- **Container list**: `caddy, postgres, redpanda, redpanda-console (admin-gated), api, exchange, market, wallet, admin-api, web (SSR), admin (SSR), btcd, ltcd (profile: chains), pg-backup (nightly pg_dump sidecar)`.
- **Postgres**: one database, one schema + one role per service (api, exchange, market, wallet, admin); healthcheck `pg_isready`; boot order is `depends_on: service_healthy` on Postgres and Redpanda.
- **Installer** (plain bash, idempotent): preflight (docker, compose plugin, disk, ports 80/443) → generate `.env` (per-service Postgres passwords, Redpanda SASL creds, session/TOTP pepper, one-time first-admin password printed once and stored root-only) → `docker compose up -d --wait` → first-run bootstrap over internal admin RPC (create first admin, seed Coins BTC/ETH/USDT and Markets BTC/USDT, ETH/USDT) → print summary (URLs, credentials location, backup path).
- **Dev story**: the same compose doubles as dev infra (`docker compose up postgres redpanda`), services run via `go run`/`nuxt dev`; a small `compose.override.yaml` exposes ports and disables Caddy. No divergent second stack.
- Redpanda runs with SASL; franz-go client config from ADR-0002/0004 is otherwise untouched.
