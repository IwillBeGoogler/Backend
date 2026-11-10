package application

import (
	"context"
	"errors"
	"testing"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/comment"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/post"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/relation"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/revision"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/series"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/social"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/user"
)

type memRelations struct {
	relation.Repository
	blocked bool
	err     error
	silent  []int
}

func (r *memRelations) Add(context.Context, int, int, relation.Kind) error    { return r.err }
func (r *memRelations) Remove(context.Context, int, int, relation.Kind) error { return r.err }
func (r *memRelations) List(context.Context, int, relation.Kind, domain.Paging) (user.SummaryPage, error) {
	return user.SummaryPage{Total: 1}, r.err
}
func (r *memRelations) Blocked(context.Context, int, int) (bool, error) { return r.blocked, r.err }
func (r *memRelations) Silenced(context.Context, int, []int) ([]int, error) {
	return r.silent, r.err
}

type memRevisions struct {
	revision.Repository
	list []revision.Revision
	err  error
}

func (r *memRevisions) Add(context.Context, int, int, revision.Content) (revision.Revision, error) {
	return revision.Revision{}, r.err
}
func (r *memRevisions) List(context.Context, int) ([]revision.Revision, error) { return r.list, r.err }
func (r *memRevisions) Get(_ context.Context, _, v int) (revision.Revision, error) {
	if v == 99 {
		return revision.Revision{}, domain.ErrNotFound
	}
	return revision.Revision{Version: v, Title: "old title", Body: "old body"}, nil
}

type memSeries struct {
	series.Repository
	s        series.Series
	setErr   error
	err      error
	parts    []series.Part
	notFound bool
}

func (r *memSeries) Create(_ context.Context, uid int, slug string, in series.SeriesInput) (series.Series, error) {
	return series.Series{ID: 1, UserID: uid, Slug: slug, Title: in.Title}, r.err
}
func (r *memSeries) Update(_ context.Context, id int, in series.SeriesInput) (series.Series, error) {
	return series.Series{ID: id, Title: in.Title}, r.err
}
func (r *memSeries) Delete(context.Context, int) error { return r.err }
func (r *memSeries) GetBySlug(context.Context, string) (series.Series, error) {
	if r.notFound {
		return series.Series{}, domain.ErrNotFound
	}
	return r.s, nil
}
func (r *memSeries) ByUser(context.Context, int) ([]series.Series, error) { return nil, r.err }
func (r *memSeries) Parts(context.Context, int, bool) ([]series.Part, error) {
	return r.parts, r.err
}
func (r *memSeries) SetPosts(context.Context, int, int, []int) error { return r.setErr }
func (r *memSeries) OfPost(context.Context, int) (series.Series, error) {
	if r.notFound {
		return series.Series{}, domain.ErrNotFound
	}
	return r.s, r.err
}

type memDiscovery struct {
	Discovery
	ids []int
	err error
}

func (d memDiscovery) RelatedPosts(context.Context, int, int) ([]int, error) { return d.ids, d.err }
func (d memDiscovery) SuggestUsers(context.Context, int, int) ([]SuggestedUser, error) {
	return nil, d.err
}
func (d memDiscovery) UserIDs(context.Context, []string) ([]int, error) { return []int{1, 2, 3}, d.err }

func TestBlogRelationsAndSilencing(t *testing.T) {
	w := newFat(1)
	users := newMemUsers(user.User{ID: 1, Username: "ali"}, user.User{ID: 2, Username: "vali"})
	rel := &memRelations{}
	w.b.Users, w.b.Relations = users, rel
	if err := w.b.Relate(ctx, ali, "vali", "enemy"); err == nil {
		t.Error("bad kind")
	}
	if err := w.b.Relate(ctx, ali, "nobody", relation.Block); !errors.Is(err, domain.ErrNotFound) {
		t.Error(err)
	}
	if err := w.b.Relate(ctx, ali, "ali", relation.Mute); err == nil {
		t.Error("self")
	}
	if err := w.b.Relate(ctx, ali, "vali", relation.Block); err != nil {
		t.Error(err)
	}
	if err := w.b.Unrelate(ctx, ali, "nobody", relation.Block); err == nil {
		t.Error("missing")
	}
	if err := w.b.Unrelate(ctx, ali, "vali", relation.Block); err != nil {
		t.Error(err)
	}
	if p, err := w.b.Related(ctx, ali, relation.Block, domain.Paging{}); err != nil || p.Total != 1 {
		t.Error(p, err)
	}

	// notBlocked / unsilenced
	if err := w.b.notBlocked(ctx, 1, 2); err != nil {
		t.Error(err)
	}
	rel.blocked = true
	if err := w.b.notBlocked(ctx, 1, 2); !errors.Is(err, relation.ErrBlocked) {
		t.Error(err)
	}
	rel.silent = []int{2}
	if got := w.b.unsilenced(ctx, 1, []int{2, 3}); len(got) != 1 || got[0] != 3 {
		t.Error(got)
	}
	rel.err = errBoom
	if got := w.b.unsilenced(ctx, 1, []int{2, 3}); len(got) != 2 {
		t.Error("a failing lookup must not drop recipients", got)
	}
	rel.err, rel.blocked = nil, false

	// mentions notify unblocked users except the post author
	w.b.Discovery = memDiscovery{}
	w.p.posts[1] = pub(1, 2)
	w.b.mentionInComment(ctx, commentWith(1, 1, "hello @vali and @x"), "")
	if w.s.sent == 0 {
		t.Error("no mention notifications")
	}
	w.b.Discovery = memDiscovery{err: errBoom}
	w.b.mention(ctx, []string{"x"}, socialMention(1), 2)
	rel.blocked = true
	w.b.Discovery = memDiscovery{}
	sent := w.s.sent
	w.b.mention(ctx, []string{"x"}, socialMention(1))
	if w.s.sent != sent {
		t.Error("blocked users were notified")
	}
}

func TestBlogRevisions(t *testing.T) {
	w := newFat(1, "post.update")
	cur := pub(1, 2)
	cur.Title, cur.Body = "new title", "new body"
	w.p.posts[1] = cur
	revs := &memRevisions{}
	w.b.Revisions = revs
	if l, err := w.b.PostRevisions(ctx, ali, 1); err != nil || l == nil {
		t.Error(l, err)
	}
	if _, err := w.b.PostRevisions(ctx, ali, 77); err == nil {
		t.Error("missing post")
	}
	if _, err := newFat(1).b.PostRevisions(ctx, ali, 1); err == nil {
		t.Error("no such post")
	}
	denied := newFat(1)
	denied.p.posts[1] = cur
	denied.b.Revisions = revs
	if _, err := denied.b.PostRevisions(ctx, ali, 1); !errors.Is(err, domain.ErrForbidden) {
		t.Error(err)
	}
	if d, err := w.b.PostRevision(ctx, ali, 1, 1); err != nil || d.Title != "old title" {
		t.Error(d, err)
	}
	if _, err := w.b.PostRevision(ctx, ali, 1, 99); err == nil {
		t.Error("missing version")
	}
	if _, err := denied.b.PostRevision(ctx, ali, 1, 1); err == nil {
		t.Error("denied")
	}
	if _, err := denied.b.RestoreRevision(ctx, ali, 1, 1); err == nil {
		t.Error("denied")
	}
	if _, err := w.b.RestoreRevision(ctx, ali, 1, 99); err == nil {
		t.Error("missing version")
	}
	// snapshot: unchanged text is not stored, changed text is
	if err := w.b.snapshot(ctx, ali, cur, cur); err != nil {
		t.Error(err)
	}
	changed := cur
	changed.Body = "other"
	if err := w.b.snapshot(ctx, ali, cur, changed); err != nil {
		t.Error(err)
	}
	revs.err = errBoom
	if err := w.b.snapshot(ctx, ali, cur, changed); err == nil {
		t.Error("snapshot error swallowed")
	}
	w.b.Revisions = nil
	if err := w.b.snapshot(ctx, ali, cur, changed); err != nil {
		t.Error(err)
	}
}

func TestBlogSeries(t *testing.T) {
	w := newFat(1, "post.create", "post.update")
	sr := &memSeries{s: series.Series{ID: 1, UserID: 1, Slug: "s"}}
	users := newMemUsers(user.User{ID: 1, Username: "ali"})
	w.b.Series, w.b.Users = sr, users
	if s, err := w.b.CreateSeries(ctx, ali, series.SeriesInput{Title: "My series"}); err != nil || s.Slug == "" {
		t.Error(s, err)
	}
	if _, err := w.b.CreateSeries(ctx, ali, series.SeriesInput{}); err == nil {
		t.Error("empty title")
	}
	if _, err := newFat(1).b.CreateSeries(ctx, ali, series.SeriesInput{Title: "x"}); err == nil {
		t.Error("forbidden")
	}
	w.b.RequireVerifiedEmail = true
	if _, err := w.b.CreateSeries(ctx, ali, series.SeriesInput{Title: "x"}); !errors.Is(err, domain.ErrEmailNotVerified) {
		t.Error(err)
	}
	w.b.RequireVerifiedEmail = false

	if _, err := w.b.UpdateSeries(ctx, "s", series.SeriesInput{Title: "T"}); err != nil {
		t.Error(err)
	}
	if _, err := w.b.UpdateSeries(ctx, "s", series.SeriesInput{}); err == nil {
		t.Error("empty title")
	}
	if err := w.b.DeleteSeries(ctx, "s"); err != nil {
		t.Error(err)
	}
	sr.notFound = true
	if err := w.b.DeleteSeries(ctx, "s"); !errors.Is(err, domain.ErrNotFound) {
		t.Error(err)
	}
	if _, err := w.b.UpdateSeries(ctx, "s", series.SeriesInput{Title: "T"}); err == nil {
		t.Error("missing")
	}
	if _, err := w.b.SetSeriesPosts(ctx, "s", []int{1}); err == nil {
		t.Error("missing")
	}
	if _, err := w.b.GetSeries(ctx, "s", 1); !errors.Is(err, domain.ErrNotFound) {
		t.Error(err)
	}
	sr.notFound = false
	if _, err := w.b.SetSeriesPosts(ctx, "s", []int{1, 1}); err == nil {
		t.Error("duplicate ids")
	}
	if d, err := w.b.SetSeriesPosts(ctx, "s", []int{1}); err != nil || d.Posts == nil {
		t.Error(d, err)
	}
	sr.setErr = domain.ErrNotFound
	if _, err := w.b.SetSeriesPosts(ctx, "s", []int{1}); err == nil {
		t.Error("foreign posts")
	}
	sr.setErr = errBoom
	if _, err := w.b.SetSeriesPosts(ctx, "s", []int{1}); !errors.Is(err, errBoom) {
		t.Error(err)
	}
	sr.setErr = nil
	if _, err := w.b.GetSeries(ctx, "s", 0); err != nil {
		t.Error(err)
	}
	if _, err := w.b.GetSeries(ctx, "s", 1); err != nil {
		t.Error(err)
	}

	del := newMemUsers(user.User{ID: 1, Username: "ali"})
	w.b.Users = del
	if l, err := w.b.UserSeries(ctx, "ali"); err != nil || l == nil {
		t.Error(l, err)
	}
	if _, err := w.b.UserSeries(ctx, "nobody"); !errors.Is(err, domain.ErrNotFound) {
		t.Error(err)
	}

	// PostSeries
	w.p.posts[1] = pub(1, 1)
	sr.parts = []series.Part{{PostID: 1, Status: post.StatusPublished}}
	if _, err := w.b.PostSeries(ctx, 1, 1); err != nil {
		t.Error(err)
	}
	if _, err := w.b.PostSeries(ctx, 77, 1); err == nil {
		t.Error("missing post")
	}
	sr.parts = nil
	if _, err := w.b.PostSeries(ctx, 1, 1); !errors.Is(err, domain.ErrNotFound) {
		t.Error("post not in parts", err)
	}
	sr.err = errBoom
	if _, err := w.b.PostSeries(ctx, 1, 1); err == nil {
		t.Error("series error")
	}
	sr.err, sr.notFound = nil, true
	if _, err := w.b.PostSeries(ctx, 1, 1); err == nil {
		t.Error("no series")
	}
}

func TestBlogRecommendations(t *testing.T) {
	w := newFat(1)
	w.p.posts[1] = pub(1, 2)
	w.b.Discovery = memDiscovery{ids: []int{1}}
	if ps, err := w.b.RelatedPosts(ctx, 1, 1, 5); err != nil || len(ps) != 1 {
		t.Error(ps, err)
	}
	if _, err := w.b.RelatedPosts(ctx, 9, 1, 5); err == nil {
		t.Error("missing")
	}
	w.b.Discovery = memDiscovery{}
	if ps, err := w.b.RelatedPosts(ctx, 1, 1, 0); err != nil || ps == nil {
		t.Error(ps, err)
	}
	if l, err := w.b.SuggestUsers(ctx, ali, 5); err != nil || l == nil {
		t.Error(l, err)
	}
}

func commentWith(id, postID int, text string) comment.Comment {
	return comment.Comment{ID: id, PostID: postID, UserID: 1, Text: text}
}

func socialMention(actor int) social.Notification {
	return social.Notification{Type: social.NotifyMention, ActorID: actor, PostID: 1}
}
