#!/bin/bash
# Postgres, Redis and (optional) MongoDB on the host under systemd, reachable only from localhost and the stack's
# docker network `iwillbegoogler` (gateway 172.30.0.1, created by install.sh). Idempotent; run as root:
#   deploy/server/datastores.sh [ENV_FILE]            # default /run/iwillbegoogler/env (rendered from Vault)
#   MONGO=1 deploy/server/datastores.sh ...           # also MongoDB (needs MONGO_ROOT_PASSWORD in the env file)
# First install, before the first deploy rendered anything: pass the dotenv you seeded Vault with (.env.prod), then delete it.
# Reads POSTGRES_PASSWORD, REDIS_PASSWORD [, MONGO_ROOT_PASSWORD]. Rotation: change the value in Vault, deploy (re-renders),
# re-run this script, then restart the app containers (they read DATABASE_URL/REDIS_URL from Vault on start).
# Env: PG_MAJOR (18, = pg_dump in deploy/backup), GW (172.30.0.1), SUBNET (172.30.0.0/24), MONGO_SERIES (8.0)
set -euo pipefail
umask 077
[ "$(id -u)" = 0 ] || { echo "run as root" >&2; exit 1; }
ENV_FILE=${1:-/run/iwillbegoogler/env}
PG_MAJOR=${PG_MAJOR:-18}
GW=${GW:-172.30.0.1}
SUBNET=${SUBNET:-172.30.0.0/24}
MONGO_SERIES=${MONGO_SERIES:-8.0}
[ -r "$ENV_FILE" ] || { echo "$ENV_FILE not readable" >&2; exit 1; }
ip -4 addr show | grep -q "inet $GW/" || { echo "$GW not on any interface: run install.sh (docker network iwillbegoogler)" >&2; exit 1; }

# get KEY: value from the dotenv (KEY=v, KEY='v' or KEY="v"); never echoed
get() {
	local v
	v=$(sed -n "s/^$1=//p" "$ENV_FILE" | tail -1)
	v=${v#\'}; v=${v%\'}; v=${v#\"}; v=${v%\"}
	printf '%s' "$v"
}
need() { # KEY -> value, checked to be a safe password (no quotes/spaces: it goes into SQL, URLs and config files)
	local v
	v=$(get "$1")
	[ ${#v} -ge 16 ] || { echo "$1 missing or shorter than 16 chars in $ENV_FILE" >&2; exit 1; }
	[[ $v =~ ^[A-Za-z0-9._~+/=-]+$ ]] || { echo "$1 has characters other than [A-Za-z0-9._~+/=-]: use openssl rand -hex" >&2; exit 1; }
	printf '%s' "$v"
}
# start after dockerd (it creates the 172.30.0.1 bridge), restart on failure
dropin() { # unit
	install -d "/etc/systemd/system/$1.d"
	cat >"/etc/systemd/system/$1.d/iwillbegoogler.conf" <<'UNIT'
[Unit]
Wants=docker.service
After=docker.service

[Service]
Restart=on-failure
RestartSec=5
UNIT
}
export DEBIAN_FRONTEND=noninteractive

# ---------------------------------------------------------------- Postgres (PGDG repo, same major as the backup image)
PG_PW=$(need POSTGRES_PASSWORD)
if [ ! -x "/usr/lib/postgresql/$PG_MAJOR/bin/postgres" ]; then
	apt-get update -qq
	apt-get install -y -qq postgresql-common ca-certificates
	/usr/share/postgresql-common/pgdg/apt.postgresql.org.sh -y >/dev/null
	apt-get install -y -qq "postgresql-$PG_MAJOR"
fi
PGCONF=/etc/postgresql/$PG_MAJOR/main
install -d -o postgres -g postgres "$PGCONF/conf.d"
cat >"$PGCONF/conf.d/iwillbegoogler.conf" <<CONF
# managed by iwillbegoogler deploy/server/datastores.sh
listen_addresses = '127.0.0.1,$GW'
max_connections = 60
shared_buffers = 256MB
password_encryption = scram-sha-256
CONF
chown postgres:postgres "$PGCONF/conf.d/iwillbegoogler.conf"
hba=$PGCONF/pg_hba.conf
sed -i '/# BEGIN iwillbegoogler/,/# END iwillbegoogler/d' "$hba"
cat >>"$hba" <<HBA
# BEGIN iwillbegoogler
host    blog    blog    $SUBNET    scram-sha-256
host    blog    blog    127.0.0.1/32    scram-sha-256
# END iwillbegoogler
HBA
dropin postgresql@.service
systemctl daemon-reload
systemctl enable -q "postgresql@$PG_MAJOR-main"
systemctl restart "postgresql@$PG_MAJOR-main"
# password goes through stdin, never argv
runuser -u postgres -- psql -q -v ON_ERROR_STOP=1 -d postgres <<SQL
\set pw '$PG_PW'
SELECT 'CREATE ROLE blog LOGIN' WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'blog')\gexec
ALTER ROLE blog WITH LOGIN PASSWORD :'pw';
SELECT 'CREATE DATABASE blog OWNER blog' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'blog')\gexec
SQL
echo "postgres $PG_MAJOR: listening on 127.0.0.1 and $GW:5432, db/user blog"

# ---------------------------------------------------------------- Redis
REDIS_PW=$(need REDIS_PASSWORD)
command -v redis-server >/dev/null || { apt-get update -qq; apt-get install -y -qq redis-server; }
cat >/etc/redis/iwillbegoogler.conf <<CONF
# managed by iwillbegoogler deploy/server/datastores.sh
bind 127.0.0.1 $GW
protected-mode yes
requirepass $REDIS_PW
appendonly yes
maxmemory 150mb
maxmemory-policy noeviction
rename-command FLUSHALL ""
rename-command CONFIG ""
CONF
chown root:redis /etc/redis/iwillbegoogler.conf
chmod 0640 /etc/redis/iwillbegoogler.conf
grep -qx 'include /etc/redis/iwillbegoogler.conf' /etc/redis/redis.conf || echo 'include /etc/redis/iwillbegoogler.conf' >>/etc/redis/redis.conf
dropin redis-server.service
systemctl daemon-reload
systemctl enable -q redis-server
systemctl restart redis-server
REDISCLI_AUTH=$REDIS_PW redis-cli -h 127.0.0.1 ping | grep -q PONG || { echo "redis: no PONG" >&2; exit 1; }
echo "redis: listening on 127.0.0.1 and $GW:6379"

# ---------------------------------------------------------------- MongoDB (optional)
if [ "${MONGO:-0}" = 1 ]; then
	MONGO_PW=$(need MONGO_ROOT_PASSWORD)
	if ! command -v mongod >/dev/null; then
		apt-get install -y -qq gnupg curl
		curl -fsSL "https://www.mongodb.org/static/pgp/server-$MONGO_SERIES.asc" | gpg --dearmor --yes -o "/usr/share/keyrings/mongodb-server-$MONGO_SERIES.gpg"
		chmod 0644 "/usr/share/keyrings/mongodb-server-$MONGO_SERIES.gpg" # umask 077 above; apt reads it unprivileged
		# MongoDB publishes no Debian 13 repo yet; the bookworm build runs on trixie (libssl3t64/libcurl4t64 provide the deps)
		echo "deb [signed-by=/usr/share/keyrings/mongodb-server-$MONGO_SERIES.gpg] https://repo.mongodb.org/apt/debian bookworm/mongodb-org/$MONGO_SERIES main" \
			>"/etc/apt/sources.list.d/mongodb-org-$MONGO_SERIES.list"
		apt-get update -qq
		apt-get install -y -qq mongodb-org
	fi
	cat >/etc/mongod.conf <<CONF
# managed by iwillbegoogler deploy/server/datastores.sh
storage:
  dbPath: /var/lib/mongodb
systemLog:
  destination: file
  logAppend: true
  path: /var/log/mongodb/mongod.log
net:
  port: 27017
  bindIp: 127.0.0.1,$GW
processManagement:
  timeZoneInfo: /usr/share/zoneinfo
security:
  authorization: enabled
CONF
	dropin mongod.service
	systemctl daemon-reload
	systemctl enable -q mongod
	systemctl restart mongod
	for _ in $(seq 30); do mongosh --quiet --eval 'db.runCommand({ping:1}).ok' >/dev/null 2>&1 && break; sleep 1; done
	# first admin via the localhost exception (only works while no user exists); script file keeps the password out of argv
	js=$(mktemp)
	printf 'db.getSiblingDB("admin").createUser({user:"root",pwd:"%s",roles:["root"]}); print("mongo: root created");\n' "$MONGO_PW" >"$js"
	mongosh --quiet "mongodb://127.0.0.1:27017/admin" --file "$js" 2>/dev/null || echo "mongo: root user exists (rotate with db.changeUserPassword as root)"
	shred -u "$js"
	echo "mongodb: listening on 127.0.0.1 and $GW:27017, user root (auth enabled)"
fi

# ---------------------------------------------------------------- firewall: docker network -> host services only
if command -v ufw >/dev/null && ufw status | grep -q 'Status: active'; then
	for p in 5432 6379 27017; do ufw allow from "$SUBNET" to "$GW" port "$p" proto tcp >/dev/null; done
	echo "ufw: $SUBNET may reach $GW:5432,6379,27017"
fi
echo "datastores ok"
