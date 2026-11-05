package postgres_test

import (
	"context"
	"testing"

	"github.com/iBlog/iblog-monolith-go/internal/domain/post"
	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/postgres"
)

// Editor autosave saves a draft every few seconds: those saves share one revision, but publishing a draft
// and editing a published post each keep the text they replace.
func TestDraftSavesShareARevision(t *testing.T) {
	db := connect(t)
	ctx := context.Background()
	users := postgres.NewUserRepo(db)
	posts := postgres.NewPostRepo(db)
	ali, err := users.Create(ctx, "revali", "revali@example.com")
	must(t, err)
	p, err := create(t, posts, post.Draft{Title: "t", Body: "v0", Status: post.StatusDraft, Tags: []string{}}, ali.ID, ali.Username)
	must(t, err)

	count := func() (n int) {
		must(t, db.QueryRow(ctx, `SELECT count(*) FROM post_revisions WHERE post_id = $1`, p.ID).Scan(&n))
		return n
	}
	save := func(body string, st post.Status) {
		t.Helper()
		_, err := posts.UpdateWithRevision(ctx, p.ID, ali.ID, post.Draft{Title: "t", Body: body, Status: st, Tags: []string{}})
		must(t, err)
	}
	save("v1", post.StatusDraft)
	save("v2", post.StatusDraft)
	save("v3", post.StatusDraft)
	if n := count(); n != 1 {
		t.Fatalf("draft saves made %d revisions, want 1", n)
	}
	save("v4", post.StatusPublished) // publishing keeps the draft text
	if n := count(); n != 2 {
		t.Fatalf("after publish %d revisions, want 2", n)
	}
	save("v5", post.StatusPublished)
	if n := count(); n != 3 {
		t.Fatalf("published edit %d revisions, want 3", n)
	}
}
