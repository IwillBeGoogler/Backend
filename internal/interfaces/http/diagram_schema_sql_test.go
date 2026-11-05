package http

// GET /api/diagrams/schema: admin only, application/sql, prefix passed on.

import (
	"context"
	"strings"
	"testing"

	"github.com/iBlog/iblog-monolith-go/internal/application"
)

func TestDiagramSchemaSQLRoute(t *testing.T) {
	var gotPrefix string
	e := &blogEnv{t: t, audit: &auditLog{}}
	e.srv, e.mem = newDiagramServerWith(t, func(d *application.Diagrams, _ *Deps) {
		d.Authz = permAuthz{adminReader: {"diagrams.read"}, adminManager: {"diagrams.manage"}}
		d.SchemaDump = func(_ context.Context, prefix string) (string, error) {
			gotPrefix = prefix
			return "CREATE TABLE dg_x (\n    id text NOT NULL\n);\n", nil
		}
	})
	const path = "/api/diagrams/schema"

	if r := e.do(0, "GET", path, nil); r.Code != 401 {
		t.Errorf("anonymous: %d", r.Code)
	}
	for _, u := range []int{1, adminReader} {
		if r := e.do(u, "GET", path, nil); r.Code != 403 {
			t.Errorf("user %d: %d, want 403", u, r.Code)
		}
	}
	r := e.want(e.do(adminManager, "GET", path+"?prefix=dg_", nil), 200)
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/sql") {
		t.Errorf("Content-Type = %q", ct)
	}
	if gotPrefix != "dg_" || !strings.HasPrefix(string(r.Raw), "CREATE TABLE dg_x") {
		t.Errorf("prefix %q, body %q", gotPrefix, r.Raw)
	}
	if r := e.do(adminManager, "GET", path+"?prefix="+strings.Repeat("x", 64), nil); r.Code != 400 {
		t.Errorf("long prefix: %d", r.Code)
	}
}
