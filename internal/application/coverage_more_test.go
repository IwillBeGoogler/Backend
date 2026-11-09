package application

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/comment"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/post"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/report"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/social"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/user"
)

type recNotices struct {
	DiagramNotices
	got []social.Notification
}

func (r *recNotices) NotifyDiagram(_ context.Context, n social.Notification, _ ...int) {
	r.got = append(r.got, n)
}

func TestDiagramEngagement(t *testing.T) {
	d, _, _ := adminWorld(t, 9)
	notes := &recNotices{}
	d.Notices = notes
	p := project(t, d, alice, "Mine")
	l := must1[diagram.Listing](t)(d.PublishFile(dctx, alice, p.File.ID, PublishRequest{Visibility: diagram.VisPublic, Title: "T"}))

	if _, err := d.ClapDiagram(dctx, bob, l.Slug, 0); err == nil {
		t.Error("zero claps accepted")
	}
	if _, err := d.ClapDiagram(dctx, bob, "nope", 1); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown slug: %v", err)
	}
	c := must1[DiagramClaps](t)(d.ClapDiagram(dctx, bob, l.Slug, 3))
	if c.Claps != 3 || c.MyClaps != 3 || len(notes.got) != 1 {
		t.Errorf("claps = %+v notes %d", c, len(notes.got))
	}
	d.ClapDiagram(dctx, bob, l.Slug, 1) // not the first clap: no second notice
	if len(notes.got) != 1 {
		t.Error("notified twice")
	}

	if err := d.BookmarkDiagram(dctx, bob, l.Slug); err != nil {
		t.Fatal(err)
	}
	if err := d.BookmarkDiagram(dctx, bob, "nope"); err == nil {
		t.Error("bookmarked unknown")
	}
	if v, err := d.DiagramViewer(dctx, bob, l.Slug); err != nil || !v.Bookmarked || v.MyClaps == 0 {
		t.Errorf("viewer = %+v %v", v, err)
	}
	if v, err := d.DiagramViewer(dctx, 0, l.Slug); err != nil || v.Bookmarked {
		t.Errorf("anonymous viewer = %+v %v", v, err)
	}
	if _, err := d.DiagramViewer(dctx, bob, "nope"); err == nil {
		t.Error("viewer on unknown")
	}
	page := must1[PublicDiagramPage](t)(d.BookmarkedDiagrams(dctx, bob, domain.Paging{Page: 1, Limit: 10}))
	if page.Total != 1 || len(page.Items) != 1 {
		t.Errorf("bookmarks = %+v", page)
	}
	if items, meta := page.Paginated(); items == nil || meta == nil {
		t.Error("Paginated")
	}
	if err := d.UnbookmarkDiagram(dctx, bob, l.Slug); err != nil {
		t.Fatal(err)
	}
	if err := d.UnbookmarkDiagram(dctx, bob, "nope"); err == nil {
		t.Error("unbookmark unknown")
	}

	d.Engage = nil
	if _, err := d.ClapDiagram(dctx, bob, l.Slug, 1); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("no engagement repo: %v", err)
	}
	if page, err := d.BookmarkedDiagrams(dctx, bob, domain.Paging{Page: 1, Limit: 10}); err != nil || page.Items == nil {
		t.Errorf("no repo page: %+v %v", page, err)
	}
}

func TestMCPAuditAndSummary(t *testing.T) {
	d, _ := newDiagrams(t)
	d.RecordMCP(dctx, diagram.MCPAudit{UserID: alice, Tool: "read_file", Status: diagram.AuditOK})
	d.RecordMCP(dctx, diagram.MCPAudit{UserID: alice, Tool: "add_nodes", Status: diagram.AuditOK})
	d.RecordMCP(dctx, diagram.MCPAudit{UserID: bob, Tool: "x", Status: diagram.AuditOK})
	log := must1[[]diagram.MCPAudit](t)(d.MCPAuditLog(dctx, alice, 10))
	if len(log) != 2 {
		t.Errorf("log = %d entries", len(log))
	}
	if _, err := d.MCPAuditLog(dctx, alice, 9999); err != nil {
		t.Error(err)
	}
	d.MCPAudit = nil
	d.RecordMCP(dctx, diagram.MCPAudit{}) // no repository: dropped
	if _, err := d.MCPAuditLog(dctx, alice, 5); err == nil {
		t.Error("log without repo")
	}
}

func TestSummarizeArgsShapes(t *testing.T) {
	long := make([]byte, 200)
	for i := range long {
		long[i] = 'x'
	}
	out := SummarizeArgs(map[string]any{
		"dsl": "secret", "s": string(long), "list": []any{1, 2, 3, 4, 5, 6, 7},
		"deep": map[string]any{"a": map[string]any{"b": map[string]any{"c": 1}}},
		"nest": []any{[]any{[]any{1}}},
	})
	for _, want := range []string{"<redacted>", "<200 chars>", "+2 more", "<object>", "<1 items>"} {
		if !contains(out, want) {
			t.Errorf("summary %q lacks %q", out, want)
		}
	}
	if SummarizeArgs(make(chan int)) != "" {
		t.Error("unencodable args")
	}
}

func TestStorageKeys(t *testing.T) {
	if ThumbKey("a/b.png") != "thumb/a/b.jpg" || ThumbKey("noext") != "thumb/noext.jpg" {
		t.Error("ThumbKey")
	}
	if VariantKey("k.png", 320) != "img/k-320.webp" {
		t.Error("VariantKey")
	}
	if NotificationsChannel(3) != "notifications:3" || CommentsChannel(4) != "comments:4" {
		t.Error("channels")
	}
}

func TestTokenListAndAdminHelpers(t *testing.T) {
	d, _, _ := adminWorld(t, 9, "diagrams.manage")
	secret, tok, err := d.CreateToken(dctx, alice, NewAccessToken{Name: "ci", Scopes: []string{diagram.Scopes[0]}})
	if err != nil || secret == "" {
		t.Fatal(err)
	}
	list := must1[[]diagram.AccessToken](t)(d.ListTokens(dctx, alice))
	if len(list) != 1 || list[0].ID != tok.ID {
		t.Errorf("tokens = %+v", list)
	}
	if _, err := d.AdminSchemaSQL(dctx, ""); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("no dumper: %v", err)
	}
	d.SchemaDump = func(_ context.Context, prefix string) (string, error) { return "CREATE TABLE " + prefix, nil }
	if s, err := d.AdminSchemaSQL(dctx, "t_"); err != nil || s != "CREATE TABLE t_" {
		t.Errorf("schema = %q %v", s, err)
	}
	page := must1[AdminProjectPage](t)(d.AdminProjects(dctx, "", "", domain.Paging{Limit: 10}))
	if items, meta := page.Paginated(); items == nil || meta == nil {
		t.Error("Paginated")
	}
	d.Tokens = nil
	if _, err := d.ListTokens(dctx, alice); err == nil {
		t.Error("tokens without repo")
	}
	d.Publish = nil
	if _, err := d.FilePublication(dctx, alice, "f"); err == nil {
		t.Error("publication without repo")
	}
	if got, _ := d.PublishedDiagrams(dctx, alice, 1, 10); got.Items == nil {
		t.Error("published without repo")
	}
	if got := d.EmbeddedDiagrams(dctx, []string{"a"}); len(got) != 0 {
		t.Error("embeds without repo")
	}
}

func TestPublishedListsAndEmbeds(t *testing.T) {
	d, _, _ := adminWorld(t, 9)
	p := project(t, d, alice, "Mine")
	l := must1[diagram.Listing](t)(d.PublishFile(dctx, alice, p.File.ID, PublishRequest{Visibility: diagram.VisPublic, Title: "T"}))
	own := must1[PublicDiagramPage](t)(d.PublishedDiagrams(dctx, alice, 1, 10))
	if own.Total != 1 {
		t.Errorf("own = %+v", own)
	}
	if got := d.EmbeddedDiagrams(dctx, []string{l.Slug, "ghost"}); len(got) != 1 || got[l.Slug].FileID != p.File.ID {
		t.Errorf("embeds = %+v", got)
	}
	if got := d.EmbeddedDiagrams(dctx, nil); len(got) != 0 {
		t.Error("empty refs")
	}
	long := make([]rune, 101)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := d.PublicDiagrams(dctx, string(long), "", domain.Paging{Page: 1, Limit: 10}); err == nil {
		t.Error("long query accepted")
	}
	if _, err := d.UserDiagrams(dctx, "alice", domain.Paging{Page: 1, Limit: 10}); err != nil {
		t.Error(err)
	}
	if _, err := d.UserDiagrams(dctx, "ghost", domain.Paging{Page: 1, Limit: 10}); err == nil {
		t.Error("unknown user")
	}
	d.Users = nil
	if _, err := d.UserDiagrams(dctx, "alice", domain.Paging{Page: 1, Limit: 10}); err == nil {
		t.Error("no users")
	}
	if _, err := d.PublicDiagram(dctx, "bad slug"); err == nil {
		t.Error("bad slug")
	}
}

// Report moderation on the blog.

type modReports struct {
	byID map[int]report.Report
}

func (f *modReports) Create(_ context.Context, r report.Report) (report.Report, error) {
	r.ID = len(f.byID) + 1
	f.byID[r.ID] = r
	return r, nil
}
func (f *modReports) Get(_ context.Context, id int) (report.Report, error) {
	r, ok := f.byID[id]
	if !ok {
		return r, domain.ErrNotFound
	}
	return r, nil
}
func (f *modReports) List(context.Context, report.ReportStatus, domain.Paging) (report.Page, error) {
	return report.Page{Total: len(f.byID)}, nil
}
func (f *modReports) Decide(_ context.Context, id int, s report.ReportStatus, _ int) (report.Report, error) {
	r := f.byID[id]
	r.Status = s
	f.byID[id] = r
	return r, nil
}

type fakeDiagMod struct {
	owner     int
	err       error
	deleteErr error
	deleted   []string
}

func (f *fakeDiagMod) ReportTarget(context.Context, int, string) (string, int, error) {
	return "prj", f.owner, f.err
}
func (f *fakeDiagMod) AdminDeleteFile(_ context.Context, _ int, id string) error {
	f.deleted = append(f.deleted, id)
	return f.deleteErr
}

type fakeUserGet struct {
	user.Repository
}

func (fakeUserGet) GetByID(_ context.Context, id int) (user.User, error) {
	if id == 99 {
		return user.User{}, domain.ErrNotFound
	}
	return user.User{ID: id}, nil
}

func TestReports(t *testing.T) {
	w := newWorld(ali.ID, "report.create", "report.moderate", "post.delete", "comment.delete")
	reports := &modReports{byID: map[int]report.Report{}}
	diag := &fakeDiagMod{owner: vali.ID}
	w.blog.Reports, w.blog.Diagrams, w.blog.Users = reports, diag, fakeUserGet{}
	w.blog.Comments = delComments{w.comments}
	w.posts.posts[1] = post.Post{ID: 1, UserID: vali.ID, Status: post.StatusPublished}
	w.comments.byID[1] = comment.Comment{ID: 1, PostID: 1, UserID: vali.ID}

	// each target type
	if _, err := w.blog.Report(ctx, ali, report.TargetPost, 1, report.ReasonSpam, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := w.blog.Report(ctx, ali, report.TargetComment, 1, report.ReasonSpam, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := w.blog.Report(ctx, ali, report.TargetUser, vali.ID, report.ReasonAbuse, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := w.blog.ReportDiagram(ctx, ali, "fil_x", report.ReasonSpam, ""); err != nil {
		t.Fatal(err)
	}
	// refusals
	if _, err := w.blog.Report(ctx, ali, report.TargetUser, ali.ID, report.ReasonAbuse, ""); err == nil {
		t.Error("self report")
	}
	if _, err := w.blog.Report(ctx, ali, report.TargetUser, 99, report.ReasonAbuse, ""); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown user: %v", err)
	}
	if _, err := w.blog.Report(ctx, ali, report.TargetComment, 77, report.ReasonSpam, ""); err == nil {
		t.Error("unknown comment")
	}
	if _, err := w.blog.Report(ctx, ali, report.TargetPost, 77, report.ReasonSpam, ""); err == nil {
		t.Error("unknown post")
	}
	if _, err := w.blog.Report(ctx, ali, report.TargetPost, 1, "bogus", ""); err == nil {
		t.Error("bad reason")
	}
	if _, err := w.blog.ReportDiagram(ctx, ali, "", report.ReasonSpam, ""); err == nil {
		t.Error("empty file id")
	}
	diag.err = domain.ErrNotFound
	if _, err := w.blog.ReportDiagram(ctx, ali, "fil_x", report.ReasonSpam, ""); err == nil {
		t.Error("invisible diagram")
	}
	diag.err = nil
	w.blog.Diagrams = nil
	if _, err := w.blog.ReportDiagram(ctx, ali, "fil_x", report.ReasonSpam, ""); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("diagrams off: %v", err)
	}
	w.blog.Diagrams = diag
	denied := newWorld(0).blog
	if _, err := denied.Report(ctx, ali, report.TargetPost, 1, report.ReasonSpam, ""); err == nil {
		t.Error("anonymous report")
	}

	if p, err := w.blog.ListReports(ctx, "", domain.Paging{Page: 1, Limit: 10}); err != nil || p.Total != 4 {
		t.Errorf("list = %+v %v", p, err)
	}
	if _, err := denied.ListReports(ctx, "", domain.Paging{}); err == nil {
		t.Error("anonymous list")
	}

	// decisions
	if _, err := w.blog.DecideReport(ctx, ali, 1, "bogus", false); err == nil {
		t.Error("bad status")
	}
	if _, err := w.blog.DecideReport(ctx, ali, 50, report.StatusDismissed, false); err == nil {
		t.Error("unknown report")
	}
	if _, err := w.blog.DecideReport(ctx, ali, 1, report.StatusDismissed, true); err == nil {
		t.Error("remove on dismissal")
	}
	if _, err := w.blog.DecideReport(ctx, ali, 1, report.StatusResolved, true); err != nil { // deletes the post
		t.Errorf("remove post: %v", err)
	}
	if _, err := w.blog.DecideReport(ctx, ali, 1, report.StatusResolved, false); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("already decided: %v", err)
	}
	if _, err := w.blog.DecideReport(ctx, ali, 2, report.StatusResolved, true); err != nil { // comment
		t.Errorf("remove comment: %v", err)
	}
	if _, err := w.blog.DecideReport(ctx, ali, 3, report.StatusResolved, true); err == nil { // user
		t.Error("remove user accepted")
	}
	if _, err := w.blog.DecideReport(ctx, ali, 4, report.StatusResolved, true); err != nil || !slices.Equal(diag.deleted, []string{"fil_x"}) {
		t.Errorf("remove diagram: %v %v", err, diag.deleted)
	}
	if reportTarget(report.Report{TargetType: report.TargetPost, TargetID: 3}) != "post:3" {
		t.Error("reportTarget")
	}
}

type delComments struct{ *fakeComments }

func (delComments) Delete(context.Context, int) error { return nil }
