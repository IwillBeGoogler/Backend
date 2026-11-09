package postgres_test

// The blog side of diagrams against a real Postgres: published pages, the
// admin views, the template gallery and reports on diagram files, with the
// cascades that keep them from outliving their file. Skipped with -short or
// without Docker.

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/report"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/postgres"
)

func TestDiagramPublishRepo(t *testing.T) {
	db := connect(t)
	ctx := context.Background()
	users := postgres.NewUserRepo(db)
	ali, err := users.Create(ctx, "dgp_ali", "dgp_ali@example.com")
	must(t, err)
	repo := postgres.NewDiagramRepo(db)
	d := application.NewDiagrams(repo, repo)
	d.Users = users

	a, err := d.CreateProject(ctx, ali.ID, application.NewDiagramProject{Name: "A", Data: dgDoc("a")})
	must(t, err)
	b, err := d.CreateProject(ctx, ali.ID, application.NewDiagramProject{Name: "B_100%", Data: dgDoc("b")})
	must(t, err)

	// a fresh file is private with no slug
	l, err := repo.Listing(ctx, a.File.ID)
	must(t, err)
	if l.Visibility != diagram.VisPrivate || l.Slug != "" || !l.PublishedAt.IsZero() || len(l.Tags) != 0 || l.AuthorUsername != "dgp_ali" {
		t.Fatalf("fresh listing = %+v", l)
	}
	if _, err := repo.Listing(ctx, "fil_missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing listing: %v", err)
	}

	at := time.Now().Truncate(time.Microsecond)
	pa := diagram.Publication{Visibility: diagram.VisPublic, Slug: "alpha-1a2b3c4d", Title: "Alpha", Description: "first", Tags: []string{"uml", "erd"}, PublishedAt: at}
	must(t, repo.SetPublication(ctx, a.File.ID, pa))
	pb := diagram.Publication{Visibility: diagram.VisUnlisted, Slug: "alpha-1a2b3c4d", Title: "Dup", PublishedAt: at}
	if err := repo.SetPublication(ctx, b.File.ID, pb); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate slug: %v", err)
	}
	pb.Slug = "beta-5e6f7a8b"
	must(t, repo.SetPublication(ctx, b.File.ID, pb))
	if err := repo.SetPublication(ctx, "fil_missing", pa); !errors.Is(err, domain.ErrNotFound) && !errors.Is(err, domain.ErrConflict) {
		t.Errorf("missing file: %v", err)
	}

	doc, err := repo.PublicBySlug(ctx, "alpha-1a2b3c4d")
	must(t, err)
	if doc.FileID != a.File.ID || doc.Title != "Alpha" || !slices.Equal(doc.Tags, []string{"uml", "erd"}) || !doc.PublishedAt.Equal(at) ||
		doc.AuthorID != ali.ID || len(doc.Data) == 0 {
		t.Errorf("public doc = %+v", doc)
	}
	if _, err := repo.PublicBySlug(ctx, "beta-5e6f7a8b"); err != nil {
		t.Errorf("unlisted must open: %v", err)
	}

	// only public files are listed; filters; literal % and _ in the search
	items, total, err := repo.ListPublic(ctx, diagram.PublicQuery{Page: 1, Limit: 10})
	must(t, err)
	if total != 1 || len(items) != 1 || items[0].FileID != a.File.ID {
		t.Fatalf("list = %+v (%d)", items, total)
	}
	for q, want := range map[string]diagram.PublicQuery{
		"tag hit":  {Tag: "erd"},
		"q title":  {Q: "ALPH"},
		"q desc":   {Q: "irst"},
		"author":   {AuthorID: ali.ID},
		"tag miss": {Tag: "board"},
		"q miss":   {Q: "zzz"},
		"wildcard": {Q: "%"},
	} {
		want.Page, want.Limit = 1, 10
		_, n, err := repo.ListPublic(ctx, want)
		must(t, err)
		if hit := q == "tag hit" || q == "q title" || q == "q desc" || q == "author"; hit != (n == 1) {
			t.Errorf("%s: total = %d", q, n)
		}
	}

	refs, err := repo.ReferencedPublic(ctx, []string{a.File.ID, "beta-5e6f7a8b", "fil_missing"})
	must(t, err)
	if len(refs) != 2 || refs[a.File.ID].Slug != "alpha-1a2b3c4d" || refs["beta-5e6f7a8b"].FileID != b.File.ID {
		t.Errorf("referenced = %+v", refs)
	}

	// clear keeps the slug; the page closes
	must(t, repo.ClearPublication(ctx, a.File.ID))
	if _, err := repo.PublicBySlug(ctx, "alpha-1a2b3c4d"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("cleared page opens: %v", err)
	}
	if l, _ = repo.Listing(ctx, a.File.ID); l.Slug != "alpha-1a2b3c4d" || l.Visibility != diagram.VisPrivate || !l.PublishedAt.IsZero() {
		t.Errorf("cleared = %+v", l)
	}
	if err := repo.ClearPublication(ctx, "fil_missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("clear missing: %v", err)
	}

	// a deleted account's diagrams leave the public
	must(t, repo.SetPublication(ctx, a.File.ID, pa))
	if _, err := db.Exec(ctx, `UPDATE users SET deleted_at = now() WHERE id = $1`, ali.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.PublicBySlug(ctx, "alpha-1a2b3c4d"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("deleted author's page opens: %v", err)
	}
	if _, n, _ := repo.ListPublic(ctx, diagram.PublicQuery{Page: 1, Limit: 10}); n != 0 {
		t.Errorf("deleted author listed: %d", n)
	}

	// the page goes with the file
	must(t, repo.DeleteFile(ctx, b.File.ID))
	if _, err := repo.PublicBySlug(ctx, "beta-5e6f7a8b"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("deleted file's page opens: %v", err)
	}
}

func TestDiagramAdminRepo(t *testing.T) {
	db := connect(t)
	ctx := context.Background()
	users := postgres.NewUserRepo(db)
	ali, err := users.Create(ctx, "dga_ali", "dga_ali@example.com")
	must(t, err)
	vali, err := users.Create(ctx, "dga_vali", "dga_vali@example.com")
	must(t, err)
	repo := postgres.NewDiagramRepo(db)
	d := application.NewDiagrams(repo, repo)
	d.Users, d.Blobs = users, &blobStore{data: map[string][]byte{}}

	a, err := d.CreateProject(ctx, ali.ID, application.NewDiagramProject{Name: "Payments", Data: dgDoc("a")})
	must(t, err)
	_, err = d.CreateProject(ctx, vali.ID, application.NewDiagramProject{Name: "Pay_roll", Data: dgDoc("b")})
	must(t, err)
	_, err = d.AddMember(ctx, ali.ID, a.Project.ID, "dga_vali", diagram.RoleEditor)
	must(t, err)
	link, _, err := d.CreateShare(ctx, ali.ID, a.Project.ID, application.NewShare{Role: diagram.RoleViewer})
	must(t, err)

	st, err := repo.Stats(ctx, time.Now().Add(-time.Hour))
	must(t, err)
	if st.Projects < 2 || st.Files < 2 || st.FilesByKind["sdd"] < 2 || st.ActiveEditors7d < 1 || st.StorageBytes <= 0 {
		t.Errorf("stats = %+v", st)
	}
	if st, _ = repo.Stats(ctx, time.Now().Add(time.Hour)); st.ActiveEditors7d != 0 {
		t.Errorf("editors in the future = %d", st.ActiveEditors7d)
	}

	rows, err := repo.Projects(ctx, diagram.AdminProjectQuery{Search: "pay", Limit: 10})
	must(t, err)
	if len(rows) != 2 {
		t.Fatalf("pay = %+v", rows)
	}
	if rows, _ = repo.Projects(ctx, diagram.AdminProjectQuery{Search: "_", Limit: 10}); len(rows) != 1 || rows[0].Name != "Pay_roll" {
		t.Errorf("literal underscore = %+v", rows)
	}
	rows, _ = repo.Projects(ctx, diagram.AdminProjectQuery{Owner: "DGA_ALI", Limit: 10})
	if len(rows) != 1 || rows[0].ID != a.Project.ID || !rows[0].Shared || rows[0].Files != 1 || rows[0].Owner.Username != "dga_ali" {
		t.Errorf("owner = %+v", rows)
	}
	if rows, _ = repo.Projects(ctx, diagram.AdminProjectQuery{Search: "pay", Limit: 1}); len(rows) != 2 {
		t.Errorf("limit+1 rows expected, got %d", len(rows))
	}
	if rows, _ = repo.Projects(ctx, diagram.AdminProjectQuery{Search: "pay", Limit: 1, Offset: 1}); len(rows) != 1 {
		t.Errorf("offset = %d rows", len(rows))
	}

	det, err := repo.ProjectDetail(ctx, a.Project.ID)
	must(t, err)
	if len(det.FileList) != 1 || len(det.Members) != 1 || det.Members[0].User.Username != "dga_vali" || det.Members[0].Role != diagram.RoleEditor ||
		len(det.Shares) != 1 || det.Shares[0].CreatedBy != "dga_ali" {
		t.Errorf("detail = %+v", det)
	}
	if _, err := repo.ProjectDetail(ctx, "prj_missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing detail: %v", err)
	}

	// revoke keeps the first revocation time and works across projects
	at := time.Now().Truncate(time.Microsecond)
	pid, err := repo.RevokeShareByID(ctx, link.ID, at)
	must(t, err)
	if pid != a.Project.ID {
		t.Errorf("project = %q", pid)
	}
	_, err = repo.RevokeShareByID(ctx, link.ID, at.Add(time.Hour))
	must(t, err)
	det, _ = repo.ProjectDetail(ctx, a.Project.ID)
	if det.Shared || det.Shares[0].RevokedAt == nil || !det.Shares[0].RevokedAt.Equal(at) {
		t.Errorf("revoked = %+v", det.Shares[0])
	}
	if _, err := repo.RevokeShareByID(ctx, "shr_missing", at); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing share: %v", err)
	}
}

func TestDiagramTemplateRepo(t *testing.T) {
	db := connect(t)
	ctx := context.Background()
	users := postgres.NewUserRepo(db)
	ali, err := users.Create(ctx, "dgt_ali", "dgt_ali@example.com")
	must(t, err)
	repo := postgres.NewDiagramRepo(db)
	tpl := postgres.NewDiagramTemplateRepo(db)
	d := application.NewDiagrams(repo, repo)
	d.Users = users

	p, err := d.CreateProject(ctx, ali.ID, application.NewDiagramProject{Name: "Src", Data: dgDoc("a")})
	must(t, err)
	now := time.Now().Truncate(time.Microsecond)
	mk := func(id, title string, pos int, published bool) diagram.Template {
		return diagram.Template{ID: id, Title: title, Kind: "system", Description: "d", FileID: p.File.ID, Published: published,
			Position: pos, CreatedAt: now, UpdatedAt: now}
	}
	must(t, tpl.Create(ctx, mk("tpl_a", "Alpha", 2, true)))
	must(t, tpl.Create(ctx, mk("tpl_b", "beta", 1, false)))
	must(t, tpl.Create(ctx, mk("tpl_c", "Gamma", 1, true)))
	if err := tpl.Create(ctx, mk("tpl_a", "Dup", 0, true)); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("duplicate id: %v", err)
	}
	bad := mk("tpl_x", "X", 0, true)
	bad.FileID = "fil_missing"
	if err := tpl.Create(ctx, bad); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("missing file: %v", err)
	}

	ids := func(ts []diagram.Template) []string {
		var out []string
		for _, x := range ts {
			out = append(out, x.ID)
		}
		return out
	}
	all, err := tpl.List(ctx, false)
	must(t, err)
	if !slices.Equal(ids(all), []string{"tpl_b", "tpl_c", "tpl_a"}) || all[0].PageCount != 1 {
		t.Errorf("all = %v", ids(all))
	}
	pub, _ := tpl.List(ctx, true)
	if !slices.Equal(ids(pub), []string{"tpl_c", "tpl_a"}) {
		t.Errorf("published = %v", ids(pub))
	}

	got, err := tpl.Get(ctx, "tpl_a")
	must(t, err)
	got.Title, got.Published, got.UpdatedAt = "Alpha 2", false, now.Add(time.Minute)
	must(t, tpl.Update(ctx, got))
	if g, _ := tpl.Get(ctx, "tpl_a"); g.Title != "Alpha 2" || g.Published || !g.CreatedAt.Equal(now) {
		t.Errorf("updated = %+v", g)
	}
	if err := tpl.Update(ctx, mk("tpl_nope", "N", 0, true)); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("update missing: %v", err)
	}

	// reorder is all or nothing
	if err := tpl.Reorder(ctx, []string{"tpl_a", "tpl_nope"}, now); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("reorder unknown: %v", err)
	}
	if all, _ = tpl.List(ctx, false); !slices.Equal(ids(all), []string{"tpl_b", "tpl_c", "tpl_a"}) {
		t.Errorf("failed reorder changed %v", ids(all))
	}
	must(t, tpl.Reorder(ctx, []string{"tpl_c", "tpl_a", "tpl_c"}, now))
	all, _ = tpl.List(ctx, false)
	if !slices.Equal(ids(all), []string{"tpl_c", "tpl_a", "tpl_b"}) || all[0].Position != 0 || all[2].Position != 2 {
		t.Errorf("reordered = %+v", all)
	}

	// delete leaves the file; deleting the file takes the template
	must(t, tpl.Delete(ctx, "tpl_b"))
	if err := tpl.Delete(ctx, "tpl_b"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("second delete: %v", err)
	}
	if _, err := repo.File(ctx, p.File.ID); err != nil {
		t.Errorf("source file: %v", err)
	}
	must(t, repo.DeleteFile(ctx, p.File.ID))
	if all, _ = tpl.List(ctx, false); len(all) != 0 {
		t.Errorf("templates of a deleted file: %v", ids(all))
	}
}

func TestDiagramReportRepo(t *testing.T) {
	db := connect(t)
	ctx := context.Background()
	users := postgres.NewUserRepo(db)
	ali, err := users.Create(ctx, "dgr_ali", "dgr_ali@example.com")
	must(t, err)
	vali, err := users.Create(ctx, "dgr_vali", "dgr_vali@example.com")
	must(t, err)
	eve, err := users.Create(ctx, "dgr_eve", "dgr_eve@example.com")
	must(t, err)
	repo := postgres.NewDiagramRepo(db)
	d := application.NewDiagrams(repo, repo)
	d.Users = users
	reports := postgres.NewReportRepo(db)

	p1, err := d.CreateProject(ctx, ali.ID, application.NewDiagramProject{Name: "One", Data: dgDoc("a")})
	must(t, err)
	p2, err := d.CreateProject(ctx, ali.ID, application.NewDiagramProject{Name: "Two", Data: dgDoc("b")})
	must(t, err)

	file := func(reporter int, id string) report.Report {
		r, err := report.NewDiagram(reporter, id, report.ReasonSpam, "")
		must(t, err)
		return r
	}
	r1, err := reports.Create(ctx, file(vali.ID, p1.File.ID))
	must(t, err)
	if r1.TargetType != report.TargetDiagram || r1.TargetID != 0 || r1.TargetFileID != p1.File.ID || r1.TargetProjectID != p1.Project.ID || r1.Reports != 1 {
		t.Errorf("created = %+v", r1)
	}
	// one open report per reporter and file; other files and reporters are separate
	if _, err := reports.Create(ctx, file(vali.ID, p1.File.ID)); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("duplicate: %v", err)
	}
	if _, err := reports.Create(ctx, file(vali.ID, p2.File.ID)); err != nil {
		t.Errorf("other file: %v", err)
	}
	if r, err := reports.Create(ctx, file(eve.ID, p1.File.ID)); err != nil || r.Reports != 2 {
		t.Errorf("second reporter = %+v, %v", r, err)
	}

	// deciding one report closes every open report on that file only
	dec, err := reports.Decide(ctx, r1.ID, report.StatusResolved, ali.ID)
	must(t, err)
	if dec.Status != report.StatusResolved {
		t.Errorf("decided = %+v", dec)
	}
	page, err := reports.List(ctx, report.StatusOpen, domain.NewPaging(1, 50))
	must(t, err)
	open := 0
	for _, r := range page.Items {
		if r.TargetFileID == p1.File.ID {
			t.Errorf("report on the decided file is still open: %+v", r)
		}
		if r.TargetFileID == p2.File.ID {
			open++
		}
	}
	if open != 1 {
		t.Errorf("open reports on the other file = %d", open)
	}
	// reporting again after the decision is allowed
	if _, err := reports.Create(ctx, file(vali.ID, p1.File.ID)); err != nil {
		t.Errorf("report after decision: %v", err)
	}

	// the file is gone: the report stays, without a project
	must(t, repo.DeleteFile(ctx, p2.File.ID))
	page, _ = reports.List(ctx, report.StatusOpen, domain.NewPaging(1, 50))
	for _, r := range page.Items {
		if r.TargetFileID == p2.File.ID && r.TargetProjectID != "" {
			t.Errorf("project of a deleted file = %q", r.TargetProjectID)
		}
	}
}

func TestDiagramEngagementRepo(t *testing.T) {
	db := connect(t)
	ctx := context.Background()
	users := postgres.NewUserRepo(db)
	ali, err := users.Create(ctx, "dge_ali", "dge_ali@example.com")
	must(t, err)
	bob, err := users.Create(ctx, "dge_bob", "dge_bob@example.com")
	must(t, err)
	repo := postgres.NewDiagramRepo(db)
	d := application.NewDiagrams(repo, repo)
	d.Users = users
	a, err := d.CreateProject(ctx, ali.ID, application.NewDiagramProject{Name: "A", Data: dgDoc("a")})
	must(t, err)
	fid := a.File.ID

	// private files take no claps or bookmarks
	if _, _, _, err := repo.Clap(ctx, fid, bob.ID, 3); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("clap on private: %v", err)
	}
	if err := repo.Bookmark(ctx, fid, bob.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("bookmark on private: %v", err)
	}
	must(t, repo.SetPublication(ctx, fid, diagram.Publication{Visibility: diagram.VisUnlisted, Slug: "eng-1a2b3c4d", Title: "Eng", PublishedAt: time.Now()}))
	if l, err := repo.PublicListing(ctx, "eng-1a2b3c4d"); err != nil || l.FileID != fid {
		t.Fatalf("PublicListing = %+v, %v", l, err)
	}

	total, mine, created, err := repo.Clap(ctx, fid, bob.ID, 30)
	must(t, err)
	if total != 30 || mine != 30 || !created {
		t.Errorf("first clap = %d %d %v", total, mine, created)
	}
	total, mine, created, err = repo.Clap(ctx, fid, bob.ID, 30)
	must(t, err)
	if total != 50 || mine != 50 || created {
		t.Errorf("capped clap = %d %d %v", total, mine, created)
	}
	total, _, _, err = repo.Clap(ctx, fid, ali.ID, 2)
	must(t, err)
	if total != 52 {
		t.Errorf("total = %d", total)
	}
	if l, _ := repo.Listing(ctx, fid); l.Claps != 52 {
		t.Errorf("listing claps = %d", l.Claps)
	}

	must(t, repo.Bookmark(ctx, fid, bob.ID))
	must(t, repo.Bookmark(ctx, fid, bob.ID))
	if v, err := repo.Viewer(ctx, fid, bob.ID); err != nil || v.MyClaps != 50 || !v.Bookmarked {
		t.Errorf("viewer = %+v, %v", v, err)
	}
	items, n, err := repo.ListBookmarked(ctx, bob.ID, 1, 10)
	must(t, err)
	if n != 1 || len(items) != 1 || items[0].FileID != fid || items[0].Claps != 52 {
		t.Errorf("bookmarked = %+v (%d)", items, n)
	}
	must(t, repo.ClearPublication(ctx, fid))
	if _, n, _ := repo.ListBookmarked(ctx, bob.ID, 1, 10); n != 0 {
		t.Errorf("private file still listed: %d", n)
	}
	must(t, repo.Unbookmark(ctx, fid, bob.ID))
}
