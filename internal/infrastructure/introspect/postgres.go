package introspect

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/netip"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/netguard"
)

// The catalog queries. $1 is the schema name. Names are cast to text and
// counts to int so every column scans plainly.
const (
	pgTables      = `select table_name::text, table_type::text from information_schema.tables where table_schema = $1 and table_type in ('BASE TABLE','VIEW') order by table_name`
	pgColumns     = `select table_name::text, column_name::text, data_type::text, udt_name::text, character_maximum_length::int, numeric_precision::int, numeric_scale::int, is_nullable::text, column_default::text, is_identity::text, is_generated::text, generation_expression::text from information_schema.columns where table_schema = $1 order by table_name, ordinal_position`
	pgConstraints = `select tc.table_name::text, tc.constraint_name::text, tc.constraint_type::text, kcu.column_name::text from information_schema.table_constraints tc join information_schema.key_column_usage kcu on kcu.constraint_name = tc.constraint_name and kcu.constraint_schema = tc.constraint_schema and kcu.table_name = tc.table_name where tc.table_schema = $1 and tc.constraint_type in ('PRIMARY KEY','UNIQUE') order by tc.table_name, tc.constraint_name, kcu.ordinal_position`
	pgFKs         = `select con.conname::text, cl.relname::text, att.attname::text, fcl.relname::text, fatt.attname::text, con.confdeltype::text, con.confupdtype::text from pg_constraint con join pg_class cl on cl.oid = con.conrelid join pg_namespace ns on ns.oid = cl.relnamespace cross join lateral unnest(con.conkey, con.confkey) with ordinality as u(a, fa, ord) join pg_attribute att on att.attrelid = con.conrelid and att.attnum = u.a join pg_class fcl on fcl.oid = con.confrelid join pg_attribute fatt on fatt.attrelid = con.confrelid and fatt.attnum = u.fa where con.contype = 'f' and ns.nspname = $1 order by cl.relname, con.conname, u.ord`
	pgIndexes     = `select tablename::text, indexname::text, indexdef::text from pg_indexes where schemaname = $1 order by tablename, indexname`
	pgEnums       = `select t.typname::text, array_agg(e.enumlabel::text order by e.enumsortorder) from pg_type t join pg_enum e on e.enumtypid = t.oid join pg_namespace n on n.oid = t.typnamespace where n.nspname = $1 group by t.typname order by t.typname`
)

type pgColumn struct {
	table, name, dataType, udt string
	maxLen, precision, scale   *int
	nullable                   string
	def                        *string
	identity, generated        string
	genExpr                    *string
}

type pgConstraint struct{ table, name, kind, column string }
type pgFK struct{ name, table, column, refTable, refColumn, onDelete, onUpdate string }
type pgIndex struct{ table, name, def string }

type pgRows struct {
	tables      [][2]string
	columns     []pgColumn
	constraints []pgConstraint
	fks         []pgFK
	indexes     []pgIndex
	enums       []diagram.SchemaEnum
}

func (s *Service) postgres(ctx context.Context, t diagram.DBTarget) (diagram.Introspected, error) {
	var out diagram.Introspected
	err := s.withPostgres(ctx, t, func(tx pgx.Tx) error {
		schema := t.Schema
		if schema == "" {
			schema = "public"
		}
		var rows pgRows
		if err := readPG(ctx, tx, schema, &rows); err != nil {
			return err
		}
		out = schemaFromPostgres(rows)
		return nil
	})
	return out, err
}

// probePostgres opens the session as introspection does and reports the
// read-only and statement timeout settings the server applied (tests).
func (s *Service) probePostgres(ctx context.Context, t diagram.DBTarget, report func(readOnly, timeout string)) error {
	return s.withPostgres(ctx, t, func(tx pgx.Tx) error {
		var ro, timeout string
		if err := tx.QueryRow(ctx, `show transaction_read_only`).Scan(&ro); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `show statement_timeout`).Scan(&timeout); err != nil {
			return err
		}
		report(ro, timeout)
		return nil
	})
}

// withPostgres connects, opens a read-only transaction with a statement
// timeout and runs fn in it; the transaction is always rolled back.
func (s *Service) withPostgres(ctx context.Context, t diagram.DBTarget, fn func(pgx.Tx) error) error {
	cc, err := pgx.ParseConfig("")
	if err != nil {
		return err
	}
	cfg := &cc.Config
	// built field by field: no connection string is assembled from user input, and the
	// password is always the one given (never one found in the environment or a .pgpass)
	cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Database = t.Host, uint16(t.Port), t.User, t.Password, t.Database
	cfg.ConnectTimeout = ConnectTimeout
	cfg.TLSConfig, cfg.Fallbacks = nil, nil
	if t.SSL {
		cfg.TLSConfig = &tls.Config{InsecureSkipVerify: true, ServerName: t.Host} // like the Node server: encrypted, not verified
	}
	cfg.RuntimeParams = map[string]string{"application_name": "sdd-introspect"}
	d := s.dialer()
	// resolve once, check every answer, dial the checked address
	cfg.LookupFunc = func(ctx context.Context, host string) ([]string, error) {
		ips, err := netguard.Resolve(ctx, host, d.AllowPrivate, d.Lookup)
		if err != nil {
			return nil, err
		}
		return addrStrings(ips), nil
	}
	cfg.DialFunc = d.DialContext

	cc.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	conn, err := pgx.ConnectConfig(ctx, cc)
	if err != nil {
		return err
	}
	defer conn.Close(context.WithoutCancel(ctx))
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck // read only: nothing to keep
	for _, q := range []string{
		fmt.Sprintf("SET LOCAL statement_timeout = %d", StatementTimeout.Milliseconds()),
		"SET LOCAL default_transaction_read_only = on",
	} {
		if _, err := tx.Exec(ctx, q); err != nil {
			return err
		}
	}
	return fn(tx)
}

func addrStrings(ips []netip.Addr) []string {
	out := make([]string, len(ips))
	for i, ip := range ips {
		out[i] = ip.String()
	}
	return out
}

func readPG(ctx context.Context, tx pgx.Tx, schema string, r *pgRows) error {
	scan := func(sql string, each func(pgx.Rows) error) error {
		rows, err := tx.Query(ctx, sql, schema)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			if err := each(rows); err != nil {
				return err
			}
		}
		return rows.Err()
	}
	if err := scan(pgTables, func(rs pgx.Rows) error {
		var n, k string
		err := rs.Scan(&n, &k)
		r.tables = append(r.tables, [2]string{n, k})
		return err
	}); err != nil {
		return err
	}
	if err := scan(pgColumns, func(rs pgx.Rows) error {
		var c pgColumn
		err := rs.Scan(&c.table, &c.name, &c.dataType, &c.udt, &c.maxLen, &c.precision, &c.scale, &c.nullable, &c.def, &c.identity, &c.generated, &c.genExpr)
		r.columns = append(r.columns, c)
		return err
	}); err != nil {
		return err
	}
	if err := scan(pgConstraints, func(rs pgx.Rows) error {
		var c pgConstraint
		err := rs.Scan(&c.table, &c.name, &c.kind, &c.column)
		r.constraints = append(r.constraints, c)
		return err
	}); err != nil {
		return err
	}
	if err := scan(pgFKs, func(rs pgx.Rows) error {
		var f pgFK
		err := rs.Scan(&f.name, &f.table, &f.column, &f.refTable, &f.refColumn, &f.onDelete, &f.onUpdate)
		r.fks = append(r.fks, f)
		return err
	}); err != nil {
		return err
	}
	if err := scan(pgIndexes, func(rs pgx.Rows) error {
		var i pgIndex
		err := rs.Scan(&i.table, &i.name, &i.def)
		r.indexes = append(r.indexes, i)
		return err
	}); err != nil {
		return err
	}
	return scan(pgEnums, func(rs pgx.Rows) error {
		var e diagram.SchemaEnum
		err := rs.Scan(&e.Name, &e.Values)
		r.enums = append(r.enums, e)
		return err
	})
}

var pgAction = map[string]string{"r": "RESTRICT", "c": "CASCADE", "n": "SET NULL", "d": "SET DEFAULT"} // "a" (no action) is the default

func pgType(c pgColumn) string {
	switch c.dataType {
	case "character varying":
		if c.maxLen != nil && *c.maxLen > 0 {
			return fmt.Sprintf("varchar(%d)", *c.maxLen)
		}
		return "varchar"
	case "character":
		n := 1
		if c.maxLen != nil {
			n = *c.maxLen
		}
		return fmt.Sprintf("char(%d)", n)
	case "numeric":
		if c.precision != nil && c.scale != nil {
			return fmt.Sprintf("numeric(%d,%d)", *c.precision, *c.scale)
		}
		return "numeric"
	case "USER-DEFINED":
		return c.udt
	case "ARRAY":
		return strings.TrimPrefix(c.udt, "_") + "[]"
	case "timestamp with time zone":
		return "timestamptz"
	case "timestamp without time zone":
		return "timestamp"
	case "time with time zone":
		return "timetz"
	case "time without time zone":
		return "time"
	}
	return c.dataType
}

var (
	pgIndexDef = regexp.MustCompile(`USING \w+ \((.*)\)(?: WHERE .*)?$`)
	pgOrdering = regexp.MustCompile(`(?i)\s+(ASC|DESC).*$`)
	nextval    = regexp.MustCompile(`(?i)^nextval\(`)
)

func schemaFromPostgres(rows pgRows) diagram.Introspected {
	var warnings []string
	keyNames := map[string]bool{}
	for _, c := range rows.constraints {
		keyNames[c.name] = true
	}
	var tables []diagram.SchemaTable
	for _, t := range rows.tables {
		name := t[0]
		var pk []string
		uniques := map[string][]string{}
		var uniqueOrder []string
		for _, c := range rows.constraints {
			if c.table != name {
				continue
			}
			switch c.kind {
			case "PRIMARY KEY":
				pk = append(pk, c.column)
			case "UNIQUE":
				if _, seen := uniques[c.name]; !seen {
					uniqueOrder = append(uniqueOrder, c.name)
				}
				uniques[c.name] = append(uniques[c.name], c.column)
			}
		}
		var fks []pgFK
		fkSize := map[string]int{}
		for _, f := range rows.fks {
			if f.table == name {
				fks = append(fks, f)
				fkSize[f.name]++
			}
		}
		var indexes []diagram.SchemaIndex
		for _, u := range uniqueOrder {
			if len(uniques[u]) > 1 {
				indexes = append(indexes, diagram.SchemaIndex{Name: u, Columns: uniques[u], Unique: true})
			}
		}
		for _, ix := range rows.indexes {
			if ix.table != name || keyNames[ix.name] {
				continue
			}
			m := pgIndexDef.FindStringSubmatch(ix.def)
			if m == nil || strings.ContainsAny(m[1], "()") {
				warnings = append(warnings, fmt.Sprintf("%s: index %s uses an expression or condition and was skipped", name, ix.name))
				continue
			}
			var cols []string
			for _, c := range strings.Split(m[1], ",") {
				c = pgOrdering.ReplaceAllString(strings.TrimSpace(c), "") // "col" DESC -> "col"
				cols = append(cols, strings.TrimSuffix(strings.TrimPrefix(c, `"`), `"`))
			}
			indexes = append(indexes, diagram.SchemaIndex{Name: ix.name, Columns: cols, Unique: strings.HasPrefix(ix.def, "CREATE UNIQUE")})
		}
		var columns []diagram.SchemaColumn
		for _, c := range rows.columns {
			if c.table != name {
				continue
			}
			isPK := contains(pk, c.name)
			single := false
			for _, u := range uniques {
				single = single || len(u) == 1 && u[0] == c.name
			}
			col := diagram.SchemaColumn{Name: c.name, Type: pgType(c), PK: isPK, Unique: single && !isPK, Nullable: c.nullable == "YES" && !isPK}
			switch {
			case c.identity == "YES":
				col.Generated = "identity"
			case c.generated == "ALWAYS" && c.genExpr != nil && *c.genExpr != "":
				col.Generated = "as (" + *c.genExpr + ")"
			case c.def != nil && nextval.MatchString(*c.def):
				col.Generated = "autoincrement"
			case c.def != nil:
				col.Default = *c.def
			}
			for _, f := range fks {
				if f.column != c.name {
					continue
				}
				fk := &diagram.SchemaFK{Table: f.refTable, Column: f.refColumn, Name: f.name, OnDelete: pgAction[f.onDelete], OnUpdate: pgAction[f.onUpdate]}
				if fkSize[f.name] > 1 {
					fk.Group = f.name
				}
				col.FK = fk
				break
			}
			columns = append(columns, col)
		}
		kind := "table"
		if t[1] == "VIEW" {
			kind = "view"
		}
		tables = append(tables, diagram.SchemaTable{Name: name, Kind: kind, Columns: nonNil(columns), Indexes: nonNil(indexes), Checks: []diagram.SchemaCheck{}})
	}
	return diagram.Introspected{Schema: diagram.Schema{Tables: nonNil(tables), Enums: nonNil(rows.enums)}, Warnings: nonNil(warnings)}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
