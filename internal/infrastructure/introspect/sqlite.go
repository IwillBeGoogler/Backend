package introspect

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"

	_ "modernc.org/sqlite" // pure Go SQLite driver

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
)

var sqliteMagic = []byte("SQLite format 3\x00")

// IntrospectSQLite reads an uploaded SQLite database. The bytes are written
// to a private temp file, opened read only (mode=ro, query_only) and the
// file is removed right after.
func (s *Service) IntrospectSQLite(ctx context.Context, file []byte) (diagram.Introspected, error) {
	if len(file) < 100 || !bytes.HasPrefix(file, sqliteMagic[:15]) {
		return diagram.Introspected{}, diagram.NotSQLiteError{}
	}
	f, err := os.CreateTemp("", "sdd-introspect-*.db") // mode 0600
	if err != nil {
		return diagram.Introspected{}, &diagram.IntrospectFailedError{Msg: "could not read the file"}
	}
	path := f.Name()
	defer os.Remove(path)
	_, werr := f.Write(file)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return diagram.Introspected{}, &diagram.IntrospectFailedError{Msg: "could not read the file"}
	}
	ctx, cancel := context.WithTimeout(ctx, Deadline)
	defer cancel()
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		return diagram.Introspected{}, &diagram.IntrospectFailedError{Msg: truncate(Redact(err.Error()), 400)}
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	out, err := schemaFromSQLite(ctx, db)
	if err != nil {
		return diagram.Introspected{}, &diagram.IntrospectFailedError{Msg: truncate(Redact(strings.ReplaceAll(err.Error(), path, "<file>")), 400)}
	}
	return out, nil
}

// sqliteDSN opens path read only: mode=ro, and query_only as a second lock.
func sqliteDSN(path string) string {
	return (&url.URL{Scheme: "file", Path: path, RawQuery: url.Values{"mode": {"ro"}, "_pragma": {"query_only(1)", "busy_timeout(5000)"}}.Encode()}).String()
}

// quoteIdent quotes an identifier for SQLite.
func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

var autoIncrement = regexp.MustCompile(`(?i)autoincrement`)
var intType = regexp.MustCompile(`(?i)int`)

func schemaFromSQLite(ctx context.Context, db *sql.DB) (diagram.Introspected, error) {
	var warnings []string
	rows, err := db.QueryContext(ctx, `select name, type, sql from sqlite_master where type in ('table','view') and name not like 'sqlite\_%' escape '\' order by name`)
	if err != nil {
		return diagram.Introspected{}, err
	}
	type obj struct {
		name, typ string
		sql       sql.NullString
	}
	var objs []obj
	for rows.Next() {
		var o obj
		if err := rows.Scan(&o.name, &o.typ, &o.sql); err != nil {
			rows.Close()
			return diagram.Introspected{}, err
		}
		objs = append(objs, o)
	}
	if err := rows.Err(); err != nil {
		return diagram.Introspected{}, err
	}
	rows.Close()

	tables := []diagram.SchemaTable{}
	for _, o := range objs {
		q := quoteIdent(o.name)
		type info struct {
			name, typ string
			notnull   int
			dflt      sql.NullString
			pk        int
		}
		var cols []info
		r, err := db.QueryContext(ctx, `pragma table_info(`+q+`)`)
		if err != nil {
			return diagram.Introspected{}, err
		}
		for r.Next() {
			var cid int
			var c info
			if err := r.Scan(&cid, &c.name, &c.typ, &c.notnull, &c.dflt, &c.pk); err != nil {
				r.Close()
				return diagram.Introspected{}, err
			}
			cols = append(cols, c)
		}
		r.Close()
		var pkCols []string
		pkRank := map[string]int{}
		for _, c := range cols {
			if c.pk > 0 {
				pkRank[c.name] = c.pk
				pkCols = append(pkCols, c.name)
			}
		}
		sort.SliceStable(pkCols, func(i, j int) bool { return pkRank[pkCols[i]] < pkRank[pkCols[j]] })

		type fkRow struct {
			id             int
			table, from    string
			to             sql.NullString
			onUpd, onDelet string
		}
		var fks []fkRow
		fkCount := map[int]int{}
		if o.typ == "table" {
			r, err := db.QueryContext(ctx, `pragma foreign_key_list(`+q+`)`)
			if err != nil {
				return diagram.Introspected{}, err
			}
			for r.Next() {
				var f fkRow
				var seq int
				var match string
				if err := r.Scan(&f.id, &seq, &f.table, &f.from, &f.to, &f.onUpd, &f.onDelet, &match); err != nil {
					r.Close()
					return diagram.Introspected{}, err
				}
				fks = append(fks, f)
				fkCount[f.id]++
			}
			r.Close()
		}

		uniqueSingle := map[string]bool{}
		indexes := []diagram.SchemaIndex{}
		if o.typ == "table" {
			type ixRow struct {
				name, origin string
				unique       int
			}
			var list []ixRow
			r, err := db.QueryContext(ctx, `pragma index_list(`+q+`)`)
			if err != nil {
				return diagram.Introspected{}, err
			}
			for r.Next() {
				var seq, partial int
				var ix ixRow
				if err := r.Scan(&seq, &ix.name, &ix.unique, &ix.origin, &partial); err != nil {
					r.Close()
					return diagram.Introspected{}, err
				}
				list = append(list, ix)
			}
			r.Close()
			for _, ix := range list {
				ri, err := db.QueryContext(ctx, `pragma index_info(`+quoteIdent(ix.name)+`)`)
				if err != nil {
					return diagram.Introspected{}, err
				}
				var names []string
				expr := false
				for ri.Next() {
					var seqno, cid int
					var n sql.NullString
					if err := ri.Scan(&seqno, &cid, &n); err != nil {
						ri.Close()
						return diagram.Introspected{}, err
					}
					if !n.Valid {
						expr = true
					}
					names = append(names, n.String)
				}
				ri.Close()
				switch {
				case expr:
					warnings = append(warnings, fmt.Sprintf("%s: index %s uses an expression and was skipped", o.name, ix.name))
				case ix.origin == "pk":
				case ix.origin == "u" && len(names) == 1:
					uniqueSingle[names[0]] = true
				default:
					indexes = append(indexes, diagram.SchemaIndex{Name: ix.name, Columns: names, Unique: ix.unique != 0})
				}
			}
			sort.Slice(indexes, func(i, j int) bool { return indexes[i].Name < indexes[j].Name })
		}

		auto := o.sql.Valid && autoIncrement.MatchString(o.sql.String)
		columns := []diagram.SchemaColumn{}
		for _, c := range cols {
			isPK := c.pk > 0
			typ := c.typ
			if typ == "" {
				typ = "TEXT"
			}
			col := diagram.SchemaColumn{Name: c.name, Type: typ, PK: isPK, Unique: uniqueSingle[c.name] && !isPK, Nullable: c.notnull == 0 && !isPK}
			if c.dflt.Valid {
				col.Default = c.dflt.String
			}
			if isPK && len(pkCols) == 1 && auto && intType.MatchString(typ) {
				col.Generated = "autoincrement"
			}
			for _, f := range fks {
				if f.from != c.name {
					continue
				}
				to := f.to.String
				if !f.to.Valid || to == "" {
					to = "id"
					if pk := sqliteFirstPK(ctx, db, f.table); pk != "" {
						to = pk
					}
				}
				fk := &diagram.SchemaFK{Table: f.table, Column: to}
				if f.onDelet != "" && f.onDelet != "NO ACTION" {
					fk.OnDelete = f.onDelet
				}
				if f.onUpd != "" && f.onUpd != "NO ACTION" {
					fk.OnUpdate = f.onUpd
				}
				if fkCount[f.id] > 1 {
					fk.Group = fmt.Sprintf("fk%d", f.id)
				}
				col.FK = fk
				break
			}
			columns = append(columns, col)
		}
		kind := "table"
		if o.typ == "view" {
			kind = "view"
		}
		tables = append(tables, diagram.SchemaTable{Name: o.name, Kind: kind, Columns: columns, Indexes: indexes, Checks: []diagram.SchemaCheck{}})
	}
	return diagram.Introspected{Schema: diagram.Schema{Tables: tables, Enums: []diagram.SchemaEnum{}}, Warnings: nonNil(warnings)}, nil
}

// sqliteFirstPK returns the first primary-key column of table, or "" when
// it has none or cannot be read. An implicit FK target (REFERENCES parent)
// points at the parent's primary key, not the referencing table's.
func sqliteFirstPK(ctx context.Context, db *sql.DB, table string) string {
	r, err := db.QueryContext(ctx, `pragma table_info(`+quoteIdent(table)+`)`)
	if err != nil {
		return ""
	}
	defer r.Close()
	best, name := 0, ""
	for r.Next() {
		var cid, notnull, pk int
		var n, typ string
		var dflt sql.NullString
		if err := r.Scan(&cid, &n, &typ, &notnull, &dflt, &pk); err != nil {
			return ""
		}
		if pk > 0 && (best == 0 || pk < best) {
			best, name = pk, n
		}
	}
	return name
}
