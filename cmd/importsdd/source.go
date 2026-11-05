package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// Row is one row of the old server's database, keyed by column. Values are
// what the drivers return (string, int64, float64, []byte, nil). A column the
// old schema did not have yet reads as absent, so databases from any older
// migration import with defaults.
type Row map[string]any

func (r Row) str(k string) string {
	switch v := r[k].(type) {
	case string:
		return v
	case []byte:
		return string(v)
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

// strp is nil for NULL or an absent column.
func (r Row) strp(k string) *string {
	if r[k] == nil {
		return nil
	}
	s := r.str(k)
	return &s
}

func (r Row) i64(k string) (int64, bool) {
	switch v := r[k].(type) {
	case int64:
		return v, true
	case int32:
		return int64(v), true
	case int16:
		return int64(v), true
	case int:
		return int64(v), true
	case float64:
		return int64(v), true
	case bool:
		if v {
			return 1, true
		}
		return 0, true
	case string:
		var n int64
		_, err := fmt.Sscanf(v, "%d", &n)
		return n, err == nil
	}
	return 0, false
}

func (r Row) int(k string, def int) int {
	if n, ok := r.i64(k); ok {
		return int(n)
	}
	return def
}

func (r Row) f64(k string) float64 {
	if f, ok := r[k].(float64); ok {
		return f
	}
	n, _ := r.i64(k)
	return float64(n)
}

func (r Row) flag(k string, def bool) bool {
	if n, ok := r.i64(k); ok {
		return n != 0
	}
	return def
}

// epoch reads the old server's timestamps: unix milliseconds (seconds when
// the value is too small to be milliseconds).
func epoch(n int64) time.Time {
	if n > -1e11 && n < 1e11 {
		return time.Unix(n, 0).UTC()
	}
	return time.UnixMilli(n).UTC()
}

func (r Row) ts(k string, def time.Time) time.Time {
	if n, ok := r.i64(k); ok {
		return epoch(n)
	}
	return def
}

func (r Row) tsp(k string) *time.Time {
	if n, ok := r.i64(k); ok {
		t := epoch(n)
		return &t
	}
	return nil
}

// Source reads the old server's tables.
type Source interface {
	// Has reports whether the table exists (old databases lack newer tables).
	Has(ctx context.Context, table string) (bool, error)
	// Select reads "SELECT * FROM table [WHERE where]"; "?" marks arguments.
	// Table and where are fixed strings of this program, never user input.
	Select(ctx context.Context, table, where string, args ...any) ([]Row, error)
	Close() error
}

type sqliteSource struct{ db *sql.DB }

// OpenSQLite opens the old SQLite file read only.
func OpenSQLite(path string) (Source, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return sqliteSource{db}, nil
}

func (s sqliteSource) Has(ctx context.Context, table string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&n)
	return n > 0, err
}

func (s sqliteSource) Select(ctx context.Context, table, where string, args ...any) ([]Row, error) {
	q := "SELECT * FROM " + table
	if where != "" {
		q += " WHERE " + where
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []Row
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		r := make(Row, len(cols))
		for i, c := range cols {
			r[c] = vals[i]
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s sqliteSource) Close() error { return s.db.Close() }

type pgSource struct{ conn *pgx.Conn }

// OpenPostgres connects to the old server's Postgres. The session is read
// only.
func OpenPostgres(ctx context.Context, url string) (Source, error) {
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Exec(ctx, `SET default_transaction_read_only = on`); err != nil {
		conn.Close(ctx)
		return nil, err
	}
	return pgSource{conn}, nil
}

func (s pgSource) Has(ctx context.Context, table string) (bool, error) {
	var ok bool
	err := s.conn.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, "public."+table).Scan(&ok)
	return ok, err
}

func (s pgSource) Select(ctx context.Context, table, where string, args ...any) ([]Row, error) {
	q := "SELECT * FROM " + table
	if where != "" {
		for i := 1; strings.Contains(where, "?"); i++ {
			where = strings.Replace(where, "?", fmt.Sprintf("$%d", i), 1)
		}
		q += " WHERE " + where
	}
	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		fds := rows.FieldDescriptions()
		r := make(Row, len(fds))
		for i, fd := range fds {
			r[fd.Name] = vals[i]
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s pgSource) Close() error { return s.conn.Close(context.Background()) }

var errBlobMissing = errors.New("blob missing")
