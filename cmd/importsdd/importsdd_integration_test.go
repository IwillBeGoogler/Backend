package main

// The importer against a real Postgres: TEST_DATABASE_URL, else a container.
// Skipped with -short or without Docker.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/postgres"
)

func connect(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test")
	}
	ctx := context.Background()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		pg, err := tcpostgres.Run(ctx, "postgres:18-alpine",
			tcpostgres.WithDatabase("blog"), tcpostgres.WithUsername("blog"), tcpostgres.WithPassword("blog"),
			testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)))
		if err != nil {
			t.Skipf("docker unavailable: %v", err)
		}
		t.Cleanup(func() { testcontainers.TerminateContainer(pg) })
		if dsn, err = pg.ConnectionString(ctx, "sslmode=disable"); err != nil {
			t.Fatal(err)
		}
	}
	db, err := postgres.Connect(ctx, dsn, postgres.Options{MaxConns: 4, Migrate: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return db
}

func count(t *testing.T, db *pgxpool.Pool, q string) (n int) {
	t.Helper()
	if err := db.QueryRow(context.Background(), q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestImportIntoPostgres(t *testing.T) {
	db := connect(t)
	ctx := context.Background()
	users := postgres.NewUserRepo(db)
	// the email of u1 differs in case: matching is case-insensitive
	if _, err := users.Create(ctx, "ali", "ali@example.com"); err != nil {
		t.Fatal(err)
	}
	im, _, blobs := newImporter(t, false)
	im.st = newPGStore(db, nil)

	rep, err := im.run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.failed) != 0 {
		t.Fatalf("failed: %v %v", rep.failed, rep.problems)
	}
	for q, want := range map[string]int{
		`SELECT count(*) FROM dg_projects`:                                1,
		`SELECT count(*) FROM dg_folders`:                                 3,
		`SELECT count(*) FROM dg_files`:                                   3,
		`SELECT count(*) FROM dg_file_versions`:                           3,
		`SELECT count(*) FROM dg_share_links`:                             1,
		`SELECT count(*) FROM dg_comments`:                                2,
		`SELECT count(*) FROM dg_comment_mentions`:                        1,
		`SELECT count(*) FROM notifications WHERE type LIKE 'diagram\_%'`: 2,
		`SELECT count(*) FROM dg_webhooks`:                                1,
		`SELECT count(*) FROM dg_file_ydocs`:                              1,
		`SELECT count(*) FROM dg_file_yupdates`:                           1,
		`SELECT count(*) FROM dg_assets`:                                  1,
		`SELECT count(*) FROM dg_access_tokens`:                           2,
		`SELECT count(*) FROM dg_user_prefs`:                              1,
		`SELECT count(*) FROM dg_mcp_audit`:                               1,
		`SELECT count(*) FROM dg_project_library`:                         1,
	} {
		if got := count(t, db, q); got != want {
			t.Errorf("%s = %d, want %d", q, got, want)
		}
	}
	if len(blobs.put) != 1 {
		t.Errorf("blobs = %d", len(blobs.put))
	}

	// a second run changes nothing
	rep, err = im.run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.existing["project"] != 1 || rep.imported["asset"] != 0 || count(t, db, `SELECT count(*) FROM notifications WHERE type LIKE 'diagram\_%'`) != 2 {
		t.Errorf("not idempotent: existing %v imported %v", rep.existing, rep.imported)
	}
}
