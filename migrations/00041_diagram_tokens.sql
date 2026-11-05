-- +goose Up
-- Personal access tokens for the public API (/api/diagrams/v1) and MCP:
-- "sdd_pat_" + 32 url-safe characters, shown once; only the sha256 is kept.
-- A token carries scopes and may be limited to some projects.
CREATE TABLE dg_access_tokens (
    id           TEXT PRIMARY KEY,
    user_id      BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE,
    prefix       TEXT NOT NULL,
    scopes       TEXT[] NOT NULL,
    -- NULL: every project the owner can reach
    project_ids  TEXT[],
    expires_at   TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX dg_access_tokens_user_idx ON dg_access_tokens (user_id, created_at DESC) WHERE revoked_at IS NULL;

-- +goose Down
DROP TABLE dg_access_tokens;
