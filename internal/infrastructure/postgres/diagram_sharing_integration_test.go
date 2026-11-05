package postgres_test

// Diagram sharing against a real Postgres: members, share links, assets,
// the library's compare-and-swap and private stickies, with the cascades
// that keep them from outliving their project or file. Skipped with -short
// or without Docker.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/application"
	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/postgres"
)

type blobStore struct {
	mu   sync.Mutex
	data map[string][]byte
}

func (b *blobStore) Put(_ context.Context, key string, r io.Reader, _ int64, _ string) error {
	data, err := io.ReadAll(r)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data[key] = data
	return err
}

func (b *blobStore) Get(_ context.Context, key string) (application.Object, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	data, ok := b.data[key]
	if !ok {
		return application.Object{}, domain.ErrNotFound
	}
	return application.Object{Body: io.NopCloser(bytes.NewReader(data)), Size: int64(len(data))}, nil
}

var itPNG = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{9}, 32)...)

const itLibrary = `{"components":{},"styles":{"colors":{},"texts":{},"effects":{}},"tokens":{"t":{"id":"t","name":"n","light":"#fff","dark":"#000"}},"future":{"a":[1,2]}}`

func TestDiagramSharingRepo(t *testing.T) {
	db := connect(t)
	ctx := context.Background()
	users := postgres.NewUserRepo(db)
	ali, err := users.Create(ctx, "dgs_ali", "dgs_ali@example.com")
	must(t, err)
	vali, err := users.Create(ctx, "dgs_vali", "dgs_vali@example.com")
	must(t, err)
	eve, err := users.Create(ctx, "dgs_eve", "dgs_eve@example.com")
	must(t, err)
	repo := postgres.NewDiagramRepo(db)
	d := application.NewDiagrams(repo, repo)
	d.Users, d.Blobs = users, &blobStore{data: map[string][]byte{}}

	made, err := d.CreateProject(ctx, ali.ID, application.NewDiagramProject{Name: "Sharing", Data: dgDoc("a")})
	must(t, err)
	pid, fid := made.Project.ID, made.File.ID

	t.Run("members resolve accounts by email and username", func(t *testing.T) {
		m, err := d.AddMember(ctx, ali.ID, pid, "DGS_VALI@example.com", diagram.RoleViewer)
		must(t, err)
		if m.UserID != vali.ID || m.Name != "dgs_vali" || m.Role != diagram.RoleViewer {
			t.Errorf("member = %+v", m)
		}
		if _, err := d.AddMember(ctx, ali.ID, pid, "dgs_eve", diagram.RoleEditor); err != nil {
			t.Fatal(err)
		}
		if _, err := d.AddMember(ctx, ali.ID, pid, "dgs_vali", diagram.RoleEditor); err != nil { // re-invite
			t.Fatal(err)
		}
		items, err := d.Members(ctx, ali.ID, pid)
		must(t, err)
		if len(items) != 3 || items[0].UserID != ali.ID || !items[0].IsOriginalOwner || items[0].Email != "dgs_ali@example.com" {
			t.Fatalf("members = %+v", items)
		}
		if f, err := d.GetFile(ctx, vali.ID, fid); err != nil || f.Role != diagram.RoleEditor {
			t.Errorf("role after re-invite: %v %s", err, f.Role)
		}
		if _, err := d.AddMember(ctx, ali.ID, pid, "nobody@example.com", diagram.RoleViewer); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("unknown account: %v", err)
		}
		if _, err := d.AddMember(ctx, ali.ID, pid, ali.Email, diagram.RoleViewer); err == nil {
			t.Error("the owner is not a member")
		}
		if err := d.SetMemberRole(ctx, ali.ID, pid, vali.ID, diagram.RoleViewer); err != nil {
			t.Error(err)
		}
		if err := d.SetMemberRole(ctx, ali.ID, pid, 987654, diagram.RoleViewer); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("non-member: %v", err)
		}
		if got := rowCount(t, db, "dg_project_members WHERE project_id = '"+pid+"'"); got != 2 {
			t.Errorf("rows = %d", got)
		}
	})

	t.Run("a role the schema does not know is refused by the database too", func(t *testing.T) {
		if err := repo.SetMember(ctx, pid, eve.ID, "owner", time.Now()); err == nil {
			t.Error("CHECK (role IN ('viewer','editor')) must hold")
		}
	})

	t.Run("overrides go with the member and with the file", func(t *testing.T) {
		second, _, err := d.CreateFile(ctx, ali.ID, pid, application.NewDiagramFile{Name: "second", Data: dgDoc("b")})
		must(t, err)
		must(t, d.SetFileMember(ctx, ali.ID, fid, vali.ID, diagram.RoleViewer))
		must(t, d.SetFileMember(ctx, ali.ID, second.ID, vali.ID, diagram.RoleViewer))
		must(t, d.SetFileMember(ctx, ali.ID, second.ID, vali.ID, diagram.RoleEditor)) // upsert
		ms, err := d.FileMembers(ctx, ali.ID, second.ID)
		must(t, err)
		if len(ms) != 1 || ms[0].Role != diagram.RoleEditor || ms[0].Email != "dgs_vali@example.com" {
			t.Errorf("overrides = %+v", ms)
		}
		if err := d.SetFileMember(ctx, ali.ID, fid, 987654, diagram.RoleViewer); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("non-member override: %v", err)
		}
		must(t, d.RemoveFileMember(ctx, ali.ID, fid, vali.ID))
		if err := d.RemoveFileMember(ctx, ali.ID, fid, vali.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("second removal: %v", err)
		}
		// removing the member takes its other overrides along
		must(t, d.RemoveMember(ctx, vali.ID, pid, vali.ID))
		if got := rowCount(t, db, "dg_file_members WHERE user_id = "+itoa(vali.ID)); got != 0 {
			t.Errorf("%d overrides outlived the member", got)
		}
		if _, err := d.GetFile(ctx, vali.ID, fid); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("after leaving: %v", err)
		}
		must(t, d.DeleteFile(ctx, ali.ID, second.ID))
	})

	t.Run("share links store only the token hash", func(t *testing.T) {
		link, token, err := d.CreateShare(ctx, ali.ID, pid, application.NewShare{Role: diagram.RoleEditor, FileID: &fid})
		must(t, err)
		var stored string
		must(t, db.QueryRow(ctx, `SELECT token_hash FROM dg_share_links WHERE id = $1`, link.ID).Scan(&stored))
		if stored != diagram.HashToken(token) || strings.Contains(stored, token) {
			t.Errorf("stored = %q", stored)
		}
		var hits int
		must(t, db.QueryRow(ctx, `SELECT count(*) FROM dg_share_links WHERE token_hash = $1 OR id = $1 OR file_id = $1`, token).Scan(&hits))
		if hits != 0 {
			t.Error("the raw token is in the table")
		}

		v, err := d.OpenShare(ctx, token)
		must(t, err)
		if v.File == nil || v.File.ID != fid || v.File.Role != diagram.RoleEditor {
			t.Errorf("view = %+v", v)
		}
		saved, err := d.SaveShare(ctx, token, "", nil, dgDoc("a", "b"), `"`+itoa(v.File.Version)+`"`)
		must(t, err)
		if saved.Version != v.File.Version+1 || saved.UpdatedBy != 0 {
			t.Errorf("saved = v%d by %d", saved.Version, saved.UpdatedBy)
		}
		if _, err := d.SaveShare(ctx, token, "", nil, dgDoc("c"), `"`+itoa(v.File.Version)+`"`); !errors.Is(err, diagram.ErrPrecondition) {
			t.Errorf("stale share save: %v", err)
		}

		list, err := d.Shares(ctx, ali.ID, pid)
		must(t, err)
		if len(list) != 1 || list[0].CreatedBy != ali.ID || list[0].FileID == nil || *list[0].FileID != fid {
			t.Errorf("list = %+v", list)
		}
		must(t, d.RevokeShare(ctx, ali.ID, pid, link.ID))
		if _, err := d.OpenShare(ctx, token); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("revoked: %v", err)
		}
	})

	t.Run("expiry, project links and cascades", func(t *testing.T) {
		link, token, err := d.CreateShare(ctx, ali.ID, pid, application.NewShare{Role: diagram.RoleViewer, ExpiresInSeconds: ptr(3600)})
		must(t, err)
		if _, err := d.OpenShare(ctx, token); err != nil {
			t.Fatal(err)
		}
		_, err = db.Exec(ctx, `UPDATE dg_share_links SET expires_at = now() - interval '1 second' WHERE id = $1`, link.ID)
		must(t, err)
		if _, err := d.OpenShare(ctx, token); !errors.Is(err, diagram.ErrGone) {
			t.Errorf("expired: %v", err)
		}

		doomed, _, err := d.CreateFile(ctx, ali.ID, pid, application.NewDiagramFile{Name: "doomed", Data: dgDoc("x")})
		must(t, err)
		_, ft, err := d.CreateShare(ctx, ali.ID, pid, application.NewShare{Role: diagram.RoleViewer, FileID: &doomed.ID})
		must(t, err)
		must(t, d.DeleteFile(ctx, ali.ID, doomed.ID))
		if _, err := d.OpenShare(ctx, ft); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("link of a deleted file: %v", err)
		}

		p2, err := d.CreateProject(ctx, ali.ID, application.NewDiagramProject{Name: "Gone", Data: dgDoc("a")})
		must(t, err)
		_, pt, err := d.CreateShare(ctx, ali.ID, p2.Project.ID, application.NewShare{Role: diagram.RoleViewer})
		must(t, err)
		_, err = d.SaveLibrary(ctx, ali.ID, p2.Project.ID, 0, json.RawMessage(itLibrary))
		must(t, err)
		must(t, d.DeleteProject(ctx, ali.ID, p2.Project.ID))
		if _, err := d.OpenShare(ctx, pt); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("link of a deleted project: %v", err)
		}
		if got := rowCount(t, db, "dg_project_library WHERE project_id = '"+p2.Project.ID+"'"); got != 0 {
			t.Errorf("library outlived its project")
		}
	})

	t.Run("assets are content-addressed", func(t *testing.T) {
		var wg sync.WaitGroup
		results := make([]application.StoredAsset, 6)
		for i := range results {
			wg.Add(1)
			go func() {
				defer wg.Done()
				a, err := d.StoreAsset(ctx, ali.ID, itPNG)
				if err != nil {
					t.Error(err)
				}
				results[i] = a
			}()
		}
		wg.Wait()
		fresh := 0
		for _, a := range results {
			if !a.Deduped {
				fresh++
			}
			if a.Hash != diagram.HashBytes(itPNG) || a.Mime != "image/png" {
				t.Errorf("asset = %+v", a)
			}
		}
		// the Put race may let two uploads both see no row, but exactly one inserts
		if fresh != 1 {
			t.Errorf("%d uploads reported new bytes, want 1", fresh)
		}
		if got := rowCount(t, db, "dg_assets WHERE hash = '"+diagram.HashBytes(itPNG)+"'"); got != 1 {
			t.Errorf("rows = %d", got)
		}
		meta, err := d.AssetMeta(ctx, diagram.HashBytes(itPNG))
		must(t, err)
		if meta.Size != len(itPNG) || meta.UploadedBy != ali.ID {
			t.Errorf("meta = %+v", meta)
		}
		if err := func() error {
			_, err := repo.InsertAsset(ctx, diagram.Asset{Hash: "NOT-A-HASH", Mime: "image/png", Size: 1})
			return err
		}(); err == nil {
			t.Error("CHECK (hash ~ '^[0-9a-f]{64}$') must hold")
		}
	})

	t.Run("thumbnails point at assets without touching the version", func(t *testing.T) {
		before, err := d.GetFile(ctx, ali.ID, fid)
		must(t, err)
		hash, err := d.SetThumbnail(ctx, ali.ID, fid, itPNG)
		must(t, err)
		after, err := d.GetFile(ctx, ali.ID, fid)
		must(t, err)
		if after.Version != before.Version || after.ThumbnailHash == nil || *after.ThumbnailHash != hash {
			t.Errorf("file = v%d %v", after.Version, after.ThumbnailHash)
		}
		tree, err := d.Tree(ctx, ali.ID, pid)
		must(t, err)
		if tree.Files[0].ThumbnailHash == nil || *tree.Files[0].ThumbnailHash != hash {
			t.Errorf("tree = %+v", tree.Files[0])
		}
		if a, err := d.Thumbnail(ctx, ali.ID, "", fid); err != nil || a.Mime != "image/png" {
			t.Errorf("thumbnail = %+v %v", a, err)
		}
		if err := repo.SetThumbnail(ctx, "fil_missing", hash); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("missing file: %v", err)
		}
	})

	t.Run("the library is a compare-and-swap on rev", func(t *testing.T) {
		l, err := d.GetLibrary(ctx, ali.ID, "", pid)
		must(t, err)
		if l.Rev != 0 || string(l.Doc) != string(diagram.EmptyLibrary) {
			t.Errorf("fresh = %d %s", l.Rev, l.Doc)
		}
		var wg sync.WaitGroup
		var mu sync.Mutex
		wins, conflicts := 0, 0
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := d.SaveLibrary(ctx, ali.ID, pid, 0, json.RawMessage(itLibrary))
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil:
					wins++
				case errors.Is(err, diagram.ErrLibraryConflict):
					conflicts++
				default:
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		if wins != 1 || conflicts != 7 {
			t.Errorf("first save: %d wins, %d conflicts", wins, conflicts)
		}
		wins, conflicts = 0, 0
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := d.SaveLibrary(ctx, ali.ID, pid, 1, json.RawMessage(itLibrary))
				mu.Lock()
				defer mu.Unlock()
				if err == nil {
					wins++
				} else if errors.Is(err, diagram.ErrLibraryConflict) {
					conflicts++
				} else {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		if wins != 1 || conflicts != 7 {
			t.Errorf("update from rev 1: %d wins, %d conflicts", wins, conflicts)
		}
		l, err = d.GetLibrary(ctx, ali.ID, "", pid)
		must(t, err)
		var doc map[string]any
		must(t, json.Unmarshal(l.Doc, &doc))
		if l.Rev != 2 || doc["future"] == nil {
			t.Errorf("library = %d %s (unknown fields must survive)", l.Rev, l.Doc)
		}
		_, err = d.SaveLibrary(ctx, ali.ID, pid, 1, json.RawMessage(itLibrary))
		var c *diagram.LibraryConflict
		if !errors.As(err, &c) || c.Current.Rev != 2 {
			t.Errorf("stale save: %v", err)
		}
	})

	t.Run("private stickies belong to one person and one file", func(t *testing.T) {
		stk := func(id string) json.RawMessage {
			return json.RawMessage(`{"nodes":[{"id":"` + id + `","type":"sticky","position":{"x":1,"y":2},"data":{"label":"mine"}}],"edges":[]}`)
		}
		put, err := d.PutPrivate(ctx, ali.ID, fid, stk("p1"))
		must(t, err)
		if put.UpdatedAt.IsZero() {
			t.Error("no updatedAt")
		}
		if _, err := d.PutPrivate(ctx, ali.ID, fid, stk("p2")); err != nil { // upsert
			t.Fatal(err)
		}
		got, err := d.GetPrivate(ctx, ali.ID, fid)
		must(t, err)
		if !strings.Contains(string(got.Data), `"p2"`) || strings.Contains(string(got.Data), `"p1"`) || !strings.Contains(string(got.Data), `"authorId"`) || !strings.Contains(string(got.Data), `"`+itoa(ali.ID)+`"`) {
			t.Errorf("private = %s", got.Data)
		}
		if got := rowCount(t, db, "dg_file_private WHERE file_id = '"+fid+"'"); got != 1 {
			t.Errorf("rows = %d", got)
		}
		// others see nothing
		_, err = d.AddMember(ctx, ali.ID, pid, "dgs_eve", diagram.RoleEditor)
		must(t, err)
		other, err := d.GetPrivate(ctx, eve.ID, fid)
		must(t, err)
		if string(other.Data) != string(diagram.EmptyPrivate) {
			t.Errorf("eve sees %s", other.Data)
		}
		// clearing removes the row
		_, err = d.PutPrivate(ctx, ali.ID, fid, json.RawMessage(`{"nodes":[],"edges":[]}`))
		must(t, err)
		if got := rowCount(t, db, "dg_file_private WHERE file_id = '"+fid+"'"); got != 0 {
			t.Errorf("rows after clear = %d", got)
		}
		// and a file's deletion takes the rest
		second, _, err := d.CreateFile(ctx, ali.ID, pid, application.NewDiagramFile{Name: "tmp", Data: dgDoc("t")})
		must(t, err)
		_, err = d.PutPrivate(ctx, ali.ID, second.ID, stk("z"))
		must(t, err)
		must(t, d.DeleteFile(ctx, ali.ID, second.ID))
		if got := rowCount(t, db, "dg_file_private WHERE file_id = '"+second.ID+"'"); got != 0 {
			t.Errorf("private rows outlived the file: %d", got)
		}
	})
}

func itoa(n int) string { return strconv.Itoa(n) }
