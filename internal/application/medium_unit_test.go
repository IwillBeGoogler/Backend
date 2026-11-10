package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/post"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/readinglist"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/social"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/user"
)

// Extra methods on the shared fakes for the reads, hides, pins and lists.

func (r *fatPosts) Read(context.Context, int, int, string) (bool, error) { return true, r.err }
func (r *fatPosts) DailyReads(context.Context, int, time.Time) (map[string]int, error) {
	return map[string]int{"2026-10-05": 2}, r.err
}

func (s *fatSocial) CountHidden(context.Context, int) (int, error) { return s.hidden, s.err }
func (s *fatSocial) Hide(context.Context, int, social.HideKind, string) error {
	return s.err
}
func (s *fatSocial) Unhide(context.Context, int, social.HideKind, string) error { return s.err }
func (s *fatSocial) Hidden(context.Context, int, domain.Paging) (social.HidePage, error) {
	return social.HidePage{}, s.err
}

func (v fatViews) Daily(_ context.Context, _ int, days []time.Time) ([]int64, error) {
	return make([]int64, len(days)), v.dailyErr
}

type memLists struct {
	readinglist.Repository
	l      readinglist.List
	count  int
	err    error
	absent bool
}

func (r *memLists) Count(context.Context, int) (int, error) { return r.count, r.err }
func (r *memLists) Create(_ context.Context, uid int, slug string, in readinglist.ListInput) (readinglist.List, error) {
	return readinglist.List{ID: 1, UserID: uid, Slug: slug, Name: in.Name}, r.err
}
func (r *memLists) GetBySlug(context.Context, string) (readinglist.List, error) {
	if r.absent {
		return readinglist.List{}, domain.ErrNotFound
	}
	return r.l, nil
}
func (r *memLists) Update(_ context.Context, id int, in readinglist.ListInput) (readinglist.List, error) {
	return readinglist.List{ID: id, Name: in.Name}, r.err
}
func (r *memLists) Delete(context.Context, int) error      { return r.err }
func (r *memLists) Add(context.Context, int, int) error    { return r.err }
func (r *memLists) Remove(context.Context, int, int) error { return r.err }
func (r *memLists) ByUser(context.Context, int, bool, int, domain.Paging) (readinglist.Page, error) {
	return readinglist.Page{Total: 1}, r.err
}

type memImporter struct {
	in  Imported
	err error
}

func (m memImporter) Fetch(context.Context, string) (Imported, error) { return m.in, m.err }

func TestBlogReadsAndDailyStats(t *testing.T) {
	w := newFat(1, "post.update")
	w.p.posts[1] = pub(1, 2)
	own := pub(2, 1)
	w.p.posts[2] = own
	d := pub(3, 2)
	d.Status = post.StatusDraft
	w.p.posts[3] = d
	if _, err := w.b.ReadPost(ctx, 1, 1, "r", 2, 0); err == nil {
		t.Error("bad progress")
	}
	if _, err := w.b.ReadPost(ctx, 99, 1, "r", 1, 0); err == nil {
		t.Error("missing")
	}
	if _, err := w.b.ReadPost(ctx, 3, 1, "r", 1, 0); !errors.Is(err, domain.ErrNotFound) {
		t.Error(err)
	}
	if ok, err := w.b.ReadPost(ctx, 2, 1, "r", 1, 0); ok || err != nil {
		t.Error("author reads must not count", ok, err)
	}
	if ok, err := w.b.ReadPost(ctx, 1, 1, "r", 0.1, 0); ok || err != nil {
		t.Error("skim must not count", ok, err)
	}
	if ok, err := w.b.ReadPost(ctx, 1, 1, "r", 1, 0); !ok || err != nil {
		t.Error(ok, err)
	}
	w.p.posts[1] = func() post.Post { p := pub(1, 1); return p }()
	st, err := w.b.DailyStats(ctx, ali, 1, 100)
	if err != nil || len(st.Days) != maxStatDays {
		t.Errorf("%d %v", len(st.Days), err)
	}
	if _, err := w.b.DailyStats(ctx, ali, 77, 3); err == nil {
		t.Error("missing")
	}
	w.b.Views = fatViews{dailyErr: errBoom}
	if _, err := w.b.DailyStats(ctx, ali, 1, 3); err == nil {
		t.Error("views err")
	}
	w.b.Views = fatViews{}
	w.p.err = errBoom
	if _, err := w.b.DailyStats(ctx, ali, 1, 3); err == nil {
		t.Error("reads err")
	}
}

func TestBlogForYouHidePinSubscribe(t *testing.T) {
	w := newFat(1)
	users := newMemUsers(user.User{ID: 1, Username: "ali"}, user.User{ID: 2, Username: "vali"})
	w.b.Users = users
	w.p.posts[1] = pub(1, 1)
	w.b.Discovery = memDiscovery{ids: []int{1}}

	// ForYou: Discovery stub returns related ids only; use a dedicated stub
	w.b.Discovery = forYou{ids: []int{1}}
	if p, err := w.b.ForYou(ctx, ali, domain.NewPaging(1, 10)); err != nil || len(p.Items) != 1 || p.Total != 4 {
		t.Error(p, err)
	}
	w.b.Discovery = forYou{}
	if p, err := w.b.ForYou(ctx, ali, domain.NewPaging(1, 10)); err != nil || p.Items == nil {
		t.Error(p, err)
	}

	// Hide
	for _, c := range []struct {
		k      social.HideKind
		target string
		ok     bool
	}{
		{social.HidePost, "1", true}, {social.HidePost, "x", false}, {social.HidePost, "99", false},
		{social.HideAuthor, "vali", true}, {social.HideAuthor, "nobody", false},
		{social.HideTag, "go", true}, {social.HideTag, "", false}, {"weird", "x", false},
	} {
		err := w.b.Hide(ctx, ali, c.k, c.target)
		if (err == nil) != c.ok {
			t.Errorf("Hide(%s,%q) = %v", c.k, c.target, err)
		}
		err = w.b.Unhide(ctx, ali, c.k, c.target)
		if c.k != "weird" && c.target != "x" && c.target != "" && c.target != "nobody" && c.target != "99" && err != nil {
			t.Errorf("Unhide(%s,%q) = %v", c.k, c.target, err)
		}
	}
	w.s.hidden = maxHides
	if err := w.b.Hide(ctx, ali, social.HideTag, "go"); err == nil {
		t.Error("too many hides")
	}
	w.s.hidden = 0
	w.s.err = errBoom
	if err := w.b.Hide(ctx, ali, social.HideTag, "go"); err == nil {
		t.Error("count err")
	}
	w.s.err = nil
	if _, err := w.b.Hidden(ctx, ali, domain.Paging{}); err != nil {
		t.Error(err)
	}

	// Pin
	if err := w.b.Pin(ctx, ali, 0); err != nil {
		t.Error(err)
	}
	if err := w.b.Pin(ctx, ali, 1); err != nil {
		t.Error(err)
	}
	if err := w.b.Pin(ctx, ali, 99); err == nil {
		t.Error("missing")
	}
	w.p.posts[5] = pub(5, 2)
	if err := w.b.Pin(ctx, ali, 5); err == nil {
		t.Error("foreign post")
	}

	// Subscribe
	verified := user.User{ID: 1, Username: "ali", EmailVerified: true}
	if err := w.b.Subscribe(ctx, ali, "vali"); !errors.Is(err, domain.ErrEmailNotVerified) {
		t.Error(err)
	}
	if err := w.b.Subscribe(ctx, verified, "nobody"); err == nil {
		t.Error("missing")
	}
	if err := w.b.Subscribe(ctx, verified, "ali"); err == nil {
		t.Error("self")
	}
	if err := w.b.Subscribe(ctx, verified, "vali"); err != nil {
		t.Error(err)
	}
	w.b.Relations = &memRelations{blocked: true}
	if err := w.b.Subscribe(ctx, verified, "vali"); err == nil {
		t.Error("blocked")
	}
	w.b.Relations = nil
	if err := w.b.Unsubscribe(ctx, verified, "nobody"); err == nil {
		t.Error("missing")
	}
	if err := w.b.Unsubscribe(ctx, verified, "vali"); err != nil {
		t.Error(err)
	}

	// one-click unsubscribe
	if err := w.b.UnsubscribeByLink(ctx, 1, 2, "x"); err == nil {
		t.Error("no key")
	}
	w.b.UnsubscribeKey = []byte("k")
	w.b.APIURL = "http://api"
	if err := w.b.UnsubscribeByLink(ctx, 1, 2, "bad"); err == nil {
		t.Error("bad sig")
	}
	if err := w.b.UnsubscribeByLink(ctx, 1, 2, w.b.unsubscribeSig(1, 2)); err != nil {
		t.Error(err)
	}
	if w.b.unsubscribeURL(1, 2) == "" {
		t.Error("url")
	}
}

type forYou struct {
	Discovery
	ids []int
}

func (f forYou) ForYou(context.Context, int, int, int) ([]int, int, error) { return f.ids, 4, nil }

func TestBlogFanoutAndEmail(t *testing.T) {
	w := newFat(1)
	w.p.posts[1] = pub(1, 1)
	users := &fanUsers{memUsers: newMemUsers(user.User{ID: 1, Username: "ali"})}
	w.b.Users = users
	w.b.SiteURL, w.b.APIURL, w.b.UnsubscribeKey = "http://site", "http://api", []byte("k")
	if err := w.b.FanoutNewPost(ctx, 1); err != nil || w.s.sent != 2 || len(w.j.mail) != 2 {
		t.Errorf("fanout: %v sent=%d mail=%d", err, w.s.sent, len(w.j.mail))
	}
	if err := w.b.FanoutNewPost(ctx, 404); err != nil {
		t.Error("missing post is ignored", err)
	}
	d := pub(2, 1)
	d.Status = post.StatusDraft
	w.p.posts[2] = d
	if err := w.b.FanoutNewPost(ctx, 2); err != nil {
		t.Error(err)
	}
	users.err = errBoom
	if err := w.b.FanoutNewPost(ctx, 1); err == nil {
		t.Error("followers err")
	}
	users.err = nil
	users.subErr = errBoom // email failure is logged, not returned
	if err := w.b.FanoutNewPost(ctx, 1); err != nil {
		t.Error(err)
	}
	users.subErr = nil
	w.b.Relations = &memRelations{silent: []int{20}}
	w.j.mail = nil
	_ = w.b.emailSubscribers(ctx, w.p.posts[1])
	if len(w.j.mail) != 1 {
		t.Errorf("muted subscriber got mail: %d", len(w.j.mail))
	}
}

type fanUsers struct {
	*memUsers
	subErr error
}

func (u *fanUsers) FollowerIDs(context.Context, int) ([]int, error) { return []int{10, 11}, u.err }
func (u *fanUsers) Subscribers(context.Context, int) ([]user.Subscriber, error) {
	return []user.Subscriber{{ID: 10, Email: "a@x.io"}, {ID: 20, Email: "b@x.io"}}, u.subErr
}

func TestBlogReadingLists(t *testing.T) {
	w := newFat(1, "post.create")
	users := newMemUsers(user.User{ID: 1, Username: "ali"}, user.User{ID: 2, Username: "vali"})
	w.b.Users = users
	lists := &memLists{l: readinglist.List{ID: 1, UserID: 1, Slug: "l"}}
	w.b.Lists = lists
	w.p.posts[1] = pub(1, 2)
	in := readinglist.ListInput{Name: "Later"}
	if l, err := w.b.CreateList(ctx, ali, in); err != nil || l.Slug == "" {
		t.Error(l, err)
	}
	if _, err := w.b.CreateList(ctx, ali, readinglist.ListInput{}); err == nil {
		t.Error("empty name")
	}
	lists.count = readinglist.MaxLists
	if _, err := w.b.CreateList(ctx, ali, in); !errors.Is(err, readinglist.ErrTooMany) {
		t.Error(err)
	}
	lists.err = errBoom
	if _, err := w.b.CreateList(ctx, ali, in); err == nil {
		t.Error("count err")
	}
	lists.err, lists.count = nil, 0

	if _, err := w.b.GetList(ctx, "l", 1); err != nil {
		t.Error(err)
	}
	lists.l.Private = true
	if _, err := w.b.GetList(ctx, "l", 2); !errors.Is(err, domain.ErrNotFound) {
		t.Error(err)
	}
	lists.l.Private = false
	// someone else's public list: readable, not writable
	other := user.User{ID: 2, Username: "vali"}
	if _, err := w.b.UpdateList(ctx, other, "l", in); !errors.Is(err, domain.ErrForbidden) {
		t.Error(err)
	}
	if err := w.b.DeleteList(ctx, other, "l"); !errors.Is(err, domain.ErrForbidden) {
		t.Error(err)
	}
	if err := w.b.AddToList(ctx, other, "l", 1); !errors.Is(err, domain.ErrForbidden) {
		t.Error(err)
	}
	if err := w.b.RemoveFromList(ctx, other, "l", 1); !errors.Is(err, domain.ErrForbidden) {
		t.Error(err)
	}
	if _, err := w.b.UpdateList(ctx, ali, "l", readinglist.ListInput{}); err == nil {
		t.Error("empty name")
	}
	if _, err := w.b.UpdateList(ctx, ali, "l", in); err != nil {
		t.Error(err)
	}
	if err := w.b.AddToList(ctx, ali, "l", 1); err != nil {
		t.Error(err)
	}
	if err := w.b.AddToList(ctx, ali, "l", 99); err == nil {
		t.Error("missing post")
	}
	if err := w.b.RemoveFromList(ctx, ali, "l", 1); err != nil {
		t.Error(err)
	}
	if _, err := w.b.ListItems(ctx, "l", 1, domain.Paging{}); err != nil {
		t.Error(err)
	}
	if err := w.b.DeleteList(ctx, ali, "l"); err != nil {
		t.Error(err)
	}
	lists.absent = true
	if _, err := w.b.ListItems(ctx, "l", 1, domain.Paging{}); err == nil {
		t.Error("missing list")
	}
	if err := w.b.DeleteList(ctx, ali, "l"); err == nil {
		t.Error("missing list")
	}
	if _, err := w.b.MyLists(ctx, ali, 1, domain.Paging{}); err != nil {
		t.Error(err)
	}
	if _, err := w.b.UserLists(ctx, "vali", 1, domain.Paging{}); err != nil {
		t.Error(err)
	}
	if _, err := w.b.UserLists(ctx, "nobody", 1, domain.Paging{}); !errors.Is(err, domain.ErrNotFound) {
		t.Error(err)
	}
}

func TestBlogImportPost(t *testing.T) {
	w := newFat(1, "post.create")
	w.p.posts = map[int]post.Post{}
	create := &createPosts{fatPosts: w.p}
	w.b.Posts = create
	if _, err := w.b.ImportPost(ctx, ali, "https://x.test/a"); err == nil {
		t.Error("no importer")
	}
	w.b.Importer = memImporter{in: Imported{Title: "", Body: "body text", CanonicalURL: "https://x.test/a"}}
	if p, err := w.b.ImportPost(ctx, ali, "  https://x.test/a "); err != nil || p.Title != "Imported story" {
		t.Error(p, err)
	}
	for _, u := range []string{"ftp://x", "", "https://" + string(make([]byte, 2001))} {
		if _, err := w.b.ImportPost(ctx, ali, u); err == nil {
			t.Errorf("accepted %q", u)
		}
	}
	w.b.Importer = memImporter{err: errBoom}
	if _, err := w.b.ImportPost(ctx, ali, "https://x.test/a"); err == nil {
		t.Error("fetch err")
	}
	w.b.Importer = memImporter{err: domain.Invalid("blocked page")}
	if _, err := w.b.ImportPost(ctx, ali, "https://x.test/a"); err == nil || err.Error() != "blocked page" && !errors.As(err, new(*domain.ValidationError)) {
		t.Error(err)
	}
	if _, err := newFat(1).b.ImportPost(ctx, ali, "https://x.test/a"); err == nil {
		t.Error("no importer")
	}
	nf := newFat(1)
	nf.b.Importer = memImporter{}
	if _, err := nf.b.ImportPost(ctx, ali, "https://x.test/a"); !errors.Is(err, domain.ErrForbidden) {
		t.Error(err)
	}
	w.b.RequireVerifiedEmail = true
	if _, err := w.b.ImportPost(ctx, ali, "https://x.test/a"); !errors.Is(err, domain.ErrEmailNotVerified) {
		t.Error(err)
	}
	long := string(make([]rune, 0)) + "é"
	for len(long) < 600 {
		long += "é"
	}
	if got := truncate(long, 201); len(got) > 201 {
		t.Error("truncate", len(got))
	}
	if truncate("  hi ", 10) != "hi" {
		t.Error("trim")
	}
}

// createPosts makes Create work on the shared fake.
type createPosts struct{ *fatPosts }

func (c *createPosts) Create(_ context.Context, d post.Draft, uid int, author string) (post.Post, error) {
	p := post.Post{ID: len(c.posts) + 1, Title: d.Title, Status: d.Status, UserID: uid, Author: author}
	c.posts[p.ID] = p
	return p, nil
}

func (m *memUsers) SetPinned(context.Context, int, int) error   { return m.err }
func (m *memUsers) Subscribe(context.Context, int, int) error   { return m.err }
func (m *memUsers) Unsubscribe(context.Context, int, int) error { return m.err }
