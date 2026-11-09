#!/bin/bash
# HashiCorp Vault on this host under systemd (raft storage, loopback only; the host nginx terminates TLS,
# deploy/proxy/nginx/vault.conf). Run as root, idempotent:
#   VAULT_DOMAIN=vault.iwillbegoogler.uz deploy/vault/install-server.sh
# First run initialises (3 key shares, threshold 2) into /root/vault-init.json (0600) and unseals.
# MOVE THAT FILE OFF THE SERVER (password manager), then shred it: it holds the unseal keys and the root token.
# After every reboot Vault is sealed: unseal with 2 keys (`VAULT_ADDR=http://127.0.0.1:8200 vault operator unseal`),
# then `systemctl restart iwillbegoogler-secrets` (no TPM / cloud KMS here for auto-unseal).
set -euo pipefail
: "${VAULT_DOMAIN:?}"
[ "$(id -u)" = 0 ] || { echo "run as root" >&2; exit 1; }
export VAULT_ADDR=http://127.0.0.1:8200

if ! command -v vault >/dev/null; then
	apt-get update -qq
	apt-get install -y -qq gpg curl lsb-release
	curl -fsSL https://apt.releases.hashicorp.com/gpg | gpg --dearmor --yes -o /usr/share/keyrings/hashicorp-archive-keyring.gpg
	codename=$(lsb_release -cs)
	curl -fsI "https://apt.releases.hashicorp.com/dists/$codename/Release" >/dev/null 2>&1 || codename=bookworm
	echo "deb [signed-by=/usr/share/keyrings/hashicorp-archive-keyring.gpg] https://apt.releases.hashicorp.com $codename main" \
		>/etc/apt/sources.list.d/hashicorp.list
	apt-get update -qq
	apt-get install -y -qq vault
fi

install -d -o vault -g vault -m 0700 /opt/vault/data
install -d -o vault -g vault -m 0750 /var/log/vault
cat >/etc/vault.d/vault.hcl <<HCL
# managed by iwillbegoogler deploy/vault/install-server.sh
ui            = false
disable_mlock = true # recommended with integrated storage (raft)
api_addr      = "https://$VAULT_DOMAIN"
cluster_addr  = "http://127.0.0.1:8201"

storage "raft" {
  path    = "/opt/vault/data"
  node_id = "iwillbegoogler-1"
}

# Loopback only; TLS is done by the host nginx. X-Forwarded-For is trusted from nginx (127.0.0.1) only, so
# AppRole secret_id/token CIDR binding sees the real client address.
listener "tcp" {
  address                               = "127.0.0.1:8200"
  cluster_address                       = "127.0.0.1:8201"
  tls_disable                           = true
  x_forwarded_for_authorized_addrs      = "127.0.0.1/32"
  x_forwarded_for_reject_not_authorized = false
  x_forwarded_for_reject_not_present    = false
}
HCL
chown root:vault /etc/vault.d/vault.hcl
chmod 0640 /etc/vault.d/vault.hcl
systemctl enable -q vault
systemctl restart vault
for _ in $(seq 30); do
	code=$(curl -s -o /dev/null -w '%{http_code}' "$VAULT_ADDR/v1/sys/health" || true)
	case $code in 200 | 429 | 472 | 473 | 501 | 503) break ;; esac
	sleep 1
done

if [ "$(vault status -format=json | jq -r .initialized)" != true ]; then
	umask 077
	vault operator init -key-shares=3 -key-threshold=2 -format=json >/root/vault-init.json
	echo "initialised: /root/vault-init.json (unseal keys + root token). Move it off the server and shred it."
fi
if [ "$(vault status -format=json | jq -r .sealed)" = true ]; then
	if [ -r /root/vault-init.json ]; then
		# key via stdin to the API, never argv
		for i in 0 1; do jq -c "{key: .unseal_keys_b64[$i]}" /root/vault-init.json | curl -fsS -X PUT --data-binary @- "$VAULT_ADDR/v1/sys/unseal" >/dev/null; done
		echo "unsealed"
	else
		echo "Vault is sealed: run 'VAULT_ADDR=$VAULT_ADDR vault operator unseal' twice with your keys" >&2
	fi
fi
vault status | grep -E 'Initialized|Sealed|Version'
