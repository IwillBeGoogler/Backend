package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
	"github.com/jackc/pgx/v5"
)

// Comments (diagram.CommentRepository).

const dgCommentCols = `id, file_id, project_id, parent_id, COALESCE(author_id, 0), body, anchor, resolved_at,
	COALESCE(resolved_by, 0), created_at, updated_at`

func scanComment(row pgx.Row) (diagram.Comment, error) {
	var c diagram.Comment
	err := row.Scan(&c.ID, &c.FileID, &c.ProjectID, &c.ParentID, &c.AuthorID, &c.Body, &c.Anchor, &c.ResolvedAt,
		&c.ResolvedBy, &c.CreatedAt, &c.UpdatedAt)
	return c, notFound(err)
}

func (r *DiagramRepo) mentions(ctx context.Context, ids []string) (map[string][]int, error) {
	out := map[string][]int{}
	if len(ids) == 0 {
		return out, nil
	}
	// insertion order is the order of appearance in the body
	rows, err := r.db.Query(ctx, `SELECT comment_id, user_id FROM dg_comment_mentions WHERE comment_id = ANY($1)
		ORDER BY comment_id, ctid`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var user int
		if err := rows.Scan(&id, &user); err != nil {
			return nil, err
		}
		out[id] = append(out[id], user)
	}
	return out, rows.Err()
}

func (r *DiagramRepo) Comment(ctx context.Context, id string) (diagram.Comment, error) {
	c, err := scanComment(r.db.QueryRow(ctx, `SELECT `+dgCommentCols+` FROM dg_comments WHERE id = $1`, id))
	if err != nil {
		return c, err
	}
	m, err := r.mentions(ctx, []string{id})
	c.Mentions = m[id]
	return c, err
}

func (r *DiagramRepo) Comments(ctx context.Context, fileID string) ([]diagram.Comment, error) {
	rows, err := r.db.Query(ctx, `SELECT `+dgCommentCols+` FROM dg_comments WHERE file_id = $1 ORDER BY created_at, id`, fileID)
	if err != nil {
		return nil, err
	}
	cs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (diagram.Comment, error) { return scanComment(row) })
	if err != nil || len(cs) == 0 {
		return cs, err
	}
	ids := make([]string, len(cs))
	for i, c := range cs {
		ids[i] = c.ID
	}
	m, err := r.mentions(ctx, ids)
	for i := range cs {
		cs[i].Mentions = m[cs[i].ID]
	}
	return cs, err
}

func (r *DiagramRepo) InsertComment(ctx context.Context, c diagram.Comment) error {
	return pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		var anchor any // NULL for a reply
		if len(c.Anchor) > 0 {
			anchor = json.RawMessage(c.Anchor)
		}
		_, err := tx.Exec(ctx, `INSERT INTO dg_comments (id, file_id, project_id, parent_id, author_id, body, anchor, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			c.ID, c.FileID, c.ProjectID, c.ParentID, userRef(c.AuthorID), c.Body, anchor, c.CreatedAt, c.UpdatedAt)
		switch pgCode(err) {
		case pgUniqueViolation:
			return domain.ErrConflict
		case pgForeignKeyViolation:
			return domain.ErrNotFound
		}
		if err != nil {
			return err
		}
		for _, u := range c.Mentions {
			if _, err := tx.Exec(ctx, `INSERT INTO dg_comment_mentions (comment_id, user_id) VALUES ($1, $2)
				ON CONFLICT DO NOTHING`, c.ID, u); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *DiagramRepo) UpdateComment(ctx context.Context, c diagram.Comment, setBody, setResolved bool) error {
	// untouched parts keep their stored value, so a body edit never rewrites
	// a resolve made in between (and the reverse)
	return execOne(ctx, r.db, `UPDATE dg_comments SET
			body = CASE WHEN $6 THEN $2 ELSE body END,
			resolved_at = CASE WHEN $7 THEN $3 ELSE resolved_at END,
			resolved_by = CASE WHEN $7 THEN $4 ELSE resolved_by END,
			updated_at = $5 WHERE id = $1`,
		c.ID, c.Body, c.ResolvedAt, userRef(c.ResolvedBy), c.UpdatedAt, setBody, setResolved)
}

// DeleteComment deletes a comment; a root's replies, mentions and
// notifications go with it by cascade.
func (r *DiagramRepo) DeleteComment(ctx context.Context, id string) ([]string, error) {
	rows, err := r.db.Query(ctx, `WITH gone AS (DELETE FROM dg_comments WHERE id = $1 RETURNING id)
		SELECT id FROM (SELECT id, 0 AS ord FROM gone
			UNION ALL SELECT id, 1 FROM dg_comments WHERE parent_id = $1) all_gone ORDER BY ord, id`, id)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, domain.ErrNotFound
	}
	return ids, nil
}

func (r *DiagramRepo) ReadAt(ctx context.Context, fileID string, userID int) (time.Time, error) {
	var at time.Time
	err := r.db.QueryRow(ctx, `SELECT read_at FROM dg_comment_reads WHERE file_id = $1 AND user_id = $2`, fileID, userID).Scan(&at)
	return at, ignoreNoRows(err)
}

func (r *DiagramRepo) MarkRead(ctx context.Context, fileID string, userID int, at time.Time) error {
	_, err := r.db.Exec(ctx, `INSERT INTO dg_comment_reads (file_id, user_id, read_at) VALUES ($1, $2, $3)
		ON CONFLICT (file_id, user_id) DO UPDATE SET read_at = EXCLUDED.read_at`, fileID, userID, at)
	if pgCode(err) == pgForeignKeyViolation {
		return domain.ErrNotFound
	}
	return err
}

var _ diagram.CommentRepository = (*DiagramRepo)(nil)
