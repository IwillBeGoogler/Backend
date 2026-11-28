-- +goose Up
CREATE TABLE challenge_follows (
    challenge_id INT         NOT NULL REFERENCES challenges (id) ON DELETE CASCADE,
    user_id      INT         NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (challenge_id, user_id)
);
CREATE INDEX challenge_follows_user_id_idx ON challenge_follows (user_id);

CREATE TABLE challenge_cheers (
    challenge_id INT         NOT NULL REFERENCES challenges (id) ON DELETE CASCADE,
    user_id      INT         NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    day          DATE        NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (challenge_id, day, user_id)
);

-- Keys of notifications already sent, so retries never send twice.
CREATE TABLE challenge_sent (
    key        TEXT PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE challenges
    ADD COLUMN reminders BOOLEAN NOT NULL DEFAULT true,
    ADD COLUMN remind_at TIME    NOT NULL DEFAULT '20:00';

ALTER TABLE notifications ADD COLUMN challenge_id INT REFERENCES challenges (id) ON DELETE CASCADE;

-- +goose Down
DELETE FROM notifications WHERE type LIKE 'challenge\_%';
ALTER TABLE notifications DROP COLUMN challenge_id;
ALTER TABLE challenges DROP COLUMN remind_at, DROP COLUMN reminders;
DROP TABLE challenge_sent;
DROP TABLE challenge_cheers;
DROP TABLE challenge_follows;
