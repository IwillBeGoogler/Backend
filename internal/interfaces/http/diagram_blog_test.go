package http

// Wire format of the diagrams admin API (blog conventions: {data, meta},
// snake_case, RFC 3339, problem details, 204) and of the blog integration:
// publishing, the public reads and the template gallery.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram/diagramtest"
)

// permAuthz grants each test user a list of "resource.action" permissions.
type permAuthz map[int][]string

func (p permAuthz) Can(ctx context.Context, resource, action string, _, _ int) error {
	id, _ := ctx.Value(testUser{}).(int)
	switch {
	case id == 0:
		return domain.ErrUnauthorized
	case slices.Contains(p[id], resource+"."+action):
		return nil
	}
	return domain.ErrForbidden
}

type auditLog struct{ actions []string }

func (a *auditLog) Record(_ context.Context, _ int, action, _ string, _ map[string]any) {
	a.actions = append(a.actions, action)
}

const (
	adminReader  = 7
	adminManager = 8
	tplAdmin     = 9
	tplOnly      = 10
)

type blogEnv struct {
	t     *testing.T
	srv   *httptest.Server
	mem   *diagramtest.Memory
	audit *auditLog
}

func newBlogEnv(t *testing.T) *blogEnv {
	t.Helper()
	e := &blogEnv{t: t, audit: &auditLog{}}
	e.srv, e.mem = newDiagramServerWith(t, func(d *application.Diagrams, _ *Deps) {
		d.Authz = permAuthz{
			adminReader:  {"diagrams.read"},
			adminManager: {"diagrams.manage"},
			// templates publish file content, so editing them also needs diagrams.read
			tplAdmin: {"templates.manage", "diagrams.read"},
			tplOnly:  {"templates.manage"},
		}
		d.Audit = e.audit
		d.Templates = diagramtest.NewTemplates(d.Repo.(*diagramtest.Memory))
	})
	e.mem.SetUser(1, "alice")
	e.mem.SetUser(2, "bob")
	return e
}

type resp struct {
	Code   int
	Header http.Header
	Raw    []byte
	Body   map[string]any
}

func (r resp) data() map[string]any { m, _ := r.Body["data"].(map[string]any); return m }
func (r resp) list() []any          { l, _ := r.Body["data"].([]any); return l }
func (r resp) meta() map[string]any { m, _ := r.Body["meta"].(map[string]any); return m }

func (e *blogEnv) do(user int, method, path string, body any, hdr ...string) resp {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = strings.NewReader(string(raw))
	}
	req, err := http.NewRequest(method, e.srv.URL+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if user != 0 {
		req.Header.Set("X-Test-User", strconv.Itoa(user))
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	out := resp{Code: res.StatusCode, Header: res.Header}
	out.Raw, _ = io.ReadAll(res.Body)
	if len(out.Raw) > 0 {
		_ = json.Unmarshal(out.Raw, &out.Body)
	}
	return out
}

func (e *blogEnv) want(r resp, code int) resp {
	e.t.Helper()
	if r.Code != code {
		e.t.Fatalf("status = %d, want %d; body %s", r.Code, code, r.Raw)
	}
	return r
}

// problem checks an RFC 9457 body.
func (e *blogEnv) problem(r resp, code int) map[string]any {
	e.t.Helper()
	e.want(r, code)
	if ct := r.Header.Get("Content-Type"); !strings.Contains(ct, "problem+json") {
		e.t.Errorf("Content-Type = %q, want problem+json", ct)
	}
	for _, k := range []string{"type", "title", "status", "detail", "code"} {
		if _, ok := r.Body[k]; !ok {
			e.t.Errorf("problem lacks %q: %s", k, r.Raw)
		}
	}
	if int(r.Body["status"].(float64)) != code {
		e.t.Errorf("problem status = %v", r.Body["status"])
	}
	return r.Body
}

// project makes alice (or user) a project with the document and returns
// its project and file ids.
func (e *blogEnv) project(user int, name string) (string, string) {
	r := e.want(e.do(user, "POST", DiagramPrefix+"/projects", map[string]any{"name": name,
		"file": map[string]any{"name": name + " file", "data": json.RawMessage(`{"version":2,"activePageId":"pg","pages":[{"id":"pg","name":"P","nodes":[{"id":"n1","type":"service"}],"edges":[]}]}`)}}), 201)
	return r.Body["project"].(map[string]any)["id"].(string), r.Body["file"].(map[string]any)["id"].(string)
}

func TestAdminDiagramsWire(t *testing.T) {
	e := newBlogEnv(t)
	pid, fid := e.project(1, "Payments")
	e.project(2, "Search")
	e.project(1, "Payroll")

	// 401 without a session, 403 without the permission (as problems)
	for _, p := range []string{"/stats", "/projects", "/projects/" + pid, "/templates"} {
		e.problem(e.do(0, "GET", "/api/admin/diagrams"+p, nil), 401)
		e.problem(e.do(1, "GET", "/api/admin/diagrams"+p, nil), 403)
	}
	e.problem(e.do(adminReader, "DELETE", "/api/admin/diagrams/projects/"+pid, nil), 403)
	e.problem(e.do(adminReader, "POST", "/api/admin/diagrams/shares/shr_x/revoke", nil), 403)
	e.problem(e.do(adminManager, "GET", "/api/admin/diagrams/templates", nil), 403)
	e.problem(e.do(adminReader, "POST", "/api/admin/diagrams/templates", map[string]any{}), 403)
	e.problem(e.do(tplOnly, "GET", "/api/admin/diagrams/stats", nil), 403)

	// stats: diagrams.manage implies read
	for _, u := range []int{adminReader, adminManager} {
		st := e.want(e.do(u, "GET", "/api/admin/diagrams/stats", nil), 200).data()
		kinds, _ := st["files_by_kind"].(map[string]any)
		if st["projects"] != 3.0 || st["files"] != 3.0 || kinds["sdd"] != 3.0 || kinds["erd"] != 0.0 || kinds["uml"] != 0.0 || kinds["board"] != 0.0 ||
			st["active_editors_7d"] != 2.0 {
			t.Errorf("stats = %v", st)
		}
		if n, _ := st["storage_bytes"].(float64); n <= 0 {
			t.Errorf("storage_bytes = %v", st["storage_bytes"])
		}
	}

	// project list: {data, meta}, snake_case, RFC 3339
	r := e.want(e.do(adminReader, "GET", "/api/admin/diagrams/projects?limit=2", nil), 200)
	if len(r.list()) != 2 || r.meta()["limit"] != 2.0 || r.meta()["has_more"] != true || r.meta()["next_cursor"] == "" {
		t.Fatalf("page 1 = %s", r.Raw)
	}
	row := r.list()[0].(map[string]any)
	owner, _ := row["owner"].(map[string]any)
	for _, k := range []string{"id", "name", "owner", "files", "updated_at", "thumbnail_url", "shared"} {
		if _, ok := row[k]; !ok {
			t.Errorf("row lacks %q: %v", k, row)
		}
	}
	if _, err := time.Parse(time.RFC3339, row["updated_at"].(string)); err != nil {
		t.Errorf("updated_at = %v", row["updated_at"])
	}
	if _, ok := owner["id"].(string); !ok || owner["username"] == "" || row["files"] != 1.0 || row["shared"] != false || row["thumbnail_url"] != nil {
		t.Errorf("row = %v", row)
	}
	next := r.meta()["next_cursor"].(string)
	r2 := e.want(e.do(adminReader, "GET", "/api/admin/diagrams/projects?limit=2&cursor="+next, nil), 200)
	if len(r2.list()) != 1 || r2.meta()["has_more"] != false {
		t.Errorf("page 2 = %s", r2.Raw)
	}
	e.problem(e.do(adminReader, "GET", "/api/admin/diagrams/projects?cursor=zzz", nil), 400)
	if got := e.want(e.do(adminReader, "GET", "/api/admin/diagrams/projects?q=PAY&owner=alice", nil), 200).list(); len(got) != 2 {
		t.Errorf("q+owner = %v", got)
	}
	if got := e.want(e.do(adminReader, "GET", "/api/admin/diagrams/projects?owner=bob", nil), 200).list(); len(got) != 1 {
		t.Errorf("owner = %v", got)
	}
	if got := e.want(e.do(adminReader, "GET", "/api/admin/diagrams/projects?q=nothing", nil), 200); got.Body["data"] == nil || len(got.list()) != 0 {
		t.Errorf("empty list must be [], got %s", got.Raw)
	}

	// detail: files, members and shares; never a token or a document
	e.mem.AddMember(pid, 2, diagram.RoleEditor)
	sh := e.want(e.do(1, "POST", DiagramPrefix+"/projects/"+pid+"/shares", map[string]any{"role": "viewer"}), 201)
	shareID := sh.Body["share"].(map[string]any)["id"].(string)
	d := e.want(e.do(adminReader, "GET", "/api/admin/diagrams/projects/"+pid, nil), 200)
	det := d.data()
	if det["id"] != pid || det["shared"] != true || det["owner"].(map[string]any)["username"] != "alice" {
		t.Errorf("detail = %s", d.Raw)
	}
	f0 := det["files"].([]any)[0].(map[string]any)
	if f0["id"] != fid || f0["kind"] != "sdd" || f0["version"] != 1.0 || f0["thumbnail_url"] != nil {
		t.Errorf("file = %v", f0)
	}
	m0 := det["members"].([]any)[0].(map[string]any)
	if m0["user_id"] != "2" || m0["username"] != "bob" || m0["role"] != "editor" || m0["joined_at"] == nil {
		t.Errorf("member = %v", m0)
	}
	s0 := det["shares"].([]any)[0].(map[string]any)
	if s0["id"] != shareID || s0["role"] != "viewer" || s0["created_by"] != "alice" || s0["expires_at"] != nil || s0["revoked_at"] != nil {
		t.Errorf("share = %v", s0)
	}
	for _, leak := range []string{"token", `"pages"`, `"nodes"`} {
		if strings.Contains(string(d.Raw), leak) {
			t.Errorf("detail leaks %q: %s", leak, d.Raw)
		}
	}
	e.problem(e.do(adminReader, "GET", "/api/admin/diagrams/projects/prj_nope", nil), 404)

	// revoke: 204, idempotent, 404; the status shows in the detail
	for range 2 {
		if r := e.want(e.do(adminManager, "POST", "/api/admin/diagrams/shares/"+shareID+"/revoke", nil), 204); len(r.Raw) != 0 {
			t.Errorf("204 with a body: %s", r.Raw)
		}
	}
	e.problem(e.do(adminManager, "POST", "/api/admin/diagrams/shares/shr_nope/revoke", nil), 404)
	det = e.want(e.do(adminReader, "GET", "/api/admin/diagrams/projects/"+pid, nil), 200).data()
	if det["shared"] != false || det["shares"].([]any)[0].(map[string]any)["revoked_at"] == nil {
		t.Errorf("after revoke = %v", det)
	}

	// delete: 204, then 404; audited
	e.want(e.do(adminManager, "DELETE", "/api/admin/diagrams/projects/"+pid, nil), 204)
	e.problem(e.do(adminManager, "DELETE", "/api/admin/diagrams/projects/"+pid, nil), 404)
	e.problem(e.do(adminReader, "GET", "/api/admin/diagrams/projects/"+pid, nil), 404)
	if !slices.Equal(e.audit.actions, []string{"diagram.share.revoke", "diagram.share.revoke", "diagram.project.delete"}) {
		t.Errorf("audit = %v", e.audit.actions)
	}
}

func TestAdminTemplatesWire(t *testing.T) {
	e := newBlogEnv(t)
	_, f1 := e.project(1, "One")
	_, f2 := e.project(1, "Two")
	const base = "/api/admin/diagrams/templates"

	if got := e.want(e.do(tplAdmin, "GET", base, nil), 200); got.Body["data"] == nil || len(got.list()) != 0 {
		t.Errorf("empty = %s", got.Raw)
	}
	// validation: shown inline as problem details
	for _, bad := range []map[string]any{
		{"title": "", "kind": "system", "file_id": f1},
		{"title": "T", "kind": "other", "file_id": f1},
		{"title": "T", "kind": "system", "file_id": "fil_missing"},
		{"title": strings.Repeat("x", 121), "kind": "db", "file_id": f1},
		{"title": "T", "kind": "db", "file_id": f1, "description": strings.Repeat("d", 501)},
	} {
		p := e.problem(e.do(tplAdmin, "POST", base, bad), 400)
		if p["detail"] == "" {
			t.Errorf("no detail for %v", bad)
		}
	}
	e.problem(e.do(tplAdmin, "POST", base, "{not json"), 400)

	create := func(title, file string, pos int) map[string]any {
		r := e.want(e.do(tplAdmin, "POST", base, map[string]any{"title": title, "kind": "ood", "description": "d", "file_id": file, "published": false, "position": pos}), 201)
		return r.data()
	}
	a, b := create("Alpha", f1, 0), create("Beta", f2, 1)
	for _, k := range []string{"id", "title", "kind", "description", "file_id", "published", "position"} {
		if _, ok := a[k]; !ok {
			t.Errorf("template lacks %q: %v", k, a)
		}
	}
	if a["published"] != false || a["kind"] != "ood" || a["file_id"] != f1 || !strings.HasPrefix(a["id"].(string), "tpl_") {
		t.Errorf("template = %v", a)
	}

	// PATCH published toggle, position swap, bad value, unknown id
	r := e.want(e.do(tplAdmin, "PATCH", base+"/"+a["id"].(string), map[string]any{"published": true}), 200)
	if r.data()["published"] != true || r.data()["title"] != "Alpha" {
		t.Errorf("patched = %s", r.Raw)
	}
	e.want(e.do(tplAdmin, "PATCH", base+"/"+a["id"].(string), map[string]any{"position": 5}), 200)
	e.problem(e.do(tplAdmin, "PATCH", base+"/"+a["id"].(string), map[string]any{"kind": "uml"}), 400)
	e.problem(e.do(tplAdmin, "PATCH", base+"/tpl_nope", map[string]any{"published": true}), 404)

	// atomic reorder: the listed ids first; an unknown id changes nothing
	e.problem(e.do(tplAdmin, "POST", base+"/reorder", map[string]any{"ids": []string{b["id"].(string), "tpl_nope"}}), 404)
	titles := func(rows []any) []string {
		var out []string
		for _, x := range rows {
			out = append(out, x.(map[string]any)["title"].(string))
		}
		return out
	}
	if got := titles(e.want(e.do(tplAdmin, "GET", base, nil), 200).list()); !slices.Equal(got, []string{"Beta", "Alpha"}) {
		t.Errorf("after failed reorder = %v", got)
	}
	r = e.want(e.do(tplAdmin, "POST", base+"/reorder", map[string]any{"ids": []string{a["id"].(string), b["id"].(string)}}), 200)
	if got := titles(r.list()); !slices.Equal(got, []string{"Alpha", "Beta"}) {
		t.Errorf("reordered = %v", got)
	}
	if r.list()[0].(map[string]any)["position"] != 0.0 || r.list()[1].(map[string]any)["position"] != 1.0 {
		t.Errorf("positions = %s", r.Raw)
	}
	e.problem(e.do(tplAdmin, "POST", base+"/reorder", map[string]any{"ids": []string{}}), 400)
	e.problem(e.do(adminManager, "POST", base+"/reorder", map[string]any{"ids": []string{"x"}}), 403)

	// the public gallery lists published templates only
	gal := e.want(e.do(0, "GET", DiagramPrefix+"/templates", nil), 200)
	if len(gal.list()) != 1 {
		t.Fatalf("gallery = %s", gal.Raw)
	}
	g0 := gal.list()[0].(map[string]any)
	if g0["title"] != "Alpha" || g0["id"] != a["id"] || g0["page_count"] != 1.0 {
		t.Errorf("gallery row = %v", g0)
	}
	if _, leaks := g0["file_id"]; leaks {
		t.Error("the gallery shows the source file id")
	}

	// fork: needs a session, copies into the caller's own project
	e.want(e.do(0, "POST", DiagramPrefix+"/templates/"+a["id"].(string)+"/fork", nil), 401)
	e.want(e.do(2, "POST", DiagramPrefix+"/templates/"+b["id"].(string)+"/fork", nil), 404) // a draft
	fk := e.want(e.do(2, "POST", DiagramPrefix+"/templates/"+a["id"].(string)+"/fork", nil), 201)
	proj, file := fk.Body["project"].(map[string]any), fk.Body["file"].(map[string]any)
	if proj["name"] != "Alpha" || proj["ownerId"] != "2" || proj["role"] != "owner" || file["id"] == f1 || file["data"] == nil ||
		fk.Header.Get("ETag") != `"1"` || !strings.HasPrefix(fk.Header.Get("Location"), DiagramPrefix+"/projects/prj_") {
		t.Errorf("fork = %s", fk.Raw)
	}
	e.want(e.do(1, "GET", DiagramPrefix+"/files/"+file["id"].(string), nil), 404)
	e.want(e.do(2, "GET", DiagramPrefix+"/files/"+file["id"].(string), nil), 200)

	// delete removes only the template
	e.want(e.do(tplAdmin, "DELETE", base+"/"+a["id"].(string), nil), 204)
	e.problem(e.do(tplAdmin, "DELETE", base+"/"+a["id"].(string), nil), 404)
	e.want(e.do(1, "GET", DiagramPrefix+"/files/"+f1, nil), 200)
}

func TestPublishAndPublicWire(t *testing.T) {
	e := newBlogEnv(t)
	_, fid := e.project(1, "Checkout")
	e.mem.SetUser(1, "alice")
	pub := DiagramPrefix + "/files/" + fid + "/publish"

	// not published yet
	r := e.want(e.do(1, "GET", pub, nil), 200)
	if r.Body["visibility"] != "private" || r.Body["slug"] != nil || r.Body["url"] != nil {
		t.Errorf("fresh = %s", r.Raw)
	}
	e.want(e.do(0, "POST", pub, map[string]any{"visibility": "public"}), 401)
	e.want(e.do(2, "POST", pub, map[string]any{"visibility": "public"}), 404)
	if r := e.do(1, "POST", pub, map[string]any{"visibility": "private"}); r.Code != 400 || r.Body["error"].(map[string]any)["code"] != "VALIDATION_ERROR" {
		t.Errorf("private publish = %d %s", r.Code, r.Raw)
	}

	r = e.want(e.do(1, "POST", pub, map[string]any{"visibility": "public", "title": "Checkout flow", "description": "How we pay", "tags": []string{"Payments", "uml"}}), 200)
	slug, _ := r.Body["slug"].(string)
	if r.Body["fileId"] != fid || r.Body["visibility"] != "public" || r.Body["title"] != "Checkout flow" || r.Body["url"] != "/d/v/"+slug ||
		!strings.HasPrefix(slug, "checkout-flow-") || r.Body["publishedAt"] == nil || len(r.Body["tags"].([]any)) != 2 {
		t.Errorf("published = %s", r.Raw)
	}

	// the public page: anonymous, cacheable, document + author
	pg := e.want(e.do(0, "GET", DiagramPrefix+"/public/"+slug, nil), 200)
	if cc := pg.Header.Get("Cache-Control"); !strings.HasPrefix(cc, "public, max-age=") || pg.Header.Get("ETag") == "" || pg.Header.Get("X-Robots-Tag") != "" {
		t.Errorf("headers = %v", pg.Header)
	}
	d := pg.data()
	author, _ := d["author"].(map[string]any)
	if d["file_id"] != fid || d["slug"] != slug || d["title"] != "Checkout flow" || d["visibility"] != "public" || d["kind"] != "sdd" ||
		d["page_count"] != 1.0 || author["username"] != "alice" || author["id"] != 1.0 || d["thumbnail_url"] != nil {
		t.Errorf("public = %s", pg.Raw)
	}
	doc, _ := d["data"].(map[string]any)
	if doc["id"] != fid || doc["pages"] == nil {
		t.Errorf("document = %v", doc)
	}
	if _, err := time.Parse(time.RFC3339, d["published_at"].(string)); err != nil {
		t.Errorf("published_at = %v", d["published_at"])
	}
	if r := e.do(0, "GET", DiagramPrefix+"/public/"+slug, nil, "If-None-Match", pg.Header.Get("ETag")); r.Code != 304 {
		t.Errorf("revalidation = %d", r.Code)
	}
	e.problem(e.do(0, "GET", DiagramPrefix+"/public/nope-123", nil), 404)
	e.problem(e.do(0, "GET", DiagramPrefix+"/public/..%2Fadmin", nil), 404)

	// listing: search, tag, profile tab with meta
	lst := e.want(e.do(0, "GET", DiagramPrefix+"/public?q=checkout&tag=PAYMENTS", nil), 200)
	if len(lst.list()) != 1 || lst.meta()["total"] != 1.0 || lst.meta()["limit"] != 10.0 {
		t.Errorf("public list = %s", lst.Raw)
	}
	if _, has := lst.list()[0].(map[string]any)["data"]; has {
		t.Error("listings carry documents")
	}
	if got := e.want(e.do(0, "GET", DiagramPrefix+"/public?q=zzz", nil), 200); got.Body["data"] == nil || len(got.list()) != 0 {
		t.Errorf("no match = %s", got.Raw)
	}
	prof := e.want(e.do(0, "GET", "/api/users/Alice/diagrams?limit=5", nil), 200)
	if len(prof.list()) != 1 || prof.meta()["limit"] != 5.0 || prof.meta()["has_more"] != false {
		t.Errorf("profile tab = %s", prof.Raw)
	}
	e.problem(e.do(0, "GET", "/api/users/nobody/diagrams", nil), 404)

	// unlisted: opens by slug (noindex), but is not listed
	e.want(e.do(1, "POST", pub, map[string]any{"visibility": "unlisted"}), 200)
	if pg = e.want(e.do(0, "GET", DiagramPrefix+"/public/"+slug, nil), 200); pg.Header.Get("X-Robots-Tag") != "noindex" {
		t.Errorf("unlisted headers = %v", pg.Header)
	}
	if got := e.want(e.do(0, "GET", "/api/users/alice/diagrams", nil), 200); len(got.list()) != 0 {
		t.Errorf("unlisted on profile: %s", got.Raw)
	}

	// unpublish: 204 (again too), the page is gone, the slug survives
	e.want(e.do(2, "DELETE", pub, nil), 404)
	for range 2 {
		if r := e.want(e.do(1, "DELETE", pub, nil), 204); len(r.Raw) != 0 {
			t.Errorf("204 with body %s", r.Raw)
		}
	}
	e.problem(e.do(0, "GET", DiagramPrefix+"/public/"+slug, nil), 404)
	if r := e.want(e.do(1, "POST", pub, map[string]any{"visibility": "public"}), 200); r.Body["slug"] != slug {
		t.Errorf("republished slug = %v, want %s", r.Body["slug"], slug)
	}
}

func TestPublishOnlyForOwners(t *testing.T) {
	e := newBlogEnv(t)
	pid, fid := e.project(1, "Shared")
	e.mem.AddMember(pid, 2, diagram.RoleEditor)
	pub := DiagramPrefix + "/files/" + fid + "/publish"
	r := e.do(2, "POST", pub, map[string]any{"visibility": "public"})
	if r.Code != 403 || r.Body["error"].(map[string]any)["code"] != "FORBIDDEN" {
		t.Errorf("editor publish = %d %s", r.Code, r.Raw)
	}
	if r := e.do(2, "DELETE", pub, nil); r.Code != 403 {
		t.Errorf("editor unpublish = %d", r.Code)
	}
	// a member may read the state
	e.want(e.do(2, "GET", pub, nil), 200)
}
