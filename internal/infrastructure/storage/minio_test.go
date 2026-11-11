package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcminio "github.com/testcontainers/testcontainers-go/modules/minio"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
)

func TestMinIO(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	ctx := context.Background()
	c, err := tcminio.Run(ctx, minioImage())
	if err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	t.Cleanup(func() { testcontainers.TerminateContainer(c) })
	ep, err := c.ConnectionString(ctx)
	if err != nil {
		t.Fatal(err)
	}

	m, err := NewMinIO(ctx, ep, c.Username, c.Password, "bkt", false)
	if err != nil {
		t.Fatal(err)
	}
	// second call: bucket already exists
	if _, err := NewMinIO(ctx, ep, c.Username, c.Password, "bkt", false); err != nil {
		t.Fatal(err)
	}
	if err := m.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	if err := m.Put(ctx, "a/b.txt", bytes.NewReader([]byte("hello")), 5, "text/plain"); err != nil {
		t.Fatal(err)
	}
	obj, err := m.Get(ctx, "a/b.txt")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(obj.Body)
	obj.Body.Close()
	if string(body) != "hello" || obj.ContentType != "text/plain" || obj.Size != 5 {
		t.Errorf("got %q %q %d", body, obj.ContentType, obj.Size)
	}

	if _, err := m.Get(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing: %v", err)
	}

	// Remove deletes the object, and a missing one is not an error
	if err := m.Remove(ctx, "a/b.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get(ctx, "a/b.txt"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("after Remove: %v", err)
	}
	if err := m.Remove(ctx, "a/b.txt"); err != nil {
		t.Errorf("removing a missing object: %v", err)
	}
	if _, err := m.Get(ctx, ""); err == nil || errors.Is(err, domain.ErrNotFound) {
		t.Errorf("empty key: %v", err)
	}
	if err := m.Put(ctx, "", bytes.NewReader(nil), 0, ""); err == nil {
		t.Error("empty key put accepted")
	}

	// Bucket gone: Stat fails with something other than NoSuchKey; Ping stays ok-or-error.
	if err := m.client.RemoveObject(ctx, "bkt", "a/b.txt", minioRemoveOpts()); err != nil {
		t.Fatal(err)
	}
	if err := m.client.RemoveBucket(ctx, "bkt"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get(ctx, "x"); err == nil || errors.Is(err, domain.ErrNotFound) {
		t.Errorf("no bucket: %v", err)
	}
	if err := m.Put(ctx, "x", bytes.NewReader([]byte("1")), 1, ""); err == nil {
		t.Error("put into missing bucket")
	}

	// NewMinIO creates a missing bucket.
	if _, err := NewMinIO(ctx, ep, c.Username, c.Password, "fresh", false); err != nil {
		t.Fatal(err)
	}
}

func TestNewMinIOErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := NewMinIO(ctx, "http://bad endpoint", "k", "s", "b", false); err == nil {
		t.Error("bad endpoint accepted")
	}
	// Unreachable server: retry loop honours context cancellation.
	cctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	if _, err := NewMinIO(cctx, "127.0.0.1:1", "k", "s", "b", false); err == nil {
		t.Error("unreachable server accepted")
	}
}
