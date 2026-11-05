package introspect

import (
	"context"
	"crypto/tls"
	"database/sql"
	"net"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-sql-driver/mysql"

	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
)

const (
	myTables  = `select table_name, table_type from information_schema.tables where table_schema = ? and table_type in ('BASE TABLE','VIEW') order by table_name`
	myColumns = `select table_name, column_name, column_type, is_nullable, column_default, extra, column_key, generation_expression, column_comment from information_schema.columns where table_schema = ? order by table_name, ordinal_position`
	myFKs     = `select k.table_name, k.column_name, k.constraint_name, k.referenced_table_name, k.referenced_column_name, r.delete_rule, r.update_rule from information_schema.key_column_usage k join information_schema.referential_constraints r on r.constraint_name = k.constraint_name and r.constraint_schema = k.constraint_schema and r.table_name = k.table_name where k.table_schema = ? and k.referenced_table_name is not null order by k.table_name, k.constraint_name, k.ordinal_position`
	myStats   = `select table_name, index_name, non_unique, column_name from information_schema.statistics where table_schema = ? order by table_name, index_name, seq_in_index`
)

type myColumn struct {
	table, name, typ, nullable string
	def, genExpr               sql.NullString
	extra, key, comment        string
}

type myFK struct{ table, column, name, refTable, refColumn, onDelete, onUpdate string }
type myStat struct {
	table, index, column string
	nonUnique            int
}

func (s *Service) mysql(ctx context.Context, t diagram.DBTarget) (diagram.Introspected, error) {
	cfg := mysql.NewConfig()
	cfg.User, cfg.Passwd, cfg.DBName = t.User, t.Password, t.Database
	cfg.Net, cfg.Addr = "tcp", net.JoinHostPort(strings.Trim(t.Host, "[]"), strconv.Itoa(t.Port))
	cfg.Timeout, cfg.ReadTimeout = ConnectTimeout, StatementTimeout
	cfg.AllowAllFiles, cfg.MultiStatements, cfg.AllowCleartextPasswords = false, false, false
	if t.SSL {
		cfg.TLS = &tls.Config{InsecureSkipVerify: true, ServerName: t.Host} // encrypted, not verified, like the Node server
	}
	d := s.dialer()
	cfg.DialFunc = d.DialContext // resolves, checks every answer, dials the checked address
	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		return diagram.Introspected{}, err
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(1)
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		return diagram.Introspected{}, err
	}
	defer conn.Close()
	for _, q := range []string{"SET SESSION TRANSACTION READ ONLY", "START TRANSACTION READ ONLY"} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			return diagram.Introspected{}, err
		}
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK") //nolint:errcheck // read only: nothing to keep
	schema := t.Schema
	if schema == "" {
		schema = t.Database
	}
	var tables [][2]string
	var cols []myColumn
	var fks []myFK
	var stats []myStat
	read := func(q string, each func(*sql.Rows) error) error {
		rows, err := conn.QueryContext(ctx, q, schema)
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
	if err := read(myTables, func(r *sql.Rows) error {
		var n, k string
		err := r.Scan(&n, &k)
		tables = append(tables, [2]string{n, k})
		return err
	}); err != nil {
		return diagram.Introspected{}, err
	}
	if err := read(myColumns, func(r *sql.Rows) error {
		var c myColumn
		err := r.Scan(&c.table, &c.name, &c.typ, &c.nullable, &c.def, &c.extra, &c.key, &c.genExpr, &c.comment)
		cols = append(cols, c)
		return err
	}); err != nil {
		return diagram.Introspected{}, err
	}
	if err := read(myFKs, func(r *sql.Rows) error {
		var f myFK
		err := r.Scan(&f.table, &f.column, &f.name, &f.refTable, &f.refColumn, &f.onDelete, &f.onUpdate)
		fks = append(fks, f)
		return err
	}); err != nil {
		return diagram.Introspected{}, err
	}
	if err := read(myStats, func(r *sql.Rows) error {
		var st myStat
		err := r.Scan(&st.table, &st.index, &st.nonUnique, &st.column)
		stats = append(stats, st)
		return err
	}); err != nil {
		return diagram.Introspected{}, err
	}
	return schemaFromMySQL(tables, cols, fks, stats), nil
}

var (
	autoInc     = regexp.MustCompile(`(?i)auto_increment`)
	generated   = regexp.MustCompile(`GENERATED`)
	defGen      = regexp.MustCompile(`(?i)DEFAULT_GENERATED`)
	plainNumber = regexp.MustCompile(`^-?\d+(\.\d+)?$`)
	currentTS   = regexp.MustCompile(`(?i)^current_timestamp`)
	nullWord    = regexp.MustCompile(`(?i)^null$`)
)

func schemaFromMySQL(tables [][2]string, cols []myColumn, fks []myFK, stats []myStat) diagram.Introspected {
	var out []diagram.SchemaTable
	for _, t := range tables {
		name := t[0]
		var tfks []myFK
		fkSize := map[string]int{}
		for _, f := range fks {
			if f.table == name {
				tfks = append(tfks, f)
				fkSize[f.name]++
			}
		}
		type idx struct {
			unique bool
			cols   []string
		}
		byName := map[string]*idx{}
		var order []string
		for _, s := range stats {
			if s.table != name || s.index == "PRIMARY" {
				continue
			}
			e := byName[s.index]
			if e == nil {
				e = &idx{unique: s.nonUnique == 0}
				byName[s.index] = e
				order = append(order, s.index)
			}
			e.cols = append(e.cols, s.column)
		}
		uniqueSingle := map[string]bool{}
		var indexes []diagram.SchemaIndex
		for _, n := range order {
			e := byName[n]
			if e.unique && len(e.cols) == 1 {
				uniqueSingle[e.cols[0]] = true
				continue
			}
			indexes = append(indexes, diagram.SchemaIndex{Name: n, Columns: e.cols, Unique: e.unique})
		}
		var columns []diagram.SchemaColumn
		for _, c := range cols {
			if c.table != name {
				continue
			}
			pk := c.key == "PRI"
			col := diagram.SchemaColumn{Name: c.name, Type: c.typ, PK: pk, Unique: !pk && (c.key == "UNI" || uniqueSingle[c.name]),
				Nullable: c.nullable == "YES" && !pk, Comment: c.comment}
			switch {
			case autoInc.MatchString(c.extra):
				col.Generated = "autoincrement"
			case generated.MatchString(c.extra) && c.genExpr.String != "":
				col.Generated = "as (" + c.genExpr.String + ")"
			case c.def.Valid:
				d := c.def.String
				if defGen.MatchString(c.extra) || plainNumber.MatchString(d) || currentTS.MatchString(d) || nullWord.MatchString(d) {
					col.Default = d
				} else {
					col.Default = "'" + strings.ReplaceAll(d, "'", "''") + "'"
				}
			}
			for _, f := range tfks {
				if f.column != c.name {
					continue
				}
				fk := &diagram.SchemaFK{Table: f.refTable, Column: f.refColumn, Name: f.name}
				if f.onDelete != "" && f.onDelete != "NO ACTION" {
					fk.OnDelete = f.onDelete
				}
				if f.onUpdate != "" && f.onUpdate != "NO ACTION" {
					fk.OnUpdate = f.onUpdate
				}
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
		out = append(out, diagram.SchemaTable{Name: name, Kind: kind, Columns: nonNil(columns), Indexes: nonNil(indexes), Checks: []diagram.SchemaCheck{}})
	}
	return diagram.Introspected{Schema: diagram.Schema{Tables: nonNil(out), Enums: []diagram.SchemaEnum{}}, Warnings: []string{}}
}
