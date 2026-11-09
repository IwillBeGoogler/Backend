# Production deploy (single VPS, Docker Compose)

Files: `docker-compose.prod.yml` (override), `.env.prod.example`, `deploy/caddy/Caddyfile`, `deploy/backup/`.
Sized for 4 GB RAM / 2 vCPU (limits add up to about 3.7 GB, idle use is far lower). On 2 GB: add 2 GB swap, drop `smtp-relay`
(use an external relay) and lower `shared_buffers` (Postgres) / `memory` limits in the override.

## 1. Server

1. Ubuntu/Debian VPS, Docker Engine + Compose v2 (`curl -fsSL https://get.docker.com | sh`).
2. DNS: `A`/`AAAA` records for `DOMAIN` (site) and `ADMIN_DOMAIN` (admin) -> server IP. Wait for propagation before first start
   (Let's Encrypt validates over :80/:443).
3. Firewall: allow only 22 (restrict to your IP if possible), 80, 443/tcp, 443/udp (HTTP/3). Docker bypasses `ufw` for published
   ports, but only Caddy publishes any; verify with `ss -tlnp`.
   ```sh
   ufw default deny incoming && ufw allow 22/tcp && ufw allow 80/tcp && ufw allow 443 && ufw enable
   ```
4. Get the repo on the server, then secrets:
   ```sh
   cp .env.prod.example .env.prod && chmod 600 .env.prod
   # fill every CHANGE_ME using the generation command next to it (openssl rand -hex 32 ...)
   ```
5. Start:
   ```sh
   docker compose --env-file .env.prod -f docker-compose.yml -f docker-compose.prod.yml up -d --build
   docker compose --env-file .env.prod -f docker-compose.yml -f docker-compose.prod.yml ps
   ```
   Tip: `export COMPOSE_FILE=docker-compose.yml:docker-compose.prod.yml` and put `COMPOSE_ENV_FILES=.env.prod` in your shell to shorten
   every command below (`dc` = that compose command).
6. Check: `https://DOMAIN`, `https://ADMIN_DOMAIN`, log in with `ADMIN_USERNAME`/`ADMIN_PASSWORD`, change the password, enable 2FA.

## 2. Secrets

All in `.env.prod`; the API/worker/migrate refuse to start in `APP_ENV=production` with a missing, short (< 32) or dev-default
value (`Backend/config/config.go`, tests in `config_test.go`).

| Variable | Generate | Notes |
|---|---|---|
| `POSTGRES_PASSWORD` | `openssl rand -hex 24` | set before the first start; changing later needs `ALTER USER` |
| `REDIS_PASSWORD` | `openssl rand -hex 24` | |
| `S3_ACCESS_KEY` / `S3_SECRET_KEY` | `openssl rand -hex 10` / `-hex 24` | MinIO root credentials, also used by the API |
| `MFA_KEY` | `openssl rand -hex 32` | encrypts 2FA secrets; changing it disables all 2FA |
| `UNSUBSCRIBE_KEY` | `openssl rand -hex 32` | signs unsubscribe links |
| `AUDIT_EMAIL_KEY` | `openssl rand -hex 32` | hex, keys audit email fingerprints; required in production |
| `MQTT_PASSWORD` | `openssl rand -hex 32` | the API's broker login |
| `MQTT_INTERNAL_SECRET` | `openssl rand -hex 32` | EMQX -> API auth/ACL bearer, only in env, never routed publicly |
| `EMQX_NODE_COOKIE` | `openssl rand -hex 32` | Erlang cookie; the compose refuses to start without it |
| `METRICS_TOKEN` | `openssl rand -hex 32` | bearer for `/metrics` |
| `ADMIN_PASSWORD` | `openssl rand -base64 24` | >= 12 chars |
| `TURNSTILE_SITE_KEY` / `TURNSTILE_SECRET` | Cloudflare dashboard | both or neither |
| `SMTP_USERNAME` / `SMTP_PASSWORD` (+ `SMTP_ADDR`, `SMTP_TLS=starttls\|tls`) | your mail provider | direct SMTP AUTH from the API; no relay needed |
| `SMTP_RELAY_USERNAME` / `_PASSWORD` | your mail provider | only if you keep the optional `smtp-relay` container |
| `BACKUP_S3_ACCESS_KEY` / `_SECRET_KEY` | your offsite bucket | optional |

Keep a copy of `.env.prod` in a password manager: losing `MFA_KEY`/`AUDIT_EMAIL_KEY` breaks 2FA/audit continuity.

## 3. What is exposed

Only Caddy (80, 443). Postgres, Redis, MinIO (console off), EMQX (dashboard off, no host listeners), web, admin, API have no host
ports. Caddy routes: `/` -> web, `/api/*` -> api (SSE `.../stream` unbuffered, uncompressed), `/mqtt` -> EMQX WebSocket,
`/api/internal/*` -> 404 (also blocked in both nginx configs), `/metrics` -> api (needs the bearer token). Admin is limited to
`ADMIN_ALLOW_CIDRS` when set. Headers: HSTS, nosniff, referrer/permissions policy, CSP equal to `Web/nginx.conf` plus
`connect-src wss://DOMAIN`. If you change the CSP in `Web/nginx.conf`, change it in `deploy/caddy/Caddyfile` too (Caddy's replaces it).

Hardening notes: all app containers `no-new-privileges`; api/worker/migrate (distroless, nonroot), postgres, redis, minio, web,
admin, caddy, pgbackup have read-only root filesystems with tmpfs. nginx and Caddy run their master as root with every capability
dropped except what binding :80/:443 needs (nginx workers drop to `nginx`). EMQX keeps a writable root (it writes under `/opt/emqx`).

## 4. Backups and restore

`pgbackup` runs `pg_dump -Fc` daily at `BACKUP_HOUR_UTC` into volume `pgbackups`, keeps `BACKUP_KEEP_DAYS`, and, when
`BACKUP_S3_BUCKET` is set, uploads with rclone (AWS S3, Cloudflare R2, Backblaze B2, or a MinIO elsewhere). Use an offsite bucket:
the local MinIO lives on the same disk as the database. Uploaded files (MinIO `uploads`) are not in the dump: snapshot the VPS
disk, or `rclone sync` the `miniodata` volume to the same offsite bucket.

The dump is the whole database (no table exclusions), so the diagram tables (`dg_*`) are included. Diagram blobs (images, key
`diagram/<sha256>`) live in the same MinIO bucket as blog uploads (`S3_BUCKET`), so they are not in the dump either. Optional mirror:
set `BACKUP_MIRROR=1`, `BACKUP_MIRROR_SRC_ENDPOINT=http://minio:9000`, `BACKUP_MIRROR_SRC_ACCESS_KEY/_SECRET_KEY` (= `S3_ACCESS_KEY/_SECRET_KEY`),
`BACKUP_MIRROR_SRC_BUCKET` (default `uploads`) and `BACKUP_MIRROR_PREFIX` (default `iwillbegoogler/objects`); it needs `BACKUP_S3_BUCKET` and runs
after each dump with `rclone copy` (no remote deletes; blobs are immutable).

Verify the latest dump restores (throwaway Postgres container, checks `dg_projects`, `dg_files`, `dg_file_versions` have rows):
`deploy/backup/verify.sh [dump]` (env: `BACKUP_DIR` host dir of dumps or `BACKUP_VOLUME` compose volume name, `MIN_ROWS`, `VERIFY_TABLES`).

```sh
dc exec pgbackup sh -c 'pg_dump -Fc -f /backups/blog-manual.dump'   # manual dump now
dc exec pgbackup ls -lh /backups
```

Restore (into the running stack; stops writers first):

```sh
dc stop caddy api worker
dc exec pgbackup ls /backups                       # pick a file (or rclone copy it back from the bucket)
dc exec -T postgres psql -U blog -d postgres -c 'DROP DATABASE blog WITH (FORCE)' -c 'CREATE DATABASE blog OWNER blog'
dc exec pgbackup sh -c 'pg_restore -h postgres -U blog -d blog --no-owner --exit-on-error /backups/blog-YYYYMMDDTHHMMSSZ.dump'
dc up -d                                           # migrate re-runs (idempotent), api/worker/caddy start
```

From a bare server: bring the stack up, `dc stop api worker caddy`, copy the dump in (`docker cp file $(dc ps -q pgbackup):/backups/`), run
the same restore. Test a restore on a scratch machine once a quarter; an untested backup is not a backup.

## 5. Upgrades and rollback

```sh
git pull
export IMAGE_TAG=$(git rev-parse --short HEAD)      # or write it into .env.prod
dc build && dc up -d                                # migrate runs first, then api/worker; web/admin swap
dc logs -f --tail=100 api
```

Before an upgrade that has migrations: `dc exec pgbackup sh -c 'pg_dump -Fc -f /backups/blog-pre-upgrade.dump'`.
Rollback: `export IMAGE_TAG=<previous sha>; dc up -d` (images of earlier tags stay on disk until `docker image prune`). Migrations are
forward-only: if the new version changed the schema, restore the pre-upgrade dump (section 4) instead of only swapping images.
Images come from `${IMAGE_REGISTRY:-docker.io/bakhod1r}/iwillbegoogler-<svc>:<tag>` (tag = `TAG_WEB` / `TAG_ADMIN` / `TAG_API` for api+worker+migrate, else `IMAGE_TAG`).
Local build fallback (no registry): `IMAGE_REGISTRY=local dc build && IMAGE_REGISTRY=local dc up -d` (build contexts are in the base compose files).
Prune occasionally: `docker image prune -af --filter until=720h`.

## 6. Monitoring

- Prometheus scrape of `https://DOMAIN/metrics` with `authorization: { type: Bearer, credentials: <METRICS_TOKEN> }`, or
  `api:9090/metrics` / `worker:9090/metrics` from a Prometheus container joined to the compose network (no token on that private listener).
- Health: every service has a healthcheck (`dc ps`); uptime monitor on `https://DOMAIN/healthz`.
- Alert on: `pgbackup` unhealthy (no dump in 26 h), disk usage of `/var/lib/docker`, certificate expiry (Caddy renews on its own),
  Postgres connections near 60.
- Logs: json-file rotation (10 MB x 5 per container). Traces/metrics/logs over OTLP: set `OTEL_EXPORTER_OTLP_ENDPOINT`.

### Monitoring stack (optional)

`docker compose [--env-file .env.prod -f ...] --profile monitoring up -d` adds Prometheus (`127.0.0.1:9091`), Grafana (`127.0.0.1:3200`,
login `admin` / `GRAFANA_ADMIN_PASSWORD`, default `admin`: set it in prod), `postgres-exporter` and `redis-exporter`, about 300 MB with the
limits set. Reach them over an SSH tunnel; nothing is published publicly. Files: `deploy/monitoring/grafana/` (datasource +
`iwillbegoogler-overview` dashboard: API rate/latency/5xx, EMQX connections/messages/drops, Yjs updates in/out, batch write latency, ACL decisions,
Postgres, Redis); the Prometheus config is the `prometheus_conf` entry in `docker-compose.yml`.

- Diagram dashboard `iwillbegoogler-diagrams` (save rate/latency, 412 and 5xx ratio, Yjs flush/rooms/stored updates/compactions, MQTT decisions) and alert
  rules `deploy/monitoring/prometheus/diagram-alerts.yml` (flush failures, stored updates p95 > 100 for 30 min, 5xx > 2 %, compactions refused).
  The compose Prometheus loads them via `rule_files` + a read-only mount; for an external Prometheus add the file to its `rule_files` and reload.
  There are no webhook delivery metrics in the code, so none are shown.
- API is scraped on the private `api:9090` listener (no token inside the compose network); `METRICS_TOKEN` is only for scraping through `https://DOMAIN/metrics`.
- EMQX is scraped at `emqx:18083/api/v5/prometheus/stats` with the API key (`EMQX_API_KEY` / `EMQX_API_SECRET`). The listener is not published to the host; the API also uses it to disconnect revoked clients (`EMQX_API_URL`).
- Exporter DSNs use `POSTGRES_PASSWORD` / `REDIS_PASSWORD` (dev defaults `blog` / none).
- API metrics added for realtime: `iwillbegoogler_mqtt_client_connected`, `iwillbegoogler_mqtt_messages_{received,published}_total`, `iwillbegoogler_mqtt_decisions_total{kind,result}`,
  `iwillbegoogler_yjs_updates_{in,out}_total`, `iwillbegoogler_yjs_batch_write_seconds`, `iwillbegoogler_yjs_batch_write_failures_total`.

## 7. Decisions you own

- `DOMAIN`, `ADMIN_DOMAIN`, `ACME_EMAIL`, optional `ADMIN_ALLOW_CIDRS`.
- Mail provider. The Go mailer now does SMTP AUTH (PLAIN) over STARTTLS or implicit TLS, so the `smtp-relay` postfix container is optional.
  Direct: set `SMTP_ADDR=host:587`, `SMTP_TLS=starttls` (or `host:465` + `tls`), `SMTP_USERNAME`, `SMTP_PASSWORD`. Auth is refused over `SMTP_TLS=none`.
  Relay (still the compose default): keep `SMTP_ADDR=smtp-relay:587` and set `SMTP_RELAYHOST` + relay user/password. Either way, SPF/DKIM/DMARC on the sender domain.
- Offsite backup bucket (R2/B2/S3).
- Turnstile keys (CAPTCHA on signup) and a TURN server (`ICE_SERVERS`) if audio rooms must work behind strict NATs.

## 8. CI/CD + Vault

### Branches

| Branch | Runs | Deploys |
|---|---|---|
| feature branches | CI on the PR | nothing |
| `dev` | CI | nothing (no dev environment / server) |
| `main` | CI + CD | `production` (GitHub environment), Vault `kv/iwillbegoogler/production`, AppRole `iwillbegoogler-production` |

Work on a feature branch, PR into `dev`, then PR `dev` -> `main` to release. Restrict the `production` GitHub environment to
`main` (Settings -> Environments -> Deployment branches) and add required reviewers if wanted. The scripts still accept
`ENV=dev` should a dev server appear later.

### Single server (Vault, stack and other sites on one host)

When nginx already serves other sites on 80/443 (here code-server):

1. `deploy/vault/install-server.sh` with `VAULT_DOMAIN=vault.<domain>`: Vault on 127.0.0.1:8200 (raft). Keep `/root/vault-init.json` off the
   server. After a reboot Vault is sealed: unseal (2 of 3 keys), then `systemctl restart iwillbegoogler-secrets`.
2. `deploy/server/nginx.sh`: certbot certificates, `deploy/proxy/nginx/*.conf`, disables older sites claiming the same names. Vault's
   `/v1/sys/*` is reachable only from the host (except `sys/wrapping/unwrap`).
3. `BEHIND_NGINX=1` in `kv/iwillbegoogler/production`: deploy.sh adds `deploy/proxy/docker-compose.behind-nginx.yml` (Caddy on
   127.0.0.1:8088, plain HTTP; client IP from nginx's X-Real-IP).
4. `setup.sh` with `SERVER_CIDRS=<public ip>/32,172.30.0.0/24`: containers reach `https://vault.<domain>` over the public IP without NAT, so
   Vault sees their 172.30.0.x address.

Trade-off: Vault and the app share the machine, so root on it reaches every secret; there is no TPM (see below).

### Flow

App repos (web, admin, backend): build -> Trivy -> push `docker.io/bakhod1r/iwillbegoogler-<svc>:<short sha>` (Docker Hub login read from
Vault `kv/ci/dockerhub` with JWT role `iwillbegoogler-ci-images`) -> `repository_dispatch` `deploy` to this repo with
`{service, tag, environment}`.

This repo, `.github/workflows/cd.yml`: GitHub OIDC -> Vault JWT role `iwillbegoogler-<env>-ci` (may only create/list/destroy secret_ids of
AppRole `iwillbegoogler-<env>`) -> wrapped (60 s, single use) secret_id -> SSH -> `sudo iwillbegoogler-secrets accept`:

1. unwrap, encrypt with `systemd-creds` into `/etc/iwillbegoogler/secret_id.cred` (`host+tpm2` when the machine has a TPM2, else `host`);
2. `render`: `/run/iwillbegoogler/secrets/vault_secret_id` (tmpfs, 0400, uid 65532) for api/worker/migrate, mounted read-only. They log in
   to Vault themselves (`Backend/config/vault.go`), read `kv/iwillbegoogler/<env>` once, revoke the token, and keep secrets in memory
   only: `docker inspect` shows paths, not values;
3. `/run/iwillbegoogler/env` (tmpfs, 0400, deploy user) only for compose interpolation of containers that cannot talk to Vault
   (minio, emqx, grafana, pgbackup, exporters).

Then `deploy/server/deploy.sh` (pull, pre-upgrade dump, migrate, `up -d`, wait healthy, rollback to `tags.env.prev` on failure) and the
old secret_id accessors are destroyed. No Vault credential lives in GitHub. On boot `iwillbegoogler-secrets.service` re-renders
(`Before=docker.service`). A secret_id lives 168 h: deploy (or run `workflow_dispatch`) at least weekly.

Postgres, Redis and optional MongoDB run on the host under systemd (`deploy/server/datastores.sh`), listening on 127.0.0.1 and
172.30.0.1, the gateway of the pre-created docker network `iwillbegoogler`; only this stack's containers reach them.

### Server-local Vault endpoint

`deploy/server/vault-internal.sh` (after `install-server.sh` and `install.sh`): private CA in `/etc/iwillbegoogler/pki`, nginx listener
`https://172.30.0.1:8443` -> Vault, and the helper switched to it. The helper and the containers never leave the host to read secrets;
`/v1/sys/*` is refused there (except unwrap). The public `vault.<domain>` is only for GitHub Actions (OIDC logins, secret_id issue/prune).
First secret_id without CI (bootstrap, as root with a Vault token):
`vault write -wrap-ttl=60s -field=wrapping_token -f auth/approle/role/iwillbegoogler-production/secret-id | { cat; echo; cat /etc/iwillbegoogler/role_id; } | iwillbegoogler-secrets accept`.
`SKIP_PULL=1 TAG_*=local deploy.sh` deploys images built on the server (`docker build`).

### No SSH from outside

sshd listens on 127.0.0.1 only (`/etc/ssh/sshd_config.d/05-iwillbegoogler-listen.conf`); port 22 is closed. CD runs on a self-hosted
runner on the server (`deploy/server/runner.sh`, user `deploy`, label `iwillbegoogler-prod`, infra repo only) and calls the helper and
`deploy.sh` locally. Admin access: Hetzner Console, or code-server. GitHub vars `SERVER_HOST`/`SERVER_USER` and secrets
`SSH_PRIVATE_KEY`/`SSH_KNOWN_HOSTS` are no longer used.

### TPM

`install.sh` uses the TPM2 when `/dev/tpmrm0` exists and `systemd-analyze has-tpm2` says `yes`: a stolen disk or snapshot then cannot
open the `.cred`. Hetzner Cloud vServers have no (v)TPM and none can be added; there the key is the host key
(`/var/lib/systemd/credential.secret`), which protects against other users but not against a full disk image. For TPM: GCP Shielded VM,
Azure Trusted Launch, AWS NitroTPM, or a dedicated server with (f)TPM enabled in firmware; `REQUIRE_TPM=1 install.sh` then refuses
to run without one. A software TPM (swtpm) on the same disk adds nothing and is not supported.

### Setup order

1. Vault server (separate host, TLS). Operator, with a privileged token, once per env:
   `ENV=production SERVER_IP=<server ip> deploy/vault/setup.sh` (and `ENV=dev ...`). Prints the AppRole `role_id`.
2. Seed: `ENV=production deploy/vault/seed-from-env.sh .env.prod` (derives `DATABASE_URL`/`REDIS_URL` for the host datastores).
   CI creds, once: `vault kv put kv/ci/dockerhub username=... token=...` and `vault kv put kv/ci/github dispatch_token=...`
   (fine-grained PAT, contents read/write on this repo only, used for `repository_dispatch`).
3. Server (Debian 12/13, Ubuntu 24.04; Docker; deploy user in group `docker` with the CI SSH key):
   `VAULT_ADDR=... VAULT_ROLE_ID=... KV_PATH=kv/iwillbegoogler/production DEPLOY_USER=deploy DEPLOY_DIR=/opt/iwillbegoogler [VAULT_CACERT_FILE=ca.pem] [REQUIRE_TPM=1] deploy/server/install.sh`
   then `deploy/server/datastores.sh .env.prod` (`MONGO=1` for MongoDB) and `shred -u .env.prod`.
4. GitHub, environments `production` and `dev`:
   - this repo: vars `VAULT_ADDR`, `VAULT_ROLE_ID`, `SERVER_HOST`, `SERVER_USER`, `DEPLOY_DIR`, optional `VAULT_CACERT_PEM`;
     secrets `SSH_PRIVATE_KEY`, `SSH_KNOWN_HOSTS` (`ssh-keyscan -t ed25519 host`, checked by hand), `BACKEND_READ_TOKEN`
     (backend repo is private), optional `TELEGRAM_BOT_TOKEN`, `TELEGRAM_CHAT_ID`;
   - web, admin, backend: var `VAULT_ADDR` (backend also `APP_NAME=iwillbegoogler`), optional `TELEGRAM_*`.
5. Run `workflow_dispatch` once with `image-tag` set. Check: `ssh server sudo iwillbegoogler-secrets status`,
   `docker inspect iwillbegoogler-api-1 | grep -c MFA_KEY` -> 0.

### Compromise runbook

1. Contain: destroy every secret_id of the role (CI or by hand), so nothing can log in again:
   `for a in $(vault list -format=json auth/approle/role/iwillbegoogler-production/secret-id | jq -r '.[]'); do vault write auth/approle/role/iwillbegoogler-production/secret-id-accessor/destroy secret_id_accessor=$a; done`.
   Revoke live tokens: `vault lease revoke -prefix auth/approle/login`. Suspect the CI path: `vault delete auth/jwt/role/iwillbegoogler-production-ci`
   (re-run `setup.sh` later), rotate the GitHub `SSH_PRIVATE_KEY`, remove it from the server `authorized_keys`. On the server:
   `shred -u /etc/iwillbegoogler/secret_id.cred /run/iwillbegoogler/env /run/iwillbegoogler/secrets/vault_secret_id`, then stop the app containers (they hold secrets in memory).
2. Audit: `jq 'select(.request.path|test("iwillbegoogler|approle"))' /var/log/vault/audit.log`. Look for logins/KV reads from unexpected IPs,
   `sys/wrapping/unwrap` failures (a failed unwrap on the server means someone used the wrapped token first), and secret-id creates outside CD runs
   (`metadata.run` = workflow run id, cross-check with Actions history).
3. Rotate: treat every key in `kv/iwillbegoogler/<env>` as leaked. Generate new values (section 2 table), `vault kv put` / `seed-from-env.sh`.
   Caveats: `POSTGRES_PASSWORD` needs `ALTER USER`; `S3_*` change MinIO root; `MFA_KEY` rotation disables 2FA; `ADMIN_PASSWORD` is only used on first start
   (change in UI); also revoke third-party keys (SMTP, Turnstile, OAuth, backup bucket) at the provider.
4. Rebuild: if the server itself is suspect, provision a fresh VPS, update `SERVER_IP` (`setup.sh` again rebinds the CIDR), run `install.sh`, update
   `SERVER_HOST`/`SSH_KNOWN_HOSTS`, restore the latest dump (section 4), then run the deploy workflow.
