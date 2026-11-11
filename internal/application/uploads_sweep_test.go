package application

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

type fakeUnused struct {
	keys      []string
	err       error
	forgotten []string
	cutoff    time.Time
	limit     int
}

func (f *fakeUnused) Unused(_ context.Context, olderThan time.Time, limit int) ([]string, error) {
	f.cutoff, f.limit = olderThan, limit
	return f.keys, f.err
}

func (f *fakeUnused) Forget(_ context.Context, keys []string) error {
	f.forgotten = append(f.forgotten, keys...)
	return nil
}

type fakeRemover struct {
	removed []string
	failOn  string
}

func (r *fakeRemover) Remove(_ context.Context, key string) error {
	if key == r.failOn {
		return errors.New("s3 down")
	}
	r.removed = append(r.removed, key)
	return nil
}

func TestUploadSweeper(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	ledger := &fakeUnused{keys: []string{"aaa.webp", "bbb.gif"}}
	store := &fakeRemover{}
	s := &UploadSweeper{Ledger: ledger, Storage: store, Grace: 72 * time.Hour, Batch: 50, Now: func() time.Time { return now }}
	if err := s.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := now.Add(-72 * time.Hour); !ledger.cutoff.Equal(want) || ledger.limit != 50 {
		t.Errorf("asked for before %v limit %d", ledger.cutoff, ledger.limit)
	}
	// each image goes with its thumbnail and every variant
	for _, k := range []string{"aaa.webp", "thumb/aaa.jpg", "img/aaa-320.webp", "img/aaa-640.webp", "img/aaa-1280.webp", "bbb.gif", "thumb/bbb.jpg", "img/bbb-1280.webp"} {
		if !slices.Contains(store.removed, k) {
			t.Errorf("%s was not removed (removed: %v)", k, store.removed)
		}
	}
	if !slices.Equal(ledger.forgotten, []string{"aaa.webp", "bbb.gif"}) {
		t.Errorf("forgotten = %v", ledger.forgotten)
	}
}

func TestUploadSweeperKeepsRowsOfFailedRemovals(t *testing.T) {
	ledger := &fakeUnused{keys: []string{"aaa.webp", "bbb.webp"}}
	store := &fakeRemover{failOn: "thumb/aaa.jpg"}
	s := &UploadSweeper{Ledger: ledger, Storage: store, Grace: time.Hour}
	err := s.Sweep(context.Background())
	if err == nil || !strings.Contains(err.Error(), "remove aaa.webp") {
		t.Fatalf("err = %v", err)
	}
	if !slices.Equal(ledger.forgotten, []string{"bbb.webp"}) {
		t.Errorf("only the removed image may be forgotten, got %v", ledger.forgotten)
	}
	if ledger.limit != 1 {
		t.Errorf("a zero batch must still make progress, limit = %d", ledger.limit)
	}
}

func TestUploadSweeperOffAndErrors(t *testing.T) {
	var off *UploadSweeper
	if err := off.Sweep(context.Background()); err != nil {
		t.Errorf("nil sweeper: %v", err)
	}
	ledger := &fakeUnused{keys: []string{"a.webp"}}
	if err := (&UploadSweeper{Ledger: ledger, Storage: &fakeRemover{}}).Sweep(context.Background()); err != nil || ledger.limit != 0 {
		t.Errorf("zero grace must do nothing: %v, asked %d", err, ledger.limit)
	}
	boom := errors.New("db down")
	if err := (&UploadSweeper{Ledger: &fakeUnused{err: boom}, Storage: &fakeRemover{}, Grace: time.Hour}).Sweep(context.Background()); !errors.Is(err, boom) {
		t.Errorf("ledger error: %v", err)
	}
	if err := (&UploadSweeper{Ledger: &fakeUnused{}, Storage: &fakeRemover{}, Grace: time.Hour}).Sweep(context.Background()); err != nil {
		t.Errorf("nothing unused: %v", err)
	}
}
