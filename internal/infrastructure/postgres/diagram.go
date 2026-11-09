package postgres

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DiagramRepo stores projects, folders, files, history and preferences
// (diagram.Repository and diagram.PrefsRepository).
type DiagramRepo struct{ db *pgxpool.Pool }

func NewDiagramRepo(db *pgxpool.Pool) *DiagramRepo { return &DiagramRepo{db: db} }

// shareRef maps "no share link" ("") to NULL.
func shareRef(id string) *string {
	if id == "" {
		return nil
	}
	return &id
}

// userRef maps "no user" (0) to NULL.
func userRef(id int) *int {
	if id == 0 {
		return nil
	}
	return &id
}

const projectCols = `p.id, p.owner_id, p.name, p.tree_rev, p.created_at, p.updated_at, COALESCE(p.updated_by, 0)`

func scanProject(row pgx.Row) (diagram.Project, error) {
	var p diagram.Project
	err := row.Scan(&p.ID, &p.OwnerID, &p.Name, &p.TreeRev, &p.CreatedAt, &p.UpdatedAt, &p.UpdatedBy)
	return p, notFound(err)
}

func (r *DiagramRepo) CreateProject(ctx context.Context, p diagram.Project) error {
	_, err := r.db.Exec(ctx, `INSERT INTO dg_projects (id, owner_id, name, tree_rev, created_at, updated_at, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, p.ID, p.OwnerID, p.Name, p.TreeRev, p.CreatedAt, p.UpdatedAt, userRef(p.UpdatedBy))
	if pgCode(err) == pgUniqueViolation {
		return domain.ErrConflict
	}
	return err
}

func (r *DiagramRepo) Project(ctx context.Context, id string) (diagram.Project, error) {
	return scanProject(r.db.QueryRow(ctx, `SELECT `+projectCols+` FROM dg_projects p WHERE p.id = $1`, id))
}

// likeEscape makes %, _ and \ literal in a LIKE pattern.
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func (r *DiagramRepo) ListProjects(ctx context.Context, userID int, q diagram.ProjectQuery) ([]diagram.ProjectSummary, int, error) {
	pattern := ""
	if q.Search != "" {
		pattern = "%" + likeEscape(strings.ToLower(q.Search)) + "%"
	}
	const join = ` FROM dg_projects p
		LEFT JOIN dg_project_members m ON m.project_id = p.id AND m.user_id = $1`
	const where = `
		WHERE (p.owner_id = $1 OR m.user_id IS NOT NULL)
		AND ($2 = '' OR lower(p.name) LIKE $2 ESCAPE '\')`
	var total int
	if err := r.db.QueryRow(ctx, `SELECT count(*)`+join+where, userID, pattern).Scan(&total); err != nil {
		return nil, 0, err
	}
	key, dir := "p.updated_at", "DESC"
	if q.SortByName {
		key = "lower(p.name)"
	}
	if q.Asc {
		dir = "ASC"
	}
	rows, err := r.db.Query(ctx, `SELECT `+projectCols+`,
		CASE WHEN p.owner_id = $1 THEN 'owner' ELSE m.role END,
		(SELECT count(*) FROM dg_files WHERE project_id = p.id),
		(SELECT count(*) FROM dg_folders WHERE project_id = p.id),
		COALESCE(t.id, ''), t.thumbnail_hash`+join+`
		LEFT JOIN LATERAL (SELECT id, thumbnail_hash FROM dg_files WHERE project_id = p.id
			ORDER BY position, name, id LIMIT 1) t ON true`+where+
		` ORDER BY `+key+` `+dir+`, p.id LIMIT $3 OFFSET $4`,
		userID, pattern, q.PageSize, (q.Page-1)*q.PageSize)
	if err != nil {
		return nil, 0, err
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (diagram.ProjectSummary, error) {
		var s diagram.ProjectSummary
		err := row.Scan(&s.ID, &s.OwnerID, &s.Name, &s.TreeRev, &s.CreatedAt, &s.UpdatedAt, &s.UpdatedBy,
			&s.Role, &s.FileCount, &s.FolderCount, &s.ThumbFileID, &s.ThumbnailHash)
		return s, err
	})
	return items, total, err
}

func (r *DiagramRepo) Counts(ctx context.Context, projectID string) (files, folders int, err error) {
	err = r.db.QueryRow(ctx, `SELECT (SELECT count(*) FROM dg_files WHERE project_id = $1),
		(SELECT count(*) FROM dg_folders WHERE project_id = $1)`, projectID).Scan(&files, &folders)
	return files, folders, err
}

func (r *DiagramRepo) ProjectThumb(ctx context.Context, projectID string) (string, *string, error) {
	var id string
	var hash *string
	err := r.db.QueryRow(ctx, `SELECT id, thumbnail_hash FROM dg_files WHERE project_id = $1
		ORDER BY position, name, id LIMIT 1`, projectID).Scan(&id, &hash)
	return id, hash, ignoreNoRows(err)
}

func (r *DiagramRepo) RenameProject(ctx context.Context, id, name string, at time.Time) error {
	return execOne(ctx, r.db, `UPDATE dg_projects SET name = $2, updated_at = $3 WHERE id = $1`, id, name, at)
}

func (r *DiagramRepo) TouchTree(ctx context.Context, id string, by int, at time.Time) (int, error) {
	var rev int
	err := r.db.QueryRow(ctx, `UPDATE dg_projects SET tree_rev = tree_rev + 1, updated_at = $3, updated_by = $2
		WHERE id = $1 RETURNING tree_rev`, id, userRef(by), at).Scan(&rev)
	return rev, notFound(err)
}

// DeleteProject cascades to folders, files, history and members.
func (r *DiagramRepo) DeleteProject(ctx context.Context, id string) error {
	return execOne(ctx, r.db, `DELETE FROM dg_projects WHERE id = $1`, id)
}

func (r *DiagramRepo) MemberRole(ctx context.Context, projectID string, userID int) (diagram.Role, error) {
	var role diagram.Role
	err := r.db.QueryRow(ctx, `SELECT role FROM dg_project_members WHERE project_id = $1 AND user_id = $2`,
		projectID, userID).Scan(&role)
	return role, ignoreNoRows(err)
}

func (r *DiagramRepo) FileRole(ctx context.Context, fileID string, userID int) (diagram.Role, error) {
	var role diagram.Role
	err := r.db.QueryRow(ctx, `SELECT role FROM dg_file_members WHERE file_id = $1 AND user_id = $2`,
		fileID, userID).Scan(&role)
	return role, ignoreNoRows(err)
}

const folderCols = `id, project_id, parent_id, name, position, created_at, updated_at`

func scanFolder(row pgx.Row) (diagram.Folder, error) {
	var f diagram.Folder
	err := row.Scan(&f.ID, &f.ProjectID, &f.ParentID, &f.Name, &f.Position, &f.CreatedAt, &f.UpdatedAt)
	return f, notFound(err)
}

const fileCols = `id, project_id, folder_id, name, kind, position, version, page_count, thumbnail_hash,
	created_at, updated_at, COALESCE(updated_by, 0), COALESCE(updated_by_share, ''), restored`

func scanFile(row pgx.Row, extra ...any) (diagram.File, error) {
	var f diagram.File
	dst := append([]any{&f.ID, &f.ProjectID, &f.FolderID, &f.Name, &f.Kind, &f.Position, &f.Version, &f.PageCount,
		&f.ThumbnailHash, &f.CreatedAt, &f.UpdatedAt, &f.UpdatedBy, &f.UpdatedByShare, &f.Restored}, extra...)
	return f, notFound(row.Scan(dst...))
}

func (r *DiagramRepo) Tree(ctx context.Context, projectID string) ([]diagram.Folder, []diagram.File, error) {
	rows, err := r.db.Query(ctx, `SELECT `+folderCols+` FROM dg_folders WHERE project_id = $1 ORDER BY position, name, id`, projectID)
	if err != nil {
		return nil, nil, err
	}
	folders, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (diagram.Folder, error) { return scanFolder(row) })
	if err != nil {
		return nil, nil, err
	}
	rows, err = r.db.Query(ctx, `SELECT `+fileCols+` FROM dg_files WHERE project_id = $1 ORDER BY position, name, id`, projectID)
	if err != nil {
		return nil, nil, err
	}
	files, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (diagram.File, error) { return scanFile(row) })
	return folders, files, err
}

func (r *DiagramRepo) Folder(ctx context.Context, id string) (diagram.Folder, error) {
	return scanFolder(r.db.QueryRow(ctx, `SELECT `+folderCols+` FROM dg_folders WHERE id = $1`, id))
}

func (r *DiagramRepo) CreateFolder(ctx context.Context, f diagram.Folder) error {
	_, err := r.db.Exec(ctx, `INSERT INTO dg_folders (`+folderCols+`) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		f.ID, f.ProjectID, f.ParentID, f.Name, f.Position, f.CreatedAt, f.UpdatedAt)
	if pgCode(err) == pgUniqueViolation {
		return domain.ErrConflict
	}
	return err
}

func (r *DiagramRepo) UpdateFolder(ctx context.Context, f diagram.Folder) error {
	return execOne(ctx, r.db, `UPDATE dg_folders SET parent_id = $2, name = $3, position = $4, updated_at = $5 WHERE id = $1`,
		f.ID, f.ParentID, f.Name, f.Position, f.UpdatedAt)
}

func (r *DiagramRepo) NextPosition(ctx context.Context, projectID string, parentID *string) (float64, error) {
	var pos float64
	err := r.db.QueryRow(ctx, `SELECT COALESCE(max(p) + 1, 0) FROM (
		SELECT position AS p FROM dg_files WHERE project_id = $1 AND folder_id IS NOT DISTINCT FROM $2::text
		UNION ALL
		SELECT position FROM dg_folders WHERE project_id = $1 AND parent_id IS NOT DISTINCT FROM $2::text) s`,
		projectID, parentID).Scan(&pos)
	return pos, err
}

// DeleteFolders removes the folders; their files and the files' history go
// with them (ON DELETE CASCADE).
func (r *DiagramRepo) DeleteFolders(ctx context.Context, projectID string, ids []string) (int, error) {
	var files int
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM dg_files WHERE project_id = $1 AND folder_id = ANY($2)`,
			projectID, ids).Scan(&files); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM dg_folders WHERE project_id = $1 AND id = ANY($2)`, projectID, ids)
		return err
	})
	return files, err
}

func (r *DiagramRepo) InsertTree(ctx context.Context, folders []diagram.Folder, files []diagram.NewFile) error {
	return pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		if len(folders) > 0 {
			var ids, projects, names []string
			var parents []*string
			var positions []float64
			var created, updated []time.Time
			for _, f := range folders {
				ids, projects, parents, names = append(ids, f.ID), append(projects, f.ProjectID), append(parents, f.ParentID), append(names, f.Name)
				positions, created, updated = append(positions, f.Position), append(created, f.CreatedAt), append(updated, f.UpdatedAt)
			}
			// one statement, so a child may come before its parent
			_, err := tx.Exec(ctx, `INSERT INTO dg_folders (`+folderCols+`)
				SELECT * FROM unnest($1::text[], $2::text[], $3::text[], $4::text[], $5::float8[], $6::timestamptz[], $7::timestamptz[])`,
				ids, projects, parents, names, positions, created, updated)
			if err != nil {
				return err
			}
		}
		for _, f := range files {
			if err := insertFile(ctx, tx, f); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *DiagramRepo) File(ctx context.Context, id string) (diagram.Doc, error) {
	var d diagram.Doc
	var err error
	d.File, err = scanFile(r.db.QueryRow(ctx, `SELECT `+fileCols+`, data FROM dg_files WHERE id = $1`, id), &d.Data)
	return d, err
}

func insertFile(ctx context.Context, tx pgx.Tx, f diagram.NewFile) error {
	d := f.Doc
	_, err := tx.Exec(ctx, `INSERT INTO dg_files (id, project_id, folder_id, name, kind, position, data, version, page_count,
		thumbnail_hash, created_at, updated_at, updated_by, updated_by_share) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
		d.ID, d.ProjectID, d.FolderID, d.Name, d.Kind, d.Position, d.Data, d.Version, d.PageCount,
		d.ThumbnailHash, d.CreatedAt, d.UpdatedAt, userRef(d.UpdatedBy), shareRef(d.UpdatedByShare))
	if pgCode(err) == pgUniqueViolation {
		return domain.ErrConflict
	}
	if err != nil {
		return err
	}
	return addVersion(ctx, tx, d.ID, f.First)
}

func addVersion(ctx context.Context, tx pgx.Tx, fileID string, v diagram.Version) error {
	_, err := tx.Exec(ctx, `INSERT INTO dg_file_versions (id, file_id, version, name, data, label, named, created_by, created_by_share, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		diagram.NewID(diagram.PrefixVersion), fileID, v.Version, v.Name, v.Data, v.Label, v.Named, userRef(v.CreatedBy), shareRef(v.CreatedByShare), v.CreatedAt)
	return err
}

func (r *DiagramRepo) InsertFile(ctx context.Context, f diagram.NewFile) error {
	return pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error { return insertFile(ctx, tx, f) })
}

func (r *DiagramRepo) UpdateFileMeta(ctx context.Context, f diagram.File) error {
	return execOne(ctx, r.db, `UPDATE dg_files SET name = $2, kind = $3, folder_id = $4, position = $5, updated_at = $6 WHERE id = $1`,
		f.ID, f.Name, f.Kind, f.FolderID, f.Position, f.UpdatedAt)
}

// SaveFile is a compare-and-swap on the version: the UPDATE matches only
// while the file is still at expected, so concurrent writers cannot both win.
func (r *DiagramRepo) SaveFile(ctx context.Context, f diagram.Doc, expected int, v diagram.Version) (bool, error) {
	return r.saveFile(ctx, f, expected, v, false)
}

// SaveFileOver is SaveFile that rewrites the snapshot of version expected.
func (r *DiagramRepo) SaveFileOver(ctx context.Context, f diagram.Doc, expected int, v diagram.Version) (bool, error) {
	return r.saveFile(ctx, f, expected, v, true)
}

func (r *DiagramRepo) saveFile(ctx context.Context, f diagram.Doc, expected int, v diagram.Version, over bool) (bool, error) {
	saved := false
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE dg_files SET name = $3, kind = $4, data = $5, version = $6, page_count = $7,
			updated_at = $8, updated_by = $9, updated_by_share = $10, restored = $11 WHERE id = $1 AND version = $2`,
			f.ID, expected, f.Name, f.Kind, f.Data, f.Version, f.PageCount, f.UpdatedAt, userRef(f.UpdatedBy), shareRef(f.UpdatedByShare), f.Restored)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		saved = true
		if over {
			tag, err := tx.Exec(ctx, `UPDATE dg_file_versions SET version = $3, name = $4, data = $5, created_at = $6
				WHERE file_id = $1 AND version = $2 AND NOT named AND label IS NULL`,
				f.ID, expected, v.Version, v.Name, v.Data, v.CreatedAt)
			if err != nil || tag.RowsAffected() > 0 {
				return err
			}
		}
		return addVersion(ctx, tx, f.ID, v)
	})
	return saved && err == nil, err
}

// DeleteFile cascades to the file's history and member overrides.
func (r *DiagramRepo) DeleteFile(ctx context.Context, id string) error {
	return execOne(ctx, r.db, `DELETE FROM dg_files WHERE id = $1`, id)
}

const versionCols = `version, name, label, named, COALESCE(created_by, 0), COALESCE(created_by_share, ''), created_at`

func scanVersion(row pgx.Row, extra ...any) (diagram.Version, error) {
	var v diagram.Version
	dst := append([]any{&v.Version, &v.Name, &v.Label, &v.Named, &v.CreatedBy, &v.CreatedByShare, &v.CreatedAt}, extra...)
	return v, notFound(row.Scan(dst...))
}

func (r *DiagramRepo) Versions(ctx context.Context, fileID string) ([]diagram.Version, error) {
	rows, err := r.db.Query(ctx, `SELECT `+versionCols+`, COALESCE((SELECT COALESCE(NULLIF(u.display_name, ''), u.username) FROM users u WHERE u.id = dg_file_versions.created_by), '')
		FROM dg_file_versions WHERE file_id = $1 ORDER BY version DESC`, fileID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (diagram.Version, error) {
		var name string
		v, err := scanVersion(row, &name)
		v.CreatedByName = name
		return v, err
	})
}

func (r *DiagramRepo) GetVersion(ctx context.Context, fileID string, version int) (diagram.Version, error) {
	var data json.RawMessage
	v, err := scanVersion(r.db.QueryRow(ctx, `SELECT `+versionCols+`, data FROM dg_file_versions
		WHERE file_id = $1 AND version = $2`, fileID, version), &data)
	v.Data = data
	return v, err
}

func (r *DiagramRepo) VersionInfo(ctx context.Context, fileID string, version int) (diagram.Version, error) {
	return scanVersion(r.db.QueryRow(ctx, `SELECT `+versionCols+` FROM dg_file_versions
		WHERE file_id = $1 AND version = $2`, fileID, version))
}

func (r *DiagramRepo) CountVersions(ctx context.Context, fileID string) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `SELECT count(*) FROM dg_file_versions WHERE file_id = $1`, fileID).Scan(&n)
	return n, err
}

func (r *DiagramRepo) NameHead(ctx context.Context, fileID, label string, by int, at time.Time) (diagram.Version, error) {
	var v diagram.Version
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		var head int
		var name string
		var data json.RawMessage
		if err := tx.QueryRow(ctx, `SELECT version, name, data FROM dg_files WHERE id = $1 FOR UPDATE`, fileID).
			Scan(&head, &name, &data); err != nil {
			return notFound(err)
		}
		var err error
		v, err = scanVersion(tx.QueryRow(ctx, `INSERT INTO dg_file_versions (id, file_id, version, name, data, label, named, created_by, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, true, $7, $8)
			ON CONFLICT (file_id, version) DO UPDATE SET label = EXCLUDED.label, named = true
			RETURNING `+versionCols, diagram.NewID(diagram.PrefixVersion), fileID, head, name, data, label, userRef(by), at))
		return err
	})
	return v, err
}

func (r *DiagramRepo) DeleteVersions(ctx context.Context, fileID string, versions []int) error {
	_, err := r.db.Exec(ctx, `DELETE FROM dg_file_versions WHERE file_id = $1 AND version = ANY($2)`, fileID, versions)
	return err
}

func (r *DiagramRepo) Prefs(ctx context.Context, userID int) (json.RawMessage, error) {
	var data json.RawMessage
	err := r.db.QueryRow(ctx, `SELECT data FROM dg_user_prefs WHERE user_id = $1`, userID).Scan(&data)
	if ignoreNoRows(err) != nil {
		return nil, err
	}
	if data == nil {
		data = json.RawMessage(`{}`)
	}
	return data, nil
}

func (r *DiagramRepo) UpdatePrefs(ctx context.Context, userID int, at time.Time, apply func(json.RawMessage) (json.RawMessage, error)) (json.RawMessage, error) {
	var out json.RawMessage
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		// make sure the row exists so FOR UPDATE serializes concurrent patches
		if _, err := tx.Exec(ctx, `INSERT INTO dg_user_prefs (user_id, data, updated_at) VALUES ($1, '{}', $2)
			ON CONFLICT (user_id) DO NOTHING`, userID, at); err != nil {
			return err
		}
		var cur json.RawMessage
		if err := tx.QueryRow(ctx, `SELECT data FROM dg_user_prefs WHERE user_id = $1 FOR UPDATE`, userID).Scan(&cur); err != nil {
			return err
		}
		next, err := apply(cur)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE dg_user_prefs SET data = $2, updated_at = $3 WHERE user_id = $1`, userID, next, at); err != nil {
			return err
		}
		out = next
		return nil
	})
	return out, err
}

var (
	_ diagram.Repository      = (*DiagramRepo)(nil)
	_ diagram.PrefsRepository = (*DiagramRepo)(nil)
)
