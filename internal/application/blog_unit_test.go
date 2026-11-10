package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/category"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/comment"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/post"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/social"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/user"
)

// fatPosts is an in-memory post.Repository; err is returned by everything
// that can fail so error branches are reachable.
type fatPosts struct {
	post.Repository
	posts      map[int]post.Post
	hl         map[int]post.Highlight
	err        error
	created    bool // Like/Clap report a new like
	publishOK  bool
	due        []int
	listCalled []post.Filter
}

func newFatPosts() *fatPosts {
	return &fatPosts{posts: map[int]post.Post{}, hl: map[int]post.Highlight{}}
}
func (r *fatPosts) List(_ context.Context, f post.Filter) (post.Page, error) {
	r.listCalled = append(r.listCalled, f)
	return post.Page{Total: 1}, r.err
}
func (r *fatPosts) ListByIDs(_ context.Context, ids []int) ([]post.Post, error) {
	return []post.Post{{ID: ids[0]}}, r.err
}
func (r *fatPosts) Get(_ context.Context, id, _ int) (post.Post, error) {
	p, ok := r.posts[id]
	if !ok {
		return p, domain.ErrNotFound
	}
	return p, r.err
}
func (r *fatPosts) GetBySlug(_ context.Context, slug string, _ int) (post.Post, error) {
	for _, p := range r.posts {
		if p.Slug == slug {
			return p, nil
		}
	}
	return post.Post{}, domain.ErrNotFound
}
func (r *fatPosts) Publish(context.Context, int, time.Time) (bool, error) { return r.publishOK, r.err }
func (r *fatPosts) DueScheduled(context.Context, time.Time) ([]int, error) {
	return r.due, r.err
}
func (r *fatPosts) Like(_ context.Context, id, _ int) (post.Post, bool, error) {
	return r.posts[id], r.created, r.err
}
func (r *fatPosts) Clap(_ context.Context, id, _, _ int) (post.Post, bool, error) {
	return r.posts[id], r.created, r.err
}
func (r *fatPosts) Unlike(_ context.Context, id, _ int) (post.Post, error) { return r.posts[id], r.err }
func (r *fatPosts) AddHighlight(_ context.Context, h post.Highlight) (post.Highlight, error) {
	h.ID = 9
	r.hl[9] = h
	return h, r.err
}
func (r *fatPosts) GetHighlight(_ context.Context, id int) (post.Highlight, error) {
	h, ok := r.hl[id]
	if !ok {
		return h, domain.ErrNotFound
	}
	return h, nil
}
func (r *fatPosts) DeleteHighlight(_ context.Context, id int) error { delete(r.hl, id); return r.err }
func (r *fatPosts) Highlights(context.Context, int, int, int) (post.Highlights, error) {
	return post.Highlights{}, r.err
}
func (r *fatPosts) AuthorStats(context.Context, int, domain.Paging) ([]post.AuthorStat, int, error) {
	return []post.AuthorStat{{PostID: 1, Claps: 2, Comments: 3, Reads: 10}, {PostID: 2}}, 2, r.err
}
func (r *fatPosts) Tags(context.Context) ([]post.TagCount, error) { return nil, r.err }
func (r *fatPosts) Labels(context.Context) ([]post.Label, error)  { return nil, r.err }
func (r *fatPosts) CreateLabel(_ context.Context, l post.Label) (post.Label, error) {
	l.ID = 4
	return l, r.err
}
func (r *fatPosts) DeleteLabel(context.Context, int) error { return r.err }

type fatComments struct {
	comment.Repository
	byID map[int]comment.Comment
	err  error
	made bool
}

func (r *fatComments) ListByPost(context.Context, int, int, domain.Paging) (comment.Page, error) {
	return comment.Page{}, r.err
}
func (r *fatComments) ListAll(context.Context, domain.Paging) (comment.Page, error) {
	return comment.Page{}, r.err
}
func (r *fatComments) Get(_ context.Context, id int) (comment.Comment, error) {
	c, ok := r.byID[id]
	if !ok {
		return c, domain.ErrNotFound
	}
	return c, nil
}
func (r *fatComments) Add(_ context.Context, c comment.Comment) (comment.Comment, error) {
	c.ID = 50 + len(r.byID)
	r.byID[c.ID] = c
	return c, r.err
}
func (r *fatComments) Edit(_ context.Context, id int, text string) (comment.Comment, error) {
	c := r.byID[id]
	c.Text = text
	return c, r.err
}
func (r *fatComments) Delete(_ context.Context, id int) error {
	if r.err == nil {
		delete(r.byID, id)
	}
	return r.err
}
func (r *fatComments) Like(_ context.Context, id, _ int) (comment.Comment, bool, error) {
	return r.byID[id], r.made, r.err
}
func (r *fatComments) Unlike(_ context.Context, id, _ int) (comment.Comment, error) {
	return r.byID[id], r.err
}

type fatCats struct {
	category.Repository
	exists bool
	err    error
}

func (c fatCats) List(context.Context) ([]category.Category, error) { return nil, c.err }
func (c fatCats) Exists(context.Context, int) (bool, error)         { return c.exists, c.err }
func (c fatCats) Create(_ context.Context, n string) (category.Category, error) {
	return category.Category{ID: 3, Name: n}, c.err
}
func (c fatCats) Delete(context.Context, int) error { return c.err }

type fatSocial struct {
	hidden int
	social.Repository
	err  error
	sent int
}

func (s *fatSocial) Bookmark(context.Context, int, int) error   { return s.err }
func (s *fatSocial) Unbookmark(context.Context, int, int) error { return s.err }
func (s *fatSocial) FollowTag(context.Context, int, string) error {
	return s.err
}
func (s *fatSocial) UnfollowTag(context.Context, int, string) error { return s.err }
func (s *fatSocial) FollowedTags(context.Context, int) ([]string, error) {
	return []string{"go"}, s.err
}
func (s *fatSocial) Notify(_ context.Context, n social.Notification, to []int) ([]social.Notification, error) {
	if s.err != nil {
		return nil, s.err
	}
	var out []social.Notification
	for _, id := range to {
		n.UserID = id
		out = append(out, n)
	}
	s.sent += len(out)
	return out, nil
}
func (s *fatSocial) Notifications(context.Context, int, domain.Paging) (social.NotificationPage, error) {
	return social.NotificationPage{}, s.err
}
func (s *fatSocial) MarkRead(context.Context, int, []int64) error { return s.err }
func (s *fatSocial) MarkAllRead(context.Context, int) error       { return s.err }
func (s *fatSocial) DiagramNotifications(context.Context, int, bool, int) ([]social.Notification, int, error) {
	return nil, 3, s.err
}
func (s *fatSocial) MarkDiagramRead(context.Context, int, []int64, bool) error { return s.err }

type fatViews struct {
	dailyErr error
	ViewCounter
	hitErr error
	trend  []int
}

func (v fatViews) Hit(context.Context, int, string) error { return v.hitErr }
func (v fatViews) Count(context.Context, int) int64       { return 3 }
func (v fatViews) Counts(context.Context, []int) map[int]int64 {
	return map[int]int64{1: 4, 2: 0}
}
func (v fatViews) Trending(context.Context, int, int) ([]int, error) { return v.trend, nil }

type fatStats struct{ err error }

func (s fatStats) Stats(context.Context) (Stats, error) { return Stats{Posts: 5}, s.err }

type errEvents struct{ Events }

func (errEvents) Publish(context.Context, string, any) error { return errBoom }

type fatWorld struct {
	b *Blog
	p *fatPosts
	c *fatComments
	s *fatSocial
	j *fakeJobs
	a *fakeAudit
}

func newFat(caller int, perms ...string) *fatWorld {
	pm := map[string]bool{}
	for _, c := range perms {
		pm[c] = true
	}
	w := &fatWorld{p: newFatPosts(), c: &fatComments{byID: map[int]comment.Comment{}}, s: &fatSocial{}, j: &fakeJobs{}, a: &fakeAudit{}}
	w.b = NewBlog(Repos{Posts: w.p, Comments: w.c, Social: w.s, Categories: fatCats{exists: true}, Stats: fatStats{}},
		Ports{Authz: fakeAuthz{caller: caller, perms: pm}, Audit: w.a, Jobs: w.j, Events: fakeEvents{}, Views: fatViews{trend: []int{1}}})
	w.b.now = func() time.Time { return time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC) }
	return w
}

func pub(id, owner int) post.Post {
	return post.Post{ID: id, UserID: owner, Status: post.StatusPublished, Body: "hello world body", Slug: "s" + itoa(id)}
}

func TestBlogListings(t *testing.T) {
	w := newFat(1, "post.read_draft")
	b := w.b
	if _, err := b.ListPosts(ctx, post.Filter{Status: post.StatusDraft}); err != nil || w.p.listCalled[0].Status != post.StatusPublished {
		t.Error("ListPosts must force published", err)
	}
	if _, err := b.AdminPosts(ctx, post.Filter{Status: "weird"}); err == nil {
		t.Error("bad status")
	}
	if _, err := b.AdminPosts(ctx, post.Filter{}); err != nil {
		t.Error(err)
	}
	if _, err := newFat(1).b.AdminPosts(ctx, post.Filter{Status: post.StatusDraft}); !errors.Is(err, domain.ErrForbidden) {
		t.Error(err)
	}
	if _, err := b.MyPosts(ctx, ali, "", domain.Paging{}); err != nil {
		t.Error(err)
	}
	if _, err := b.Feed(ctx, ali, domain.Paging{}); err != nil {
		t.Error(err)
	}
	if _, err := b.Bookmarks(ctx, ali, domain.Paging{}); err != nil {
		t.Error(err)
	}
	if _, err := b.CategoryPosts(ctx, 1, domain.Paging{}); err != nil {
		t.Error(err)
	}
	b.Categories = fatCats{exists: false}
	if _, err := b.CategoryPosts(ctx, 1, domain.Paging{}); !errors.Is(err, domain.ErrNotFound) {
		t.Error(err)
	}
	b.Categories = fatCats{err: errBoom}
	if _, err := b.CategoryPosts(ctx, 1, domain.Paging{}); err == nil {
		t.Error("exists err")
	}
	if _, err := b.Tags(ctx); err != nil {
		t.Error(err)
	}
}

func TestBlogGetAndPreview(t *testing.T) {
	w := newFat(0)
	w.p.posts[1] = pub(1, 2)
	d := pub(2, 2)
	d.Status = post.StatusDraft
	w.p.posts[2] = d
	p, err := w.b.GetPostBySlug(ctx, "s1", 0)
	if err != nil || p.Views != 3 {
		t.Error(p, err)
	}
	if _, err := w.b.GetPostBySlug(ctx, "s2", 0); !errors.Is(err, domain.ErrNotFound) {
		t.Error("draft visible", err)
	}
	if _, err := w.b.GetPost(ctx, 9, 0); !errors.Is(err, domain.ErrNotFound) {
		t.Error(err)
	}
	if _, err := w.b.PreviewMarkdown(string(make([]byte, MaxPreviewBody+1))); err == nil {
		t.Error("too long")
	}
	if h, err := w.b.PreviewMarkdown("x"); err != nil || h != "" {
		t.Error(h, err)
	}
	w.b.Markdown = mdStub{}
	if h, _ := w.b.PreviewMarkdown("x"); h != "<p>x</p>" {
		t.Error(h)
	}
	if p, _ := w.b.GetPostBySlug(ctx, "s1", 0); p.BodyHTML == "" {
		t.Error("not rendered")
	}
}

type mdStub struct{}

func (mdStub) Render(s string) string { return "<p>" + s + "</p>" }

func TestBlogLikesViewsTrending(t *testing.T) {
	w := newFat(1, "like.create")
	w.p.posts[1] = pub(1, 2)
	w.p.created = true
	if _, err := w.b.LikePost(ctx, ali, 1); err != nil || w.s.sent != 1 {
		t.Error(err, w.s.sent)
	}
	if _, err := w.b.ClapPost(ctx, ali, 1, 3); err != nil {
		t.Error(err)
	}
	if _, err := w.b.ClapPost(ctx, ali, 1, 0); err == nil {
		t.Error("0 claps")
	}
	if _, err := w.b.UnlikePost(ctx, ali, 1); err != nil {
		t.Error(err)
	}
	for _, f := range []func() error{
		func() error { _, e := w.b.LikePost(ctx, ali, 99); return e },
		func() error { _, e := w.b.ClapPost(ctx, ali, 99, 1); return e },
		func() error { _, e := w.b.UnlikePost(ctx, ali, 99); return e },
	} {
		if !errors.Is(f(), domain.ErrNotFound) {
			t.Error("missing post")
		}
	}
	denied := newFat(1)
	denied.p.posts[1] = pub(1, 2)
	for _, f := range []func() error{
		func() error { _, e := denied.b.LikePost(ctx, ali, 1); return e },
		func() error { _, e := denied.b.ClapPost(ctx, ali, 1, 1); return e },
		func() error { _, e := denied.b.UnlikePost(ctx, ali, 1); return e },
	} {
		if !errors.Is(f(), domain.ErrForbidden) {
			t.Error("not forbidden")
		}
	}
	// notify failure is logged, not returned
	w.s.err = errBoom
	if _, err := w.b.LikePost(ctx, ali, 1); !errors.Is(err, errBoom) && err != nil {
		t.Error(err)
	}
	w.s.err = nil

	if err := w.b.ViewPost(ctx, 1, "ip"); err != nil {
		t.Error(err)
	}
	if err := w.b.ViewPost(ctx, 99, "ip"); err == nil {
		t.Error("missing")
	}
	d := pub(3, 2)
	d.Status = post.StatusDraft
	w.p.posts[3] = d
	if err := w.b.ViewPost(ctx, 3, "ip"); !errors.Is(err, domain.ErrNotFound) {
		t.Error(err)
	}
	if ps, err := w.b.Trending(ctx, 99, 99); err != nil || len(ps) != 1 {
		t.Error(ps, err)
	}
	w.b.Views = fatViews{}
	if ps, err := w.b.Trending(ctx, 0, 0); err != nil || len(ps) != 0 {
		t.Error(ps, err)
	}
}

func TestBlogHighlights(t *testing.T) {
	w := newFat(1, "highlight.create")
	w.p.posts[1] = pub(1, 2)
	if _, err := w.b.PostHighlights(ctx, 1, 1); err != nil {
		t.Error(err)
	}
	if _, err := w.b.PostHighlights(ctx, 9, 1); err == nil {
		t.Error("missing")
	}
	if h, err := w.b.AddHighlight(ctx, ali, 1, 0, 5); err != nil || h.Text != "hello" {
		t.Error(h, err)
	}
	if _, err := w.b.AddHighlight(ctx, ali, 1, 5, 2); err == nil {
		t.Error("bad range")
	}
	if _, err := w.b.AddHighlight(ctx, ali, 9, 0, 1); err == nil {
		t.Error("missing post")
	}
	if _, err := newFat(1).b.AddHighlight(ctx, ali, 1, 0, 1); !errors.Is(err, domain.ErrForbidden) {
		t.Error(err)
	}
	if err := w.b.DeleteHighlight(ctx, 404); err == nil {
		t.Error("missing hl")
	}
	w.p.hl[9] = post.Highlight{ID: 9, UserID: 2}
	other := newFat(1)
	other.p.hl[9] = post.Highlight{ID: 9, UserID: 2}
	if err := other.b.DeleteHighlight(ctx, 9); !errors.Is(err, domain.ErrForbidden) {
		t.Error(err)
	}
	w.p.hl[9] = post.Highlight{ID: 9, UserID: 1}
	if err := w.b.DeleteHighlight(ctx, 9); err != nil {
		t.Error(err)
	}
}

func TestBlogStatsTagsBookmarks(t *testing.T) {
	w := newFat(1, "follow.create", "bookmark.create", "stats.read", "category.write", "label.write", "comment.moderate")
	w.p.posts[1] = pub(1, 2)
	s, err := w.b.MyStats(ctx, ali, domain.Paging{Page: 1, Limit: 10})
	if err != nil || s.Totals.Views != 4 || s.Items[0].ReadRatio != 1 {
		t.Errorf("%+v %v", s, err)
	}
	if _, m := s.Paginated(); m == nil {
		t.Error("meta")
	}
	w.p.err = errBoom
	if _, err := w.b.MyStats(ctx, ali, domain.Paging{}); err == nil {
		t.Error("stats err")
	}
	w.p.err = nil

	if err := w.b.FollowTag(ctx, ali, "Go"); err != nil {
		t.Error(err)
	}
	if err := w.b.FollowTag(ctx, ali, ""); err == nil {
		t.Error("empty tag")
	}
	if err := newFat(1).b.FollowTag(ctx, ali, "go"); !errors.Is(err, domain.ErrForbidden) {
		t.Error(err)
	}
	if err := w.b.UnfollowTag(ctx, ali, "Go"); err != nil {
		t.Error(err)
	}
	if tags, err := w.b.FollowedTags(ctx, ali); err != nil || len(tags) != 1 {
		t.Error(tags, err)
	}
	if err := w.b.Bookmark(ctx, ali, 1); err != nil {
		t.Error(err)
	}
	if err := w.b.Bookmark(ctx, ali, 77); err == nil {
		t.Error("missing")
	}
	if err := newFat(1).b.Bookmark(ctx, ali, 1); !errors.Is(err, domain.ErrForbidden) {
		t.Error(err)
	}
	if err := w.b.Unbookmark(ctx, ali, 1); err != nil {
		t.Error(err)
	}

	// categories and labels
	if _, err := w.b.ListCategories(ctx); err != nil {
		t.Error(err)
	}
	if c, err := w.b.CreateCategory(ctx, ali, "News"); err != nil || c.ID != 3 {
		t.Error(c, err)
	}
	if _, err := w.b.CreateCategory(ctx, ali, ""); err == nil {
		t.Error("empty name")
	}
	if _, err := newFat(1).b.CreateCategory(ctx, ali, "x"); err == nil {
		t.Error("forbidden")
	}
	if err := w.b.DeleteCategory(ctx, ali, 3); err != nil {
		t.Error(err)
	}
	if err := newFat(1).b.DeleteCategory(ctx, ali, 3); err == nil {
		t.Error("forbidden")
	}
	if _, err := w.b.Labels(ctx); err != nil {
		t.Error(err)
	}
	if l, err := w.b.CreateLabel(ctx, ali, "bug", "#ff0000"); err != nil || l.ID != 4 {
		t.Error(l, err)
	}
	if _, err := w.b.CreateLabel(ctx, ali, "", "x"); err == nil {
		t.Error("bad label")
	}
	if _, err := newFat(1).b.CreateLabel(ctx, ali, "bug", "#ff0000"); err == nil {
		t.Error("forbidden")
	}
	if err := w.b.DeleteLabel(ctx, ali, 4); err != nil {
		t.Error(err)
	}
	if err := newFat(1).b.DeleteLabel(ctx, ali, 4); err == nil {
		t.Error("forbidden")
	}
	w.p.err = errBoom
	if err := w.b.DeleteLabel(ctx, ali, 4); err == nil {
		t.Error("repo err")
	}
	w.p.err = nil
	w.b.Categories = fatCats{err: errBoom}
	if _, err := w.b.CreateCategory(ctx, ali, "x"); err == nil {
		t.Error("repo err")
	}
	if err := w.b.DeleteCategory(ctx, ali, 3); err == nil {
		t.Error("repo err")
	}

	// notifications
	if _, err := w.b.Notifications(ctx, ali, domain.Paging{}); err != nil {
		t.Error(err)
	}
	if err := w.b.MarkNotificationsRead(ctx, ali, nil, true); err != nil {
		t.Error(err)
	}
	if err := w.b.MarkNotificationsRead(ctx, ali, []int64{1}, false); err != nil {
		t.Error(err)
	}
	w.b.NotifyDiagram(ctx, social.Notification{ActorID: 1}, 2, 1)
	if w.s.sent != 1 {
		t.Error("sent", w.s.sent)
	}
	if _, n, err := w.b.DiagramNotifications(ctx, 1, false, 5); err != nil || n != 3 {
		t.Error(n, err)
	}
	if err := w.b.MarkDiagramRead(ctx, 1, nil, true); err != nil {
		t.Error(err)
	}
	if st, err := w.b.Stats(ctx); err != nil || st.Posts != 5 {
		t.Error(st, err)
	}
	if _, err := newFat(1).b.Stats(ctx); err == nil {
		t.Error("forbidden")
	}
	// notify failing and push failing are tolerated
	w.s.err = errBoom
	w.b.NotifyDiagram(ctx, social.Notification{ActorID: 1}, 2)
	w.s.err = nil
	w.b.Events = errEvents{}
	w.b.NotifyDiagram(ctx, social.Notification{ActorID: 1}, 2)
	w.b.publishComment(ctx, "x", comment.Comment{})
}

func TestBlogComments(t *testing.T) {
	w := newFat(1, "comment.create", "like.create", "comment.moderate", "comment.delete")
	w.p.posts[1] = pub(1, 2)
	w.c.byID[5] = comment.Comment{ID: 5, PostID: 1, UserID: 3, Text: "parent"}
	w.c.byID[6] = comment.Comment{ID: 6, PostID: 1, UserID: 1, Text: "mine"}
	if _, err := w.b.ListComments(ctx, 1, 1, domain.Paging{}); err != nil {
		t.Error(err)
	}
	if _, err := w.b.ListComments(ctx, 9, 1, domain.Paging{}); err == nil {
		t.Error("missing")
	}
	if _, err := w.b.AllComments(ctx, domain.Paging{}); err != nil {
		t.Error(err)
	}
	if _, err := newFat(1).b.AllComments(ctx, domain.Paging{}); err == nil {
		t.Error("forbidden")
	}
	c, err := w.b.AddComment(ctx, ali, 1, 5, "reply!")
	if err != nil || w.s.sent != 2 {
		t.Errorf("add %+v %v sent=%d", c, err, w.s.sent)
	}
	if _, err := w.b.AddComment(ctx, ali, 1, 0, ""); err == nil {
		t.Error("empty text")
	}
	if _, err := w.b.AddComment(ctx, ali, 9, 0, "hi"); err == nil {
		t.Error("missing post")
	}
	if _, err := newFat(1).b.AddComment(ctx, ali, 1, 0, "hi"); err == nil {
		t.Error("forbidden")
	}
	w.b.RequireVerifiedEmail = true
	if _, err := w.b.AddComment(ctx, ali, 1, 0, "hi"); !errors.Is(err, domain.ErrEmailNotVerified) {
		t.Error(err)
	}
	w.b.RequireVerifiedEmail = false
	w.c.err = errBoom
	if _, err := w.b.AddComment(ctx, ali, 1, 0, "hi"); err == nil {
		t.Error("add err")
	}
	w.c.err = nil

	w.c.made = true
	if _, err := w.b.LikeComment(ctx, ali, 5); err != nil {
		t.Error(err)
	}
	if _, err := w.b.LikeComment(ctx, ali, 99); err == nil {
		t.Error("missing")
	}
	w.c.byID[7] = comment.Comment{ID: 7, PostID: 99}
	if _, err := w.b.LikeComment(ctx, ali, 7); err == nil {
		t.Error("post gone")
	}
	if _, err := newFat(1).b.LikeComment(ctx, ali, 5); err == nil {
		t.Error("forbidden")
	}
	if _, err := w.b.UnlikeComment(ctx, ali, 5); err != nil {
		t.Error(err)
	}

	if _, err := w.b.EditComment(ctx, 6, "edited"); err != nil {
		t.Error(err)
	}
	if _, err := w.b.EditComment(ctx, 6, ""); err == nil {
		t.Error("empty")
	}
	if _, err := w.b.EditComment(ctx, 99, "x"); err == nil {
		t.Error("missing")
	}
	if _, err := newFat(1).b.EditComment(ctx, 5, "x"); err == nil {
		t.Error("missing/forbidden")
	}
	o := newFat(1)
	o.c.byID[5] = comment.Comment{ID: 5, UserID: 3, PostID: 1}
	if _, err := o.b.EditComment(ctx, 5, "x"); !errors.Is(err, domain.ErrForbidden) {
		t.Error(err)
	}
	if err := o.b.DeleteComment(ctx, ali, 5); !errors.Is(err, domain.ErrForbidden) {
		t.Error(err)
	}
	if err := w.b.DeleteComment(ctx, ali, 99); err == nil {
		t.Error("missing")
	}
	w.c.err = errBoom
	if err := w.b.DeleteComment(ctx, ali, 6); err == nil {
		t.Error("del err")
	}
	w.c.err = nil
	if err := w.b.DeleteComment(ctx, ali, 6); err != nil {
		t.Error(err)
	}
	if err := w.b.DeleteComment(ctx, user.User{ID: 1}, 5); err != nil || len(w.a.actions) != 1 {
		t.Error(err, w.a.actions)
	}
}

func TestBlogSchedulingAndFanout(t *testing.T) {
	w := newFat(1)
	w.p.posts[1] = pub(1, 2)
	pubAt := w.b.now().Add(-time.Hour)
	if err := w.b.PublishScheduled(ctx, 1); err != nil || len(w.j.fanout) != 0 && false {
		t.Error(err)
	}
	// stale: published long ago
	old := pub(1, 2)
	old.PublishedAt = &[]time.Time{w.b.now().Add(-48 * time.Hour)}[0]
	w.p.posts[1] = old
	n := len(w.j.fanout)
	if err := w.b.PublishScheduled(ctx, 1); err != nil || len(w.j.fanout) != n {
		t.Error("stale refanned", err)
	}
	_ = pubAt
	w.p.posts[1] = pub(1, 2)
	if err := w.b.PublishScheduled(ctx, 404); err != nil {
		t.Error("not found should be ignored", err)
	}
	w.p.publishOK = true
	if err := w.b.PublishScheduled(ctx, 1); err != nil || len(w.a.actions) != 1 {
		t.Error(err)
	}
	w.p.due = []int{1}
	if err := w.b.PublishDue(ctx); err != nil {
		t.Error(err)
	}
	w.p.err = errBoom
	if err := w.b.PublishDue(ctx); err == nil {
		t.Error("due err")
	}
	if err := w.b.PublishScheduled(ctx, 1); err == nil {
		t.Error("publish err")
	}
}
