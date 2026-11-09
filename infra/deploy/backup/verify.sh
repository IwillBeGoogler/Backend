#!/bin/sh
# Restore-verification: restores a dump into a throwaway Postgres container and checks the diagram tables.
# Usage: deploy/backup/verify.sh [dump-file]   (default: newest blog-*.dump in $BACKUP_DIR or in volume pgbackups)
# Env: BACKUP_DIR (host dir with dumps; if unset the compose volume "pgbackups" is used via docker run),
#      PG_IMAGE (default postgres:18-alpine), MIN_ROWS (default 1; rows required in each table),
#      VERIFY_TABLES (default "dg_projects dg_files dg_file_versions"), VERIFY_COMPARE_LIVE=1 (also compare counts with
#      the live DB reachable by PGHOST/PGUSER/PGPASSWORD/PGDATABASE via psql; exact match is NOT expected after the dump).
# Exit 0 = ok, 1 = check failed, 2 = setup error.
set -eu

IMAGE=${PG_IMAGE:-postgres:18-alpine}
MIN=${MIN_ROWS:-1}
TABLES=${VERIFY_TABLES:-dg_projects dg_files dg_file_versions}
NAME=iwillbegoogler-verify-$$
VOL=${BACKUP_VOLUME:-pgbackups}
ARG=${1:-}

command -v docker >/dev/null || { echo "verify: docker required" >&2; exit 2; }

cleanup() { docker rm -f "$NAME" >/dev/null 2>&1 || true; }
trap cleanup EXIT INT TERM

if [ -n "${BACKUP_DIR:-}" ]; then
	MOUNT="$BACKUP_DIR:/backups:ro"
else
	MOUNT="$VOL:/backups:ro"
fi

if [ -n "$ARG" ]; then
	DUMP=/backups/$(basename "$ARG")
	if [ -f "$ARG" ] && [ -z "${BACKUP_DIR:-}" ]; then
		MOUNT="$(cd "$(dirname "$ARG")" && pwd):/backups:ro"
	fi
else
	DUMP=$(docker run --rm -v "$MOUNT" --entrypoint sh "$IMAGE" -c 'ls -1 /backups/blog-*.dump 2>/dev/null | sort | tail -n1')
	[ -n "$DUMP" ] || { echo "verify: no blog-*.dump found in $MOUNT" >&2; exit 2; }
fi
echo "verify: using $DUMP"

docker run -d --name "$NAME" -e POSTGRES_PASSWORD=verify -e POSTGRES_USER=blog -e POSTGRES_DB=verify \
	-v "$MOUNT" "$IMAGE" >/dev/null
i=0
until docker exec "$NAME" pg_isready -U blog -d verify -h 127.0.0.1 >/dev/null 2>&1; do
	i=$((i + 1))
	[ "$i" -gt 60 ] && { echo "verify: temp postgres did not start" >&2; exit 2; }
	sleep 1
done

echo "verify: restoring"
docker exec "$NAME" pg_restore -U blog -d verify --no-owner --exit-on-error "$DUMP" || { echo "verify: pg_restore FAILED" >&2; exit 1; }

rc=0
for t in $TABLES; do
	n=$(docker exec "$NAME" psql -U blog -d verify -Atc "SELECT count(*) FROM $t" 2>/dev/null || echo ERR)
	if [ "$n" = ERR ] || [ -z "$n" ]; then
		echo "verify: $t MISSING" >&2; rc=1; continue
	fi
	live=""
	if [ "${VERIFY_COMPARE_LIVE:-0}" = 1 ]; then
		live=" (live: $(psql -Atc "SELECT count(*) FROM $t" 2>/dev/null || echo n/a))"
	fi
	if [ "$n" -ge "$MIN" ]; then echo "verify: $t rows=$n ok$live"; else echo "verify: $t rows=$n < $MIN FAIL$live" >&2; rc=1; fi
done
[ "$rc" = 0 ] && echo "verify: OK" || echo "verify: FAILED" >&2
exit "$rc"
