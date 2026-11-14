-- +goose Up
-- Terms defined by one article (term, aliases, definition); they win over the
-- site-wide glossary while that article is read.
ALTER TABLE posts ADD COLUMN glossary JSONB NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE posts DROP COLUMN glossary;
