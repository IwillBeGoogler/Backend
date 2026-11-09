package postgres

import (
	"context"
	"strings"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The admin's view of the workspace (diagram.AdminRepository) and the
// template gallery (diagram.TemplateRepository).

func (r *DiagramRepo) Stats(ctx context.Context, since time.Time) (diagram.AdminStats, error) {
	st := diagram.AdminStats{FilesByKind: map[string]int{}}
	err := r.db.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM dg_projects),
		(SELECT count(*) FROM dg_files),
		(SELECT count(DISTINCT created_by) FROM dg_file_versions WHERE created_at >= $1 AND created_by IS NOT NULL),
		(SELECT COALESCE(sum(octet_length(data::text)), 0) FROM dg_files)
			+ (SELECT COALESCE(sum(octet_length(data::text)), 0) FROM dg_file_versions)
			+ (SELECT COALESCE(sum(size), 0) FROM dg_assets)`, since).
		Scan(&st.Projects, &st.Files, &st.ActiveEditors7d, &st.StorageBytes)
	if err != nil {
		return st, err
	}
	rows, err := r.db.Query(ctx, `SELECT kind, count(*) FROM dg_files GROUP BY kind`)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var n int
		if err := rows.Scan(&kind, &n); err != nil {
			return st, err
		}
		st.FilesByKind[kind] = n
	}
	return st, rows.Err()
}

const adminProjectCols = `p.id, p.name, p.owner_id, COALESCE(u.username, ''), p.created_at, p.updated_at,
	(SELECT count(*) FROM dg_files WHERE project_id = p.id),
	COALESCE(t.id, ''), t.thumbnail_hash,
	EXISTS (SELECT 1 FROM dg_share_links s WHERE s.project_id = p.id AND s.revoked_at IS NULL
		AND (s.expires_at IS NULL OR s.expires_at > now()))`

const adminProjectFrom = ` FROM dg_projects p
	JOIN users u ON u.id = p.owner_id
	LEFT JOIN LATERAL (SELECT id, thumbnail_hash FROM dg_files WHERE project_id = p.id
		ORDER BY position, name, id LIMIT 1) t ON true`

func scanAdminProject(row pgx.Row) (diagram.AdminProject, error) {
	var p diagram.AdminProject
	err := row.Scan(&p.ID, &p.Name, &p.Owner.ID, &p.Owner.Username, &p.CreatedAt, &p.UpdatedAt,
		&p.Files, &p.ThumbFileID, &p.ThumbnailHash, &p.Shared)
	return p, notFound(err)
}

func (r *DiagramRepo) Projects(ctx context.Context, q diagram.AdminProjectQuery) ([]diagram.AdminProject, error) {
	pattern := ""
	if q.Search != "" {
		pattern = "%" + likeEscape(strings.ToLower(q.Search)) + "%"
	}
	rows, err := r.db.Query(ctx, `SELECT `+adminProjectCols+adminProjectFrom+`
		WHERE ($1 = '' OR lower(p.name) LIKE $1 ESCAPE '\') AND ($2 = '' OR lower(u.username) = lower($2))
		ORDER BY p.updated_at DESC, p.id LIMIT $3 OFFSET $4`, pattern, q.Owner, q.Limit+1, q.Offset)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (diagram.AdminProject, error) { return scanAdminProject(row) })
}

func (r *DiagramRepo) ProjectDetail(ctx context.Context, id string) (diagram.AdminProjectDetail, error) {
	var d diagram.AdminProjectDetail
	var err error
	d.AdminProject, err = scanAdminProject(r.db.QueryRow(ctx, `SELECT `+adminProjectCols+adminProjectFrom+` WHERE p.id = $1`, id))
	if err != nil {
		return d, err
	}
	rows, err := r.db.Query(ctx, `SELECT id, name, kind, version, updated_at, thumbnail_hash FROM dg_files
		WHERE project_id = $1 ORDER BY position, name, id`, id)
	if err != nil {
		return d, err
	}
	d.FileList, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (diagram.AdminFile, error) {
		var f diagram.AdminFile
		err := row.Scan(&f.ID, &f.Name, &f.Kind, &f.Version, &f.UpdatedAt, &f.ThumbnailHash)
		return f, err
	})
	if err != nil {
		return d, err
	}
	rows, err = r.db.Query(ctx, `SELECT m.user_id, u.username, m.role, m.created_at FROM dg_project_members m
		JOIN users u ON u.id = m.user_id WHERE m.project_id = $1 ORDER BY m.created_at, m.user_id`, id)
	if err != nil {
		return d, err
	}
	d.Members, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (diagram.AdminMember, error) {
		var m diagram.AdminMember
		err := row.Scan(&m.User.ID, &m.User.Username, &m.Role, &m.JoinedAt)
		return m, err
	})
	if err != nil {
		return d, err
	}
	rows, err = r.db.Query(ctx, `SELECT s.id, s.role, COALESCE(u.username, ''), s.created_at, s.expires_at, s.revoked_at
		FROM dg_share_links s LEFT JOIN users u ON u.id = s.created_by
		WHERE s.project_id = $1 ORDER BY s.created_at DESC, s.id`, id)
	if err != nil {
		return d, err
	}
	d.Shares, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (diagram.AdminShare, error) {
		var s diagram.AdminShare
		err := row.Scan(&s.ID, &s.Role, &s.CreatedBy, &s.CreatedAt, &s.ExpiresAt, &s.RevokedAt)
		return s, err
	})
	return d, err
}

func (r *DiagramRepo) RevokeShareByID(ctx context.Context, id string, at time.Time) (string, error) {
	var project string
	err := r.db.QueryRow(ctx, `UPDATE dg_share_links SET revoked_at = COALESCE(revoked_at, $2)
		WHERE id = $1 RETURNING project_id`, id, at).Scan(&project)
	return project, notFound(err)
}

// DiagramTemplateRepo stores the template gallery (diagram.TemplateRepository).
type DiagramTemplateRepo struct{ db *pgxpool.Pool }

func NewDiagramTemplateRepo(db *pgxpool.Pool) *DiagramTemplateRepo {
	return &DiagramTemplateRepo{db: db}
}

const templateCols = `id, title, kind, description, file_id, published, position, created_at, updated_at`

// templateSel reads a template with its file's preview.
const templateSel = `SELECT t.id, t.title, t.kind, t.description, t.file_id, t.published, t.position, t.created_at, t.updated_at,
	f.thumbnail_hash, COALESCE(f.page_count, 1) FROM dg_templates t JOIN dg_files f ON f.id = t.file_id`

func scanTemplate(row pgx.Row) (diagram.Template, error) {
	var t diagram.Template
	err := row.Scan(&t.ID, &t.Title, &t.Kind, &t.Description, &t.FileID, &t.Published, &t.Position, &t.CreatedAt, &t.UpdatedAt, &t.ThumbnailHash, &t.PageCount)
	return t, notFound(err)
}

func (r *DiagramTemplateRepo) List(ctx context.Context, onlyPublished bool) ([]diagram.Template, error) {
	rows, err := r.db.Query(ctx, templateSel+`
		WHERE (NOT $1 OR t.published) ORDER BY t.position, lower(t.title), t.id`, onlyPublished)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (diagram.Template, error) { return scanTemplate(row) })
}

func (r *DiagramTemplateRepo) Get(ctx context.Context, id string) (diagram.Template, error) {
	return scanTemplate(r.db.QueryRow(ctx, templateSel+` WHERE t.id = $1`, id))
}

func (r *DiagramTemplateRepo) Create(ctx context.Context, t diagram.Template) error {
	_, err := r.db.Exec(ctx, `INSERT INTO dg_templates (`+templateCols+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		t.ID, t.Title, t.Kind, t.Description, t.FileID, t.Published, t.Position, t.CreatedAt, t.UpdatedAt)
	switch pgCode(err) {
	case pgUniqueViolation, pgForeignKeyViolation:
		return domain.ErrConflict
	}
	return err
}

func (r *DiagramTemplateRepo) Update(ctx context.Context, t diagram.Template) error {
	err := execOne(ctx, r.db, `UPDATE dg_templates SET title = $2, kind = $3, description = $4, file_id = $5,
		published = $6, position = $7, updated_at = $8 WHERE id = $1`,
		t.ID, t.Title, t.Kind, t.Description, t.FileID, t.Published, t.Position, t.UpdatedAt)
	if pgCode(err) == pgForeignKeyViolation {
		return domain.ErrConflict
	}
	return err
}

func (r *DiagramTemplateRepo) Delete(ctx context.Context, id string) error {
	return execOne(ctx, r.db, `DELETE FROM dg_templates WHERE id = $1`, id)
}

func (r *DiagramTemplateRepo) Reorder(ctx context.Context, ids []string, at time.Time) error {
	return pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id FROM dg_templates ORDER BY position, lower(title), id FOR UPDATE`)
		if err != nil {
			return err
		}
		current, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		have := make(map[string]bool, len(current))
		for _, id := range current {
			have[id] = true
		}
		order := make([]string, 0, len(current))
		placed := make(map[string]bool, len(ids))
		for _, id := range ids {
			if !have[id] {
				return domain.ErrNotFound
			}
			if !placed[id] {
				placed[id] = true
				order = append(order, id)
			}
		}
		for _, id := range current {
			if !placed[id] {
				order = append(order, id)
			}
		}
		pos := make([]int32, len(order))
		for i := range pos {
			pos[i] = int32(i)
		}
		_, err = tx.Exec(ctx, `UPDATE dg_templates t SET position = v.pos, updated_at = $3
			FROM unnest($1::text[], $2::int[]) AS v(id, pos) WHERE t.id = v.id`, order, pos, at)
		return err
	})
}

var (
	_ diagram.AdminRepository    = (*DiagramRepo)(nil)
	_ diagram.PublishRepository  = (*DiagramRepo)(nil)
	_ diagram.TemplateRepository = (*DiagramTemplateRepo)(nil)
)
