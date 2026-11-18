-- +goose Up
ALTER TABLE series ADD COLUMN cover_url TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE series DROP COLUMN cover_url;
