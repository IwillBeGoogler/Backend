package introspect

// PostgreSQL introspection against a real server. Uses TEST_DATABASE_URL
// when set, else a container; skipped with -short or without Docker.

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
)

func pgTarget(t *testing.T) (diagram.DBTarget, *pgxpool.Pool) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test")
	}
	ctx := context.Background()
	dsn := ""
	if dsn = os.Getenv("TEST_DATABASE_URL"); dsn == "" {
		pg, err := tcpostgres.Run(ctx, "postgres:18-alpine",
			tcpostgres.WithDatabase("blog"), tcpostgres.WithUsername("blog"), tcpostgres.WithPassword("blog"),
			testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(60*time.Second)))
		if err != nil {
			t.Skipf("docker unavailable: %v", err)
		}
		t.Cleanup(func() { testcontainers.TerminateContainer(pg) })
		if dsn, err = pg.ConnectionString(ctx, "sslmode=disable"); err != nil {
			t.Fatal(err)
		}
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(u.Port())
	pass, _ := u.User.Password()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return diagram.DBTarget{Dialect: diagram.DialectPostgres, Host: u.Hostname(), Port: port, User: u.User.Username(), Password: pass, Database: strings.TrimPrefix(u.Path, "/")}, pool
}

func TestIntrospectPostgres(t *testing.T) {
	target, pool := pgTarget(t)
	ctx := context.Background()
	schema := "intro_" + strconv.FormatInt(time.Now().UnixNano()%1e9, 36)
	for _, q := range []string{
		`CREATE SCHEMA ` + schema,
		`CREATE TYPE ` + schema + `.mood AS ENUM ('sad', 'ok', 'happy')`,
		`CREATE TABLE ` + schema + `.users (id serial PRIMARY KEY, email varchar(255) NOT NULL UNIQUE, mood ` + schema + `.mood, bio text DEFAULT 'none', a int, b int, UNIQUE (a, b))`,
		`CREATE TABLE ` + schema + `.orders (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, user_id int NOT NULL REFERENCES ` + schema + `.users(id) ON DELETE CASCADE,
			total numeric(10,2) NOT NULL, at timestamptz NOT NULL DEFAULT now(), tags text[], double numeric GENERATED ALWAYS AS (total * 2) STORED)`,
		`CREATE INDEX orders_user_at ON ` + schema + `.orders (user_id, at DESC)`,
		`CREATE INDEX orders_lower ON ` + schema + `.orders ((total + 1))`,
		`CREATE VIEW ` + schema + `.v AS SELECT id FROM ` + schema + `.users`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	t.Cleanup(func() { pool.Exec(ctx, `DROP SCHEMA `+schema+` CASCADE`) })
	var objectsBefore int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = $1`, schema).Scan(&objectsBefore)

	s := New(true)
	target.Schema = schema
	res, err := s.Introspect(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]diagram.SchemaTable{}
	for _, tb := range res.Schema.Tables {
		by[tb.Name] = tb
	}
	if len(by) != 3 || by["v"].Kind != "view" || by["users"].Kind != "table" {
		t.Fatalf("tables = %+v", res.Schema.Tables)
	}
	col := func(tb, c string) diagram.SchemaColumn {
		for _, x := range by[tb].Columns {
			if x.Name == c {
				return x
			}
		}
		t.Fatalf("no column %s.%s", tb, c)
		return diagram.SchemaColumn{}
	}
	if c := col("users", "id"); !c.PK || c.Generated != "autoincrement" || c.Type != "integer" || c.Nullable {
		t.Errorf("users.id = %+v", c)
	}
	if c := col("users", "email"); !c.Unique || c.Type != "varchar(255)" || c.Nullable {
		t.Errorf("users.email = %+v", c)
	}
	if c := col("users", "mood"); c.Type != "mood" || !c.Nullable {
		t.Errorf("users.mood = %+v", c)
	}
	if c := col("users", "bio"); !strings.Contains(c.Default, "none") {
		t.Errorf("users.bio = %+v", c)
	}
	if c := col("orders", "id"); c.Generated != "identity" || c.Type != "bigint" {
		t.Errorf("orders.id = %+v", c)
	}
	if c := col("orders", "user_id"); c.FK == nil || c.FK.Table != "users" || c.FK.Column != "id" || c.FK.OnDelete != "CASCADE" || c.FK.OnUpdate != "" {
		t.Errorf("orders.user_id = %+v", c.FK)
	}
	for c, want := range map[string]string{"total": "numeric(10,2)", "at": "timestamptz", "tags": "text[]"} {
		if got := col("orders", c).Type; got != want {
			t.Errorf("orders.%s type = %q, want %q", c, got, want)
		}
	}
	if c := col("orders", "double"); !strings.HasPrefix(c.Generated, "as (") {
		t.Errorf("orders.double = %+v", c)
	}
	if ix := by["orders"].Indexes; len(ix) != 1 || ix[0].Name != "orders_user_at" || strings.Join(ix[0].Columns, ",") != "user_id,at" {
		t.Errorf("orders indexes = %+v", ix)
	}
	if ix := by["users"].Indexes; len(ix) != 1 || !ix[0].Unique || strings.Join(ix[0].Columns, ",") != "a,b" {
		t.Errorf("users indexes = %+v", ix)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "orders_lower") {
		t.Errorf("warnings = %v", res.Warnings)
	}
	if len(res.Schema.Enums) != 1 || strings.Join(res.Schema.Enums[0].Values, ",") != "sad,ok,happy" {
		t.Errorf("enums = %+v", res.Schema.Enums)
	}

	var objectsAfter int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = $1`, schema).Scan(&objectsAfter)
	if objectsAfter != objectsBefore {
		t.Errorf("introspection changed the database: %d objects before, %d after", objectsBefore, objectsAfter)
	}

	// the default schema is public; an unknown schema is empty, not an error
	if res, err := s.Introspect(ctx, diagram.DBTarget{Dialect: target.Dialect, Host: target.Host, Port: target.Port, User: target.User, Password: target.Password, Database: target.Database, Schema: "no_such_schema"}); err != nil || len(res.Schema.Tables) != 0 {
		t.Errorf("unknown schema: %v %v", res, err)
	}

	// the SSRF guard: this server is on loopback, which a default service refuses
	var blocked *diagram.TargetBlockedError
	if _, err := New(false).Introspect(ctx, target); !errors.As(err, &blocked) {
		t.Errorf("loopback with the guard on: %v", err)
	}

	// a wrong password fails without echoing it
	bad := target
	bad.Password = "wrongpw-" + "sup3rs3cret"
	_, err = s.Introspect(ctx, bad)
	var failed *diagram.IntrospectFailedError
	if !errors.As(err, &failed) || strings.Contains(failed.Msg, "sup3rs3cret") {
		t.Errorf("bad password: %v", err)
	}
	// a closed port fails quickly
	closed := target
	closed.Port = 1
	if _, err := s.Introspect(ctx, closed); !errors.As(err, &failed) {
		t.Errorf("closed port: %v", err)
	}
}

// The session is read only: even if a catalog query were swapped for a write,
// the server would refuse it. This checks the session settings directly.
func TestIntrospectPostgresSessionIsReadOnly(t *testing.T) {
	target, _ := pgTarget(t)
	s := New(true)
	ctx := context.Background()
	cfgTarget := target
	cfgTarget.Schema = "public"
	// run the same connection setup and ask the server what it applied
	var got struct{ ro, timeout string }
	err := s.probePostgres(ctx, cfgTarget, func(ro, timeout string) { got.ro, got.timeout = ro, timeout })
	if err != nil {
		t.Fatal(err)
	}
	if got.ro != "on" || got.timeout != "10s" {
		t.Errorf("transaction_read_only = %q, statement_timeout = %q", got.ro, got.timeout)
	}
}
