#!/bin/bash
# Host nginx in front of the stack (when nginx already owns 80/443 for other sites). Run as root, idempotent:
#   DOMAIN=iwillbegoogler.uz ADMIN_DOMAIN=admin.iwillbegoogler.uz [VAULT_DOMAIN=vault.iwillbegoogler.uz] ACME_EMAIL=... deploy/server/nginx.sh
# Gets/extends certificates with certbot (nginx plugin), installs deploy/proxy/nginx/*.conf, disables the sites that
# served the same names before (moved to /etc/nginx/sites-disabled-iwillbegoogler/), then reloads.
# Set BEHIND_NGINX=1 in Vault (kv/iwillbegoogler/<env>) so deploy.sh binds Caddy to 127.0.0.1:8088 without TLS.
set -euo pipefail
: "${DOMAIN:?}" "${ADMIN_DOMAIN:?}" "${ACME_EMAIL:?}"
[ "$(id -u)" = 0 ] || { echo "run as root" >&2; exit 1; }
here=$(cd "$(dirname "$0")/../proxy/nginx" && pwd)
command -v nginx >/dev/null || { echo "nginx not installed" >&2; exit 1; }
command -v certbot >/dev/null || { apt-get update -qq; apt-get install -y -qq certbot python3-certbot-nginx; }

cert() { # name domains...
	local name=$1
	shift
	local args=()
	for d in "$@"; do args+=(-d "$d"); done
	certbot certonly --nginx -n --agree-tos -m "$ACME_EMAIL" --keep-until-expiring --expand --cert-name "$name" "${args[@]}"
}
cert "$DOMAIN" "$DOMAIN" "www.$DOMAIN" "$ADMIN_DOMAIN"
[ -z "${VAULT_DOMAIN:-}" ] || cert "$VAULT_DOMAIN" "$VAULT_DOMAIN"

# sites that already claim one of our names
names=("$DOMAIN" "www.$DOMAIN" "$ADMIN_DOMAIN" ${VAULT_DOMAIN:+"$VAULT_DOMAIN"})
install -d /etc/nginx/sites-disabled-iwillbegoogler
for f in /etc/nginx/sites-enabled/*; do
	[ -e "$f" ] || continue
	case $(basename "$f") in iwillbegoogler.conf | vault.conf) continue ;; esac
	for n in "${names[@]}"; do
		if grep -Eq "server_name[^;]*[[:space:]]${n//./\\.}([[:space:];]|$)" "$f"; then
			echo "disabling $(basename "$f") (serves $n)"
			mv "$f" "/etc/nginx/sites-disabled-iwillbegoogler/$(basename "$f")"
			break
		fi
	done
done

sed -e "s/@DOMAIN@/$DOMAIN/g" -e "s/@ADMIN_DOMAIN@/$ADMIN_DOMAIN/g" "$here/iwillbegoogler.conf" >/etc/nginx/sites-available/iwillbegoogler.conf
ln -sf /etc/nginx/sites-available/iwillbegoogler.conf /etc/nginx/sites-enabled/iwillbegoogler.conf
if [ -n "${VAULT_DOMAIN:-}" ]; then
	sed "s/@VAULT_DOMAIN@/$VAULT_DOMAIN/g" "$here/vault.conf" >/etc/nginx/sites-available/vault.conf
	ln -sf /etc/nginx/sites-available/vault.conf /etc/nginx/sites-enabled/vault.conf
fi
nginx -t
systemctl reload nginx
echo "nginx ok: https://$DOMAIN, https://$ADMIN_DOMAIN${VAULT_DOMAIN:+, https://$VAULT_DOMAIN} (Caddy upstream 127.0.0.1:8088)"
