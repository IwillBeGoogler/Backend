package http

// The schema introspection routes: input validation, the error codes, the
// token ban and the rate limit, against a fake reader (the readers have
// their own tests).

import (
	"context"
	"strings"
	"testing"

	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
)

type fakeSchema struct {
	target diagram.DBTarget
	err    error
	file   []byte
	calls  int
}

func (f *fakeSchema) result() diagram.Introspected {
	return diagram.Introspected{Schema: diagram.Schema{
		Tables: []diagram.SchemaTable{
			{Name: "a", Kind: "table", Columns: []diagram.SchemaColumn{{Name: "id", Type: "int", PK: true}}, Indexes: []diagram.SchemaIndex{}, Checks: []diagram.SchemaCheck{}},
			{Name: "v", Kind: "view", Columns: []diagram.SchemaColumn{{Name: "x", Type: "text", Nullable: true, FK: &diagram.SchemaFK{Table: "a", Column: "id"}}}, Indexes: []diagram.SchemaIndex{}, Checks: []diagram.SchemaCheck{}},
		},
		Enums: []diagram.SchemaEnum{{Name: "mood", Values: []string{"ok"}}}}, Warnings: []string{"w"}}
}

func (f *fakeSchema) Introspect(_ context.Context, t diagram.DBTarget) (diagram.Introspected, error) {
	f.target, f.calls = t, f.calls+1
	return f.result(), f.err
}

func (f *fakeSchema) IntrospectSQLite(_ context.Context, b []byte) (diagram.Introspected, error) {
	f.file, f.calls = b, f.calls+1
	return f.result(), f.err
}

func TestDiagramSchemaIntrospectRoute(t *testing.T) {
	env := newTokenServer(t)
	alice := env.session(t, 1)
	good := map[string]any{"dialect": "postgres", "host": " db.example.com ", "user": "bob", "password": "hunter2hunter2", "database": "app", "ssl": true}
	with := func(k string, v any) map[string]any {
		m := map[string]any{}
		for kk, vv := range good {
			m[kk] = vv
		}
		if v == nil {
			delete(m, k)
		} else {
			m[k] = v
		}
		return m
	}

	r := alice.expect(alice.do("POST", "/schema/introspect", good), 200)
	counts := r.obj("counts")
	if counts["tables"] != float64(1) || counts["views"] != float64(1) || counts["columns"] != float64(2) || counts["enums"] != float64(1) || r.Body["warnings"].([]any)[0] != "w" {
		t.Errorf("response = %v", r.Body)
	}
	if env.schema.target.Host != "db.example.com" || env.schema.target.Port != 0 || !env.schema.target.SSL || env.schema.target.Password != "hunter2hunter2" || env.schema.target.Dialect != "postgres" {
		t.Errorf("target = %+v", env.schema.target)
	}
	tables := r.obj("schema")["tables"].([]any)
	if fk := tables[1].(map[string]any)["columns"].([]any)[0].(map[string]any)["fk"]; fk == nil {
		t.Errorf("the foreign key is missing: %v", tables[1])
	}

	for name, body := range map[string]map[string]any{
		"dialect":   with("dialect", "oracle"),
		"no host":   with("host", ""),
		"port":      with("port", 70000),
		"port zero": with("port", 0),
		"no user":   with("user", nil),
		"database":  with("database", ""),
		"password":  with("password", strings.Repeat("x", 300)),
	} {
		calls := env.schema.calls
		if r := alice.do("POST", "/schema/introspect", body); r.Code != 400 || env.schema.calls != calls {
			t.Errorf("%s: %d %v (reader called: %v)", name, r.Code, r.Body, env.schema.calls != calls)
		}
	}
	if r := env.session(t, 0).do("POST", "/schema/introspect", good); r.Code != 401 {
		t.Errorf("anonymous: %d", r.Code)
	}
	tok := env.bearer(t, env.mint(t, alice, diagram.Scopes, nil))
	if r := tok("POST", "/api/diagrams/schema/introspect", good); r.Code != 403 {
		t.Errorf("token: %d", r.Code)
	}

	// the reader's errors keep their codes; nothing leaks the password
	env.schema.err = &diagram.TargetBlockedError{Reason: "Private, loopback and link-local addresses are not allowed"}
	if r := alice.do("POST", "/schema/introspect", good); r.Code != 422 || r.errCode() != "TARGET_BLOCKED" {
		t.Errorf("blocked: %d %v", r.Code, r.Body)
	}
	env.schema.err = &diagram.IntrospectFailedError{Msg: "connection refused"}
	if r := alice.do("POST", "/schema/introspect", good); r.Code != 422 || r.errCode() != "INTROSPECT_FAILED" || strings.Contains(string(mustJSON(t, r.Body)), "hunter2") {
		t.Errorf("failed: %d %v", r.Code, r.Body)
	}
}

func TestDiagramSchemaIntrospectSQLiteRoute(t *testing.T) {
	env := newTokenServer(t)
	alice := env.session(t, 1)
	file := append([]byte("SQLite format 3\x00"), make([]byte, 200)...)
	if r := alice.uploadSDD("/schema/introspect/sqlite", file).json(); r.Code != 200 || r.obj("counts")["tables"] != float64(1) || len(env.schema.file) != len(file) {
		t.Errorf("sqlite: %d %v", r.Code, r.Body)
	}
	env.schema.err = diagram.NotSQLiteError{}
	if r := alice.uploadSDD("/schema/introspect/sqlite", []byte("nope")).json(); r.Code != 422 || r.errCode() != "NOT_SQLITE" {
		t.Errorf("not sqlite: %d %v", r.Code, r.Body)
	}
	if r := alice.raw("POST", "/schema/introspect/sqlite", []byte("{}"), "application/json"); r.Code != 415 {
		t.Errorf("not multipart: %d", r.Code)
	}
	if r := env.session(t, 0).uploadSDD("/schema/introspect/sqlite", file); r.Code != 401 {
		t.Errorf("anonymous: %d", r.Code)
	}
}

func TestDiagramSchemaRoutesAreOffWithoutAReader(t *testing.T) {
	srv, _ := newDiagramServer(t) // no Schema
	alice := dgClient{t, srv, 1}
	if r := alice.do("POST", "/schema/introspect", map[string]any{}); r.Code != 404 {
		t.Errorf("off: %d", r.Code)
	}
}
