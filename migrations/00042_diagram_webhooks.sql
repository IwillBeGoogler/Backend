-- +goose Up
-- Outgoing webhooks of a project and their delivery log. Deliveries are
-- sent by the background worker (asynq) with retries and backoff.
CREATE TABLE dg_webhooks (
    id         TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES dg_projects(id) ON DELETE CASCADE,
    url        TEXT NOT NULL,
    -- whsec_...: signs every delivery (HMAC-SHA256)
    secret     TEXT NOT NULL,
    events     TEXT[] NOT NULL,
    active     BOOLEAN NOT NULL DEFAULT true,
    created_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX dg_webhooks_project_idx ON dg_webhooks (project_id, created_at DESC);

CREATE TABLE dg_webhook_deliveries (
    id              TEXT PRIMARY KEY,
    webhook_id      TEXT NOT NULL REFERENCES dg_webhooks(id) ON DELETE CASCADE,
    event           TEXT NOT NULL,
    payload         TEXT NOT NULL,
    attempt         INT NOT NULL DEFAULT 0,
    -- pending | ok | failed
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'ok', 'failed')),
    response_code   INT,
    error           TEXT,
    next_attempt_at TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX dg_webhook_deliveries_hook_idx ON dg_webhook_deliveries (webhook_id, created_at DESC);

-- +goose Down
DROP TABLE dg_webhook_deliveries;
DROP TABLE dg_webhooks;
