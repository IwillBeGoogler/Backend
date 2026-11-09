package http

// The sharing half of the diagram API's wire format, against the in-memory
// repository: members, share links, assets and thumbnails, the library and
// private stickies.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/svgsafe"
)

type httpBlobs struct {
	mu   sync.Mutex
	data map[string][]byte
}

func (m *httpBlobs) Put(_ context.Context, key string, r io.Reader, _ int64, _ string) error {
	b, err := io.ReadAll(r)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = b
	return err
}

func (m *httpBlobs) Get(_ context.Context, key string) (application.Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.data[key]
	if !ok {
		return application.Object{}, domain.ErrNotFound
	}
	return application.Object{Body: io.NopCloser(bytes.NewReader(b)), Size: int64(len(b))}, nil
}

var (
	testPNG  = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{7}, 40)...)
	testWebP = append([]byte("RIFF\x20\x00\x00\x00WEBPVP8 "), bytes.Repeat([]byte{1}, 24)...)
)

// rawResp is a response whose body is not JSON.
type rawResp struct {
	Code   int
	Header http.Header
	Body   []byte
}

// raw sends a body as it is, with the given content type.
func (c dgClient) raw(method, path string, body []byte, contentType string, hdr ...string) rawResp {
	c.t.Helper()
	req, err := http.NewRequest(method, c.srv.URL+DiagramPrefix+path, bytes.NewReader(body))
	if err != nil {
		c.t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
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
	b, _ := io.ReadAll(res.Body)
	return rawResp{res.StatusCode, res.Header, b}
}

func (r rawResp) json() dgResp {
	out := dgResp{Code: r.Code, Header: r.Header}
	_ = json.Unmarshal(r.Body, &out.Body)
	return out
}

func multipartBody(t *testing.T, field, filename string, data []byte) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="`+field+`"; filename="`+filename+`"`)
	h.Set("Content-Type", "application/octet-stream") // the declared type is never trusted
	part, err := w.CreatePart(h)
	if err != nil {
		t.Fatal(err)
	}
	part.Write(data)
	w.Close()
	return buf.Bytes(), w.FormDataContentType()
}

func (c dgClient) upload(field string, data []byte) rawResp {
	c.t.Helper()
	body, ct := multipartBody(c.t, field, "icon.bin", data)
	return c.raw("POST", "/assets", body, ct)
}

func TestDiagramHTTPMembers(t *testing.T) {
	srv, _ := newDiagramServer(t)
	alice, bob, eve, anon := dgClient{t, srv, 1}, dgClient{t, srv, 2}, dgClient{t, srv, 3}, dgClient{t, srv, 0}
	created := alice.expect(alice.do("POST", "/projects", map[string]any{"name": "Team"}), 201)
	pid, fid := created.obj("project")["id"].(string), created.obj("file")["id"].(string)

	if r := anon.do("GET", "/projects/"+pid+"/members", nil); r.Code != 401 || r.errCode() != "UNAUTHENTICATED" {
		t.Errorf("anonymous: %d %v", r.Code, r.Body)
	}
	if r := eve.do("GET", "/projects/"+pid+"/members", nil); r.Code != 404 {
		t.Errorf("stranger: %d", r.Code)
	}

	add := alice.expect(alice.do("POST", "/projects/"+pid+"/members", map[string]any{"email": "Bob@Example.com", "role": "viewer"}), 201)
	m := add.obj("member")
	if m["userId"] != "2" || m["email"] != "bob@example.com" || m["name"] != "bob" || m["role"] != "viewer" {
		t.Errorf("member = %v", m)
	}
	if r := alice.do("POST", "/projects/"+pid+"/members", map[string]any{"username": "eve", "role": "editor"}); r.Code != 201 {
		t.Errorf("by username: %d %v", r.Code, r.Body)
	}

	list := alice.expect(alice.do("GET", "/projects/"+pid+"/members", nil), 200)
	items := list.Body["items"].([]any)
	first := items[0].(map[string]any)
	if len(items) != 3 || first["userId"] != "1" || first["role"] != "owner" || first["isOriginalOwner"] != true || first["name"] != "Alice A" ||
		first["email"] != "alice@example.com" || first["createdAt"].(float64) < 1e12 {
		t.Errorf("members = %v", items)
	}
	if items[1].(map[string]any)["isOriginalOwner"] != false {
		t.Errorf("member = %v", items[1])
	}

	for name, c := range map[string]struct {
		body any
		code int
		err  string
	}{
		"unknown":    {map[string]any{"email": "ghost@example.com", "role": "viewer"}, 404, "USER_NOT_FOUND"},
		"owner":      {map[string]any{"email": "alice@example.com", "role": "viewer"}, 400, "OWNER_IMMUTABLE"},
		"bad role":   {map[string]any{"email": "bob", "role": "owner"}, 400, "VALIDATION_ERROR"},
		"admin":      {map[string]any{"email": "bob", "role": "admin"}, 400, "VALIDATION_ERROR"},
		"no email":   {map[string]any{"role": "viewer"}, 400, "VALIDATION_ERROR"},
		"bad json":   {"{nope", 400, "INVALID_JSON"},
		"wrong type": {map[string]any{"email": 5, "role": "viewer"}, 400, "VALIDATION_ERROR"},
	} {
		if r := alice.do("POST", "/projects/"+pid+"/members", c.body); r.Code != c.code || r.errCode() != c.err {
			t.Errorf("%s: %d %v", name, r.Code, r.Body)
		}
	}
	if r := bob.do("POST", "/projects/"+pid+"/members", map[string]any{"email": "eve", "role": "viewer"}); r.Code != 403 || r.errCode() != "FORBIDDEN" {
		t.Errorf("viewer invite: %d %v", r.Code, r.Body)
	}

	patched := alice.expect(alice.do("PATCH", "/projects/"+pid+"/members/2", map[string]any{"role": "editor"}), 200)
	if m := patched.obj("member"); m["userId"] != "2" || m["role"] != "editor" {
		t.Errorf("patched = %v", m)
	}
	if r := alice.do("PATCH", "/projects/"+pid+"/members/1", map[string]any{"role": "viewer"}); r.Code != 400 || r.errCode() != "OWNER_IMMUTABLE" {
		t.Errorf("owner: %d %v", r.Code, r.Body)
	}
	for _, id := range []string{"99", "abc", "0", "-1"} {
		if r := alice.do("PATCH", "/projects/"+pid+"/members/"+id, map[string]any{"role": "viewer"}); r.Code != 404 {
			t.Errorf("member %s: %d", id, r.Code)
		}
	}

	// per-file overrides
	if r := alice.do("PUT", "/files/"+fid+"/members/2", map[string]any{"role": "viewer"}); r.Code != 200 || r.obj("member")["role"] != "viewer" {
		t.Errorf("override: %d %v", r.Code, r.Body)
	}
	ov := alice.expect(alice.do("GET", "/files/"+fid+"/members", nil), 200).Body["items"].([]any)
	if len(ov) != 1 || ov[0].(map[string]any)["userId"] != "2" || ov[0].(map[string]any)["email"] != "bob@example.com" {
		t.Errorf("overrides = %v", ov)
	}
	if r := bob.do("GET", "/files/"+fid+"/members", nil); r.Code != 403 {
		t.Errorf("member list: %d", r.Code)
	}
	if r := alice.do("PUT", "/files/"+fid+"/members/1", map[string]any{"role": "viewer"}); r.Code != 400 || r.errCode() != "OWNER_IMMUTABLE" {
		t.Errorf("owner override: %d %v", r.Code, r.Body)
	}
	if r := alice.do("PUT", "/files/"+fid+"/members/3", map[string]any{"role": "viewer"}); r.Code != 200 {
		t.Errorf("eve is an editor member: %d", r.Code)
	}
	if r := alice.do("DELETE", "/files/"+fid+"/members/2", nil); r.Code != 204 {
		t.Errorf("remove override: %d", r.Code)
	}
	if r := alice.do("DELETE", "/files/"+fid+"/members/2", nil); r.Code != 404 {
		t.Errorf("remove again: %d", r.Code)
	}

	// removal: only owners remove others; a member can leave
	if r := bob.do("DELETE", "/projects/"+pid+"/members/3", nil); r.Code != 403 || r.errCode() != "FORBIDDEN" {
		t.Errorf("remove another: %d %v", r.Code, r.Body)
	}
	if r := bob.do("DELETE", "/projects/"+pid+"/members/2", nil); r.Code != 204 || len(r.Body) != 0 {
		t.Errorf("leave: %d", r.Code)
	}
	if r := bob.do("GET", "/projects/"+pid, nil); r.Code != 404 {
		t.Errorf("after leaving: %d", r.Code)
	}
	if r := alice.do("DELETE", "/projects/"+pid+"/members/1", nil); r.Code != 400 || r.errCode() != "OWNER_IMMUTABLE" {
		t.Errorf("remove owner: %d %v", r.Code, r.Body)
	}
	if r := alice.do("DELETE", "/projects/"+pid+"/members/2", nil); r.Code != 404 {
		t.Errorf("remove non-member: %d", r.Code)
	}
}

func TestDiagramHTTPShareLinks(t *testing.T) {
	srv, _ := newDiagramServer(t)
	alice, bob, anon := dgClient{t, srv, 1}, dgClient{t, srv, 2}, dgClient{t, srv, 0}
	created := alice.expect(alice.do("POST", "/projects", map[string]any{"name": "Shared"}), 201)
	pid, fid := created.obj("project")["id"].(string), created.obj("file")["id"].(string)
	second := alice.expect(alice.do("POST", "/projects/"+pid+"/files", map[string]any{"name": "Second"}), 201).obj("file")["id"].(string)

	made := alice.expect(alice.do("POST", "/projects/"+pid+"/shares", map[string]any{"role": "viewer", "fileId": fid, "expiresInSeconds": 3600}), 201)
	token := made.Body["token"].(string)
	sh := made.obj("share")
	if len(token) != 22 || made.Body["url"] != "/api/diagrams/share/"+token || sh["role"] != "viewer" || sh["fileId"] != fid ||
		sh["revokedAt"] != nil || sh["createdBy"] != "1" || !strings.HasPrefix(sh["id"].(string), "shr_") || sh["expiresAt"].(float64) < 1e12 {
		t.Errorf("created = %v", made.Body)
	}
	list := alice.expect(alice.do("GET", "/projects/"+pid+"/shares", nil), 200)
	if b, _ := json.Marshal(list.Body); strings.Contains(string(b), token) || len(list.Body["items"].([]any)) != 1 {
		t.Errorf("the list must not carry the token: %s", b)
	}

	// viewer file link, anonymous
	open := anon.expect(anon.do("GET", "/share/"+token, nil), 200)
	if open.Header.Get("ETag") != `"1"` {
		t.Errorf("ETag = %q", open.Header.Get("ETag"))
	}
	if s := open.obj("share"); s["scope"] != "file" || s["role"] != "viewer" || s["fileId"] != fid {
		t.Errorf("share = %v", s)
	}
	proj := open.obj("project")
	if _, has := proj["ownerId"]; has || proj["id"] != pid || proj["role"] != "viewer" || proj["name"] != "Shared" {
		t.Errorf("project = %v", proj)
	}
	if f := open.obj("file"); f["id"] != fid || f["role"] != "viewer" || f["data"] == nil {
		t.Errorf("file = %v", f)
	}
	if r := anon.do("PUT", "/share/"+token, map[string]any{"name": "hack"}, "If-Match", `"1"`); r.Code != 403 || r.errCode() != "FORBIDDEN" ||
		r.obj("error")["message"] != "This share link is read-only" {
		t.Errorf("viewer write: %d %v", r.Code, r.Body)
	}
	if r := anon.do("GET", "/share/"+token+"/files/"+second, nil); r.Code != 404 {
		t.Errorf("other file: %d", r.Code)
	}

	// X-Share-Token on the file route, read only
	if r := anon.do("GET", "/files/"+fid, nil, "X-Share-Token", token); r.Code != 200 || r.obj("file")["role"] != "viewer" {
		t.Errorf("file by token: %d %v", r.Code, r.Body)
	}
	if r := anon.do("GET", "/files/"+second, nil, "X-Share-Token", token); r.Code != 404 {
		t.Errorf("other file by token: %d", r.Code)
	}
	if r := anon.do("GET", "/files/"+fid, nil); r.Code != 401 {
		t.Errorf("no session, no token: %d", r.Code)
	}
	if r := anon.do("PUT", "/files/"+fid, map[string]any{"name": "x"}, "X-Share-Token", token, "If-Match", `"1"`); r.Code != 401 {
		t.Errorf("save by header: %d", r.Code)
	}
	if r := anon.do("GET", "/projects/"+pid+"/tree", nil, "X-Share-Token", token); r.Code != 401 {
		t.Errorf("tree by header: %d", r.Code)
	}

	// editor link: saves with If-Match
	ed := alice.expect(alice.do("POST", "/projects/"+pid+"/shares", map[string]any{"role": "editor", "fileId": fid}), 201).Body["token"].(string)
	if r := anon.do("PUT", "/share/"+ed, map[string]any{"name": "x"}); r.Code != 412 || r.errCode() != "IF_MATCH_REQUIRED" {
		t.Errorf("no If-Match: %d %v", r.Code, r.Body)
	}
	w := anon.expect(anon.do("PUT", "/share/"+ed, map[string]any{"name": "Renamed"}, "If-Match", `"1"`), 200)
	if w.Header.Get("ETag") != `"2"` || w.obj("file")["version"] != float64(2) || w.obj("file")["role"] != "editor" {
		t.Errorf("saved = %v ETag %q", w.obj("file"), w.Header.Get("ETag"))
	}
	if r := anon.do("PUT", "/share/"+ed, map[string]any{"name": "again"}, "If-Match", `"1"`); r.Code != 412 || r.errCode() != "VERSION_CONFLICT" ||
		r.Header.Get("ETag") != `"2"` {
		t.Errorf("stale: %d %v", r.Code, r.Body)
	}

	// project link: tree, any file, save by file id
	pt := alice.expect(alice.do("POST", "/projects/"+pid+"/shares", map[string]any{"role": "editor"}), 201).Body["token"].(string)
	root := anon.expect(anon.do("GET", "/share/"+pt, nil), 200)
	if s := root.obj("share"); s["scope"] != "project" || s["fileId"] != nil {
		t.Errorf("share = %v", s)
	}
	files := root.Body["files"].([]any)
	if len(files) != 2 || root.Body["treeRev"] == nil || root.Body["folders"] == nil || files[0].(map[string]any)["data"] != nil || files[0].(map[string]any)["role"] != "editor" {
		t.Errorf("tree = %v", root.Body)
	}
	if r := anon.do("PUT", "/share/"+pt+"/files/"+second, map[string]any{"name": "S2"}, "If-Match", `"1"`); r.Code != 200 || r.obj("file")["name"] != "S2" {
		t.Errorf("project link save: %d %v", r.Code, r.Body)
	}
	if r := anon.do("PUT", "/share/"+pt, map[string]any{"name": "x"}, "If-Match", `"1"`); r.Code != 404 {
		t.Errorf("project link without a file: %d", r.Code)
	}
	if r := anon.do("GET", "/projects/"+pid+"/library", nil, "X-Share-Token", pt); r.Code != 200 || r.Body["rev"] != float64(0) {
		t.Errorf("library by project link: %d %v", r.Code, r.Body)
	}

	// bad tokens
	if r := anon.do("GET", "/share/short", nil); r.Code != 400 || r.errCode() != "VALIDATION_ERROR" {
		t.Errorf("malformed: %d %v", r.Code, r.Body)
	}
	if r := anon.do("GET", "/share/"+strings.Repeat("A", 22), nil); r.Code != 404 || r.errCode() != "NOT_FOUND" {
		t.Errorf("unknown: %d %v", r.Code, r.Body)
	}

	// revoke and permissions
	if r := bob.do("GET", "/projects/"+pid+"/shares", nil); r.Code != 404 {
		t.Errorf("stranger list: %d", r.Code)
	}
	id := made.obj("share")["id"].(string)
	if r := alice.do("DELETE", "/projects/"+pid+"/shares/"+id, nil); r.Code != 204 {
		t.Errorf("revoke: %d", r.Code)
	}
	if r := anon.do("GET", "/share/"+token, nil); r.Code != 404 {
		t.Errorf("revoked: %d", r.Code)
	}
	if r := alice.do("DELETE", "/projects/"+pid+"/shares/shr_nope", nil); r.Code != 404 {
		t.Errorf("missing: %d", r.Code)
	}

	// validation
	for name, body := range map[string]any{
		"owner role": map[string]any{"role": "owner"},
		"too short":  map[string]any{"role": "viewer", "expiresInSeconds": 5},
		"negative":   map[string]any{"role": "viewer", "expiresInSeconds": -5},
		"fractional": map[string]any{"role": "viewer", "expiresInSeconds": 60.5},
		"foreign":    map[string]any{"role": "viewer", "fileId": "fil_nothere"},
	} {
		if r := alice.do("POST", "/projects/"+pid+"/shares", body); r.Code != 400 {
			t.Errorf("%s: %d %v", name, r.Code, r.Body)
		}
	}
	if r := alice.do("POST", "/projects/"+pid+"/shares", map[string]any{"role": "viewer", "fileId": "fil_nothere"}); r.errCode() != "FILE_NOT_FOUND" {
		t.Errorf("foreign file code = %q", r.errCode())
	}
}

func TestDiagramHTTPShareExpiry(t *testing.T) {
	srv, mem := newDiagramServer(t)
	alice, anon := dgClient{t, srv, 1}, dgClient{t, srv, 0}
	pid := alice.expect(alice.do("POST", "/projects", map[string]any{"name": "P"}), 201).obj("project")["id"].(string)
	token := alice.expect(alice.do("POST", "/projects/"+pid+"/shares", map[string]any{"role": "viewer", "expiresInSeconds": 60}), 201).Body["token"].(string)
	if r := anon.do("GET", "/share/"+token, nil); r.Code != 200 {
		t.Fatalf("fresh: %d", r.Code)
	}
	mem.ExpireShares()
	r := anon.do("GET", "/share/"+token, nil)
	if r.Code != 410 || r.errCode() != "SHARE_EXPIRED" || r.obj("error")["message"] != "This share link has expired" {
		t.Errorf("expired: %d %v", r.Code, r.Body)
	}
}

func TestDiagramHTTPAssets(t *testing.T) {
	srv, _ := newDiagramServer(t)
	alice, anon := dgClient{t, srv, 1}, dgClient{t, srv, 0}

	if r := anon.upload("file", testPNG); r.Code != 401 {
		t.Errorf("anonymous upload: %d", r.Code)
	}
	up := alice.upload("file", testPNG)
	if up.Code != 201 {
		t.Fatalf("upload: %d %s", up.Code, up.Body)
	}
	a := up.json().obj("asset")
	hash := a["hash"].(string)
	if len(hash) != 64 || a["mime"] != "image/png" || a["size"] != float64(len(testPNG)) || a["deduped"] != false || a["url"] != "/api/diagrams/assets/"+hash {
		t.Errorf("asset = %v", a)
	}
	if again := alice.upload("file", testPNG); again.Code != 200 || again.json().obj("asset")["deduped"] != true {
		t.Errorf("duplicate upload: %d %s", again.Code, again.Body)
	}

	// public, immutable, unsniffable
	g := anon.raw("GET", "/assets/"+hash, nil, "")
	if g.Code != 200 || !bytes.Equal(g.Body, testPNG) || g.Header.Get("Content-Type") != "image/png" ||
		g.Header.Get("Cache-Control") != "public, max-age=31536000, immutable" || g.Header.Get("ETag") != `"`+hash+`"` ||
		g.Header.Get("X-Content-Type-Options") != "nosniff" || g.Header.Get("Cross-Origin-Resource-Policy") != "cross-origin" {
		t.Errorf("asset GET: %d %v", g.Code, g.Header)
	}
	if c := anon.raw("GET", "/assets/"+hash, nil, "", "If-None-Match", `"`+hash+`"`); c.Code != 304 || len(c.Body) != 0 {
		t.Errorf("conditional GET: %d", c.Code)
	}
	for _, h := range []string{strings.Repeat("a", 64), "nothex", "..%2f..%2fetc%2fpasswd"} {
		if r := anon.raw("GET", "/assets/"+h, nil, ""); r.Code != 404 {
			t.Errorf("GET %s: %d", h, r.Code)
		}
	}

	// the type comes from the bytes
	if r := alice.upload("file", testWebP); r.Code != 201 || r.json().obj("asset")["mime"] != "image/webp" {
		t.Errorf("webp: %d %s", r.Code, r.Body)
	}
	for name, data := range map[string][]byte{
		"html": []byte("<html><script>alert(1)</script></html>"),
		"gif":  []byte("GIF89a......"),
		"svg":  []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 4 4"/>`),
	} {
		if r := alice.upload("file", data); r.Code != 415 || r.json().errCode() != "UNSUPPORTED_MEDIA_TYPE" {
			t.Errorf("%s: %d %s", name, r.Code, r.Body)
		}
	}
	if r := alice.upload("other", testPNG); r.Code != 400 || r.json().errCode() != "MISSING_FILE" {
		t.Errorf("wrong field: %d %s", r.Code, r.Body)
	}
	if r := alice.raw("POST", "/assets", []byte("{}"), "application/json"); r.Code != 415 {
		t.Errorf("not multipart: %d", r.Code)
	}
	if r := alice.raw("POST", "/assets", nil, "multipart/form-data; boundary=x"); r.Code != 400 || r.json().errCode() != "MISSING_FILE" {
		t.Errorf("empty multipart: %d %s", r.Code, r.Body)
	}
	big := append(append([]byte{}, testPNG...), make([]byte, 5<<20)...)
	if r := alice.upload("file", big); r.Code != 413 || r.json().errCode() != "PAYLOAD_TOO_LARGE" {
		t.Errorf("too large: %d %s", r.Code, r.Body)
	}
}

func TestDiagramHTTPThumbnails(t *testing.T) {
	srv, mem := newDiagramServer(t)
	alice, bob, eve, anon := dgClient{t, srv, 1}, dgClient{t, srv, 2}, dgClient{t, srv, 3}, dgClient{t, srv, 0}
	created := alice.expect(alice.do("POST", "/projects", map[string]any{"name": "Thumbs"}), 201)
	pid, fid := created.obj("project")["id"].(string), created.obj("file")["id"].(string)
	mem.AddMember(pid, 2, "viewer")

	if r := alice.do("GET", "/files/"+fid+"/thumbnail", nil); r.Code != 404 {
		t.Errorf("none yet: %d", r.Code)
	}
	if f := alice.expect(alice.do("GET", "/files/"+fid, nil), 200).obj("file"); f["thumbnailUrl"] != nil {
		t.Errorf("thumbnailUrl = %v, want null", f["thumbnailUrl"])
	}
	put := alice.raw("PUT", "/files/"+fid+"/thumbnail", testPNG, "image/png")
	url, _ := put.json().Body["thumbnailUrl"].(string)
	if put.Code != 200 || !strings.HasPrefix(url, "/api/diagrams/files/"+fid+"/thumbnail?v=") || len(url) != len("/api/diagrams/files/"+fid+"/thumbnail?v=")+12 {
		t.Fatalf("put: %d %s", put.Code, put.Body)
	}

	// filled into the file and the tree, and the version stays
	f := alice.expect(alice.do("GET", "/files/"+fid, nil), 200)
	if f.obj("file")["thumbnailUrl"] != url || f.Header.Get("ETag") != `"1"` {
		t.Errorf("file = %v ETag %q", f.obj("file"), f.Header.Get("ETag"))
	}
	tree := alice.expect(alice.do("GET", "/projects/"+pid+"/tree", nil), 200)
	if files := tree.Body["files"].([]any); files[0].(map[string]any)["thumbnailUrl"] != url {
		t.Errorf("tree files = %v", files)
	}

	img := alice.raw("GET", strings.TrimPrefix(url, DiagramPrefix), nil, "")
	etag := img.Header.Get("ETag")
	if img.Code != 200 || !bytes.Equal(img.Body, testPNG) || img.Header.Get("Content-Type") != "image/png" ||
		!strings.Contains(img.Header.Get("Cache-Control"), "private") || !strings.Contains(img.Header.Get("Cache-Control"), "max-age=3600") ||
		etag == "" || img.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("GET: %d %v", img.Code, img.Header)
	}
	if c := alice.raw("GET", "/files/"+fid+"/thumbnail", nil, "", "If-None-Match", etag); c.Code != 304 {
		t.Errorf("conditional: %d", c.Code)
	}

	// who may see and set it
	if r := bob.raw("GET", "/files/"+fid+"/thumbnail", nil, ""); r.Code != 200 {
		t.Errorf("viewer GET: %d", r.Code)
	}
	if r := eve.raw("GET", "/files/"+fid+"/thumbnail", nil, ""); r.Code != 404 {
		t.Errorf("stranger GET: %d", r.Code)
	}
	if r := anon.raw("GET", "/files/"+fid+"/thumbnail", nil, ""); r.Code != 401 {
		t.Errorf("anonymous GET: %d", r.Code)
	}
	token := alice.expect(alice.do("POST", "/projects/"+pid+"/shares", map[string]any{"role": "viewer"}), 201).Body["token"].(string)
	if r := anon.raw("GET", "/files/"+fid+"/thumbnail", nil, "", "X-Share-Token", token); r.Code != 200 {
		t.Errorf("share GET: %d", r.Code)
	}
	if r := bob.raw("PUT", "/files/"+fid+"/thumbnail", testPNG, "image/png"); r.Code != 403 {
		t.Errorf("viewer PUT: %d", r.Code)
	}
	if r := eve.raw("PUT", "/files/"+fid+"/thumbnail", testPNG, "image/png"); r.Code != 404 {
		t.Errorf("stranger PUT: %d", r.Code)
	}

	// webp, and what is refused
	if r := alice.raw("PUT", "/files/"+fid+"/thumbnail", testWebP, "image/webp"); r.Code != 200 {
		t.Errorf("webp: %d %s", r.Code, r.Body)
	}
	if r := alice.raw("GET", "/files/"+fid+"/thumbnail", nil, ""); r.Header.Get("Content-Type") != "image/webp" {
		t.Errorf("webp type = %q", r.Header.Get("Content-Type"))
	}
	if r := alice.raw("PUT", "/files/"+fid+"/thumbnail", []byte("definitely not a png"), "image/png"); r.Code != 415 {
		t.Errorf("not an image: %d", r.Code)
	}
	if r := alice.raw("PUT", "/files/"+fid+"/thumbnail", nil, "image/png"); r.Code != 415 {
		t.Errorf("empty: %d", r.Code)
	}
	if r := alice.raw("PUT", "/files/"+fid+"/thumbnail", []byte("<svg xmlns='http://www.w3.org/2000/svg'/>"), "image/svg+xml"); r.Code != 415 {
		t.Errorf("svg: %d", r.Code)
	}
	if r := alice.raw("PUT", "/files/"+fid+"/thumbnail", append(append([]byte{}, testPNG...), make([]byte, 200*1024)...), "image/png"); r.Code != 413 {
		t.Errorf("over 200 KB: %d", r.Code)
	}
	if r := alice.raw("PUT", "/files/"+fid+"/thumbnail", append(append([]byte{}, testPNG...), make([]byte, 1<<20)...), "image/png"); r.Code != 413 {
		t.Errorf("far over: %d", r.Code)
	}
}

func TestDiagramHTTPLibrary(t *testing.T) {
	srv, mem := newDiagramServer(t)
	alice, bob, dana, eve, anon := dgClient{t, srv, 1}, dgClient{t, srv, 2}, dgClient{t, srv, 4}, dgClient{t, srv, 3}, dgClient{t, srv, 0}
	pid := alice.expect(alice.do("POST", "/projects", map[string]any{"name": "Lib"}), 201).obj("project")["id"].(string)
	mem.AddMember(pid, 2, "editor")
	mem.AddMember(pid, 4, "viewer")

	g := dana.expect(dana.do("GET", "/projects/"+pid+"/library", nil), 200)
	if g.Body["rev"] != float64(0) || g.obj("doc")["components"] == nil || g.obj("doc")["styles"] == nil || g.obj("doc")["tokens"] == nil {
		t.Errorf("fresh = %v", g.Body)
	}
	if r := eve.do("GET", "/projects/"+pid+"/library", nil); r.Code != 404 {
		t.Errorf("stranger: %d", r.Code)
	}
	if r := anon.do("GET", "/projects/"+pid+"/library", nil); r.Code != 401 {
		t.Errorf("anonymous: %d", r.Code)
	}

	doc := map[string]any{"components": map[string]any{}, "styles": map[string]any{"colors": map[string]any{}, "texts": map[string]any{}, "effects": map[string]any{}},
		"tokens": map[string]any{"tk": map[string]any{"id": "tk", "name": "surface", "light": "#fff", "dark": "#111"}}, "future": 1}
	saved := bob.expect(bob.do("PUT", "/projects/"+pid+"/library", map[string]any{"baseRev": 0, "doc": doc}), 200)
	if saved.Body["rev"] != float64(1) || saved.obj("doc")["future"] != float64(1) {
		t.Errorf("saved = %v", saved.Body)
	}
	if r := dana.do("PUT", "/projects/"+pid+"/library", map[string]any{"baseRev": 1, "doc": doc}); r.Code != 403 {
		t.Errorf("viewer save: %d", r.Code)
	}

	// a stale baseRev: 412 with the current {rev, doc} next to the error
	stale := alice.do("PUT", "/projects/"+pid+"/library", map[string]any{"baseRev": 0, "doc": doc})
	if stale.Code != 412 || stale.errCode() != "LIBRARY_CONFLICT" || stale.Body["rev"] != float64(1) || stale.obj("doc", "tokens", "tk") == nil {
		t.Errorf("stale: %d %v", stale.Code, stale.Body)
	}
	if _, has := stale.obj("error")["requestId"]; has {
		// the test server has no request id middleware; the field is optional
		t.Log("requestId present")
	}
	if r := alice.do("PUT", "/projects/"+pid+"/library", map[string]any{"baseRev": 1, "doc": doc}); r.Code != 200 || r.Body["rev"] != float64(2) {
		t.Errorf("next: %d %v", r.Code, r.Body)
	}

	for name, body := range map[string]any{
		"no baseRev": map[string]any{"doc": doc},
		"no doc":     map[string]any{"baseRev": 2},
		"bad doc":    map[string]any{"baseRev": 2, "doc": map[string]any{"components": map[string]any{"x": map[string]any{"id": "x"}}, "styles": doc["styles"], "tokens": map[string]any{}}},
		"negative":   map[string]any{"baseRev": -1, "doc": doc},
		"fractional": map[string]any{"baseRev": 1.5, "doc": doc},
	} {
		if r := alice.do("PUT", "/projects/"+pid+"/library", body); r.Code != 400 || r.errCode() != "VALIDATION_ERROR" {
			t.Errorf("%s: %d %v", name, r.Code, r.Body)
		}
	}
	if r := alice.do("GET", "/projects/"+pid+"/library", nil); r.Body["rev"] != float64(2) {
		t.Errorf("rev = %v, want 2", r.Body["rev"])
	}
	huge := `{"baseRev":2,"doc":{"components":{},"styles":{"colors":{},"texts":{},"effects":{}},"tokens":{},"pad":"` + strings.Repeat("x", 3<<20) + `"}}`
	if r := alice.do("PUT", "/projects/"+pid+"/library", huge); r.Code != 413 || r.errCode() != "PAYLOAD_TOO_LARGE" {
		t.Errorf("huge: %d %v", r.Code, r.Body)
	}
}

func TestDiagramHTTPPrivateStickies(t *testing.T) {
	srv, mem := newDiagramServer(t)
	alice, bob, dana, eve, anon := dgClient{t, srv, 1}, dgClient{t, srv, 2}, dgClient{t, srv, 4}, dgClient{t, srv, 3}, dgClient{t, srv, 0}
	created := alice.expect(alice.do("POST", "/projects", map[string]any{"name": "Priv"}), 201)
	pid, fid := created.obj("project")["id"].(string), created.obj("file")["id"].(string)
	mem.AddMember(pid, 2, "editor")
	mem.AddMember(pid, 4, "viewer")
	sticky := func(id string) map[string]any {
		return map[string]any{"id": id, "type": "sticky", "position": map[string]any{"x": 1, "y": 2}, "data": map[string]any{"label": "secret", "authorId": "999"}}
	}

	fresh := alice.expect(alice.do("GET", "/files/"+fid+"/private", nil), 200)
	if fresh.Header.Get("Cache-Control") != "no-store" || fresh.Body["updatedAt"] != float64(0) || len(fresh.Body["nodes"].([]any)) != 0 || len(fresh.Body["edges"].([]any)) != 0 {
		t.Errorf("fresh = %v %v", fresh.Body, fresh.Header)
	}
	put := alice.expect(alice.do("PUT", "/files/"+fid+"/private", map[string]any{"nodes": []any{sticky("p1")}, "edges": []any{map[string]any{"id": "e", "source": "a", "target": "p1"}}}), 200)
	n := put.Body["nodes"].([]any)[0].(map[string]any)
	data := n["data"].(map[string]any)
	if n["id"] != "p1" || data["private"] != true || data["authorId"] != "1" || data["label"] != "secret" ||
		put.Body["updatedAt"].(float64) < 1e12 || put.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("put = %v", put.Body)
	}
	mine := alice.expect(alice.do("GET", "/files/"+fid+"/private", nil), 200)
	if len(mine.Body["nodes"].([]any)) != 1 || len(mine.Body["edges"].([]any)) != 1 {
		t.Errorf("mine = %v", mine.Body)
	}
	// only the caller's own
	for name, c := range map[string]dgClient{"editor": bob, "viewer": dana} {
		if r := c.expect(c.do("GET", "/files/"+fid+"/private", nil), 200); len(r.Body["nodes"].([]any)) != 0 {
			t.Errorf("%s sees %v", name, r.Body)
		}
	}
	if r := eve.do("GET", "/files/"+fid+"/private", nil); r.Code != 404 {
		t.Errorf("stranger: %d", r.Code)
	}
	if r := anon.do("GET", "/files/"+fid+"/private", nil); r.Code != 401 {
		t.Errorf("anonymous: %d", r.Code)
	}
	if r := anon.do("GET", "/files/"+fid+"/private", nil, "X-Share-Token", "x"); r.Code != 401 {
		t.Errorf("a share token must not open private stickies: %d", r.Code)
	}
	if r := dana.do("PUT", "/files/"+fid+"/private", map[string]any{"nodes": []any{sticky("c")}, "edges": []any{}}); r.Code != 403 {
		t.Errorf("viewer put: %d", r.Code)
	}
	if r := bob.do("PUT", "/files/"+fid+"/private", map[string]any{"nodes": []any{sticky("b")}, "edges": []any{}}); r.Code != 200 {
		t.Errorf("editor put: %d", r.Code)
	}
	if r := alice.expect(alice.do("GET", "/files/"+fid+"/private", nil), 200); r.Body["nodes"].([]any)[0].(map[string]any)["id"] != "p1" {
		t.Errorf("alice's stickies changed: %v", r.Body)
	}

	bad := map[string]any{"nodes": []any{map[string]any{"id": "x", "type": "icon", "position": map[string]any{"x": 0, "y": 0}, "data": map[string]any{"label": "n"}}}, "edges": []any{}}
	if r := alice.do("PUT", "/files/"+fid+"/private", bad); r.Code != 400 || r.errCode() != "VALIDATION_ERROR" {
		t.Errorf("invalid: %d %v", r.Code, r.Body)
	}
	huge := `{"nodes":[` + strings.Repeat(`{"id":"a","type":"sticky","position":{"x":0,"y":0},"data":{"label":"`+strings.Repeat("x", 9000)+`"}},`, 120)
	huge = strings.TrimSuffix(huge, ",") + `],"edges":[]}`
	if r := alice.do("PUT", "/files/"+fid+"/private", huge); r.Code != 413 || r.errCode() != "PAYLOAD_TOO_LARGE" {
		t.Errorf("over 1 MB: %d %v", r.Code, r.Body)
	}
	if r := alice.do("PUT", "/files/"+fid+"/private", map[string]any{"nodes": []any{}, "edges": []any{}}); r.Code != 200 {
		t.Errorf("clear: %d", r.Code)
	}
	if r := alice.expect(alice.do("GET", "/files/"+fid+"/private", nil), 200); len(r.Body["nodes"].([]any)) != 0 || r.Body["updatedAt"] != float64(0) {
		t.Errorf("after clear = %v", r.Body)
	}
}

func TestDiagramHTTPSVGAssets(t *testing.T) {
	srv, _ := newDiagramServerWith(t, func(d *application.Diagrams, _ *Deps) {
		d.SanitizeSVG = func(b []byte) ([]byte, bool, error) {
			if !svgsafe.Looks(b) {
				return nil, false, nil
			}
			c, err := svgsafe.Sanitize(b)
			return c, true, err
		}
	})
	alice, anon := dgClient{t, srv, 1}, dgClient{t, srv, 0}

	evil := []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"><script>alert(1)</script><path d="M0 0L4 4"/></svg>`)
	up := alice.upload("file", evil)
	if up.Code != 201 || up.json().obj("asset")["mime"] != "image/svg+xml" {
		t.Fatalf("svg upload: %d %s", up.Code, up.Body)
	}
	hash := up.json().obj("asset")["hash"].(string)
	g := anon.raw("GET", "/assets/"+hash, nil, "")
	if g.Code != 200 || g.Header.Get("Content-Type") != "image/svg+xml" ||
		g.Header.Get("Content-Security-Policy") != "default-src 'none'; style-src 'unsafe-inline'; sandbox" ||
		g.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("svg GET: %d %v", g.Code, g.Header)
	}
	if b := string(g.Body); strings.Contains(b, "script") || strings.Contains(b, "onload") || !strings.Contains(b, `<path d="M0 0L4 4"/>`) {
		t.Errorf("served svg not sanitized: %s", b)
	}
	// PNG responses carry no CSP
	png := alice.upload("file", testPNG)
	if pg := anon.raw("GET", "/assets/"+png.json().obj("asset")["hash"].(string), nil, ""); pg.Header.Get("Content-Security-Policy") != "" {
		t.Errorf("png csp: %v", pg.Header)
	}
	// unsalvageable: DOCTYPE
	bad := []byte(`<!DOCTYPE svg [<!ENTITY x "y">]><svg xmlns="http://www.w3.org/2000/svg"/>`)
	if r := alice.upload("file", bad); r.Code != 415 || r.json().errCode() != "UNSUPPORTED_MEDIA_TYPE" {
		t.Errorf("doctype: %d %s", r.Code, r.Body)
	}
	// previews stay raster-only
	if r := alice.raw("PUT", "/files/x/thumbnail", evil, "image/svg+xml"); r.Code == 200 || r.Code == 201 {
		t.Errorf("svg thumbnail: %d", r.Code)
	}
}
