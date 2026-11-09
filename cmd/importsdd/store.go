package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/postgres"
)

// pgStore writes into the blog's Postgres.
type pgStore struct {
	db *pgxpool.Pool
	// identity makes Guard accounts (-create-missing); nil: no user creation.
	identity application.Identity
	users    *postgres.UserRepo
}

func newPGStore(db *pgxpool.Pool, identity application.Identity) *pgStore {
	return &pgStore{db: db, identity: identity, users: postgres.NewUserRepo(db)}
}

func (s *pgStore) Users(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.Query(ctx, `SELECT lower(email), id FROM users WHERE email IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	defer rows.Close()
	for rows.Next() {
		var e string
		var id int
		if err := rows.Scan(&e, &id); err != nil {
			return nil, err
		}
		out[e] = id
	}
	return out, rows.Err()
}

// CreateUser makes the blog user and its Guard account with a random
// password nobody knows, then puts the old argon2id hash in its place so the
// old password keeps working. The email stays unverified. Without a usable
// old hash the user has to use "forgot password".
func (s *pgStore) CreateUser(ctx context.Context, u newUser) (int, error) {
	if s.identity == nil {
		return 0, fmt.Errorf("no Guard available")
	}
	created, err := s.users.Create(ctx, u.Username, u.Email)
	if err != nil {
		return 0, err
	}
	var pw [24]byte
	_, _ = rand.Read(pw[:])
	if err := s.identity.CreateAccount(ctx, created.ID, u.Email, base64.RawURLEncoding.EncodeToString(pw[:])); err != nil {
		_ = s.users.Delete(ctx, created.ID)
		return 0, err
	}
	if u.PasswordHash != "" {
		if _, err := s.db.Exec(ctx, `UPDATE guard_account SET secret = $2 WHERE user_id = $1`, created.ID, u.PasswordHash); err != nil {
			return created.ID, fmt.Errorf("keep old password: %w", err)
		}
	}
	return created.ID, nil
}

func (s *pgStore) ProjectExists(ctx context.Context, id string) (bool, error) {
	var ok bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM dg_projects WHERE id = $1)`, id).Scan(&ok)
	return ok, err
}

func (s *pgStore) ImportProject(ctx context.Context, p *projectBundle) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		return runBatch(ctx, tx, projectBatch(p))
	})
}

func (s *pgStore) ImportGlobal(ctx context.Context, g *globalBundle) (map[string]int, error) {
	got := map[string]int{}
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		b := &pgx.Batch{}
		var entities []string
		queue := func(entity, sql string, args ...any) {
			b.Queue(sql, args...)
			entities = append(entities, entity)
		}
		for _, a := range g.Assets {
			queue("asset", `INSERT INTO dg_assets (hash, mime, size, uploaded_by, created_at) VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`,
				a.Hash, a.Mime, a.Size, a.UploadedBy, a.CreatedAt)
		}
		for _, p := range g.Prefs {
			queue("prefs", `INSERT INTO dg_user_prefs (user_id, data, updated_at) VALUES ($1,$2::jsonb,$3) ON CONFLICT DO NOTHING`,
				p.UserID, string(p.Data), p.UpdatedAt)
		}
		for _, t := range g.Tokens {
			queue("access token", `INSERT INTO dg_access_tokens (id, user_id, name, token_hash, prefix, scopes, project_ids, expires_at, last_used_at, revoked_at, created_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT DO NOTHING`,
				t.ID, t.UserID, t.Name, t.Hash, t.Prefix, t.Scopes, t.ProjectIDs, t.ExpiresAt, t.LastUsedAt, t.RevokedAt, t.CreatedAt)
		}
		for _, a := range g.Audit {
			queue("mcp audit", `INSERT INTO dg_mcp_audit (id, user_id, token_id, tool, project_id, file_id, status, detail, duration_ms, created_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT DO NOTHING`,
				a.ID, a.UserID, a.TokenID, a.Tool, a.ProjectID, a.FileID, a.Status, a.Detail, a.DurationMs, a.CreatedAt)
		}
		br := tx.SendBatch(ctx, b)
		defer br.Close()
		for _, e := range entities {
			tag, err := br.Exec()
			if err != nil {
				return fmt.Errorf("%s: %w", e, err)
			}
			got[e] += int(tag.RowsAffected())
		}
		return br.Close()
	})
	return got, err
}

func runBatch(ctx context.Context, tx pgx.Tx, b *pgx.Batch) error {
	br := tx.SendBatch(ctx, b)
	defer br.Close()
	for i := range b.Len() {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("statement %d (%s): %w", i+1, strings.Fields(b.QueuedQueries[i].SQL)[2], err)
		}
	}
	return br.Close()
}

// projectBatch is the project's rows in foreign key order.
func projectBatch(p *projectBundle) *pgx.Batch {
	b := &pgx.Batch{}
	pr := p.Project
	b.Queue(`INSERT INTO dg_projects (id, owner_id, name, tree_rev, created_at, updated_at, updated_by) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		pr.ID, pr.OwnerID, pr.Name, pr.TreeRev, pr.CreatedAt, pr.UpdatedAt, pr.UpdatedBy)
	for _, f := range p.Folders {
		b.Queue(`INSERT INTO dg_folders (id, project_id, parent_id, name, position, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			f.ID, f.ProjectID, f.ParentID, f.Name, f.Position, f.CreatedAt, f.UpdatedAt)
	}
	for _, f := range p.Files {
		b.Queue(`INSERT INTO dg_files (id, project_id, folder_id, name, kind, file_kind, position, data, version, page_count, thumbnail_hash, created_at, updated_at, updated_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9,$10,$11,$12,$13,$14)`,
			f.ID, f.ProjectID, f.FolderID, f.Name, f.Kind, f.FileKind, f.Position, string(f.Data), f.Version, f.PageCount, f.ThumbnailHash, f.CreatedAt, f.UpdatedAt, f.UpdatedBy)
	}
	for _, v := range p.Versions {
		b.Queue(`INSERT INTO dg_file_versions (id, file_id, version, name, data, label, named, created_by, created_at) VALUES ($1,$2,$3,$4,$5::jsonb,$6,$7,$8,$9)`,
			v.ID, v.FileID, v.Version, v.Name, string(v.Data), v.Label, v.Named, v.CreatedBy, v.CreatedAt)
	}
	for _, m := range p.Members {
		b.Queue(`INSERT INTO dg_project_members (project_id, user_id, role, created_at) VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`, m.ParentID, m.UserID, m.Role, m.CreatedAt)
	}
	for _, m := range p.FileMembers {
		b.Queue(`INSERT INTO dg_file_members (file_id, user_id, role, created_at) VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`, m.ParentID, m.UserID, m.Role, m.CreatedAt)
	}
	for _, s := range p.Shares {
		b.Queue(`INSERT INTO dg_share_links (id, project_id, file_id, token_hash, role, expires_at, revoked_at, created_by, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			s.ID, s.ProjectID, s.FileID, s.TokenHash, s.Role, s.ExpiresAt, s.RevokedAt, s.CreatedBy, s.CreatedAt)
	}
	if l := p.Library; l != nil {
		b.Queue(`INSERT INTO dg_project_library (project_id, rev, data, updated_at, updated_by) VALUES ($1,$2,$3::jsonb,$4,$5)`,
			l.ProjectID, l.Rev, string(l.Data), l.UpdatedAt, l.UpdatedBy)
	}
	for _, x := range p.Private {
		b.Queue(`INSERT INTO dg_file_private (file_id, user_id, data, updated_at) VALUES ($1,$2,$3::jsonb,$4) ON CONFLICT DO NOTHING`, x.FileID, x.UserID, string(x.Data), x.UpdatedAt)
	}
	for _, c := range p.Comments {
		var anchor *string
		if c.Anchor != nil {
			a := string(c.Anchor)
			anchor = &a
		}
		b.Queue(`INSERT INTO dg_comments (id, file_id, project_id, parent_id, author_id, body, anchor, resolved_at, resolved_by, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8,$9,$10,$11)`,
			c.ID, c.FileID, c.ProjectID, c.ParentID, c.AuthorID, c.Body, anchor, c.ResolvedAt, c.ResolvedBy, c.CreatedAt, c.UpdatedAt)
	}
	for _, m := range p.Mentions {
		b.Queue(`INSERT INTO dg_comment_mentions (comment_id, user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, m.CommentID, m.UserID)
	}
	for _, r := range p.Reads {
		b.Queue(`INSERT INTO dg_comment_reads (file_id, user_id, read_at) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, r.FileID, r.UserID, r.ReadAt)
	}
	for _, n := range p.Notifs {
		b.Queue(`INSERT INTO notifications (user_id, type, actor_id, dg_project_id, dg_file_id, dg_comment_id, text, read_at, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			n.UserID, n.Type, n.ActorID, n.ProjectID, n.FileID, n.CommentID, n.Text, n.ReadAt, n.CreatedAt)
	}
	for _, w := range p.Webhooks {
		b.Queue(`INSERT INTO dg_webhooks (id, project_id, url, secret, events, active, created_by, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			w.ID, w.ProjectID, w.URL, w.Secret, w.Events, w.Active, w.CreatedBy, w.CreatedAt)
	}
	for _, y := range p.YDocs {
		b.Queue(`INSERT INTO dg_file_ydocs (file_id, snapshot, seq, updated_at) VALUES ($1,$2,$3,$4)`, y.FileID, y.Snapshot, y.Seq, y.UpdatedAt)
	}
	for _, y := range p.YUpdates {
		b.Queue(`INSERT INTO dg_file_yupdates (file_id, seq, data, created_at) VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`, y.FileID, y.Seq, y.Data, y.CreatedAt)
	}
	return b
}

// dryStore answers lookups from memory and records what it is given: the
// dry run (fed from the target database when it is reachable) and tests.
type dryStore struct {
	mu       sync.Mutex
	users    map[string]int
	projects map[string]bool
	nextUser int

	Created  []newUser
	Projects []*projectBundle
	Global   *globalBundle
}

func (d *dryStore) Users(context.Context) (map[string]int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make(map[string]int, len(d.users))
	for k, v := range d.users {
		out[k] = v
	}
	return out, nil
}

func (d *dryStore) CreateUser(_ context.Context, u newUser) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.nextUser++
	d.Created = append(d.Created, u)
	if d.users == nil {
		d.users = map[string]int{}
	}
	d.users[strings.ToLower(u.Email)] = 1000 + d.nextUser
	return 1000 + d.nextUser, nil
}

func (d *dryStore) ProjectExists(_ context.Context, id string) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.projects[id], nil
}

func (d *dryStore) ImportProject(_ context.Context, b *projectBundle) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.projects == nil {
		d.projects = map[string]bool{}
	}
	d.projects[b.Project.ID] = true
	d.Projects = append(d.Projects, b)
	return nil
}

func (d *dryStore) ImportGlobal(_ context.Context, g *globalBundle) (map[string]int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.Global = g
	return map[string]int{"asset": len(g.Assets), "prefs": len(g.Prefs), "access token": len(g.Tokens), "mcp audit": len(g.Audit)}, nil
}
