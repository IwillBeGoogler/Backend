#!/bin/bash
# One-time server setup (run as root from a repo checkout):
#   VAULT_ADDR=https://vault.example:8200 VAULT_ROLE_ID=<approle role_id> KV_PATH=kv/iwillbegoogler/production \
#   DEPLOY_USER=deploy DEPLOY_DIR=/opt/iwillbegoogler [VAULT_CACERT_FILE=ca.pem] [REQUIRE_TPM=1] deploy/server/install.sh
# Needs systemd >= 250 (systemd-creds), e.g. Debian 12/13, Ubuntu 24.04.
#
# secret_id protection (systemd-creds --with-key):
#   TPM2 present -> host+tpm2: the .cred opens only on this machine with this disk (disk/snapshot theft is useless).
#   no TPM2      -> host: key in /var/lib/systemd/credential.secret (root only); a full disk image still opens it.
#                   REQUIRE_TPM=1 refuses to install instead. Hetzner Cloud vServers have no vTPM; providers that do:
#                   GCP Shielded VM, Azure Trusted Launch, AWS NitroTPM, or a dedicated server with (f)TPM enabled.
# Also creates the fixed docker network `iwillbegoogler` (172.30.0.0/24, gateway 172.30.0.1) that the host's
# Postgres/Redis/MongoDB listen on (deploy/server/datastores.sh).
set -euo pipefail
: "${VAULT_ADDR:?}" "${VAULT_ROLE_ID:?}" "${KV_PATH:?}" "${DEPLOY_USER:?}" "${DEPLOY_DIR:?}"
[ "$(id -u)" = 0 ] || { echo "run as root" >&2; exit 1; }
here=$(cd "$(dirname "$0")" && pwd)
id "$DEPLOY_USER" >/dev/null
command -v systemd-creds >/dev/null || { echo "systemd-creds missing (systemd >= 250 needed)" >&2; exit 1; }
for b in curl jq rsync; do command -v "$b" >/dev/null || { apt-get update -qq && apt-get install -y -qq "$b"; }; done
command -v docker >/dev/null || { echo "install Docker Engine + compose v2 first" >&2; exit 1; }
case $VAULT_ADDR in https://*) ;; *) echo "VAULT_ADDR must be https://" >&2; exit 1 ;; esac

# TPM2: systemd-analyze has-tpm2 prints "yes" when firmware, driver and system all support it
if [ -e /dev/tpmrm0 ] && [ "$(systemd-analyze has-tpm2 2>/dev/null | head -1)" = yes ]; then
	creds_key=host+tpm2
else
	[ "${REQUIRE_TPM:-0}" = 1 ] && { echo "no usable TPM2 (/dev/tpmrm0) and REQUIRE_TPM=1" >&2; exit 1; }
	creds_key=host
	echo "WARNING: no TPM2 on this machine: the secret_id is encrypted with the host key only (see header)." >&2
fi

install -m 0755 "$here/iwillbegoogler-secrets" /usr/local/sbin/iwillbegoogler-secrets
install -m 0644 "$here/iwillbegoogler-secrets.service" /etc/systemd/system/iwillbegoogler-secrets.service
install -d -m 0700 /etc/iwillbegoogler
printf '%s\n' "$VAULT_ROLE_ID" >/etc/iwillbegoogler/role_id
chmod 0600 /etc/iwillbegoogler/role_id
cacert=""
if [ -n "${VAULT_CACERT_FILE:-}" ]; then
	install -m 0644 "$VAULT_CACERT_FILE" /etc/iwillbegoogler/vault-ca.pem
	cacert=/etc/iwillbegoogler/vault-ca.pem
fi
cat >/etc/iwillbegoogler/secrets.conf <<CONF
VAULT_ADDR=$VAULT_ADDR
KV_PATH=$KV_PATH
DEPLOY_USER=$DEPLOY_USER
CREDS_KEY=$creds_key
APP_UID=65532
${cacert:+VAULT_CACERT=$cacert}
CONF
chmod 0600 /etc/iwillbegoogler/secrets.conf

tmp=$(mktemp)
sed "s/@DEPLOY_USER@/$DEPLOY_USER/" "$here/sudoers.tpl" >"$tmp"
visudo -cf "$tmp" >/dev/null || { echo "bad sudoers" >&2; exit 1; }
install -m 0440 "$tmp" /etc/sudoers.d/iwillbegoogler
rm -f "$tmp"

docker network inspect iwillbegoogler >/dev/null 2>&1 ||
	docker network create --driver bridge --subnet 172.30.0.0/24 --gateway 172.30.0.1 iwillbegoogler >/dev/null

install -d -o "$DEPLOY_USER" -m 0755 "$DEPLOY_DIR"
systemctl daemon-reload
systemctl enable iwillbegoogler-secrets.service
echo "ok (secret_id key: $creds_key). Next: first deploy via CI stores the secret_id and renders the env;"
echo "then deploy/server/datastores.sh installs Postgres/Redis(/MongoDB) under systemd with passwords from Vault."
