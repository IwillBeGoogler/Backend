#!/bin/bash
# Load a dotenv file into KV v2 at kv/iwillbegoogler/$ENV (one `vault kv put`, replaces the whole secret).
#   ENV=production deploy/vault/seed-from-env.sh .env.prod
# Skips blank lines/comments and empty values. Values with ' or newlines are rejected (compose dotenv cannot hold them).
set -euo pipefail
: "${ENV:?production|dev}"
file=${1:?dotenv file}
args=()
while IFS= read -r line || [ -n "$line" ]; do
	case $line in '' | '#'*) continue ;; esac
	key=${line%%=*}
	val=${line#*=}
	val=${val%%  #*}   # strip trailing "  # comment"
	val=${val%"${val##*[![:space:]]}"}
	case $val in \"*\") val=${val#\"}; val=${val%\"} ;; \'*\') val=${val#\'}; val=${val%\'} ;; esac
	[[ $key =~ ^[A-Z][A-Z0-9_]*$ ]] || { echo "skip bad key: $key" >&2; continue; }
	[ -n "$val" ] || continue
	case $val in *\'*) echo "value of $key contains ': refusing" >&2; exit 1 ;; esac
	[ "${val#CHANGE_ME}" != "$val" ] && { echo "$key is still CHANGE_ME: refusing" >&2; exit 1; }
	args+=("$key=$val")
done <"$file"
# The Go services read DATABASE_URL / REDIS_URL from Vault themselves; derive them from the host datastore passwords
# (deploy/server/datastores.sh) unless the file sets them. HOST_GATEWAY = gateway of the docker network iwillbegoogler.
gw=${HOST_GATEWAY:-172.30.0.1}
has() { printf '%s\n' "${args[@]}" | grep -q "^$1="; }
val() { printf '%s\n' "${args[@]}" | sed -n "s/^$1=//p" | tail -1; }
if ! has DATABASE_URL && has POSTGRES_PASSWORD; then args+=("DATABASE_URL=postgres://blog:$(val POSTGRES_PASSWORD)@$gw:5432/blog?sslmode=disable"); fi
if ! has REDIS_URL && has REDIS_PASSWORD; then args+=("REDIS_URL=redis://:$(val REDIS_PASSWORD)@$gw:6379/0"); fi
vault kv put "kv/iwillbegoogler/$ENV" "${args[@]}" >/dev/null
echo "wrote ${#args[@]} keys to kv/iwillbegoogler/$ENV (use 'vault kv metadata get' to see versions)"
