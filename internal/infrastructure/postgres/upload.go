package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// UploadRepo is the ledger of uploaded images behind the storage quota.
type UploadRepo struct{ db *pgxpool.Pool }

func NewUploadRepo(db *pgxpool.Pool) *UploadRepo { return &UploadRepo{db: db} }

// Usage is the total size, in bytes, of the images userID uploaded.
func (r *UploadRepo) Usage(ctx context.Context, userID int) (int64, error) {
	var n int64
	err := r.db.QueryRow(ctx, `SELECT COALESCE(sum(size), 0)::bigint FROM uploads WHERE user_id = $1`, userID).Scan(&n)
	return n, err
}

// Record notes a stored image. Keys are random, so a repeat is the same upload.
func (r *UploadRepo) Record(ctx context.Context, key string, userID int, size int64) error {
	_, err := r.db.Exec(ctx, `INSERT INTO uploads (key, user_id, size) VALUES ($1, $2, $3) ON CONFLICT (key) DO NOTHING`, key, userID, size)
	return err
}

// Unused returns up to limit keys of images uploaded before olderThan that no
// content refers to any more: not a story or its revisions, a comment, a
// profile, a publication, a series or a list. Images that are still in use
// are marked as checked, so each is looked at once a day, not on every run.
func (r *UploadRepo) Unused(ctx context.Context, olderThan time.Time, limit int) ([]string, error) {
	rows, err := r.db.Query(ctx, `
WITH due AS (
    SELECT key FROM uploads
    WHERE created_at < $1 AND (checked_at IS NULL OR checked_at < now() - interval '1 day')
    ORDER BY checked_at NULLS FIRST, created_at
    LIMIT $2
), judged AS (
    SELECT d.key, NOT (
        EXISTS (SELECT 1 FROM posts WHERE position(d.key in body) > 0 OR position(d.key in cover_url) > 0 OR position(d.key in subtitle) > 0)
        OR EXISTS (SELECT 1 FROM post_revisions WHERE position(d.key in body) > 0 OR position(d.key in subtitle) > 0)
        OR EXISTS (SELECT 1 FROM comments WHERE position(d.key in text) > 0)
        OR EXISTS (SELECT 1 FROM users WHERE position(d.key in avatar_url) > 0 OR position(d.key in bio) > 0)
        OR EXISTS (SELECT 1 FROM publications WHERE position(d.key in avatar_url) > 0 OR position(d.key in description) > 0)
        OR EXISTS (SELECT 1 FROM series WHERE position(d.key in description) > 0)
        OR EXISTS (SELECT 1 FROM lists WHERE position(d.key in description) > 0)
    ) AS unused
    FROM due d
), marked AS (
    UPDATE uploads u SET checked_at = now() FROM judged j WHERE u.key = j.key AND NOT j.unused
)
SELECT key FROM judged WHERE unused`, olderThan, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// Forget drops the ledger rows of images that were removed.
func (r *UploadRepo) Forget(ctx context.Context, keys []string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM uploads WHERE key = ANY($1)`, keys)
	return err
}
