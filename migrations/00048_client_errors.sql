-- +goose Up
-- JavaScript errors reported by the web app, one row per distinct error.
-- Deliberately holds no user id and no IP: the data is not tied to a person.
CREATE TABLE client_errors (
    id          SERIAL PRIMARY KEY,
    fingerprint TEXT NOT NULL UNIQUE,
    message     TEXT NOT NULL,
    stack       TEXT NOT NULL DEFAULT '',
    page_url    TEXT NOT NULL DEFAULT '',
    user_agent  TEXT NOT NULL DEFAULT '',
    count       INT NOT NULL DEFAULT 1,
    status      TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved', 'ignored')),
    first_seen  TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX client_errors_status_idx ON client_errors (status, last_seen DESC);
-- The retention sweep deletes by age alone.
CREATE INDEX client_errors_last_seen_idx ON client_errors (last_seen);

-- +goose Down
DROP TABLE client_errors;
