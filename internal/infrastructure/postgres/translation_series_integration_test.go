package postgres_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/post"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/series"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/postgres"
)

func TestPostTranslations(t *testing.T) {
	db := connect(t)
	ctx := context.Background()
	users, posts := postgres.NewUserRepo(db), postgres.NewPostRepo(db)
	u, err := users.Create(ctx, "tr_author", "tr@example.com")
	must(t, err)
	p, err := create(t, posts, post.Draft{Title: "Salom", Body: "matn", Status: post.StatusPublished}, u.ID, u.Username)
	must(t, err)
	if p.Lang != "uz" || !slices.Equal(p.Languages, []string{"uz"}) {
		t.Fatalf("new post lang = %q %v", p.Lang, p.Languages)
	}

	if _, err := posts.GetTranslation(ctx, p.ID, "en"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing translation: %v", err)
	}
	tr, err := posts.SaveTranslation(ctx, p.ID, "en", post.TranslationInput{Title: "Hello", Body: "one two"})
	if err != nil || tr.Title != "Hello" || tr.ReadingTime != 1 {
		t.Fatalf("save = %+v %v", tr, err)
	}
	tr, err = posts.SaveTranslation(ctx, p.ID, "en", post.TranslationInput{Title: "Hello 2", Subtitle: "s", Body: "b"})
	if err != nil || tr.Title != "Hello 2" || tr.Subtitle != "s" {
		t.Fatalf("upsert = %+v %v", tr, err)
	}
	_, err = posts.SaveTranslation(ctx, p.ID, "ru", post.TranslationInput{Title: "Привет"})
	must(t, err)
	got, err := posts.Get(ctx, p.ID, 0)
	if err != nil || !slices.Equal(got.Languages, []string{"uz", "en", "ru"}) {
		t.Fatalf("languages = %v %v", got.Languages, err)
	}
	if tr, err := posts.GetTranslation(ctx, p.ID, "en"); err != nil || tr.Title != "Hello 2" || tr.Body != "b" {
		t.Fatalf("get = %+v %v", tr, err)
	}
	if _, err := posts.SaveTranslation(ctx, 999999, "en", post.TranslationInput{Title: "x"}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown post: %v", err)
	}

	// Switching the original language to en drops the en translation.
	d := got.Draft()
	d.Lang = "en"
	d, err = d.Normalize(got.UpdatedAt)
	must(t, err)
	upd, err := posts.Update(ctx, p.ID, d)
	if err != nil || upd.Lang != "en" || !slices.Equal(upd.Languages, []string{"en", "ru"}) {
		t.Fatalf("after lang switch = %q %v %v", upd.Lang, upd.Languages, err)
	}

	must(t, posts.DeleteTranslation(ctx, p.ID, "ru"))
	if err := posts.DeleteTranslation(ctx, p.ID, "ru"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("delete twice: %v", err)
	}
	// Translations go with the post.
	_, err = posts.SaveTranslation(ctx, p.ID, "uz", post.TranslationInput{Title: "Salom"})
	must(t, err)
	must(t, posts.Delete(ctx, p.ID))
	var n int
	must(t, db.QueryRow(ctx, `SELECT count(*) FROM post_translations WHERE post_id = $1`, p.ID).Scan(&n))
	if n != 0 {
		t.Errorf("orphan translations: %d", n)
	}
}

func TestSeriesCoverAndRecent(t *testing.T) {
	db := connect(t)
	ctx := context.Background()
	users, posts, sers := postgres.NewUserRepo(db), postgres.NewPostRepo(db), postgres.NewSeriesRepo(db)
	u, err := users.Create(ctx, "sr_author", "sr@example.com")
	must(t, err)

	s, err := sers.Create(ctx, u.ID, "sr-one", series.SeriesInput{Title: "One", CoverURL: "/api/uploads/a.png"})
	if err != nil || s.CoverURL != "/api/uploads/a.png" {
		t.Fatalf("create = %+v %v", s, err)
	}
	s, err = sers.Update(ctx, s.ID, series.SeriesInput{Title: "One", CoverURL: "https://x.y/b.jpg"})
	if err != nil || s.CoverURL != "https://x.y/b.jpg" {
		t.Fatalf("update = %+v %v", s, err)
	}
	thin, err := sers.Create(ctx, u.ID, "sr-thin", series.SeriesInput{Title: "Thin"})
	must(t, err)

	var ids []int
	for _, st := range []post.Status{post.StatusPublished, post.StatusPublished, post.StatusDraft} {
		p, err := create(t, posts, post.Draft{Title: "part", Status: st}, u.ID, u.Username)
		must(t, err)
		ids = append(ids, p.ID)
	}
	must(t, sers.SetPosts(ctx, s.ID, u.ID, ids))
	one, err := create(t, posts, post.Draft{Title: "solo", Status: post.StatusPublished}, u.ID, u.Username)
	must(t, err)
	must(t, sers.SetPosts(ctx, thin.ID, u.ID, []int{one.ID}))

	recent, err := sers.Recent(ctx, 2, 10)
	must(t, err)
	if i := slices.IndexFunc(recent, func(x series.Series) bool { return x.ID == s.ID }); i < 0 || recent[i].Parts != 2 || recent[i].CoverURL == "" {
		t.Errorf("recent missing series with 2 published parts: %+v", recent)
	}
	if slices.ContainsFunc(recent, func(x series.Series) bool { return x.ID == thin.ID }) {
		t.Error("series with one part listed")
	}
	if r, err := sers.Recent(ctx, 2, 0); err != nil || len(r) != 0 {
		t.Errorf("limit 0 = %v %v", r, err)
	}

	must(t, users.MarkDeleted(ctx, u.ID, s.CreatedAt))
	recent, err = sers.Recent(ctx, 2, 10)
	must(t, err)
	if slices.ContainsFunc(recent, func(x series.Series) bool { return x.ID == s.ID }) {
		t.Error("deleted author's series listed")
	}
}
