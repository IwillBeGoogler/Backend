// Package schemadump renders the live schema of a PostgreSQL database
// (the public schema) as SQL DDL text. Read reads the catalog into plain
// rows, Render formats them; the two are split so the formatting is
// testable without a database. Shared by GET /api/diagrams/schema and
// cmd/schemadump.
package schemadump

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Column is one table column. Type is format_type() output; Default is the
// raw default expression ("" for none).
type Column struct {
	Name    string
	Type    string
	NotNull bool
	Default string
	Comment string
}

// Constraint is a primary key, unique, foreign key or check constraint.
// Def is pg_get_constraintdef() output (it carries ON DELETE and friends).
type Constraint struct {
	Name    string
	Def     string
	Comment string
}

// Index is a stand-alone index (not one that backs a constraint). Def is
// pg_get_indexdef() output.
type Index struct {
	Name    string
	Def     string
	Comment string
}

// Table is a table with everything hanging off it.
type Table struct {
	Name        string
	Comment     string
	Columns     []Column
	Constraints []Constraint
	Indexes     []Index
}

// Enum is an enum type with its labels in sort order.
type Enum struct {
	Name    string
	Values  []string
	Comment string
}

// Schema is what Render formats.
type Schema struct {
	Enums  []Enum
	Tables []Table
}

// hidden are the migration bookkeeping tables, never part of the dump.
var hidden = []string{"goose_db_version", "migration_version"}

// Querier is the part of a pgx pool Read needs.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Dump reads the public schema and renders it. prefix keeps only the
// tables (and the enums) whose name starts with it; "" keeps all.
func Dump(ctx context.Context, db Querier, prefix string) (string, error) {
	s, err := Read(ctx, db, prefix)
	if err != nil {
		return "", err
	}
	return Render(s), nil
}

const tablesSQL = `SELECT c.oid::int8, c.relname, COALESCE(obj_description(c.oid, 'pg_class'), '')
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p') AND NOT c.relispartition
  AND starts_with(c.relname, $1) AND NOT (c.relname = ANY($2))
ORDER BY c.relname`

const columnsSQL = `SELECT a.attrelid::int8, a.attname, format_type(a.atttypid, a.atttypmod), a.attnotnull,
  COALESCE(pg_get_expr(d.adbin, d.adrelid), ''), COALESCE(col_description(a.attrelid, a.attnum), '')
FROM pg_attribute a LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
WHERE a.attrelid = ANY($1) AND a.attnum > 0 AND NOT a.attisdropped
ORDER BY a.attrelid, a.attnum`

// Foreign keys to a table outside the prefix are left out: the target
// would not be in the dump.
const constraintsSQL = `SELECT c.conrelid::int8, c.conname, pg_get_constraintdef(c.oid), COALESCE(obj_description(c.oid, 'pg_constraint'), '')
FROM pg_constraint c
WHERE c.conrelid = ANY($1) AND c.contype IN ('p', 'u', 'f', 'c')
  AND (c.contype <> 'f' OR (SELECT starts_with(t.relname, $2) AND NOT (t.relname = ANY($3)) FROM pg_class t WHERE t.oid = c.confrelid))
ORDER BY c.conrelid, CASE c.contype WHEN 'p' THEN 0 WHEN 'u' THEN 1 WHEN 'c' THEN 2 ELSE 3 END, c.conname`

const indexesSQL = `SELECT i.indrelid::int8, ic.relname, pg_get_indexdef(i.indexrelid), COALESCE(obj_description(i.indexrelid, 'pg_class'), '')
FROM pg_index i JOIN pg_class ic ON ic.oid = i.indexrelid
WHERE i.indrelid = ANY($1)
  AND NOT EXISTS (SELECT 1 FROM pg_constraint c WHERE c.conindid = i.indexrelid)
ORDER BY i.indrelid, ic.relname`

const enumsSQL = `SELECT t.typname, e.enumlabel, COALESCE(obj_description(t.oid, 'pg_type'), '')
FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace JOIN pg_enum e ON e.enumtypid = t.oid
WHERE n.nspname = 'public' AND starts_with(t.typname, $1)
ORDER BY t.typname, e.enumsortorder`

// Read loads the public schema from the catalog.
func Read(ctx context.Context, db Querier, prefix string) (Schema, error) {
	var s Schema
	rows, err := db.Query(ctx, tablesSQL, prefix, hidden)
	if err != nil {
		return s, err
	}
	byOID := map[int64]*Table{}
	var oids []int64
	var order []int64
	for rows.Next() {
		var oid int64
		var t Table
		if err := rows.Scan(&oid, &t.Name, &t.Comment); err != nil {
			rows.Close()
			return s, err
		}
		byOID[oid] = &t
		oids = append(oids, oid)
		order = append(order, oid)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return s, err
	}

	if len(oids) > 0 {
		if rows, err = db.Query(ctx, columnsSQL, oids); err != nil {
			return s, err
		}
		for rows.Next() {
			var oid int64
			var c Column
			if err := rows.Scan(&oid, &c.Name, &c.Type, &c.NotNull, &c.Default, &c.Comment); err != nil {
				rows.Close()
				return s, err
			}
			byOID[oid].Columns = append(byOID[oid].Columns, c)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return s, err
		}

		if rows, err = db.Query(ctx, constraintsSQL, oids, prefix, hidden); err != nil {
			return s, err
		}
		for rows.Next() {
			var oid int64
			var c Constraint
			if err := rows.Scan(&oid, &c.Name, &c.Def, &c.Comment); err != nil {
				rows.Close()
				return s, err
			}
			byOID[oid].Constraints = append(byOID[oid].Constraints, c)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return s, err
		}

		if rows, err = db.Query(ctx, indexesSQL, oids); err != nil {
			return s, err
		}
		for rows.Next() {
			var oid int64
			var x Index
			if err := rows.Scan(&oid, &x.Name, &x.Def, &x.Comment); err != nil {
				rows.Close()
				return s, err
			}
			byOID[oid].Indexes = append(byOID[oid].Indexes, x)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return s, err
		}
	}
	for _, oid := range order {
		s.Tables = append(s.Tables, *byOID[oid])
	}

	if rows, err = db.Query(ctx, enumsSQL, prefix); err != nil {
		return s, err
	}
	defer rows.Close()
	for rows.Next() {
		var name, label, comment string
		if err := rows.Scan(&name, &label, &comment); err != nil {
			return s, err
		}
		if n := len(s.Enums); n == 0 || s.Enums[n-1].Name != name {
			s.Enums = append(s.Enums, Enum{Name: name, Comment: comment})
		}
		e := &s.Enums[len(s.Enums)-1]
		e.Values = append(e.Values, label)
	}
	return s, rows.Err()
}

// Render formats s as DDL: enums first, then each table with its
// constraints inline, its indexes and its comments. Output is
// deterministic (enums and tables sorted by name).
func Render(s Schema) string {
	var b strings.Builder
	enums := append([]Enum(nil), s.Enums...)
	sort.SliceStable(enums, func(i, j int) bool { return enums[i].Name < enums[j].Name })
	tables := append([]Table(nil), s.Tables...)
	sort.SliceStable(tables, func(i, j int) bool { return tables[i].Name < tables[j].Name })

	for _, e := range enums {
		vals := make([]string, len(e.Values))
		for i, v := range e.Values {
			vals[i] = quoteLit(v)
		}
		fmt.Fprintf(&b, "CREATE TYPE %s AS ENUM (%s);\n", quoteIdent(e.Name), strings.Join(vals, ", "))
		comment(&b, "TYPE "+quoteIdent(e.Name), e.Comment)
		b.WriteByte('\n')
	}
	for _, t := range tables {
		fmt.Fprintf(&b, "CREATE TABLE %s (\n", quoteIdent(t.Name))
		lines := make([]string, 0, len(t.Columns)+len(t.Constraints))
		for _, c := range t.Columns {
			l := "    " + quoteIdent(c.Name) + " " + c.Type
			if c.NotNull {
				l += " NOT NULL"
			}
			if c.Default != "" {
				l += " DEFAULT " + c.Default
			}
			lines = append(lines, l)
		}
		for _, c := range t.Constraints {
			lines = append(lines, "    CONSTRAINT "+quoteIdent(c.Name)+" "+c.Def)
		}
		b.WriteString(strings.Join(lines, ",\n"))
		b.WriteString("\n);\n")
		for _, x := range t.Indexes {
			b.WriteString(strings.TrimRight(x.Def, "; ") + ";\n")
		}
		comment(&b, "TABLE "+quoteIdent(t.Name), t.Comment)
		for _, c := range t.Columns {
			comment(&b, "COLUMN "+quoteIdent(t.Name)+"."+quoteIdent(c.Name), c.Comment)
		}
		for _, c := range t.Constraints {
			comment(&b, "CONSTRAINT "+quoteIdent(c.Name)+" ON "+quoteIdent(t.Name), c.Comment)
		}
		for _, x := range t.Indexes {
			comment(&b, "INDEX "+quoteIdent(x.Name), x.Comment)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func comment(b *strings.Builder, target, text string) {
	if text != "" {
		fmt.Fprintf(b, "COMMENT ON %s IS %s;\n", target, quoteLit(text))
	}
}

// quoteIdent double-quotes a name only when it is not a plain lower-case
// identifier.
func quoteIdent(s string) string {
	plain := s != ""
	for i, r := range s {
		if !(r == '_' || r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9') {
			plain = false
			break
		}
	}
	if plain && !reserved[s] {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func quoteLit(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

var reserved = map[string]bool{"user": true, "order": true, "group": true, "table": true, "column": true,
	"check": true, "default": true, "primary": true, "references": true, "select": true, "from": true,
	"where": true, "constraint": true, "unique": true, "foreign": true, "limit": true, "offset": true,
	"to": true, "end": true, "all": true, "analyse": true, "analyze": true, "and": true, "any": true,
	"array": true, "as": true, "asc": true, "desc": true, "do": true, "else": true, "for": true,
	"grant": true, "having": true, "in": true, "into": true, "not": true, "null": true, "on": true,
	"or": true, "then": true, "union": true, "using": true, "when": true, "with": true}
