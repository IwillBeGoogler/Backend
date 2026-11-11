-- +goose Up
-- Who uploaded which image (POST /api/uploads) and how large it is: the
-- per-user storage quota sums it. The object itself lives in the bucket
-- under key; thumbnails and WebP variants derive from it and are not counted.
-- Images stored before this table have no row: they count as zero and are never swept.
CREATE TABLE uploads (
    key        TEXT PRIMARY KEY,
    user_id    INT REFERENCES users(id) ON DELETE SET NULL,
    size       BIGINT NOT NULL CHECK (size >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- last time the unused-image sweep found the image still referenced
    checked_at TIMESTAMPTZ
);
CREATE INDEX uploads_user_id_idx ON uploads (user_id);
CREATE INDEX uploads_sweep_idx ON uploads (created_at);

-- +goose Down
DROP TABLE uploads;
