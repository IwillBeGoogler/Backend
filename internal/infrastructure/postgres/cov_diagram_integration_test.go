package postgres_test

// Coverage of the diagram repository (core, Yjs state) and postgres.go.
// Error branches are reached by fault injection: covDiaSweep re-runs a
// scenario on a fresh pool whose connection fails at the n-th write, for
// n = 0, 1, 2 ... until a run is never hit; that last run is a clean one and
// must pass all its checks. Skipped with -short or without Docker.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/postgres"
)

type covDiaFault struct {
	left atomic.Int64
	hit  atomic.Bool
}

type covDiaConn struct {
	net.Conn
	f *covDiaFault
}

func (c covDiaConn) Write(b []byte) (int, error) {
	if c.f.left.Add(-1) < 0 {
		c.f.hit.Store(true)
		_ = c.Conn.Close()
		return 0, errors.New("covDia: injected write failure")
	}
	return c.Conn.Write(b)
}

// covDiaSweep runs fn (given a unique tag per run) under every failure point.
func covDiaSweep(t *testing.T, db *pgxpool.Pool, name string, fn func(ctx context.Context, tag string, db *pgxpool.Pool) error) {
	t.Helper()
	ctx := context.Background()
	for n := 0; n < 1500; n++ {
		f := &covDiaFault{}
		f.left.Store(1 << 60)
		cfg := db.Config().Copy()
		cfg.MinConns, cfg.MaxConns = 0, 1
		cfg.ConnConfig.DialFunc = func(ctx context.Context, network, addr string) (net.Conn, error) {
			c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			return covDiaConn{c, f}, nil
		}
		p, err := pgxpool.NewWithConfig(ctx, cfg)
		must(t, err)
		c, err := p.Acquire(ctx)
		must(t, err)
		c.Release()
		f.left.Store(int64(n))
		err = fn(ctx, fmt.Sprintf("%s%d", name, n), p)
		p.Close()
		if !f.hit.Load() {
			if err != nil {
				t.Fatalf("clean run %d: %v", n, err)
			}
			return
		}
	}
	t.Fatal("sweep never completed")
}

func covDiaRun(steps ...func() error) error {
	for i, s := range steps {
		if err := s(); err != nil {
			return fmt.Errorf("step %d: %w", i, err)
		}
	}
	return nil
}

// covDiaChk passes err through, or fails the check.
func covDiaChk(err error, ok bool, msg string) error {
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("check failed: " + msg)
	}
	return nil
}

// covDiaIs wants err to be target.
func covDiaIs(err, target error) error {
	if err == nil {
		return fmt.Errorf("want %v, got nil", target)
	}
	if errors.Is(err, target) {
		return nil
	}
	return err
}

var errCovDia = errors.New("covDia sentinel")

type covDiaW struct {
	r            *postgres.DiagramRepo
	owner, other int
	tag          string
	pid          string
	fld1, fld2   string
	fa, fb       string
	now          time.Time
	data         json.RawMessage
	missing      string
}

func covDiaNewFile(w *covDiaW, id string, folder *string, name string, pos float64) diagram.NewFile {
	return diagram.NewFile{
		Doc: diagram.Doc{
			File: diagram.File{ID: id, ProjectID: w.pid, FolderID: folder, Name: name, Kind: "sdd", Position: pos, Version: 1,
				PageCount: 1, CreatedAt: w.now, UpdatedAt: w.now, UpdatedBy: w.owner},
			Data: w.data,
		},
		First: diagram.Version{Version: 1, Name: name, Data: w.data, CreatedAt: w.now, CreatedBy: w.owner},
	}
}

// covDiaBuild creates a project with two folders (child listed first) and
// two files, and checks the duplicate-id conflicts.
func covDiaBuild(ctx context.Context, db *pgxpool.Pool, tag string, owner, other int) (*covDiaW, error) {
	w := &covDiaW{r: postgres.NewDiagramRepo(db), owner: owner, other: other, tag: tag, now: time.Now().UTC().Truncate(time.Microsecond),
		data: json.RawMessage(`{"nodes":[]}`), missing: "missing_" + tag}
	w.pid, w.fld1, w.fld2, w.fa, w.fb = "prj_"+tag, "fld_"+tag+"1", "fld_"+tag+"2", "fil_"+tag+"a", "fil_"+tag+"b"
	p := diagram.Project{ID: w.pid, OwnerID: owner, Name: "<" + tag + ">", TreeRev: 1, CreatedAt: w.now, UpdatedAt: w.now, UpdatedBy: owner}
	return w, covDiaRun(
		func() error { return w.r.CreateProject(ctx, p) },
		func() error { return covDiaIs(w.r.CreateProject(ctx, p), domain.ErrConflict) },
		func() error {
			parent := w.fld1
			return w.r.InsertTree(ctx,
				[]diagram.Folder{
					{ID: w.fld2, ProjectID: w.pid, ParentID: &parent, Name: "child", Position: 1, CreatedAt: w.now, UpdatedAt: w.now},
					{ID: w.fld1, ProjectID: w.pid, Name: "root", Position: 0, CreatedAt: w.now, UpdatedAt: w.now},
				},
				[]diagram.NewFile{covDiaNewFile(w, w.fa, &parent, "a", 0), covDiaNewFile(w, w.fb, nil, "b", 1)})
		},
		func() error { return w.r.InsertTree(ctx, nil, nil) },
		func() error {
			return covDiaIs(w.r.InsertTree(ctx, nil, []diagram.NewFile{covDiaNewFile(w, w.fa, nil, "dup", 0)}), domain.ErrConflict)
		},
		func() error {
			return covDiaIs(w.r.InsertFile(ctx, covDiaNewFile(w, w.fa, nil, "dup", 0)), domain.ErrConflict)
		},
	)
}

func covDiaUsers(t *testing.T, db *pgxpool.Pool) (owner, other int) {
	t.Helper()
	users := postgres.NewUserRepo(db)
	a, err := users.Create(context.Background(), "covdia_owner", "covdia_owner@example.com")
	must(t, err)
	b, err := users.Create(context.Background(), "covdia_other", "covdia_other@example.com")
	must(t, err)
	return a.ID, b.ID
}

func TestCovDiagramRepo(t *testing.T) {
	db := connect(t)
	owner, other := covDiaUsers(t, db)

	// postgres.go first: t.Setenv and no parallelism.
	t.Run("connect and migrate branches", func(t *testing.T) {
		ctx := context.Background()
		if _, err := postgres.Connect(ctx, "://bad", postgres.Options{}); err == nil {
			t.Error("bad url: want error")
		}
		if _, err := postgres.Connect(ctx, "postgres://u:p@127.0.0.1:1/db?connect_timeout=2", postgres.Options{}); err == nil {
			t.Error("unreachable: want error")
		}
		if err := postgres.Migrate(ctx, "://bad"); err == nil {
			t.Error("Migrate bad url: want error")
		}
		if err := postgres.MigrateDownTo(ctx, "://bad", 0); err == nil {
			t.Error("MigrateDownTo bad url: want error")
		}
		if err := postgres.Migrate(ctx, "postgres://u:p@127.0.0.1:1/db?connect_timeout=2"); err == nil {
			t.Error("Migrate unreachable: want error")
		}
		if v, err := postgres.LatestMigration(); err != nil || v == 0 {
			t.Errorf("LatestMigration = %d, %v", v, err)
		}

		t.Setenv("PG_MAX_CONNS", "3")
		t.Setenv("PG_MIN_CONNS", "x")
		t.Setenv("PG_HEALTH_CHECK_PERIOD", "45s")
		dsn := db.Config().ConnString()
		p, err := postgres.Connect(ctx, dsn, postgres.Options{Migrate: true})
		must(t, err)
		if p.Config().MaxConns != 3 {
			t.Errorf("MaxConns = %d, want 3 from env", p.Config().MaxConns)
		}
		p.Close()
		p, err = postgres.Connect(ctx, dsn, postgres.Options{})
		must(t, err)
		p.Close()

		// migrate a scratch database, then "roll back" to its latest version (a no-op)
		if _, err := db.Exec(ctx, `CREATE DATABASE covdia_down`); err != nil {
			t.Skipf("cannot create database: %v", err)
		}
		du, err := url.Parse(dsn)
		must(t, err)
		du.Path = "/covdia_down"
		latest, err := postgres.LatestMigration()
		must(t, err)
		must(t, postgres.Migrate(ctx, du.String()))
		must(t, postgres.MigrateDownTo(ctx, du.String(), latest))

		// a database whose migration table is unusable: Connect reports the migrate error
		if _, err := db.Exec(ctx, `CREATE DATABASE covdia_badmig`); err != nil {
			t.Skipf("cannot create database: %v", err)
		}
		u, err := url.Parse(dsn)
		must(t, err)
		u.Path = "/covdia_badmig"
		bad := *u
		bp, err := postgres.Connect(ctx, bad.String(), postgres.Options{})
		must(t, err)
		_, err = bp.Exec(ctx, `CREATE TABLE migration_version (x int)`)
		must(t, err)
		bp.Close()
		if p, err := postgres.Connect(ctx, bad.String(), postgres.Options{Migrate: true}); err == nil {
			p.Close()
			t.Error("Connect with broken migration table: want error")
		}
	})

	t.Run("exists and deleteByID helpers", func(t *testing.T) {
		covDiaSweep(t, db, "cat", func(ctx context.Context, tag string, p *pgxpool.Pool) error {
			cats := postgres.NewCategoryRepo(p)
			var id int
			return covDiaRun(
				func() error { c, err := cats.Create(ctx, "covdia "+tag); id = c.ID; return err },
				func() error { ok, err := cats.Exists(ctx, id); return covDiaChk(err, ok, "exists") },
				func() error { ok, err := cats.Exists(ctx, -1); return covDiaChk(err, !ok, "not exists") },
				func() error { return cats.Delete(ctx, id) },
				func() error { return covDiaIs(cats.Delete(ctx, id), domain.ErrNotFound) },
			)
		})
	})

	t.Run("not found and edge cases", func(t *testing.T) {
		ctx := context.Background()
		r := postgres.NewDiagramRepo(db)
		now := time.Now()
		for name, err := range map[string]error{
			"RenameProject":  r.RenameProject(ctx, "nope", "x", now),
			"UpdateFolder":   r.UpdateFolder(ctx, diagram.Folder{ID: "nope", Name: "x", UpdatedAt: now}),
			"UpdateFileMeta": r.UpdateFileMeta(ctx, diagram.File{ID: "nope", Name: "x", Kind: "sdd", UpdatedAt: now}),
		} {
			if !errors.Is(err, domain.ErrNotFound) {
				t.Errorf("%s: err = %v, want ErrNotFound", name, err)
			}
		}
		if _, err := r.VersionInfo(ctx, "nope", 1); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("VersionInfo: %v", err)
		}
		if _, err := r.NameHead(ctx, "nope", "l", owner, now); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("NameHead: %v", err)
		}
		if saved, err := r.SaveFileOver(ctx, diagram.Doc{File: diagram.File{ID: "nope"}, Data: json.RawMessage(`{}`)}, 1, diagram.Version{}); saved || err != nil {
			t.Errorf("SaveFileOver missing = %v, %v", saved, err)
		}
		if n, err := r.DeleteFolders(ctx, "nope", []string{"x"}); n != 0 || err != nil {
			t.Errorf("DeleteFolders missing = %d, %v", n, err)
		}
		if p, err := r.Prefs(ctx, owner); err != nil || string(p) != `{}` {
			t.Errorf("Prefs empty = %s, %v", p, err)
		}
		// an already-cancelled context fails every query
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		if _, _, err := r.ListProjects(cctx, owner, diagram.ProjectQuery{Page: 1, PageSize: 5}); err == nil {
			t.Error("ListProjects cancelled: want error")
		}
		if _, _, err := r.Tree(cctx, "x"); err == nil {
			t.Error("Tree cancelled: want error")
		}
		if _, err := r.YLoad(cctx, "x"); err == nil {
			t.Error("YLoad cancelled: want error")
		}
	})

	t.Run("core", func(t *testing.T) {
		t.Parallel()
		covDiaSweep(t, db, "core", func(ctx context.Context, tag string, p *pgxpool.Pool) error {
			w, err := covDiaBuild(ctx, p, tag, owner, other)
			if err != nil {
				return err
			}
			r := w.r
			later := w.now.Add(time.Minute)
			ver2 := diagram.Version{Version: 2, Name: "a2", Data: json.RawMessage(`{"nodes":[1]}`), CreatedAt: later, CreatedBy: owner, CreatedByShare: "shr_" + tag}
			doc2 := func(v int, name string) diagram.Doc {
				return diagram.Doc{File: diagram.File{ID: w.fa, Name: name, Kind: "sdd", Version: v, PageCount: 2, UpdatedAt: later, UpdatedBy: owner, UpdatedByShare: "shr_" + tag, Restored: true},
					Data: json.RawMessage(`{"nodes":[1]}`)}
			}
			return covDiaRun(
				func() error {
					pr, err := r.Project(ctx, w.pid)
					return covDiaChk(err, pr.Name == "<"+tag+">", "project")
				},
				func() error { _, err := r.Project(ctx, w.missing); return covDiaIs(err, domain.ErrNotFound) },
				func() error {
					l, total, err := r.ListProjects(ctx, owner, diagram.ProjectQuery{Page: 1, PageSize: 100, Search: "<" + tag + ">%_"})
					return covDiaChk(err, len(l) == 0 && total == 0, "list search escapes wildcards")
				},
				func() error {
					l, total, err := r.ListProjects(ctx, owner, diagram.ProjectQuery{Page: 1, PageSize: 100, Search: "<" + tag + ">"})
					return covDiaChk(err, len(l) == 1 && total == 1 && l[0].Role == diagram.RoleOwner && l[0].FileCount == 2 && l[0].FolderCount == 2, "list search")
				},
				func() error {
					l, _, err := r.ListProjects(ctx, owner, diagram.ProjectQuery{Page: 1, PageSize: 3, SortByName: true, Asc: true})
					return covDiaChk(err, len(l) >= 1, "list by name asc")
				},
				func() error {
					_, _, err := r.ListProjects(ctx, other, diagram.ProjectQuery{Page: 1, PageSize: 3})
					return err
				},
				func() error {
					f, d, err := r.Counts(ctx, w.pid)
					return covDiaChk(err, f == 2 && d == 2, "counts")
				},
				func() error {
					id, _, err := r.ProjectThumb(ctx, w.pid)
					return covDiaChk(err, id == w.fa, "thumb is first file (position 0)")
				},
				func() error {
					id, _, err := r.ProjectThumb(ctx, w.missing)
					return covDiaChk(err, id == "", "thumb of none")
				},
				func() error { return r.RenameProject(ctx, w.pid, "renamed", later) },
				func() error { return covDiaIs(r.RenameProject(ctx, w.missing, "x", later), domain.ErrNotFound) },
				func() error {
					rev, err := r.TouchTree(ctx, w.pid, owner, later)
					return covDiaChk(err, rev == 2, "tree rev")
				},
				func() error {
					_, err := r.TouchTree(ctx, w.missing, 0, later)
					return covDiaIs(err, domain.ErrNotFound)
				},
				func() error {
					role, err := r.MemberRole(ctx, w.pid, other)
					return covDiaChk(err, role == "", "no member role")
				},
				func() error {
					role, err := r.FileRole(ctx, w.fa, other)
					return covDiaChk(err, role == "", "no file role")
				},
				func() error { f, err := r.Folder(ctx, w.fld1); return covDiaChk(err, f.Name == "root", "folder") },
				func() error { _, err := r.Folder(ctx, w.missing); return covDiaIs(err, domain.ErrNotFound) },
				func() error {
					return r.CreateFolder(ctx, diagram.Folder{ID: w.fld1 + "x", ProjectID: w.pid, Name: "x", CreatedAt: w.now, UpdatedAt: w.now})
				},
				func() error {
					return covDiaIs(r.CreateFolder(ctx, diagram.Folder{ID: w.fld1, ProjectID: w.pid, Name: "x", CreatedAt: w.now, UpdatedAt: w.now}), domain.ErrConflict)
				},
				func() error {
					return r.UpdateFolder(ctx, diagram.Folder{ID: w.fld2, ParentID: nil, Name: "moved", Position: 5, UpdatedAt: later})
				},
				func() error {
					return covDiaIs(r.UpdateFolder(ctx, diagram.Folder{ID: w.missing, Name: "x", UpdatedAt: later}), domain.ErrNotFound)
				},
				func() error {
					pos, err := r.NextPosition(ctx, w.pid, nil)
					return covDiaChk(err, pos >= 6, "next position")
				},
				func() error {
					pos, err := r.NextPosition(ctx, w.pid, &w.fld1)
					return covDiaChk(err, pos == 1, "next position in folder")
				},
				func() error {
					fs, files, err := r.Tree(ctx, w.pid)
					return covDiaChk(err, len(fs) == 3 && len(files) == 2, "tree")
				},
				func() error {
					d, err := r.File(ctx, w.fa)
					return covDiaChk(err, d.Name == "a" && len(d.Data) > 0, "file")
				},
				func() error { _, err := r.File(ctx, w.missing); return covDiaIs(err, domain.ErrNotFound) },
				func() error {
					return r.UpdateFileMeta(ctx, diagram.File{ID: w.fb, Name: "b2", Kind: "erd", FolderID: &w.fld1, Position: 3, UpdatedAt: later})
				},
				func() error {
					return covDiaIs(r.UpdateFileMeta(ctx, diagram.File{ID: w.missing, Name: "x", Kind: "sdd", UpdatedAt: later}), domain.ErrNotFound)
				},
				func() error {
					ok, err := r.SaveFile(ctx, doc2(2, "a2"), 1, ver2)
					return covDiaChk(err, ok, "save 1->2")
				},
				func() error {
					ok, err := r.SaveFile(ctx, doc2(3, "stale"), 1, ver2)
					return covDiaChk(err, !ok, "stale save loses")
				},
				func() error {
					// rewrites the unnamed snapshot of version 2 in place
					v3 := ver2
					v3.Version = 3
					ok, err := r.SaveFileOver(ctx, doc2(3, "a3"), 2, v3)
					return covDiaChk(err, ok, "save over 2->3")
				},
				func() error {
					n, err := r.CountVersions(ctx, w.fa)
					return covDiaChk(err, n == 2, "versions after rewrite")
				},
				func() error {
					l := "milestone"
					v, err := r.NameHead(ctx, w.fa, l, owner, later)
					return covDiaChk(err, v.Version == 3 && v.Named && *v.Label == l, "name head")
				},
				func() error {
					v, err := r.NameHead(ctx, w.fa, "renamed", 0, later)
					return covDiaChk(err, v.Version == 3 && *v.Label == "renamed", "name head again updates the label")
				},
				func() error {
					// the head is named now: a rewrite cannot reuse it and appends instead
					v4 := ver2
					v4.Version = 4
					ok, err := r.SaveFileOver(ctx, doc2(4, "a4"), 3, v4)
					return covDiaChk(err, ok, "save over a named head appends")
				},
				func() error { n, err := r.CountVersions(ctx, w.fa); return covDiaChk(err, n == 3, "versions") },
				func() error {
					vs, err := r.Versions(ctx, w.fa)
					return covDiaChk(err, len(vs) == 3 && vs[0].Version == 4 && vs[0].CreatedByName != "", "versions list")
				},
				func() error {
					v, err := r.GetVersion(ctx, w.fa, 3)
					return covDiaChk(err, v.Version == 3 && len(v.Data) > 0, "get version")
				},
				func() error { _, err := r.GetVersion(ctx, w.fa, 99); return covDiaIs(err, domain.ErrNotFound) },
				func() error {
					v, err := r.VersionInfo(ctx, w.fa, 3)
					return covDiaChk(err, v.Version == 3 && v.Named, "version info")
				},
				func() error { _, err := r.VersionInfo(ctx, w.fa, 99); return covDiaIs(err, domain.ErrNotFound) },
				func() error { return r.DeleteVersions(ctx, w.fa, []int{1, 2}) },
				func() error { p, err := r.Prefs(ctx, owner); return covDiaChk(err, len(p) > 0, "prefs default") },
				func() error {
					out, err := r.UpdatePrefs(ctx, owner, later, func(cur json.RawMessage) (json.RawMessage, error) {
						return json.RawMessage(`{"theme":"dark"}`), nil
					})
					return covDiaChk(err, string(out) == `{"theme": "dark"}` || string(out) == `{"theme":"dark"}`, "update prefs")
				},
				func() error {
					_, err := r.UpdatePrefs(ctx, owner, later, func(json.RawMessage) (json.RawMessage, error) { return nil, errCovDia })
					return covDiaIs(err, errCovDia)
				},
				func() error {
					p, err := r.Prefs(ctx, owner)
					return covDiaChk(err, len(p) > 0 && string(p) != `{}`, "prefs stored")
				},
				func() error { return r.DeleteFile(ctx, w.fb) },
				func() error { return covDiaIs(r.DeleteFile(ctx, w.fb), domain.ErrNotFound) },
				func() error {
					n, err := r.DeleteFolders(ctx, w.pid, []string{w.fld1})
					return covDiaChk(err, n == 1, "delete folders counts the files inside")
				},
				func() error { return r.DeleteProject(ctx, w.pid) },
				func() error { return covDiaIs(r.DeleteProject(ctx, w.pid), domain.ErrNotFound) },
			)
		})
	})

	t.Run("yjs", func(t *testing.T) {
		t.Parallel()
		covDiaSweep(t, db, "yjs", func(ctx context.Context, tag string, p *pgxpool.Pool) error {
			w, err := covDiaBuild(ctx, p, tag, owner, other)
			if err != nil {
				return err
			}
			r := w.r
			return covDiaRun(
				func() error {
					s, err := r.YLoad(ctx, w.fa)
					return covDiaChk(err, len(s.Updates) == 0 && s.SnapshotSeq == 0, "empty state")
				},
				func() error { s, err := r.YAppend(ctx, w.fa, []byte{1}); return covDiaChk(err, s == 1, "append 1") },
				func() error { _, err := r.YAppend(ctx, w.missing, []byte{1}); return covDiaIs(err, domain.ErrNotFound) },
				func() error {
					s, err := r.YAppendMany(ctx, w.fa, [][]byte{{2}, {3}})
					return covDiaChk(err, s == 3, "append many")
				},
				func() error {
					_, err := r.YAppendMany(ctx, w.missing, [][]byte{{2}})
					return covDiaIs(err, domain.ErrNotFound)
				},
				func() error {
					s, err := r.YLoad(ctx, w.fa)
					return covDiaChk(err, len(s.Updates) == 3, "three updates")
				},
				func() error {
					ok, err := r.YCompact(ctx, w.fa, []byte("snap"), 2)
					return covDiaChk(err, ok, "compact to 2")
				},
				func() error {
					ok, err := r.YCompact(ctx, w.fa, []byte("snap"), 2)
					return covDiaChk(err, !ok, "compact again is a no-op")
				},
				func() error {
					ok, err := r.YCompact(ctx, w.fa, []byte("snap"), 99)
					return covDiaChk(err, !ok, "compact past the end is a no-op")
				},
				func() error {
					ok, err := r.YCompact(ctx, w.missing, []byte("snap"), 1)
					return covDiaChk(err, !ok, "compact of a missing file")
				},
				func() error {
					s, err := r.YLoad(ctx, w.fa)
					return covDiaChk(err, s.SnapshotSeq == 2 && len(s.Updates) == 1 && string(s.Snapshot) == "snap", "state after compaction")
				},
				func() error {
					s, err := r.YAppend(ctx, w.fa, []byte{4})
					return covDiaChk(err, s == 4, "append after compaction")
				},
				func() error { return r.YReset(ctx, w.fa) },
				func() error {
					s, err := r.YLoad(ctx, w.fa)
					return covDiaChk(err, s.SnapshotSeq == 0 && len(s.Updates) == 0, "reset")
				},
				func() error {
					s, err := r.YAppend(ctx, w.fa, []byte{5})
					return covDiaChk(err, s == 1, "numbering restarts")
				},
			)
		})
	})
}
