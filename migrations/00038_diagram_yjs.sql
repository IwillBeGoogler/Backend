-- +goose Up
-- Live co-editing (Yjs). The server relays and stores updates but never
-- merges them: a file's state is an optional snapshot, which a client sends
-- as one update covering everything up to seq, plus the updates after it.
-- Both columns hold raw Yjs v1 update bytes.
CREATE TABLE dg_file_ydocs (
    file_id    TEXT PRIMARY KEY REFERENCES dg_files(id) ON DELETE CASCADE,
    snapshot   BYTEA NOT NULL,
    seq        BIGINT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE dg_file_yupdates (
    file_id    TEXT NOT NULL REFERENCES dg_files(id) ON DELETE CASCADE,
    seq        BIGINT NOT NULL,
    data       BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (file_id, seq)
);

-- +goose Down
DROP TABLE dg_file_yupdates;
DROP TABLE dg_file_ydocs;
