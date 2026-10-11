#!/bin/sh
# Nightly pg_dump loop: full -Fc dump at 03:00 UTC, keep MAL_BACKUP_KEEP
# (default 14) most recent dumps. Busybox-safe (no GNU date parsing).
set -eu

KEEP="${MAL_BACKUP_KEEP:-14}"
echo "pg-backup: starting (keep=${KEEP}, schedule=03:00 UTC)"

while true; do
    sleep 3600
    [ "$(date -u +%H)" != "03" ] && continue
    stamp=$(date -u +%Y%m%dT%H%M%SZ)
    out="/backups/malxchange-${stamp}.dump"
    echo "pg-backup: dumping to ${out}"
    if pg_dump --format=custom --file "${out}"; then
        echo "pg-backup: wrote ${out} ($(du -h "${out}" | cut -f1))"
        ls -1t /backups/malxchange-*.dump 2>/dev/null | tail -n +"$((KEEP + 1))" | while IFS= read -r old; do
            echo "pg-backup: pruning ${old}"
            rm -f "$old"
        done
        # skip past the rest of hour 03 so we dump once, not hourly
        sleep 3600
    else
        echo "pg-backup: FAILED ($(date -u +%FT%TZ)) — previous dumps retained" >&2
        rm -f "${out}"
    fi
done
