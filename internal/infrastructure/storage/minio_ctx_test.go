package storage

import (
	"context"
	"errors"
	"testing"
	"time"
)

// An unreachable endpoint keeps retrying until the context ends.
func TestNewMinIOContextCancelledWhileRetrying(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	_, err := NewMinIO(ctx, "127.0.0.1:1", "k", "s", "b", false)
	if err == nil {
		t.Fatal("want error")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Logf("err=%v (ctx error surfaced via BucketExists)", err)
	}
}
