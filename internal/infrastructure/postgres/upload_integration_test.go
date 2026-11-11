package postgres_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/postgres"
)

func TestUploadRepo(t *testing.T) {
	db := connect(t)
	ctx := context.Background()
	repo := postgres.NewUploadRepo(db)
	a := covUCSUser(t, postgres.NewUserRepo(db), "upl-a")
	b := covUCSUser(t, postgres.NewUserRepo(db), "upl-b")

	usage := func(id int) int64 {
		n, err := repo.Usage(ctx, id)
		must(t, err)
		return n
	}
	if usage(a.ID) != 0 {
		t.Fatal("a new user has uploads")
	}
	must(t, repo.Record(ctx, "upl-1.png", a.ID, 1000))
	must(t, repo.Record(ctx, "upl-2.jpg", a.ID, 234))
	must(t, repo.Record(ctx, "upl-1.png", a.ID, 999999)) // same key: counted once
	must(t, repo.Record(ctx, "upl-3.png", b.ID, 7))
	if usage(a.ID) != 1234 || usage(b.ID) != 7 {
		t.Fatalf("usage a=%d b=%d", usage(a.ID), usage(b.ID))
	}
	// a deleted account keeps the rows (the objects still exist) without an owner
	_, err := db.Exec(ctx, `DELETE FROM users WHERE id = $1`, b.ID)
	must(t, err)
	var orphans int
	must(t, db.QueryRow(ctx, `SELECT count(*) FROM uploads WHERE key = 'upl-3.png' AND user_id IS NULL`).Scan(&orphans))
	if orphans != 1 {
		t.Errorf("orphaned rows = %d", orphans)
	}
}

func TestUploadRepoUnused(t *testing.T) {
	db := connect(t)
	ctx := context.Background()
	repo := postgres.NewUploadRepo(db)
	users := postgres.NewUserRepo(db)
	posts := postgres.NewPostRepo(db)
	u := covUCSUser(t, users, "unused-owner")
	_ = posts

	rec := func(key string, age time.Duration) {
		must(t, repo.Record(ctx, key, u.ID, 10))
		_, err := db.Exec(ctx, `UPDATE uploads SET created_at = now() - $2::interval WHERE key = $1`, key, age.String())
		must(t, err)
	}
	old := 5 * 24 * time.Hour
	rec("unused-orphan.webp", old)
	rec("unused-young.webp", time.Hour)
	rec("unused-in-bio.webp", old)
	rec("unused-in-comment.webp", old)
	_, err := db.Exec(ctx, `UPDATE users SET bio = 'me ![x](/api/uploads/unused-in-bio.webp)' WHERE id = $1`, u.ID)
	must(t, err)
	post := covUCSPost(t, posts, "unused-post", u)
	_, err = db.Exec(ctx, `INSERT INTO comments (post_id, user_id, author, text) VALUES ($1, $2, $3, '/api/uploads/unused-in-comment.webp')`, post.ID, u.ID, u.Username)
	must(t, err)

	cutoff := time.Now().Add(-72 * time.Hour)
	keys, err := repo.Unused(ctx, cutoff, 100)
	must(t, err)
	if len(keys) != 1 || keys[0] != "unused-orphan.webp" {
		t.Fatalf("unused = %v, want only the orphan", keys)
	}
	// images that are still used were marked checked: the next run does not look at them again
	var checked int
	must(t, db.QueryRow(ctx, `SELECT count(*) FROM uploads WHERE key IN ('unused-in-bio.webp','unused-in-comment.webp') AND checked_at IS NOT NULL`).Scan(&checked))
	if checked != 2 {
		t.Errorf("checked = %d", checked)
	}
	// once the text no longer mentions the image it is found after its day is up
	_, err = db.Exec(ctx, `UPDATE users SET bio = '' WHERE id = $1`, u.ID)
	must(t, err)
	_, err = db.Exec(ctx, `UPDATE uploads SET checked_at = now() - interval '2 days' WHERE key = 'unused-in-bio.webp'`)
	must(t, err)
	keys, err = repo.Unused(ctx, cutoff, 100)
	must(t, err)
	if !slices.Contains(keys, "unused-in-bio.webp") || slices.Contains(keys, "unused-in-comment.webp") {
		t.Errorf("after the bio changed: %v", keys)
	}
	must(t, repo.Forget(ctx, keys))
	left, err := repo.Usage(ctx, u.ID)
	must(t, err)
	if left != 20 { // young + in-comment remain
		t.Errorf("usage after Forget = %d", left)
	}
}
