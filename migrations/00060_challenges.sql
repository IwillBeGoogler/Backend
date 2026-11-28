-- +goose Up
CREATE TABLE challenges (
    id          SERIAL PRIMARY KEY,
    user_id     INT         NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    title       TEXT        NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    days        INT         NOT NULL CHECK (days BETWEEN 7 AND 365),
    visibility  TEXT        NOT NULL CHECK (visibility IN ('public', 'private')),
    timezone    TEXT        NOT NULL,
    start_date  DATE        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX challenges_user_id_idx ON challenges (user_id);
CREATE INDEX challenges_public_start_idx ON challenges (start_date) WHERE visibility = 'public';

CREATE TABLE challenge_posts (
    challenge_id INT         NOT NULL REFERENCES challenges (id) ON DELETE CASCADE,
    post_id      INT         NOT NULL UNIQUE REFERENCES posts (id) ON DELETE CASCADE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (challenge_id, post_id)
);

-- +goose Down
DROP TABLE challenge_posts;
DROP TABLE challenges;
