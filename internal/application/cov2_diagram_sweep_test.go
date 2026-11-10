package application

// Fault-injection sweeps: every scenario runs once clean to count its
// repository calls, then again with each single call failing, so every
// repository-error branch of the diagram use cases is walked.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram/diagramtest"
)

// sink swallows the results of a call; sweeps only care that nothing panics.
func sink(...any) {}

type sweepViews struct{ DiagramViewCounter }

func (sweepViews) Hit(context.Context, string, string) error { return nil }
func (sweepViews) Count(context.Context, string) int64       { return 3 }
func (sweepViews) Counts(context.Context, []string) map[string]int64 {
	return map[string]int64{}
}

// flakyBlobs fails its Put or Get when the shared repository counter says so.
type flakyBlobs struct {
	inner *memBlobs
	fl    *flaky
}

func (b *flakyBlobs) Put(ctx context.Context, key string, r io.Reader, size int64, ct string) error {
	if err := b.fl.hit(); err != nil {
		return err
	}
	return b.inner.Put(ctx, key, r, size, ct)
}

func (b *flakyBlobs) Get(ctx context.Context, key string) (Object, error) {
	if err := b.fl.hit(); err != nil {
		return Object{}, err
	}
	return b.inner.Get(ctx, key)
}

// sweepEnv is a fully wired Diagrams over a flaky repository.
func sweepEnv(failAt int) *Diagrams {
	mem := diagramtest.NewMemory()
	fl := &flaky{Memory: mem, failAt: failAt}
	d := NewDiagrams(fl, fl)
	d.Coalesce = 0
	at := time.Unix(1_700_000_000, 0)
	d.now = func() time.Time { at = at.Add(time.Second); return at }
	users := diagramtest.NewUsers()
	users.Add(alice, "alice", "alice@example.com", "Alice A")
	users.Add(bob, "bob", "bob@example.com", "")
	users.Add(eve, "eve", "eve@example.com", "Eve")
	mem.SetUser(alice, "alice")
	mem.SetUser(bob, "bob")
	d.Users = users
	d.Blobs = &flakyBlobs{inner: &memBlobs{data: map[string][]byte{}}, fl: fl}
	d.SanitizeSVG = func(b []byte) ([]byte, bool, error) {
		if !bytes.HasPrefix(b, []byte("<svg")) {
			return nil, false, nil
		}
		if bytes.Contains(b, []byte("script")) {
			return nil, true, errBoom
		}
		return b, true, nil
	}
	d.Notices = diagramtest.NewNotices()
	d.Templates = diagramtest.NewTemplates(mem)
	d.Authz = fakeAuthz{caller: alice, perms: map[string]bool{"diagrams.read": true, "diagrams.manage": true, "templates.manage": true}}
	d.Audit = &fakeAudit{}
	d.Views = sweepViews{}
	d.SchemaDump = func(context.Context, string) (string, error) { return "create table t();", nil }
	d.Hooks = NewWebhookDispatcher(d.Webhooks, &memQueue{}, &scriptedSender{status: 200}, func(context.Context, string) error { return nil })
	d.Events = d.Hooks
	return d
}

func sweep(t *testing.T, scenario func(d *Diagrams)) {
	t.Helper()
	probe := sweepEnv(0)
	scenario(probe)
	n := probe.Repo.(*flaky).calls
	if n == 0 {
		t.Fatal("scenario made no repository calls")
	}
	for k := 1; k <= n; k++ {
		scenario(sweepEnv(k))
	}
}

func sp(s string) *string { return &s }

func newPrj(d *Diagrams, owner int, name string) (pid, fid string) {
	p, _ := d.CreateProject(dctx, owner, NewDiagramProject{Name: name, Data: docOf("a", "b")})
	return p.Project.ID, p.File.ID
}

func TestSweepProjectsAndTree(t *testing.T) {
	sweep(t, func(d *Diagrams) {
		pid, fid := newPrj(d, alice, "Shop")
		sink(d.ListProjects(dctx, alice, diagram.ProjectQuery{Page: 1, PageSize: 10}))
		sink(d.GetProject(dctx, alice, pid))
		sink(d.ListAllProjects(dctx, alice))
		sink(d.Tree(dctx, alice, pid))
		sink(d.RenameProject(dctx, alice, pid, "Shop 2"))
		f1, _, _ := d.CreateFolder(dctx, alice, pid, NewFolder{Name: "f1"})
		f2, _, _ := d.CreateFolder(dctx, alice, pid, NewFolder{Name: "f2", ParentID: &f1.ID})
		sink(d.CreateFolder(dctx, alice, pid, NewFolder{Name: "f3", ParentID: &f1.ID, Position: new(float64)}))
		pos := 2.5
		sink(d.UpdateFolder(dctx, alice, f2.ID, FolderPatch{Name: sp("g2"), ParentID: diagram.Opt[*string]{Set: true}, Position: &pos}))
		sink(d.UpdateFolder(dctx, alice, f2.ID, FolderPatch{ParentID: diagram.Opt[*string]{Set: true, Val: &f1.ID}}))
		sink(d.UpdateFolder(dctx, alice, f1.ID, FolderPatch{ParentID: diagram.Opt[*string]{Set: true, Val: &f2.ID}}))
		nf, _, _ := d.CreateFile(dctx, alice, pid, NewDiagramFile{Name: "n", Kind: "sdd", FolderID: &f1.ID, Data: docOf("x")})
		sink(d.CreateFile(dctx, alice, pid, NewDiagramFile{Name: "m", Kind: "erd", Position: &pos}))
		sink(d.GetFile(dctx, alice, nf.ID))
		sink(d.SaveFile(dctx, alice, nf.ID, sp("renamed"), docOf("y"), ifMatch(1)))
		sink(d.SaveFile(dctx, alice, nf.ID, nil, docOf("y", "z"), ifMatch(9)))
		sink(d.SaveFileLabeled(dctx, alice, fid, nil, docOf("q"), ifMatch(1), sp("label")))
		sink(d.UpdateFileMeta(dctx, alice, nf.ID, FilePatch{Name: sp("nn"), Position: &pos, Kind: sp("erd"), FolderID: diagram.Opt[*string]{Set: true}}))
		sink(d.UpdateFileMeta(dctx, alice, nf.ID, FilePatch{FolderID: diagram.Opt[*string]{Set: true, Val: &f2.ID}}))
		sink(d.Versions(dctx, alice, nf.ID))
		sink(d.Version(dctx, alice, nf.ID, 1))
		sink(d.SaveNamedVersion(dctx, alice, nf.ID, "v-one"))
		sink(d.RestoreVersion(dctx, alice, nf.ID, 1))
		sink(d.DuplicateFile(dctx, alice, nf.ID, diagram.Opt[*string]{}))
		sink(d.DuplicateFile(dctx, alice, nf.ID, diagram.Opt[*string]{Set: true, Val: &f2.ID}))
		sink(d.DuplicateFolder(dctx, alice, f1.ID))
		sink(d.GetPrefs(dctx, alice))
		sink(d.PatchPrefs(dctx, alice, json.RawMessage(`{"theme":"dark"}`)))
		sink(d.DeleteFile(dctx, alice, nf.ID))
		sink(d.DeleteFolder(dctx, alice, f1.ID))
		sink(d.CutExpiredShares(dctx))
		sink(d.DeleteProject(dctx, alice, pid))
	})
}

func TestSweepSharing(t *testing.T) {
	sweep(t, func(d *Diagrams) {
		pid, fid := newPrj(d, alice, "Team")
		sink(d.Members(dctx, alice, pid))
		sink(d.AddMember(dctx, alice, pid, "bob", diagram.RoleEditor))
		sink(d.AddMember(dctx, alice, pid, "eve@example.com", diagram.RoleViewer))
		sink(d.SetMemberRole(dctx, alice, pid, bob, diagram.RoleViewer))
		sink(d.GetFile(dctx, bob, fid))
		sink(d.FileMembers(dctx, alice, fid))
		sink(d.SetFileMember(dctx, alice, fid, bob, diagram.RoleEditor))
		sink(d.SetFileMember(dctx, alice, fid, eve, diagram.RoleCommenter))
		sink(d.GetFile(dctx, eve, fid))
		sink(d.RemoveFileMember(dctx, alice, fid, bob))
		sink(d.RemoveMember(dctx, alice, pid, eve))
		sink(d.RemoveMember(dctx, alice, pid, bob))
		_, tok, _ := d.CreateShare(dctx, alice, pid, NewShare{Role: diagram.RoleEditor})
		_, ftok, _ := d.CreateShare(dctx, alice, pid, NewShare{Role: diagram.RoleViewer, FileID: &fid, ExpiresInSeconds: new(int)})
		for _, tk := range []string{tok, ftok} {
			sink(d.CheckShare(dctx, tk))
			sink(d.ShareLinkOf(dctx, tk))
			sink(d.OpenShare(dctx, tk))
			sink(d.ShareFile(dctx, tk, fid))
			sink(d.SaveShare(dctx, tk, fid, nil, docOf("s"), ifMatch(1)))
			sink(d.GetFileVia(dctx, 0, tk, fid))
			sink(d.GetFileVia(dctx, eve, tk, fid))
			sink(d.GetLibrary(dctx, 0, tk, pid))
			sink(d.Thumbnail(dctx, 0, tk, fid))
			sink(d.OpenChannel(dctx, 0, tk, "file", fid))
		}
		sink(d.OpenChannel(dctx, alice, "", "project", pid))
		sink(d.OpenChannel(dctx, alice, "", "file", fid))
		sh, _ := d.Shares(dctx, alice, pid)
		sink(d.Shares(dctx, alice, pid))
		for _, s := range sh {
			sink(d.RevokeShare(dctx, alice, pid, s.ID))
		}
		a, _ := d.StoreAsset(dctx, alice, pngBytes)
		sink(d.StoreAsset(dctx, alice, pngBytes))
		sink(d.StoreAsset(dctx, alice, webpBytes, "image/webp"))
		sink(d.AssetMeta(dctx, a.Hash))
		sink(d.AssetBlob(dctx, a.Hash))
		sink(d.SetThumbnail(dctx, alice, fid, pngBytes))
		sink(d.Thumbnail(dctx, alice, "", fid))
		sink(d.GetLibrary(dctx, alice, "", pid))
		sink(d.SaveLibrary(dctx, alice, pid, 0, json.RawMessage(`{"items":[]}`)))
		sink(d.SaveLibrary(dctx, alice, pid, 1, json.RawMessage(`{"items":[1]}`)))
		sink(d.GetPrivate(dctx, alice, fid))
		sink(d.PutPrivate(dctx, alice, fid, json.RawMessage(`{"stickies":[]}`)))
		sink(d.PutPrivate(dctx, alice, fid, json.RawMessage(`{"stickies":[1]}`)))
		sink(d.GetPrivate(dctx, alice, fid))
	})
}

func TestSweepComments(t *testing.T) {
	sweep(t, func(d *Diagrams) {
		pid, fid := newPrj(d, alice, "Notes")
		sink(d.AddMember(dctx, alice, pid, "bob", diagram.RoleCommenter))
		anchor := json.RawMessage(`{"type":"point","x":1,"y":2}`)
		c, _ := d.AddComment(dctx, bob, fid, NewComment{Body: "hello @alice", Anchor: anchor})
		sink(d.AddComment(dctx, alice, fid, NewComment{Body: "reply @bob", ParentID: c.ID}))
		sink(d.AddComment(dctx, alice, fid, NewComment{Body: "other", Anchor: anchor}))
		sink(d.FileComments(dctx, alice, fid))
		sink(d.FileComments(dctx, bob, fid))
		yes := true
		sink(d.UpdateComment(dctx, alice, c.ID, CommentPatch{Resolved: &yes}))
		sink(d.UpdateComment(dctx, bob, c.ID, CommentPatch{Body: sp("edited")}))
		sink(d.MarkCommentsRead(dctx, alice, fid))
		sink(d.Notifications(dctx, alice, false, 10))
		sink(d.Notifications(dctx, bob, true, 10))
		sink(d.ReadNotifications(dctx, alice, []string{"1"}, false))
		sink(d.ReadNotifications(dctx, alice, nil, true))
		sink(d.DeleteComment(dctx, bob, c.ID))
	})
}

func TestSweepSDD(t *testing.T) {
	sweep(t, func(d *Diagrams) {
		pid, fid := newPrj(d, alice, "Shop")
		a, _ := d.StoreAsset(dctx, alice, pngBytes)
		sink(d.SetThumbnail(dctx, alice, fid, pngBytes))
		sink(d.SaveFile(dctx, alice, fid, nil, json.RawMessage(`{"version":2,"activePageId":"pg","pages":[{"id":"pg","name":"P","nodes":[{"id":"a","type":"image","data":{"hash":"`+a.Hash+`"}}],"edges":[]}],"assets":{"`+a.Hash+`":{"hash":"`+a.Hash+`"}}}`), ifMatch(1)))
		f, _ := d.ExportFile(dctx, alice, "", fid)
		p, _ := d.ExportProject(dctx, alice, "", pid)
		txt, _ := d.ExportFileText(dctx, alice, "", fid)
		sink(d.ImportAny(dctx, bob, "file.sdd", f.Data))
		sink(d.ImportAny(dctx, bob, "proj.sdd", p.Data))
		sink(d.ImportAny(dctx, bob, "t.sdd", txt.Data))
		sink(d.ImportFileAsProject(dctx, bob, "file.sdd", f.Data))
		sink(d.ImportProject(dctx, bob, "proj.sdd", p.Data))
		np, _ := newPrj(d, bob, "Target")
		sink(d.ImportIntoProject(dctx, bob, np, nil, "file.sdd", f.Data))
		sink(d.ImportIntoProject(dctx, bob, np, nil, "t.sdd", txt.Data))
		sink(d.ImportIntoProject(dctx, bob, np, nil, "junk.sdd", []byte("junk")))
	})
}

func TestSweepAdminPublishTokens(t *testing.T) {
	sweep(t, func(d *Diagrams) {
		pid, fid := newPrj(d, alice, "Pub")
		pid2, fid2 := newPrj(d, bob, "Other")
		sink(d.AdminSchemaSQL(dctx, "t"))
		sink(d.AdminStats(dctx))
		sink(d.AdminProjects(dctx, "Pub", "alice", domain.Paging{Page: 1, Limit: 10}))
		sink(d.AdminProjects(dctx, "", "", domain.Paging{}))
		sink(d.AdminProject(dctx, pid))
		sink(d.ReportTarget(dctx, bob, fid))
		tpl, _ := d.CreateTemplate(dctx, alice, NewTemplate{Title: "T", Kind: "sdd", FileID: fid, Published: true})
		tpl2, _ := d.CreateTemplate(dctx, alice, NewTemplate{Title: "U", Kind: "erd", FileID: fid2})
		sink(d.AdminTemplates(dctx))
		yes := true
		sink(d.UpdateTemplate(dctx, alice, tpl.ID, TemplatePatch{Title: sp("T2"), Kind: sp("erd"), Description: sp("d"), FileID: &fid2, Published: &yes, Position: new(int)}))
		sink(d.ReorderTemplates(dctx, alice, []string{tpl2.ID, tpl.ID}))
		sink(d.PublicTemplates(dctx))
		sink(d.ForkTemplate(dctx, bob, tpl.ID))
		sink(d.DeleteTemplate(dctx, alice, tpl2.ID))

		sink(d.PublishFile(dctx, alice, fid, PublishRequest{Visibility: diagram.VisPublic, Title: "My diagram", Description: "desc", Tags: []string{"a", "b"}}))
		sink(d.PublishFile(dctx, bob, fid2, PublishRequest{Visibility: diagram.VisUnlisted, Title: "Other"}))
		l, _ := d.FilePublication(dctx, alice, fid)
		sink(d.FilePublication(dctx, alice, fid))
		sink(d.PublicDiagram(dctx, l.Slug))
		sink(d.PublicDiagrams(dctx, "diagram", "a", domain.Paging{Page: 1, Limit: 10}))
		sink(d.UserDiagrams(dctx, "alice", domain.Paging{Page: 1, Limit: 10}))
		sink(d.PublishedDiagrams(dctx, alice, 1, 10))
		sink(d.EmbeddedDiagrams(dctx, []string{l.Slug, "nope"}))
		sink(d.ClapDiagram(dctx, bob, l.Slug, 2))
		sink(d.BookmarkDiagram(dctx, bob, l.Slug))
		sink(d.DiagramViewer(dctx, bob, l.Slug))
		sink(d.BookmarkedDiagrams(dctx, bob, domain.Paging{Page: 1, Limit: 10}))
		sink(d.ViewDiagram(dctx, l.Slug, "ip"))
		sink(d.UnbookmarkDiagram(dctx, bob, l.Slug))
		sink(d.UnpublishFile(dctx, alice, fid))

		raw, tk, _ := d.CreateToken(dctx, alice, NewAccessToken{Name: "ci", Scopes: []string{diagram.ScopeFilesRead}, ExpiresInDays: 3, ProjectIDs: []string{pid}})
		sink(d.CreateToken(dctx, alice, NewAccessToken{Name: "all", Scopes: diagram.Scopes}))
		sink(d.ListTokens(dctx, alice))
		sink(d.AuthenticateToken(dctx, raw))
		sink(d.RevokeToken(dctx, alice, tk.ID))
		sink(d.MCPLabel(dctx, alice, "edit"))
		d.RecordMCP(dctx, diagram.MCPAudit{UserID: alice, Tool: "edit"})
		sink(d.MCPAuditLog(dctx, alice, 10))

		h, _ := d.CreateWebhook(dctx, alice, pid, "https://hooks.example.com/in", diagram.WebhookEvents)
		sink(d.ListWebhooks(dctx, alice, pid))
		no := false
		sink(d.UpdateWebhook(dctx, alice, pid, h.ID, WebhookPatch{URL: sp("https://hooks.example.com/2"), Events: []string{diagram.WebhookFileUpdated}, Active: &no}))
		sink(d.SaveFile(dctx, alice, fid, nil, docOf("w"), ifMatch(1)))
		sink(d.TestWebhook(dctx, alice, pid, h.ID))
		sink(d.WebhookDeliveries(dctx, alice, pid, h.ID))
		sink(d.DeleteWebhook(dctx, alice, pid, h.ID))

		sink(d.EditFile(dctx, alice, fid, "ai edit", func(doc Obj) error { doc["marker"] = 1; return nil }))
		sink(d.UndoMCPChange(dctx, alice, fid))
		sink(d.AdminRevokeShare(dctx, alice, "shr_x"))
		_, tok, _ := d.CreateShare(dctx, bob, pid2, NewShare{Role: diagram.RoleViewer})
		sh, _ := d.Shares(dctx, bob, pid2)
		for _, s := range sh {
			sink(d.AdminRevokeShare(dctx, alice, s.ID))
		}
		sink(tok)
		sink(d.AdminDeleteFile(dctx, alice, fid2))
		sink(d.AdminDeleteProject(dctx, alice, pid2))
	})
}

func TestSweepAssets(t *testing.T) {
	sweep(t, func(d *Diagrams) {
		pid, fid := newPrj(d, alice, "Assets")
		sink(d.StoreAsset(dctx, alice, []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`)))
		sink(d.StoreAsset(dctx, alice, []byte(`<svg><script>x</script></svg>`)))
		sink(d.StoreAsset(dctx, alice, jpegBytes))
		sink(d.StoreAsset(dctx, alice, pngBytes))
		sink(d.StoreAsset(dctx, alice, pngBytes))
		sink(d.StoreAsset(dctx, alice, []byte("not an image")))
		sink(d.SetThumbnail(dctx, alice, fid, pngBytes))
		sink(d.SetThumbnail(dctx, alice, fid, pngBytes))
		sink(d.Thumbnail(dctx, alice, "", fid))
		sink(d.ExportFile(dctx, alice, "", fid))
		sink(d.ExportProject(dctx, alice, "", pid))
		sink(d.SaveFile(dctx, alice, fid, sp(" "), docOf("a"), ifMatch(2)))
		sink(d.SaveShare(dctx, "nope", fid, sp(" "), docOf("a"), ifMatch(2)))
		_, tok, _ := d.CreateShare(dctx, alice, pid, NewShare{Role: diagram.RoleEditor})
		sink(d.SaveShare(dctx, tok, fid, sp(" "), docOf("a"), ifMatch(2)))
		sink(d.SaveShare(dctx, tok, "file_missing", nil, docOf("a"), ifMatch(2)))
		sink(d.ShareFile(dctx, tok, "file_missing"))
	})
}
