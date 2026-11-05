-- +goose Up
-- Publishing a diagram file to the blog: private (default), unlisted (open
-- by its link, not listed) or public (listed on profiles and in search).
-- The slug is kept when a file is unpublished, so its link survives a
-- republish. Tags are free-form like a post's.
ALTER TABLE dg_files
    ADD COLUMN visibility   TEXT NOT NULL DEFAULT 'private' CHECK (visibility IN ('private', 'unlisted', 'public')),
    ADD COLUMN published_at TIMESTAMPTZ,
    ADD COLUMN slug         TEXT,
    ADD COLUMN title        TEXT NOT NULL DEFAULT '',
    ADD COLUMN description  TEXT NOT NULL DEFAULT '',
    ADD COLUMN tags         TEXT[] NOT NULL DEFAULT '{}';
CREATE UNIQUE INDEX dg_files_slug_idx ON dg_files (slug) WHERE slug IS NOT NULL;
CREATE INDEX dg_files_public_idx ON dg_files (published_at DESC, id) WHERE visibility = 'public';
CREATE INDEX dg_files_public_tags_idx ON dg_files USING GIN (tags) WHERE visibility = 'public';

-- +goose Down
DROP INDEX dg_files_public_tags_idx;
DROP INDEX dg_files_public_idx;
DROP INDEX dg_files_slug_idx;
ALTER TABLE dg_files
    DROP COLUMN tags, DROP COLUMN description, DROP COLUMN title,
    DROP COLUMN slug, DROP COLUMN published_at, DROP COLUMN visibility;
