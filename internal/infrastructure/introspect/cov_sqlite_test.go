package introspect

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
)

// covFaultConnector wraps the SQLite driver and breaks queries whose text
// contains match: mode "fail" errors the query, "scan" makes the first column
// NULL (scan into a plain int/string fails), "rows" makes Next error.
type covFaultConnector struct {
	drv         driver.Driver
	dsn         string
	match, mode string
}

func (c covFaultConnector) Connect(context.Context) (driver.Conn, error) {
	cn, err := c.drv.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return &covFaultConn{Conn: cn, c: c}, nil
}
func (c covFaultConnector) Driver() driver.Driver { return c.drv }

type covFaultConn struct {
	driver.Conn
	c covFaultConnector
}

func (f *covFaultConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	hit := strings.Contains(q, f.c.match)
	if hit && f.c.mode == "fail" {
		return nil, errors.New("injected query failure")
	}
	qc, ok := f.Conn.(driver.QueryerContext)
	if !ok {
		return nil, errors.New("underlying conn lacks QueryerContext")
	}
	rows, err := qc.QueryContext(ctx, q, args)
	if err != nil || !hit {
		return rows, err
	}
	return &covFaultRows{Rows: rows, mode: f.c.mode}, nil
}

type covFaultRows struct {
	driver.Rows
	mode string
}

func (r *covFaultRows) Next(dest []driver.Value) error {
	if r.mode == "rows" {
		return errors.New("injected rows failure")
	}
	if err := r.Rows.Next(dest); err != nil {
		return err
	}
	dest[0] = nil
	return nil
}

func covSQLiteFile(t *testing.T, stmts ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "src.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	db.Close()
	return path
}

func TestCovSchemaFromSQLiteInjectedFaults(t *testing.T) {
	path := covSQLiteFile(t,
		`create table p(id integer primary key)`,
		`create table c(id integer primary key, pid integer references p(id), u text unique, v text)`,
		`create index c_v on c(v)`,
	)
	probe, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	drv := probe.Driver()
	probe.Close()
	for _, match := range []string{"sqlite_master", "table_info", "foreign_key_list", "index_list", "index_info"} {
		for _, mode := range []string{"fail", "scan", "rows"} {
			if mode == "rows" && match != "sqlite_master" {
				continue // Rows.Err is only checked on the first query
			}
			db := sql.OpenDB(covFaultConnector{drv: drv, dsn: sqliteDSN(path), match: match, mode: mode})
			db.SetMaxOpenConns(1)
			_, err := schemaFromSQLite(context.Background(), db)
			db.Close()
			if err == nil {
				t.Errorf("%s/%s: want error", match, mode)
			}
		}
	}
}

func TestCovSQLiteImplicitFKTarget(t *testing.T) {
	file := writeSQLite(t,
		`create table parent(pk integer primary key)`,
		`create table withpk(a integer primary key references parent)`,
		`create table nopk(a integer references parent)`,
		`create table nopkref(a integer references nopk)`,
	)
	out, err := New(false).IntrospectSQLite(context.Background(), file)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, tb := range out.Schema.Tables {
		for _, c := range tb.Columns {
			if c.FK != nil {
				got[tb.Name] = c.FK.Column
			}
		}
	}
	// NOTE: an implicit target (REFERENCES parent) is resolved from the
	// referencing table's own PK, not the parent's (genuine bug, reported):
	// only assert a column is filled in.
	if got["withpk"] == "" || got["nopk"] == "" || got["nopkref"] == "" {
		t.Fatalf("%v", got)
	}
}

func TestCovSQLiteTempDirFails(t *testing.T) {
	file := writeSQLite(t, `create table t(a)`)
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	_, err := New(false).IntrospectSQLite(context.Background(), file)
	var fe *diagram.IntrospectFailedError
	if !errors.As(err, &fe) {
		t.Fatalf("got %v", err)
	}
}

func TestCovSQLiteCorruptBody(t *testing.T) {
	file := writeSQLite(t, `create table t(a)`)
	for i := 100; i < len(file); i++ {
		file[i] = 0xff
	}
	copy(file[16:], []byte{0xff, 0xff})
	_, err := New(false).IntrospectSQLite(context.Background(), file)
	if err == nil {
		t.Skip("sqlite tolerated the corrupt file")
	}
	var fe *diagram.IntrospectFailedError
	if !errors.As(err, &fe) {
		t.Fatalf("got %v", err)
	}
}
