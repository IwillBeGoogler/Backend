#!/bin/bash
# Private CA + nginx listener https://172.30.0.1:8443 -> Vault (127.0.0.1:8200), and points the secrets helper at it.
# Run as root after install.sh and deploy/vault/install-server.sh. Idempotent; the CA is created once (10 y),
# the server cert (IP SAN 172.30.0.1) is reissued when it expires within 30 days.
set -euo pipefail
[ "$(id -u)" = 0 ] || { echo "run as root" >&2; exit 1; }
here=$(cd "$(dirname "$0")/../proxy/nginx" && pwd)
GW=${GW:-172.30.0.1}
PKI=/etc/iwillbegoogler/pki
install -d -m 0700 "$PKI"
cd "$PKI"
if [ ! -s ca.key ]; then
	openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 3650 -subj "/CN=iwillbegoogler internal CA" \
		-keyout ca.key -out ca.pem 2>/dev/null
fi
if [ ! -s vault-internal.crt ] || ! openssl x509 -checkend $((30 * 86400)) -noout -in vault-internal.crt >/dev/null; then
	openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -subj "/CN=$GW" -keyout vault-internal.key -out vault-internal.csr 2>/dev/null
	printf 'subjectAltName=IP:%s\nextendedKeyUsage=serverAuth\nkeyUsage=digitalSignature\n' "$GW" >ext.cnf
	openssl x509 -req -in vault-internal.csr -CA ca.pem -CAkey ca.key -CAcreateserial -days 825 -extfile ext.cnf -out vault-internal.crt 2>/dev/null
	rm -f vault-internal.csr ext.cnf
fi
chmod 0600 ca.key vault-internal.key
chmod 0644 ca.pem vault-internal.crt
install -m 0644 ca.pem /etc/iwillbegoogler/vault-ca.pem
install -m 0644 ca.pem /etc/ssl/iwillbegoogler-vault-ca.pem # readable by the runner (deploy user); /etc/iwillbegoogler is 0700

echo 'net.ipv4.ip_nonlocal_bind = 1' >/etc/sysctl.d/60-iwillbegoogler.conf
sysctl -q -p /etc/sysctl.d/60-iwillbegoogler.conf
install -m 0644 "$here/vault-internal.conf" /etc/nginx/sites-available/vault-internal.conf
ln -sf /etc/nginx/sites-available/vault-internal.conf /etc/nginx/sites-enabled/vault-internal.conf
nginx -t
systemctl reload nginx

conf=/etc/iwillbegoogler/secrets.conf
sed -i -e '/^VAULT_ADDR=/d' -e '/^VAULT_CACERT=/d' "$conf"
printf 'VAULT_ADDR=https://%s:8443\nVAULT_CACERT=/etc/iwillbegoogler/vault-ca.pem\n' "$GW" >>"$conf"
curl -fsS --cacert /etc/iwillbegoogler/vault-ca.pem "https://$GW:8443/v1/sys/seal-status" >/dev/null 2>&1 ||
	curl -sS -o /dev/null -w "internal vault endpoint: HTTP %{http_code}\n" --cacert /etc/iwillbegoogler/vault-ca.pem "https://$GW:8443/v1/auth/token/lookup-self"
echo "ok: helper and containers use https://$GW:8443 (CA /etc/iwillbegoogler/vault-ca.pem)"
