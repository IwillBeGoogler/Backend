-- +goose Up
-- Categories become a tree (parent_id) with slugs and uz/ru names, and a post
-- may sit in several categories (post_categories, position 0 = primary).
-- posts.category_id stays as the primary category for older readers.
ALTER TABLE categories
    ADD COLUMN slug        TEXT,
    ADD COLUMN parent_id   INT REFERENCES categories (id) ON DELETE CASCADE,
    ADD COLUMN description TEXT NOT NULL DEFAULT '',
    ADD COLUMN position    INT NOT NULL DEFAULT 0,
    ADD COLUMN names       JSONB NOT NULL DEFAULT '{}',
    ADD CONSTRAINT categories_not_own_parent CHECK (parent_id <> id);

CREATE TABLE post_categories (
    post_id     INT NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
    category_id INT NOT NULL REFERENCES categories (id) ON DELETE CASCADE,
    position    INT NOT NULL DEFAULT 0,
    PRIMARY KEY (post_id, category_id)
);
CREATE INDEX post_categories_category_idx ON post_categories (category_id, post_id);
INSERT INTO post_categories (post_id, category_id)
SELECT id, category_id FROM posts WHERE category_id IS NOT NULL;

UPDATE categories SET slug = trim(both '-' FROM regexp_replace(lower(name), '[^a-z0-9]+', '-', 'g')) || '-' || id
WHERE slug IS NULL;
ALTER TABLE categories ALTER COLUMN slug SET NOT NULL;

-- +goose Down
DROP TABLE post_categories;
-- Subcategories do not exist in the flat model (their names repeat); posts become uncategorized.
DELETE FROM categories WHERE parent_id IS NOT NULL;
ALTER TABLE categories DROP CONSTRAINT categories_not_own_parent;
ALTER TABLE categories DROP COLUMN names, DROP COLUMN position, DROP COLUMN description,
    DROP COLUMN parent_id, DROP COLUMN slug;
