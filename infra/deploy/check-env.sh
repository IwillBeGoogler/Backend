#!/bin/sh
# Fails when a key of .env.prod.example never reaches docker-compose*.yml (it would be silently ignored in production).
# Keys the Go services read from Vault themselves (Backend/config/vault.go) or that only deploy/server/datastores.sh uses
# are not in any compose file on purpose.
# Run from anywhere: deploy/check-env.sh
cd "$(dirname "$0")/.." || exit 1
status=0
for key in $(sed -nE 's/^#?([A-Z][A-Z0-9_]*)=.*/\1/p' .env.prod.example | sort -u); do
	case $key in
	MFA_KEY | UNSUBSCRIBE_KEY | AUDIT_EMAIL_KEY | MQTT_PASSWORD | MQTT_INTERNAL_SECRET | METRICS_TOKEN | TURNSTILE_SECRET | \
		ADMIN_PASSWORD | GOOGLE_CLIENT_SECRET | GITHUB_CLIENT_SECRET | SMTP_PASSWORD | DOCS_KEY | ICE_SERVERS | EMQX_API_SECRET | \
		DATABASE_URL | REDIS_URL | MONGO_ROOT_PASSWORD | HOST_GATEWAY | BEHIND_NGINX) continue ;;
	esac
	case $key in IMAGE_TAG|IMAGE_REGISTRY|TAG_WEB|TAG_ADMIN|TAG_API) grep -q "$key" docker-compose.prod.yml && continue ;; esac
	if ! grep -q "\${${key}[:}]" docker-compose.yml docker-compose.prod.yml Backend/docker-compose.yml; then
		echo "not passed to any service: $key" >&2
		status=1
	fi
done
exit $status
