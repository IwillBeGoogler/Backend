#!/bin/bash
# Runs on the server from $DEPLOY_DIR (CD calls it after `sudo iwillbegoogler-secrets accept`).
# Optional env: TAG_WEB TAG_ADMIN TAG_API = tags to roll out; unset keeps the current ones in tags.env.
#               ROLLBACK_STEPS=N = redeploy the release N steps back in tags.history (1 = the previous one); TAG_* ignored.
# tags.env = current per-service tags, tags.env.prev = before this run (automatic rollback target on failure),
# tags.history = every healthy release, newest last (one line: TAG_WEB=.. TAG_ADMIN=.. TAG_API=..), last 30 kept.
set -euo pipefail
cd "$(dirname "$0")/../.."
ENVF=/run/iwillbegoogler/env
WAIT=${WAIT_TIMEOUT:-300}
[ -r "$ENVF" ] || { echo "$ENVF missing: run 'sudo iwillbegoogler-secrets render'" >&2; exit 1; }
touch tags.env
files=(-f docker-compose.yml -f docker-compose.prod.yml)
# host nginx owns 80/443 (deploy/server/nginx.sh): Caddy only on 127.0.0.1:8088, plain HTTP
grep -Eq "^BEHIND_NGINX='?1'?$" "$ENVF" && files+=(-f deploy/proxy/docker-compose.behind-nginx.yml)
dc() { docker compose --env-file "$ENVF" --env-file tags.env "${files[@]}" "$@"; }

touch tags.history
cp tags.env tags.env.prev
had_prev=$(grep -c . tags.env.prev || true)
if [ -n "${ROLLBACK_STEPS:-}" ] && [ "${ROLLBACK_STEPS}" != 0 ]; then
	[[ $ROLLBACK_STEPS =~ ^[0-9]+$ ]] || { echo "ROLLBACK_STEPS must be a number" >&2; exit 1; }
	n=$(grep -c . tags.history || true)
	[ "$n" -gt "$ROLLBACK_STEPS" ] || { echo "only $n releases in tags.history: cannot go $ROLLBACK_STEPS back" >&2; exit 1; }
	target=$(tail -n $((ROLLBACK_STEPS + 1)) tags.history | head -1)
	echo "manual rollback $ROLLBACK_STEPS step(s) to: $target"
	tr ' ' '\n' <<<"$target" | grep -E '^TAG_(WEB|ADMIN|API)=[A-Za-z0-9_.-]+$' >tags.env
	unset TAG_WEB TAG_ADMIN TAG_API
fi
for k in TAG_WEB TAG_ADMIN TAG_API; do
	[ -n "${!k:-}" ] || continue
	grep -v "^$k=" tags.env >tags.env.new || true
	echo "$k=${!k}" >>tags.env.new
	mv tags.env.new tags.env
done
echo "tags: $(tr '\n' ' ' <tags.env)"

rollback() {
	echo "deploy failed" >&2
	dc ps >&2 || true
	dc logs --tail=40 api >&2 || true
	if [ "$had_prev" -gt 0 ]; then
		echo "rolling back to: $(tr '\n' ' ' <tags.env.prev)" >&2
		cp tags.env.prev tags.env
		dc up -d --remove-orphans || true
		echo "note: migrations are forward-only; restore the pre-upgrade dump if the schema changed" >&2
	else
		echo "no previous tags (first deploy): nothing to roll back to" >&2
	fi
	exit 1
}

wait_healthy() {
	local end=$((SECONDS + WAIT)) svc cid st bad
	while :; do
		bad=""
		for svc in $(dc config --services); do
			case $svc in migrate | pgbackup) continue ;; esac # one-shot / 26 h start_period
			cid=$(dc ps -q "$svc")
			[ -n "$cid" ] || { bad="$bad $svc(absent)"; continue; }
			st=$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$cid")
			case $st in healthy | running) ;; *) bad="$bad $svc($st)" ;; esac
		done
		[ -z "$bad" ] && return 0
		[ "$SECONDS" -lt "$end" ] || { echo "not healthy:$bad" >&2; return 1; }
		sleep 5
	done
}

# Postgres/Redis run on the host under systemd (deploy/server/datastores.sh), not in compose.
for u in "postgresql@${PG_MAJOR:-18}-main" redis-server; do
	systemctl is-active -q "$u" || { echo "$u is not running: run 'sudo deploy/server/datastores.sh' once" >&2; exit 1; }
done
[ -e /run/iwillbegoogler/secrets/vault_secret_id ] ||
	{ echo "/run/iwillbegoogler/secrets/vault_secret_id missing: run 'sudo iwillbegoogler-secrets render'" >&2; exit 1; }

dc config -q
{
	{ [ "${SKIP_PULL:-0}" = 1 ] || dc pull web admin api worker migrate; } && # SKIP_PULL=1: images built locally
		dc build pgbackup &&
		{ dc exec -T pgbackup sh -c 'pg_dump -Fc -f /backups/blog-pre-upgrade.dump' || echo "pre-upgrade dump skipped"; } &&
		dc up -d --wait minio emqx &&
		dc run --rm --no-deps migrate &&
		dc up -d --remove-orphans &&
		wait_healthy
} || rollback

# record the healthy release (a rollback is recorded too, so ROLLBACK_STEPS=1 again = undo the rollback)
line=$(grep -E '^TAG_' tags.env | sort | tr '\n' ' ' | sed 's/ $//')
[ -z "$line" ] || [ "$(tail -1 tags.history)" = "$line" ] || echo "$line" >>tags.history
tail -n 30 tags.history >tags.history.new && mv tags.history.new tags.history
# keep images of the releases in tags.history (fast rollback); drop other unused ones older than 30 days
docker image prune -af --filter until=720h >/dev/null || true
echo "deploy ok: $line"
