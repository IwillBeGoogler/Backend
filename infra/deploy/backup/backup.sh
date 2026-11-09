#!/bin/sh
# Daily pg_dump (custom format) + retention + optional rclone upload.
# Env: PGHOST PGUSER PGPASSWORD PGDATABASE, BACKUP_HOUR_UTC (0-23, default 3), BACKUP_KEEP_DAYS (default 14),
#      BACKUP_RUN_NOW=1 (dump once at start), BACKUP_S3_* (upload only when BACKUP_S3_BUCKET is set).
# Blob mirror (optional, needs BACKUP_S3_BUCKET): BACKUP_MIRROR=1 copies the app object bucket (blog uploads AND diagram
#      blobs under "diagram/") to $BACKUP_S3_BUCKET/${BACKUP_MIRROR_PREFIX:-iwillbegoogler/objects}. Source: BACKUP_MIRROR_SRC_ENDPOINT
#      (e.g. http://minio:9000), BACKUP_MIRROR_SRC_ACCESS_KEY, BACKUP_MIRROR_SRC_SECRET_KEY, BACKUP_MIRROR_SRC_BUCKET (default uploads).
#      Uses `rclone copy` (never deletes remote: blobs are content-addressed and immutable).
set -eu

DIR=/backups
HOUR=${BACKUP_HOUR_UTC:-3}
KEEP=${BACKUP_KEEP_DAYS:-14}

remote_env() {
	export RCLONE_CONFIG_REMOTE_TYPE=s3
	export RCLONE_CONFIG_REMOTE_PROVIDER="${BACKUP_S3_PROVIDER:-Other}"
	export RCLONE_CONFIG_REMOTE_ENDPOINT="${BACKUP_S3_ENDPOINT:-}"
	export RCLONE_CONFIG_REMOTE_ACCESS_KEY_ID="${BACKUP_S3_ACCESS_KEY:-}"
	export RCLONE_CONFIG_REMOTE_SECRET_ACCESS_KEY="${BACKUP_S3_SECRET_KEY:-}"
	export RCLONE_CONFIG_REMOTE_REGION="${BACKUP_S3_REGION:-}"
}

# Mirror the app's object bucket (uploads + "diagram/<sha256>" blobs) to the offsite bucket.
mirror() {
	[ "${BACKUP_MIRROR:-0}" = 1 ] || return 0
	if [ -z "${BACKUP_MIRROR_SRC_ENDPOINT:-}" ]; then
		echo "backup: BACKUP_MIRROR_SRC_ENDPOINT not set; skipping blob mirror" >&2
		return 1
	fi
	export RCLONE_CONFIG_SRC_TYPE=s3
	export RCLONE_CONFIG_SRC_PROVIDER=Minio
	export RCLONE_CONFIG_SRC_ENDPOINT="$BACKUP_MIRROR_SRC_ENDPOINT"
	export RCLONE_CONFIG_SRC_ACCESS_KEY_ID="${BACKUP_MIRROR_SRC_ACCESS_KEY:-}"
	export RCLONE_CONFIG_SRC_SECRET_ACCESS_KEY="${BACKUP_MIRROR_SRC_SECRET_KEY:-}"
	mdest="remote:${BACKUP_S3_BUCKET}/${BACKUP_MIRROR_PREFIX:-iwillbegoogler/objects}"
	echo "backup: mirroring src:${BACKUP_MIRROR_SRC_BUCKET:-uploads} to $mdest"
	rclone copy "src:${BACKUP_MIRROR_SRC_BUCKET:-uploads}" "$mdest" --transfers 4 || { echo "backup: mirror FAILED" >&2; return 1; }
}

dump() {
	ts=$(date -u +%Y%m%dT%H%M%SZ)
	tmp="$DIR/.blog-$ts.dump.part"
	out="$DIR/blog-$ts.dump"
	echo "backup: dumping to $out"
	if ! pg_dump --format=custom --no-owner --compress=6 --file="$tmp"; then
		rm -f "$tmp"
		echo "backup: pg_dump FAILED" >&2
		return 1
	fi
	mv "$tmp" "$out"
	find "$DIR" -name 'blog-*.dump' -mtime +"$KEEP" -delete
	if [ -n "${BACKUP_S3_BUCKET:-}" ]; then
		remote_env
		dest="remote:${BACKUP_S3_BUCKET}/${BACKUP_S3_PREFIX:-iwillbegoogler/postgres}"
		echo "backup: uploading to $dest"
		rclone copyto "$out" "$dest/$(basename "$out")" || echo "backup: upload FAILED (local copy kept)" >&2
		# remote retention follows the same window
		rclone delete "$dest" --min-age "${KEEP}d" --include 'blog-*.dump' || true
		mirror || true
	elif [ "${BACKUP_MIRROR:-0}" = 1 ]; then
		echo "backup: BACKUP_MIRROR=1 needs BACKUP_S3_BUCKET; skipping blob mirror" >&2
	fi
	echo "backup: done"
}

# Seconds until the next HH:00 UTC.
wait_secs() {
	now=$(date -u +%s)
	today=$(date -u +%Y-%m-%d)
	target=$(date -u -d "$today $(printf '%02d' "$HOUR"):00:00" +%s 2>/dev/null || busybox date -u -D '%Y-%m-%d %H:%M:%S' -d "$today $(printf '%02d' "$HOUR"):00:00" +%s)
	[ "$target" -le "$now" ] && target=$((target + 86400))
	echo $((target - now))
}

[ "${BACKUP_RUN_NOW:-0}" = 1 ] && dump || true
while :; do
	s=$(wait_secs)
	echo "backup: next run in ${s}s"
	sleep "$s"
	dump || true
done
