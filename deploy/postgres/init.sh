#!/bin/bash
# Postgres bootstrap (runs once on a fresh volume, as the superuser).
# ADR-0001 / ADR-0007: one database, one schema + one role per service.
# Grants never cross schemas, so a cross-service JOIN is impossible even
# before application code tries it. CREATE on the database lets a service
# re-assert its own schema at boot (CREATE SCHEMA IF NOT EXISTS).
set -euo pipefail

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<EOSQL
CREATE ROLE api     LOGIN PASSWORD '${MAL_PG_PASSWORD_API}';
CREATE ROLE exchange LOGIN PASSWORD '${MAL_PG_PASSWORD_EXCHANGE}';
CREATE ROLE market  LOGIN PASSWORD '${MAL_PG_PASSWORD_MARKET}';
CREATE ROLE wallet  LOGIN PASSWORD '${MAL_PG_PASSWORD_WALLET}';
CREATE ROLE admin   LOGIN PASSWORD '${MAL_PG_PASSWORD_ADMIN}';

CREATE SCHEMA api      AUTHORIZATION api;
CREATE SCHEMA exchange AUTHORIZATION exchange;
CREATE SCHEMA market   AUTHORIZATION market;
CREATE SCHEMA wallet   AUTHORIZATION wallet;
CREATE SCHEMA admin    AUTHORIZATION admin;

GRANT CREATE ON DATABASE ${POSTGRES_DB} TO api, exchange, market, wallet, admin;
EOSQL

echo "postgres init: service roles and schemas created"
