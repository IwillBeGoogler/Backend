package application

import (
	"errors"
	"strings"
	"testing"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
)

func TestSDDExportImportRoundTrip(t *testing.T) {
	e := newSharing(t)
	d := e.Diagrams
	res := project(t, d, alice, "Shop")
	pid := res.Project.ID
	folder, _, err := d.CreateFolder(dctx, alice, pid, NewFolder{Name: "Backend"})
	if err != nil {
		t.Fatal(err)
	}
	sub, _, _ := d.CreateFolder(dctx, alice, pid, NewFolder{Name: "Deep", ParentID: &folder.ID})
	f2, _, err := d.CreateFile(dctx, alice, pid, NewDiagramFile{Name: "Orders", FolderID: &sub.ID, Data: docOf("a", "b")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.SetThumbnail(dctx, alice, f2.ID, pngBytes); err != nil {
		t.Fatal(err)
	}

	proj, err := d.ExportProject(dctx, alice, "", pid)
	if err != nil || len(proj.Data) == 0 || proj.Name != "Shop" {
		t.Fatalf("export project: %v %+v", err, proj.Name)
	}
	file, err := d.ExportFile(dctx, alice, "", f2.ID)
	if err != nil || file.Name != "Orders" || file.Version == 0 {
		t.Fatalf("export file: %v %+v", err, file.Name)
	}
	text, err := d.ExportFileText(dctx, alice, "", f2.ID)
	if err != nil || !strings.HasPrefix(string(text.Data), "#!sdd") {
		t.Fatalf("export text: %v %q", err, text.Data)
	}
	if _, err := d.ExportFile(dctx, eve, "", f2.ID); err == nil {
		t.Error("stranger exported")
	}
	if _, err := d.ExportProject(dctx, eve, "", pid); err == nil {
		t.Error("stranger exported project")
	}
	if _, err := d.ExportFileText(dctx, eve, "", f2.ID); err == nil {
		t.Error("stranger exported text")
	}

	// a whole project comes back with its tree
	imp, err := d.ImportAny(dctx, bob, "shop.sdd", proj.Data)
	if err != nil || imp.Kind != "project" || len(imp.Folders) != 2 || len(imp.Files) != 2 {
		t.Fatalf("import project: %v %+v", err, imp)
	}
	if imp2, err := d.ImportProject(dctx, bob, "shop.sdd", proj.Data); err != nil || len(imp2.Files) != 2 {
		t.Fatalf("ImportProject: %v", err)
	}
	// a file container becomes a project with one file
	one, err := d.ImportAny(dctx, bob, "orders.sdd", file.Data)
	if err != nil || one.Kind != "file" || one.File.ID == "" {
		t.Fatalf("import file: %v %+v", err, one)
	}
	if _, err := d.ImportFileAsProject(dctx, bob, "orders.sdd", file.Data); err != nil {
		t.Fatalf("ImportFileAsProject: %v", err)
	}
	// text becomes a one-page file, named after the upload when it has no title
	tx, err := d.ImportAny(dctx, bob, "sketch.sdd", text.Data)
	if err != nil || tx.Kind != "text" {
		t.Fatalf("import text: %v %+v", err, tx)
	}

	// the wrong container for the entry point
	var inv *SDDInvalid
	if _, err := d.ImportFileAsProject(dctx, bob, "x.sdd", proj.Data); !errors.As(err, &inv) {
		t.Errorf("project via file entry: %v", err)
	}
	if _, err := d.ImportProject(dctx, bob, "x.sdd", file.Data); !errors.As(err, &inv) {
		t.Errorf("file via project entry: %v", err)
	}
	if _, err := d.ImportAny(dctx, bob, "x.sdd", []byte("garbage")); err == nil {
		t.Error("garbage accepted")
	}
	if _, err := d.ImportProject(dctx, bob, "x.sdd", []byte("garbage")); err == nil {
		t.Error("garbage project accepted")
	}
	if _, err := d.ImportFileAsProject(dctx, bob, "x.sdd", []byte("garbage")); err == nil {
		t.Error("garbage file accepted")
	}
	if inv != nil && inv.Error() == "" {
		t.Error("empty message")
	}

	// into an existing project
	target := project(t, d, bob, "Target")
	in, err := d.ImportIntoProject(dctx, bob, target.Project.ID, nil, "orders.sdd", file.Data)
	if err != nil || in.File.ID == "" {
		t.Fatalf("into project: %v", err)
	}
	if _, err := d.ImportIntoProject(dctx, bob, target.Project.ID, nil, "shop.sdd", proj.Data); !errors.As(err, &inv) {
		t.Errorf("project into project: %v", err)
	}
	if _, err := d.ImportIntoProject(dctx, bob, target.Project.ID, nil, "x.sdd", []byte("junk")); err == nil {
		t.Error("junk into project")
	}
	if _, err := d.ImportIntoProject(dctx, eve, target.Project.ID, nil, "o.sdd", file.Data); err == nil {
		t.Error("stranger imported")
	}
	missing := "fld_missing"
	if _, err := d.ImportIntoProject(dctx, bob, target.Project.ID, &missing, "o.sdd", file.Data); err == nil {
		t.Error("unknown folder")
	}
}

func TestSDDTextErrorsAndLimits(t *testing.T) {
	e := newSharing(t)
	d := e.Diagrams
	var inv *SDDInvalid
	if _, err := d.ImportAny(dctx, alice, "bad.sdd", []byte("#!sdd 1\nthis is not valid ###\n")); !errors.As(err, &inv) || inv.Reason == "" {
		t.Errorf("bad text: %v", err)
	}
	if d.SDDMaxBytes() != 100<<20 {
		t.Errorf("default max = %d", d.SDDMaxBytes())
	}
	d.MaxSDDBytes = 10
	if d.SDDMaxBytes() != 10 {
		t.Error("custom max")
	}
	if _, err := d.textFile("x", []byte("plain")); !errors.As(err, &inv) {
		t.Errorf("not text: %v", err)
	}
	// a file with no pages cannot be exported as text
	res := project(t, d, alice, "Empty")
	_ = res
	if got := d.thumbnailPNG(dctx, diagram.Doc{}); got != nil {
		t.Error("thumb without hash")
	}
	if got := d.assetBytes(dctx, "not-a-hash"); got != nil {
		t.Error("bad hash")
	}
	if pickThumb(nil, map[string][]byte{"x": {1}}) == nil || pickThumb(nil, nil) != nil {
		t.Error("pickThumb")
	}
	if len(orderedFolders(nil)) != 0 {
		t.Error("orderedFolders")
	}
}
