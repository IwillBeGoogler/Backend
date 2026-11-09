#!/bin/bash
# Called by the CD of Web, Admin and Backend on the self-hosted runner (deploy user, label iwillbegoogler-prod).
#   env: VAULT_TOKEN  token of JWT role iwillbegoogler-<env>-ci (GitHub OIDC, hashicorp/vault-action exportToken)
#        ENV_NAME     production
#        SERVICE      web | admin | backend
#        TAG          image tag to roll out (short sha); empty with ROLLBACK_STEPS
#        ROLLBACK_STEPS optional, see deploy.sh
#        SRC          Backend only: its checkout; infra/ and docker-compose.yml are synced to DEPLOY_DIR first
# Steps: issue a wrapped (60 s, single use) secret_id -> iwillbegoogler-secrets accept (stores it with systemd-creds,
# renders /run/iwillbegoogler) -> deploy.sh -> destroy the secret_ids that existed before (only after a healthy deploy).
set -euo pipefail
: "${VAULT_TOKEN:?}" "${ENV_NAME:?}" "${SERVICE:?}"
DEPLOY_DIR=${DEPLOY_DIR:-/opt/iwillbegoogler}
VAULT=${VAULT_INTERNAL:-https://172.30.0.1:8443} # server-local endpoint (deploy/server/vault-internal.sh)
CA=/etc/ssl/iwillbegoogler-vault-ca.pem
ROLE=iwillbegoogler-$ENV_NAME
umask 077
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

case $SERVICE in web) var=TAG_WEB ;; admin) var=TAG_ADMIN ;; backend) var=TAG_API ;; *) echo "bad SERVICE $SERVICE" >&2; exit 1 ;; esac
case ${TAG:-} in *[!A-Za-z0-9_.-]*) echo "bad TAG" >&2; exit 1 ;; esac
[ -n "${TAG:-}" ] || [ "${ROLLBACK_STEPS:-0}" != 0 ] || { echo "TAG or ROLLBACK_STEPS required" >&2; exit 1; }

# vcurl ARGS...: token via stdin config, never argv
vcurl() { printf 'header = "X-Vault-Token: %s"\n' "$VAULT_TOKEN" | curl -sS -K - --max-time 20 --cacert "$CA" "$@"; }

# 1. Backend owns the stack files: sync them before deploying
if [ "$SERVICE" = backend ] && [ -n "${SRC:-}" ]; then
	rsync -a --delete --exclude 'tags.env*' --exclude tags.history --exclude Backend/ "$SRC/infra/" "$DEPLOY_DIR/"
	install -d "$DEPLOY_DIR/Backend"
	install -m 0644 "$SRC/docker-compose.yml" "$DEPLOY_DIR/Backend/docker-compose.yml"
fi

# 2. secret_ids that exist now are pruned after a healthy deploy
api="$VAULT/v1/auth/approle/role/$ROLE/secret-id"
code=$(vcurl -o "$tmp/old.json" -w '%{http_code}' -X LIST "$api")
case $code in
200) jq -r '.data.keys[]' "$tmp/old.json" >"$tmp/old" ;;
404) : >"$tmp/old" ;;
*) echo "list secret_id accessors: HTTP $code" >&2; exit 1 ;;
esac

# 3. fresh secret_id, wrapped, straight into the helper
wrapped=$(vcurl -f -X POST -H 'X-Vault-Wrap-TTL: 60s' -d "{\"metadata\":\"run=${GITHUB_REPOSITORY:-manual}/${GITHUB_RUN_ID:-0}\"}" "$api" | jq -er '.wrap_info.token')
printf '%s\n' "$wrapped" | sudo -n /usr/local/sbin/iwillbegoogler-secrets accept
wrapped=""
sudo -n /usr/local/sbin/iwillbegoogler-secrets status

# 4. deploy (deploy.sh rolls back by itself on failure)
cd "$DEPLOY_DIR"
env "$var=${TAG:-}" ROLLBACK_STEPS="${ROLLBACK_STEPS:-0}" bash deploy/server/deploy.sh

# 5. healthy: the old secret_ids can go
while read -r acc; do
	[ -n "$acc" ] || continue
	vcurl -f -X POST -d "{\"secret_id_accessor\":\"$acc\"}" "$VAULT/v1/auth/approle/role/$ROLE/secret-id-accessor/destroy" >/dev/null
done <"$tmp/old"
echo "ci-deploy ok: $SERVICE ${TAG:-rollback $ROLLBACK_STEPS} ($ENV_NAME)"
