-- +goose Up
-- A certificate freezes what was achieved: it outlives its challenge and goes
-- with the account when that is purged.
CREATE TABLE challenge_certificates (
    id            TEXT PRIMARY KEY,
    challenge_id  INT UNIQUE REFERENCES challenges (id) ON DELETE SET NULL,
    user_id       INT         NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    holder_name   TEXT        NOT NULL,
    username      TEXT        NOT NULL,
    title         TEXT        NOT NULL,
    days          INT         NOT NULL,
    days_done     INT         NOT NULL,
    best_streak   INT         NOT NULL,
    start_date    DATE        NOT NULL,
    end_date      DATE        NOT NULL,
    issued_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at    TIMESTAMPTZ,
    revoke_reason TEXT        NOT NULL DEFAULT ''
);
CREATE INDEX challenge_certificates_user_id_idx ON challenge_certificates (user_id);

-- +goose Down
DROP TABLE challenge_certificates;
