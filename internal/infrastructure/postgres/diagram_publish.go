package postgres

import (
	"context"
	"strings"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
	"github.com/jackc/pgx/v5"
)

// Blog pages of diagram files (diagram.PublishRepository). The page lives
// in columns of dg_files; the author is the project's owner.

const listingCols = `f.id, f.project_id, f.name, f.kind, f.version, f.page_count, f.thumbnail_hash, f.updated_at,
	f.visibility, COALESCE(f.slug, ''), f.title, f.description, f.tags, f.published_at,
	(SELECT COALESCE(sum(c.count), 0)::int FROM dg_claps c WHERE c.file_id = f.id),
	u.id, u.username, u.display_name, u.avatar_url`

const listingFrom = ` FROM dg_files f
	JOIN dg_projects p ON p.id = f.project_id
	JOIN users u ON u.id = p.owner_id`

func scanListing(row pgx.Row, extra ...any) (diagram.Listing, error) {
	var l diagram.Listing
	var at *time.Time
	dst := append([]any{&l.FileID, &l.ProjectID, &l.Name, &l.Kind, &l.Version, &l.PageCount, &l.ThumbnailHash, &l.UpdatedAt,
		&l.Visibility, &l.Slug, &l.Title, &l.Description, &l.Tags, &at, &l.Claps,
		&l.AuthorID, &l.AuthorUsername, &l.AuthorName, &l.AuthorAvatar}, extra...)
	if err := row.Scan(dst...); err != nil {
		return l, notFound(err)
	}
	if at != nil {
		l.PublishedAt = *at
	}
	if l.Tags == nil {
		l.Tags = []string{}
	}
	return l, nil
}

func (r *DiagramRepo) SetPublication(ctx context.Context, fileID string, p diagram.Publication) error {
	if p.Tags == nil {
		p.Tags = []string{} // the column is NOT NULL
	}
	err := execOne(ctx, r.db, `UPDATE dg_files SET visibility = $2, slug = $3, title = $4, description = $5,
		tags = $6, published_at = $7 WHERE id = $1`,
		fileID, p.Visibility, p.Slug, p.Title, p.Description, p.Tags, p.PublishedAt)
	if pgCode(err) == pgUniqueViolation {
		return domain.ErrConflict
	}
	return err
}

func (r *DiagramRepo) ClearPublication(ctx context.Context, fileID string) error {
	return execOne(ctx, r.db, `UPDATE dg_files SET visibility = 'private', published_at = NULL WHERE id = $1`, fileID)
}

func (r *DiagramRepo) Listing(ctx context.Context, fileID string) (diagram.Listing, error) {
	return scanListing(r.db.QueryRow(ctx, `SELECT `+listingCols+listingFrom+` WHERE f.id = $1`, fileID))
}

func (r *DiagramRepo) PublicBySlug(ctx context.Context, slug string) (diagram.PublicDoc, error) {
	var d diagram.PublicDoc
	var err error
	d.Listing, err = scanListing(r.db.QueryRow(ctx, `SELECT `+listingCols+`, f.data`+listingFrom+`
		WHERE f.slug = $1 AND f.visibility <> 'private' AND u.deleted_at IS NULL`, slug), &d.Data)
	return d, err
}

func (r *DiagramRepo) ListPublic(ctx context.Context, q diagram.PublicQuery) ([]diagram.Listing, int, error) {
	pattern := ""
	if q.Q != "" {
		pattern = "%" + likeEscape(strings.ToLower(q.Q)) + "%"
	}
	const where = ` WHERE f.visibility = 'public' AND u.deleted_at IS NULL
		AND ($1 = '' OR lower(f.title) LIKE $1 ESCAPE '\' OR lower(f.description) LIKE $1 ESCAPE '\')
		AND ($2 = '' OR f.tags @> ARRAY[$2]::text[])
		AND ($3 = 0 OR p.owner_id = $3)`
	rows, err := r.db.Query(ctx, `SELECT `+listingCols+`, count(*) OVER ()`+listingFrom+where+
		` ORDER BY f.published_at DESC, f.id LIMIT $4 OFFSET $5`,
		pattern, q.Tag, q.AuthorID, q.Limit, (max(q.Page, 1)-1)*q.Limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []diagram.Listing
	total := 0
	for rows.Next() {
		l, err := scanListing(rows, &total)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, l)
	}
	return out, total, rows.Err()
}

func (r *DiagramRepo) ListOwned(ctx context.Context, userID, page, limit int) ([]diagram.Listing, int, error) {
	rows, err := r.db.Query(ctx, `SELECT `+listingCols+`, count(*) OVER ()`+listingFrom+
		` WHERE f.visibility IN ('public', 'unlisted') AND p.owner_id = $1
		ORDER BY f.published_at DESC, f.id LIMIT $2 OFFSET $3`,
		userID, limit, (max(page, 1)-1)*limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []diagram.Listing
	total := 0
	for rows.Next() {
		l, err := scanListing(rows, &total)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, l)
	}
	return out, total, rows.Err()
}

func (r *DiagramRepo) ReferencedPublic(ctx context.Context, refs []string) (map[string]diagram.Listing, error) {
	out := map[string]diagram.Listing{}
	if len(refs) == 0 {
		return out, nil
	}
	rows, err := r.db.Query(ctx, `SELECT `+listingCols+listingFrom+`
		WHERE f.visibility <> 'private' AND u.deleted_at IS NULL AND (f.id = ANY($1) OR f.slug = ANY($1))`, refs)
	if err != nil {
		return nil, err
	}
	ls, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (diagram.Listing, error) { return scanListing(row) })
	if err != nil {
		return nil, err
	}
	for _, l := range ls { // keyed by the ref as given, not by every name a file has
		for _, ref := range refs {
			if ref == l.FileID || ref != "" && ref == l.Slug {
				out[ref] = l
			}
		}
	}
	return out, nil
}

// Claps and bookmarks (diagram.EngagementRepository). Only unlisted and
// public files take part: a private or missing file is not found.

func (r *DiagramRepo) PublicListing(ctx context.Context, slug string) (diagram.Listing, error) {
	return scanListing(r.db.QueryRow(ctx, `SELECT `+listingCols+listingFrom+`
		WHERE f.slug = $1 AND f.visibility <> 'private' AND u.deleted_at IS NULL`, slug))
}

// Clap adds n claps in one transaction. The row is inserted first (ON
// CONFLICT DO NOTHING tells the reader's first clap); otherwise it is locked,
// so concurrent claps by one reader cannot pass MaxClaps.
func (r *DiagramRepo) Clap(ctx context.Context, fileID string, userID, n int) (total, mine int, created bool, err error) {
	n = min(n, diagram.MaxClaps)
	err = pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO dg_claps (file_id, user_id, count)
			SELECT f.id, $2, $3 FROM dg_files f WHERE f.id = $1 AND f.visibility <> 'private'
			ON CONFLICT DO NOTHING`, fileID, userID, n)
		if err != nil {
			return err
		}
		if created = tag.RowsAffected() == 1; created {
			mine = n
		} else {
			if err = tx.QueryRow(ctx, `SELECT count FROM dg_claps WHERE file_id = $1 AND user_id = $2 FOR UPDATE`,
				fileID, userID).Scan(&mine); err != nil {
				return notFound(err) // no row: the file is not published
			}
			mine = min(mine+n, diagram.MaxClaps)
			if _, err = tx.Exec(ctx, `UPDATE dg_claps SET count = $3 WHERE file_id = $1 AND user_id = $2`, fileID, userID, mine); err != nil {
				return err
			}
		}
		return tx.QueryRow(ctx, `SELECT COALESCE(sum(count), 0)::int FROM dg_claps WHERE file_id = $1`, fileID).Scan(&total)
	})
	if pgCode(err) == pgForeignKeyViolation {
		err = domain.ErrNotFound
	}
	return total, mine, created, err
}

func (r *DiagramRepo) Bookmark(ctx context.Context, fileID string, userID int) error {
	tag, err := r.db.Exec(ctx, `INSERT INTO dg_bookmarks (file_id, user_id)
		SELECT f.id, $2 FROM dg_files f WHERE f.id = $1 AND f.visibility <> 'private'
		ON CONFLICT DO NOTHING`, fileID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 { // already saved, or not published
		var ok bool
		if err := r.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM dg_bookmarks WHERE file_id = $1 AND user_id = $2)`, fileID, userID).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return domain.ErrNotFound
		}
	}
	return nil
}

func (r *DiagramRepo) Unbookmark(ctx context.Context, fileID string, userID int) error {
	_, err := r.db.Exec(ctx, `DELETE FROM dg_bookmarks WHERE file_id = $1 AND user_id = $2`, fileID, userID)
	return err
}

func (r *DiagramRepo) Viewer(ctx context.Context, fileID string, userID int) (v diagram.Viewer, err error) {
	err = r.db.QueryRow(ctx, `SELECT COALESCE((SELECT count FROM dg_claps WHERE file_id = $1 AND user_id = $2), 0),
		EXISTS (SELECT 1 FROM dg_bookmarks WHERE file_id = $1 AND user_id = $2)`, fileID, userID).Scan(&v.MyClaps, &v.Bookmarked)
	return v, err
}

func (r *DiagramRepo) ListBookmarked(ctx context.Context, userID, page, limit int) ([]diagram.Listing, int, error) {
	rows, err := r.db.Query(ctx, `SELECT `+listingCols+`, count(*) OVER ()`+listingFrom+`
		JOIN dg_bookmarks b ON b.file_id = f.id AND b.user_id = $1
		WHERE f.visibility <> 'private' AND u.deleted_at IS NULL
		ORDER BY b.created_at DESC, f.id LIMIT $2 OFFSET $3`, userID, limit, (max(page, 1)-1)*limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []diagram.Listing{}
	total := 0
	for rows.Next() {
		l, err := scanListing(rows, &total)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, l)
	}
	return out, total, rows.Err()
}
