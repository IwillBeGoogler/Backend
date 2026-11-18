-- +goose Up
ALTER TABLE posts ADD COLUMN lang TEXT NOT NULL DEFAULT 'uz' CHECK (lang IN ('uz','ru','en'));
CREATE TABLE post_translations (
  post_id    INT NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
  lang       TEXT NOT NULL CHECK (lang IN ('uz','ru','en')),
  title      TEXT NOT NULL,
  subtitle   TEXT NOT NULL DEFAULT '',
  body       TEXT NOT NULL DEFAULT '',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (post_id, lang)
);
-- +goose Down
DROP TABLE post_translations;
ALTER TABLE posts DROP COLUMN lang;
