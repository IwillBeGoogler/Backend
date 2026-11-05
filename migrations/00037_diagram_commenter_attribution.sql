-- +goose Up
-- The commenter role (reads and comments, cannot edit) sits between viewer
-- and editor on project members, per-file overrides and share links.
ALTER TABLE dg_project_members DROP CONSTRAINT dg_project_members_role_check;
ALTER TABLE dg_project_members ADD CONSTRAINT dg_project_members_role_check CHECK (role IN ('viewer', 'commenter', 'editor'));
ALTER TABLE dg_file_members DROP CONSTRAINT dg_file_members_role_check;
ALTER TABLE dg_file_members ADD CONSTRAINT dg_file_members_role_check CHECK (role IN ('viewer', 'commenter', 'editor'));
ALTER TABLE dg_share_links DROP CONSTRAINT dg_share_links_role_check;
ALTER TABLE dg_share_links ADD CONSTRAINT dg_share_links_role_check CHECK (role IN ('viewer', 'commenter', 'editor'));

-- A save through a share link has no user: the link's id is kept instead
-- (shown as "share:<id>", as the Node server did). The user column is NULL
-- then; a later save by a person clears these.
ALTER TABLE dg_files ADD COLUMN updated_by_share TEXT;
ALTER TABLE dg_file_versions ADD COLUMN created_by_share TEXT;

-- +goose Down
ALTER TABLE dg_file_versions DROP COLUMN created_by_share;
ALTER TABLE dg_files DROP COLUMN updated_by_share;
UPDATE dg_share_links SET role = 'viewer' WHERE role = 'commenter';
ALTER TABLE dg_share_links DROP CONSTRAINT dg_share_links_role_check;
ALTER TABLE dg_share_links ADD CONSTRAINT dg_share_links_role_check CHECK (role IN ('viewer', 'editor'));
UPDATE dg_file_members SET role = 'viewer' WHERE role = 'commenter';
ALTER TABLE dg_file_members DROP CONSTRAINT dg_file_members_role_check;
ALTER TABLE dg_file_members ADD CONSTRAINT dg_file_members_role_check CHECK (role IN ('viewer', 'editor'));
UPDATE dg_project_members SET role = 'viewer' WHERE role = 'commenter';
ALTER TABLE dg_project_members DROP CONSTRAINT dg_project_members_role_check;
ALTER TABLE dg_project_members ADD CONSTRAINT dg_project_members_role_check CHECK (role IN ('viewer', 'editor'));
