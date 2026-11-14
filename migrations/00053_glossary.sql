-- +goose Up
-- Glossary: terms the article reader explains in a tooltip. One row per term
-- and language; lang '' applies to every language. aliases are other spellings
-- or forms the client should match too.
CREATE TABLE glossary_terms (
    id         SERIAL PRIMARY KEY,
    term       TEXT NOT NULL,
    aliases    TEXT[] NOT NULL DEFAULT '{}',
    definition TEXT NOT NULL,
    lang       TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX glossary_terms_term_lang_idx ON glossary_terms (lower(term), lang);

-- +goose Down
DROP TABLE glossary_terms;
