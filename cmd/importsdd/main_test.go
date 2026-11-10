package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/config"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/postgres"
)

// runArgs runs the command with a fresh flag set.
func runArgs(t *testing.T, args ...string) error {
	t.Helper()
	oldArgs, oldFlags := os.Args, flag.CommandLine
	t.Cleanup(func() { os.Args, flag.CommandLine = oldArgs, oldFlags })
	flag.CommandLine = flag.NewFlagSet("importsdd", flag.ContinueOnError)
	os.Args = append([]string{"importsdd"}, args...)
	return run(context.Background())
}

func fixturePath(t *testing.T) string {
	t.Helper()
	_, store := fixture(t)
	return filepath.Join(filepath.Dir(store), "sdd.db")
}

func TestEnv(t *testing.T) {
	t.Setenv("IMPORTSDD_TEST_SET", "x")
	if env("IMPORTSDD_TEST_SET", "d") != "x" || env("IMPORTSDD_TEST_UNSET", "d") != "d" {
		t.Error("env")
	}
}

func TestRunArgumentErrors(t *testing.T) {
	t.Setenv("OLD_DATABASE_URL", "")
	if err := runArgs(t); err == nil || !strings.Contains(err.Error(), "-sqlite") {
		t.Errorf("no source: %v", err)
	}
	if err := runArgs(t, "-sqlite", filepath.Join(t.TempDir(), "missing.db")); err == nil {
		t.Error("missing file accepted")
	}
	if err := runArgs(t, "-sqlite", filepath.Join(t.TempDir(), "x"), "-bogus"); err == nil {
		t.Error("bad flag accepted")
	}
	if err := runArgs(t, "-old-database-url", "postgres://u:p@127.0.0.1:1/db?connect_timeout=1"); err == nil || !strings.Contains(err.Error(), "old database") {
		t.Errorf("unreachable old db: %v", err)
	}
	bad := filepath.Join(t.TempDir(), "bad.db")
	if err := os.WriteFile(bad, []byte("this is not sqlite"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = runArgs(t, "-sqlite", bad, "-dry-run") // must not panic
}

func TestRunDryOffline(t *testing.T) {
	t.Setenv("APP_ENV", "dev")
	t.Setenv("DATABASE_URL", "postgres://u:p@127.0.0.1:1/db?connect_timeout=1")
	path := fixturePath(t)
	// the fixture has projects that cannot import: failures are reported as an error
	err := runArgs(t, "-sqlite", path, "-dry-run", "-v")
	if err != nil && !strings.Contains(err.Error(), "failed") {
		t.Errorf("dry run: %v", err)
	}
	// S3 as the blob source (never contacted for a dry run without assets to fetch)
	_ = runArgs(t, "-sqlite", path, "-dry-run", "-old-s3-bucket", "b", "-old-s3-endpoint", "127.0.0.1:1")
	// explicit storage dir
	_ = runArgs(t, "-sqlite", path, "-dry-run", "-storage-dir", t.TempDir())
	// invalid config makes the dry run offline
	t.Setenv("APP_ENV", "bogus")
	_ = runArgs(t, "-sqlite", path, "-dry-run")
	// ... and a live run refuses
	if err := runArgs(t, "-sqlite", path); err == nil {
		t.Error("live run with bad config")
	}
}

func TestLiveAndDryTargetUnreachable(t *testing.T) {
	cfg := config.Config{DatabaseURL: "postgres://u:p@127.0.0.1:1/db?connect_timeout=1"}
	for _, create := range []bool{false, true} {
		if _, _, _, err := liveTarget(context.Background(), cfg, create); err == nil {
			t.Errorf("liveTarget(create=%v) against nothing succeeded", create)
		}
	}
	st, assume := dryTarget(context.Background(), cfg, nil)
	if !assume || st == nil {
		t.Error("dryTarget should go offline")
	}
	if _, assume := dryTarget(context.Background(), cfg, errors.New("cfg")); !assume {
		t.Error("dryTarget with bad config should go offline")
	}
}

func TestPostgresSourceAndTarget(t *testing.T) {
	db := connect(t)
	ctx := context.Background()
	src, err := OpenPostgres(ctx, db.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if ok, err := src.Has(ctx, "users"); err != nil || !ok {
		t.Errorf("Has(users) = %v %v", ok, err)
	}
	if ok, err := src.Has(ctx, "no_such_table_zz"); err != nil || ok {
		t.Errorf("Has(missing) = %v %v", ok, err)
	}
	rows, err := src.Select(ctx, "users", "id = ? AND username = ?", 0, "nobody")
	if err != nil || len(rows) != 0 {
		t.Errorf("Select: %v %v", rows, err)
	}
	if _, err := src.Select(ctx, "users", ""); err != nil {
		t.Error(err)
	}
	if _, err := src.Select(ctx, "no_such_table_zz", ""); err == nil {
		t.Error("select from missing table")
	}
	if _, err := OpenPostgres(ctx, "postgres://u:p@127.0.0.1:1/db?connect_timeout=1"); err == nil {
		t.Error("unreachable")
	}

	// dryTarget reads the real target
	cfg := config.Config{DatabaseURL: db.Config().ConnString()}
	if _, assume := dryTarget(ctx, cfg, nil); assume {
		t.Error("dryTarget should reach the database")
	}

	// CreateUser: no identity, failing identity, working identity
	if _, err := newPGStore(db, nil).CreateUser(ctx, newUser{Username: "zed", Email: "zed@example.com"}); err == nil {
		t.Error("no identity")
	}
	id := &fakeID{}
	st := newPGStore(db, id)
	if _, err := st.CreateUser(ctx, newUser{Username: "zed", Email: "zed@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUser(ctx, newUser{Username: "zed2", Email: "zed2@example.com", PasswordHash: "$argon2id$x"}); err == nil {
		t.Error("expected old-password update to fail without guard tables")
	}
	id.fail = true
	if _, err := st.CreateUser(ctx, newUser{Username: "zed3", Email: "zed3@example.com"}); err == nil {
		t.Error("identity failure accepted")
	}
	users, err := st.Users(ctx)
	if err != nil || users["zed@example.com"] == 0 || users["zed3@example.com"] != 0 {
		t.Errorf("users %v %v", users, err)
	}
	if ok, err := st.ProjectExists(ctx, "nope"); err != nil || ok {
		t.Error(ok, err)
	}
	_ = postgres.NewUserRepo
}

type fakeID struct {
	application.Identity
	fail bool
}

func (f *fakeID) CreateAccount(context.Context, int, string, string) error {
	if f.fail {
		return errors.New("guard down")
	}
	return nil
}
