package http

// The diagram API's wire format against the in-memory repository: it must
// stay what the Node diagram server sent (bare objects, camelCase, epoch
// milliseconds, string ids, ETag and If-Match, {"error": {...}}).

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/iBlog/iblog-monolith-go/internal/application"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram/diagramtest"
)

type testUser struct{}

type dgClient struct {
	t    *testing.T
	srv  *httptest.Server
	user int
}

type dgResp struct {
	Code   int
	Header http.Header
	Body   map[string]any
}

func (r dgResp) obj(keys ...string) map[string]any {
	cur := r.Body
	for _, k := range keys {
		cur, _ = cur[k].(map[string]any)
	}
	return cur
}

func (r dgResp) errCode() string {
	e, _ := r.Body["error"].(map[string]any)
	s, _ := e["code"].(string)
	return s
}

func newDiagramServer(t *testing.T) (*httptest.Server, *diagramtest.Memory) {
	t.Helper()
	return newDiagramServerWith(t, nil)
}

// newDiagramServerWith is newDiagramServer with the chance to adjust the use
// cases and the handler's dependencies (a limiter, notifications...).
func newDiagramServerWith(t *testing.T, tune func(*application.Diagrams, *Deps)) (*httptest.Server, *diagramtest.Memory) {
	t.Helper()
	mem := diagramtest.NewMemory()
	d := application.NewDiagrams(mem, mem)
	d.Coalesce = 0 // these tests count every save
	users := diagramtest.NewUsers()
	users.Add(1, "alice", "alice@example.com", "Alice A")
	users.Add(2, "bob", "bob@example.com", "")
	users.Add(3, "eve", "eve@example.com", "Eve")
	users.Add(4, "cara", "cara@example.com", "")
	users.Add(5, "vic", "vic@example.com", "")
	d.Users, d.Blobs = users, &httpBlobs{data: map[string][]byte{}}
	deps := Deps{
		Diagrams: d,
		UserID: func(ctx context.Context) int {
			id, _ := ctx.Value(testUser{}).(int)
			return id
		},
	}
	if tune != nil {
		tune(d, &deps)
	}
	h := NewHandler(deps)
	mux := http.NewServeMux()
	h.Routes(mux)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id, err := strconv.Atoi(r.Header.Get("X-Test-User")); err == nil {
			r = r.WithContext(context.WithValue(r.Context(), testUser{}, id))
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, mem
}

func (c dgClient) do(method, path string, body any, hdr ...string) dgResp {
	c.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			c.t.Fatal(err)
		}
		rd = strings.NewReader(string(raw))
	}
	req, err := http.NewRequest(method, c.srv.URL+DiagramPrefix+path, rd)
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.user != 0 {
		req.Header.Set("X-Test-User", strconv.Itoa(c.user))
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := dgResp{Code: res.StatusCode, Header: res.Header}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out.Body); err != nil {
			c.t.Fatalf("%s %s: body is not a JSON object: %s", method, path, raw)
		}
	}
	return out
}

func (c dgClient) expect(r dgResp, code int) dgResp {
	c.t.Helper()
	if r.Code != code {
		c.t.Fatalf("status = %d, want %d; body %v", r.Code, code, r.Body)
	}
	return r
}

func TestDiagramHTTPWireFormat(t *testing.T) {
	srv, _ := newDiagramServer(t)
	alice := dgClient{t, srv, 1}
	bob := dgClient{t, srv, 2}
	anon := dgClient{t, srv, 0}

	// the user must be signed in
	r := anon.do("GET", "/projects", nil)
	if r.Code != 401 || r.errCode() != "UNAUTHENTICATED" {
		t.Errorf("anonymous: %d %v", r.Code, r.Body)
	}
	if r.obj("error")["message"] != "Authentication required" {
		t.Errorf("message = %v", r.obj("error")["message"])
	}

	// create: a bare {project, file}, ETag, Location, camelCase, ms epoch, string ids
	created := alice.expect(alice.do("POST", "/projects", map[string]any{"name": "Payments"}), 201)
	project, file := created.obj("project"), created.obj("file")
	for _, k := range []string{"id", "name", "role", "treeRev", "createdAt", "updatedAt", "updatedBy", "fileCount", "folderCount", "thumbnailUrl", "ownerId"} {
		if _, ok := project[k]; !ok {
			t.Errorf("project lacks %q: %v", k, project)
		}
	}
	if project["name"] != "Payments" || project["role"] != "owner" || project["ownerId"] != "1" || project["updatedBy"] != "1" ||
		project["fileCount"] != float64(1) || project["folderCount"] != float64(0) {
		t.Errorf("project = %v", project)
	}
	if at, _ := project["createdAt"].(float64); at < 1e12 || at > 1e14 {
		t.Errorf("createdAt = %v, want epoch milliseconds", project["createdAt"])
	}
	pid, fid := project["id"].(string), file["id"].(string)
	if !strings.HasPrefix(pid, "prj_") || !strings.HasPrefix(fid, "fil_") {
		t.Errorf("ids = %q %q", pid, fid)
	}
	if created.Header.Get("ETag") != `"1"` || created.Header.Get("Location") != "/api/diagrams/projects/"+pid {
		t.Errorf("headers = ETag %q, Location %q", created.Header.Get("ETag"), created.Header.Get("Location"))
	}
	if file["folderId"] != nil || file["kind"] != "sdd" || file["version"] != float64(1) || file["role"] != "owner" || file["projectId"] != pid || file["pageCount"] != float64(1) {
		t.Errorf("file = %v", file)
	}
	data := file["data"].(map[string]any)
	if data["id"] != fid || data["name"] != "Payments" || data["version"] != float64(2) {
		t.Errorf("document = %v", data)
	}

	// get / ETag
	got := alice.expect(alice.do("GET", "/files/"+fid, nil), 200)
	if got.Header.Get("ETag") != `"1"` || got.obj("file")["data"] == nil {
		t.Errorf("get file: ETag %q", got.Header.Get("ETag"))
	}

	// save: If-Match required, 412 with the current ETag, then success
	r = alice.do("PUT", "/files/"+fid, map[string]any{"name": "v2"})
	details, _ := r.obj("error")["details"].(map[string]any)
	if r.Code != 412 || r.errCode() != "IF_MATCH_REQUIRED" || details["currentVersion"] != float64(1) {
		t.Errorf("PUT without If-Match: %d %v", r.Code, r.Body)
	}
	doc := map[string]any{"version": 2, "activePageId": "pg", "pages": []any{map[string]any{"id": "pg", "name": "P", "edges": []any{}, "nodes": []any{map[string]any{"id": "z", "type": "sticky"}}}}}
	saved := alice.expect(alice.do("PUT", "/files/"+fid, map[string]any{"name": "Payments v2", "data": doc}, "If-Match", `"1"`), 200)
	if saved.Header.Get("ETag") != `"2"` || saved.obj("file")["version"] != float64(2) || saved.obj("file")["kind"] != "board" {
		t.Errorf("saved = %v, ETag %q", saved.obj("file"), saved.Header.Get("ETag"))
	}
	if saved.obj("file", "data")["name"] != "Payments v2" {
		t.Errorf("document name = %v", saved.obj("file", "data")["name"])
	}
	stale := alice.do("PUT", "/files/"+fid, map[string]any{"name": "stale"}, "If-Match", `"1"`)
	details, _ = stale.obj("error")["details"].(map[string]any)
	if stale.Code != 412 || stale.errCode() != "VERSION_CONFLICT" || details["currentVersion"] != float64(2) || stale.Header.Get("ETag") != `"2"` {
		t.Errorf("stale: %d %v ETag %q", stale.Code, stale.Body, stale.Header.Get("ETag"))
	}
	if r := alice.do("PUT", "/files/"+fid, map[string]any{}, "If-Match", `"2"`); r.Code != 400 || r.errCode() != "VALIDATION_ERROR" {
		t.Errorf("empty PUT: %d %v", r.Code, r.Body)
	}

	// tree
	dir := alice.expect(alice.do("POST", "/projects/"+pid+"/folders", map[string]any{"name": "docs"}), 201).obj("folder")
	if dir["parentId"] != nil || dir["projectId"] != pid || dir["position"] == nil {
		t.Errorf("folder = %v", dir)
	}
	nf := alice.expect(alice.do("POST", "/projects/"+pid+"/files", map[string]any{"name": "inside", "folderId": dir["id"]}), 201)
	if nf.obj("file")["folderId"] != dir["id"] || nf.Header.Get("ETag") != `"1"` {
		t.Errorf("created file = %v", nf.obj("file"))
	}
	tree := alice.expect(alice.do("GET", "/projects/"+pid+"/tree", nil), 200)
	files, _ := tree.Body["files"].([]any)
	if tree.Body["treeRev"] != float64(4) || len(files) != 2 || len(tree.Body["folders"].([]any)) != 1 || tree.obj("project")["id"] != pid {
		t.Errorf("tree = %v", tree.Body)
	}
	if _, has := files[0].(map[string]any)["data"]; has {
		t.Error("the tree must not carry documents")
	}

	// folder cycle, patch with null, delete counts
	child := alice.expect(alice.do("POST", "/projects/"+pid+"/folders", map[string]any{"name": "sub", "parentId": dir["id"]}), 201).obj("folder")
	if r := alice.do("PATCH", "/folders/"+dir["id"].(string), map[string]any{"parentId": child["id"]}); r.Code != 409 || r.errCode() != "CYCLE" {
		t.Errorf("cycle: %d %v", r.Code, r.Body)
	}
	alice.expect(alice.do("PATCH", "/folders/"+child["id"].(string), map[string]any{"parentId": nil}), 200)
	moved := alice.expect(alice.do("PATCH", "/files/"+nf.obj("file")["id"].(string), map[string]any{"folderId": nil, "name": "outside"}), 200).obj("file")
	if moved["folderId"] != nil || moved["name"] != "outside" {
		t.Errorf("moved = %v", moved)
	}
	if _, has := moved["data"]; has {
		t.Error("PATCH /files answers without the document")
	}
	del := alice.expect(alice.do("DELETE", "/folders/"+dir["id"].(string), nil), 200)
	if del.Body["deletedFolders"] != float64(1) || del.Body["deletedFiles"] != float64(0) {
		t.Errorf("delete folder = %v", del.Body)
	}

	// duplicates
	dup := alice.expect(alice.do("POST", "/files/"+fid+"/duplicate", nil), 201)
	if dup.obj("file")["name"] != "Payments v2 (copy)" || dup.Header.Get("ETag") != `"1"` {
		t.Errorf("duplicate = %v", dup.obj("file"))
	}
	fdup := alice.expect(alice.do("POST", "/folders/"+child["id"].(string)+"/duplicate", nil), 201)
	if fdup.obj("folder")["name"] != "sub (copy)" || fdup.Body["folders"] == nil || fdup.Body["files"] == nil {
		t.Errorf("duplicate folder = %v", fdup.Body)
	}

	// versions
	list := alice.expect(alice.do("GET", "/files/"+fid+"/versions", nil), 200)
	items := list.Body["items"].([]any)
	first := items[0].(map[string]any)
	if len(items) != 2 || first["version"] != float64(2) || first["named"] != false || first["createdBy"] != "1" || first["label"] != nil {
		t.Errorf("versions = %v", items)
	}
	if _, has := first["data"]; has {
		t.Error("the version list must not carry documents")
	}
	one := alice.expect(alice.do("GET", "/files/"+fid+"/versions/1", nil), 200).obj("version")
	if one["data"] == nil || one["version"] != float64(1) {
		t.Errorf("version 1 = %v", one)
	}
	alice.expect(alice.do("GET", "/files/"+fid+"/versions/99", nil), 404)
	alice.expect(alice.do("GET", "/files/"+fid+"/versions/zero", nil), 400)
	nv := alice.expect(alice.do("POST", "/files/"+fid+"/versions", map[string]any{"name": "Release 1"}), 201).obj("version")
	if nv["version"] != float64(2) || nv["named"] != true || nv["label"] != "Release 1" {
		t.Errorf("named version = %v", nv)
	}
	if r := alice.do("POST", "/files/"+fid+"/versions", map[string]any{"name": ""}); r.Code != 400 {
		t.Errorf("empty version name: %d", r.Code)
	}
	rest := alice.expect(alice.do("POST", "/files/"+fid+"/versions/1/restore", nil), 200)
	if rest.obj("file")["version"] != float64(3) || rest.Header.Get("ETag") != `"3"` {
		t.Errorf("restore = %v", rest.obj("file"))
	}

	// access: a stranger gets 404 on everything, nothing leaks
	for _, req := range [][2]string{{"GET", "/projects/" + pid}, {"GET", "/projects/" + pid + "/tree"}, {"GET", "/files/" + fid}, {"DELETE", "/files/" + fid}, {"GET", "/files/" + fid + "/versions"}} {
		if r := bob.do(req[0], req[1], nil); r.Code != 404 || r.errCode() != "NOT_FOUND" {
			t.Errorf("stranger %s %s: %d %v", req[0], req[1], r.Code, r.Body)
		}
	}
	if r := bob.do("GET", "/projects", nil); len(r.Body["items"].([]any)) != 0 {
		t.Errorf("stranger's list = %v", r.Body)
	}

	// list: {items, total, page, pageSize}
	l := alice.expect(alice.do("GET", "/projects?pageSize=5&sort=name&order=asc&q=pay", nil), 200)
	if l.Body["total"] != float64(1) || l.Body["page"] != float64(1) || l.Body["pageSize"] != float64(5) || len(l.Body["items"].([]any)) != 1 {
		t.Errorf("list = %v", l.Body)
	}
	for _, q := range []string{"pageSize=1000", "sort=color", "page=0", "order=up"} {
		if r := alice.do("GET", "/projects?"+q, nil); r.Code != 400 || r.errCode() != "VALIDATION_ERROR" {
			t.Errorf("?%s: %d %v", q, r.Code, r.Body)
		}
	}

	// rename and delete
	ren := alice.expect(alice.do("PATCH", "/projects/"+pid, map[string]any{"name": "Platform"}), 200)
	if ren.obj("project")["name"] != "Platform" {
		t.Errorf("rename = %v", ren.Body)
	}
	alice.expect(alice.do("DELETE", "/files/"+fid, nil), 204)
	alice.expect(alice.do("GET", "/files/"+fid, nil), 404)
	alice.expect(alice.do("DELETE", "/projects/"+pid, nil), 204)
	alice.expect(alice.do("GET", "/projects/"+pid, nil), 404)
}

func TestDiagramHTTPValidationAndIDs(t *testing.T) {
	srv, _ := newDiagramServer(t)
	c := dgClient{t, srv, 1}

	if r := c.do("POST", "/projects", "{nope"); r.Code != 400 || r.errCode() != "INVALID_JSON" {
		t.Errorf("malformed json: %d %v", r.Code, r.Body)
	}
	if r := c.do("POST", "/projects", map[string]any{"name": 5}); r.Code != 400 || r.errCode() != "VALIDATION_ERROR" {
		t.Errorf("wrong type: %d %v", r.Code, r.Body)
	}
	for name, body := range map[string]any{
		"no name":    map[string]any{},
		"empty name": map[string]any{"name": ""},
		"long name":  map[string]any{"name": strings.Repeat("x", 201)},
		"bad id":     map[string]any{"id": "bad id!", "name": "x"},
		"bad data":   map[string]any{"name": "x", "data": map[string]any{"version": 1}},
	} {
		if r := c.do("POST", "/projects", body); r.Code != 400 {
			t.Errorf("%s: %d %v", name, r.Code, r.Body)
		}
	}
	if r := c.do("POST", "/projects", map[string]any{"id": "bad id!", "name": "x"}); r.errCode() != "INVALID_ID" {
		t.Errorf("bad id code = %q", r.errCode())
	}
	if r := c.do("POST", "/projects", map[string]any{"name": "a\u0007b"}); r.errCode() != "INVALID_NAME" {
		t.Errorf("control char code = %q", r.errCode())
	}
	r := c.do("GET", "/projects/"+strings.Repeat("x", 65), nil)
	if r.Code != 400 {
		t.Errorf("65 character id: %d", r.Code)
	}

	// a replayed client id answers 200, another user's attempt 409
	body := map[string]any{"id": "prj_offline01", "name": "Offline", "file": map[string]any{"id": "fil_offline01"}}
	c.expect(c.do("POST", "/projects", body), 201)
	again := c.expect(c.do("POST", "/projects", body), 200)
	if again.obj("file")["id"] != "fil_offline01" {
		t.Errorf("replay = %v", again.Body)
	}
	if r := (dgClient{t, srv, 2}).do("POST", "/projects", body); r.Code != 409 || r.errCode() != "ID_TAKEN" {
		t.Errorf("taken: %d %v", r.Code, r.Body)
	}
}

func TestDiagramHTTPRoles(t *testing.T) {
	srv, mem := newDiagramServer(t)
	alice, bob := dgClient{t, srv, 1}, dgClient{t, srv, 2}
	made := alice.expect(alice.do("POST", "/projects", map[string]any{"name": "Shared"}), 201)
	pid, fid := made.obj("project")["id"].(string), made.obj("file")["id"].(string)
	mem.AddMember(pid, 2, diagram.RoleViewer)

	if r := bob.do("GET", "/files/"+fid, nil); r.Code != 200 || r.obj("file")["role"] != "viewer" {
		t.Errorf("viewer read: %d %v", r.Code, r.Body)
	}
	for _, req := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/projects/" + pid + "/folders", map[string]any{"name": "x"}},
		{"POST", "/projects/" + pid + "/files", map[string]any{"name": "x"}},
		{"PATCH", "/files/" + fid, map[string]any{"name": "x"}},
		{"DELETE", "/files/" + fid, nil},
		{"PATCH", "/projects/" + pid, map[string]any{"name": "x"}},
		{"DELETE", "/projects/" + pid, nil},
		{"POST", "/files/" + fid + "/duplicate", nil},
		{"POST", "/files/" + fid + "/versions", map[string]any{"name": "v"}},
	} {
		if r := bob.do(req.method, req.path, req.body); r.Code != 403 || r.errCode() != "FORBIDDEN" || r.obj("error")["message"] == "" {
			t.Errorf("viewer %s %s: %d %v", req.method, req.path, r.Code, r.Body)
		}
	}
	if r := bob.do("PUT", "/files/"+fid, map[string]any{"name": "x"}, "If-Match", "1"); r.Code != 403 {
		t.Errorf("viewer save: %d", r.Code)
	}
	mem.SetOverride(fid, 2, diagram.RoleEditor)
	if r := bob.do("PUT", "/files/"+fid, map[string]any{"name": "by bob"}, "If-Match", "1"); r.Code != 200 || r.obj("file")["role"] != "editor" {
		t.Errorf("override save: %d %v", r.Code, r.Body)
	}
	if r := bob.do("GET", "/projects", nil); len(r.Body["items"].([]any)) != 1 || r.Body["items"].([]any)[0].(map[string]any)["role"] != "viewer" {
		t.Errorf("member list = %v", r.Body)
	}
}

func TestDiagramHTTPPrefs(t *testing.T) {
	srv, _ := newDiagramServer(t)
	alice, bob := dgClient{t, srv, 1}, dgClient{t, srv, 2}
	if r := (dgClient{t, srv, 0}).do("GET", "/me/prefs", nil); r.Code != 401 {
		t.Errorf("anonymous: %d", r.Code)
	}
	if r := alice.expect(alice.do("GET", "/me/prefs", nil), 200); len(r.obj("prefs")) != 0 {
		t.Errorf("fresh prefs = %v", r.Body)
	}
	alice.expect(alice.do("PATCH", "/me/prefs", map[string]any{"tabs": map[string]any{"p1": map[string]any{"active": "b"}}}), 200)
	alice.expect(alice.do("PATCH", "/me/prefs", map[string]any{"starred": []string{"a"}}), 200)
	got := alice.expect(alice.do("GET", "/me/prefs", nil), 200).obj("prefs")
	if got["tabs"] == nil || got["starred"] == nil {
		t.Errorf("prefs = %v", got)
	}
	del := alice.expect(alice.do("PATCH", "/me/prefs", map[string]any{"starred": nil}), 200).obj("prefs")
	if _, has := del["starred"]; has || del["tabs"] == nil {
		t.Errorf("after delete = %v", del)
	}
	if r := bob.do("GET", "/me/prefs", nil); len(r.obj("prefs")) != 0 {
		t.Errorf("prefs are per user: %v", r.Body)
	}
	if r := alice.do("PATCH", "/me/prefs", map[string]any{"big": strings.Repeat("x", 70*1024)}); r.Code != 413 || r.errCode() != "PAYLOAD_TOO_LARGE" {
		t.Errorf("oversize: %d %v", r.Code, r.Body)
	}
	if r := alice.do("PATCH", "/me/prefs", "[1]"); r.Code != 400 {
		t.Errorf("array body: %d", r.Code)
	}
}

func TestDiagramRoutesAreOffWithoutService(t *testing.T) {
	mux := http.NewServeMux()
	NewHandler(Deps{}).Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", DiagramPrefix+"/projects", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
