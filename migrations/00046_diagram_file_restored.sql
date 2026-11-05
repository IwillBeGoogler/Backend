-- +goose Up
-- The head was produced by a restore, not an ordinary save. Live editors
-- that get a version conflict on such a head drop their stale copy instead
-- of saving over the restore.
ALTER TABLE dg_files ADD COLUMN restored BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE dg_files DROP COLUMN restored;
