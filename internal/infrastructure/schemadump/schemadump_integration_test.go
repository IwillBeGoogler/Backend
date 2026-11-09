package schemadump_test

// Read against a real Postgres. Skipped with -short or without Docker.

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/schemadump"
)

func pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test")
	}
	ctx := context.Background()
	dsn := os.Getenv("SCHEMADUMP_TEST_DATABASE_URL") // a scratch database; the test creates dg_* objects in it
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
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return db
}

const ddl = `
CREATE TYPE dg_role AS ENUM ('owner', 'viewer');
CREATE TYPE other_kind AS ENUM ('a');
COMMENT ON TYPE dg_role IS 'who';
CREATE TABLE dg_projects (id text PRIMARY KEY, name text NOT NULL UNIQUE);
COMMENT ON TABLE dg_projects IS 'projects';
CREATE TABLE dg_files (
  id text PRIMARY KEY,
  project_id text NOT NULL REFERENCES dg_projects(id) ON DELETE CASCADE,
  role dg_role DEFAULT 'owner',
  CONSTRAINT dg_files_id_check CHECK (length(id) > 0)
);
COMMENT ON COLUMN dg_files.role IS 'the role';
COMMENT ON CONSTRAINT dg_files_id_check ON dg_files IS 'non-empty';
CREATE INDEX dg_files_project_idx ON dg_files (project_id);
COMMENT ON INDEX dg_files_project_idx IS 'lookup';
CREATE TABLE other_things (id int PRIMARY KEY, project_id text REFERENCES dg_projects(id));
CREATE TABLE dg_refs_other (id int PRIMARY KEY, thing int REFERENCES other_things(id));
CREATE TABLE goose_db_version (id int);
CREATE TABLE dg_part (id int, k int) PARTITION BY RANGE (k);
CREATE TABLE dg_part_1 PARTITION OF dg_part FOR VALUES FROM (0) TO (10);
`

func TestDumpAndRead(t *testing.T) {
	db := pool(t)
	ctx := context.Background()
	if _, err := db.Exec(ctx, ddl); err != nil {
		t.Fatal(err)
	}

	got, err := schemadump.Dump(ctx, db, "dg_")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"CREATE TYPE dg_role AS ENUM ('owner', 'viewer');\n",
		"COMMENT ON TYPE dg_role IS 'who';\n",
		"CREATE TABLE dg_files (",
		"CONSTRAINT dg_files_pkey PRIMARY KEY (id)",
		"ON DELETE CASCADE",
		"CREATE INDEX dg_files_project_idx ON public.dg_files USING btree (project_id);\n",
		"COMMENT ON INDEX dg_files_project_idx IS 'lookup';\n",
		"COMMENT ON TABLE dg_projects IS 'projects';\n",
		"COMMENT ON COLUMN dg_files.role IS 'the role';\n",
		"CREATE TABLE dg_part (",
		"CREATE TABLE dg_refs_other (",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, bad := range []string{"other_kind", "other_things (", "goose_db_version", "dg_part_1"} {
		if strings.Contains(got, bad) {
			t.Errorf("unexpected %q in:\n%s", bad, got)
		}
	}
	// FK from a dg_ table to a non-dg_ table is left out
	if strings.Contains(got, "REFERENCES other_things") {
		t.Errorf("foreign key outside the prefix leaked:\n%s", got)
	}

	// Empty prefix: everything but the bookkeeping table.
	s, err := schemadump.Read(ctx, db, "")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tb := range s.Tables {
		names = append(names, tb.Name)
	}
	all := strings.Join(names, ",")
	if !strings.Contains(all, "other_things") || strings.Contains(all, "goose_db_version") {
		t.Errorf("tables %s", all)
	}
	if len(s.Enums) != 2 {
		t.Errorf("enums %+v", s.Enums)
	}

	// A prefix that matches nothing: no tables, so no column/constraint/index queries.
	s, err = schemadump.Read(ctx, db, "zzz_")
	if err != nil || len(s.Tables) != 0 || len(s.Enums) != 0 {
		t.Errorf("empty: %+v %v", s, err)
	}
	if out, err := schemadump.Dump(ctx, db, "zzz_"); err != nil || out != "" {
		t.Errorf("dump empty: %q %v", out, err)
	}

	// Failure injection: every query can fail to start, fail to scan or fail while iterating.
	boom := errors.New("boom")
	for n := 1; n <= 5; n++ {
		for _, mode := range []string{"query", "scan", "rows"} {
			q := &faulty{db: db, at: n, mode: mode, err: boom}
			if _, err := schemadump.Read(ctx, q, "dg_"); !errors.Is(err, boom) {
				t.Errorf("query %d mode %s: err = %v", n, mode, err)
			}
		}
	}
	if _, err := schemadump.Dump(ctx, &faulty{db: db, at: 1, mode: "query", err: boom}, ""); !errors.Is(err, boom) {
		t.Errorf("Dump: %v", err)
	}
}

// faulty fails the at-th Query call: at the call itself, in Scan, or in Err.
type faulty struct {
	db   *pgxpool.Pool
	at   int
	mode string
	err  error
	n    int
}

func (f *faulty) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	f.n++
	if f.n != f.at {
		return f.db.Query(ctx, sql, args...)
	}
	if f.mode == "query" {
		return nil, f.err
	}
	rows, err := f.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return &faultyRows{Rows: rows, mode: f.mode, err: f.err}, nil
}

type faultyRows struct {
	pgx.Rows
	mode string
	err  error
}

func (r *faultyRows) Scan(dest ...any) error {
	if r.mode == "scan" {
		return r.err
	}
	return r.Rows.Scan(dest...)
}

func (r *faultyRows) Err() error {
	if r.mode == "rows" {
		return r.err
	}
	return r.Rows.Err()
}
