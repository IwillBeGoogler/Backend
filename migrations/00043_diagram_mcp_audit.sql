-- +goose Up
-- Audit trail of MCP tool calls (arguments are summarized, never contents).
CREATE TABLE dg_mcp_audit (
    id          TEXT PRIMARY KEY,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_id    TEXT,
    tool        TEXT NOT NULL,
    project_id  TEXT,
    file_id     TEXT,
    -- ok | error | denied | rate_limited
    status      TEXT NOT NULL,
    detail      TEXT,
    duration_ms INT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX dg_mcp_audit_user_idx ON dg_mcp_audit (user_id, created_at DESC);

-- +goose Down
DROP TABLE dg_mcp_audit;
