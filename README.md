# backend-monolith-go

Medium-style blogging API. Go 1.27, DDD layout, PostgreSQL (pgx) + goose migrations, Redis (cache, rate limits, views, queue, pub/sub), MinIO for uploads, [Guard](https://github.com/bakhod1r/guard) for accounts, sessions, RBAC/ABAC and audit.

Frontends: [client-react](https://github.com/IWillBeGoogler/iwillbegoogler-client-react), [admin-react](https://github.com/IWillBeGoogler/iwillbegoogler-admin-react).

## Run

```bash
docker compose up -d postgres redis minio mailpit emqx   # postgres :5433, redis :6380, minio :9000/:9001, mailpit :8025, emqx :1883 (MQTT) and :8083 (WebSocket)
cp .env.example .env
go run ./cmd/app                                    # http://localhost:8080, migrations run on start
go run ./cmd/createadmin -username admin -email admin@example.com   # super admin; password from ADMIN_PASSWORD or stdin
```

Or the whole stack: `docker compose up -d --build`.

- API console ([Spector](https://github.com/bakhod1r/spector), generated from source): http://localhost:8080/api/docs/ — spec at `/api/docs/openapi.json`
- Emails (verification, password reset) in dev: Mailpit at http://localhost:8025
- Prometheus metrics: `/metrics` · Health: `/api/healthz`

## Config (env)

Loaded with [oneenv](https://github.com/bakhod1r/oneenv) from `.env` (optional, see `.env.example`) and the process environment; the environment wins. Secrets are tagged `,secret` in `config/config.go`.

| Var | Default | |
|---|---|---|
| `PORT` | `8080` | |
| `DATABASE_URL` | `postgres://blog:blog@localhost:5433/blog?sslmode=disable` | |
| `REDIS_URL` | `redis://localhost:6380/0` | |
| `ADMIN_USERNAME`, `ADMIN_EMAIL`, `ADMIN_PASSWORD` | — | super admin seeded on start (email counts as verified) |
| `S3_ENDPOINT`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`, `S3_BUCKET`, `S3_USE_SSL` | `localhost:9000`, `minio`, `minio12345`, `uploads`, `false` | MinIO |
| `SMTP_ADDR` | — | SMTP host:port; empty keeps mail in memory |
| `SMTP_USERNAME`, `SMTP_PASSWORD` | — | SMTP AUTH (PLAIN); password is a secret (`SMTP_PASSWORD_FILE` works). Requires `SMTP_TLS=starttls` or `tls`; startup fails otherwise. A postfix relay is no longer required |
| `SMTP_TLS` | `none` | `none` (plain, Mailpit; upgrades if STARTTLS is offered), `starttls` (required, port 587), `tls` (implicit, port 465) |
| `MAIL_FROM` | `no-reply@iwillbegoogler.local` | |
| `SITE_URL` | `http://localhost:8081` | client site, for links in email, RSS and sitemap |
| `REQUIRE_VERIFIED_EMAIL` | `true` | block posting, commenting and new publications until the email is confirmed |
| `IMPORT_ALLOW_PRIVATE` | `false` | let story import fetch private/loopback addresses (tests only, never in production) |
| `PASSWORD_HASH_MEMORY_KIB`, `PASSWORD_HASH_TIME`, `PASSWORD_HASH_THREADS` | `65536`, `3`, `2` | argon2id parameters (Guard); minimum `19456`, `2`, `1`. Changing them rehashes each password on its next login, so don't change them casually in production |
| `AUDIT_EMAIL_KEY` | — | hex, ≥ 32 bytes (`openssl rand -hex 32`). Audit events then store a keyed `email_hmac` instead of an unkeyed `email_sha256`. Applies to audit events only. Startup fails on an invalid key and warns when it is empty |
| `SESSION_IDLE_TIMEOUT`, `SESSION_ABSOLUTE_TIMEOUT` | `30m`, `168h` | a session ends after this long without a request (every request slides the window) and at the latest this long after login; `POST /api/auth/refresh` rotates a live token, it cannot revive an expired session |
| `UPLOAD_UNUSED_DAYS` | `3` | the worker deletes an uploaded image (with its thumbnail and variants) that no story, revision, comment, profile, publication, series or list refers to once it is this many days old; `0` keeps everything. Only images uploaded since migration `00049` are swept |
| `UPLOAD_QUOTA_MB` | `2048` | image storage one account may use through `POST /api/uploads` (originals only; `0` = unlimited). Uploads made before migration `00049` are not counted |
| `MFA_KEY` | `dev-mfa-key-change-me` | encrypts two-factor secrets; **change in production** (changing it later breaks existing 2FA setups) |
| `SITE_NAME` | `IWillBeGoogler` | product name in RSS and prerendered pages |
| `TURNSTILE_SITE_KEY`, `TURNSTILE_SECRET` | — | Cloudflare Turnstile CAPTCHA on signup (set both or neither); the site key reaches the client via `GET /api/config`. Signup also has a honeypot field |
| `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `GITHUB_CLIENT_ID`, `GITHUB_CLIENT_SECRET` | — | social login; a provider is on only when both of its values are set. Create a Google OAuth client (Web application) with redirect URI `PUBLIC_URL/api/auth/oauth/google/callback`, and a GitHub OAuth App with callback URL `PUBLIC_URL/api/auth/oauth/github/callback`. `GOOGLE_ISSUER`, `GITHUB_AUTH_URL`, `GITHUB_TOKEN_URL`, `GITHUB_API_URL` exist only to point tests at fakes |
| `ANALYTICS` | `true` | product analytics: `POST /api/events` (page_view, read_time, signup_open; signup and publish are recorded server-side), `GET /api/admin/analytics` (DAU/WAU/MAU, funnel, D1/D7/D30 retention, read time). No IP or user agent stored; events kept 400 days |
| `DIAGRAM_VERSIONS_KEEP_LAST`, `DIAGRAM_VERSIONS_KEEP_DAYS`, `DIAGRAM_MAX_PROJECT_MB`, `DIAGRAM_MAX_ASSET_MB` | `50`, `30`, `10`, `5` | diagram workspace (`/api/diagrams`): snapshots kept per file (newest N, plus the newest of each day for N days; named versions always) the largest document accepted and the largest image upload |
| `DIAGRAM_MAX_SDD_MB`, `DIAGRAM_MAX_SDD_UNCOMPRESSED_MB` | `100`, `300` | largest `.sdd` file imported, as uploaded and unpacked (MiB) |
| `WEBHOOK_ALLOW_PRIVATE`, `SCHEMA_ALLOW_PRIVATE` | `false`, `false` | let diagram webhooks and live-database introspection reach private, loopback and link-local addresses; never in production |
| `MCP_ENABLED`, `MCP_RATE_LIMIT_PER_MINUTE` | `true`, `120` | the MCP server at `/api/diagrams/mcp` and the tool calls allowed per access token (or user) per minute |
| `SCHEMA_INTROSPECT` | `true` | `POST /api/diagrams/schema/introspect[/sqlite]` (read-only reverse-engineering of a database) |
| `COLLAB_ENABLED`, `COLLAB_COMPACT_AFTER` | `true`, `50` | diagram live co-editing: the Yjs relay on the `dg/f/{id}/yjs/*` MQTT topics (the server stores and relays Yjs updates, it never merges them), and the number of stored updates after which a client is asked for a compacted snapshot |
| `MQTT_URL` | `tcp://localhost:1883` | the MQTT broker (EMQX) of the diagram realtime channels: presence, events, Yjs, cursors, audio signaling ([docs/MQTT.md](docs/MQTT.md)). Empty turns diagram realtime off |
| `MQTT_USERNAME`, `MQTT_PASSWORD` | `api`, — | the API's own broker login (a broker superuser). `MQTT_PASSWORD` is a secret: at least 32 characters in production, a dev value when empty in `APP_ENV=dev` |
| `MQTT_INTERNAL_SECRET` | — | bearer secret EMQX sends to `/api/internal/mqtt/auth` and `/acl` (secret, at least 32 characters in production, dev value in dev; empty turns those routes off). Never route `/api/internal/` from the public proxy |
| `MQTT_PUBLIC_WS_PATH` | `/mqtt` | same-origin path browsers reach the broker's WebSocket listener on; the proxy in front of the site (Web's nginx, vite in dev) routes it to `emqx:8083` |
| `ICE_SERVERS`, `RTC_MAX_PEERS`, `AUDIO_MAX_PARTICIPANTS` | public STUN, `6`, unset | diagram audio rooms (`GET /api/diagrams/rtc/config`): the STUN/TURN servers given to browsers (JSON array) and the room size (a WebRTC mesh; clamped to 2-6, `AUDIO_MAX_PARTICIPANTS` wins when set) |
| `TRUST_PROXY` | `false` | read client IP from `X-Real-IP` |
| `WORKER` | `true` | run background jobs (email, thumbnails, scheduled posts, purges) in this process |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | — | enables OpenTelemetry traces, metrics and logs (OTLP/HTTP); compose points it at Grafana LGTM (`http://localhost:3000`) |
| `OTEL_SERVICE_NAME` | `iwillbegoogler-api` | service name on all telemetry |
| `OTEL_TRACES_SAMPLER_ARG` | `1.0` | share of new traces sampled (parent-based); Redis calls outside a request/job are not traced |
| `DOCS_DIR`, `DOCS_KEY`, `DOCS_PRODUCTION`, `PUBLIC_URL` | `.`, —, `false`, `http://localhost:8080` | Spector console |

## Test

```bash
go test -short ./...   # unit tests
go test ./...          # + integration tests (needs Docker: Postgres, Redis, MinIO via testcontainers)
```

## Structure

```
cmd/app/                      entrypoint, graceful shutdown
cmd/createadmin/              CLI: create/update super admin
app/                          wiring (+ integration tests)
config/                       env config
migrations/                   goose SQL (embedded); from 00049 on, an index on an existing table must be CREATE INDEX CONCURRENTLY in a "-- +goose NO TRANSACTION" file (migrations_test.go enforces it)
internal/
  domain/                     entities, rules, repository interfaces
    post/ comment/ category/ user/ social/ report/ publication/
  application/                use cases and permissions (Blog, Accounts, moderation, publications)
  infrastructure/
    postgres/                 repositories (pgx)
    redis/                    views + trending, events, single-use tokens, audio room slots, guest MQTT tokens
    mqtt/                     the API's connection to the MQTT broker (EMQX)
    guardauth/                Guard: identity, RBAC/ABAC, audit
    queue/                    background jobs (asynq)
    storage/                  MinIO
    images/                   JPEG thumbnail + WebP variants
    markdown/                 Markdown → sanitized HTML with embeds
    mail/ telemetry/
  interfaces/diagramhub/      diagram realtime over MQTT: presence, events, Yjs relay, cursors, audio rooms
  interfaces/http/            REST handlers and routes
    middleware/               CORS, cache, rate limit, logging + metrics
spector.yaml                  Spector CLI config
```

## API docs (Spector)

The console is served by the app; the CLI produces the same document offline:

```bash
go install github.com/bakhod1r/spector/cmd/spector@latest
spector -dir . -o openapi.json                        # OpenAPI 3.0
spector -dir . -lint                                  # routing problems
spector -dir . -sdk ts -sdk-out ../client-react/src/api/gen   # typed TS client
spector -dir . -evolve -since HEAD~1 -fail-on-breaking        # breaking-change check
```

Each handler's doc comment is its endpoint description; that is the source of truth for request bodies and rules.

## Permissions

Guard roles (RBAC) plus ownership policies (ABAC):

- **Anonymous:** read published posts, comments, profiles, publications, tags, RSS.
- **user:** post, comment, clap, highlight, bookmark, follow users and tags, upload, report, create publications. Edit/delete own posts, comments and highlights.
- **moderator:** user rights plus read drafts, edit/delete any post or comment, delete highlights, list and decide reports, ban and suspend users, list users and audit.
- **admin / super_admin:** everything (categories, labels, stats, Guard admin API under `/api/guard`).
- **Publication roles:** owner (manages editors and writers, deletes), editor (edits any post in it, manages writers), writer (posts into it).
- Guard's own `POST /api/guard/auth/login` and `/register` are closed (404): they would skip two-factor login. Use `/api/auth/*`.
- **Access by time or network:** every request of a signed-in user first checks `app.access` (granted to `user` and `moderator`; wildcard roles have it). A `deny` policy on `app.access`, bound to roles, closes the whole API for them (`403`, reason `access_closed`); signing out stays open. Conditions see `env.ip`, `env.time` (`"HH:MM"` in `ACCESS_TIMEZONE`, default `Asia/Tashkent`), `env.weekday` (`mon`…`sun`), `env.date`, `env.method`, `env.origin`. Example: deny when `env.time gte 18:00` OR `env.time lt 09:00`; or deny when `env.ip not_in 203.0.113.7`. The lowest `priority` number is strongest. Wildcard-role holders are affected only when the policy is bound to their role.
- **Moderator rules from config:** `ACCESS_MODERATOR_HOURS` (`09:00-18:00`, a window like `22:00-06:00` spans midnight) and `ACCESS_MODERATOR_IPS` (comma-separated IPs, no networks) become the policies `config: moderators only in working hours` / `config: moderators only from the office network` on every start; an empty value removes its policy. They are read-only in the admin panel.
- With `REQUIRE_VERIFIED_EMAIL`, posting, commenting and creating publications answer `403` with code `EMAIL_NOT_VERIFIED` (6002) until the email is confirmed.

## Conventions

### Response format

Every JSON success body is an envelope. One resource:

```json
{ "data": { "id": 15, "title": "K8s tips", "status": "published" } }
```

A paginated list adds `meta`:

```json
{
  "data": [ { "id": 15 }, { "id": 14 } ],
  "meta": { "page": 2, "limit": 10, "total": 42, "has_more": true, "next_cursor": "MTc1OTY2..." }
}
```

- Page with `?page=&limit=` (max 50) or walk with `?cursor=<next_cursor>` (then `page` is left out and `total` counts from the cursor on). Cursors work for posts (not with `q`), comments, followers/following and notifications.
- Extra metadata: notifications add `meta.unread`, `/api/me/stats` adds `meta.totals`.
- Small bounded lists (tags, categories, labels, trending, related, series, sessions, suggestions) are `{"data": [...]}` without `meta`. Empty lists are `[]`, never `null`.
- `204` responses have no body.
- Guard's `/api/guard/*` routes use the same format: lists (`roles`, `sessions`, `events`, …) come as `{"data": [...]}`, errors as problem details with Guard's own code in `reason` (e.g. `"reason": "role_not_found"`).
- `/stream` endpoints are Server-Sent Events; each event is `data: {"data": {...}}`.
- `/api/healthz` answers `{"data": {"postgres": "ok", ...}}`, or a `503` problem (`HANDLER_SERVICE_UNAVAILABLE`, 5003) with the same map in `checks`.
- RSS (`/api/feed.xml`, `/api/users/{u}/feed.xml`) and `/api/sitemap.xml` stay XML: feed readers and search engines only read those formats. Their errors are problem details like everywhere else.

### Errors

Errors are [RFC 9457](https://www.rfc-editor.org/rfc/rfc9457) problem details (`Content-Type: application/problem+json`), written by [errorx](https://github.com/bakhod1r/errorx):

```json
{
  "type": "about:blank",
  "title": "So'ralgan resurs topilmadi.",
  "status": 404,
  "detail": "post not found",
  "instance": "/api/posts/999",
  "code": "NOT_FOUND",
  "numeric_code": 1012,
  "request_id": "6f1c0e5a9b..."
}
```

- `code` (string) and `numeric_code` (number) are stable: clients branch on them. `detail` says what went wrong for this request; `title` is the code's message in the `Accept-Language` language (uz, ru, en).
- Every response carries `X-Request-ID` (sent by the client or generated); errors repeat it as `request_id`, and request logs use the same id.
- `retryable: true` appears when repeating can succeed, with `Retry-After` (seconds).

| code | numeric_code | HTTP | When |
|---|---|---|---|
| `BAD_REQUEST` | 1001 | 400 | Malformed JSON, bad id or parameter |
| `VALIDATION_ERROR` | 1003 | 400 | A domain rule failed; `detail` says which |
| `UNAUTHORIZED` | 1004 | 401 | Missing or invalid session |
| `FORBIDDEN` | 1008 | 403 | Not allowed |
| `NOT_FOUND` | 1012 | 404 | No such resource or endpoint |
| `CONFLICT` | 1015 | 409 | Already exists |
| `INTERNAL_ERROR` | 1017 | 500 | Unexpected; details only in the server log |
| `HANDLER_TOO_MANY_REQUESTS` | 4029 | 429 | Rate limit; `retryable`, `Retry-After` |
| `HANDLER_SERVICE_UNAVAILABLE` | 5003 | 503 | `/api/healthz`: a dependency is down |
| `INVALID_CREDENTIALS` | 6001 | 401 | Wrong login or password |
| `EMAIL_NOT_VERIFIED` | 6002 | 403 | Confirm the email first (`REQUIRE_VERIFIED_EMAIL`) |
| `USER_BLOCKED` | 6003 | 400 | You and this user blocked each other |
| `IMPORT_FORBIDDEN_ADDRESS` | 6004 | 400 | Story import from a private address |

Application codes live in `internal/interfaces/http/errors.go` (6xxx; errorx keeps 1xxx–5xxx, 8xxx–9xxx). A released code never changes; add new ones with the next number.

### Other

- Post bodies are Markdown. Single-post reads also return `body_html`: GFM rendered and sanitized, YouTube/Vimeo links on their own line become embeds.

## API

| Method | Path | Auth | |
|---|---|---|---|
| **Posts** ||||
| GET | `/api/posts?q=&tag=&category=&label=&page=&limit=&cursor=` | | Published posts; full-text search with `q` |
| GET | `/api/posts/trending?days=&limit=` | | By unique views |
| GET | `/api/posts/{id}`, `/api/slug/{slug}` | optional | Post with `body_html`, `my_claps`, `bookmarked`, `views` |
| POST | `/api/posts` | post.create | `{title, subtitle, body, status (draft/scheduled/published), publish_at, category_id, publication_id, cover_url, tags, label_ids}` |
| PUT / DELETE | `/api/posts/{id}` | author, publication editor, moderator | PUT: fields left out keep their values; `tags: []` / `label_ids: []` clear |
| POST | `/api/posts/{id}/view` | optional | Count a unique view |
| POST | `/api/posts/{id}/clap` | user | `{count: 1-50}`, max 50 per reader |
| POST / DELETE | `/api/posts/{id}/like` | user | One clap / remove all claps |
| POST / DELETE | `/api/posts/{id}/bookmark` | user | |
| GET / POST | `/api/posts/{id}/highlights` | optional / user | Top passages + mine / `{start, end}` rune offsets |
| DELETE | `/api/highlights/{id}` | owner, moderator | |
| **Comments** ||||
| GET | `/api/posts/{id}/comments` | optional | Flat list with `parent_id`, `likes`, `liked` |
| GET | `/api/posts/{id}/comments/stream` | | SSE: created / edited / deleted |
| GET | `/api/posts/{id}/related?limit=` | optional | Similar published posts (shared tags, category, author) |
| GET | `/api/posts/{id}/series` | optional | Position, prev / next and parts of the post's series |
| GET | `/api/posts/{id}/revisions` | editor of the post | Earlier versions, newest first |
| GET | `/api/posts/{id}/revisions/{version}` | editor of the post | Version with body and line diff vs current |
| POST | `/api/posts/{id}/revisions/{version}/restore` | editor of the post | Make it current (current text becomes a revision) |
| POST | `/api/series` | post.create | `{title, description}` |
| GET | `/api/series/{slug}` | optional | Series with parts (author also sees drafts) |
| PATCH / DELETE | `/api/series/{slug}` | author / post.update | |
| PUT | `/api/series/{slug}/posts` | author / post.update | `{post_ids}` in reading order |
| POST | `/api/posts/{id}/comments` | user | `{text, parent_id}` |
| PUT / DELETE | `/api/comments/{id}` | author, moderator | |
| POST / DELETE | `/api/comments/{id}/like` | user | |
| **Taxonomy** ||||
| GET / POST / DELETE | `/api/categories`, `/api/categories/{id}` | — / admin | |
| GET | `/api/categories/{id}/posts` | | |
| GET / POST / DELETE | `/api/labels`, `/api/labels/{id}` | — / admin | |
| GET | `/api/tags` | | Tags with counts |
| POST / DELETE | `/api/tags/{tag}/follow` | user | Followed tags feed `/api/me/feed` |
| **Auth** ||||
| POST | `/api/auth/register` | | `{username, email, password}`; emails a verification link |
| POST | `/api/auth/login` | | `{login, password}` (username or email) |
| POST | `/api/auth/login/2fa` | | `{mfa_token, code}`: finish login when `/api/auth/login` returned `mfa_required` |
| GET | `/api/auth/oauth/{google\|github}/login`, `/callback` | | Social login (404 unless configured; 20/min per IP). Google is OpenID Connect, GitHub plain OAuth2; both use the code flow with PKCE and a single-use state. The ID token must have `email_verified`; GitHub uses the primary verified email from `/user/emails`. The user is found by the stored provider+subject link (table `user_identities`), else by verified email (linked; an unverified local account with that email is verified and its password replaced), else created (username from name/login, deduplicated, email verified). Then it redirects to `SITE_URL/auth/callback#token=…&expires_at=…` (fragment), or `#error=failed\|unverified`. The provider counts as the first factor: local 2FA is not asked. Audit: `auth.login_sso`, `auth.sso_provision`, `auth.sso_failed`. `GET /api/config` returns `sso.google.enabled` and `sso.github.enabled` |
| POST | `/api/auth/refresh`, `/api/auth/logout`, `/api/auth/logout-all` | session | |
| POST | `/api/auth/forgot-password`, `/api/auth/reset-password` | | Email link, valid 1 h |
| POST | `/api/auth/verify-email` | | `{code}` from the emailed link, valid 48 h |
| **Me** ||||
| GET / PATCH / DELETE | `/api/me` | user | Profile (`email_verified`), update, delete (30-day grace) |
| PUT | `/api/me/password` | user | |
| POST | `/api/me/verify-email` | user | Resend verification link |
| GET | `/api/me/posts?status=` | user | Drafts, scheduled, published |
| GET | `/api/me/feed` | user | Followed authors and tags |
| GET | `/api/me/bookmarks`, `/api/me/tags`, `/api/me/publications` | user | |
| GET | `/api/me/stats` | user | Per post: views, claps, comments, bookmarks, highlights |
| GET / POST | `/api/me/notifications`, `/api/me/notifications/read` | user | |
| GET | `/api/me/notifications/stream` | user | SSE |
| GET | `/api/me/sessions` | user | Logged-in devices (`current` marks this one) |
| DELETE | `/api/me/sessions/{id}` | user | Log a device out |
| GET | `/api/me/2fa` | user | `{enabled, backup_codes_left}` |
| POST | `/api/me/2fa/setup` | user | New TOTP secret + `otpauth_url` |
| POST | `/api/me/2fa/enable` | user | `{code}`; returns 10 backup codes once |
| POST | `/api/me/2fa/disable` | user | `{password, code}` (code or backup code) |
| POST | `/api/me/2fa/backup-codes` | user | `{code}`; replaces backup codes |
| GET | `/api/me/blocks`, `/api/me/mutes` | user | |
| GET | `/api/me/suggestions/users?limit=` | user | Who to follow, with `reason` |
| **Users** ||||
| GET | `/api/users/{username}` | optional | Profile with followers, `is_following` |
| GET | `/api/users/{username}/posts`, `/followers`, `/following` | | |
| POST / DELETE | `/api/users/{username}/follow` | user | |
| POST / DELETE | `/api/users/{username}/block` | user | No follows either way, no comments / replies on your content, no notifications |
| POST / DELETE | `/api/users/{username}/mute` | user | No notifications from them |
| GET | `/api/users/{username}/series` | | |
| **Publications** ||||
| POST | `/api/publications` | user | `{slug, name, description, avatar_url}` |
| GET / PATCH / DELETE | `/api/publications/{slug}` | — / editor / owner | |
| GET | `/api/publications/{slug}/posts`, `/members` | | |
| PUT / DELETE | `/api/publications/{slug}/members/{username}` | owner, editor, self | `{role: editor/writer}` |
| **Moderation & admin** ||||
| POST | `/api/reports` | user | `{target_type: post/comment/user, target_id, reason, note}`; `target_type: diagram` takes `target_file_id` instead of `target_id` |
| GET / PUT | `/api/admin/reports`, `/api/admin/reports/{id}` | moderator | `{status: resolved/dismissed, remove_content}` (removing a diagram file also needs `diagrams.manage`) |
| POST | `/api/feedback` | optional | `{kind: bug/idea/other, message (1-5000), contact_email (anonymous only), page_url, user_agent, app_version, error_message, error_stack, captcha_token (anonymous, when CAPTCHA is on), website (honeypot)}`; works signed out; 5 per 10 min per IP anonymous, 20 per hour signed in; a filled honeypot gets 201 and stores nothing |
| POST | `/api/client-errors` | none | `{message (1-1000), stack, page_url, user_agent}`; scrubbed, grouped by a server-computed fingerprint; stores no user id or IP; 204 always for valid input (a full table of 5000 drops new errors); 20 per 10 min per IP; 32 KB body |
| GET / PUT | `/api/admin/client-errors`, `/api/admin/client-errors/{id}` | moderator | filter `status`; `{status: open/resolved/ignored}`; a new occurrence reopens resolved, not ignored; rows are deleted 90 days after last seen |
| GET / PUT | `/api/admin/feedback`, `/api/admin/feedback/{id}` | moderator | filter `status`, `kind`; `{status: open/resolved/dismissed}` |
| GET | `/api/admin/diagrams/stats`, `/projects`, `/projects/{id}` | diagrams.read / diagrams.manage | Workspace totals, project list (cursor), project detail with files, members, share links |
| DELETE / POST | `/api/admin/diagrams/projects/{id}`, `/shares/{shareId}/revoke` | diagrams.manage | 204, audited |
| GET / POST / PATCH / DELETE | `/api/admin/diagrams/templates[/{id}]` | templates.manage | Template gallery; `POST /templates/reorder {ids}` renumbers atomically |
| POST / DELETE / GET | `/api/diagrams/files/{id}/publish` | project owner / any role | `{visibility: unlisted/public, title?, description?, tags?}`; the slug survives an unpublish |
| GET | `/api/diagrams/public`, `/public/{slug}`, `/api/users/{username}/diagrams` | | Public diagrams: search (`q`, `tag`), one with its document, a profile tab |
| GET | `/api/diagrams/published` | user | The caller's own unlisted and public files, newest first (`page`, `pageSize`): `{items: [{fileId, slug, title, visibility, thumbnailUrl, publishedAt}], total, page, pageSize}` |
| GET / POST / DELETE | `/api/diagrams/tokens[/{id}]` | session | Personal access tokens: `POST {name, scopes, expiresInDays?, projectIds?}` returns `{token: "sdd_pat_...", item}` once. Scopes: `projects:read`, `files:read`, `files:write`, `library:read`, `library:write`, `webhooks:manage` |
| GET | `/api/diagrams/v1/projects`, `/projects/{id}/files`, `/files/{id}`, `/projects/{id}/library` | token or session | Public API: `Authorization: Bearer sdd_pat_...` acts as its owner; the owner's project role still applies and the token's scopes and project list only narrow it. Also `PUT /v1/files/{id}` (If-Match), `PUT /v1/projects/{id}/library`, webhooks, export and import below. Tokens work on `/v1` and MCP only |
| GET | `/api/diagrams/files/{id}/export[.sdd]`, `/projects/{id}/export[.sddproj]` | viewer | `.sdd` container download (ZIP, `application/vnd.sdd+zip`); `?type=text` is the `#!sdd` diagrams-as-code text of the active page (`text/vnd.sdd`) |
| POST | `/api/diagrams/import`, `/projects/import`, `/projects/import-project`, `/projects/{id}/import` | user / editor | Multipart `file`: any `.sdd` (container or `#!sdd` text) to a new project, a single file to a new project, a project container to a new project, a single file into a project (`?folderId=`); bad files are 422 `SDD_INVALID {reason, entry}` |
| GET / POST / PATCH / DELETE | `/api/diagrams/projects/{id}/webhooks[/{wid}]`, `/{wid}/deliveries`, `/{wid}/test` | editor | Outgoing webhooks (`file.created`, `file.updated`, `file.deleted`, `library.updated`); the `whsec_` secret is shown once; deliveries are signed (`X-SDD-Signature: sha256=HMAC(secret, "<timestamp>.<body>")`), sent by the worker with 4 retries (10s, 1m, 6m, 36m) and never to private addresses |
| POST / GET / DELETE | `/api/diagrams/mcp`, `/mcp/audit` | token or session | MCP over Streamable HTTP (stateless; GET and DELETE answer 405), 15 tools scoped per token and written to the audit log (`GET /mcp/audit`, session only); layout, icon search, `#!sdd` import/export, other export formats and lint are Go ports of the TypeScript core, see [docs/MCP.md](docs/MCP.md) |
| POST | `/api/diagrams/schema/introspect`, `/schema/introspect/sqlite` | user | Reverse-engineer a live PostgreSQL/MySQL database (`{dialect, host, port?, user, password?, database, ssl?, schema?}`) or an uploaded SQLite file; read-only, 10 a minute |
| GET / POST | `/api/diagrams/templates`, `/templates/{id}/fork` | — / user | Published template gallery; fork copies one into a new project |
| GET | `/api/admin/users`, `/api/admin/comments`, `/api/admin/stats` | moderator / admin | |
| GET | `/api/admin/bans` | user.ban (moderator, admin) | Banned and suspended users |
| GET / PUT / DELETE | `/api/admin/users/{username}/ban` | user.ban | `PUT {reason, until?}`: no `until` = ban, `until` = suspension lifted automatically |
| **Media & feeds** ||||
| POST | `/api/uploads` | user | Multipart `file` ≤ 5 MB → `url`, `thumbnail_url`, WebP `variants`, `srcset` |
| GET | `/api/uploads/{key}` | | |
| GET | `/api/feed.xml`, `/api/users/{username}/feed.xml`, `/api/sitemap.xml` | | RSS, per-author RSS, sitemap |
| **Ops** ||||
| GET | `/api/healthz`, `/metrics`, `/api/docs/` | | Health, Prometheus, API console. `/metrics` is served on `METRICS_ADDR` (`:9090`); on `PORT` only with `METRICS_TOKEN` as a bearer token |

## Licence

Copyright © IWillBeGoogler. All rights reserved ([LICENSE](LICENSE)); the source is public to read, not to reuse. See [CONTRIBUTING.md](CONTRIBUTING.md) and [SECURITY.md](SECURITY.md).
