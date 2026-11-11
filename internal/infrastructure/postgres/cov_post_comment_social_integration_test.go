package postgres_test

// Extra coverage for post.go, comment.go and social.go against a real Postgres.

import (
	"context"
	"errors"
	"fmt"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/category"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/comment"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/post"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/publication"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/social"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/postgres"
)

func covPCSIs(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("err = %v, want %v", got, want)
	}
}

func TestCovPCSPosts(t *testing.T) {
	db := connect(t)
	ctx := context.Background()
	users := postgres.NewUserRepo(db)
	posts := postgres.NewPostRepo(db)
	cats := postgres.NewCategoryRepo(db)
	pubs := postgres.NewPublicationRepo(db)
	social := postgres.NewSocialRepo(db)

	u1, err := users.Create(ctx, "covpcs_a", "covpcs_a@x.io")
	must(t, err)
	u2, err := users.Create(ctx, "covpcs_b", "covpcs_b@x.io")
	must(t, err)
	cat, err := cats.Create(ctx, category.Input{Name: "covpcs-cat", Slug: "covpcs-cat"})
	must(t, err)
	pub, err := pubs.Create(ctx, publication.PublicationInput{Slug: "covpcs-pub", Name: "Pub"}, u1.ID)
	must(t, err)
	lbl, err := posts.CreateLabel(ctx, post.Label{Name: "covpcs-l1", Color: "#fff"})
	must(t, err)
	lbl2, err := posts.CreateLabel(ctx, post.Label{Name: "covpcs-l2", Color: "#000"})
	must(t, err)

	mk := func(title, body string, tags []string, uid int, d post.Draft) post.Post {
		t.Helper()
		d.Title, d.Body, d.Tags = title, body, tags
		p, err := create(t, posts, d, uid, "x")
		must(t, err)
		return p
	}
	p1 := mk("Alpha golang", "body one", []string{"go", "db"}, u1.ID,
		post.Draft{Status: post.StatusPublished, CategoryID: cat.ID, PublicationID: pub.ID, LabelIDs: []int{lbl.ID}})
	p2 := mk("Beta rust", "body two", []string{"rust"}, u2.ID, post.Draft{Status: post.StatusPublished})
	p3 := mk("Gamma draft", "body three", []string{"go"}, u1.ID, post.Draft{Status: post.StatusDraft})
	past := time.Now().Add(-time.Hour)
	p4 := mk("Delta sched", "body four", nil, u1.ID, post.Draft{Status: post.StatusScheduled, PublishAt: &past})

	t.Run("labels", func(t *testing.T) {
		_, err := posts.CreateLabel(ctx, post.Label{Name: "covpcs-l1", Color: "#fff"})
		covPCSIs(t, err, domain.ErrConflict)
		ls, err := posts.Labels(ctx)
		must(t, err)
		if len(ls) < 2 {
			t.Fatal(ls)
		}
		tmp, err := posts.CreateLabel(ctx, post.Label{Name: "covpcs-tmp", Color: "#111"})
		must(t, err)
		must(t, posts.DeleteLabel(ctx, tmp.ID))
		covPCSIs(t, posts.DeleteLabel(ctx, tmp.ID), domain.ErrNotFound)
	})

	t.Run("list", func(t *testing.T) {
		pg := domain.NewPaging(1, 10)
		must(t, social.Bookmark(ctx, u2.ID, p1.ID))
		must(t, social.FollowTag(ctx, u2.ID, "rust"))
		_, err := db.Exec(ctx, `INSERT INTO follows (follower_id, followee_id) VALUES ($1, $2)`, u2.ID, u1.ID)
		must(t, err)
		var listID int
		must(t, db.QueryRow(ctx, `INSERT INTO lists (user_id, slug, name) VALUES ($1, 'covpcs-list', 'L') RETURNING id`, u1.ID).Scan(&listID))
		_, err = db.Exec(ctx, `INSERT INTO list_items (list_id, post_id) VALUES ($1, $2)`, listID, p2.ID)
		must(t, err)
		must(t, social.Hide(ctx, u2.ID, "post", fmt.Sprint(p1.ID)))

		cases := []struct {
			name string
			f    post.Filter
			want int
		}{
			{"default", post.Filter{Paging: pg}, 2},
			{"draft", post.Filter{Status: post.StatusDraft, Paging: pg}, 1},
			{"query", post.Filter{Query: "golang", Paging: pg}, 1},
			{"tag", post.Filter{Tag: "go", Paging: pg}, 1},
			{"category", post.Filter{CategoryID: cat.ID, Paging: pg}, 1},
			{"user", post.Filter{UserID: u2.ID, Paging: pg}, 1},
			{"feed", post.Filter{FeedOf: u2.ID, Paging: pg}, 2},
			{"label", post.Filter{LabelID: lbl.ID, Paging: pg}, 1},
			{"bookmarked", post.Filter{BookmarkedBy: u2.ID, Paging: pg}, 1},
			{"publication", post.Filter{PublicationID: pub.ID, Paging: pg}, 1},
			{"inlist", post.Filter{InList: listID, Paging: pg}, 1},
			{"hidefor", post.Filter{HideFor: u2.ID, Paging: pg}, 1},
		}
		for _, c := range cases {
			got, err := posts.List(ctx, c.f)
			must(t, err)
			if len(got.Items) != c.want {
				t.Errorf("%s: got %d want %d", c.name, len(got.Items), c.want)
			}
		}
		// cursor paging
		first, err := posts.List(ctx, post.Filter{Paging: domain.NewPaging(1, 1)})
		must(t, err)
		if first.NextCursor == "" {
			t.Fatal("want next cursor")
		}
		next, err := posts.List(ctx, post.Filter{Paging: domain.Paging{Limit: 1, Cursor: first.NextCursor}})
		must(t, err)
		if len(next.Items) != 1 || next.Items[0].ID == first.Items[0].ID {
			t.Fatalf("cursor page: %+v", next.Items)
		}
		_, err = posts.List(ctx, post.Filter{Query: "x", Paging: domain.Paging{Limit: 1, Cursor: first.NextCursor}})
		if err == nil {
			t.Fatal("cursor+q must fail")
		}
		_, err = posts.List(ctx, post.Filter{Paging: domain.Paging{Limit: 1, Cursor: "!!"}})
		if err == nil {
			t.Fatal("bad cursor must fail")
		}
	})

	t.Run("list-by-ids-get", func(t *testing.T) {
		got, err := posts.ListByIDs(ctx, []int{p2.ID, p1.ID, p3.ID})
		must(t, err)
		if len(got) != 2 || got[0].ID != p2.ID {
			t.Fatalf("%+v", got)
		}
		g, err := posts.GetBySlug(ctx, p1.Slug, u1.ID)
		must(t, err)
		if g.ID != p1.ID || len(g.Labels) != 1 {
			t.Fatalf("%+v", g)
		}
		_, err = posts.GetBySlug(ctx, "nope", 0)
		covPCSIs(t, err, domain.ErrNotFound)
	})

	t.Run("update", func(t *testing.T) {
		d := post.Draft{Title: "Alpha2", Body: "new body", Status: post.StatusPublished, LabelIDs: []int{lbl2.ID}, Tags: []string{"x"}}
		u, err := posts.Update(ctx, p1.ID, d)
		must(t, err)
		if u.Title != "Alpha2" || len(u.Labels) != 1 || u.Labels[0].ID != lbl2.ID {
			t.Fatalf("%+v", u)
		}
		_, err = posts.Update(ctx, 999999, d)
		covPCSIs(t, err, domain.ErrNotFound)
		d.CategoryID = 999999
		_, err = posts.Update(ctx, p1.ID, d)
		covPCSIs(t, err, post.ErrUnknownCategory)
		d.CategoryID, d.PublicationID = 0, 999999
		_, err = posts.Update(ctx, p1.ID, d)
		covPCSIs(t, err, domain.ErrNotFound)
		d.PublicationID, d.LabelIDs = 0, []int{999999}
		_, err = posts.Update(ctx, p1.ID, d)
		covPCSIs(t, err, post.ErrUnknownLabel)
		_, err = create(t, posts, post.Draft{Title: "bad", Status: post.StatusPublished, CategoryID: 999999}, u1.ID, "x")
		covPCSIs(t, err, post.ErrUnknownCategory)
	})

	t.Run("update-with-revision", func(t *testing.T) {
		d := post.Draft{Title: "Gamma draft", Body: "edited", Status: post.StatusDraft, Tags: []string{}}
		_, err := posts.UpdateWithRevision(ctx, p3.ID, u1.ID, d)
		must(t, err)
		// second draft save absorbed by recent revision
		d.Body = "edited again"
		_, err = posts.UpdateWithRevision(ctx, p3.ID, u1.ID, d)
		must(t, err)
		var n int
		must(t, db.QueryRow(ctx, `SELECT count(*) FROM post_revisions WHERE post_id=$1`, p3.ID).Scan(&n))
		if n != 1 {
			t.Fatalf("revisions = %d", n)
		}
		// unchanged text: no revision
		_, err = posts.UpdateWithRevision(ctx, p3.ID, 0, d)
		must(t, err)
		// publish: not draft->draft, so revision stored (editor 0)
		d.Status, d.Body = post.StatusPublished, "published body"
		_, err = posts.UpdateWithRevision(ctx, p3.ID, 0, d)
		must(t, err)
		must(t, db.QueryRow(ctx, `SELECT count(*) FROM post_revisions WHERE post_id=$1`, p3.ID).Scan(&n))
		if n != 2 {
			t.Fatalf("revisions = %d", n)
		}
		_, err = posts.UpdateWithRevision(ctx, 999999, u1.ID, d)
		covPCSIs(t, err, domain.ErrNotFound)
		d.CategoryID = 999999
		_, err = posts.UpdateWithRevision(ctx, p3.ID, u1.ID, d)
		covPCSIs(t, err, post.ErrUnknownCategory)
	})

	t.Run("scheduled", func(t *testing.T) {
		ids, err := posts.DueScheduled(ctx, time.Now())
		must(t, err)
		if len(ids) != 1 || ids[0] != p4.ID {
			t.Fatalf("%v", ids)
		}
	})

	t.Run("likes-claps", func(t *testing.T) {
		p, created, err := posts.Like(ctx, p2.ID, u1.ID)
		must(t, err)
		if !created || p.Likes != 1 || !p.Liked {
			t.Fatalf("%+v", p)
		}
		_, created, err = posts.Like(ctx, p2.ID, u1.ID)
		must(t, err)
		if created {
			t.Fatal("dup like created")
		}
		_, _, err = posts.Like(ctx, 999999, u1.ID)
		covPCSIs(t, err, domain.ErrNotFound)
		_, _, err = posts.Clap(ctx, 999999, u1.ID, 3)
		covPCSIs(t, err, domain.ErrNotFound)

		// new clap by u2, then more, then cap
		p, created, err = posts.Clap(ctx, p1.ID, u2.ID, 5)
		must(t, err)
		if !created || p.MyClaps != 5 {
			t.Fatalf("%+v", p)
		}
		p, created, err = posts.Clap(ctx, p1.ID, u2.ID, 10)
		must(t, err)
		if created || p.MyClaps != 15 {
			t.Fatalf("%+v", p)
		}
		p, _, err = posts.Clap(ctx, p1.ID, u2.ID, 1000)
		must(t, err)
		if p.MyClaps != post.MaxClaps {
			t.Fatalf("%+v", p)
		}
		before := p.Claps
		p, _, err = posts.Clap(ctx, p1.ID, u2.ID, 5) // already at cap: added == 0
		must(t, err)
		if p.Claps != before {
			t.Fatalf("%+v", p)
		}
		p, err = posts.Unlike(ctx, p1.ID, u2.ID)
		must(t, err)
		if p.MyClaps != 0 || p.Claps != 0 {
			t.Fatalf("%+v", p)
		}
		_, err = posts.Unlike(ctx, 999999, u2.ID)
		covPCSIs(t, err, domain.ErrNotFound)
	})

	t.Run("tags", func(t *testing.T) {
		tc, err := posts.Tags(ctx)
		must(t, err)
		if len(tc) == 0 {
			t.Fatal("no tags")
		}
	})

	t.Run("highlights", func(t *testing.T) {
		h, err := posts.AddHighlight(ctx, post.Highlight{UserID: u1.ID, PostID: p2.ID, Text: "body", Start: 0, End: 4})
		must(t, err)
		_, err = posts.AddHighlight(ctx, post.Highlight{UserID: u2.ID, PostID: p2.ID, Text: "body", Start: 0, End: 4})
		must(t, err)
		_, err = posts.AddHighlight(ctx, post.Highlight{UserID: u1.ID, PostID: 999999, Text: "x", Start: 0, End: 1})
		covPCSIs(t, err, domain.ErrNotFound)
		got, err := posts.GetHighlight(ctx, h.ID)
		must(t, err)
		if got.Text != "body" {
			t.Fatal(got)
		}
		_, err = posts.GetHighlight(ctx, 999999)
		covPCSIs(t, err, domain.ErrNotFound)
		hs, err := posts.Highlights(ctx, p2.ID, u1.ID, 5)
		must(t, err)
		if len(hs.Top) != 1 || hs.Top[0].Count != 2 || len(hs.Mine) != 1 {
			t.Fatalf("%+v", hs)
		}
		hs, err = posts.Highlights(ctx, p2.ID, 0, 5)
		must(t, err)
		if len(hs.Mine) != 0 {
			t.Fatalf("%+v", hs)
		}
		must(t, posts.DeleteHighlight(ctx, h.ID))
		covPCSIs(t, posts.DeleteHighlight(ctx, h.ID), domain.ErrNotFound)
	})

	t.Run("reads-stats", func(t *testing.T) {
		ok, err := posts.Read(ctx, p2.ID, u1.ID, "r1")
		must(t, err)
		if !ok {
			t.Fatal("first read not counted")
		}
		ok, err = posts.Read(ctx, p2.ID, u1.ID, "r1")
		must(t, err)
		if ok {
			t.Fatal("dup read counted")
		}
		_, err = posts.Read(ctx, p2.ID, 0, "anon")
		must(t, err)
		_, err = posts.Read(ctx, 999999, 0, "anon")
		covPCSIs(t, err, domain.ErrNotFound)
		days, err := posts.DailyReads(ctx, p2.ID, time.Now().Add(-48*time.Hour))
		must(t, err)
		total := 0
		for _, n := range days {
			total += n
		}
		if total != 2 {
			t.Fatalf("%v", days)
		}
		stats, total2, err := posts.AuthorStats(ctx, u2.ID, domain.NewPaging(1, 10))
		must(t, err)
		if total2 != 1 || len(stats) != 1 || stats[0].Reads != 2 {
			t.Fatalf("%+v %d", stats, total2)
		}
	})

	t.Run("delete", func(t *testing.T) {
		must(t, posts.Delete(ctx, p4.ID))
		covPCSIs(t, posts.Delete(ctx, p4.ID), domain.ErrNotFound)
	})
}

func TestCovPCSCommentsSocial(t *testing.T) {
	db := connect(t)
	ctx := context.Background()
	users := postgres.NewUserRepo(db)
	posts := postgres.NewPostRepo(db)
	comments := postgres.NewCommentRepo(db)
	soc := postgres.NewSocialRepo(db)

	u1, err := users.Create(ctx, "covpcs_c", "covpcs_c@x.io")
	must(t, err)
	u2, err := users.Create(ctx, "covpcs_d", "covpcs_d@x.io")
	must(t, err)
	p, err := create(t, posts, post.Draft{Title: "Cmt post", Body: "b", Status: post.StatusPublished}, u1.ID, "x")
	must(t, err)
	p2, err := create(t, posts, post.Draft{Title: "Other post", Body: "b", Status: post.StatusPublished}, u1.ID, "x")
	must(t, err)

	t.Run("comments", func(t *testing.T) {
		c1, err := comments.Add(ctx, comment.Comment{PostID: p.ID, UserID: u1.ID, Author: "a", Text: "one"})
		must(t, err)
		c2, err := comments.Add(ctx, comment.Comment{PostID: p.ID, ParentID: c1.ID, UserID: u2.ID, Author: "b", Text: "two"})
		must(t, err)
		c3, err := comments.Add(ctx, comment.Comment{PostID: p.ID, Author: "anon", Text: "three"})
		must(t, err)
		_, err = comments.Add(ctx, comment.Comment{PostID: p2.ID, ParentID: c1.ID, Author: "a", Text: "bad parent"})
		covPCSIs(t, err, comment.ErrBadParent)
		_, err = comments.Add(ctx, comment.Comment{PostID: 999999, Author: "a", Text: "no post"})
		covPCSIs(t, err, domain.ErrNotFound)

		got, err := comments.Get(ctx, c1.ID)
		must(t, err)
		if got.Text != "one" || got.Author != u1.Username {
			t.Fatalf("%+v", got)
		}
		_, err = comments.Get(ctx, 999999)
		covPCSIs(t, err, domain.ErrNotFound)

		pg, err := comments.ListByPost(ctx, p.ID, u1.ID, domain.NewPaging(1, 2))
		must(t, err)
		if len(pg.Items) != 2 || pg.Total != 3 || pg.NextCursor == "" || pg.Items[0].ID != c1.ID {
			t.Fatalf("%+v", pg)
		}
		pg2, err := comments.ListByPost(ctx, p.ID, 0, domain.Paging{Limit: 2, Cursor: pg.NextCursor})
		must(t, err)
		if len(pg2.Items) != 1 || pg2.Items[0].ID != c3.ID {
			t.Fatalf("%+v", pg2)
		}
		_, err = comments.ListByPost(ctx, p.ID, 0, domain.Paging{Limit: 2, Cursor: "!!"})
		if err == nil {
			t.Fatal("bad cursor")
		}
		_, err = comments.ListByPost(ctx, 999999, 0, domain.NewPaging(1, 2))
		covPCSIs(t, err, domain.ErrNotFound)

		all, err := comments.ListAll(ctx, domain.NewPaging(1, 2))
		must(t, err)
		if len(all.Items) != 2 || all.Items[0].ID != c3.ID || all.NextCursor == "" {
			t.Fatalf("%+v", all)
		}
		all2, err := comments.ListAll(ctx, domain.Paging{Limit: 2, Cursor: all.NextCursor})
		must(t, err)
		if len(all2.Items) != 1 {
			t.Fatalf("%+v", all2)
		}

		e, err := comments.Edit(ctx, c1.ID, "edited")
		must(t, err)
		if e.Text != "edited" || e.EditedAt == nil {
			t.Fatalf("%+v", e)
		}
		_, err = comments.Edit(ctx, 999999, "x")
		covPCSIs(t, err, domain.ErrNotFound)

		l, created, err := comments.Like(ctx, c1.ID, u2.ID)
		must(t, err)
		if !created || l.Likes != 1 || !l.Liked {
			t.Fatalf("%+v", l)
		}
		_, created, err = comments.Like(ctx, c1.ID, u2.ID)
		must(t, err)
		if created {
			t.Fatal("dup like")
		}
		_, _, err = comments.Like(ctx, 999999, u2.ID)
		covPCSIs(t, err, domain.ErrNotFound)
		l, err = comments.Unlike(ctx, c1.ID, u2.ID)
		must(t, err)
		if l.Likes != 0 {
			t.Fatalf("%+v", l)
		}
		_, err = comments.Unlike(ctx, 999999, u2.ID)
		covPCSIs(t, err, domain.ErrNotFound)

		must(t, comments.Delete(ctx, c1.ID)) // cascades to c2
		covPCSIs(t, comments.Delete(ctx, c1.ID), domain.ErrNotFound)
		_, err = comments.Get(ctx, c2.ID)
		covPCSIs(t, err, domain.ErrNotFound)
	})

	t.Run("bookmarks-tags", func(t *testing.T) {
		must(t, soc.Bookmark(ctx, u1.ID, p.ID))
		must(t, soc.Bookmark(ctx, u1.ID, p.ID))
		covPCSIs(t, soc.Bookmark(ctx, u1.ID, 999999), domain.ErrNotFound)
		must(t, soc.Unbookmark(ctx, u1.ID, p.ID))
		must(t, soc.FollowTag(ctx, u1.ID, "b"))
		must(t, soc.FollowTag(ctx, u1.ID, "a"))
		must(t, soc.FollowTag(ctx, u1.ID, "a"))
		tags, err := soc.FollowedTags(ctx, u1.ID)
		must(t, err)
		if len(tags) != 2 || tags[0] != "a" {
			t.Fatal(tags)
		}
		must(t, soc.UnfollowTag(ctx, u1.ID, "a"))
		tags, err = soc.FollowedTags(ctx, u1.ID)
		must(t, err)
		if len(tags) != 1 {
			t.Fatal(tags)
		}
	})

	t.Run("notifications", func(t *testing.T) {
		ns, err := soc.Notify(ctx, social.Notification{Type: social.NotifyNewPost, ActorID: u2.ID, PostID: p.ID}, []int{u1.ID})
		must(t, err)
		if len(ns) != 1 || ns[0].Actor != u2.Username {
			t.Fatalf("%+v", ns)
		}
		ns, err = soc.Notify(ctx, social.Notification{Type: social.NotifyNewPost, ActorID: u2.ID, PostID: p.ID}, []int{u1.ID})
		must(t, err)
		if len(ns) != 0 {
			t.Fatalf("dedupe failed: %+v", ns)
		}
		for i := 0; i < 3; i++ {
			_, err = soc.Notify(ctx, social.Notification{Type: social.NotifyLike, ActorID: u2.ID, PostID: p.ID}, []int{u1.ID})
			must(t, err)
		}
		dn, err := soc.Notify(ctx, social.Notification{Type: social.NotifyDiagramComment, ActorID: u2.ID, Text: "hi"}, []int{u1.ID})
		must(t, err)
		dn2, err := soc.Notify(ctx, social.Notification{Type: social.NotifyDiagramReply, Text: "re"}, []int{u1.ID})
		must(t, err)

		pg, err := soc.Notifications(ctx, u1.ID, domain.NewPaging(1, 2))
		must(t, err)
		if len(pg.Items) != 2 || pg.Total != 6 || pg.Unread != 6 || pg.NextCursor == "" {
			t.Fatalf("%+v", pg)
		}
		pg2, err := soc.Notifications(ctx, u1.ID, domain.Paging{Limit: 2, Cursor: pg.NextCursor})
		must(t, err)
		if len(pg2.Items) != 2 {
			t.Fatalf("%+v", pg2)
		}
		_, err = soc.Notifications(ctx, u1.ID, domain.Paging{Limit: 2, Cursor: "!!"})
		if err == nil {
			t.Fatal("bad cursor")
		}

		items, unread, err := soc.DiagramNotifications(ctx, u1.ID, false, 10)
		must(t, err)
		if len(items) != 2 || unread != 2 {
			t.Fatalf("%+v %d", items, unread)
		}
		must(t, soc.MarkDiagramRead(ctx, u1.ID, nil, false)) // no-op
		must(t, soc.MarkDiagramRead(ctx, u1.ID, []int64{dn[0].ID}, false))
		items, unread, err = soc.DiagramNotifications(ctx, u1.ID, true, 10)
		must(t, err)
		if len(items) != 1 || unread != 1 || items[0].ID != dn2[0].ID {
			t.Fatalf("%+v %d", items, unread)
		}
		must(t, soc.MarkDiagramRead(ctx, u1.ID, nil, true))
		_, unread, err = soc.DiagramNotifications(ctx, u1.ID, true, 10)
		must(t, err)
		if unread != 0 {
			t.Fatal(unread)
		}

		must(t, soc.MarkRead(ctx, u1.ID, nil)) // no-op
		must(t, soc.MarkRead(ctx, u1.ID, []int64{pg2.Items[0].ID}))
		pg, err = soc.Notifications(ctx, u1.ID, domain.NewPaging(1, 10))
		must(t, err)
		if pg.Unread != 3 {
			t.Fatalf("unread %d", pg.Unread)
		}
		must(t, soc.MarkAllRead(ctx, u1.ID))
		pg, err = soc.Notifications(ctx, u1.ID, domain.NewPaging(1, 10))
		must(t, err)
		if pg.Unread != 0 {
			t.Fatalf("unread %d", pg.Unread)
		}
	})

	t.Run("hides", func(t *testing.T) {
		must(t, soc.Hide(ctx, u1.ID, social.HidePost, fmt.Sprint(p.ID)))
		must(t, soc.Hide(ctx, u1.ID, social.HidePost, fmt.Sprint(p.ID)))
		must(t, soc.Hide(ctx, u1.ID, social.HideAuthor, fmt.Sprint(u2.ID)))
		must(t, soc.Hide(ctx, u1.ID, social.HideTag, "go"))
		pg, err := soc.Hidden(ctx, u1.ID, domain.NewPaging(1, 2))
		must(t, err)
		if len(pg.Items) != 2 || pg.Total != 3 {
			t.Fatalf("%+v", pg)
		}
		found := false
		all, err := soc.Hidden(ctx, u1.ID, domain.NewPaging(1, 10))
		must(t, err)
		for _, h := range all.Items {
			if h.Kind == social.HideAuthor && h.Target == u2.Username {
				found = true
			}
		}
		if !found {
			t.Fatalf("author name not resolved: %+v", all)
		}
		past, err := soc.Hidden(ctx, u1.ID, domain.NewPaging(5, 2)) // past the end
		must(t, err)
		if len(past.Items) != 0 || past.Total != 3 {
			t.Fatalf("%+v", past)
		}
		n, err := soc.CountHidden(ctx, u1.ID)
		must(t, err)
		if n != 3 {
			t.Fatal(n)
		}
		must(t, soc.Unhide(ctx, u1.ID, social.HideTag, "go"))
		n, err = soc.CountHidden(ctx, u1.ID)
		must(t, err)
		if n != 2 {
			t.Fatal(n)
		}
	})
}

// Every repo method returns the database error (here: canceled context).
func TestCovPCSErrors(t *testing.T) {
	db := connect(t)
	posts := postgres.NewPostRepo(db)
	comments := postgres.NewCommentRepo(db)
	soc := postgres.NewSocialRepo(db)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pg := domain.NewPaging(1, 5)
	d := post.Draft{Title: "t", Status: post.StatusDraft, Tags: []string{}}
	calls := map[string]func() error{
		"List":         func() error { _, e := posts.List(ctx, post.Filter{Paging: pg}); return e },
		"ListByIDs":    func() error { _, e := posts.ListByIDs(ctx, []int{1}); return e },
		"Get":          func() error { _, e := posts.Get(ctx, 1, 0); return e },
		"Create":       func() error { _, e := posts.Create(ctx, d, 1, "x"); return e },
		"Update":       func() error { _, e := posts.Update(ctx, 1, d); return e },
		"UpdateRev":    func() error { _, e := posts.UpdateWithRevision(ctx, 1, 1, d); return e },
		"Delete":       func() error { return posts.Delete(ctx, 1) },
		"Publish":      func() error { _, e := posts.Publish(ctx, 1, time.Now()); return e },
		"DueScheduled": func() error { _, e := posts.DueScheduled(ctx, time.Now()); return e },
		"Like":         func() error { _, _, e := posts.Like(ctx, 1, 1); return e },
		"Clap":         func() error { _, _, e := posts.Clap(ctx, 1, 1, 1); return e },
		"Unlike":       func() error { _, e := posts.Unlike(ctx, 1, 1); return e },
		"Tags":         func() error { _, e := posts.Tags(ctx); return e },
		"Labels":       func() error { _, e := posts.Labels(ctx); return e },
		"CreateLabel":  func() error { _, e := posts.CreateLabel(ctx, post.Label{Name: "e"}); return e },
		"AddHighlight": func() error { _, e := posts.AddHighlight(ctx, post.Highlight{}); return e },
		"GetHighlight": func() error { _, e := posts.GetHighlight(ctx, 1); return e },
		"Highlights":   func() error { _, e := posts.Highlights(ctx, 1, 1, 1); return e },
		"AuthorStats":  func() error { _, _, e := posts.AuthorStats(ctx, 1, pg); return e },
		"Read":         func() error { _, e := posts.Read(ctx, 1, 1, "r"); return e },
		"DailyReads":   func() error { _, e := posts.DailyReads(ctx, 1, time.Now()); return e },
		"CListByPost":  func() error { _, e := comments.ListByPost(ctx, 1, 0, pg); return e },
		"CListAll":     func() error { _, e := comments.ListAll(ctx, pg); return e },
		"CGet":         func() error { _, e := comments.Get(ctx, 1); return e },
		"CAdd":         func() error { _, e := comments.Add(ctx, comment.Comment{PostID: 1}); return e },
		"CEdit":        func() error { _, e := comments.Edit(ctx, 1, "x"); return e },
		"CDelete":      func() error { return comments.Delete(ctx, 1) },
		"CLike":        func() error { _, _, e := comments.Like(ctx, 1, 1); return e },
		"CUnlike":      func() error { _, e := comments.Unlike(ctx, 1, 1); return e },
		"Bookmark":     func() error { return soc.Bookmark(ctx, 1, 1) },
		"FollowedTags": func() error { _, e := soc.FollowedTags(ctx, 1); return e },
		"Notify": func() error {
			_, e := soc.Notify(ctx, social.Notification{Type: social.NotifyLike}, []int{1})
			return e
		},
		"Notifications":   func() error { _, e := soc.Notifications(ctx, 1, pg); return e },
		"DiagramNotifs":   func() error { _, _, e := soc.DiagramNotifications(ctx, 1, false, 5); return e },
		"MarkDiagramRead": func() error { return soc.MarkDiagramRead(ctx, 1, nil, true) },
		"MarkRead":        func() error { return soc.MarkRead(ctx, 1, []int64{1}) },
		"MarkAllRead":     func() error { return soc.MarkAllRead(ctx, 1) },
		"Hide":            func() error { return soc.Hide(ctx, 1, social.HideTag, "x") },
		"Hidden":          func() error { _, e := soc.Hidden(ctx, 1, pg); return e },
		"CountHidden":     func() error { _, e := soc.CountHidden(ctx, 1); return e },
	}
	for name, fn := range calls {
		if err := fn(); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}
