-- +goose Up
-- One chat per account and one account per chat; a link goes with the account
-- when that is purged.
CREATE TABLE telegram_links (
    user_id       INT PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    chat_id       BIGINT      NOT NULL UNIQUE,
    language_code TEXT        NOT NULL DEFAULT '',
    linked_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE telegram_links;
