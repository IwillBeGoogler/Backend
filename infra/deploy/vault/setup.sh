#!/bin/bash
# Idempotent Vault bootstrap. Needs `vault` CLI, VAULT_ADDR and a privileged VAULT_TOKEN (run by an operator, not CI).
#   ENV=production SERVER_IP=203.0.113.10 vault/setup.sh
#   same host as Vault: SERVER_CIDRS=203.0.113.10/32,172.30.0.0/24 ENV=production SERVER_IP=203.0.113.10 vault/setup.sh
# Run once per env (production, dev). Optional: IMAGE_REPOS (repos that build and deploy) (space separated), JWT_AUD, AUDIT_LOG, SKIP_AUDIT=1
set -euo pipefail
: "${ENV:?production|dev}" "${SERVER_IP:?server public IP}"
# Where AppRole logins may come from. Vault on the same host behind nginx: the host itself (public IP) and the
# containers (docker network iwillbegoogler, they reach https://vault... via the public IP without NAT).
SERVER_CIDRS=${SERVER_CIDRS:-$SERVER_IP/32}
IMAGE_REPOS=${IMAGE_REPOS:-"IwillBeGoogler/Web IwillBeGoogler/Admin IwillBeGoogler/Backend"}
JWT_AUD=${JWT_AUD:-iwillbegoogler-vault}   # = jwtGithubAudience in cd.yml
AUDIT_LOG=${AUDIT_LOG:-/var/log/vault/audit.log}
here=$(cd "$(dirname "$0")" && pwd)
ROLE=iwillbegoogler-$ENV
render() { sed "s/@ENV@/$ENV/g" "$here/policies/$1"; }

# secrets engines + auth methods
vault secrets list -format=json | grep -q '"kv/"' || vault secrets enable -path=kv -version=2 kv
vault auth list -format=json | grep -q '"approle/"' || vault auth enable approle
vault auth list -format=json | grep -q '"jwt/"' || vault auth enable jwt
[ "${SKIP_AUDIT:-0}" = 1 ] || vault audit list -format=json | grep -q '"file/"' || vault audit enable file file_path="$AUDIT_LOG"

# policies
render approle-read.hcl.tpl | vault policy write "$ROLE" -
render ci-deploy.hcl.tpl | vault policy write "$ROLE-ci" -
vault policy write iwillbegoogler-ci-images "$here/policies/ci-images.hcl"

# AppRole: read-only, secret_id bound to server IP, short tokens
vault write "auth/approle/role/$ROLE" \
	token_policies="$ROLE" token_ttl=5m token_max_ttl=10m \
	token_bound_cidrs="$SERVER_CIDRS" secret_id_bound_cidrs="$SERVER_CIDRS" \
	secret_id_ttl=168h secret_id_num_uses=0 token_num_uses=0
echo "role_id (VAULT_ROLE_ID in the GitHub '$ENV' environment): $(vault read -field=role_id "auth/approle/role/$ROLE/role-id")"

# GitHub OIDC
vault write auth/jwt/config oidc_discovery_url=https://token.actions.githubusercontent.com bound_issuer=https://token.actions.githubusercontent.com
vault write "auth/jwt/role/$ROLE-ci" - <<JSON
{"role_type":"jwt","user_claim":"repository","bound_audiences":["$JWT_AUD"],
 "bound_claims_type":"string",
 "bound_claims":{"repository":[$(echo "$IMAGE_REPOS" | tr ' ' '\n' | sed 's/.*/"&"/' | paste -sd, -)],"ref":"refs/heads/main","environment":"$ENV"},
 "token_policies":["$ROLE-ci"],"token_ttl":"10m","token_max_ttl":"15m"}
JSON
# Image builds of the app repos (web/admin/backend): main -> production, dev -> dev. Shared by both envs.
repos_json=$(echo "$IMAGE_REPOS" | tr ' ' '\n' | sed 's/.*/"&"/' | paste -sd, -)
vault write auth/jwt/role/iwillbegoogler-ci-images - <<JSON
{"role_type":"jwt","user_claim":"repository","bound_audiences":["$JWT_AUD"],
 "bound_claims_type":"glob",
 "bound_claims":{"repository":[$repos_json],"ref":["refs/heads/main","refs/heads/dev"],"environment":["production","dev"]},
 "token_policies":["iwillbegoogler-ci-images"],"token_ttl":"10m","token_max_ttl":"15m"}
JSON
echo "done. Seed secrets: deploy/vault/seed-from-env.sh. KV path: kv/iwillbegoogler/$ENV"
