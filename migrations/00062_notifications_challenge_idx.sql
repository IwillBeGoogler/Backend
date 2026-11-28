-- +goose NO TRANSACTION

-- +goose Up
CREATE INDEX CONCURRENTLY IF NOT EXISTS notifications_challenge_id_idx
    ON notifications (challenge_id) WHERE challenge_id IS NOT NULL;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS notifications_challenge_id_idx;
