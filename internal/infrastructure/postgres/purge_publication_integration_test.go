package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/postgres"
)

// A publication whose only owner is purged passes to its longest-standing editor;
// one nobody else belongs to goes away with its owner.
func TestPurgeHandsOverPublications(t *testing.T) {
	db := connect(t)
	ctx := context.Background()
	users := postgres.NewUserRepo(db)
	owner, err := users.Create(ctx, "pubowner", "pubowner@example.com")
	must(t, err)
	editor, err := users.Create(ctx, "pubeditor", "pubeditor@example.com")
	must(t, err)

	var shared, solo int
	for slug, dst := range map[string]*int{"shared-pub": &shared, "solo-pub": &solo} {
		must(t, db.QueryRow(ctx, `INSERT INTO publications (slug, name, description, avatar_url) VALUES ($1, $1, '', '') RETURNING id`, slug).Scan(dst))
		_, err := db.Exec(ctx, `INSERT INTO publication_members (publication_id, user_id, role) VALUES ($1, $2, 'owner')`, *dst, owner.ID)
		must(t, err)
	}
	_, err = db.Exec(ctx, `INSERT INTO publication_members (publication_id, user_id, role) VALUES ($1, $2, 'editor')`, shared, editor.ID)
	must(t, err)

	deletedAt := time.Now().Add(-48 * time.Hour)
	must(t, users.MarkDeleted(ctx, owner.ID, deletedAt))
	if ok, err := users.Purge(ctx, owner.ID, time.Now()); !ok || err != nil {
		t.Fatalf("Purge = %v, %v", ok, err)
	}

	var role string
	if err := db.QueryRow(ctx, `SELECT role FROM publication_members WHERE publication_id = $1 AND user_id = $2`, shared, editor.ID).Scan(&role); err != nil || role != "owner" {
		t.Fatalf("editor role = %q, %v; want owner", role, err)
	}
	var n int
	must(t, db.QueryRow(ctx, `SELECT count(*) FROM publications WHERE id = $1`, solo).Scan(&n))
	if n != 0 {
		t.Fatalf("a publication with no other member survived its owner")
	}
}
