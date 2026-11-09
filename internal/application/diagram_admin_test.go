package application

// Admin, publishing, gallery and diagram-report use cases on the in-memory
// repositories.

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram/diagramtest"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/report"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/user"
)

// adminWorld is a Diagrams with a gallery, an authorizer holding perms for
// the caller and an audit log.
func adminWorld(t *testing.T, caller int, perms ...string) (*Diagrams, *diagramtest.Memory, *fakeAudit) {
	t.Helper()
	d, mem := newDiagrams(t)
	p := map[string]bool{}
	for _, c := range perms {
		p[c] = true
	}
	audit := &fakeAudit{}
	d.Templates = diagramtest.NewTemplates(mem)
	d.Authz, d.Audit = fakeAuthz{caller: caller, perms: p}, audit
	users := diagramtest.NewUsers()
	users.Add(alice, "alice", "alice@example.com", "Alice")
	users.Add(bob, "bob", "bob@example.com", "")
	d.Users = users
	mem.SetUser(alice, "alice")
	mem.SetUser(bob, "bob")
	return d, mem, audit
}

func TestAdminPermissions(t *testing.T) {
	cases := []struct {
		name  string
		perms []string
		call  func(d *Diagrams) error
		ok    bool
	}{
		{"stats need read", nil, func(d *Diagrams) error { _, err := d.AdminStats(dctx); return err }, false},
		{"stats with read", []string{"diagrams.read"}, func(d *Diagrams) error { _, err := d.AdminStats(dctx); return err }, true},
		{"manage implies read", []string{"diagrams.manage"}, func(d *Diagrams) error { _, err := d.AdminStats(dctx); return err }, true},
		{"templates.manage is not read", []string{"templates.manage"}, func(d *Diagrams) error { _, err := d.AdminStats(dctx); return err }, false},
		{"delete needs manage", []string{"diagrams.read"}, func(d *Diagrams) error { return d.AdminDeleteProject(dctx, 9, "prj_x") }, false},
		{"revoke needs manage", []string{"diagrams.read"}, func(d *Diagrams) error { return d.AdminRevokeShare(dctx, 9, "shr_x") }, false},
		{"templates need templates.manage", []string{"diagrams.manage"}, func(d *Diagrams) error { _, err := d.AdminTemplates(dctx); return err }, false},
		{"templates listed", []string{"templates.manage"}, func(d *Diagrams) error { _, err := d.AdminTemplates(dctx); return err }, true},
	}
	for _, c := range cases {
		d, _, _ := adminWorld(t, 9, c.perms...)
		err := c.call(d)
		if c.ok && err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
		if !c.ok && !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("%s: err = %v, want forbidden", c.name, err)
		}
	}
	d, _, _ := adminWorld(t, 0, "diagrams.read")
	if _, err := d.AdminStats(dctx); !errors.Is(err, domain.ErrUnauthorized) {
		t.Errorf("anonymous: %v", err)
	}
	d.Authz = nil
	if _, err := d.AdminStats(dctx); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("no authorizer must deny: %v", err)
	}
}

func TestAdminStatsAndProjects(t *testing.T) {
	d, _, _ := adminWorld(t, 9, "diagrams.read")
	a := project(t, d, alice, "Payments")
	project(t, d, bob, "Search")
	project(t, d, alice, "Payroll")
	if _, _, err := d.CreateFile(dctx, alice, a.Project.ID, NewDiagramFile{Name: "Extra", Kind: "erd", Data: docOf("n")}); err != nil {
		t.Fatal(err)
	}

	st := must1[diagram.AdminStats](t)(d.AdminStats(dctx))
	if st.Projects != 3 || st.Files != 4 || st.FilesByKind["erd"] != 1 {
		t.Fatalf("stats = %+v", st)
	}
	if st.StorageBytes == 0 {
		t.Error("no storage counted")
	}
	if st.ActiveEditors7d != 2 {
		t.Errorf("active editors = %d, want alice and bob", st.ActiveEditors7d)
	}

	// search and owner filters; newest update first
	page := must1[AdminProjectPage](t)(d.AdminProjects(dctx, "pay", "", domain.NewPaging(1, 10)))
	if len(page.Items) != 2 || page.HasMore {
		t.Fatalf("pay = %+v", page)
	}
	if page.Items[0].UpdatedAt.Before(page.Items[1].UpdatedAt) {
		t.Error("not newest first")
	}
	page = must1[AdminProjectPage](t)(d.AdminProjects(dctx, "", "BOB", domain.NewPaging(1, 10)))
	if len(page.Items) != 1 || page.Items[0].Name != "Search" || page.Items[0].Owner.Username != "bob" {
		t.Fatalf("owner filter = %+v", page)
	}

	// cursor paging
	p1 := must1[AdminProjectPage](t)(d.AdminProjects(dctx, "", "", domain.Paging{Page: 1, Limit: 2}))
	if len(p1.Items) != 2 || !p1.HasMore || p1.NextCursor == "" {
		t.Fatalf("page 1 = %+v", p1)
	}
	p2 := must1[AdminProjectPage](t)(d.AdminProjects(dctx, "", "", domain.Paging{Page: 1, Limit: 2, Cursor: p1.NextCursor}))
	if len(p2.Items) != 1 || p2.HasMore || p2.Items[0].ID == p1.Items[0].ID || p2.Items[0].ID == p1.Items[1].ID {
		t.Fatalf("page 2 = %+v", p2)
	}
	if _, err := d.AdminProjects(dctx, "", "", domain.Paging{Limit: 2, Cursor: "garbage"}); err == nil {
		t.Error("bad cursor accepted")
	}
}

func TestAdminProjectDetailAndShares(t *testing.T) {
	d, mem, audit := adminWorld(t, 9, "diagrams.manage")
	p := project(t, d, alice, "Payments")
	mem.AddMember(p.Project.ID, bob, diagram.RoleEditor)
	link, token, err := d.CreateShare(dctx, alice, p.Project.ID, NewShare{Role: diagram.RoleViewer})
	if err != nil {
		t.Fatal(err)
	}

	det := must1[diagram.AdminProjectDetail](t)(d.AdminProject(dctx, p.Project.ID))
	if det.Owner.Username != "alice" || !det.Shared || len(det.FileList) != 1 || len(det.Members) != 1 || det.Members[0].User.Username != "bob" ||
		len(det.Shares) != 1 || det.Shares[0].CreatedBy != "alice" {
		t.Fatalf("detail = %+v", det)
	}
	if _, err := d.AdminProject(dctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown project: %v", err)
	}

	// revoke: idempotent, the link stops working, the row stays listed
	for range 2 {
		if err := d.AdminRevokeShare(dctx, 9, link.ID); err != nil {
			t.Fatal(err)
		}
	}
	det = must1[diagram.AdminProjectDetail](t)(d.AdminProject(dctx, p.Project.ID))
	if det.Shared || det.Shares[0].RevokedAt == nil {
		t.Errorf("after revoke: %+v", det)
	}
	if _, err := d.OpenShare(dctx, token); err == nil {
		t.Error("revoked link still opens")
	}
	if err := d.AdminRevokeShare(dctx, 9, "shr_unknown"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown share: %v", err)
	}

	// delete: any project, audited, gone for good
	if err := d.AdminDeleteProject(dctx, 9, p.Project.ID); err != nil {
		t.Fatal(err)
	}
	if err := d.AdminDeleteProject(dctx, 9, p.Project.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("second delete: %v", err)
	}
	if _, err := d.Repo.File(dctx, p.File.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Error("file survived its project")
	}
	if !slices.Equal(audit.actions, []string{"diagram.share.revoke", "diagram.share.revoke", "diagram.project.delete"}) {
		t.Errorf("audit = %v", audit.actions)
	}
}

func TestTemplateFileNeedsRead(t *testing.T) {
	d, _, _ := adminWorld(t, 9, "templates.manage")
	p := project(t, d, alice, "Source")
	if _, err := d.CreateTemplate(dctx, 9, NewTemplate{Title: "T", Kind: "system", FileID: p.File.ID}); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("template of a file the actor cannot read: %v", err)
	}
}

func TestTemplatesCRUDAndReorder(t *testing.T) {
	d, _, audit := adminWorld(t, 9, "templates.manage", "diagrams.read")
	p := project(t, d, alice, "Source")
	f := p.File.ID

	if _, err := d.CreateTemplate(dctx, 9, NewTemplate{Title: "T", Kind: "system", FileID: "fil_missing"}); !isValidation(err) {
		t.Errorf("missing file: %v", err)
	}
	for _, bad := range []NewTemplate{
		{Title: "", Kind: "system", FileID: f},
		{Title: strings.Repeat("x", 121), Kind: "system", FileID: f},
		{Title: "T", Kind: "other", FileID: f},
		{Title: "T", Kind: "db", FileID: f, Description: strings.Repeat("d", 501)},
		{Title: "T", Kind: "db", FileID: f, Position: -1},
	} {
		if _, err := d.CreateTemplate(dctx, 9, bad); !isValidation(err) {
			t.Errorf("%+v accepted: %v", bad, err)
		}
	}

	var ids []string
	for i, title := range []string{"One", "Two", "Three"} {
		tp := must1[diagram.Template](t)(d.CreateTemplate(dctx, 9, NewTemplate{Title: title, Kind: "ood", FileID: f, Position: i}))
		ids = append(ids, tp.ID)
	}
	list := must1[[]diagram.Template](t)(d.AdminTemplates(dctx))
	if len(list) != 3 || list[0].Title != "One" || list[2].Title != "Three" {
		t.Fatalf("list = %+v", list)
	}

	// PATCH: partial, validated
	pub := true
	tp := must1[diagram.Template](t)(d.UpdateTemplate(dctx, 9, ids[1], TemplatePatch{Published: &pub}))
	if !tp.Published || tp.Title != "Two" {
		t.Errorf("patched = %+v", tp)
	}
	empty := ""
	if _, err := d.UpdateTemplate(dctx, 9, ids[1], TemplatePatch{Title: &empty}); !isValidation(err) {
		t.Errorf("empty title: %v", err)
	}
	if _, err := d.UpdateTemplate(dctx, 9, "tpl_nope", TemplatePatch{Published: &pub}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown: %v", err)
	}

	// reorder is atomic: an unknown id changes nothing
	if _, err := d.ReorderTemplates(dctx, 9, []string{ids[2], "tpl_nope"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("reorder unknown: %v", err)
	}
	if list = must1[[]diagram.Template](t)(d.AdminTemplates(dctx)); list[0].Title != "One" {
		t.Error("failed reorder changed the order")
	}
	list = must1[[]diagram.Template](t)(d.ReorderTemplates(dctx, 9, []string{ids[2], ids[0]}))
	if got := []string{list[0].Title, list[1].Title, list[2].Title}; !slices.Equal(got, []string{"Three", "One", "Two"}) {
		t.Errorf("reordered = %v", got)
	}
	if list[0].Position != 0 || list[2].Position != 2 {
		t.Errorf("positions = %d %d %d", list[0].Position, list[1].Position, list[2].Position)
	}
	if _, err := d.ReorderTemplates(dctx, 9, nil); !isValidation(err) {
		t.Errorf("empty reorder: %v", err)
	}

	// the gallery shows only published templates, in order
	gal := must1[[]diagram.Template](t)(d.PublicTemplates(dctx))
	if len(gal) != 1 || gal[0].Title != "Two" {
		t.Errorf("gallery = %+v", gal)
	}

	// delete leaves the file alone
	if err := d.DeleteTemplate(dctx, 9, ids[0]); err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteTemplate(dctx, 9, ids[0]); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("second delete: %v", err)
	}
	if _, err := d.Repo.File(dctx, f); err != nil {
		t.Errorf("source file: %v", err)
	}
	if !slices.Contains(audit.actions, "diagram.template.reorder") || !slices.Contains(audit.actions, "diagram.template.delete") {
		t.Errorf("audit = %v", audit.actions)
	}

	// deleting the source file takes its template along
	if err := d.Repo.DeleteFile(dctx, f); err != nil {
		t.Fatal(err)
	}
	if list = must1[[]diagram.Template](t)(d.AdminTemplates(dctx)); len(list) != 0 {
		t.Errorf("templates of a deleted file: %+v", list)
	}
}

func TestForkTemplate(t *testing.T) {
	d, mem, _ := adminWorld(t, 9, "templates.manage", "diagrams.read")
	src := must1[DiagramProject](t)(d.CreateProject(dctx, alice, NewDiagramProject{Name: "Source",
		File: &NewDiagramFile{Name: "Arch", Kind: "uml", Data: docOf("a", "b")}}))
	thumb := strings.Repeat("a", 64)
	if err := d.Sharing.SetThumbnail(dctx, src.File.ID, thumb); err != nil {
		t.Fatal(err)
	}
	tp := must1[diagram.Template](t)(d.CreateTemplate(dctx, 9, NewTemplate{Title: "Starter", Kind: "system", FileID: src.File.ID}))

	// a draft template cannot be forked
	if _, err := d.ForkTemplate(dctx, bob, tp.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("draft fork: %v", err)
	}
	yes := true
	must1[diagram.Template](t)(d.UpdateTemplate(dctx, 9, tp.ID, TemplatePatch{Published: &yes}))

	res := must1[DiagramProject](t)(d.ForkTemplate(dctx, bob, tp.ID))
	if res.Project.OwnerID != bob || res.Project.Name != "Starter" || res.Project.Role != diagram.RoleOwner {
		t.Errorf("project = %+v", res.Project)
	}
	if res.File.ID == src.File.ID || res.File.ProjectID != res.Project.ID || res.File.Kind != "uml" || res.File.Version != 1 {
		t.Errorf("file = %+v", res.File.File)
	}
	if nodeCount(t, res.File.Data) != 2 {
		t.Error("document not copied")
	}
	if res.File.ThumbnailHash == nil || *res.File.ThumbnailHash != thumb {
		t.Errorf("thumbnail = %v", res.File.ThumbnailHash)
	}
	// the copy is private and independent of the source
	if l := must1[diagram.Listing](t)(mem.Listing(dctx, res.File.ID)); l.Visibility != diagram.VisPrivate {
		t.Errorf("visibility = %q", l.Visibility)
	}
	if _, err := d.GetFile(dctx, alice, res.File.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("alice sees bob's fork: %v", err)
	}
	other := must1[DiagramProject](t)(d.ForkTemplate(dctx, bob, tp.ID))
	if other.Project.ID == res.Project.ID {
		t.Error("second fork reused the project")
	}
	if _, err := d.ForkTemplate(dctx, bob, "tpl_nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown template: %v", err)
	}
}

func TestPublishFile(t *testing.T) {
	d, _, _ := adminWorld(t, 9)
	p := project(t, d, alice, "Mine")
	f := p.File.ID
	req := PublishRequest{Visibility: diagram.VisPublic, Title: "Checkout flow", Description: "How we pay", Tags: []string{" Payments ", "payments", "UML"}}

	// only the project's owner publishes; others do not even learn it exists
	d.Repo.(*diagramtest.Memory).AddMember(p.Project.ID, bob, diagram.RoleEditor)
	if _, err := d.PublishFile(dctx, bob, f, req); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("editor publish: %v", err)
	}
	if _, err := d.PublishFile(dctx, eve, f, req); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("stranger publish: %v", err)
	}
	for _, bad := range []PublishRequest{
		{Visibility: "private"}, {Visibility: ""}, {Visibility: diagram.VisPublic, Title: strings.Repeat("t", 121)},
		{Visibility: diagram.VisPublic, Description: strings.Repeat("d", 501)},
		{Visibility: diagram.VisPublic, Tags: []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11"}},
		{Visibility: diagram.VisPublic, Tags: []string{strings.Repeat("x", 41)}},
	} {
		if _, err := d.PublishFile(dctx, alice, f, bad); !isValidation(err) {
			t.Errorf("%+v accepted: %v", bad, err)
		}
	}

	l := must1[diagram.Listing](t)(d.PublishFile(dctx, alice, f, req))
	if l.Visibility != diagram.VisPublic || !diagram.ValidSlug(l.Slug) || !strings.HasPrefix(l.Slug, "checkout-flow-") ||
		!slices.Equal(l.Tags, []string{"payments", "uml"}) || l.PublishedAt.IsZero() || l.AuthorUsername != "alice" {
		t.Fatalf("listing = %+v", l)
	}

	// republishing keeps slug and date; the title can change
	req.Title, req.Visibility = "Renamed", diagram.VisUnlisted
	l2 := must1[diagram.Listing](t)(d.PublishFile(dctx, alice, f, req))
	if l2.Slug != l.Slug || !l2.PublishedAt.Equal(l.PublishedAt) || l2.Title != "Renamed" || l2.Visibility != diagram.VisUnlisted {
		t.Errorf("republish = %+v", l2)
	}
	// an untitled publish takes the file's name
	l3 := must1[diagram.Listing](t)(d.PublishFile(dctx, alice, f, PublishRequest{Visibility: diagram.VisPublic}))
	if l3.Title != p.File.Name {
		t.Errorf("default title = %q", l3.Title)
	}

	// public reads: the document by slug, listings only for public files
	doc := must1[diagram.PublicDoc](t)(d.PublicDiagram(dctx, l.Slug))
	if doc.FileID != f || len(doc.Data) == 0 {
		t.Errorf("public doc = %+v", doc)
	}
	must1[diagram.Listing](t)(d.PublishFile(dctx, alice, f, PublishRequest{Visibility: diagram.VisUnlisted}))
	if page := must1[PublicDiagramPage](t)(d.PublicDiagrams(dctx, "", "", domain.NewPaging(1, 10))); len(page.Items) != 0 {
		t.Errorf("unlisted is listed: %+v", page.Items)
	}
	if _, err := d.PublicDiagram(dctx, l.Slug); err != nil {
		t.Errorf("unlisted must open by slug: %v", err)
	}

	// unpublish: gone from the public, slug kept, idempotent
	for range 2 {
		if err := d.UnpublishFile(dctx, alice, f); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.PublicDiagram(dctx, l.Slug); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unpublished still opens: %v", err)
	}
	if err := d.UnpublishFile(dctx, bob, f); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("editor unpublish: %v", err)
	}
	back := must1[diagram.Listing](t)(d.PublishFile(dctx, alice, f, PublishRequest{Visibility: diagram.VisPublic}))
	if back.Slug != l.Slug {
		t.Errorf("slug changed from %q to %q", l.Slug, back.Slug)
	}
	cur := must1[diagram.Listing](t)(d.FilePublication(dctx, bob, f))
	if cur.Slug != l.Slug {
		t.Errorf("publication read: %+v", cur)
	}
	if _, err := d.FilePublication(dctx, eve, f); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("stranger reads publication: %v", err)
	}

	// deleting the file deletes the page
	if err := d.DeleteFile(dctx, alice, f); err != nil {
		t.Fatal(err)
	}
	if _, err := d.PublicDiagram(dctx, l.Slug); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("deleted file still opens: %v", err)
	}
	if _, err := d.PublicDiagram(dctx, "../etc/passwd"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("bad slug: %v", err)
	}
}

func TestPublicListings(t *testing.T) {
	d, _, _ := adminWorld(t, 9)
	mk := func(owner int, name string, vis diagram.Visibility, tags ...string) string {
		p := project(t, d, owner, name)
		must1[diagram.Listing](t)(d.PublishFile(dctx, owner, p.File.ID, PublishRequest{Visibility: vis, Title: name, Description: name + " notes", Tags: tags}))
		return p.File.ID
	}
	mk(alice, "Payments", diagram.VisPublic, "uml")
	mk(alice, "Search", diagram.VisPublic, "erd")
	mk(bob, "Billing_100%", diagram.VisPublic, "uml")
	mk(bob, "Hidden", diagram.VisUnlisted, "uml")
	project(t, d, bob, "Private") // never published

	all := must1[PublicDiagramPage](t)(d.PublicDiagrams(dctx, "", "", domain.NewPaging(1, 10)))
	if all.Total != 3 || len(all.Items) != 3 || all.Items[0].Title != "Billing_100%" {
		t.Fatalf("all = %+v", all)
	}
	if got := must1[PublicDiagramPage](t)(d.PublicDiagrams(dctx, "", "UML", domain.NewPaging(1, 10))); got.Total != 2 {
		t.Errorf("tag uml = %d", got.Total)
	}
	if got := must1[PublicDiagramPage](t)(d.PublicDiagrams(dctx, "search", "", domain.NewPaging(1, 10))); got.Total != 1 {
		t.Errorf("q search = %d", got.Total)
	}
	if got := must1[PublicDiagramPage](t)(d.PublicDiagrams(dctx, "", "", domain.NewPaging(2, 2))); len(got.Items) != 1 || got.Total != 3 {
		t.Errorf("page 2 = %+v", got)
	}
	if _, err := d.PublicDiagrams(dctx, strings.Repeat("q", 101), "", domain.NewPaging(1, 10)); !isValidation(err) {
		t.Errorf("long q: %v", err)
	}

	mine := must1[PublicDiagramPage](t)(d.UserDiagrams(dctx, "ALICE", domain.NewPaging(1, 10)))
	if mine.Total != 2 {
		t.Errorf("alice = %+v", mine)
	}
	bobs := must1[PublicDiagramPage](t)(d.UserDiagrams(dctx, "bob", domain.NewPaging(1, 10)))
	if bobs.Total != 1 {
		t.Errorf("bob (public only) = %+v", bobs)
	}
	if _, err := d.UserDiagrams(dctx, "nobody", domain.NewPaging(1, 10)); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown user: %v", err)
	}
	// an e-mail address is no username
	if _, err := d.UserDiagrams(dctx, "alice@example.com", domain.NewPaging(1, 10)); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("email lookup: %v", err)
	}
}

func TestEmbeddedDiagrams(t *testing.T) {
	d, _, _ := adminWorld(t, 9)
	pub := project(t, d, alice, "Pub")
	unl := project(t, d, alice, "Unl")
	priv := project(t, d, alice, "Priv")
	lp := must1[diagram.Listing](t)(d.PublishFile(dctx, alice, pub.File.ID, PublishRequest{Visibility: diagram.VisPublic, Title: "Pub"}))
	must1[diagram.Listing](t)(d.PublishFile(dctx, alice, unl.File.ID, PublishRequest{Visibility: diagram.VisUnlisted, Title: "Unl"}))

	got := d.EmbeddedDiagrams(dctx, []string{pub.File.ID, lp.Slug, unl.File.ID, priv.File.ID, "fil_missing"})
	if len(got) != 3 || got[pub.File.ID].Slug != lp.Slug || got[lp.Slug].FileID != pub.File.ID || got[unl.File.ID].Title != "Unl" {
		t.Errorf("embedded = %+v", got)
	}
	if _, ok := got[priv.File.ID]; ok {
		t.Error("private file embeddable")
	}
	must1[any](t)(nil, d.UnpublishFile(dctx, alice, pub.File.ID))
	if got = d.EmbeddedDiagrams(dctx, []string{pub.File.ID}); len(got) != 0 {
		t.Errorf("unpublished still embeddable: %+v", got)
	}
}

// Reports on diagram files

type fakeReports struct {
	rows []report.Report
}

func (f *fakeReports) Create(_ context.Context, r report.Report) (report.Report, error) {
	for _, o := range f.rows {
		if o.ReporterID == r.ReporterID && o.TargetType == r.TargetType && o.TargetID == r.TargetID &&
			o.TargetFileID == r.TargetFileID && o.Status == report.StatusOpen {
			return r, domain.ErrConflict
		}
	}
	r.ID = len(f.rows) + 1
	f.rows = append(f.rows, r)
	return r, nil
}

func (f *fakeReports) Get(_ context.Context, id int) (report.Report, error) {
	if id < 1 || id > len(f.rows) {
		return report.Report{}, domain.ErrNotFound
	}
	return f.rows[id-1], nil
}

func (f *fakeReports) List(context.Context, report.ReportStatus, domain.Paging) (report.Page, error) {
	return report.Page{Items: f.rows}, nil
}

func (f *fakeReports) Decide(_ context.Context, id int, s report.ReportStatus, by int) (report.Report, error) {
	r := f.rows[id-1]
	for i, o := range f.rows {
		if o.TargetType == r.TargetType && o.TargetID == r.TargetID && o.TargetFileID == r.TargetFileID && o.Status == report.StatusOpen {
			f.rows[i].Status, f.rows[i].ResolvedBy = s, by
		}
	}
	return f.rows[id-1], nil
}

func TestDiagramReports(t *testing.T) {
	d, _, audit := adminWorld(t, bob, "report.create", "report.moderate", "diagrams.manage")
	pub := project(t, d, alice, "Pub")
	priv := project(t, d, alice, "Priv")
	must1[diagram.Listing](t)(d.PublishFile(dctx, alice, pub.File.ID, PublishRequest{Visibility: diagram.VisPublic}))

	reports := &fakeReports{}
	blog := NewBlog(Repos{Reports: reports}, Ports{Authz: d.Authz, Audit: audit, Diagrams: d})
	bobU, aliceU := user.User{ID: bob, Username: "bob"}, user.User{ID: alice, Username: "alice"}

	// a stranger reports a published file, but cannot see a private one
	r := must1[report.Report](t)(blog.ReportDiagram(dctx, bobU, pub.File.ID, report.ReasonSpam, ""))
	if r.TargetType != report.TargetDiagram || r.TargetID != 0 || r.TargetFileID != pub.File.ID {
		t.Errorf("report = %+v", r)
	}
	if _, err := blog.ReportDiagram(dctx, bobU, priv.File.ID, report.ReasonSpam, ""); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("private file: %v", err)
	}
	if _, err := blog.ReportDiagram(dctx, bobU, "fil_missing", report.ReasonSpam, ""); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing file: %v", err)
	}
	if _, err := blog.ReportDiagram(dctx, bobU, pub.File.ID, report.ReasonSpam, ""); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("second open report: %v", err)
	}
	if _, err := blog.ReportDiagram(dctx, bobU, pub.File.ID, report.ReasonOther, " "); !isValidation(err) {
		t.Errorf("reason other without note: %v", err)
	}
	if _, err := blog.ReportDiagram(dctx, bobU, "", report.ReasonSpam, ""); !isValidation(err) {
		t.Errorf("empty file id: %v", err)
	}
	// no reporting your own work
	if _, err := blog.ReportDiagram(dctx, aliceU, pub.File.ID, report.ReasonSpam, ""); !isValidation(err) {
		t.Errorf("own file: %v", err)
	}
	// a member sees a private file, so may report it
	d.Repo.(*diagramtest.Memory).AddMember(priv.Project.ID, bob, diagram.RoleViewer)
	must1[report.Report](t)(blog.ReportDiagram(dctx, bobU, priv.File.ID, report.ReasonAbuse, ""))

	// resolving with remove_content deletes the file and closes the report
	got := must1[report.Report](t)(blog.DecideReport(dctx, bobU, r.ID, report.StatusResolved, true))
	if got.Status != report.StatusResolved {
		t.Errorf("decided = %+v", got)
	}
	if _, err := d.Repo.File(dctx, pub.File.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("reported file survived: %v", err)
	}
	if !slices.Contains(audit.actions, "diagram.file.delete") || !slices.Contains(audit.actions, "report.resolved") {
		t.Errorf("audit = %v", audit.actions)
	}

	// removal needs diagrams.manage on top of report.moderate
	weakAuthz := fakeAuthz{caller: bob, perms: map[string]bool{"report.moderate": true}}
	d.Authz = weakAuthz
	weak := NewBlog(Repos{Reports: reports}, Ports{Authz: weakAuthz, Audit: audit, Diagrams: d})
	if _, err := weak.DecideReport(dctx, bobU, 2, report.StatusResolved, true); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("remove without diagrams.manage: %v", err)
	}
	if _, err := d.Repo.File(dctx, priv.File.ID); err != nil {
		t.Errorf("file removed anyway: %v", err)
	}
	// dismissing needs no more than report.moderate
	if _, err := weak.DecideReport(dctx, bobU, 2, report.StatusDismissed, false); err != nil {
		t.Errorf("dismiss: %v", err)
	}
}
