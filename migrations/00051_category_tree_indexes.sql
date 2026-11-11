-- +goose NO TRANSACTION
-- +goose Up
-- Indexes for the category tree (00050). Names repeat across the tree
-- ("Caching" under several parents), so names are unique per parent only.
CREATE INDEX CONCURRENTLY IF NOT EXISTS categories_parent_idx ON categories (parent_id, position);
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS categories_slug_idx ON categories (slug);
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS categories_parent_name_idx ON categories (COALESCE(parent_id, 0), lower(name));
DROP INDEX CONCURRENTLY IF EXISTS categories_name_lower_idx;

-- +goose Down
-- Non-unique: the seeded tree repeats names, so the old unique index cannot return.
CREATE INDEX CONCURRENTLY IF NOT EXISTS categories_name_lower_idx ON categories (lower(name));
DROP INDEX CONCURRENTLY IF EXISTS categories_parent_name_idx;
DROP INDEX CONCURRENTLY IF EXISTS categories_slug_idx;
DROP INDEX CONCURRENTLY IF EXISTS categories_parent_idx;
