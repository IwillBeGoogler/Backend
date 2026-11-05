package application

// Diagram use-case tests on the in-memory repository: permissions, tree
// revisions, optimistic saves, history and duplication.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram/diagramtest"
)

const (
	alice = 1
	bob   = 2
	eve   = 3
)

var dctx = context.Background()

func newDiagrams(t *testing.T) (*Diagrams, *diagramtest.Memory) {
	t.Helper()
	mem := diagramtest.NewMemory()
	d := NewDiagrams(mem, mem)
	d.Coalesce = 0 // history tests count every save; coalescing has its own tests
	var mu sync.Mutex
	at := time.Unix(1_700_000_000, 0)
	d.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		at = at.Add(time.Second)
		return at
	}
	return d, mem
}

func must1[T any](t *testing.T) func(T, error) T {
	return func(v T, err error) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

func project(t *testing.T, d *Diagrams, owner int, name string) DiagramProject {
	t.Helper()
	return must1[DiagramProject](t)(d.CreateProject(dctx, owner, NewDiagramProject{Name: name}))
}

func isCode(err error, kind error, code string) bool {
	var e *diagram.Error
	return errors.Is(err, kind) && errors.As(err, &e) && e.Code == code
}

func docOf(nodes ...string) json.RawMessage {
	var ns []string
	for _, n := range nodes {
		ns = append(ns, fmt.Sprintf(`{"id":%q,"type":"service"}`, n))
	}
	return json.RawMessage(`{"version":2,"activePageId":"pg","pages":[{"id":"pg","name":"P","nodes":[` + strings.Join(ns, ",") + `],"edges":[]}]}`)
}

func ifMatch(v int) string { return fmt.Sprintf(`"%d"`, v) }

func nodeCount(t *testing.T, data json.RawMessage) int {
	t.Helper()
	var doc struct {
		Pages []struct {
			Nodes []json.RawMessage `json:"nodes"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	return len(doc.Pages[0].Nodes)
}

func TestDiagramCreateProject(t *testing.T) {
	d, _ := newDiagrams(t)
	res := project(t, d, alice, "  Platform ")
	p, f := res.Project, res.File
	if !res.Created || p.Name != "Platform" || p.Role != diagram.RoleOwner || p.OwnerID != alice || p.FileCount != 1 || p.FolderCount != 0 {
		t.Errorf("project = %+v", p)
	}
	if !strings.HasPrefix(p.ID, "prj_") || !strings.HasPrefix(f.ID, "fil_") || f.Name != "Platform" || f.Version != 1 || f.ProjectID != p.ID {
		t.Errorf("first file = %+v", f.File)
	}
	if p.TreeRev != 1 {
		t.Errorf("tree rev = %d, want 1 (the first file)", p.TreeRev)
	}

	named := must1[DiagramProject](t)(d.CreateProject(dctx, alice, NewDiagramProject{Name: "P", File: &NewDiagramFile{Name: "Overview"}}))
	if named.File.Name != "Overview" || named.Project.Name != "P" {
		t.Errorf("named first file: %q in %q", named.File.Name, named.Project.Name)
	}
	// the name may come from the document
	fromData := must1[DiagramProject](t)(d.CreateProject(dctx, alice, NewDiagramProject{Data: json.RawMessage(`{"version":2,"name":"From doc","activePageId":"a","pages":[{"id":"a","name":"A","nodes":[],"edges":[]}]}`)}))
	if fromData.Project.Name != "From doc" {
		t.Errorf("name from data = %q", fromData.Project.Name)
	}

	for name, in := range map[string]NewDiagramProject{
		"no name":      {},
		"blank name":   {Name: "   "},
		"too long":     {Name: strings.Repeat("x", 201)},
		"control char": {Name: "a\u0007b"},
		"bad id":       {ID: "bad id!", Name: "x"},
		"bad data":     {Name: "x", Data: json.RawMessage(`{"version":1}`)},
	} {
		var v *domain.ValidationError
		if _, err := d.CreateProject(dctx, alice, in); !errors.As(err, &v) {
			t.Errorf("%s: err = %v, want a validation error", name, err)
		}
	}
	if page := must1[DiagramProjectPage](t)(d.ListProjects(dctx, alice, diagram.ProjectQuery{Page: 1, PageSize: 50})); page.Total != 3 {
		t.Errorf("a rejected create must store nothing: total = %d, want 3", page.Total)
	}
}

func TestDiagramClientIDsAreIdempotent(t *testing.T) {
	d, _ := newDiagrams(t)
	in := NewDiagramProject{ID: "prj_offline01", Name: "Offline", File: &NewDiagramFile{ID: "fil_offline01"}}
	first := must1[DiagramProject](t)(d.CreateProject(dctx, alice, in))
	if !first.Created || first.Project.ID != "prj_offline01" || first.File.ID != "fil_offline01" {
		t.Fatalf("first = %+v", first)
	}
	again := must1[DiagramProject](t)(d.CreateProject(dctx, alice, in))
	if again.Created || again.File.ID != "fil_offline01" {
		t.Errorf("replay = created %v, file %s", again.Created, again.File.ID)
	}
	if tree := must1[DiagramTree](t)(d.Tree(dctx, alice, "prj_offline01")); len(tree.Files) != 1 {
		t.Errorf("replay made %d files, want 1", len(tree.Files))
	}
	if _, err := d.CreateProject(dctx, bob, in); !isCode(err, domain.ErrConflict, "ID_TAKEN") {
		t.Errorf("another user taking the id: %v", err)
	}

	f1, created, err := d.CreateFolder(dctx, alice, "prj_offline01", NewFolder{ID: "fld_client001", Name: "x"})
	if err != nil || !created {
		t.Fatalf("folder: %v, created %v", err, created)
	}
	if f2, created, err := d.CreateFolder(dctx, alice, "prj_offline01", NewFolder{ID: "fld_client001", Name: "x"}); err != nil || created || f2.ID != f1.ID {
		t.Errorf("folder replay: %v, created %v", err, created)
	}
	other := project(t, d, alice, "Other")
	if _, _, err := d.CreateFolder(dctx, alice, other.Project.ID, NewFolder{ID: "fld_client001", Name: "x"}); !isCode(err, domain.ErrConflict, "ID_TAKEN") {
		t.Errorf("folder id in another project: %v", err)
	}
	if _, created, err := d.CreateFile(dctx, alice, "prj_offline01", NewDiagramFile{ID: "fil_client001", Name: "f"}); err != nil || !created {
		t.Fatalf("file: %v", err)
	}
	if _, created, err := d.CreateFile(dctx, alice, "prj_offline01", NewDiagramFile{ID: "fil_client001", Name: "f"}); err != nil || created {
		t.Errorf("file replay: %v, created %v", err, created)
	}
	if _, _, err := d.CreateFile(dctx, alice, other.Project.ID, NewDiagramFile{ID: "fil_client001", Name: "f"}); !isCode(err, domain.ErrConflict, "ID_TAKEN") {
		t.Errorf("file id in another project: %v", err)
	}
}

func TestDiagramAccess(t *testing.T) {
	d, mem := newDiagrams(t)
	res := project(t, d, alice, "Private")
	pid, fid := res.Project.ID, res.File.ID

	// a stranger learns nothing: not found, never forbidden
	if _, err := d.GetProject(dctx, eve, pid); !isCode(err, domain.ErrNotFound, "NOT_FOUND") {
		t.Errorf("stranger GetProject: %v", err)
	}
	if _, err := d.GetFile(dctx, eve, fid); !isCode(err, domain.ErrNotFound, "NOT_FOUND") {
		t.Errorf("stranger GetFile: %v", err)
	}
	if _, err := d.Tree(dctx, eve, pid); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("stranger Tree: %v", err)
	}
	if _, err := d.GetFile(dctx, alice, "fil_missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing file: %v", err)
	}

	mem.AddMember(pid, bob, diagram.RoleViewer)
	if f, err := d.GetFile(dctx, bob, fid); err != nil || f.Role != diagram.RoleViewer {
		t.Errorf("viewer read: %v, role %s", err, f.Role)
	}
	forbidden := func(what string, err error) {
		t.Helper()
		if !isCode(err, domain.ErrForbidden, "FORBIDDEN") {
			t.Errorf("viewer %s: %v, want forbidden", what, err)
		}
	}
	_, _, err := d.CreateFolder(dctx, bob, pid, NewFolder{Name: "x"})
	forbidden("create folder", err)
	_, _, err = d.CreateFile(dctx, bob, pid, NewDiagramFile{Name: "x"})
	forbidden("create file", err)
	_, err = d.SaveFile(dctx, bob, fid, ptr("x"), nil, ifMatch(1))
	forbidden("save", err)
	_, _, err = d.UpdateFileMeta(dctx, bob, fid, FilePatch{Name: ptr("x")})
	forbidden("patch file", err)
	forbidden("delete file", d.DeleteFile(dctx, bob, fid))
	_, err = d.RenameProject(dctx, bob, pid, "x")
	forbidden("rename project", err)
	forbidden("delete project", d.DeleteProject(dctx, bob, pid))
	_, err = d.DuplicateFile(dctx, bob, fid, diagram.Opt[*string]{})
	forbidden("duplicate", err)
	_, err = d.SaveNamedVersion(dctx, bob, fid, "v")
	forbidden("name a version", err)
	_, err = d.RestoreVersion(dctx, bob, fid, 1)
	forbidden("restore", err)

	// an editor writes but cannot delete the project
	mem.AddMember(pid, bob, diagram.RoleEditor)
	if _, err := d.SaveFile(dctx, bob, fid, ptr("by bob"), nil, ifMatch(1)); err != nil {
		t.Errorf("editor save: %v", err)
	}
	forbidden("delete project", d.DeleteProject(dctx, bob, pid))
}

func TestDiagramPerFileOverride(t *testing.T) {
	d, mem := newDiagrams(t)
	res := project(t, d, alice, "P")
	pid, first := res.Project.ID, res.File.ID
	second, _, _ := d.CreateFile(dctx, alice, pid, NewDiagramFile{Name: "second"})

	mem.AddMember(pid, bob, diagram.RoleViewer)
	save := func(id string) error {
		_, err := d.SaveFile(dctx, bob, id, ptr("by bob"), nil, ifMatch(1))
		return err
	}
	if err := save(first); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("viewer save: %v", err)
	}
	mem.SetOverride(first, bob, diagram.RoleEditor) // a viewer may edit one file
	if err := save(first); err != nil {
		t.Errorf("override save: %v", err)
	}
	if err := save(second.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("override is for one file only: %v", err)
	}
	if f, _ := d.GetFile(dctx, bob, first); f.Role != diagram.RoleEditor {
		t.Errorf("role = %s, want editor", f.Role)
	}

	mem.AddMember(pid, bob, diagram.RoleEditor)
	mem.SetOverride(second.ID, bob, diagram.RoleViewer) // an editor may only view one
	if err := save(second.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("downgraded editor save: %v", err)
	}
	if _, err := d.GetFile(dctx, bob, second.ID); err != nil {
		t.Errorf("downgraded editor read: %v", err)
	}
	// the override cannot take anything from an owner
	mem.SetOverride(first, alice, diagram.RoleViewer)
	if _, err := d.SaveFile(dctx, alice, first, ptr("by alice"), nil, ifMatch(2)); err != nil {
		t.Errorf("owner with an override: %v", err)
	}
}

func ptr[T any](v T) *T { return &v }

func TestDiagramTreeRevision(t *testing.T) {
	d, _ := newDiagrams(t)
	res := project(t, d, alice, "T")
	pid := res.Project.ID
	rev := func() int { return must1[DiagramTree](t)(d.Tree(dctx, alice, pid)).Project.TreeRev }
	want := rev()
	bump := func(what string) {
		t.Helper()
		want++
		if got := rev(); got != want {
			t.Fatalf("after %s: tree rev = %d, want %d", what, got, want)
		}
	}

	dir, _, _ := d.CreateFolder(dctx, alice, pid, NewFolder{Name: "docs"})
	bump("create folder")
	f, _, _ := d.CreateFile(dctx, alice, pid, NewDiagramFile{Name: "f", FolderID: &dir.ID})
	bump("create file")
	if _, _, err := d.UpdateFileMeta(dctx, alice, f.ID, FilePatch{Name: ptr("g")}); err != nil {
		t.Fatal(err)
	}
	bump("rename file")
	if _, _, err := d.UpdateFileMeta(dctx, alice, f.ID, FilePatch{Name: ptr("g")}); err != nil {
		t.Fatal(err)
	}
	if got := rev(); got != want {
		t.Errorf("a no-op patch bumped the revision: %d, want %d", got, want)
	}
	if _, err := d.UpdateFolder(dctx, alice, dir.ID, FolderPatch{Name: ptr("docs2")}); err != nil {
		t.Fatal(err)
	}
	bump("rename folder")
	if _, err := d.RenameProject(dctx, alice, pid, "T2"); err != nil {
		t.Fatal(err)
	}
	bump("rename project")
	// saving content does not change the tree; saving under a new name does
	if _, err := d.SaveFile(dctx, alice, f.ID, nil, docOf("a"), ifMatch(1)); err != nil {
		t.Fatal(err)
	}
	if got := rev(); got != want {
		t.Errorf("a content save bumped the revision: %d, want %d", got, want)
	}
	if _, err := d.SaveFile(dctx, alice, f.ID, ptr("renamed by save"), nil, ifMatch(2)); err != nil {
		t.Fatal(err)
	}
	bump("rename by save")
	if _, err := d.DuplicateFile(dctx, alice, f.ID, diagram.Opt[*string]{}); err != nil {
		t.Fatal(err)
	}
	bump("duplicate file")
	if _, err := d.DuplicateFolder(dctx, alice, dir.ID); err != nil {
		t.Fatal(err)
	}
	bump("duplicate folder")
	if err := d.DeleteFile(dctx, alice, f.ID); err != nil {
		t.Fatal(err)
	}
	bump("delete file")
	if _, _, err := d.DeleteFolder(dctx, alice, dir.ID); err != nil {
		t.Fatal(err)
	}
	bump("delete folder")
}

func TestDiagramFolders(t *testing.T) {
	d, _ := newDiagrams(t)
	pid := project(t, d, alice, "F").Project.ID
	mk := func(name string, parent *string) diagram.Folder {
		t.Helper()
		f, _, err := d.CreateFolder(dctx, alice, pid, NewFolder{Name: name, ParentID: parent})
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	a := mk("a", nil)
	b := mk("b", &a.ID)
	c := mk("c", &b.ID)
	if c.ParentID == nil || *c.ParentID != b.ID {
		t.Errorf("c.parent = %v", c.ParentID)
	}

	for _, target := range []string{a.ID, b.ID, c.ID} { // into itself or a descendant
		_, err := d.UpdateFolder(dctx, alice, a.ID, FolderPatch{ParentID: diagram.Opt[*string]{Set: true, Val: &target}})
		if !isCode(err, domain.ErrConflict, "CYCLE") {
			t.Errorf("move a into %s: %v, want CYCLE", target, err)
		}
	}
	root := diagram.Opt[*string]{Set: true}
	if _, err := d.UpdateFolder(dctx, alice, c.ID, FolderPatch{ParentID: root}); err != nil {
		t.Errorf("move c to the root: %v", err)
	}
	if _, err := d.UpdateFolder(dctx, alice, a.ID, FolderPatch{ParentID: diagram.Opt[*string]{Set: true, Val: &c.ID}}); err != nil {
		t.Errorf("move a into c: %v", err)
	}
	ghost := "fld_nope0000"
	if _, _, err := d.CreateFolder(dctx, alice, pid, NewFolder{Name: "x", ParentID: &ghost}); !isCode(err, domain.ErrNotFound, "") && !strings.Contains(fmt.Sprint(err), "Parent folder") {
		t.Errorf("unknown parent: %v", err)
	}
	foreign := project(t, d, alice, "Other").Project.ID
	fo, _, _ := d.CreateFolder(dctx, alice, foreign, NewFolder{Name: "foreign"})
	var v *domain.ValidationError
	if _, _, err := d.CreateFolder(dctx, alice, pid, NewFolder{Name: "x", ParentID: &fo.ID}); !errors.As(err, &v) {
		t.Errorf("parent from another project: %v", err)
	}
	if f, _, _ := d.CreateFolder(dctx, alice, pid, NewFolder{Name: "  spaced  "}); f.Name != "spaced" {
		t.Errorf("name = %q", f.Name)
	}
	if _, _, err := d.CreateFolder(dctx, alice, pid, NewFolder{Name: ""}); err == nil {
		t.Error("empty folder name accepted")
	}
	if _, _, err := d.CreateFolder(dctx, alice, pid, NewFolder{Name: "a\nb"}); err == nil {
		t.Error("control characters accepted")
	}
}

func TestDiagramDeleteFolder(t *testing.T) {
	d, _ := newDiagrams(t)
	res := project(t, d, alice, "D")
	pid := res.Project.ID
	a, _, _ := d.CreateFolder(dctx, alice, pid, NewFolder{Name: "a"})
	b, _, _ := d.CreateFolder(dctx, alice, pid, NewFolder{Name: "b", ParentID: &a.ID})
	f1, _, _ := d.CreateFile(dctx, alice, pid, NewDiagramFile{Name: "one", FolderID: &a.ID})
	d.CreateFile(dctx, alice, pid, NewDiagramFile{Name: "two", FolderID: &b.ID})

	folders, files, err := d.DeleteFolder(dctx, alice, a.ID)
	if err != nil || folders != 2 || files != 2 {
		t.Fatalf("deleted %d folders, %d files, %v; want 2, 2", folders, files, err)
	}
	tree := must1[DiagramTree](t)(d.Tree(dctx, alice, pid))
	if len(tree.Folders) != 0 || len(tree.Files) != 1 || tree.Files[0].ID != res.File.ID {
		t.Errorf("tree after delete: %d folders, %d files", len(tree.Folders), len(tree.Files))
	}
	if _, err := d.GetFile(dctx, alice, f1.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("file inside survived: %v", err)
	}
	if _, _, err := d.DeleteFolder(dctx, alice, a.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("second delete: %v", err)
	}
}

func TestDiagramDuplicateFolder(t *testing.T) {
	d, _ := newDiagrams(t)
	pid := project(t, d, alice, "D").Project.ID
	a, _, _ := d.CreateFolder(dctx, alice, pid, NewFolder{Name: "a"})
	b, _, _ := d.CreateFolder(dctx, alice, pid, NewFolder{Name: "b", ParentID: &a.ID})
	f, _, _ := d.CreateFile(dctx, alice, pid, NewDiagramFile{Name: "doc", FolderID: &b.ID, Data: docOf("n1")})

	c := must1[FolderCopy](t)(d.DuplicateFolder(dctx, alice, a.ID))
	if c.Folder.Name != "a (copy)" || c.Folder.ParentID != nil || len(c.Folders) != 2 || len(c.Files) != 1 {
		t.Fatalf("copy = %+v", c)
	}
	var copyB diagram.Folder
	for _, f := range c.Folders {
		if f.Name == "b" {
			copyB = f
		}
	}
	if copyB.ParentID == nil || *copyB.ParentID != c.Folder.ID || copyB.ID == b.ID {
		t.Errorf("copied subfolder = %+v", copyB)
	}
	cf := c.Files[0]
	if cf.ID == f.ID || cf.FolderID == nil || *cf.FolderID != copyB.ID || cf.Version != 1 {
		t.Errorf("copied file = %+v", cf)
	}
	got := must1[DiagramFile](t)(d.GetFile(dctx, alice, cf.ID))
	if nodeCount(t, got.Data) != 1 {
		t.Error("copied document lost its node")
	}
	var doc struct{ ID string }
	json.Unmarshal(got.Data, &doc)
	if doc.ID != cf.ID {
		t.Errorf("copied document id = %q, want %q", doc.ID, cf.ID)
	}
	if tree := must1[DiagramTree](t)(d.Tree(dctx, alice, pid)); len(tree.Folders) != 4 {
		t.Errorf("folders = %d, want 4", len(tree.Folders))
	}
}

func TestDiagramFileOrdering(t *testing.T) {
	d, _ := newDiagrams(t)
	pid := project(t, d, alice, "O").Project.ID
	f1, _, _ := d.CreateFile(dctx, alice, pid, NewDiagramFile{Name: "f1"})
	d1, _, _ := d.CreateFolder(dctx, alice, pid, NewFolder{Name: "d1"})
	f2, _, _ := d.CreateFile(dctx, alice, pid, NewDiagramFile{Name: "f2"})
	if !(f1.Position < d1.Position && d1.Position < f2.Position) {
		t.Errorf("new items go last, files and folders share one sequence: %v %v %v", f1.Position, d1.Position, f2.Position)
	}
	mid := (f1.Position + d1.Position) / 2
	moved, _, err := d.UpdateFileMeta(dctx, alice, f2.ID, FilePatch{Position: &mid})
	if err != nil || moved.Position != mid {
		t.Fatalf("explicit position: %v, %v", moved.Position, err)
	}
	// moving to a folder appends at its end
	in, _, _ := d.CreateFile(dctx, alice, pid, NewDiagramFile{Name: "in", FolderID: &d1.ID})
	m, _, err := d.UpdateFileMeta(dctx, alice, f1.ID, FilePatch{FolderID: diagram.Opt[*string]{Set: true, Val: &d1.ID}})
	if err != nil || m.FolderID == nil || m.Position <= in.Position {
		t.Errorf("moved = %+v, %v", m, err)
	}
	ghost := "fld_nope0000"
	if _, _, err := d.UpdateFileMeta(dctx, alice, f1.ID, FilePatch{FolderID: diagram.Opt[*string]{Set: true, Val: &ghost}}); err == nil {
		t.Error("moved into an unknown folder")
	}
	// the document version is untouched by tree operations
	if got := must1[DiagramFile](t)(d.GetFile(dctx, alice, f1.ID)); got.Version != 1 {
		t.Errorf("version = %d, want 1", got.Version)
	}
}

func TestDiagramSaveIsOptimistic(t *testing.T) {
	d, _ := newDiagrams(t)
	f := project(t, d, alice, "S").File

	_, err := d.SaveFile(dctx, alice, f.ID, ptr("x"), nil, "")
	var e *diagram.Error
	if !isCode(err, diagram.ErrPrecondition, "IF_MATCH_REQUIRED") || !errors.As(err, &e) || e.Current != 1 {
		t.Errorf("no If-Match: %v", err)
	}
	if _, err := d.SaveFile(dctx, alice, f.ID, ptr("x"), nil, "abc"); !isCode(err, diagram.ErrPrecondition, "IF_MATCH_REQUIRED") {
		t.Errorf("garbage If-Match: %v", err)
	}
	if _, err := d.SaveFile(dctx, alice, f.ID, nil, nil, ifMatch(1)); !errors.Is(err, domain.ErrNotFound) && err == nil {
		t.Errorf("empty body accepted")
	}

	v2 := must1[DiagramFile](t)(d.SaveFile(dctx, alice, f.ID, ptr("v2"), docOf("a"), ifMatch(1)))
	if v2.Version != 2 || v2.Name != "v2" || v2.UpdatedBy != alice {
		t.Errorf("saved = %+v", v2.File)
	}
	for _, h := range []string{`2`, `"2"`, `W/"2"`} { // bare, quoted and weak etags
		cur := must1[DiagramFile](t)(d.GetFile(dctx, alice, f.ID))
		if _, err := d.SaveFile(dctx, alice, f.ID, ptr("n"), nil, strings.ReplaceAll(h, "2", fmt.Sprint(cur.Version))); err != nil {
			t.Errorf("If-Match %s: %v", h, err)
		}
	}
	cur := must1[DiagramFile](t)(d.GetFile(dctx, alice, f.ID))
	_, err = d.SaveFile(dctx, alice, f.ID, ptr("stale"), nil, ifMatch(1))
	if !isCode(err, diagram.ErrPrecondition, "VERSION_CONFLICT") || !errors.As(err, &e) || e.Current != cur.Version {
		t.Errorf("stale save: %v, want VERSION_CONFLICT at %d", err, cur.Version)
	}
	if got := must1[DiagramFile](t)(d.GetFile(dctx, alice, f.ID)); got.Name == "stale" {
		t.Error("a rejected save wrote")
	}
	if _, err := d.SaveFile(dctx, alice, f.ID, nil, json.RawMessage(`{"version":3}`), ifMatch(cur.Version)); err == nil {
		t.Error("invalid document accepted")
	}
}

func TestDiagramConcurrentSavesOneWins(t *testing.T) {
	d, _ := newDiagrams(t)
	f := project(t, d, alice, "R").File
	var wg sync.WaitGroup
	var mu sync.Mutex
	var wins, conflicts int
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := d.SaveFile(dctx, alice, f.ID, ptr(fmt.Sprintf("w%d", i)), nil, ifMatch(1))
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				wins++
			case isCode(err, diagram.ErrPrecondition, "VERSION_CONFLICT"):
				conflicts++
			default:
				t.Errorf("save: %v", err)
			}
		}()
	}
	wg.Wait()
	if wins != 1 || conflicts != 7 {
		t.Errorf("wins %d, conflicts %d; want 1 and 7", wins, conflicts)
	}
}

func TestDiagramRenameKeepsDocumentIdentity(t *testing.T) {
	d, _ := newDiagrams(t)
	f := project(t, d, alice, "N").File
	if _, _, err := d.UpdateFileMeta(dctx, alice, f.ID, FilePatch{Name: ptr("tree name")}); err != nil {
		t.Fatal(err)
	}
	// a save carrying a stale document name must not undo the rename
	stale := json.RawMessage(`{"version":2,"name":"old doc name","activePageId":"p","pages":[{"id":"p","name":"P","nodes":[],"edges":[]}]}`)
	got := must1[DiagramFile](t)(d.SaveFile(dctx, alice, f.ID, nil, stale, ifMatch(1)))
	if got.Name != "tree name" {
		t.Errorf("name = %q, want the tree name", got.Name)
	}
}

func TestDiagramKindFollowsContent(t *testing.T) {
	d, _ := newDiagrams(t)
	f := project(t, d, alice, "K").File
	board := json.RawMessage(`{"version":2,"activePageId":"p","pages":[{"id":"p","name":"P","edges":[],"nodes":[
		{"id":"s1","type":"sticky"},{"id":"s2","type":"sticky"},{"id":"s3","type":"sticky"},{"id":"a","type":"service"}]}]}`)
	got := must1[DiagramFile](t)(d.SaveFile(dctx, alice, f.ID, nil, board, ifMatch(1)))
	if got.Kind != diagram.KindBoard || got.PageCount != 1 {
		t.Errorf("kind = %q, pages = %d", got.Kind, got.PageCount)
	}
}

func TestDiagramHistory(t *testing.T) {
	d, _ := newDiagrams(t)
	f := project(t, d, alice, "H").File
	save := func(v int, nodes ...string) {
		t.Helper()
		if _, err := d.SaveFile(dctx, alice, f.ID, nil, docOf(nodes...), ifMatch(v)); err != nil {
			t.Fatal(err)
		}
	}
	save(1, "a")
	save(2, "a", "b")

	vs := must1[[]diagram.Version](t)(d.Versions(dctx, alice, f.ID))
	if len(vs) != 3 || vs[0].Version != 3 || vs[2].Version != 1 || vs[0].CreatedBy != alice || vs[0].Data != nil {
		t.Errorf("versions = %+v", vs)
	}
	v2 := must1[diagram.Version](t)(d.Version(dctx, alice, f.ID, 2))
	if nodeCount(t, v2.Data) != 1 {
		t.Errorf("v2 has %d nodes, want 1", nodeCount(t, v2.Data))
	}
	if _, err := d.Version(dctx, alice, f.ID, 99); !isCode(err, domain.ErrNotFound, "NOT_FOUND") {
		t.Errorf("unknown version: %v", err)
	}
	if _, err := d.Versions(dctx, eve, f.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("stranger: %v", err)
	}

	// restore builds a NEW version on the head, with a label
	save(3, "a", "b", "c")
	restored := must1[DiagramFile](t)(d.RestoreVersion(dctx, alice, f.ID, 1))
	if restored.Version != 5 || nodeCount(t, restored.Data) != 0 {
		t.Errorf("restored = v%d with %d nodes, want v5 with 0 (the first, empty document)", restored.Version, nodeCount(t, restored.Data))
	}
	if _, err := d.RestoreVersion(dctx, alice, f.ID, 99); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("restore unknown: %v", err)
	}
}

func TestDiagramRestoreLabelsAndPrunes(t *testing.T) {
	d, _ := newDiagrams(t)
	d.KeepLast, d.KeepDays = 3, 0
	f := project(t, d, alice, "H").File
	for v := 1; v <= 5; v++ {
		if _, err := d.SaveFile(dctx, alice, f.ID, nil, docOf(fmt.Sprint("n", v)), ifMatch(v)); err != nil {
			t.Fatal(err)
		}
	}
	vs := must1[[]diagram.Version](t)(d.Versions(dctx, alice, f.ID))
	if len(vs) != 3 || vs[0].Version != 6 || vs[2].Version != 4 {
		t.Fatalf("after pruning: %+v", vs)
	}
	if _, err := d.Version(dctx, alice, f.ID, 1); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("v1 should be pruned: %v", err)
	}
	if _, err := d.RestoreVersion(dctx, alice, f.ID, 4); err != nil {
		t.Fatal(err)
	}
	head := must1[diagram.Version](t)(d.Version(dctx, alice, f.ID, 7))
	if head.Label == nil || *head.Label != "Restored from v4" {
		t.Errorf("label = %v", head.Label)
	}
}

func TestDiagramNamedVersions(t *testing.T) {
	d, _ := newDiagrams(t)
	d.KeepLast, d.KeepDays = 2, 0
	f := project(t, d, alice, "V").File

	v := must1[diagram.Version](t)(d.SaveNamedVersion(dctx, alice, f.ID, "  Release 1  "))
	if v.Version != 1 || !v.Named || v.Label == nil || *v.Label != "Release 1" || v.Data != nil {
		t.Errorf("named = %+v", v)
	}
	// naming again relabels the same snapshot instead of adding one
	v = must1[diagram.Version](t)(d.SaveNamedVersion(dctx, alice, f.ID, "Release 1.0"))
	if vs := must1[[]diagram.Version](t)(d.Versions(dctx, alice, f.ID)); len(vs) != 1 || *vs[0].Label != "Release 1.0" {
		t.Errorf("versions = %+v", vs)
	}
	for ver := 1; ver <= 4; ver++ { // enough saves to prune v1 if it were not named
		if _, err := d.SaveFile(dctx, alice, f.ID, nil, docOf(fmt.Sprint("n", ver)), ifMatch(ver)); err != nil {
			t.Fatal(err)
		}
	}
	vs := must1[[]diagram.Version](t)(d.Versions(dctx, alice, f.ID))
	if len(vs) != 3 || vs[2].Version != 1 || !vs[2].Named {
		t.Errorf("named version must survive pruning: %+v", vs)
	}
	for _, bad := range []string{"", "   ", strings.Repeat("x", MaxVersionLabel+1)} {
		var iv *domain.ValidationError
		if _, err := d.SaveNamedVersion(dctx, alice, f.ID, bad); !errors.As(err, &iv) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestDiagramDuplicateFile(t *testing.T) {
	d, mem := newDiagrams(t)
	res := project(t, d, alice, "Original")
	pid := res.Project.ID
	src := must1[DiagramFile](t)(d.SaveFile(dctx, alice, res.File.ID, nil, docOf("a"), ifMatch(1)))

	cp := must1[DiagramFile](t)(d.DuplicateFile(dctx, alice, src.ID, diagram.Opt[*string]{}))
	if cp.ID == src.ID || cp.Name != "Original (copy)" || cp.Version != 1 || cp.ProjectID != pid || cp.FolderID != nil {
		t.Errorf("copy = %+v", cp.File)
	}
	if nodeCount(t, cp.Data) != 1 {
		t.Error("copy lost its node")
	}
	hist := must1[diagram.Version](t)(d.Version(dctx, alice, cp.ID, 1))
	if hist.Label == nil || *hist.Label != "Duplicated from Original" {
		t.Errorf("history label = %v", hist.Label)
	}

	dir, _, _ := d.CreateFolder(dctx, alice, pid, NewFolder{Name: "d"})
	into := must1[DiagramFile](t)(d.DuplicateFile(dctx, alice, src.ID, diagram.Opt[*string]{Set: true, Val: &dir.ID}))
	if into.FolderID == nil || *into.FolderID != dir.ID {
		t.Errorf("copy into folder = %v", into.FolderID)
	}
	ghost := "fld_nope0000"
	if _, err := d.DuplicateFile(dctx, alice, src.ID, diagram.Opt[*string]{Set: true, Val: &ghost}); err == nil {
		t.Error("copy into an unknown folder")
	}

	// the copy lands in the project: viewers cannot, strangers get not found
	if _, err := d.DuplicateFile(dctx, bob, src.ID, diagram.Opt[*string]{}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("stranger: %v", err)
	}
	mem.AddMember(pid, bob, diagram.RoleViewer)
	if _, err := d.DuplicateFile(dctx, bob, src.ID, diagram.Opt[*string]{}); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("viewer: %v", err)
	}
	mem.AddMember(pid, bob, diagram.RoleEditor)
	if _, err := d.DuplicateFile(dctx, bob, src.ID, diagram.Opt[*string]{}); err != nil {
		t.Errorf("editor: %v", err)
	}
}

func TestDiagramListProjects(t *testing.T) {
	d, mem := newDiagrams(t)
	for _, n := range []string{"Alpha", "beta", "Gamma", "delta_x", "Epsilon 100%"} {
		project(t, d, alice, n)
	}
	shared := project(t, d, bob, "Shared")
	mem.AddMember(shared.Project.ID, alice, diagram.RoleViewer)
	project(t, d, bob, "Bob only")

	q := func(q diagram.ProjectQuery) DiagramProjectPage {
		if q.Page == 0 {
			q.Page, q.PageSize = 1, 20
		}
		return must1[DiagramProjectPage](t)(d.ListProjects(dctx, alice, q))
	}
	all := q(diagram.ProjectQuery{})
	if all.Total != 6 {
		t.Errorf("total = %d, want 6 (own + shared)", all.Total)
	}
	roles := map[string]diagram.Role{}
	for _, p := range all.Items {
		roles[p.Name] = p.Role
	}
	if roles["Shared"] != diagram.RoleViewer || roles["Alpha"] != diagram.RoleOwner || roles["Bob only"] != "" {
		t.Errorf("roles = %v", roles)
	}
	if got := q(diagram.ProjectQuery{Search: "ALPHA"}); got.Total != 1 || got.Items[0].Name != "Alpha" {
		t.Errorf("search = %+v", got)
	}
	byName := q(diagram.ProjectQuery{SortByName: true, Asc: true})
	if byName.Items[0].Name != "Alpha" || byName.Items[1].Name != "beta" {
		t.Errorf("sorted by name: %s, %s", byName.Items[0].Name, byName.Items[1].Name)
	}
	if p2 := q(diagram.ProjectQuery{Page: 2, PageSize: 4}); len(p2.Items) != 2 || p2.Total != 6 || p2.Page != 2 {
		t.Errorf("page 2 = %d items of %d", len(p2.Items), p2.Total)
	}
}

func TestDiagramDeleteProject(t *testing.T) {
	d, _ := newDiagrams(t)
	res := project(t, d, alice, "Gone")
	if err := d.DeleteProject(dctx, alice, res.Project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.GetProject(dctx, alice, res.Project.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("project survived: %v", err)
	}
	if _, err := d.GetFile(dctx, alice, res.File.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("file survived: %v", err)
	}
}

func TestDiagramRenameProject(t *testing.T) {
	d, _ := newDiagrams(t)
	p := project(t, d, alice, "Old").Project
	got := must1[diagram.ProjectSummary](t)(d.RenameProject(dctx, alice, p.ID, "  New  "))
	if got.Name != "New" || got.TreeRev != p.TreeRev+1 {
		t.Errorf("renamed = %+v", got)
	}
	// whitespace only keeps the old name
	if got := must1[diagram.ProjectSummary](t)(d.RenameProject(dctx, alice, p.ID, "   ")); got.Name != "New" {
		t.Errorf("blank rename = %q", got.Name)
	}
	for _, bad := range []string{"", strings.Repeat("x", 201), "a\u0007"} {
		if _, err := d.RenameProject(dctx, alice, p.ID, bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestDiagramPrefs(t *testing.T) {
	d, _ := newDiagrams(t)
	get := func(user int) string { return string(must1[json.RawMessage](t)(d.GetPrefs(dctx, user))) }
	if get(alice) != `{}` {
		t.Errorf("fresh prefs = %s", get(alice))
	}
	patch := func(user int, body string) error {
		_, err := d.PatchPrefs(dctx, user, json.RawMessage(body))
		return err
	}
	if err := patch(alice, `{"tabs":{"p1":{"open":["a","b"],"active":"b"}}}`); err != nil {
		t.Fatal(err)
	}
	if err := patch(alice, `{"starred":["a"]}`); err != nil {
		t.Fatal(err)
	}
	if got := get(alice); got != `{"starred":["a"],"tabs":{"p1":{"active":"b","open":["a","b"]}}}` && got != `{"starred":["a"],"tabs":{"p1":{"open":["a","b"],"active":"b"}}}` {
		t.Errorf("merged = %s", got)
	}
	if err := patch(alice, `{"starred":null}`); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(get(alice), "starred") {
		t.Errorf("null did not delete: %s", get(alice))
	}
	if get(bob) != `{}` {
		t.Errorf("prefs are per user: %s", get(bob))
	}
	big := fmt.Sprintf(`{"big":%q}`, strings.Repeat("x", 70*1024))
	if err := patch(alice, big); !errors.Is(err, diagram.ErrTooLarge) {
		t.Errorf("oversize: %v", err)
	}
	if strings.Contains(get(alice), "big") {
		t.Error("an oversize patch was stored")
	}
}

func TestDiagramDataLimit(t *testing.T) {
	d, _ := newDiagrams(t)
	d.MaxDataBytes = 1024
	huge := json.RawMessage(`{"version":2,"activePageId":"p","pages":[{"id":"p","name":"P","edges":[],"nodes":[{"id":"a","type":"service","label":"` + strings.Repeat("x", 2048) + `"}]}]}`)
	if _, err := d.CreateProject(dctx, alice, NewDiagramProject{Name: "big", Data: huge}); !errors.Is(err, diagram.ErrTooLarge) {
		t.Errorf("create: %v", err)
	}
	f := project(t, d, alice, "small").File
	if _, err := d.SaveFile(dctx, alice, f.ID, nil, huge, ifMatch(1)); !errors.Is(err, diagram.ErrTooLarge) {
		t.Errorf("save: %v", err)
	}
}

func TestDiagramSaveCoalescing(t *testing.T) {
	d, mem := newDiagrams(t)
	d.Coalesce = 5 * time.Minute
	pid := project(t, d, alice, "C")
	f := pid.File
	mem.AddMember(pid.Project.ID, bob, diagram.RoleEditor)
	save := func(who, v int, nodes ...string) {
		t.Helper()
		if _, err := d.SaveFile(dctx, who, f.ID, nil, docOf(nodes...), ifMatch(v)); err != nil {
			t.Fatal(err)
		}
	}
	versions := func() []int {
		t.Helper()
		var out []int
		for _, v := range must1[[]diagram.Version](t)(d.Versions(dctx, alice, f.ID)) {
			out = append(out, v.Version)
		}
		return out
	}

	save(alice, 1, "a")
	save(alice, 2, "a", "b")
	if got := versions(); len(got) != 2 || got[0] != 3 {
		t.Errorf("one editing burst = one snapshot (v1, as created, is kept), got %v", got)
	}
	v3 := must1[diagram.Version](t)(d.Version(dctx, alice, f.ID, 3))
	if nodeCount(t, v3.Data) != 2 {
		t.Errorf("snapshot holds %d nodes, want the latest 2", nodeCount(t, v3.Data))
	}

	// another writer starts their own snapshot
	save(bob, 3, "a", "b", "c")
	if got := versions(); len(got) != 3 || got[0] != 4 {
		t.Errorf("other writer: %v", got)
	}

	// a named head is never rewritten
	if _, err := d.SaveNamedVersion(dctx, alice, f.ID, "keep"); err != nil {
		t.Fatal(err)
	}
	save(bob, 4, "a")
	if got := versions(); len(got) != 4 || got[0] != 5 {
		t.Errorf("named head: %v", got)
	}

	// a stale head is never rewritten
	d.Coalesce = time.Second
	save(bob, 5, "a", "b")
	if got := versions(); len(got) != 5 {
		t.Errorf("old head: %v", got)
	}

	// 0 disables
	d.Coalesce = 0
	save(bob, 6, "a")
	save(bob, 7, "a")
	if got := versions(); len(got) != 7 {
		t.Errorf("disabled: %v", got)
	}
}

func TestDiagramRestoredFlag(t *testing.T) {
	d, _ := newDiagrams(t)
	d.Coalesce = 5 * time.Minute
	f := project(t, d, alice, "R").File
	if _, err := d.SaveFile(dctx, alice, f.ID, nil, docOf("a"), ifMatch(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RestoreVersion(dctx, alice, f.ID, 2); err != nil {
		t.Fatal(err)
	}
	_, err := d.SaveFile(dctx, alice, f.ID, nil, docOf("x"), ifMatch(1))
	var e *diagram.Error
	if !errors.As(err, &e) || e.Code != "VERSION_CONFLICT" || !e.Restored || e.Current != 3 {
		t.Fatalf("stale save over a restore = %+v", err)
	}
	// the save after a restore adds a snapshot: the restore stays in history
	if _, err := d.SaveFile(dctx, alice, f.ID, nil, docOf("x"), ifMatch(3)); err != nil {
		t.Fatal(err)
	}
	if got := must1[[]diagram.Version](t)(d.Versions(dctx, alice, f.ID)); len(got) < 2 || got[1].Version != 3 {
		t.Errorf("restore snapshot lost: %+v", got)
	}
	_, err = d.SaveFile(dctx, alice, f.ID, nil, docOf("y"), ifMatch(3))
	if !errors.As(err, &e) || e.Restored {
		t.Errorf("ordinary head must not report restored: %+v", err)
	}
}
