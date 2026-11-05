package postgres_test

// The diagram repository and the use cases on top of it, against a real
// Postgres. Skipped with -short or without Docker.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/iBlog/iblog-monolith-go/internal/application"
	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/postgres"
)

func dgDoc(nodes ...string) json.RawMessage {
	var ns []string
	for _, n := range nodes {
		ns = append(ns, fmt.Sprintf(`{"id":%q,"type":"service","position":{"x":1.5,"y":2}}`, n))
	}
	return json.RawMessage(`{"version":2,"activePageId":"pg","pages":[{"id":"pg","name":"P","nodes":[` + strings.Join(ns, ",") + `],"edges":[]}],"futureKey":{"x":1}}`)
}

func rowCount(t *testing.T, db *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	must(t, db.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&n))
	return n
}

func TestDiagramRepo(t *testing.T) {
	db := connect(t)
	ctx := context.Background()
	users := postgres.NewUserRepo(db)
	ali, err := users.Create(ctx, "dg_ali", "dg_ali@example.com")
	must(t, err)
	vali, err := users.Create(ctx, "dg_vali", "dg_vali@example.com")
	must(t, err)
	repo := postgres.NewDiagramRepo(db)
	d := application.NewDiagrams(repo, repo)
	d.Coalesce = 0 // these tests count every save
	d.KeepLast, d.KeepDays = 3, 0

	made, err := d.CreateProject(ctx, ali.ID, application.NewDiagramProject{Name: "Platform", Data: dgDoc("a", "b")})
	must(t, err)
	pid, fid := made.Project.ID, made.File.ID
	if made.Project.TreeRev != 1 || made.Project.FileCount != 1 || made.Project.OwnerID != ali.ID {
		t.Errorf("project = %+v", made.Project)
	}

	t.Run("document round trip keeps unknown fields", func(t *testing.T) {
		f, err := d.GetFile(ctx, ali.ID, fid)
		must(t, err)
		var doc map[string]any
		must(t, json.Unmarshal(f.Data, &doc))
		if doc["futureKey"] == nil || doc["id"] != fid || doc["name"] != "Platform" {
			t.Errorf("document = %v", doc)
		}
		if f.Kind != "sdd" || f.PageCount != 1 || f.UpdatedBy != ali.ID || f.CreatedAt.IsZero() {
			t.Errorf("file = %+v", f.File)
		}
	})

	t.Run("client ids: the database refuses a taken id", func(t *testing.T) {
		if err := repo.CreateProject(ctx, diagram.Project{ID: pid, OwnerID: ali.ID, Name: "dup"}); !errors.Is(err, domain.ErrConflict) {
			t.Errorf("duplicate project id: %v", err)
		}
		if err := repo.CreateFolder(ctx, diagram.Folder{ID: "fld_dup00001", ProjectID: pid, Name: "x"}); err != nil {
			t.Fatal(err)
		}
		if err := repo.CreateFolder(ctx, diagram.Folder{ID: "fld_dup00001", ProjectID: pid, Name: "x"}); !errors.Is(err, domain.ErrConflict) {
			t.Errorf("duplicate folder id: %v", err)
		}
		if _, _, err := d.CreateFile(ctx, ali.ID, pid, application.NewDiagramFile{ID: "fil_taken001", Name: "x"}); err != nil {
			t.Fatal(err)
		}
		other, _ := d.CreateProject(ctx, ali.ID, application.NewDiagramProject{Name: "Other"})
		if _, _, err := d.CreateFile(ctx, ali.ID, other.Project.ID, application.NewDiagramFile{ID: "fil_taken001", Name: "x"}); !errors.Is(err, domain.ErrConflict) {
			t.Errorf("file id from another project: %v", err)
		}
		must(t, repo.DeleteProject(ctx, other.Project.ID))
	})

	t.Run("tree order and next position", func(t *testing.T) {
		pos, err := repo.NextPosition(ctx, "prj_nothing", nil)
		must(t, err)
		if pos != 0 {
			t.Errorf("empty parent: %v, want 0", pos)
		}
		folders, files, err := repo.Tree(ctx, pid)
		must(t, err)
		next, err := repo.NextPosition(ctx, pid, nil)
		must(t, err)
		var top float64
		for _, f := range folders {
			top = max(top, f.Position)
		}
		for _, f := range files {
			top = max(top, f.Position)
		}
		if next != top+1 {
			t.Errorf("next = %v, want %v", next, top+1)
		}
	})

	t.Run("duplicate folder inserts children whatever the order", func(t *testing.T) {
		a, _, err := d.CreateFolder(ctx, ali.ID, pid, application.NewFolder{Name: "a", Position: ptr(5.0)})
		must(t, err)
		b, _, err := d.CreateFolder(ctx, ali.ID, pid, application.NewFolder{Name: "b", ParentID: &a.ID, Position: ptr(9.0)})
		must(t, err)
		c, _, err := d.CreateFolder(ctx, ali.ID, pid, application.NewFolder{Name: "c", ParentID: &b.ID, Position: ptr(1.0)}) // sorts before its parent
		must(t, err)
		_, _, err = d.CreateFile(ctx, ali.ID, pid, application.NewDiagramFile{Name: "deep", FolderID: &c.ID, Data: dgDoc("n")})
		must(t, err)
		cp, err := d.DuplicateFolder(ctx, ali.ID, a.ID)
		must(t, err)
		if len(cp.Folders) != 3 || len(cp.Files) != 1 || cp.Folder.Name != "a (copy)" {
			t.Errorf("copy = %d folders, %d files, %q", len(cp.Folders), len(cp.Files), cp.Folder.Name)
		}
		folders, files, err := d.DeleteFolder(ctx, ali.ID, cp.Folder.ID)
		must(t, err)
		if folders != 3 || files != 1 {
			t.Errorf("deleted %d folders, %d files, want 3 and 1", folders, files)
		}
		// the original is untouched
		if _, err := d.GetFile(ctx, ali.ID, fid); err != nil {
			t.Error(err)
		}
		_, _, err = d.DeleteFolder(ctx, ali.ID, a.ID)
		must(t, err)
	})

	t.Run("saves are compare-and-swap; history is pruned", func(t *testing.T) {
		f, _, err := d.CreateFile(ctx, ali.ID, pid, application.NewDiagramFile{Name: "race"})
		must(t, err)
		var wg sync.WaitGroup
		var mu sync.Mutex
		wins := 0
		for i := range 6 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := d.SaveFile(ctx, ali.ID, f.ID, ptr(fmt.Sprintf("w%d", i)), nil, `"1"`)
				mu.Lock()
				defer mu.Unlock()
				if err == nil {
					wins++
				} else if !errors.Is(err, diagram.ErrPrecondition) {
					t.Errorf("save: %v", err)
				}
			}()
		}
		wg.Wait()
		if wins != 1 {
			t.Errorf("%d concurrent saves won, want 1", wins)
		}
		vs, err := repo.Versions(ctx, f.ID)
		must(t, err)
		if len(vs) != 2 || vs[0].Version != 2 {
			t.Errorf("history = %+v, want v2 and v1 (a lost save leaves no snapshot)", vs)
		}

		for v := 2; v <= 6; v++ {
			_, err := d.SaveFile(ctx, ali.ID, f.ID, nil, dgDoc(fmt.Sprint("n", v)), fmt.Sprintf(`"%d"`, v))
			must(t, err)
		}
		vs, err = repo.Versions(ctx, f.ID)
		must(t, err)
		if len(vs) != 3 || vs[0].Version != 7 || vs[2].Version != 5 {
			t.Errorf("after pruning (keep 3) = %+v", vs)
		}
	})

	t.Run("named versions and restore", func(t *testing.T) {
		f, _, err := d.CreateFile(ctx, ali.ID, pid, application.NewDiagramFile{Name: "named", Data: dgDoc("first")})
		must(t, err)
		v, err := d.SaveNamedVersion(ctx, ali.ID, f.ID, "Release 1")
		must(t, err)
		if v.Version != 1 || !v.Named || *v.Label != "Release 1" || v.CreatedBy != ali.ID {
			t.Errorf("named = %+v", v)
		}
		if _, err := d.SaveNamedVersion(ctx, ali.ID, f.ID, "Release 1.0"); err != nil { // relabels
			t.Fatal(err)
		}
		for ver := 1; ver <= 5; ver++ {
			_, err := d.SaveFile(ctx, ali.ID, f.ID, nil, dgDoc(fmt.Sprint("n", ver)), fmt.Sprintf(`"%d"`, ver))
			must(t, err)
		}
		old, err := d.Version(ctx, ali.ID, f.ID, 1)
		if err != nil || !old.Named || *old.Label != "Release 1.0" {
			t.Fatalf("a named version must survive pruning: %+v, %v", old, err)
		}
		restored, err := d.RestoreVersion(ctx, ali.ID, f.ID, 1)
		must(t, err)
		if restored.Version != 7 || !strings.Contains(string(restored.Data), `"first"`) {
			t.Errorf("restored = v%d", restored.Version)
		}
		head, err := d.Version(ctx, ali.ID, f.ID, 7)
		must(t, err)
		if head.Label == nil || *head.Label != "Restored from v1" {
			t.Errorf("restore label = %v", head.Label)
		}
		if _, err := d.Version(ctx, ali.ID, f.ID, 99); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("unknown version: %v", err)
		}
	})

	t.Run("project list: access, search, sort, paging", func(t *testing.T) {
		for _, n := range []string{"Alpha", "beta", "delta_x", "Epsilon 100%"} {
			_, err := d.CreateProject(ctx, ali.ID, application.NewDiagramProject{Name: n})
			must(t, err)
		}
		bobs, err := d.CreateProject(ctx, vali.ID, application.NewDiagramProject{Name: "Bob only"})
		must(t, err)
		shared, err := d.CreateProject(ctx, vali.ID, application.NewDiagramProject{Name: "Shared"})
		must(t, err)
		_, err = db.Exec(ctx, `INSERT INTO dg_project_members (project_id, user_id, role) VALUES ($1, $2, 'viewer')`, shared.Project.ID, ali.ID)
		must(t, err)

		list := func(q diagram.ProjectQuery) ([]diagram.ProjectSummary, int) {
			if q.Page == 0 {
				q.Page, q.PageSize = 1, 50
			}
			items, total, err := repo.ListProjects(ctx, ali.ID, q)
			must(t, err)
			return items, total
		}
		all, total := list(diagram.ProjectQuery{})
		if total != 6 || len(all) != 6 { // Platform + 4 + Shared
			t.Errorf("total = %d, items = %d, want 6", total, len(all))
		}
		for _, p := range all {
			if p.ID == bobs.Project.ID {
				t.Error("listed a project of a stranger")
			}
			if p.ID == shared.Project.ID && p.Role != diagram.RoleViewer || p.OwnerID == ali.ID && p.Role != diagram.RoleOwner {
				t.Errorf("role of %s = %s", p.Name, p.Role)
			}
			if p.FileCount < 1 {
				t.Errorf("%s: file count %d", p.Name, p.FileCount)
			}
		}
		for q, want := range map[string]int{"ALPHA": 1, "100%": 1, "delt_": 0, "delta_": 1, "%": 1, "a": 5} {
			if _, n := list(diagram.ProjectQuery{Search: q}); n != want {
				t.Errorf("search %q = %d, want %d", q, n, want)
			}
		}
		asc, _ := list(diagram.ProjectQuery{SortByName: true, Asc: true})
		if asc[0].Name != "Alpha" || asc[1].Name != "beta" {
			t.Errorf("by name: %s, %s", asc[0].Name, asc[1].Name)
		}
		desc, _ := list(diagram.ProjectQuery{SortByName: true})
		if desc[0].Name != "Shared" {
			t.Errorf("by name desc starts with %s", desc[0].Name)
		}
		p2, total := list(diagram.ProjectQuery{Page: 2, PageSize: 4})
		if len(p2) != 2 || total != 6 {
			t.Errorf("page 2 = %d of %d", len(p2), total)
		}
		access, err := repo.MemberRole(ctx, shared.Project.ID, ali.ID)
		must(t, err)
		if access != diagram.RoleViewer {
			t.Errorf("member role = %q", access)
		}
		if role, _ := repo.MemberRole(ctx, shared.Project.ID, 999999); role != "" {
			t.Errorf("stranger role = %q", role)
		}
	})

	t.Run("per-file override", func(t *testing.T) {
		shared, err := d.CreateProject(ctx, vali.ID, application.NewDiagramProject{Name: "Overrides"})
		must(t, err)
		_, err = db.Exec(ctx, `INSERT INTO dg_project_members (project_id, user_id, role) VALUES ($1, $2, 'viewer')`, shared.Project.ID, ali.ID)
		must(t, err)
		if _, err := d.SaveFile(ctx, ali.ID, shared.File.ID, ptr("x"), nil, `"1"`); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("viewer save: %v", err)
		}
		_, err = db.Exec(ctx, `INSERT INTO dg_file_members (file_id, user_id, role) VALUES ($1, $2, 'editor')`, shared.File.ID, ali.ID)
		must(t, err)
		if _, err := d.SaveFile(ctx, ali.ID, shared.File.ID, ptr("x"), nil, `"1"`); err != nil {
			t.Errorf("override save: %v", err)
		}
	})

	t.Run("deletes cascade", func(t *testing.T) {
		tmp, err := d.CreateProject(ctx, ali.ID, application.NewDiagramProject{Name: "Temp"})
		must(t, err)
		dir, _, _ := d.CreateFolder(ctx, ali.ID, tmp.Project.ID, application.NewFolder{Name: "d"})
		_, _, err = d.CreateFile(ctx, ali.ID, tmp.Project.ID, application.NewDiagramFile{Name: "in", FolderID: &dir.ID})
		must(t, err)
		_, err = db.Exec(ctx, `INSERT INTO dg_file_members (file_id, user_id, role) VALUES ($1, $2, 'viewer')`, tmp.File.ID, vali.ID)
		must(t, err)
		files, versions := rowCount(t, db, "dg_files"), rowCount(t, db, "dg_file_versions")
		must(t, d.DeleteProject(ctx, ali.ID, tmp.Project.ID))
		if got := rowCount(t, db, "dg_files"); got != files-2 {
			t.Errorf("files = %d, want %d", got, files-2)
		}
		if got := rowCount(t, db, "dg_file_versions"); got != versions-2 {
			t.Errorf("versions = %d, want %d", got, versions-2)
		}
		var members int
		must(t, db.QueryRow(ctx, `SELECT count(*) FROM dg_file_members WHERE file_id = $1`, tmp.File.ID).Scan(&members))
		if members != 0 {
			t.Errorf("file members left: %d", members)
		}
		if err := repo.DeleteProject(ctx, tmp.Project.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("second delete: %v", err)
		}
	})

	t.Run("deleting a user removes their projects and unsets authorship", func(t *testing.T) {
		gone, err := users.Create(ctx, "dg_gone", "dg_gone@example.com")
		must(t, err)
		mine, err := d.CreateProject(ctx, gone.ID, application.NewDiagramProject{Name: "Mine"})
		must(t, err)
		shared, err := d.CreateProject(ctx, ali.ID, application.NewDiagramProject{Name: "Edited by gone"})
		must(t, err)
		_, err = db.Exec(ctx, `INSERT INTO dg_project_members (project_id, user_id, role) VALUES ($1, $2, 'editor')`, shared.Project.ID, gone.ID)
		must(t, err)
		_, err = d.SaveFile(ctx, gone.ID, shared.File.ID, ptr("by gone"), nil, `"1"`)
		must(t, err)
		_, err = db.Exec(ctx, `DELETE FROM users WHERE id = $1`, gone.ID)
		must(t, err)
		if _, err := repo.Project(ctx, mine.Project.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("project of a deleted user: %v", err)
		}
		f, err := repo.File(ctx, shared.File.ID)
		must(t, err)
		if f.UpdatedBy != 0 {
			t.Errorf("updatedBy = %d, want 0 (unset)", f.UpdatedBy)
		}
		vs, err := repo.Versions(ctx, shared.File.ID)
		must(t, err)
		if vs[0].CreatedBy != 0 {
			t.Errorf("createdBy = %d, want 0", vs[0].CreatedBy)
		}
	})
}

func TestDiagramPrefsRepo(t *testing.T) {
	db := connect(t)
	ctx := context.Background()
	ali, err := postgres.NewUserRepo(db).Create(ctx, "dg_prefs", "dg_prefs@example.com")
	must(t, err)
	repo := postgres.NewDiagramRepo(db)
	d := application.NewDiagrams(repo, repo)
	d.Coalesce = 0 // these tests count every save

	if got, err := d.GetPrefs(ctx, ali.ID); err != nil || string(got) != `{}` {
		t.Fatalf("fresh = %s, %v", got, err)
	}
	// concurrent patches of different keys must all land
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := d.PatchPrefs(ctx, ali.ID, json.RawMessage(fmt.Sprintf(`{"k%d":%d}`, i, i))); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var got map[string]int
	raw, err := d.GetPrefs(ctx, ali.ID)
	must(t, err)
	must(t, json.Unmarshal(raw, &got))
	if len(got) != 8 {
		t.Errorf("prefs = %s, want 8 keys", raw)
	}
	if _, err := d.PatchPrefs(ctx, ali.ID, json.RawMessage(`{"k0":null,"k1":null}`)); err != nil {
		t.Fatal(err)
	}
	raw, _ = d.GetPrefs(ctx, ali.ID)
	if strings.Contains(string(raw), `"k0"`) || !strings.Contains(string(raw), `"k2"`) {
		t.Errorf("after delete = %s", raw)
	}
	big := fmt.Sprintf(`{"big":%q}`, strings.Repeat("x", 70*1024))
	if _, err := d.PatchPrefs(ctx, ali.ID, json.RawMessage(big)); !errors.Is(err, diagram.ErrTooLarge) {
		t.Errorf("oversize: %v", err)
	}
}

func ptr[T any](v T) *T { return &v }
