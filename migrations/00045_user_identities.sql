-- +goose Up
-- Social login links: a provider's stable subject id (Google "sub", GitHub
-- user id) maps to a user, so a later sign-in matches by subject even when the
-- email at the provider changed. Deleting a user deletes the links.
CREATE TABLE user_identities (
    provider   TEXT NOT NULL,
    subject    TEXT NOT NULL,
    user_id    INT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, subject)
);
CREATE INDEX user_identities_user_idx ON user_identities (user_id);

-- +goose Down
DROP TABLE user_identities;
