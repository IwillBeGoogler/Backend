package application

// Input-validation, replay and unconfigured-dependency paths of the diagram
// use cases, driven through the same flaky wiring as the sweeps.

import (
	"strings"
	"testing"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
)

func TestDiagramBadInputsSweep(t *testing.T) {
	long := strings.Repeat("n", 300)
	sweep(t, func(d *Diagrams) {
		pid, fid := newPrj(d, alice, "Inputs")
		sink(d.CreateProject(dctx, alice, NewDiagramProject{ID: "bad id!", Name: "x"}))
		sink(d.CreateProject(dctx, alice, NewDiagramProject{ID: "proj_fixed1", Name: "Fixed", Data: docOf("a")}))
		sink(d.CreateProject(dctx, alice, NewDiagramProject{ID: "proj_fixed1", Name: "Fixed", Data: docOf("a")}))
		sink(d.CreateProject(dctx, bob, NewDiagramProject{ID: "proj_fixed1", Name: "Fixed"}))
		sink(d.CreateProject(dctx, alice, NewDiagramProject{Name: "F", File: &NewDiagramFile{ID: "file_fixed1", Name: "f", Kind: "sdd", Data: docOf("a")}}))
		sink(d.CreateProject(dctx, alice, NewDiagramProject{ID: "proj_fixed2", Name: "F", File: &NewDiagramFile{ID: "file_fixed1", Name: "f"}}))
		sink(d.CreateProject(dctx, alice, NewDiagramProject{Name: long}))

		sink(d.CreateFolder(dctx, alice, pid, NewFolder{ID: "bad id!", Name: "f"}))
		sink(d.CreateFolder(dctx, alice, pid, NewFolder{ID: "fold_fixed1", Name: "f"}))
		sink(d.CreateFolder(dctx, alice, pid, NewFolder{ID: "fold_fixed1", Name: "f"}))
		sink(d.CreateFolder(dctx, bob, pid, NewFolder{ID: "fold_fixed1", Name: "f"}))
		sink(d.CreateFolder(dctx, alice, pid, NewFolder{Name: long}))
		sink(d.UpdateFolder(dctx, alice, "fold_fixed1", FolderPatch{Name: &long}))
		sink(d.UpdateFolder(dctx, alice, "fold_fixed1", FolderPatch{Name: sp("   ")}))
		sink(d.UpdateFolder(dctx, alice, "fold_fixed1", FolderPatch{ParentID: diagram.Opt[*string]{Set: true, Val: sp("missing")}}))

		sink(d.CreateFile(dctx, alice, pid, NewDiagramFile{ID: "bad id!", Name: "f"}))
		sink(d.CreateFile(dctx, alice, pid, NewDiagramFile{Name: long}))
		sink(d.CreateFile(dctx, alice, pid, NewDiagramFile{Name: "f", Kind: "nope"}))
		sink(d.CreateFile(dctx, alice, pid, NewDiagramFile{ID: "file_fixed9", Name: "f", Kind: "uml", Data: docOf("a")}))
		sink(d.CreateFile(dctx, alice, pid, NewDiagramFile{ID: "file_fixed9", Name: "f", Kind: "uml", Data: docOf("a")}))
		sink(d.CreateFile(dctx, bob, pid, NewDiagramFile{ID: "file_fixed9", Name: "f"}))
		sink(d.CreateFile(dctx, alice, pid, NewDiagramFile{Name: "f", FolderID: sp("missing")}))

		sink(d.SaveFile(dctx, alice, fid, &long, docOf("a"), ifMatch(1)))
		sink(d.SaveFile(dctx, alice, fid, sp("  "), docOf("a"), ifMatch(1)))
		sink(d.SaveFile(dctx, alice, fid, sp("renamed"), docOf("c"), ifMatch(1)))
		sink(d.SaveFile(dctx, alice, fid, nil, []byte("not json"), ifMatch(2)))
		sink(d.SaveFile(dctx, bob, fid, nil, docOf("a"), ifMatch(2)))
		sink(d.UpdateFileMeta(dctx, alice, fid, FilePatch{Name: &long}))
		sink(d.UpdateFileMeta(dctx, alice, fid, FilePatch{Name: sp("  ")}))
		sink(d.UpdateFileMeta(dctx, alice, fid, FilePatch{Kind: sp("nope")}))
		sink(d.UpdateFileMeta(dctx, alice, fid, FilePatch{Kind: sp("uml")}))
		sink(d.UpdateFileMeta(dctx, alice, fid, FilePatch{FolderID: diagram.Opt[*string]{Set: true, Val: sp("missing")}}))
		sink(d.UpdateFileMeta(dctx, bob, fid, FilePatch{Name: sp("x")}))
		sink(d.RestoreVersion(dctx, alice, fid, 99))
		sink(d.RestoreVersion(dctx, alice, fid, 1))
		sink(d.DuplicateFolder(dctx, alice, "missing"))
		sink(d.DuplicateFile(dctx, alice, fid, diagram.Opt[*string]{Set: true, Val: sp("missing")}))
		sink(d.DeleteFile(dctx, bob, fid))
		sink(d.DeleteFolder(dctx, bob, "fold_fixed1"))
		sink(d.DeleteProject(dctx, bob, pid))
	})
}

func TestDiagramUnconfigured(t *testing.T) {
	d := sweepEnv(0)
	pid, fid := newPrj(d, alice, "Plain")
	tpl, _ := d.CreateTemplate(dctx, alice, NewTemplate{Title: "T", Kind: "sdd", FileID: fid})
	calls := func() {
		sink(d.AdminSchemaSQL(dctx, ""))
		sink(d.AdminStats(dctx))
		sink(d.AdminProjects(dctx, "", "", dpaging()))
		sink(d.AdminProject(dctx, pid))
		sink(d.AdminRevokeShare(dctx, alice, "shr_x"))
		sink(d.AdminDeleteProject(dctx, alice, pid))
		sink(d.AdminDeleteFile(dctx, alice, fid))
		sink(d.CreateTemplate(dctx, alice, NewTemplate{Title: "T", Kind: "sdd", FileID: fid}))
		sink(d.UpdateTemplate(dctx, alice, tpl.ID, TemplatePatch{}))
		sink(d.DeleteTemplate(dctx, alice, tpl.ID))
		sink(d.ReorderTemplates(dctx, alice, nil))
		sink(d.AdminTemplates(dctx))
		sink(d.PublicTemplates(dctx))
		sink(d.ForkTemplate(dctx, alice, tpl.ID))
	}
	d.Admin, d.Templates = nil, nil
	calls()
	d.Authz = nil
	calls()
}

func dpaging() domain.Paging { return domain.Paging{Page: 1, Limit: 5} }
