package introspect

import (
	"context"
	"database/sql"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
)

func lookupTo(ips ...string) func(context.Context, string) ([]netip.Addr, error) {
	return func(context.Context, string) ([]netip.Addr, error) {
		var out []netip.Addr
		for _, s := range ips {
			out = append(out, netip.MustParseAddr(s))
		}
		return out, nil
	}
}

func TestCheckTargetBlocksInternalHosts(t *testing.T) {
	ctx := context.Background()
	s := &Service{Lookup: lookupTo("8.8.8.8")}
	blocked := func(err error) bool {
		var b *diagram.TargetBlockedError
		return errors.As(err, &b)
	}
	for _, host := range []string{"127.0.0.1", "localhost", "LOCALHOST", "10.0.0.5", "192.168.1.1", "172.16.0.1", "169.254.169.254", "[::1]", "::1", "fd00::1",
		"db.internal", "db.local", "foo.localhost", "100.64.0.1", "0.0.0.0", "::ffff:10.0.0.1"} {
		if err := s.CheckTarget(ctx, host, 5432); !blocked(err) {
			t.Errorf("%s: %v", host, err)
		}
	}
	// names that resolve to an internal address, even one of several answers
	for _, ips := range [][]string{{"10.0.0.1"}, {"8.8.8.8", "127.0.0.1"}, {"169.254.169.254"}} {
		if err := (&Service{Lookup: lookupTo(ips...)}).CheckTarget(ctx, "rebind.example.com", 5432); !blocked(err) || !strings.Contains(err.Error(), "resolves to a private address") {
			t.Errorf("%v: %v", ips, err)
		}
	}
	if err := (&Service{Lookup: func(context.Context, string) ([]netip.Addr, error) { return nil, errors.New("nx") }}).CheckTarget(ctx, "gone.example.com", 5432); !blocked(err) {
		t.Errorf("unresolvable: %v", err)
	}
	for _, c := range []struct {
		host string
		port int
	}{{"", 5432}, {"a b", 5432}, {"user@host", 5432}, {"a/b", 5432}, {`a\b`, 5432}, {"db.example.com", 0}, {"db.example.com", 70000}} {
		if err := s.CheckTarget(ctx, c.host, c.port); !blocked(err) {
			t.Errorf("%q:%d: %v", c.host, c.port, err)
		}
	}
	if err := s.CheckTarget(ctx, "db.example.com", 5432); err != nil {
		t.Errorf("public host: %v", err)
	}
	// the administrator may allow private targets
	if err := (&Service{AllowPrivate: true}).CheckTarget(ctx, "127.0.0.1", 5432); err != nil {
		t.Errorf("allowed: %v", err)
	}
	// and Introspect checks before it connects
	if _, err := s.Introspect(ctx, diagram.DBTarget{Dialect: "postgres", Host: "127.0.0.1", User: "u", Database: "d"}); !blocked(err) {
		t.Errorf("Introspect to loopback: %v", err)
	}
	if _, err := s.Introspect(ctx, diagram.DBTarget{Dialect: "mysql", Host: "169.254.169.254", User: "u", Database: "d"}); !blocked(err) {
		t.Errorf("Introspect to the metadata address: %v", err)
	}
}

// A name that answers with a public address at the check and a private one at
// the dial is refused by the dial itself (no second chance for rebinding).
func TestDialIsPinned(t *testing.T) {
	flip := 0
	s := &Service{Lookup: func(context.Context, string) ([]netip.Addr, error) {
		flip++
		if flip == 1 {
			return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}}
	if err := s.CheckTarget(context.Background(), "rebind.example.com", 5432); err != nil {
		t.Fatal(err)
	}
	if _, err := s.dialer().DialContext(context.Background(), "tcp", "rebind.example.com:5432"); err == nil {
		t.Fatal("the dial reached a private address")
	}
}

func TestRedact(t *testing.T) {
	for in, want := range map[string]string{
		"failed to connect to postgres://bob:hunter2hunter2@db.example.com:5432/app: refused": "failed to connect to postgres://***@db.example.com:5432/app: refused",
		"dial error password=hunter2hunter2 failed":                                           "dial error password=*** failed",
		"Access denied for user 'bob'@'1.2.3.4' (using password: YES)":                        "Access denied for user '***'@'1.2.3.4' (using password: ***",
		"the secret sup3rs3cretpw leaked":                                                     "the secret *** leaked",
	} {
		if got := Redact(in, "sup3rs3cretpw", "bob"); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
	if Redact("ab in abc", "ab") != "ab in abc" {
		t.Error("secrets shorter than 3 characters are left alone")
	}
}

func TestSchemaFromPostgres(t *testing.T) {
	i := func(n int) *int { return &n }
	s := func(v string) *string { return &v }
	rows := pgRows{
		tables: [][2]string{{"orders", "BASE TABLE"}, {"users", "BASE TABLE"}, {"v_users", "VIEW"}},
		columns: []pgColumn{
			{table: "orders", name: "id", dataType: "bigint", nullable: "NO", identity: "YES", generated: "NEVER"},
			{table: "orders", name: "user_id", dataType: "bigint", nullable: "NO", identity: "NO", generated: "NEVER"},
			{table: "orders", name: "code", dataType: "character varying", maxLen: i(20), nullable: "YES", def: s("'x'::character varying"), identity: "NO", generated: "NEVER"},
			{table: "orders", name: "total", dataType: "numeric", precision: i(10), scale: i(2), nullable: "NO", identity: "NO", generated: "NEVER"},
			{table: "orders", name: "at", dataType: "timestamp with time zone", nullable: "NO", identity: "NO", generated: "NEVER"},
			{table: "orders", name: "tags", dataType: "ARRAY", udt: "_text", nullable: "YES", identity: "NO", generated: "NEVER"},
			{table: "orders", name: "mood", dataType: "USER-DEFINED", udt: "mood", nullable: "YES", identity: "NO", generated: "NEVER"},
			{table: "orders", name: "dbl", dataType: "integer", nullable: "YES", identity: "NO", generated: "ALWAYS", genExpr: s("(total * 2)")},
			{table: "users", name: "id", dataType: "integer", nullable: "NO", def: s("nextval('users_id_seq'::regclass)"), identity: "NO", generated: "NEVER"},
			{table: "users", name: "email", dataType: "text", nullable: "NO", identity: "NO", generated: "NEVER"},
			{table: "users", name: "a", dataType: "text", nullable: "YES", identity: "NO", generated: "NEVER"},
			{table: "users", name: "b", dataType: "text", nullable: "YES", identity: "NO", generated: "NEVER"},
			{table: "v_users", name: "email", dataType: "text", nullable: "YES", identity: "NO", generated: "NEVER"},
		},
		constraints: []pgConstraint{
			{"orders", "orders_pkey", "PRIMARY KEY", "id"}, {"users", "users_pkey", "PRIMARY KEY", "id"},
			{"users", "users_email_key", "UNIQUE", "email"}, {"users", "users_a_b_key", "UNIQUE", "a"}, {"users", "users_a_b_key", "UNIQUE", "b"},
		},
		fks: []pgFK{{"orders_user_fk", "orders", "user_id", "users", "id", "c", "a"}},
		indexes: []pgIndex{
			{"orders", "orders_pkey", "CREATE UNIQUE INDEX orders_pkey ON public.orders USING btree (id)"},
			{"orders", "orders_user_idx", `CREATE INDEX orders_user_idx ON public.orders USING btree (user_id, "at" DESC)`},
			{"orders", "orders_lower_idx", "CREATE INDEX orders_lower_idx ON public.orders USING btree (lower(code))"},
			{"orders", "orders_part_idx", "CREATE UNIQUE INDEX orders_part_idx ON public.orders USING btree (code) WHERE (total > 0)"},
			{"users", "users_email_key", "CREATE UNIQUE INDEX users_email_key ON public.users USING btree (email)"},
		},
		enums: []diagram.SchemaEnum{{Name: "mood", Values: []string{"sad", "ok"}}},
	}
	got := schemaFromPostgres(rows)
	byName := map[string]diagram.SchemaTable{}
	for _, tb := range got.Schema.Tables {
		byName[tb.Name] = tb
	}
	col := func(tb, c string) diagram.SchemaColumn {
		for _, x := range byName[tb].Columns {
			if x.Name == c {
				return x
			}
		}
		t.Fatalf("no column %s.%s", tb, c)
		return diagram.SchemaColumn{}
	}
	if byName["v_users"].Kind != "view" || byName["orders"].Kind != "table" || len(got.Schema.Tables) != 3 {
		t.Errorf("tables = %+v", got.Schema.Tables)
	}
	if c := col("orders", "id"); !c.PK || c.Nullable || c.Generated != "identity" {
		t.Errorf("orders.id = %+v", c)
	}
	if c := col("orders", "user_id"); c.FK == nil || *c.FK != (diagram.SchemaFK{Table: "users", Column: "id", Name: "orders_user_fk", OnDelete: "CASCADE"}) {
		t.Errorf("orders.user_id = %+v", c.FK)
	}
	for c, want := range map[string]string{"code": "varchar(20)", "total": "numeric(10,2)", "at": "timestamptz", "tags": "text[]", "mood": "mood"} {
		if got := col("orders", c).Type; got != want {
			t.Errorf("orders.%s type = %q, want %q", c, got, want)
		}
	}
	if c := col("orders", "code"); c.Default != "'x'::character varying" || !c.Nullable {
		t.Errorf("orders.code = %+v", c)
	}
	if c := col("orders", "dbl"); c.Generated != "as ((total * 2))" {
		t.Errorf("orders.dbl = %+v", c)
	}
	if c := col("users", "id"); c.Generated != "autoincrement" || c.Default != "" || !c.PK {
		t.Errorf("users.id = %+v", c)
	}
	if c := col("users", "email"); !c.Unique || c.PK {
		t.Errorf("users.email = %+v", c)
	}
	if c := col("users", "a"); c.Unique {
		t.Errorf("a column of a composite unique is not unique on its own: %+v", c)
	}
	users := byName["users"].Indexes
	if len(users) != 1 || users[0].Name != "users_a_b_key" || !users[0].Unique || strings.Join(users[0].Columns, ",") != "a,b" {
		t.Errorf("users indexes = %+v", users)
	}
	orders := byName["orders"].Indexes
	if len(orders) != 1 || orders[0].Name != "orders_user_idx" || strings.Join(orders[0].Columns, ",") != "user_id,at" || orders[0].Unique {
		t.Errorf("orders indexes = %+v", orders)
	}
	if len(got.Warnings) != 2 || !strings.Contains(got.Warnings[0], "orders_lower_idx") || !strings.Contains(got.Warnings[1], "orders_part_idx") {
		t.Errorf("warnings = %v", got.Warnings)
	}
	if len(got.Schema.Enums) != 1 || got.Schema.Enums[0].Name != "mood" {
		t.Errorf("enums = %+v", got.Schema.Enums)
	}
}

func TestSchemaFromMySQL(t *testing.T) {
	ns := func(v string) sql.NullString { return sql.NullString{String: v, Valid: true} }
	tables := [][2]string{{"orders", "BASE TABLE"}, {"users", "BASE TABLE"}}
	cols := []myColumn{
		{table: "orders", name: "id", typ: "bigint unsigned", nullable: "NO", key: "PRI", extra: "auto_increment"},
		{table: "orders", name: "user_id", typ: "bigint unsigned", nullable: "NO", key: "MUL"},
		{table: "orders", name: "status", typ: "varchar(10)", nullable: "NO", def: ns("it's"), extra: ""},
		{table: "orders", name: "qty", typ: "int", nullable: "NO", def: ns("1")},
		{table: "orders", name: "made", typ: "timestamp", nullable: "NO", def: ns("CURRENT_TIMESTAMP"), extra: "DEFAULT_GENERATED"},
		{table: "orders", name: "double", typ: "int", nullable: "YES", extra: "VIRTUAL GENERATED", genExpr: ns("`qty` * 2")},
		{table: "users", name: "id", typ: "int", nullable: "NO", key: "PRI", extra: "auto_increment"},
		{table: "users", name: "email", typ: "varchar(255)", nullable: "NO", key: "UNI", comment: "login"},
	}
	fks := []myFK{{"orders", "user_id", "fk_orders_user", "users", "id", "CASCADE", "NO ACTION"}}
	stats := []myStat{{"orders", "PRIMARY", "id", 0}, {"orders", "ix_user", "user_id", 1}, {"users", "uq_email", "email", 0}}
	got := schemaFromMySQL(tables, cols, fks, stats).Schema.Tables
	o, u := got[0], got[1]
	if c := o.Columns[0]; !c.PK || c.Generated != "autoincrement" || c.Nullable {
		t.Errorf("orders.id = %+v", c)
	}
	if c := o.Columns[1]; c.FK == nil || c.FK.OnDelete != "CASCADE" || c.FK.OnUpdate != "" || c.FK.Name != "fk_orders_user" {
		t.Errorf("orders.user_id = %+v", c.FK)
	}
	if o.Columns[2].Default != "'it''s'" || o.Columns[3].Default != "1" || o.Columns[4].Default != "CURRENT_TIMESTAMP" || o.Columns[5].Generated != "as (`qty` * 2)" {
		t.Errorf("defaults = %q %q %q %q", o.Columns[2].Default, o.Columns[3].Default, o.Columns[4].Default, o.Columns[5].Generated)
	}
	if len(o.Indexes) != 1 || o.Indexes[0].Name != "ix_user" || o.Indexes[0].Unique {
		t.Errorf("orders indexes = %+v", o.Indexes)
	}
	if c := u.Columns[1]; !c.Unique || c.Comment != "login" || len(u.Indexes) != 0 {
		t.Errorf("users.email = %+v, indexes %+v", c, u.Indexes)
	}
}

// writeSQLite builds a SQLite database file with the driver and returns its bytes.
func writeSQLite(t *testing.T, stmts ...string) []byte {
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
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func leftovers(t *testing.T) []string {
	m, _ := filepath.Glob(filepath.Join(os.TempDir(), "sdd-introspect-*.db"))
	return m
}

// The Node server's route test: two tables, a foreign key, an index.
func TestIntrospectSQLiteLikeTheNodeServer(t *testing.T) {
	before := len(leftovers(t))
	file := writeSQLite(t,
		`CREATE TABLE a (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE b (id INTEGER PRIMARY KEY, a_id INTEGER REFERENCES a(id))`,
		`CREATE INDEX b_a ON b (a_id)`)
	res, err := New(false).IntrospectSQLite(context.Background(), file)
	if err != nil {
		t.Fatal(err)
	}
	c := res.Schema.Counts()
	if c.Tables != 2 || c.Columns != 4 || c.Views != 0 {
		t.Errorf("counts = %+v", c)
	}
	b := res.Schema.Tables[1]
	if b.Name != "b" || b.Columns[1].FK == nil || *b.Columns[1].FK != (diagram.SchemaFK{Table: "a", Column: "id"}) {
		t.Errorf("b = %+v", b)
	}
	if len(b.Indexes) != 1 || b.Indexes[0].Name != "b_a" || strings.Join(b.Indexes[0].Columns, ",") != "a_id" || b.Indexes[0].Unique {
		t.Errorf("b indexes = %+v", b.Indexes)
	}
	if got := len(leftovers(t)); got != before {
		t.Errorf("the temp file was not removed (%d left)", got-before)
	}
}

func TestIntrospectSQLiteDetails(t *testing.T) {
	file := writeSQLite(t,
		`CREATE TABLE "we ird""name" (id INTEGER PRIMARY KEY AUTOINCREMENT, code TEXT UNIQUE, note, qty INT NOT NULL DEFAULT 5)`,
		`CREATE TABLE pair (a INTEGER, b INTEGER, PRIMARY KEY (a, b))`,
		`CREATE TABLE child (x INTEGER, y INTEGER, FOREIGN KEY (x, y) REFERENCES pair (a, b) ON DELETE CASCADE ON UPDATE SET NULL, UNIQUE (x, y))`,
		`CREATE INDEX child_expr ON child (x + y)`,
		`CREATE UNIQUE INDEX child_uq ON child (y, x)`,
		`CREATE VIEW v AS SELECT x FROM child`)
	res, err := New(false).IntrospectSQLite(context.Background(), file)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]diagram.SchemaTable{}
	for _, tb := range res.Schema.Tables {
		by[tb.Name] = tb
	}
	w := by[`we ird"name`]
	if len(w.Columns) != 4 || !w.Columns[0].PK || w.Columns[0].Generated != "autoincrement" || w.Columns[0].Nullable {
		t.Errorf("weird table = %+v", w)
	}
	if w.Columns[1].Name != "code" || !w.Columns[1].Unique || w.Columns[2].Type != "TEXT" || w.Columns[3].Default != "5" || w.Columns[3].Nullable {
		t.Errorf("weird columns = %+v", w.Columns)
	}
	child := by["child"]
	fk := child.Columns[0].FK
	if fk == nil || fk.Table != "pair" || fk.Column != "a" || fk.OnDelete != "CASCADE" || fk.OnUpdate != "SET NULL" || fk.Group == "" || child.Columns[1].FK.Group != fk.Group {
		t.Errorf("composite fk = %+v / %+v", fk, child.Columns[1].FK)
	}
	if by["pair"].Columns[0].Generated != "" || !by["pair"].Columns[0].PK || !by["pair"].Columns[1].PK {
		t.Errorf("composite pk = %+v", by["pair"].Columns)
	}
	if v := by["v"]; v.Kind != "view" || len(v.Columns) != 1 || len(v.Indexes) != 0 {
		t.Errorf("view = %+v", v)
	}
	names := []string{}
	for _, ix := range child.Indexes {
		names = append(names, ix.Name)
	}
	if len(child.Indexes) != 2 || !child.Indexes[0].Unique || !child.Indexes[1].Unique {
		t.Errorf("child indexes = %v", child.Indexes)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "child_expr") {
		t.Errorf("warnings = %v", res.Warnings)
	}
	if res.Schema.Enums == nil || res.Warnings == nil {
		t.Error("empty lists must be [], not null")
	}
}

func TestIntrospectSQLiteRejectsOtherFiles(t *testing.T) {
	for name, b := range map[string][]byte{"garbage": make([]byte, 500), "short": []byte("SQLite format 3\x00"), "text": []byte(strings.Repeat("hello ", 50)), "empty": nil} {
		if _, err := New(false).IntrospectSQLite(context.Background(), b); !errors.As(err, new(diagram.NotSQLiteError)) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// a right header over a broken body is a failed read, with no temp path in the message
	bad := append([]byte("SQLite format 3\x00"), make([]byte, 300)...)
	_, err := New(false).IntrospectSQLite(context.Background(), bad)
	var failed *diagram.IntrospectFailedError
	if !errors.As(err, &failed) {
		t.Fatalf("broken body: %v", err)
	}
	if strings.Contains(failed.Msg, os.TempDir()) {
		t.Errorf("the message shows a temp path: %s", failed.Msg)
	}
}

// The file is opened read only: a hostile database with a trigger or a view
// that writes cannot change anything, and the introspection never writes.
func TestIntrospectSQLiteIsReadOnly(t *testing.T) {
	file := writeSQLite(t, `CREATE TABLE t (id INTEGER PRIMARY KEY)`, `CREATE VIEW v AS SELECT id FROM t`)
	res, err := New(false).IntrospectSQLite(context.Background(), file)
	if err != nil || len(res.Schema.Tables) != 2 {
		t.Fatalf("%v %v", res, err)
	}
	path := filepath.Join(t.TempDir(), "ro.db")
	if err := os.WriteFile(path, file, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO t VALUES (1)`); err == nil {
		t.Error("the read-only DSN allowed a write")
	}
	if _, err := db.Exec(`CREATE TABLE x (a)`); err == nil {
		t.Error("the read-only DSN allowed a schema change")
	}
}
