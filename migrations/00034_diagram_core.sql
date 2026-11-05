-- +goose Up
-- Diagrams (ported from the Node diagram server): a project holds folders
-- and files; a file is one JSON document with optimistic versioning and a
-- snapshot history. Ids are text ("prj_<hex>", "fld_<hex>", "fil_<hex>",
-- "ver_<hex>"), so offline clients can choose their own. Tables are
-- prefixed dg_ to stay clear of the blog's.
CREATE TABLE dg_projects (
    id         TEXT PRIMARY KEY,
    owner_id   BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    -- bumped on every tree mutation so clients can reconcile
    tree_rev   INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by BIGINT REFERENCES users(id) ON DELETE SET NULL
);
CREATE INDEX dg_projects_owner_idx ON dg_projects (owner_id);
CREATE INDEX dg_projects_updated_idx ON dg_projects (updated_at);

CREATE TABLE dg_folders (
    id         TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES dg_projects(id) ON DELETE CASCADE,
    parent_id  TEXT REFERENCES dg_folders(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    -- sort key among the siblings (folders and files share one sequence)
    position   DOUBLE PRECISION NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX dg_folders_project_idx ON dg_folders (project_id);
CREATE INDEX dg_folders_parent_idx ON dg_folders (parent_id) WHERE parent_id IS NOT NULL;

CREATE TABLE dg_files (
    id             TEXT PRIMARY KEY,
    project_id     TEXT NOT NULL REFERENCES dg_projects(id) ON DELETE CASCADE,
    folder_id      TEXT REFERENCES dg_folders(id) ON DELETE CASCADE,
    name           TEXT NOT NULL,
    -- dominant diagram kind, for the icon: sdd | erd | uml | board
    kind           TEXT NOT NULL DEFAULT 'sdd',
    -- the author's chosen lens: general | system | db | ood
    file_kind      TEXT NOT NULL DEFAULT 'general',
    position       DOUBLE PRECISION NOT NULL DEFAULT 0,
    data           JSONB NOT NULL,
    version        INT NOT NULL,
    page_count     INT NOT NULL DEFAULT 1,
    thumbnail_hash TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by     BIGINT REFERENCES users(id) ON DELETE SET NULL
);
CREATE INDEX dg_files_project_idx ON dg_files (project_id);
CREATE INDEX dg_files_folder_idx ON dg_files (folder_id) WHERE folder_id IS NOT NULL;

CREATE TABLE dg_file_versions (
    id         TEXT PRIMARY KEY,
    file_id    TEXT NOT NULL REFERENCES dg_files(id) ON DELETE CASCADE,
    version    INT NOT NULL,
    name       TEXT NOT NULL,
    data       JSONB NOT NULL,
    label      TEXT,
    -- saved by a person with a name ("Save version..."): never pruned
    named      BOOLEAN NOT NULL DEFAULT false,
    created_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (file_id, version)
);

CREATE TABLE dg_project_members (
    project_id TEXT NOT NULL REFERENCES dg_projects(id) ON DELETE CASCADE,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role       TEXT NOT NULL CHECK (role IN ('viewer', 'editor')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, user_id)
);
CREATE INDEX dg_project_members_user_idx ON dg_project_members (user_id);

-- Optional per-file role override for a project member.
CREATE TABLE dg_file_members (
    file_id    TEXT NOT NULL REFERENCES dg_files(id) ON DELETE CASCADE,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role       TEXT NOT NULL CHECK (role IN ('viewer', 'editor')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (file_id, user_id)
);
CREATE INDEX dg_file_members_user_idx ON dg_file_members (user_id);

-- Small per-user JSON blob (open tabs, starred files, ...).
CREATE TABLE dg_user_prefs (
    user_id    BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    data       JSONB NOT NULL DEFAULT '{}',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE dg_user_prefs;
DROP TABLE dg_file_members;
DROP TABLE dg_project_members;
DROP TABLE dg_file_versions;
DROP TABLE dg_files;
DROP TABLE dg_folders;
DROP TABLE dg_projects;
