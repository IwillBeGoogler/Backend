package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
	"github.com/jackc/pgx/v5"
)

// Members, share links, assets, libraries and private stickies
// (diagram.SharingRepository).

func scanMember(row pgx.CollectableRow) (diagram.Member, error) {
	var m diagram.Member
	err := row.Scan(&m.UserID, &m.Role, &m.CreatedAt)
	return m, err
}

func (r *DiagramRepo) Members(ctx context.Context, projectID string) ([]diagram.Member, error) {
	rows, err := r.db.Query(ctx, `SELECT user_id, role, created_at FROM dg_project_members
		WHERE project_id = $1 ORDER BY created_at, user_id`, projectID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanMember)
}

func (r *DiagramRepo) SetMember(ctx context.Context, projectID string, userID int, role diagram.Role, at time.Time) error {
	_, err := r.db.Exec(ctx, `INSERT INTO dg_project_members (project_id, user_id, role, created_at) VALUES ($1, $2, $3, $4)
		ON CONFLICT (project_id, user_id) DO UPDATE SET role = EXCLUDED.role`, projectID, userID, role, at)
	if pgCode(err) == pgForeignKeyViolation {
		return domain.ErrNotFound
	}
	return err
}

func (r *DiagramRepo) UpdateMember(ctx context.Context, projectID string, userID int, role diagram.Role) (bool, error) {
	tag, err := r.db.Exec(ctx, `UPDATE dg_project_members SET role = $3 WHERE project_id = $1 AND user_id = $2`, projectID, userID, role)
	return tag.RowsAffected() > 0, err
}

func (r *DiagramRepo) RemoveMember(ctx context.Context, projectID string, userID int) (bool, error) {
	removed := false
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM dg_project_members WHERE project_id = $1 AND user_id = $2`, projectID, userID)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		removed = true
		_, err = tx.Exec(ctx, `DELETE FROM dg_file_members WHERE user_id = $2
			AND file_id IN (SELECT id FROM dg_files WHERE project_id = $1)`, projectID, userID)
		return err
	})
	return removed && err == nil, err
}

func (r *DiagramRepo) FileMembers(ctx context.Context, fileID string) ([]diagram.Member, error) {
	rows, err := r.db.Query(ctx, `SELECT user_id, role, created_at FROM dg_file_members
		WHERE file_id = $1 ORDER BY created_at, user_id`, fileID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanMember)
}

func (r *DiagramRepo) SetFileMember(ctx context.Context, fileID string, userID int, role diagram.Role, at time.Time) error {
	_, err := r.db.Exec(ctx, `INSERT INTO dg_file_members (file_id, user_id, role, created_at) VALUES ($1, $2, $3, $4)
		ON CONFLICT (file_id, user_id) DO UPDATE SET role = EXCLUDED.role`, fileID, userID, role, at)
	if pgCode(err) == pgForeignKeyViolation {
		return domain.ErrNotFound
	}
	return err
}

func (r *DiagramRepo) RemoveFileMember(ctx context.Context, fileID string, userID int) (bool, error) {
	tag, err := r.db.Exec(ctx, `DELETE FROM dg_file_members WHERE file_id = $1 AND user_id = $2`, fileID, userID)
	return tag.RowsAffected() > 0, err
}

const shareCols = `id, project_id, file_id, token_hash, role, expires_at, revoked_at, COALESCE(created_by, 0), created_at`

func scanShare(row pgx.Row) (diagram.ShareLink, error) {
	var s diagram.ShareLink
	err := row.Scan(&s.ID, &s.ProjectID, &s.FileID, &s.TokenHash, &s.Role, &s.ExpiresAt, &s.RevokedAt, &s.CreatedBy, &s.CreatedAt)
	return s, notFound(err)
}

func (r *DiagramRepo) CreateShare(ctx context.Context, s diagram.ShareLink) error {
	var by *int
	if s.CreatedBy != 0 {
		by = &s.CreatedBy
	}
	_, err := r.db.Exec(ctx, `INSERT INTO dg_share_links (id, project_id, file_id, token_hash, role, expires_at, revoked_at, created_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		s.ID, s.ProjectID, s.FileID, s.TokenHash, s.Role, s.ExpiresAt, s.RevokedAt, by, s.CreatedAt)
	if pgCode(err) == pgUniqueViolation {
		return domain.ErrConflict
	}
	return err
}

func (r *DiagramRepo) Shares(ctx context.Context, projectID string) ([]diagram.ShareLink, error) {
	rows, err := r.db.Query(ctx, `SELECT `+shareCols+` FROM dg_share_links WHERE project_id = $1 ORDER BY created_at DESC, id DESC`, projectID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (diagram.ShareLink, error) { return scanShare(row) })
}

func (r *DiagramRepo) RevokeShare(ctx context.Context, projectID, id string, at time.Time) (bool, error) {
	tag, err := r.db.Exec(ctx, `UPDATE dg_share_links SET revoked_at = COALESCE(revoked_at, $3) WHERE id = $2 AND project_id = $1`, projectID, id, at)
	return tag.RowsAffected() > 0, err
}

func (r *DiagramRepo) SharesExpiredBetween(ctx context.Context, from, to time.Time) ([]diagram.ShareLink, error) {
	rows, err := r.db.Query(ctx, `SELECT `+shareCols+` FROM dg_share_links WHERE expires_at > $1 AND expires_at <= $2`, from, to)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (diagram.ShareLink, error) { return scanShare(row) })
}

func (r *DiagramRepo) ShareByHash(ctx context.Context, hash string) (diagram.ShareLink, error) {
	return scanShare(r.db.QueryRow(ctx, `SELECT `+shareCols+` FROM dg_share_links WHERE token_hash = $1`, hash))
}

func (r *DiagramRepo) Asset(ctx context.Context, hash string) (diagram.Asset, error) {
	var a diagram.Asset
	err := r.db.QueryRow(ctx, `SELECT hash, mime, size, COALESCE(uploaded_by, 0), created_at FROM dg_assets WHERE hash = $1`, hash).
		Scan(&a.Hash, &a.Mime, &a.Size, &a.UploadedBy, &a.CreatedAt)
	return a, notFound(err)
}

func (r *DiagramRepo) InsertAsset(ctx context.Context, a diagram.Asset) (bool, error) {
	tag, err := r.db.Exec(ctx, `INSERT INTO dg_assets (hash, mime, size, uploaded_by, created_at) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (hash) DO NOTHING`, a.Hash, a.Mime, a.Size, userRef(a.UploadedBy), a.CreatedAt)
	return tag.RowsAffected() > 0, err
}

func (r *DiagramRepo) SetThumbnail(ctx context.Context, fileID, hash string) error {
	return execOne(ctx, r.db, `UPDATE dg_files SET thumbnail_hash = $2 WHERE id = $1`, fileID, hash)
}

func (r *DiagramRepo) Library(ctx context.Context, projectID string) (diagram.Library, error) {
	var l diagram.Library
	err := r.db.QueryRow(ctx, `SELECT rev, data FROM dg_project_library WHERE project_id = $1`, projectID).Scan(&l.Rev, &l.Doc)
	return l, ignoreNoRows(err)
}

// SaveLibrary is a compare-and-swap on rev: the first save inserts (and
// loses to a concurrent first save), later ones update only while the
// library is still at baseRev.
func (r *DiagramRepo) SaveLibrary(ctx context.Context, projectID string, baseRev int, doc json.RawMessage, by int, at time.Time) (bool, error) {
	var sql string
	args := []any{projectID, baseRev + 1, doc, at, userRef(by)}
	if baseRev == 0 {
		sql = `INSERT INTO dg_project_library (project_id, rev, data, updated_at, updated_by) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (project_id) DO NOTHING`
	} else {
		sql = `UPDATE dg_project_library SET rev = $2, data = $3, updated_at = $4, updated_by = $5
			WHERE project_id = $1 AND rev = $6`
		args = append(args, baseRev)
	}
	tag, err := r.db.Exec(ctx, sql, args...)
	if pgCode(err) == pgForeignKeyViolation {
		return false, domain.ErrNotFound
	}
	return tag.RowsAffected() > 0, err
}

func (r *DiagramRepo) Private(ctx context.Context, fileID string, userID int) (diagram.Private, error) {
	var p diagram.Private
	err := r.db.QueryRow(ctx, `SELECT data, updated_at FROM dg_file_private WHERE file_id = $1 AND user_id = $2`, fileID, userID).
		Scan(&p.Data, &p.UpdatedAt)
	return p, notFound(err)
}

func (r *DiagramRepo) PutPrivate(ctx context.Context, fileID string, userID int, data json.RawMessage, at time.Time) error {
	_, err := r.db.Exec(ctx, `INSERT INTO dg_file_private (file_id, user_id, data, updated_at) VALUES ($1, $2, $3, $4)
		ON CONFLICT (file_id, user_id) DO UPDATE SET data = EXCLUDED.data, updated_at = EXCLUDED.updated_at`, fileID, userID, data, at)
	if pgCode(err) == pgForeignKeyViolation {
		return domain.ErrNotFound
	}
	return err
}

func (r *DiagramRepo) DeletePrivate(ctx context.Context, fileID string, userID int) error {
	_, err := r.db.Exec(ctx, `DELETE FROM dg_file_private WHERE file_id = $1 AND user_id = $2`, fileID, userID)
	return err
}

var _ diagram.SharingRepository = (*DiagramRepo)(nil)
