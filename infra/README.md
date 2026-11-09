# infra/ — production stack of IWillBeGoogler

Lives in the Backend repo (no separate infra repo). On the server it is synced to `/opt/iwillbegoogler` by the Backend CD; this
repo's `docker-compose.yml` lands in `/opt/iwillbegoogler/Backend/` (the stack `include`s it). Web, Admin and Backend each deploy their
own service from their CD, on the self-hosted runner on the server (`deploy/server/ci-deploy.sh`). Details: `deploy/README.md`.

# IWillBeGoogler workspace

Three self-contained projects side by side, one compose stack at the root. Each builds, tests and runs without the others' folders; they talk over HTTP/MQTT at runtime only.

| Folder | What | Needs |
|---|---|---|
| `Backend/` | Go API, worker, migrations, own `docker-compose.yml` | nothing (standalone) |
| `Web/` | blog + embedded diagram editor (`Web/editor`) | nothing (talks to the API over HTTP/MQTT) |
| `Admin/` | admin panel | nothing (talks to the API over HTTP) |

(`Client/` is the retired standalone editor; its code now lives in `Web/editor`.)

Local stack: `cp Backend/.env.example Backend/.env && docker compose up -d --build`. Production: `deploy/README.md`.
Optional metrics and dashboards: `docker compose --profile monitoring up -d` (`deploy/README.md`, "Monitoring stack").

## CI

Each project has its own `.github/workflows` and runs on its own; nothing crosses folders.

- **Web** CI is self-contained: lint, typecheck, unit tests (blog + `Web/editor`), editor type check (`bun run typecheck:editor`: fails when
  `Web/src/contexts/diagrams/diagram-editor.d.ts` drifts from `DiagramEditorProps` in `Web/editor/src/embed.tsx`), build, then the image with
  context `Web/`.
- **Backend** CI/CD is self-contained. Production secrets reach the server through the `APP_SECRETS` environment secret (dotenv text); the
  keys delivered as files are listed in `Backend/.github/workflows/cd.yml` (`secret-keys`, includes `SMTP_PASSWORD`, `EMQX_API_SECRET`,
  `MQTT_PASSWORD`, `MQTT_INTERNAL_SECRET`).
- **Admin** CI is self-contained. Its container only needs the API's address (`API_URL`) at run time.
- The root `docker-compose.yml` is the only place that builds all of them together; CI of the individual repos does not use it.
