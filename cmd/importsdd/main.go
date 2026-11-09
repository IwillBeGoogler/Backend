// Command importsdd is the one-shot migration of the old Node diagram
// server's data into the Go server's dg_* tables.
//
//	go run ./cmd/importsdd -dry-run                      # counts and problems, writes nothing
//	go run ./cmd/importsdd -sqlite /path/to/old/data/sdd.db
//	go run ./cmd/importsdd -old-database-url postgres://...  -old-s3-bucket sdd ...
//
// Source: the old SQLite file (-sqlite) or its
// Postgres (-old-database-url / OLD_DATABASE_URL); blobs from its storage
// directory (default <db dir>/storage) or S3 (-old-s3-*, OLD_S3_*). Target:
// DATABASE_URL, S3_* and the rest of the settings load like cmd/app.
//
// Old users are matched to blog users by email. Without -create-missing the
// data of unmatched users is skipped and reported; with it an account is
// made (email unverified) that keeps the old password. Every project is
// imported in its own transaction and skipped when its id already exists,
// so a rerun finishes what failed. Nothing in the old data is modified.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"go.uber.org/zap"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/app/core"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/config"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/guardauth"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/postgres"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/storage"
)

func main() {
	logCfg := zap.NewDevelopmentConfig()
	logCfg.Level = zap.NewAtomicLevelAt(zap.WarnLevel)
	logCfg.DisableStacktrace = true
	zap.ReplaceGlobals(zap.Must(logCfg.Build()))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "importsdd:", err)
		os.Exit(1)
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func run(ctx context.Context) error {
	sqlitePath := flag.String("sqlite", "", "old SQLite file")
	pgURL := flag.String("old-database-url", os.Getenv("OLD_DATABASE_URL"), "old server Postgres URL (instead of -sqlite)")
	storageDir := flag.String("storage-dir", "", "old local blob directory (default <sqlite dir>/storage)")
	s3 := s3Config{}
	flag.StringVar(&s3.Bucket, "old-s3-bucket", os.Getenv("OLD_S3_BUCKET"), "old S3 bucket (instead of -storage-dir)")
	flag.StringVar(&s3.Endpoint, "old-s3-endpoint", env("OLD_S3_ENDPOINT", "s3.amazonaws.com"), "old S3 endpoint host[:port]")
	flag.StringVar(&s3.Region, "old-s3-region", env("OLD_S3_REGION", "us-east-1"), "old S3 region")
	flag.StringVar(&s3.AccessKey, "old-s3-access-key", os.Getenv("OLD_S3_ACCESS_KEY_ID"), "old S3 access key id")
	flag.StringVar(&s3.SecretKey, "old-s3-secret-key", os.Getenv("OLD_S3_SECRET_ACCESS_KEY"), "old S3 secret key")
	flag.BoolVar(&s3.SSL, "old-s3-ssl", os.Getenv("OLD_S3_SSL") == "true", "old S3 over HTTPS")
	create := flag.Bool("create-missing", false, "create accounts for old users without a blog account (email unverified)")
	dry := flag.Bool("dry-run", false, "print counts and problems, write nothing")
	verbose := flag.Bool("v", false, "list every problem")
	flag.Parse()

	var src Source
	var err error
	dir := ""
	if *pgURL != "" {
		if src, err = OpenPostgres(ctx, *pgURL); err != nil {
			return fmt.Errorf("old database: %w", err)
		}
	} else {
		path := *sqlitePath
		if path == "" {
			return fmt.Errorf("old SQLite file not found: pass -sqlite")
		}
		if _, err := os.Stat(path); err != nil {
			return err
		}
		dir = filepath.Dir(path)
		if src, err = OpenSQLite(path); err != nil {
			return err
		}
	}
	defer src.Close()

	var blobsIn BlobSource
	switch {
	case s3.Bucket != "":
		if blobsIn, err = newS3Blobs(s3); err != nil {
			return fmt.Errorf("old S3: %w", err)
		}
	case *storageDir != "":
		blobsIn = localBlobs{*storageDir}
	case dir != "":
		blobsIn = localBlobs{filepath.Join(dir, "storage")}
	}

	im := &importer{src: src, blobsIn: blobsIn, createMissing: *create, dry: *dry, out: os.Stderr}
	cfg, cfgErr := config.Load()
	if *dry {
		im.st, im.assumeUsers = dryTarget(ctx, cfg, cfgErr)
	} else {
		if cfgErr != nil {
			return cfgErr
		}
		st, sink, closeFn, err := liveTarget(ctx, cfg, *create)
		if err != nil {
			return err
		}
		defer closeFn()
		im.st, im.blobsOut = st, sink
	}

	rep, err := im.run(ctx)
	if rep != nil {
		rep.print(os.Stdout, *dry, *verbose)
	}
	if err != nil {
		return err
	}
	if len(rep.failed) > 0 {
		return fmt.Errorf("%d project(s) failed", len(rep.failed))
	}
	return nil
}

// liveTarget connects for real. Guard (and so Redis) is only needed to make
// accounts.
func liveTarget(ctx context.Context, cfg config.Config, create bool) (store, BlobSink, func(), error) {
	if create {
		s, err := core.New(ctx, cfg)
		if err != nil {
			return nil, nil, nil, err
		}
		return newPGStore(s.DB, guardauth.Identity{G: s.Guard}), s.Storage, s.Close, nil
	}
	db, err := postgres.Connect(ctx, cfg.DatabaseURL, postgres.Options{MaxConns: 4, Migrate: cfg.MigrateOnStart})
	if err != nil {
		return nil, nil, nil, err
	}
	st, err := storage.NewMinIO(ctx, cfg.S3Endpoint, cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3Bucket, cfg.S3UseSSL)
	if err != nil {
		db.Close()
		return nil, nil, nil, fmt.Errorf("minio: %w", err)
	}
	var id application.Identity
	return newPGStore(db, id), st, db.Close, nil
}

// dryTarget reads the lookups of a dry run from the target database. When it
// is not reachable the run goes on offline: every old user is assumed to
// have an account and no project exists yet.
func dryTarget(ctx context.Context, cfg config.Config, cfgErr error) (store, bool) {
	offline := func(why error) (store, bool) {
		fmt.Fprintf(os.Stderr, "dry run: target database not checked (%v); assuming every old user has an account and no project is imported yet\n", why)
		return &dryStore{}, true
	}
	if cfgErr != nil {
		return offline(cfgErr)
	}
	db, err := postgres.Connect(ctx, cfg.DatabaseURL, postgres.Options{MaxConns: 2})
	if err != nil {
		return offline(err)
	}
	defer db.Close()
	pg := newPGStore(db, nil)
	users, err := pg.Users(ctx)
	if err != nil {
		return offline(err)
	}
	d := &dryStore{users: users, projects: map[string]bool{}}
	rows, err := db.Query(ctx, `SELECT id FROM dg_projects`)
	if err != nil {
		return offline(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return offline(err)
		}
		d.projects[id] = true
	}
	return d, false
}
