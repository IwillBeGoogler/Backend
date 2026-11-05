-- +goose Up
-- Diagrams admin: the public template gallery and reports on diagram files.

-- A template is a curated pointer at a diagram file. Users fork it into a
-- project of their own; deleting a template leaves the file alone, deleting
-- the file removes the template.
CREATE TABLE dg_templates (
    id          TEXT PRIMARY KEY,
    title       TEXT NOT NULL,
    -- the gallery lens: system | db | ood
    kind        TEXT NOT NULL CHECK (kind IN ('system', 'db', 'ood')),
    description TEXT NOT NULL DEFAULT '',
    file_id     TEXT NOT NULL REFERENCES dg_files(id) ON DELETE CASCADE,
    published   BOOLEAN NOT NULL DEFAULT false,
    position    INT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX dg_templates_order_idx ON dg_templates (position, title);
CREATE INDEX dg_templates_file_idx ON dg_templates (file_id);

-- Reports gain a diagram target: target_type 'diagram' names the file in
-- target_file_id (target_id is 0). The one-open-report rule and the open
-- count follow the target, whichever column names it.
ALTER TABLE reports ADD COLUMN target_file_id TEXT NOT NULL DEFAULT '';
ALTER TABLE reports DROP CONSTRAINT reports_target_type_check;
ALTER TABLE reports ADD CONSTRAINT reports_target_type_check
    CHECK (target_type IN ('post', 'comment', 'user', 'diagram'));
DROP INDEX reports_open_unique_idx;
CREATE UNIQUE INDEX reports_open_unique_idx
    ON reports (reporter_id, target_type, target_id, target_file_id) WHERE status = 'open';
DROP INDEX reports_target_idx;
CREATE INDEX reports_target_idx ON reports (target_type, target_id, target_file_id);

-- Admin: project name search, and the active-editors stat over snapshots.
CREATE INDEX dg_projects_name_lower_idx ON dg_projects (lower(name) text_pattern_ops);
CREATE INDEX dg_file_versions_created_idx ON dg_file_versions (created_at) WHERE created_by IS NOT NULL;

-- +goose Down
DROP INDEX dg_file_versions_created_idx;
DROP INDEX dg_projects_name_lower_idx;
DELETE FROM reports WHERE target_type = 'diagram';
DROP INDEX reports_target_idx;
CREATE INDEX reports_target_idx ON reports (target_type, target_id);
DROP INDEX reports_open_unique_idx;
CREATE UNIQUE INDEX reports_open_unique_idx ON reports (reporter_id, target_type, target_id) WHERE status = 'open';
ALTER TABLE reports DROP CONSTRAINT reports_target_type_check;
ALTER TABLE reports ADD CONSTRAINT reports_target_type_check CHECK (target_type IN ('post', 'comment', 'user'));
ALTER TABLE reports DROP COLUMN target_file_id;
DROP TABLE dg_templates;
