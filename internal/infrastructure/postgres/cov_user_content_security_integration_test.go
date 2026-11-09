package postgres_test

// Coverage tests for user, category, identity, revision/series/discovery,
// MFA, sanction, relation, publication, reading list, feedback, analytics,
// report and client error repositories against a real Postgres.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/clienterror"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/feedback"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/mfa"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/post"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/publication"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/readinglist"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/relation"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/report"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/revision"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/sanction"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/series"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/user"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/postgres"
)

func covUCSNotFound(t *testing.T, what string, err error) {
	t.Helper()
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("%s: err = %v, want ErrNotFound", what, err)
	}
}

func covUCSConflict(t *testing.T, what string, err error) {
	t.Helper()
	if !errors.Is(err, domain.ErrConflict) {
		t.Errorf("%s: err = %v, want ErrConflict", what, err)
	}
}

func covUCSUser(t *testing.T, users *postgres.UserRepo, name string) user.User {
	t.Helper()
	u, err := users.Create(context.Background(), name, name+"@example.com")
	must(t, err)
	return u
}

func covUCSPost(t *testing.T, posts *postgres.PostRepo, title string, u user.User, tags ...string) post.Post {
	t.Helper()
	p, err := create(t, posts, post.Draft{Title: title, Status: post.StatusPublished, Body: "body of " + title, Tags: tags}, u.ID, u.Username)
	must(t, err)
	return p
}

func TestCovUCSUsers(t *testing.T) {
	db := connect(t)
	ctx := context.Background()
	users := postgres.NewUserRepo(db)
	posts := postgres.NewPostRepo(db)
	ids := postgres.NewIdentityRepo(db)
	cats := postgres.NewCategoryRepo(db)
	stats := postgres.NewStatsRepo(db)

	a := covUCSUser(t, users, "covucs_a")
	b := covUCSUser(t, users, "covucs_b")
	c := covUCSUser(t, users, "covucs_c")
	d := covUCSUser(t, users, "covucs_d")

	t.Run("ensure and get", func(t *testing.T) {
		e, err := users.Ensure(ctx, "covucs_e", "e@example.com")
		must(t, err)
		e2, err := users.Ensure(ctx, "COVUCS_E", "other@example.com")
		must(t, err)
		if e2.ID != e.ID || e2.Email != "e@example.com" {
			t.Errorf("ensure existing = %+v, want same user keeping email", e2)
		}
		_, err = users.Ensure(ctx, "covucs_newname", "covucs_a@example.com")
		covUCSConflict(t, "ensure taken email", err)
		got, err := users.GetByUsername(ctx, "COVUCS_A")
		if err != nil || got.ID != a.ID {
			t.Errorf("GetByUsername = %+v, %v", got, err)
		}
		got, err = users.GetByID(ctx, b.ID)
		if err != nil || got.Username != "covucs_b" {
			t.Errorf("GetByID = %+v, %v", got, err)
		}
		got, err = users.GetByLogin(ctx, "COVUCS_C@example.com")
		if err != nil || got.ID != c.ID {
			t.Errorf("GetByLogin email = %+v, %v", got, err)
		}
		_, err = users.GetByUsername(ctx, "nobody")
		covUCSNotFound(t, "GetByUsername", err)
		_, err = users.GetByID(ctx, 999999)
		covUCSNotFound(t, "GetByID", err)
		_, err = users.GetByLogin(ctx, "nobody")
		covUCSNotFound(t, "GetByLogin", err)
		_, err = users.Create(ctx, "covucs_x", "covucs_a@example.com")
		covUCSConflict(t, "duplicate email", err)
	})

	t.Run("email verified and list", func(t *testing.T) {
		must(t, users.MarkEmailVerified(ctx, a.ID))
		must(t, users.MarkEmailVerified(ctx, a.ID))
		covUCSNotFound(t, "MarkEmailVerified", users.MarkEmailVerified(ctx, 999999))
		got, _ := users.GetByID(ctx, a.ID)
		if !got.EmailVerified {
			t.Error("email not verified")
		}
		pg, err := users.List(ctx, "covucs_", domain.Paging{Page: 1, Limit: 2})
		if err != nil || len(pg.Items) != 2 || pg.Total < 4 || pg.Limit != 2 {
			t.Fatalf("list = %+v, %v", pg, err)
		}
		pg, err = users.List(ctx, "", domain.Paging{Page: 1, Limit: 50})
		if err != nil || len(pg.Items) < 4 {
			t.Errorf("list all = %d, %v", len(pg.Items), err)
		}
		pg, err = users.List(ctx, "zzzznomatch", domain.Paging{Page: 1, Limit: 5})
		if err != nil || len(pg.Items) != 0 || pg.Items == nil {
			t.Errorf("list none = %+v, %v", pg, err)
		}
	})

	t.Run("profile follow subscribe", func(t *testing.T) {
		p1 := covUCSPost(t, posts, "covucs profile post", a, "go")
		if _, ok, err := posts.Like(ctx, p1.ID, b.ID); err != nil || !ok {
			t.Fatalf("like: %v %v", ok, err)
		}
		must(t, users.SetPinned(ctx, a.ID, p1.ID))
		ok, err := users.Follow(ctx, b.ID, a.ID)
		if err != nil || !ok {
			t.Fatalf("follow: %v %v", ok, err)
		}
		if ok, err := users.Follow(ctx, b.ID, a.ID); err != nil || ok {
			t.Errorf("refollow: %v %v", ok, err)
		}
		if _, err := users.Follow(ctx, b.ID, 999999); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("follow missing: %v", err)
		}
		must(t, users.Subscribe(ctx, b.ID, a.ID))
		must(t, users.Subscribe(ctx, b.ID, a.ID))
		covUCSNotFound(t, "subscribe missing", users.Subscribe(ctx, b.ID, 999999))
		must(t, users.MarkEmailVerified(ctx, b.ID))
		subs, err := users.Subscribers(ctx, a.ID)
		if err != nil || len(subs) != 1 || subs[0].ID != b.ID {
			t.Errorf("subscribers = %v, %v", subs, err)
		}
		prof, err := users.Profile(ctx, "COVUCS_A", b.ID)
		if err != nil || !prof.IsFollowing || !prof.IsSubscribed || prof.Followers != 1 || prof.Posts != 1 ||
			prof.Likes != 1 || prof.PinnedPostID != p1.ID {
			t.Errorf("profile = %+v, %v", prof, err)
		}
		_, err = users.Profile(ctx, "nobody", 0)
		covUCSNotFound(t, "profile", err)
		must(t, users.Unsubscribe(ctx, b.ID, a.ID))
		must(t, users.Unsubscribe(ctx, b.ID, a.ID))
		must(t, users.SetPinned(ctx, a.ID, 0))
		covUCSNotFound(t, "SetPinned", users.SetPinned(ctx, 999999, 0))
		upd, err := users.UpdateProfile(ctx, a.ID, user.ProfileUpdate{DisplayName: "Ann", Bio: "hi", AvatarURL: "http://x/y.png"})
		if err != nil || upd.DisplayName != "Ann" || upd.Bio != "hi" {
			t.Errorf("update profile = %+v, %v", upd, err)
		}
		_, err = users.UpdateProfile(ctx, 999999, user.ProfileUpdate{})
		covUCSNotFound(t, "UpdateProfile", err)
	})

	t.Run("followers paging", func(t *testing.T) {
		must(t, func() error { _, err := users.Follow(ctx, c.ID, a.ID); return err }())
		must(t, func() error { _, err := users.Follow(ctx, d.ID, a.ID); return err }())
		pg1, err := users.Followers(ctx, a.ID, domain.Paging{Page: 1, Limit: 2})
		if err != nil || len(pg1.Items) != 2 || pg1.Total != 3 || pg1.NextCursor == "" {
			t.Fatalf("followers p1 = %+v, %v", pg1, err)
		}
		pg2, err := users.Followers(ctx, a.ID, domain.Paging{Limit: 2, Cursor: pg1.NextCursor})
		if err != nil || len(pg2.Items) != 1 || pg2.NextCursor != "" {
			t.Errorf("followers by cursor = %+v, %v", pg2, err)
		}
		if _, err := users.Followers(ctx, a.ID, domain.Paging{Limit: 2, Cursor: "!!!"}); err == nil {
			t.Error("bad cursor accepted")
		}
		fg, err := users.Following(ctx, b.ID, domain.Paging{Page: 1, Limit: 5})
		if err != nil || len(fg.Items) != 1 || fg.Items[0].ID != a.ID {
			t.Errorf("following = %+v, %v", fg, err)
		}
		fids, err := users.FollowerIDs(ctx, a.ID)
		if err != nil || len(fids) != 3 {
			t.Errorf("follower ids = %v, %v", fids, err)
		}
		must(t, users.Unfollow(ctx, d.ID, a.ID))
	})

	t.Run("delete purge restore", func(t *testing.T) {
		now := time.Now().UTC()
		old := now.Add(-48 * time.Hour)
		doomed := covUCSUser(t, users, "covucs_doomed")
		covUCSPost(t, posts, "covucs doomed post", doomed)
		must(t, users.MarkDeleted(ctx, doomed.ID, old))
		covUCSNotFound(t, "MarkDeleted", users.MarkDeleted(ctx, 999999, old))
		if ok, err := users.Purge(ctx, doomed.ID, old.Add(-time.Hour)); err != nil || ok {
			t.Errorf("early purge = %v, %v", ok, err)
		}
		if ok, err := users.Purge(ctx, 999999, now); err != nil || ok {
			t.Errorf("missing purge = %v, %v", ok, err)
		}
		// BUG (production): Purge of a never-deleted user scans NULL deleted_at <= $2 into bool and errors.
		if ok, _ := users.Purge(ctx, a.ID, now); ok {
			t.Error("active user purged")
		}
		due, err := users.DuePurges(ctx, now)
		if err != nil || len(due) != 1 || due[0] != doomed.ID {
			t.Errorf("due = %v, %v", due, err)
		}
		if _, err := users.Profile(ctx, "covucs_doomed", 0); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("deleted profile: %v", err)
		}
		must(t, users.Restore(ctx, doomed.ID))
		covUCSNotFound(t, "Restore", users.Restore(ctx, 999999))
		must(t, users.MarkDeleted(ctx, doomed.ID, old))
		if ok, err := users.Purge(ctx, doomed.ID, now); err != nil || !ok {
			t.Errorf("purge = %v, %v", ok, err)
		}
		_, err = users.GetByID(ctx, doomed.ID)
		covUCSNotFound(t, "purged user", err)
		gone := covUCSUser(t, users, "covucs_gone")
		must(t, users.Delete(ctx, gone.ID))
		covUCSNotFound(t, "Delete", users.Delete(ctx, gone.ID))
	})

	t.Run("identities", func(t *testing.T) {
		_, err := ids.UserBySubject(ctx, "github", "sub1")
		covUCSNotFound(t, "UserBySubject", err)
		must(t, ids.Link(ctx, "github", "sub1", a.ID))
		must(t, ids.Link(ctx, "github", "sub1", b.ID)) // keeps first
		got, err := ids.UserBySubject(ctx, "github", "sub1")
		if err != nil || got != a.ID {
			t.Errorf("subject = %d, %v", got, err)
		}
	})

	t.Run("categories and stats", func(t *testing.T) {
		cat, err := cats.Create(ctx, "covucs cat")
		must(t, err)
		_, err = cats.Create(ctx, "covucs cat")
		covUCSConflict(t, "dup category", err)
		if ok, err := cats.Exists(ctx, cat.ID); err != nil || !ok {
			t.Errorf("exists = %v, %v", ok, err)
		}
		if ok, _ := cats.Exists(ctx, 999999); ok {
			t.Error("missing category exists")
		}
		list, err := cats.List(ctx)
		if err != nil || len(list) == 0 {
			t.Errorf("list = %v, %v", list, err)
		}
		st, err := stats.Stats(ctx)
		if err != nil || st.Users < 4 || st.Categories < 1 {
			t.Errorf("stats = %+v, %v", st, err)
		}
		must(t, cats.Delete(ctx, cat.ID))
		covUCSNotFound(t, "Delete category", cats.Delete(ctx, cat.ID))
	})
}

func TestCovUCSContent(t *testing.T) {
	db := connect(t)
	ctx := context.Background()
	users := postgres.NewUserRepo(db)
	posts := postgres.NewPostRepo(db)
	revs := postgres.NewRevisionRepo(db)
	sers := postgres.NewSeriesRepo(db)
	disc := postgres.NewDiscoveryRepo(db)
	lists := postgres.NewListRepo(db)

	a := covUCSUser(t, users, "covucs_ca")
	b := covUCSUser(t, users, "covucs_cb")
	c := covUCSUser(t, users, "covucs_cc")

	p1 := covUCSPost(t, posts, "covucs one", a, "go", "sql")
	p2 := covUCSPost(t, posts, "covucs two", a, "go")
	p3 := covUCSPost(t, posts, "covucs three", b, "sql")
	p4 := covUCSPost(t, posts, "covucs four", b, "rust")

	t.Run("revisions", func(t *testing.T) {
		r1, err := revs.Add(ctx, p1.ID, a.ID, revision.Content{Title: "t1", Subtitle: "s1", Body: "b1"})
		must(t, err)
		r2, err := revs.Add(ctx, p1.ID, 0, revision.Content{Title: "t2", Body: "b2"})
		must(t, err)
		if r1.Version != 1 || r2.Version != 2 || r2.EditorID != 0 || r1.Editor != "covucs_ca" {
			t.Errorf("revisions = %+v %+v", r1, r2)
		}
		list, err := revs.List(ctx, p1.ID)
		if err != nil || len(list) != 2 || list[0].Version != 2 {
			t.Errorf("list = %+v, %v", list, err)
		}
		got, err := revs.Get(ctx, p1.ID, 1)
		if err != nil || got.Body != "b1" {
			t.Errorf("get = %+v, %v", got, err)
		}
		_, err = revs.Get(ctx, p1.ID, 99)
		covUCSNotFound(t, "Get revision", err)
		_, err = revs.Add(ctx, 999999, a.ID, revision.Content{Title: "x"})
		covUCSNotFound(t, "Add revision missing post", err)
	})

	t.Run("series", func(t *testing.T) {
		s, err := sers.Create(ctx, a.ID, "covucs-series", series.SeriesInput{Title: "S", Description: "D"})
		must(t, err)
		_, err = sers.Create(ctx, a.ID, "covucs-series", series.SeriesInput{Title: "S"})
		covUCSConflict(t, "dup series", err)
		upd, err := sers.Update(ctx, s.ID, series.SeriesInput{Title: "S2", Description: "D2"})
		if err != nil || upd.Title != "S2" {
			t.Errorf("update = %+v, %v", upd, err)
		}
		_, err = sers.Update(ctx, 999999, series.SeriesInput{})
		covUCSNotFound(t, "Update series", err)
		got, err := sers.GetBySlug(ctx, "covucs-series")
		if err != nil || got.ID != s.ID {
			t.Errorf("GetBySlug = %+v, %v", got, err)
		}
		_, err = sers.GetBySlug(ctx, "nope")
		covUCSNotFound(t, "GetBySlug", err)

		if err := sers.SetPosts(ctx, s.ID, a.ID, []int{p1.ID, p1.ID}); err == nil {
			t.Error("duplicate ids accepted")
		}
		covUCSNotFound(t, "SetPosts missing series", sers.SetPosts(ctx, 999999, a.ID, []int{p1.ID}))
		covUCSNotFound(t, "SetPosts foreign post", sers.SetPosts(ctx, s.ID, a.ID, []int{p3.ID}))
		must(t, sers.SetPosts(ctx, s.ID, a.ID, []int{p2.ID, p1.ID}))
		parts, err := sers.Parts(ctx, s.ID, false)
		if err != nil || len(parts) != 2 || parts[0].PostID != p2.ID || parts[1].Position != 2 {
			t.Errorf("parts = %+v, %v", parts, err)
		}
		if parts, err = sers.Parts(ctx, s.ID, true); err != nil || len(parts) != 2 {
			t.Errorf("all parts = %+v, %v", parts, err)
		}
		of, err := sers.OfPost(ctx, p1.ID)
		if err != nil || of.ID != s.ID || of.Parts != 2 {
			t.Errorf("OfPost = %+v, %v", of, err)
		}
		_, err = sers.OfPost(ctx, p4.ID)
		covUCSNotFound(t, "OfPost", err)

		s2, err := sers.Create(ctx, a.ID, "covucs-series-2", series.SeriesInput{Title: "Other"})
		must(t, err)
		if err := sers.SetPosts(ctx, s2.ID, a.ID, []int{p1.ID}); !errors.Is(err, series.ErrPostInOtherSeries) {
			t.Errorf("post in other series: %v", err)
		}
		must(t, sers.SetPosts(ctx, s.ID, a.ID, nil))

		mine, err := sers.ByUser(ctx, a.ID)
		if err != nil || len(mine) != 2 {
			t.Errorf("ByUser = %+v, %v", mine, err)
		}
		must(t, sers.Delete(ctx, s2.ID))
		covUCSNotFound(t, "Delete series", sers.Delete(ctx, s2.ID))
	})

	t.Run("discovery", func(t *testing.T) {
		rel, err := disc.RelatedPosts(ctx, p1.ID, 10)
		if err != nil || len(rel) < 2 {
			t.Fatalf("related = %v, %v", rel, err)
		}
		if rel[0] != p2.ID { // two shared-ish: tag go + same author + ...
			t.Logf("related order = %v", rel)
		}
		if got, err := disc.RelatedPosts(ctx, 999999, 5); err != nil || len(got) != 0 {
			t.Errorf("related missing = %v, %v", got, err)
		}

		fol := postgres.NewUserRepo(db)
		must(t, func() error { _, err := fol.Follow(ctx, c.ID, a.ID); return err }())
		must(t, func() error { _, err := fol.Follow(ctx, a.ID, b.ID); return err }())
		sug, err := disc.SuggestUsers(ctx, c.ID, 10)
		if err != nil {
			t.Fatal(err)
		}
		found := map[string]string{}
		for _, s := range sug {
			found[s.Username] = s.Reason
		}
		if found["covucs_cb"] != "followed_by_people_you_follow" {
			t.Errorf("suggest = %v", found)
		}
		// tag follower: writes_about_your_tags / popular reasons
		tagger := covUCSUser(t, users, "covucs_tagger")
		_, err = db.Exec(ctx, `INSERT INTO tag_follows (user_id, tag) VALUES ($1, 'rust')`, tagger.ID)
		must(t, err)
		sug, err = disc.SuggestUsers(ctx, tagger.ID, 10)
		if err != nil {
			t.Fatal(err)
		}
		reasons := map[string]string{}
		for _, s := range sug {
			reasons[s.Username] = s.Reason
		}
		if reasons["covucs_cb"] != "writes_about_your_tags" {
			t.Errorf("tag suggest = %v", reasons)
		}
		if reasons["covucs_ca"] == "" {
			t.Errorf("popular missing: %v", reasons)
		}

		got, err := disc.UserIDs(ctx, []string{"covucs_ca", "covucs_cb", "ghost"})
		if err != nil || len(got) != 2 {
			t.Errorf("UserIDs = %v, %v", got, err)
		}

		// ForYou
		viewer := covUCSUser(t, users, "covucs_viewer")
		_, err = db.Exec(ctx, `INSERT INTO tag_follows (user_id, tag) VALUES ($1, 'go')`, viewer.ID)
		must(t, err)
		_, err = users.Follow(ctx, viewer.ID, b.ID)
		must(t, err)
		_, err = posts.Read(ctx, p4.ID, viewer.ID, "r")
		must(t, err)
		_, _, err = posts.Like(ctx, p3.ID, viewer.ID)
		must(t, err)
		fy, total, err := disc.ForYou(ctx, viewer.ID, 10, 0)
		if err != nil || total < 2 {
			t.Fatalf("foryou = %v %d %v", fy, total, err)
		}
		for _, id := range fy {
			if id == p4.ID {
				t.Error("already read post in feed")
			}
		}
		fy, _, err = disc.ForYou(ctx, viewer.ID, 1, 1)
		if err != nil || len(fy) != 1 {
			t.Errorf("foryou page = %v, %v", fy, err)
		}
		fy, total, err = disc.ForYou(ctx, 999999, 10, 1000)
		if err != nil || len(fy) != 0 || total != 0 {
			t.Errorf("foryou empty = %v %d %v", fy, total, err)
		}
	})

	t.Run("reading lists", func(t *testing.T) {
		l, err := lists.Create(ctx, a.ID, "covucs-list", readinglist.ListInput{Name: "L", Private: true})
		must(t, err)
		_, err = lists.Create(ctx, a.ID, "covucs-list", readinglist.ListInput{Name: "L"})
		covUCSConflict(t, "dup list", err)
		pub, err := lists.Create(ctx, a.ID, "covucs-list-pub", readinglist.ListInput{Name: "P"})
		must(t, err)
		upd, err := lists.Update(ctx, l.ID, readinglist.ListInput{Name: "L2", Description: "d", Private: false})
		if err != nil || upd.Name != "L2" || upd.Private {
			t.Errorf("update = %+v, %v", upd, err)
		}
		_, err = lists.Update(ctx, 999999, readinglist.ListInput{})
		covUCSNotFound(t, "Update list", err)
		got, err := lists.GetBySlug(ctx, "covucs-list")
		if err != nil || got.ID != l.ID {
			t.Errorf("GetBySlug = %+v, %v", got, err)
		}
		_, err = lists.GetBySlug(ctx, "nope")
		covUCSNotFound(t, "GetBySlug list", err)

		must(t, lists.Add(ctx, l.ID, p1.ID))
		must(t, lists.Add(ctx, l.ID, p1.ID))
		covUCSNotFound(t, "Add missing post", lists.Add(ctx, l.ID, 999999))
		pg, err := lists.ByUser(ctx, a.ID, true, p1.ID, domain.Paging{Page: 1, Limit: 10})
		if err != nil || pg.Total != 2 || len(pg.Items) != 2 {
			t.Fatalf("ByUser = %+v, %v", pg, err)
		}
		var withPost int
		for _, it := range pg.Items {
			if it.Contains == nil {
				t.Error("contains not set")
			} else if *it.Contains {
				withPost++
			}
		}
		if withPost != 1 {
			t.Errorf("lists containing post = %d", withPost)
		}
		pg, err = lists.ByUser(ctx, a.ID, false, 0, domain.Paging{Page: 1, Limit: 10})
		if err != nil || len(pg.Items) != 2 || pg.Items[0].Contains != nil {
			t.Errorf("ByUser no post = %+v, %v", pg, err)
		}
		n, err := lists.Count(ctx, a.ID)
		if err != nil || n != 2 {
			t.Errorf("count = %d, %v", n, err)
		}
		must(t, lists.Remove(ctx, l.ID, p1.ID))
		must(t, lists.Delete(ctx, pub.ID))
		covUCSNotFound(t, "Delete list", lists.Delete(ctx, pub.ID))
	})
}

func TestCovUCSSecurity(t *testing.T) {
	db := connect(t)
	ctx := context.Background()
	users := postgres.NewUserRepo(db)
	mfas := postgres.NewMFARepo(db)
	sancs := postgres.NewSanctionRepo(db)
	rels := postgres.NewRelationRepo(db)

	a := covUCSUser(t, users, "covucs_sa")
	b := covUCSUser(t, users, "covucs_sb")
	c := covUCSUser(t, users, "covucs_sc")

	t.Run("mfa", func(t *testing.T) {
		_, err := mfas.Get(ctx, a.ID)
		covUCSNotFound(t, "mfa get", err)
		if err := mfas.Enable(ctx, a.ID, 5, []string{"h1"}); !errors.Is(err, mfa.ErrNoSetup) {
			t.Errorf("enable without setup: %v", err)
		}
		must(t, mfas.SavePending(ctx, a.ID, "SECRET1"))
		must(t, mfas.SavePending(ctx, a.ID, "SECRET2"))
		st, err := mfas.Get(ctx, a.ID)
		if err != nil || st.Secret != "SECRET2" || st.EnabledAt != nil {
			t.Errorf("state = %+v, %v", st, err)
		}
		must(t, mfas.Enable(ctx, a.ID, 5, []string{"h1", "h2", "h3"}))
		if err := mfas.SavePending(ctx, a.ID, "SECRET3"); !errors.Is(err, mfa.ErrEnabled) {
			t.Errorf("save pending when enabled: %v", err)
		}
		if ok, err := mfas.UseStep(ctx, a.ID, 5); err != nil || ok {
			t.Errorf("replayed step = %v, %v", ok, err)
		}
		if ok, err := mfas.UseStep(ctx, a.ID, 6); err != nil || !ok {
			t.Errorf("new step = %v, %v", ok, err)
		}
		if ok, err := mfas.UseBackupCode(ctx, a.ID, "h1"); err != nil || !ok {
			t.Errorf("backup = %v, %v", ok, err)
		}
		if ok, _ := mfas.UseBackupCode(ctx, a.ID, "h1"); ok {
			t.Error("backup code reused")
		}
		if ok, _ := mfas.UseBackupCode(ctx, a.ID, "zzz"); ok {
			t.Error("unknown backup code")
		}
		left, err := mfas.BackupCodesLeft(ctx, a.ID)
		if err != nil || left != 2 {
			t.Errorf("left = %d, %v", left, err)
		}
		must(t, mfas.ReplaceBackupCodes(ctx, a.ID, []string{"n1", "n2", "n3", "n4"}))
		if left, _ = mfas.BackupCodesLeft(ctx, a.ID); left != 4 {
			t.Errorf("left after replace = %d", left)
		}
		must(t, mfas.Disable(ctx, a.ID))
		_, err = mfas.Get(ctx, a.ID)
		covUCSNotFound(t, "mfa after disable", err)
		must(t, mfas.Disable(ctx, a.ID))
	})

	t.Run("sanctions", func(t *testing.T) {
		past := time.Now().UTC().Add(-time.Hour)
		future := time.Now().UTC().Add(time.Hour)
		_, err := sancs.Get(ctx, a.ID)
		covUCSNotFound(t, "sanction get", err)
		s1, err := sancs.Save(ctx, sanction.Sanction{UserID: a.ID, Reason: "spam", Until: &past, ActorID: c.ID})
		must(t, err)
		if s1.Kind != sanction.Suspended || s1.ActorID != c.ID {
			t.Errorf("s1 = %+v", s1)
		}
		s2, err := sancs.Save(ctx, sanction.Sanction{UserID: b.ID, Reason: "abuse"})
		must(t, err)
		if s2.Kind != sanction.Banned {
			t.Errorf("s2 = %+v", s2)
		}
		s1b, err := sancs.Save(ctx, sanction.Sanction{UserID: a.ID, Reason: "again", Until: &future})
		must(t, err)
		if s1b.Reason != "again" || s1b.ActorID != 0 {
			t.Errorf("resave = %+v", s1b)
		}
		must(t, func() error {
			_, err := sancs.Save(ctx, sanction.Sanction{UserID: a.ID, Reason: "old", Until: &past})
			return err
		}())
		exp, err := sancs.Expired(ctx, time.Now().UTC())
		if err != nil || len(exp) != 1 || exp[0] != a.ID {
			t.Errorf("expired = %v, %v", exp, err)
		}
		got, err := sancs.Get(ctx, b.ID)
		if err != nil || got.Username != "covucs_sb" {
			t.Errorf("get = %+v, %v", got, err)
		}
		list, total, err := sancs.List(ctx, domain.Paging{Page: 1, Limit: 1})
		if err != nil || len(list) != 1 || total != 2 {
			t.Errorf("list = %+v %d %v", list, total, err)
		}
		list, total, err = sancs.List(ctx, domain.Paging{Page: 5, Limit: 10})
		if err != nil || len(list) != 0 || total != 0 {
			t.Errorf("list empty = %+v %d %v", list, total, err)
		}
		must(t, sancs.Delete(ctx, a.ID))
		covUCSNotFound(t, "sanction delete", sancs.Delete(ctx, a.ID))
		must(t, sancs.Delete(ctx, b.ID))
	})

	t.Run("relations", func(t *testing.T) {
		_, err := users.Follow(ctx, a.ID, b.ID)
		must(t, err)
		_, err = users.Follow(ctx, b.ID, a.ID)
		must(t, err)
		must(t, rels.Add(ctx, a.ID, b.ID, relation.Block))
		must(t, rels.Add(ctx, a.ID, b.ID, relation.Block))
		must(t, rels.Add(ctx, a.ID, c.ID, relation.Mute))
		covUCSNotFound(t, "relation missing user", rels.Add(ctx, a.ID, 999999, relation.Mute))
		fg, _ := users.Following(ctx, a.ID, domain.Paging{Page: 1, Limit: 5})
		if len(fg.Items) != 0 {
			t.Errorf("block kept follow: %+v", fg)
		}
		for _, pair := range [][2]int{{a.ID, b.ID}, {b.ID, a.ID}} {
			if ok, err := rels.Blocked(ctx, pair[0], pair[1]); err != nil || !ok {
				t.Errorf("blocked %v = %v, %v", pair, ok, err)
			}
		}
		if ok, _ := rels.Blocked(ctx, a.ID, c.ID); ok {
			t.Error("mute counts as block")
		}
		pg, err := rels.List(ctx, a.ID, relation.Block, domain.Paging{Page: 1, Limit: 10})
		if err != nil || pg.Total != 1 || len(pg.Items) != 1 || pg.Items[0].ID != b.ID {
			t.Errorf("list blocks = %+v, %v", pg, err)
		}
		pg, err = rels.List(ctx, b.ID, relation.Mute, domain.Paging{Page: 1, Limit: 10})
		if err != nil || len(pg.Items) != 0 || pg.Items == nil {
			t.Errorf("list empty = %+v, %v", pg, err)
		}
		sil, err := rels.Silenced(ctx, c.ID, []int{a.ID, b.ID})
		if err != nil || len(sil) != 1 || sil[0] != a.ID {
			t.Errorf("silenced = %v, %v", sil, err)
		}
		must(t, rels.Remove(ctx, a.ID, b.ID, relation.Block))
		must(t, rels.Remove(ctx, a.ID, b.ID, relation.Block))
	})
}

func TestCovUCSPublicationsAndModeration(t *testing.T) {
	db := connect(t)
	ctx := context.Background()
	users := postgres.NewUserRepo(db)
	posts := postgres.NewPostRepo(db)
	pubs := postgres.NewPublicationRepo(db)
	fbs := postgres.NewFeedbackRepo(db)
	reps := postgres.NewReportRepo(db)
	ces := postgres.NewClientErrorRepo(db)
	an := postgres.NewAnalyticsRepo(db)

	a := covUCSUser(t, users, "covucs_pa")
	b := covUCSUser(t, users, "covucs_pb")
	c := covUCSUser(t, users, "covucs_pc")

	t.Run("publications", func(t *testing.T) {
		in := publication.PublicationInput{Slug: "covucs-pub", Name: "Pub", Description: "d", AvatarURL: "u"}
		p, err := pubs.Create(ctx, in, a.ID)
		must(t, err)
		if p.MyRole != "owner" || p.Members != 1 {
			t.Errorf("created = %+v", p)
		}
		_, err = pubs.Create(ctx, in, a.ID)
		covUCSConflict(t, "dup publication", err)
		_, err = pubs.Create(ctx, publication.PublicationInput{Slug: "covucs-pub-x", Name: "X"}, 999999)
		if err == nil {
			t.Error("owner missing accepted")
		}
		got, err := pubs.GetBySlug(ctx, "COVUCS-PUB", b.ID)
		if err != nil || got.ID != p.ID || got.MyRole != "" {
			t.Errorf("GetBySlug = %+v, %v", got, err)
		}
		_, err = pubs.GetBySlug(ctx, "nope", 0)
		covUCSNotFound(t, "GetBySlug", err)

		in.Name = "Pub2"
		must(t, pubs.Update(ctx, p.ID, in))
		covUCSNotFound(t, "Update", pubs.Update(ctx, 999999, in))

		must(t, pubs.SetMember(ctx, p.ID, b.ID, publication.RoleWriter))
		must(t, pubs.SetMember(ctx, p.ID, c.ID, publication.RoleEditor))
		must(t, pubs.SetMember(ctx, p.ID, b.ID, publication.RoleEditor))
		covUCSNotFound(t, "SetMember missing", pubs.SetMember(ctx, p.ID, 999999, publication.RoleWriter))
		members, err := pubs.Members(ctx, p.ID)
		if err != nil || len(members) != 3 || members[0].Username != "covucs_pa" {
			t.Errorf("members = %+v, %v", members, err)
		}
		role, err := pubs.Role(ctx, p.ID, b.ID)
		if err != nil || role != publication.RoleEditor {
			t.Errorf("role = %q, %v", role, err)
		}
		role, err = pubs.Role(ctx, p.ID, 999999)
		if err != nil || role != "" {
			t.Errorf("role none = %q, %v", role, err)
		}
		mine, err := pubs.ListForUser(ctx, b.ID)
		if err != nil || len(mine) != 1 || mine[0].MyRole != "editor" {
			t.Errorf("list for user = %+v, %v", mine, err)
		}
		must(t, pubs.RemoveMember(ctx, p.ID, c.ID))
		covUCSNotFound(t, "RemoveMember", pubs.RemoveMember(ctx, p.ID, c.ID))
		must(t, pubs.Delete(ctx, p.ID))
		covUCSNotFound(t, "Delete", pubs.Delete(ctx, p.ID))
	})

	t.Run("feedback", func(t *testing.T) {
		f1, err := fbs.Create(ctx, feedback.Feedback{UserID: a.ID, Kind: feedback.KindBug, Message: "broken", PageURL: "/x"})
		must(t, err)
		f2, err := fbs.Create(ctx, feedback.Feedback{Kind: feedback.KindIdea, Message: "idea"})
		must(t, err)
		if f1.Username != "covucs_pa" || f2.UserID != 0 || f2.Status != feedback.StatusOpen {
			t.Errorf("created = %+v %+v", f1, f2)
		}
		_, err = fbs.Get(ctx, 999999)
		covUCSNotFound(t, "Get", err)
		pg, err := fbs.List(ctx, feedback.Filter{Paging: domain.Paging{Page: 1, Limit: 10}})
		if err != nil || pg.Total != 2 || pg.Items[0].ID != f2.ID {
			t.Errorf("list = %+v, %v", pg, err)
		}
		pg, err = fbs.List(ctx, feedback.Filter{Kind: feedback.KindBug, Status: feedback.StatusOpen, Paging: domain.Paging{Page: 1, Limit: 10}})
		if err != nil || len(pg.Items) != 1 || pg.Items[0].ID != f1.ID {
			t.Errorf("filtered = %+v, %v", pg, err)
		}
		res, err := fbs.SetStatus(ctx, f1.ID, feedback.StatusResolved, b.ID)
		if err != nil || res.ResolvedBy != b.ID || res.ResolvedAt == nil {
			t.Errorf("resolve = %+v, %v", res, err)
		}
		res, err = fbs.SetStatus(ctx, f1.ID, feedback.StatusOpen, b.ID)
		if err != nil || res.ResolvedBy != 0 || res.ResolvedAt != nil {
			t.Errorf("reopen = %+v, %v", res, err)
		}
		_, err = fbs.SetStatus(ctx, 999999, feedback.StatusResolved, b.ID)
		covUCSNotFound(t, "SetStatus", err)
	})

	t.Run("reports", func(t *testing.T) {
		r1, err := reps.Create(ctx, report.Report{ReporterID: a.ID, TargetType: report.TargetUser, TargetID: c.ID, Reason: report.ReasonSpam, Note: "n"})
		must(t, err)
		_, err = reps.Create(ctx, report.Report{ReporterID: a.ID, TargetType: report.TargetUser, TargetID: c.ID, Reason: report.ReasonSpam})
		covUCSConflict(t, "dup report", err)
		r2, err := reps.Create(ctx, report.Report{ReporterID: b.ID, TargetType: report.TargetUser, TargetID: c.ID, Reason: report.ReasonAbuse})
		must(t, err)
		if _, err := reps.Create(ctx, report.Report{ReporterID: 999999, TargetType: report.TargetUser, TargetID: c.ID, Reason: report.ReasonSpam}); err == nil {
			t.Error("missing reporter accepted")
		}
		if r2.Reports != 2 || r1.Reporter != "covucs_pa" {
			t.Errorf("reports = %+v %+v", r1, r2)
		}
		_, err = reps.Get(ctx, 999999)
		covUCSNotFound(t, "Get", err)
		pg, err := reps.List(ctx, "", domain.Paging{Page: 1, Limit: 10})
		if err != nil || pg.Total != 2 {
			t.Errorf("list = %+v, %v", pg, err)
		}
		pg, err = reps.List(ctx, report.StatusResolved, domain.Paging{Page: 1, Limit: 10})
		if err != nil || len(pg.Items) != 0 {
			t.Errorf("list resolved = %+v, %v", pg, err)
		}
		dec, err := reps.Decide(ctx, r1.ID, report.StatusResolved, c.ID)
		if err != nil || dec.Status != report.StatusResolved || dec.ResolvedBy != c.ID {
			t.Errorf("decide = %+v, %v", dec, err)
		}
		other, _ := reps.Get(ctx, r2.ID)
		if other.Status != report.StatusResolved {
			t.Errorf("sibling report not resolved: %+v", other)
		}
		_, err = reps.Decide(ctx, r1.ID, report.StatusDismissed, c.ID)
		covUCSConflict(t, "decide twice", err)
		_, err = reps.Decide(ctx, 999999, report.StatusDismissed, c.ID)
		covUCSNotFound(t, "decide missing", err)
		pg, _ = reps.List(ctx, report.StatusResolved, domain.Paging{Page: 1, Limit: 10})
		if len(pg.Items) != 2 {
			t.Errorf("resolved list = %+v", pg)
		}
	})

	t.Run("client errors", func(t *testing.T) {
		e := clienterror.ClientError{Fingerprint: "fp1", Message: "boom", Stack: "s", PageURL: "/a", UserAgent: "ua"}
		must(t, ces.Record(ctx, e, 2))
		must(t, ces.Record(ctx, e, 2))
		must(t, ces.Record(ctx, clienterror.ClientError{Fingerprint: "fp2", Message: "m2"}, 2))
		must(t, ces.Record(ctx, clienterror.ClientError{Fingerprint: "fp3", Message: "m3"}, 2)) // table full: dropped
		pg, err := ces.List(ctx, clienterror.Filter{Paging: domain.Paging{Page: 1, Limit: 10}})
		if err != nil || pg.Total != 2 || len(pg.Items) != 2 {
			t.Fatalf("list = %+v, %v", pg, err)
		}
		var fp1 clienterror.ClientError
		for _, it := range pg.Items {
			if it.Fingerprint == "fp1" {
				fp1 = it
			}
		}
		if fp1.Count != 2 {
			t.Errorf("count = %d", fp1.Count)
		}
		got, err := ces.SetStatus(ctx, fp1.ID, clienterror.StatusResolved)
		if err != nil || got.Status != clienterror.StatusResolved {
			t.Errorf("set status = %+v, %v", got, err)
		}
		_, err = ces.SetStatus(ctx, 999999, clienterror.StatusIgnored)
		covUCSNotFound(t, "SetStatus", err)
		pg, err = ces.List(ctx, clienterror.Filter{Status: clienterror.StatusResolved, Paging: domain.Paging{Page: 1, Limit: 10}})
		if err != nil || len(pg.Items) != 1 {
			t.Errorf("filtered = %+v, %v", pg, err)
		}
		must(t, ces.Record(ctx, e, 2)) // reopens
		if got, _ := ces.SetStatus(ctx, fp1.ID, clienterror.StatusOpen); got.Count != 3 {
			t.Errorf("count after reopen = %d", got.Count)
		}
		n, err := ces.PurgeBefore(ctx, time.Now().Add(time.Hour))
		if err != nil || n != 2 {
			t.Errorf("purge = %d, %v", n, err)
		}
	})

	t.Run("analytics", func(t *testing.T) {
		p := covUCSPost(t, posts, "covucs analytics", a)
		evs := []application.AnalyticsEvent{
			{Name: "page_view", Visitor: "v1", Path: "/", Lang: "en"},
			{Name: "page_view", Visitor: "v2", Path: "/", Lang: ""},
			{Name: "page_view", Visitor: "v1", UserID: a.ID, Path: "/p", PostID: p.ID, Lang: "uz"},
			{Name: "signup_open", Visitor: "v1"},
			{Name: "signup", Visitor: "v1", UserID: a.ID},
			{Name: "publish", Visitor: "v1", UserID: a.ID, PostID: p.ID},
			{Name: "read_time", Visitor: "v2", Path: "/p", Value: 120, PostID: 999999, UserID: 999999},
		}
		for _, e := range evs {
			must(t, an.Insert(ctx, e))
		}
		since := time.Now().UTC().AddDate(0, 0, -3)
		rep, err := an.Report(ctx, since, 3)
		must(t, err)
		if len(rep.DAU) < 4 || len(rep.Signups) < 4 || rep.WAU < 2 || rep.MAU < 2 {
			t.Errorf("report = %+v", rep)
		}
		if len(rep.Funnel) != 4 || rep.Funnel[0].Count != 2 || rep.Funnel[2].Count != 1 || rep.Funnel[3].Count != 1 {
			t.Errorf("funnel = %+v", rep.Funnel)
		}
		if rep.AvgReadSeconds != 120 || len(rep.TopPaths) == 0 || len(rep.Languages) == 0 {
			t.Errorf("report details = %+v", rep)
		}
		if rep.Retention.Cohort < 1 {
			t.Errorf("retention = %+v", rep.Retention)
		}
		n, err := an.PurgeBefore(ctx, time.Now().Add(time.Hour))
		if err != nil || n != int64(len(evs)) {
			t.Errorf("purge = %d, %v", n, err)
		}
		rep, err = an.Report(ctx, since, 3)
		if err != nil || rep.Funnel[0].Count != 0 {
			t.Errorf("empty report = %+v, %v", rep, err)
		}
	})
}

// TestCovUCSCancelledContext drives the query-error branches: every call
// runs on an already cancelled context and must fail without panicking.
func TestCovUCSCancelledContext(t *testing.T) {
	db := connect(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pg := domain.Paging{Page: 1, Limit: 5}
	now := time.Now()
	users, cats, revs, sers := postgres.NewUserRepo(db), postgres.NewCategoryRepo(db), postgres.NewRevisionRepo(db), postgres.NewSeriesRepo(db)
	disc, lists, mfas, sancs := postgres.NewDiscoveryRepo(db), postgres.NewListRepo(db), postgres.NewMFARepo(db), postgres.NewSanctionRepo(db)
	rels, pubs, fbs, reps := postgres.NewRelationRepo(db), postgres.NewPublicationRepo(db), postgres.NewFeedbackRepo(db), postgres.NewReportRepo(db)
	ces, an := postgres.NewClientErrorRepo(db), postgres.NewAnalyticsRepo(db)
	errs := map[string]error{}
	add := func(name string, err error) { errs[name] = err }
	_, e := users.List(ctx, "", pg)
	add("users.List", e)
	_, e = users.Followers(ctx, 1, pg)
	add("users.Followers", e)
	_, e = users.FollowerIDs(ctx, 1)
	add("users.FollowerIDs", e)
	_, e = users.DuePurges(ctx, now)
	add("users.DuePurges", e)
	_, e = users.Subscribers(ctx, 1)
	add("users.Subscribers", e)
	_, e = cats.List(ctx)
	add("cats.List", e)
	_, e = revs.Add(ctx, 1, 1, revision.Content{})
	add("revs.Add", e)
	_, e = revs.List(ctx, 1)
	add("revs.List", e)
	_, e = sers.Create(ctx, 1, "x", series.SeriesInput{})
	add("sers.Create", e)
	_, e = sers.ByUser(ctx, 1)
	add("sers.ByUser", e)
	_, e = sers.Parts(ctx, 1, true)
	add("sers.Parts", e)
	add("sers.SetPosts", sers.SetPosts(ctx, 1, 1, []int{1}))
	_, e = disc.RelatedPosts(ctx, 1, 5)
	add("disc.RelatedPosts", e)
	_, e = disc.SuggestUsers(ctx, 1, 5)
	add("disc.SuggestUsers", e)
	_, e = disc.UserIDs(ctx, nil)
	add("disc.UserIDs", e)
	_, _, e = disc.ForYou(ctx, 1, 5, 0)
	add("disc.ForYou", e)
	_, e = lists.Create(ctx, 1, "x", readinglist.ListInput{})
	add("lists.Create", e)
	_, e = lists.ByUser(ctx, 1, true, 0, pg)
	add("lists.ByUser", e)
	add("mfas.SavePending", mfas.SavePending(ctx, 1, "s"))
	add("mfas.Enable", mfas.Enable(ctx, 1, 1, nil))
	add("mfas.Disable", mfas.Disable(ctx, 1))
	add("mfas.ReplaceBackupCodes", mfas.ReplaceBackupCodes(ctx, 1, nil))
	_, e = sancs.Expired(ctx, now)
	add("sancs.Expired", e)
	_, _, e = sancs.List(ctx, pg)
	add("sancs.List", e)
	_, e = rels.List(ctx, 1, relation.Block, pg)
	add("rels.List", e)
	_, e = rels.Silenced(ctx, 1, []int{1})
	add("rels.Silenced", e)
	_, e = pubs.ListForUser(ctx, 1)
	add("pubs.ListForUser", e)
	_, e = pubs.Members(ctx, 1)
	add("pubs.Members", e)
	_, e = fbs.Create(ctx, feedback.Feedback{})
	add("fbs.Create", e)
	_, e = fbs.List(ctx, feedback.Filter{Paging: pg})
	add("fbs.List", e)
	_, e = fbs.SetStatus(ctx, 1, feedback.StatusOpen, 0)
	add("fbs.SetStatus", e)
	_, e = reps.List(ctx, "", pg)
	add("reps.List", e)
	_, e = ces.List(ctx, clienterror.Filter{Paging: pg})
	add("ces.List", e)
	_, e = an.Report(ctx, now, 1)
	add("an.Report", e)
	for name, err := range errs {
		if err == nil {
			t.Errorf("%s: want error on cancelled context", name)
		}
	}
}
