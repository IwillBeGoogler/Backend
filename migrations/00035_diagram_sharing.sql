-- +goose Up
-- Diagram sharing (ported from the Node diagram server): share links, the
-- content-addressed asset index, the per-project library and per-user
-- private stickies.

-- A link opens a whole project (file_id NULL) or one file. Only the sha256
-- of the token is stored; the token itself is shown once, on creation.
CREATE TABLE dg_share_links (
    id         TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES dg_projects(id) ON DELETE CASCADE,
    file_id    TEXT REFERENCES dg_files(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    role       TEXT NOT NULL CHECK (role IN ('viewer', 'editor')),
    expires_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    created_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX dg_share_links_project_idx ON dg_share_links (project_id, created_at DESC);
CREATE INDEX dg_share_links_file_idx ON dg_share_links (file_id) WHERE file_id IS NOT NULL;

-- Uploaded images and file previews. The primary key is the lowercase hex
-- sha256 of the stored bytes (which is also their object key), so equal
-- uploads share one row and one blob.
CREATE TABLE dg_assets (
    hash        TEXT PRIMARY KEY CHECK (hash ~ '^[0-9a-f]{64}$'),
    mime        TEXT NOT NULL,
    size        INT NOT NULL CHECK (size >= 0),
    uploaded_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Components, styles and tokens shared by every file of a project: one JSON
-- document. rev is the optimistic version (0 means never saved: no row).
CREATE TABLE dg_project_library (
    project_id TEXT PRIMARY KEY REFERENCES dg_projects(id) ON DELETE CASCADE,
    rev        INT NOT NULL CHECK (rev >= 1),
    data       JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by BIGINT REFERENCES users(id) ON DELETE SET NULL
);

-- A person's private stickies of one file ({nodes, edges}); never part of the
-- shared document and never read for anybody but user_id.
CREATE TABLE dg_file_private (
    file_id    TEXT NOT NULL REFERENCES dg_files(id) ON DELETE CASCADE,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    data       JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (file_id, user_id)
);
CREATE INDEX dg_file_private_user_idx ON dg_file_private (user_id);

-- +goose Down
DROP TABLE dg_file_private;
DROP TABLE dg_project_library;
DROP TABLE dg_assets;
DROP TABLE dg_share_links;
