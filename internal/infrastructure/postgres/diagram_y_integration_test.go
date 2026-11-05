package postgres_test

// Yjs state of live-edited files against a real Postgres. Skipped with
// -short or without Docker.

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/iBlog/iblog-monolith-go/internal/application"
	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/postgres"
)

func TestDiagramYRepo(t *testing.T) {
	db := connect(t)
	ctx := context.Background()
	ali, err := postgres.NewUserRepo(db).Create(ctx, "dg_yali", "dg_yali@example.com")
	must(t, err)
	repo := postgres.NewDiagramRepo(db)
	d := application.NewDiagrams(repo, repo)
	made, err := d.CreateProject(ctx, ali.ID, application.NewDiagramProject{Name: "Live", Data: dgDoc("a")})
	must(t, err)
	fid := made.File.ID
	other, _, err := d.CreateFile(ctx, ali.ID, made.Project.ID, application.NewDiagramFile{Name: "other"})
	must(t, err)

	st, err := repo.YLoad(ctx, fid)
	if err != nil || st.Snapshot != nil || st.SnapshotSeq != 0 || len(st.Updates) != 0 || st.LastSeq() != 0 {
		t.Fatalf("empty state = %+v, %v", st, err)
	}

	t.Run("updates are numbered from 1 and come back as written", func(t *testing.T) {
		raw := []byte{0, 1, 0, 255, 0, 7} // NUL bytes survive
		for want := int64(1); want <= 3; want++ {
			seq, err := repo.YAppend(ctx, fid, append(raw, byte(want)))
			if err != nil || seq != want {
				t.Fatalf("append = %d, %v; want %d", seq, err, want)
			}
		}
		if seq, _ := repo.YAppend(ctx, other.ID, []byte{9}); seq != 1 {
			t.Errorf("another file starts at %d", seq)
		}
		st, _ := repo.YLoad(ctx, fid)
		if len(st.Updates) != 3 || st.LastSeq() != 3 || !bytes.Equal(st.Updates[1].Data, append(raw, 2)) || st.Updates[2].Seq != 3 {
			t.Errorf("state = %+v", st)
		}
	})

	t.Run("two instances appending at once never share a number", func(t *testing.T) {
		var wg sync.WaitGroup
		seqs := make(chan int64, 20)
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				seq, err := repo.YAppend(ctx, fid, []byte{42})
				if err != nil {
					t.Error(err)
				}
				seqs <- seq
			}()
		}
		wg.Wait()
		close(seqs)
		seen := map[int64]bool{}
		for s := range seqs {
			if seen[s] || s < 4 || s > 23 {
				t.Errorf("seq %d repeated or out of range", s)
			}
			seen[s] = true
		}
		if st, _ := repo.YLoad(ctx, fid); st.LastSeq() != 23 || len(st.Updates) != 23 {
			t.Errorf("last = %d, updates = %d", st.LastSeq(), len(st.Updates))
		}
	})

	t.Run("compaction replaces the snapshot and drops the updates up to seq", func(t *testing.T) {
		for _, c := range []struct {
			seq  int64
			want bool
		}{{0, false}, {24, false}, {20, true}, {20, false}, {10, false}, {23, true}} {
			ok, err := repo.YCompact(ctx, fid, []byte{byte(c.seq), 0, 1}, c.seq)
			if err != nil || ok != c.want {
				t.Errorf("compact(%d) = %v, %v; want %v", c.seq, ok, err, c.want)
			}
			if c.seq == 20 && c.want {
				st, _ := repo.YLoad(ctx, fid)
				if st.SnapshotSeq != 20 || !bytes.Equal(st.Snapshot, []byte{20, 0, 1}) || len(st.Updates) != 3 || st.Updates[0].Seq != 21 {
					t.Errorf("after compacting to 20: %+v", st)
				}
			}
		}
		st, _ := repo.YLoad(ctx, fid)
		if st.SnapshotSeq != 23 || len(st.Updates) != 0 || st.LastSeq() != 23 {
			t.Errorf("after compacting everything: %+v", st)
		}
		// every update dropped: the next number still follows the snapshot
		if seq, err := repo.YAppend(ctx, fid, []byte{1}); err != nil || seq != 24 {
			t.Errorf("append after compaction = %d, %v", seq, err)
		}
		if ok, _ := repo.YCompact(ctx, "nope", []byte{1}, 1); ok {
			t.Error("compacted a file that does not exist")
		}
	})

	t.Run("a file that does not exist", func(t *testing.T) {
		if _, err := repo.YAppend(ctx, "fil_missing0001", []byte{1}); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("append = %v", err)
		}
		if st, err := repo.YLoad(ctx, "fil_missing0001"); err != nil || st.LastSeq() != 0 {
			t.Errorf("load = %+v, %v", st, err)
		}
	})

	t.Run("reset forgets everything, deleting the file too", func(t *testing.T) {
		must(t, repo.YReset(ctx, fid))
		if st, _ := repo.YLoad(ctx, fid); st.Snapshot != nil || len(st.Updates) != 0 || st.LastSeq() != 0 {
			t.Errorf("after reset: %+v", st)
		}
		if seq, _ := repo.YAppend(ctx, fid, []byte{1}); seq != 1 {
			t.Errorf("after reset the numbers start again: %d", seq)
		}
		must(t, d.DeleteFile(ctx, ali.ID, other.ID))
		if n := rowCount(t, db, "dg_file_yupdates WHERE file_id = '"+other.ID+"'"); n != 0 {
			t.Errorf("%d updates of a deleted file", n)
		}
		must(t, d.DeleteProject(ctx, ali.ID, made.Project.ID))
		if n := rowCount(t, db, "dg_file_yupdates WHERE file_id = '"+fid+"'") + rowCount(t, db, "dg_file_ydocs WHERE file_id = '"+fid+"'"); n != 0 {
			t.Errorf("%d rows of a deleted project", n)
		}
	})
}

// A restore resets the Yjs state while live editors may still append: the
// two serialize on the file's lock, so the outcome is all or nothing.
func TestDiagramYResetVsAppendMany(t *testing.T) {
	db := connect(t)
	ctx := context.Background()
	ali, err := postgres.NewUserRepo(db).Create(ctx, "dg_yrace", "dg_yrace@example.com")
	must(t, err)
	repo := postgres.NewDiagramRepo(db)
	d := application.NewDiagrams(repo, repo)
	made, err := d.CreateProject(ctx, ali.ID, application.NewDiagramProject{Name: "Race", Data: dgDoc("a")})
	must(t, err)
	fid := made.File.ID

	batch := [][]byte{{1}, {2}, {3}, {4}}
	for round := 0; round < 20; round++ {
		must(t, repo.YReset(ctx, fid))
		var wg sync.WaitGroup
		var last int64
		wg.Add(2)
		go func() {
			defer wg.Done()
			var err error
			if last, err = repo.YAppendMany(ctx, fid, batch); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := repo.YReset(ctx, fid); err != nil {
				t.Error(err)
			}
		}()
		wg.Wait()
		st, err := repo.YLoad(ctx, fid)
		must(t, err)
		switch len(st.Updates) {
		case 0: // append, then reset: nothing survives
			if st.Snapshot != nil || st.LastSeq() != 0 {
				t.Fatalf("round %d: reset left %+v", round, st)
			}
		case len(batch): // reset, then append: the batch is whole and numbered from 1
			for i, u := range st.Updates {
				if u.Seq != int64(i+1) || !bytes.Equal(u.Data, batch[i]) {
					t.Fatalf("round %d: update %d = %+v", round, i, u)
				}
			}
			if last != int64(len(batch)) {
				t.Fatalf("round %d: last seq %d", round, last)
			}
		default:
			t.Fatalf("round %d: %d of %d updates survived", round, len(st.Updates), len(batch))
		}
	}
	must(t, d.DeleteProject(ctx, ali.ID, made.Project.ID))
}
