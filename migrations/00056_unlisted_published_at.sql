-- +goose Up
-- Unlisted posts now stamp published_at like public ones; backfill the ones
-- shipped before that so the Studio can show "Unlisted since <date>".
UPDATE posts SET published_at = updated_at WHERE status = 'unlisted' AND published_at IS NULL;

-- +goose Down
SELECT 1;
