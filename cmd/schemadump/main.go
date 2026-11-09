// Command schemadump prints the live PostgreSQL schema (public schema) as
// SQL DDL, the same text GET /api/diagrams/schema serves. It only needs
// DATABASE_URL (loaded like cmd/app).
//
//	go run ./cmd/schemadump                    # all tables to stdout
//	go run ./cmd/schemadump -prefix dg_        # only tables/enums starting with dg_
//	go run ./cmd/schemadump -o schema.sql      # write to a file
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/config"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/postgres"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/schemadump"
)

func main() {
	prefix := flag.String("prefix", "", "only tables and enums whose name starts with this (e.g. dg_)")
	out := flag.String("o", "", "write to this file instead of stdout")
	flag.Parse()
	if err := run(*prefix, *out); err != nil {
		fmt.Fprintln(os.Stderr, "schemadump:", err)
		os.Exit(1)
	}
}

func run(prefix, out string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	db, err := postgres.Connect(ctx, cfg.DatabaseURL, postgres.Options{MaxConns: 1})
	if err != nil {
		return err
	}
	defer db.Close()
	ddl, err := schemadump.Dump(ctx, db, prefix)
	if err != nil {
		return err
	}
	if out == "" {
		_, err = fmt.Print(ddl)
		return err
	}
	return os.WriteFile(out, []byte(ddl), 0o644)
}
