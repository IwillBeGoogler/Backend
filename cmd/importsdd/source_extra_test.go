package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
)

func TestRowHelpers(t *testing.T) {
	r := Row{"s": "x", "b": []byte("y"), "n": nil, "i": int64(7), "i32": int32(8), "i16": int16(9), "int": 10,
		"f": 2.5, "t": true, "fa": false, "num": "42", "bad": "zz", "other": struct{}{}}
	if r.str("s") != "x" || r.str("b") != "y" || r.str("n") != "" || r.str("i") != "7" || r.str("missing") != "" {
		t.Error("str")
	}
	if r.strp("n") != nil || *r.strp("s") != "x" || r.strp("missing") != nil {
		t.Error("strp")
	}
	for k, want := range map[string]int64{"i": 7, "i32": 8, "i16": 9, "int": 10, "f": 2, "t": 1, "fa": 0, "num": 42} {
		if got, ok := r.i64(k); !ok || got != want {
			t.Errorf("i64(%s) = %d %v", k, got, ok)
		}
	}
	if _, ok := r.i64("bad"); ok {
		t.Error("bad string parsed")
	}
	if _, ok := r.i64("other"); ok {
		t.Error("struct parsed")
	}
	if r.int("i", 1) != 7 || r.int("missing", 5) != 5 {
		t.Error("int")
	}
	if r.f64("f") != 2.5 || r.f64("i") != 7 || r.f64("missing") != 0 {
		t.Error("f64")
	}
	if !r.flag("t", false) || r.flag("fa", true) || !r.flag("missing", true) {
		t.Error("flag")
	}
	def := time.Unix(5, 0)
	if !r.ts("missing", def).Equal(def) || r.ts("i", def).Unix() != 7 {
		t.Error("ts")
	}
	if r.tsp("missing") != nil || r.tsp("i") == nil {
		t.Error("tsp")
	}
	if epoch(1_700_000_000_000).Unix() != 1_700_000_000 {
		t.Error("epoch ms")
	}
}

func TestSQLiteSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`CREATE TABLE things (id INTEGER, name TEXT)`, `INSERT INTO things VALUES (1,'a'),(2,'b')`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	src, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	ctx := context.Background()
	if ok, err := src.Has(ctx, "things"); err != nil || !ok {
		t.Errorf("Has: %v %v", ok, err)
	}
	if ok, _ := src.Has(ctx, "ghost"); ok {
		t.Error("ghost table")
	}
	all, err := src.Select(ctx, "things", "")
	if err != nil || len(all) != 2 {
		t.Fatalf("select all: %v %v", all, err)
	}
	one, err := src.Select(ctx, "things", "id = ?", 2)
	if err != nil || len(one) != 1 || one[0].str("name") != "b" {
		t.Fatalf("select where: %v %v", one, err)
	}
	if _, err := src.Select(ctx, "ghost", ""); err == nil {
		t.Error("select from missing table")
	}
	if _, err := OpenSQLite(filepath.Join(t.TempDir(), "missing", "x.db")); err == nil {
		t.Error("opened a missing file")
	}
}

func TestLocalBlobs(t *testing.T) {
	dir := t.TempDir()
	data := []byte("blob")
	hash := diagram.HashBytes(data)
	if err := os.MkdirAll(filepath.Join(dir, hash[:2], hash[2:4]), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, hash[:2], hash[2:4], hash), data, 0o644); err != nil {
		t.Fatal(err)
	}
	l := localBlobs{dir}
	ctx := context.Background()
	if b, err := l.Get(ctx, hash); err != nil || string(b) != "blob" {
		t.Errorf("get: %q %v", b, err)
	}
	if _, err := l.Get(ctx, diagram.HashBytes([]byte("other"))); err != errBlobMissing {
		t.Errorf("missing: %v", err)
	}
	if _, err := l.Get(ctx, "../../etc/passwd"); err != errBlobMissing {
		t.Errorf("invalid hash: %v", err)
	}
	s, err := newS3Blobs(s3Config{Endpoint: "localhost:1", Bucket: "b", AccessKey: "a", SecretKey: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "bad"); err != errBlobMissing {
		t.Errorf("s3 invalid hash: %v", err)
	}
}
