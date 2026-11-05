# MCP server

`POST /api/diagrams/mcp` (Streamable HTTP, stateless). Auth: a personal access token
(`Authorization: Bearer sdd_pat_...`) or a session. Every call is scope-checked, rate limited
(`MCP_RATE_LIMIT_PER_MINUTE`) and written to the audit log.

## Tools

| Tool | Scope | What it does |
| --- | --- | --- |
| `list_projects`, `list_files`, `list_pages`, `read_file` | `projects:read` / `files:read` | browse and read a diagram |
| `create_file`, `add_nodes`, `add_edges`, `update_node`, `delete_elements`, `undo_last_change` | `files:write` | edit; every call is a saved, attributed version ("<you> via MCP") |
| `search_catalog` | `projects:read` | ranked search of the icon catalog (icons, groups, cluster profiles); use the returned `id` as `add_nodes.icon` |
| `apply_layout` | `files:write` | deterministic layout: `layered`, `orthogonal`, `radial`, `tree`, `force`, `grid`; `direction`, `gap`, `seed`, `only` (tidy a selection), `rootId` |
| `import_dsl` | `files:write` | apply `#!sdd` text to a page; `mode` `sync` (default, page matches the text) or `append`. Syntax errors change nothing and come back with line numbers |
| `export` | `files:read` | one page as text: `mermaid`, `plantuml`, `sql`, `svg`, `json`, `sdd` |
| `run_lint` | `files:read` | architecture, ERD and UML rules; `disable`, `severity` overrides, `naming` |

`add_nodes` and `update_node` resolve `icon` through the catalog (id, name or alias); an unknown
icon is reported per item.

## The diagram core in Go

The tools above are ports of the TypeScript core of the diagram editor, in
`internal/domain/diagram/core`:

| File | Port of | Notes |
| --- | --- | --- |
| `catalog.go`, `catalog.json` | `core/catalog.ts` | data generated from `core/catalog.generated.ts` by `make generate` |
| `layout.go`, `trig.go` | `core/layout.ts` | `trig.go` reproduces V8's `Math.sin/cos` and `Math.hypot` bit for bit, so `force` and `radial` agree with the client |
| `dsl_parse.go`, `dsl_print.go`, `dsl_build.go` | `core/dsl/*` | the `#!sdd` text format; also used by `GET /files/{id}/export?type=text` and `.sdd` text uploads |
| `lint.go` | `core/lint.ts` | |
| `exporters.go` | `core/exporters.ts` | `json` keeps the fields but not the key order of the page |

The editor's own engine (the editor's `lib/autoarrange`) is not ported: the MCP server of the Node
backend never used it, it uses `core/layout.ts`.

Floating point: Go may fuse `a*b+c` into one instruction on arm64; the layout code wraps products
in `float64(...)` so results do not depend on the CPU.

## Golden tests

`internal/domain/diagram/core/testdata/golden.json` holds three fixture pages (architecture with
nested groups, ERD, UML plus sequence), `.sdd` texts (valid, broken, sync onto existing pages) and the
output of the TypeScript core on them. `go test ./internal/domain/diagram/core/` checks the Go port
against it (layout, print, parse diagnostics, apply, lint, export, search). After changing the
TypeScript core run `make golden` (`make golden TS_CORE_DIR=<ts core folder>`, needs `tsx`) and `make generate CATALOG_SRC=<catalog.generated.ts>` for the
catalog, then fix the Go side until the tests pass.
