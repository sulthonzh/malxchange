#!/usr/bin/env bash
# malxchange single-host installer (spec #20, ADR-0007).
#
#   install/install.sh --domain exchange.example.com [--admin-domain ...]
#                      [--repo-root /path/to/checkout] [--without-chains]
#
# Idempotent: re-running preserves every existing secret in deploy/.env
# and converges the stack (`docker compose up -d --wait`). The first-run
# bootstrap of business data (first Employee, seed Coins BTC/ETH/USDT and
# Markets BTC/USDT + ETH/USDT) lands with the slices that own those
# tables (#22 wallet, #21 exchange, #25 admin) — each adds its seed step
# to the bootstrap phase at the bottom of this script.
set -euo pipefail

DOMAIN=""
ADMIN_DOMAIN=""
REPO_ROOT=""
WITH_CHAINS=1

say()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mWARNING:\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31mERROR:\033[0m %s\n' "$*" >&2; exit 1; }

while [ $# -gt 0 ]; do
    case "$1" in
        --domain)       DOMAIN="${2:?}"; shift 2 ;;
        --admin-domain) ADMIN_DOMAIN="${2:?}"; shift 2 ;;
        --repo-root)    REPO_ROOT="${2:?}"; shift 2 ;;
        --without-chains) WITH_CHAINS=0; shift ;;
        -h|--help)      sed -n '2,12p' "$0"; exit 0 ;;
        *) die "unknown option: $1" ;;
    esac
done

[ -n "$DOMAIN" ] || die "--domain is required (e.g. install/install.sh --domain exchange.example.com)"
ADMIN_DOMAIN="${ADMIN_DOMAIN:-admin.${DOMAIN}}"
[ -n "$REPO_ROOT" ] || REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEPLOY_DIR="${REPO_ROOT}/deploy"
ENV_FILE="${DEPLOY_DIR}/.env"
CRED_DIR="${MAL_CRED_DIR:-/root/malxchange}"
CRED_FILE="${CRED_DIR}/credentials.txt"
COMPOSE="docker compose -f ${DEPLOY_DIR}/docker-compose.yaml"

command -v openssl >/dev/null || die "openssl is required"
gen() { openssl rand -hex 24; }

# ---------------------------------------------------------------- preflight
say "preflight"
[ -S /var/run/docker.sock ] || docker info >/dev/null 2>&1 || die "docker is not reachable (is the daemon running? is your user in the docker group?)"
docker compose version >/dev/null 2>&1 || die "the docker compose plugin is required"

docker_major() { docker version --format '{{.Server.Version}}' | cut -d. -f1; }
[ "$(docker_major)" -ge 24 ] || warn "docker server $(docker version --format '{{.Server.Version}}') < 24 is untested"

free_gb() { df -k "$1" 2>/dev/null | tail -1 | awk '{print int($4/1048576)}' || true; }
DATA_ROOT="$(docker info --format '{{.DockerRootDir}}' 2>/dev/null || echo /var/lib/docker)"
AVAIL="$(free_gb "${DATA_ROOT}")"; [ -n "${AVAIL:-}" ] || AVAIL="$(free_gb /)"
[ "${AVAIL:-999}" -ge 20 ] || die "only ${AVAIL}GB free under ${DATA_ROOT} — chains alone want 100GB+; pass --without-chains if intentional"

port_free() { ! (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null; }
if ! ${COMPOSE} ps -q caddy >/dev/null 2>&1 || [ -z "$(${COMPOSE} ps -q caddy)" ]; then
    port_free 80  || die "port 80 is already in use"
    port_free 443 || die "port 443 is already in use"
fi
say "preflight ok (docker $(docker version --format '{{.Server.Version}}'), ${AVAIL}GB free)"

# ------------------------------------------------------------ .env (merge)
say "generating ${ENV_FILE} (existing secrets preserved)"
touch "${ENV_FILE}"; chmod 600 "${ENV_FILE}"
env_set() { grep -qE "^$1=" "${ENV_FILE}"; }
env_add() { # key, value — never overwrites an existing line (idempotency)
    env_set "$1" && return
    printf '%s=%s\n' "$1" "$2" >>"${ENV_FILE}"
}
env_add MAL_DOMAIN                  "${DOMAIN}"
env_add MAL_ADMIN_DOMAIN            "${ADMIN_DOMAIN}"
env_add MAL_PG_SUPERUSER            malxchange
env_add MAL_PG_SUPERUSER_PASSWORD   "$(gen)"
env_add MAL_PG_DATABASE             malxchange
env_add MAL_PG_PASSWORD_API         "$(gen)"
env_add MAL_PG_PASSWORD_EXCHANGE    "$(gen)"
env_add MAL_PG_PASSWORD_MARKET      "$(gen)"
env_add MAL_PG_PASSWORD_WALLET      "$(gen)"
env_add MAL_PG_PASSWORD_ADMIN       "$(gen)"
env_add MAL_KAFKA_USER              malxchange
env_add MAL_KAFKA_PASSWORD          "$(gen)"
env_add MAL_BTCD_RPCUSER            malxchange
env_add MAL_BTCD_RPCPASSWORD        "$(gen)"
env_add MAL_LTCD_RPCUSER            malxchange
env_add MAL_LTCD_RPCPASSWORD        "$(gen)"
env_add MAL_PEPPER                  "$(gen)"
env_add MAL_HOT_KEYSTORE_PASSPHRASE "$(gen)"
env_add MAL_BACKUP_DIR              "${DEPLOY_DIR}/backups"
env_add MAL_BACKUP_KEEP             14

mkdir -p "${CRED_DIR}"; chmod 700 "${CRED_DIR}"
if [ ! -f "${CRED_FILE}" ]; then
    say "recording generated credentials in ${CRED_FILE} (root-only)"
    {
        echo "# malxchange credentials — generated $(date -u +%FT%TZ) by install.sh"
        echo "# Keep this file secret. Re-running the installer does NOT rotate these."
        grep -E '^(MAL_PG_SUPERUSER|MAL_PG_SUPERUSER_PASSWORD|MAL_PG_PASSWORD_|MAL_KAFKA_USER|MAL_KAFKA_PASSWORD|MAL_BTCD_RPC|MAL_LTCD_RPC|MAL_PEPPER|MAL_HOT_KEYSTORE_PASSPHRASE)' "${ENV_FILE}"
    } >"${CRED_FILE}"
    chmod 600 "${CRED_FILE}"
fi
mkdir -p "${DEPLOY_DIR}/backups"

# ------------------------------------------------------------------- boot
say "building images"
(cd "${DEPLOY_DIR}" && docker compose -f docker-compose.yaml build)

PROFILE_ARGS=()
[ "${WITH_CHAINS}" = 1 ] && PROFILE_ARGS=(--profile chains)

say "starting the stack (healthcheck-gated)"
(cd "${DEPLOY_DIR}" && docker compose -f docker-compose.yaml ${PROFILE_ARGS[@]+"${PROFILE_ARGS[@]}"} up -d --wait)

# ---------------------------------------------------------------- bootstrap
# Phase: post-boot verification. Business seeding (first Employee, Coins,
# Markets) is appended here by slices #21/#22/#25 as their tables land.
say "verifying services"
FAILED=0
for svc in postgres redpanda redpanda-console api exchange market wallet admin caddy pg-backup; do
    STATE="$(cd "${DEPLOY_DIR}" && docker compose -f docker-compose.yaml ps "$svc" --format '{{.State}} {{.Health}}' 2>/dev/null | head -1)"
    case "${STATE}" in
        running*|exited*) printf '  %-16s %s\n' "$svc" "${STATE}";;
        *) printf '  %-16s %s\n' "$svc" "${STATE:-missing}"; FAILED=1 ;;
    esac
done
if [ "${WITH_CHAINS}" = 1 ]; then
    for svc in btcd ltcd; do
        STATE="$(cd "${DEPLOY_DIR}" && docker compose -f docker-compose.yaml --profile chains ps "$svc" --format '{{.State}}' 2>/dev/null | head -1)"
        printf '  %-16s %s\n' "$svc" "${STATE:-missing}"
    done
fi
[ "${FAILED}" = 0 ] || die "one or more services are unhealthy — inspect with: ${COMPOSE} ps"

# ----------------------------------------------------------------- summary
say "install complete"
cat <<SUMMARY

  member surface    https://${DOMAIN}            (web app lands with issue #24)
  admin surface     https://${ADMIN_DOMAIN}    (admin app lands with issue #25)
  broker console    ssh -L 8090:localhost:8090 <this-host>  → http://localhost:8090
  credentials       ${CRED_FILE} (root-only)
  environment       ${ENV_FILE}
  backups           ${DEPLOY_DIR}/backups (nightly 03:00 UTC, keep 14)

  Next steps:
    1. Point DNS A records for ${DOMAIN} and ${ADMIN_DOMAIN} at this host
       (Caddy issues certificates on first request).
    2. Key ceremony before accepting real deposits (issue #22 runbook).
    3. First-run business bootstrap lands with issues #21/#22/#25.

SUMMARY
