-- +goose Up
-- Bug reports and ideas from readers, logged in or not; moderators resolve or dismiss.
CREATE TABLE feedback (
    id            SERIAL PRIMARY KEY,
    user_id       INT REFERENCES users (id) ON DELETE SET NULL,
    kind          TEXT NOT NULL CHECK (kind IN ('bug', 'idea', 'other')),
    message       TEXT NOT NULL,
    contact_email TEXT NOT NULL DEFAULT '',
    page_url      TEXT NOT NULL DEFAULT '',
    user_agent    TEXT NOT NULL DEFAULT '',
    app_version   TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    error_stack   TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved', 'dismissed')),
    resolved_by   INT REFERENCES users (id) ON DELETE SET NULL,
    resolved_at   TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX feedback_status_idx ON feedback (status, id DESC);

-- +goose Down
DROP TABLE feedback;
