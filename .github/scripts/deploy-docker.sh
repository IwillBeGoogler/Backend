#!/usr/bin/env bash
# Backend rollout with plain docker pull / docker run (no compose). Runs on the self-hosted runner on the server
# (label iwillbegoogler-prod, deploy user). Stack neighbours (caddy, emqx, minio, smtp-relay, web, admin) are left alone.
#
#   env: VAULT_TOKEN  token of JWT role iwillbegoogler-<env>-ci (GitHub OIDC, vault-action exportToken)
#        ENV_NAME     production
#        TAG          image tag to roll out (short sha), also used for a rollback to an older tag
#        IMAGE_NAME   image prefix, e.g. ghcr.io/iwillbegoogler/iwillbegoogler (-> <prefix>-{migrate,api,worker}:<tag>)
#        GITHUB_REPOSITORY, GITHUB_RUN_ID  (secret_id metadata only)
#
# Steps: wrapped secret_id (60 s, single use) -> iwillbegoogler-secrets accept (renders /run/iwillbegoogler) ->
# pull -> migrate (one-shot) -> replace api + worker (old ones are kept stopped until both are healthy; any failure
# restarts the old ones) -> destroy the secret_ids that existed before.
set -euo pipefail
: "${VAULT_TOKEN:?}" "${ENV_NAME:?}" "${TAG:?}" "${IMAGE_NAME:?}"
case $TAG in *[!A-Za-z0-9_.-]*) echo "bad TAG" >&2; exit 1 ;; esac

VAULT=${VAULT_INTERNAL:-https://172.30.0.1:8443} # server-local endpoint (nginx vault-internal)
CA=/etc/ssl/iwillbegoogler-vault-ca.pem
ROLE=iwillbegoogler-$ENV_NAME
NETWORK=${DOCKER_NETWORK:-iwillbegoogler}
WAIT=${WAIT_TIMEOUT:-180}
umask 077
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# vcurl ARGS...: token via stdin config, never argv
vcurl() { printf 'header = "X-Vault-Token: %s"\n' "$VAULT_TOKEN" | curl -sS -K - --max-time 20 --cacert "$CA" "$@"; }

# 1. secret_ids that exist now are pruned after a healthy rollout
api="$VAULT/v1/auth/approle/role/$ROLE/secret-id"
code=$(vcurl -o "$tmp/old.json" -w '%{http_code}' -X LIST "$api")
case $code in
200) jq -r '.data.keys[]' "$tmp/old.json" >"$tmp/old" ;;
404) : >"$tmp/old" ;;
*) echo "list secret_id accessors: HTTP $code" >&2; exit 1 ;;
esac

# 2. fresh secret_id, wrapped, straight into the helper (stores it with systemd-creds, renders /run/iwillbegoogler)
wrapped=$(vcurl -f -X POST -H 'X-Vault-Wrap-TTL: 60s' -d "{\"metadata\":\"run=${GITHUB_REPOSITORY:-manual}/${GITHUB_RUN_ID:-0}\"}" "$api" | jq -er '.wrap_info.token')
printf '%s\n' "$wrapped" | sudo -n /usr/local/sbin/iwillbegoogler-secrets accept
wrapped=""
sudo -n /usr/local/sbin/iwillbegoogler-secrets status

# 3. non-secret app config (secrets stay in Vault, the app reads them with the secret_id file)
set -a
# shellcheck disable=SC1091
. /run/iwillbegoogler/env
set +a
: "${DOMAIN:?}" "${ADMIN_DOMAIN:?}" "${MAIL_FROM:?}" "${ADMIN_USERNAME:?}" "${ADMIN_EMAIL:?}"
: "${VAULT_ADDR:?}" "${VAULT_ROLE_ID:?}" "${VAULT_KV_PATH:?}"

env_args=()
addenv() { env_args+=(-e "$1=$2"); }
addenv APP_ENV production
addenv DB_MAX_CONNS 15
addenv DB_MIN_CONNS 1
addenv S3_ENDPOINT "${HOST_GATEWAY:-172.30.0.1}:9000" # MinIO runs on the host under systemd (minio.service)
addenv S3_BUCKET "${S3_BUCKET:-uploads}"
addenv S3_USE_SSL false
addenv MQTT_URL "tcp://${HOST_GATEWAY:-172.30.0.1}:1883" # EMQX runs on the host under systemd (emqx.service)
addenv MQTT_USERNAME api
addenv MQTT_PUBLIC_WS_PATH /mqtt
addenv SMTP_ADDR "${SMTP_ADDR:-smtp-relay:587}"
addenv SMTP_USERNAME "${SMTP_USERNAME:-}"
addenv SMTP_TLS "${SMTP_TLS:-none}"
addenv MAIL_FROM "$MAIL_FROM"
addenv SITE_URL "https://$DOMAIN"
addenv PUBLIC_URL "https://$DOMAIN"
addenv CORS_ORIGINS "https://$DOMAIN,https://$ADMIN_DOMAIN"
addenv SITE_NAME "${SITE_NAME:-IWillBeGoogler}"
addenv TRUST_PROXY true
# Only the edge containers on the docker network (caddy, web, admin) may set X-Real-IP: host nginx -> caddy -> api. The default
# (every private range) would let any other container, or anything on the host network, pick the address rate limits see.
addenv TRUSTED_PROXIES "${TRUSTED_PROXIES:-172.30.0.0/24}"
addenv ADMIN_USERNAME "$ADMIN_USERNAME"
addenv ADMIN_EMAIL "$ADMIN_EMAIL"
addenv METRICS_ADDR ":9090"
addenv TURNSTILE_SITE_KEY "${TURNSTILE_SITE_KEY:-}"
addenv OTEL_EXPORTER_OTLP_ENDPOINT "${OTEL_EXPORTER_OTLP_ENDPOINT:-}"
addenv GOOGLE_CLIENT_ID "${GOOGLE_CLIENT_ID:-}"
addenv GITHUB_CLIENT_ID "${GITHUB_CLIENT_ID:-}"
addenv EMQX_API_URL "http://${HOST_GATEWAY:-172.30.0.1}:18083"
addenv EMQX_API_KEY "${EMQX_API_KEY:-iwillbegoogler-api}"
addenv VAULT_ADDR "$VAULT_ADDR"
addenv VAULT_ROLE_ID "$VAULT_ROLE_ID"
addenv VAULT_KV_PATH "$VAULT_KV_PATH"
addenv VAULT_SECRET_ID_FILE /run/iwillbegoogler/secrets/vault_secret_id
addenv VAULT_CACERT "${VAULT_CACERT_PATH:-}"
addenv MIGRATE_ON_START false

common=(
	--network "$NETWORK" --read-only --tmpfs /tmp
	--security-opt no-new-privileges:true
	--log-driver json-file --log-opt max-size=10m --log-opt max-file=5
	-v /run/iwillbegoogler/secrets:/run/iwillbegoogler/secrets:ro
)

# 4. pull everything first: a registry problem must not leave a half-replaced stack
for s in migrate api worker; do docker pull -q "$IMAGE_NAME-$s:$TAG" >/dev/null; done

# 5. migrations (one-shot); forward-only, restore the pre-upgrade dump if the schema changed and a rollback is needed
docker run --rm --name "iwillbegoogler-migrate-run" "${common[@]}" "${env_args[@]}" \
	--memory 128m "$IMAGE_NAME-migrate:$TAG"

# 6. replace api + worker
wait_healthy() {
	local name=$1 end=$((SECONDS + WAIT)) st
	while :; do
		st=$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$name" 2>/dev/null || echo absent)
		case $st in healthy) return 0 ;; exited | dead | absent) return 1 ;; esac
		[ "$SECONDS" -lt "$end" ] || return 1
		sleep 3
	done
}

replaced=()
rollback() {
	echo "rollout failed, restoring the previous containers" >&2
	for s in "${replaced[@]}"; do
		docker logs --tail 40 "iwillbegoogler-$s" >&2 || true
		docker rm -f "iwillbegoogler-$s" >/dev/null 2>&1 || true
		if docker inspect "iwillbegoogler-$s-old" >/dev/null 2>&1; then
			docker rename "iwillbegoogler-$s-old" "iwillbegoogler-$s"
			docker start "iwillbegoogler-$s" >/dev/null
		fi
	done
	exit 1
}

start() { # service memory cpus extra-env...
	local s=$1 mem=$2 cpus=$3
	shift 3
	local name=iwillbegoogler-$s legacy=iwillbegoogler-$s-1 # legacy: name the compose stack gave it
	docker rm -f "$name-old" >/dev/null 2>&1 || true
	if docker inspect "$name" >/dev/null 2>&1; then
		docker rename "$name" "$name-old"
	elif docker inspect "$legacy" >/dev/null 2>&1; then
		docker rename "$legacy" "$name-old"
	fi
	if docker inspect "$name-old" >/dev/null 2>&1; then docker stop -t 30 "$name-old" >/dev/null; fi
	replaced+=("$s")
	docker run -d --name "$name" --network-alias "$s" --restart unless-stopped \
		--memory "$mem" --cpus "$cpus" "${common[@]}" "${env_args[@]}" "$@" "$IMAGE_NAME-$s:$TAG" >/dev/null || rollback
	wait_healthy "$name" || rollback
}

# 127.0.0.1:18080: the host EMQX calls /api/internal/mqtt/{auth,acl} here (Caddy blocks /api/internal from outside)
start api 512m 1.5 -e WORKER=false -p 127.0.0.1:18080:8080
start worker 384m 1
echo "healthy: api worker @ $TAG"

for s in "${replaced[@]}"; do docker rm -f "iwillbegoogler-$s-old" >/dev/null 2>&1 || true; done

# 7. healthy: the old secret_ids can go
while read -r acc; do
	[ -n "$acc" ] || continue
	vcurl -f -X POST -d "{\"secret_id_accessor\":\"$acc\"}" "$VAULT/v1/auth/approle/role/$ROLE/secret-id-accessor/destroy" >/dev/null
done <"$tmp/old"
echo "deploy ok: backend $TAG ($ENV_NAME)"
