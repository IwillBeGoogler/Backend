package http

// Personal access tokens, the public API (/v1) and webhooks over HTTP:
// the wire format of the Node server, scopes and project limits on top of
// the owner's role, and the webhook endpoints' URL guard.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram/diagramtest"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/netguard"
)

type fakeQueue struct {
	mu   sync.Mutex
	jobs []string
}

func (q *fakeQueue) DeliverWebhookAt(_ context.Context, id string, attempt int, _ time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.jobs = append(q.jobs, id+"#"+strconv.Itoa(attempt))
	return nil
}

type fakeSender struct {
	mu     sync.Mutex
	status int
	got    []application.WebhookRequest
}

func (s *fakeSender) Send(_ context.Context, r application.WebhookRequest) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got = append(s.got, r)
	return s.status, nil
}

type tokenEnv struct {
	srv     *httptest.Server
	mem     *diagramtest.Memory
	sender  *fakeSender
	queue   *fakeQueue
	d       *application.Diagrams
	banned  map[int]bool
	schema  *fakeSchema
	handler *Handler
}

// newTokenServer is the diagram server behind the token middleware, with
// webhooks wired to a fake queue and sender.
func newTokenServer(t *testing.T) tokenEnv {
	t.Helper()
	mem := diagramtest.NewMemory()
	d := application.NewDiagrams(mem, mem)
	d.Coalesce = 0 // these tests count every save
	users := diagramtest.NewUsers()
	for i, n := range []string{"alice", "bob", "eve"} {
		users.Add(i+1, n, n+"@example.com", "")
	}
	d.Users, d.Blobs = users, &httpBlobs{data: map[string][]byte{}}
	env := tokenEnv{mem: mem, d: d, sender: &fakeSender{status: 200}, queue: &fakeQueue{}, banned: map[int]bool{}}
	public := func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}
	check := func(ctx context.Context, u string) error {
		_, err := netguard.CheckURL(ctx, u, false, public)
		return err
	}
	d.Hooks = application.NewWebhookDispatcher(mem, env.queue, env.sender, check)
	d.Events = d.Hooks

	env.schema = &fakeSchema{}
	h := NewHandler(Deps{Diagrams: d, MCPEnabled: true, MCPRatePerMinute: 1000, Schema: env.schema, UserID: func(ctx context.Context) int {
		id, _ := ctx.Value(testUser{}).(int)
		return id
	}})
	env.handler = h
	mux := http.NewServeMux()
	h.Routes(mux)
	active := func(_ context.Context, id int) bool { return !env.banned[id] }
	chain := TokenAuth(d, active)(mux)
	env.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id, err := strconv.Atoi(r.Header.Get("X-Test-User")); err == nil {
			r = r.WithContext(context.WithValue(r.Context(), testUser{}, id))
		}
		chain.ServeHTTP(w, r)
	}))
	t.Cleanup(env.srv.Close)
	return env
}

func (e tokenEnv) session(t *testing.T, user int) dgClient { return dgClient{t, e.srv, user} }

// bearer is a client that only sends a token.
func (e tokenEnv) bearer(t *testing.T, token string) func(method, path string, body any) dgResp {
	c := dgClient{t, e.srv, 0}
	return func(method, path string, body any) dgResp {
		return c.do(method, path, body, "Authorization", "Bearer "+token)
	}
}

func (e tokenEnv) mint(t *testing.T, c dgClient, scopes []string, extra map[string]any) string {
	t.Helper()
	body := map[string]any{"name": "ci", "scopes": scopes}
	for k, v := range extra {
		body[k] = v
	}
	r := c.expect(c.do("POST", "/tokens", body), 201)
	tok, _ := r.Body["token"].(string)
	if !strings.HasPrefix(tok, "sdd_pat_") || len(tok) != len("sdd_pat_")+32 {
		t.Fatalf("token = %q", tok)
	}
	return tok
}

func TestDiagramTokenLifecycle(t *testing.T) {
	env := newTokenServer(t)
	alice := env.session(t, 1)

	r := alice.expect(alice.do("POST", "/tokens", map[string]any{"name": " ci ", "scopes": []string{"files:read", "files:read", "projects:read"}, "expiresInDays": 30}), 201)
	secret := r.Body["token"].(string)
	item := r.obj("item")
	if item["name"] != "ci" || item["prefix"] != "sdd_pat_" || item["projectIds"] != nil || item["lastUsedAt"] != nil {
		t.Errorf("item = %v", item)
	}
	if sc, _ := item["scopes"].([]any); len(sc) != 2 {
		t.Errorf("scopes are de-duplicated: %v", item["scopes"])
	}
	exp, _ := item["expiresAt"].(float64)
	if d := time.UnixMilli(int64(exp)).Sub(time.Now()); d < 29*24*time.Hour || d > 31*24*time.Hour {
		t.Errorf("expiresAt = %v", item["expiresAt"])
	}
	if _, leaked := item["hash"]; leaked || strings.Contains(r.Header.Get("Location"), secret) {
		t.Error("the secret or hash leaked")
	}

	list := alice.expect(alice.do("GET", "/tokens", nil), 200)
	items, _ := list.Body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("list = %v", list.Body)
	}
	for k, v := range items[0].(map[string]any) {
		if s, ok := v.(string); ok && strings.Contains(s, secret) {
			t.Errorf("the list shows the secret in %q", k)
		}
	}
	if other := env.session(t, 2).expect(env.session(t, 2).do("GET", "/tokens", nil), 200); len(other.Body["items"].([]any)) != 0 {
		t.Error("another user sees the token")
	}

	// the token works, and is noted as used
	use := env.bearer(t, secret)
	alice.expect(use("GET", "/v1/projects", nil), 200)
	first := alice.expect(alice.do("GET", "/tokens", nil), 200).Body["items"].([]any)[0].(map[string]any)
	if first["lastUsedAt"] == nil {
		t.Error("lastUsedAt was not set")
	}

	// validation
	for name, body := range map[string]map[string]any{
		"no scopes":     {"name": "x", "scopes": []string{}},
		"unknown scope": {"name": "x", "scopes": []string{"admin"}},
		"no name":       {"name": " ", "scopes": []string{"files:read"}},
		"too long":      {"name": "x", "scopes": []string{"files:read"}, "expiresInDays": 4000},
		"empty project": {"name": "x", "scopes": []string{"files:read"}, "projectIds": []string{}},
	} {
		if r := alice.do("POST", "/tokens", body); r.Code != 400 {
			t.Errorf("%s: %d %v", name, r.Code, r.Body)
		}
	}
	// a project the owner cannot read cannot go in the limit
	if r := alice.do("POST", "/tokens", map[string]any{"name": "x", "scopes": []string{"files:read"}, "projectIds": []string{"prj_nope"}}); r.Code != 404 {
		t.Errorf("unknown project: %d", r.Code)
	}

	// revoke: 204, then 404, and the token stops working
	id := first["id"].(string)
	if r := env.session(t, 2).do("DELETE", "/tokens/"+id, nil); r.Code != 404 {
		t.Errorf("revoking another user's token: %d", r.Code)
	}
	alice.expect(alice.do("DELETE", "/tokens/"+id, nil), 204)
	if r := alice.do("DELETE", "/tokens/"+id, nil); r.Code != 404 {
		t.Errorf("second revoke: %d", r.Code)
	}
	if r := use("GET", "/v1/projects", nil); r.Code != 401 || r.errCode() != "INVALID_TOKEN" {
		t.Errorf("revoked token: %d %v", r.Code, r.Body)
	}
	if r := alice.do("GET", "/tokens", nil); len(r.Body["items"].([]any)) != 0 {
		t.Error("a revoked token is still listed")
	}
}

func TestDiagramTokenAuthRules(t *testing.T) {
	env := newTokenServer(t)
	alice := env.session(t, 1)
	tok := env.mint(t, alice, []string{"projects:read"}, nil)
	use := env.bearer(t, tok)

	// unknown, malformed and expired tokens
	bogus := env.bearer(t, "sdd_pat_"+strings.Repeat("A", 32))
	if r := bogus("GET", "/v1/projects", nil); r.Code != 401 || r.errCode() != "INVALID_TOKEN" {
		t.Errorf("unknown token: %d %v", r.Code, r.Body)
	}
	if r := env.bearer(t, "sdd_pat_short")("GET", "/v1/projects", nil); r.Code != 401 {
		t.Errorf("malformed token: %d", r.Code)
	}
	expired := env.mint(t, alice, []string{"projects:read"}, map[string]any{"expiresInDays": 1})
	h := diagram.HashToken(expired)
	stored, _ := env.mem.TokenByHash(context.Background(), h)
	past := time.Now().Add(-time.Minute)
	stored.ExpiresAt = &past
	env.mem.ReplaceToken(stored)
	if r := env.bearer(t, expired)("GET", "/v1/projects", nil); r.Code != 401 {
		t.Errorf("expired token: %d", r.Code)
	}

	// tokens are for /v1 and MCP only; they cannot manage tokens
	for _, c := range [][2]string{{"GET", "/projects"}, {"GET", "/tokens"}, {"POST", "/tokens"}, {"GET", "/me/prefs"}} {
		r := use(c[0], c[1], nil)
		if r.Code != 403 || r.errCode() != "TOKEN_SCOPE" {
			t.Errorf("%s %s with a token: %d %v", c[0], c[1], r.Code, r.Body)
		}
	}

	// scope
	r := use("GET", "/v1/projects/prj_x/files", nil)
	details, _ := r.obj("error")["details"].(map[string]any)
	if r.Code != 403 || r.errCode() != "TOKEN_SCOPE" || details["required"] != "files:read" {
		t.Errorf("missing scope: %d %v", r.Code, r.Body)
	}
	if r := use("PUT", "/v1/files/fil_x", map[string]any{}); r.Code != 403 || r.errCode() != "TOKEN_SCOPE" {
		t.Errorf("write without scope: %d %v", r.Code, r.Body)
	}

	// a banned owner's tokens stop working at once
	env.banned[1] = true
	if r := use("GET", "/v1/projects", nil); r.Code != 401 {
		t.Errorf("banned owner: %d", r.Code)
	}
	env.banned[1] = false

	// a session works on /v1 as well, with no scope needed
	alice.expect(alice.do("GET", "/v1/projects", nil), 200)
	// and nothing is reachable anonymously
	if r := env.session(t, 0).do("GET", "/v1/projects", nil); r.Code != 401 {
		t.Errorf("anonymous: %d", r.Code)
	}
}

func TestDiagramPublicAPI(t *testing.T) {
	env := newTokenServer(t)
	alice, bob := env.session(t, 1), env.session(t, 2)
	pa := alice.expect(alice.do("POST", "/projects", map[string]any{"name": "Alpha"}), 201)
	pb := alice.expect(alice.do("POST", "/projects", map[string]any{"name": "Beta"}), 201)
	alphaID, alphaFile := pa.obj("project")["id"].(string), pa.obj("file")["id"].(string)
	betaID, betaFile := pb.obj("project")["id"].(string), pb.obj("file")["id"].(string)
	// bob is a viewer of Alpha
	alice.expect(alice.do("POST", "/projects/"+alphaID+"/members", map[string]any{"email": "bob", "role": "viewer"}), 201)

	all := env.mint(t, alice, diagram.Scopes, nil)
	limited := env.mint(t, alice, diagram.Scopes, map[string]any{"projectIds": []string{alphaID}})
	bobTok := env.mint(t, bob, diagram.Scopes, nil)
	V := "/v1"

	r := env.bearer(t, all)("GET", V+"/projects", nil)
	if items, _ := r.Body["items"].([]any); r.Code != 200 || len(items) != 2 {
		t.Fatalf("projects = %d %v", r.Code, r.Body)
	}
	r = env.bearer(t, limited)("GET", V+"/projects", nil)
	items, _ := r.Body["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != alphaID {
		t.Errorf("the project limit shows %v", r.Body)
	}

	// files of a project, a file with its ETag
	r = env.bearer(t, all)("GET", V+"/projects/"+alphaID+"/files", nil)
	files, _ := r.Body["items"].([]any)
	if r.Code != 200 || len(files) != 1 || files[0].(map[string]any)["data"] != nil {
		t.Errorf("files = %d %v", r.Code, r.Body)
	}
	r = env.bearer(t, all)("GET", V+"/files/"+alphaFile, nil)
	if r.Code != 200 || r.Header.Get("ETag") != `"1"` || r.obj("file")["data"] == nil {
		t.Errorf("file = %d ETag %q", r.Code, r.Header.Get("ETag"))
	}

	// the project limit hides everything else, as not found
	for _, p := range []string{"/projects/" + betaID + "/files", "/files/" + betaFile, "/projects/" + betaID + "/library"} {
		if r := env.bearer(t, limited)("GET", V+p, nil); r.Code != 404 {
			t.Errorf("limited token on %s: %d", p, r.Code)
		}
	}
	if r := env.bearer(t, limited)("GET", V+"/files/"+alphaFile, nil); r.Code != 200 {
		t.Errorf("limited token on its own project: %d", r.Code)
	}

	// writes: If-Match, a labelled snapshot, and the owner's role still applies
	doc := map[string]any{"version": 2, "activePageId": "pg", "pages": []any{map[string]any{"id": "pg", "name": "P", "edges": []any{}, "nodes": []any{}}}}
	if r := env.bearer(t, all)("PUT", V+"/files/"+alphaFile, map[string]any{"data": doc}); r.Code != 412 {
		t.Errorf("PUT without If-Match: %d", r.Code)
	}
	c := dgClient{t, env.srv, 0}
	put := c.do("PUT", V+"/files/"+alphaFile, map[string]any{"data": doc}, "Authorization", "Bearer "+all, "If-Match", `"1"`)
	if put.Code != 200 || put.Header.Get("ETag") != `"2"` {
		t.Fatalf("PUT = %d %v", put.Code, put.Body)
	}
	vs := alice.expect(alice.do("GET", "/files/"+alphaFile+"/versions", nil), 200).Body["items"].([]any)
	if len(vs) == 0 || vs[0].(map[string]any)["label"] != "Saved through the API" {
		t.Errorf("versions = %v", vs)
	}
	denied := c.do("PUT", V+"/files/"+alphaFile, map[string]any{"data": doc}, "Authorization", "Bearer "+bobTok, "If-Match", `"2"`)
	if denied.Code != 403 || denied.errCode() != "FORBIDDEN" {
		t.Errorf("a viewer's token writing: %d %v", denied.Code, denied.Body)
	}
	if r := env.bearer(t, bobTok)("GET", V+"/files/"+betaFile, nil); r.Code != 404 {
		t.Errorf("a stranger's token: %d", r.Code)
	}

	// library
	r = env.bearer(t, all)("GET", V+"/projects/"+alphaID+"/library", nil)
	if r.Code != 200 || r.Body["rev"] != float64(0) || r.Body["doc"] == nil {
		t.Errorf("library = %d %v", r.Code, r.Body)
	}
}

func TestDiagramWebhookRoutes(t *testing.T) {
	env := newTokenServer(t)
	alice, bob := env.session(t, 1), env.session(t, 2)
	made := alice.expect(alice.do("POST", "/projects", map[string]any{"name": "Hooks"}), 201)
	pid, fid := made.obj("project")["id"].(string), made.obj("file")["id"].(string)
	alice.expect(alice.do("POST", "/projects/"+pid+"/members", map[string]any{"email": "bob", "role": "viewer"}), 201)
	base := "/projects/" + pid + "/webhooks"

	// the endpoint must be a public http(s) address
	for _, u := range []string{"http://localhost/x", "http://127.0.0.1:9/x", "http://169.254.169.254/latest", "ftp://example.com/x", "http://u:p@example.com/", "http://[::1]/x"} {
		r := alice.do("POST", base, map[string]any{"url": u, "events": []string{"file.updated"}})
		if r.Code != 400 || r.errCode() != "WEBHOOK_URL_REJECTED" {
			t.Errorf("%s: %d %v", u, r.Code, r.Body)
		}
	}
	if r := alice.do("POST", base, map[string]any{"url": "https://hooks.example.com/in", "events": []string{"nope"}}); r.Code != 400 {
		t.Errorf("unknown event: %d", r.Code)
	}
	if r := alice.do("POST", base, map[string]any{"url": "https://hooks.example.com/in", "events": []string{}}); r.Code != 400 {
		t.Errorf("no events: %d", r.Code)
	}

	r := alice.expect(alice.do("POST", base, map[string]any{"url": "https://hooks.example.com/in", "events": []string{"file.updated", "file.updated", "file.created"}}), 201)
	secret := r.Body["secret"].(string)
	hook := r.obj("webhook")
	wid := hook["id"].(string)
	if !strings.HasPrefix(secret, "whsec_") || !strings.HasPrefix(wid, "whk_") || hook["active"] != true || len(hook["events"].([]any)) != 2 {
		t.Errorf("created = %v secret %q", hook, secret)
	}
	if _, leaked := hook["secret"]; leaked {
		t.Error("the webhook carries its secret")
	}
	list := alice.expect(alice.do("GET", base, nil), 200)
	if strings.Contains(string(mustJSON(t, list.Body)), secret) {
		t.Error("the list shows the secret")
	}

	// viewers cannot manage webhooks
	if r := bob.do("GET", base, nil); r.Code != 403 {
		t.Errorf("viewer: %d", r.Code)
	}
	if r := env.session(t, 3).do("GET", base, nil); r.Code != 404 {
		t.Errorf("stranger: %d", r.Code)
	}

	// patch
	r = alice.expect(alice.do("PATCH", base+"/"+wid, map[string]any{"events": []string{"library.updated"}, "active": false}), 200)
	if h := r.obj("webhook"); h["active"] != false || len(h["events"].([]any)) != 1 {
		t.Errorf("patched = %v", h)
	}
	if r := alice.do("PATCH", base+"/"+wid, map[string]any{"url": "http://localhost/x"}); r.Code != 400 {
		t.Errorf("patch to a private url: %d", r.Code)
	}
	alice.expect(alice.do("PATCH", base+"/"+wid, map[string]any{"active": true, "events": []string{"file.updated"}}), 200)

	// a saved file queues a signed delivery; the log shows it
	alice.expect(alice.do("PUT", "/files/"+fid, map[string]any{"name": "Renamed"}, "If-Match", `"1"`), 200)
	if len(env.queue.jobs) != 1 {
		t.Fatalf("queued = %v", env.queue.jobs)
	}
	dl := alice.expect(alice.do("GET", base+"/"+wid+"/deliveries", nil), 200).Body["items"].([]any)
	if len(dl) != 1 {
		t.Fatalf("deliveries = %v", dl)
	}
	d0 := dl[0].(map[string]any)
	if d0["event"] != "file.updated" || d0["status"] != "pending" || d0["attempt"] != float64(0) || d0["nextAttemptAt"] == nil {
		t.Errorf("delivery = %v", d0)
	}

	// test: one signed ping, no retries, whatever the answer
	t0 := alice.expect(alice.do("POST", base+"/"+wid+"/test", nil), 200).obj("delivery")
	if t0["event"] != "ping" || t0["status"] != "ok" || t0["responseCode"] != float64(200) || t0["attempt"] != float64(1) {
		t.Errorf("ping = %v", t0)
	}
	req := env.sender.got[len(env.sender.got)-1]
	if req.Event != "ping" || !diagram.VerifySignature(secret, req.Timestamp, req.Body, req.Signature, time.Now(), 0) {
		t.Errorf("the ping is not signed with the webhook secret: %+v", req)
	}
	env.sender.status = 500
	t1 := alice.expect(alice.do("POST", base+"/"+wid+"/test", nil), 200).obj("delivery")
	if t1["status"] != "failed" || t1["nextAttemptAt"] != nil || t1["error"] != "HTTP 500" {
		t.Errorf("failed ping = %v", t1)
	}
	if n := len(env.queue.jobs); n != 1 {
		t.Errorf("a test must not be queued for retry: %d jobs", n)
	}

	// delete takes the log with it
	alice.expect(alice.do("DELETE", base+"/"+wid, nil), 204)
	if r := alice.do("GET", base+"/"+wid+"/deliveries", nil); r.Code != 404 {
		t.Errorf("deliveries of a deleted webhook: %d", r.Code)
	}
}

func TestDiagramWebhooksThroughAToken(t *testing.T) {
	env := newTokenServer(t)
	alice := env.session(t, 1)
	made := alice.expect(alice.do("POST", "/projects", map[string]any{"name": "Hooks"}), 201)
	pid := made.obj("project")["id"].(string)
	body := map[string]any{"url": "https://hooks.example.com/in", "events": []string{"file.created"}}

	without := env.bearer(t, env.mint(t, alice, []string{"files:read"}, nil))
	if r := without("POST", "/v1/projects/"+pid+"/webhooks", body); r.Code != 403 || r.errCode() != "TOKEN_SCOPE" {
		t.Errorf("without webhooks:manage: %d %v", r.Code, r.Body)
	}
	with := env.bearer(t, env.mint(t, alice, []string{"webhooks:manage"}, nil))
	if r := with("POST", "/v1/projects/"+pid+"/webhooks", body); r.Code != 201 {
		t.Errorf("with webhooks:manage: %d %v", r.Code, r.Body)
	}
	// a project the token is not limited to
	other := alice.expect(alice.do("POST", "/projects", map[string]any{"name": "Other"}), 201).obj("project")["id"].(string)
	limited := env.bearer(t, env.mint(t, alice, []string{"webhooks:manage"}, map[string]any{"projectIds": []string{pid}}))
	if r := limited("GET", "/v1/projects/"+other+"/webhooks", nil); r.Code != 404 {
		t.Errorf("limited token on another project: %d", r.Code)
	}
}

func TestDiagramPublishedList(t *testing.T) {
	srv, _ := newDiagramServer(t)
	alice, bob, anon := dgClient{t, srv, 1}, dgClient{t, srv, 2}, dgClient{t, srv, 0}
	var ids []string
	for _, name := range []string{"One", "Two", "Three"} {
		r := alice.expect(alice.do("POST", "/projects", map[string]any{"name": name}), 201)
		ids = append(ids, r.obj("file")["id"].(string))
	}
	alice.expect(alice.do("POST", "/files/"+ids[0]+"/publish", map[string]any{"visibility": "public", "title": "First"}), 200)
	alice.expect(alice.do("POST", "/files/"+ids[1]+"/publish", map[string]any{"visibility": "unlisted"}), 200)
	// ids[2] stays private; bob publishes one of his own
	other := bob.expect(bob.do("POST", "/projects", map[string]any{"name": "Bob's"}), 201)
	bob.expect(bob.do("POST", "/files/"+other.obj("file")["id"].(string)+"/publish", map[string]any{"visibility": "public"}), 200)

	if r := anon.do("GET", "/published", nil); r.Code != 401 {
		t.Errorf("anonymous: %d", r.Code)
	}
	r := alice.expect(alice.do("GET", "/published", nil), 200)
	items, _ := r.Body["items"].([]any)
	if len(items) != 2 || r.Body["total"] != float64(2) || r.Body["page"] != float64(1) || r.Body["pageSize"] != float64(20) {
		t.Fatalf("published = %v", r.Body)
	}
	got := map[string]map[string]any{}
	for _, it := range items {
		m := it.(map[string]any)
		got[m["fileId"].(string)] = m
	}
	first := got[ids[0]]
	for _, k := range []string{"fileId", "slug", "title", "visibility", "thumbnailUrl", "publishedAt"} {
		if _, ok := first[k]; !ok {
			t.Errorf("item lacks %q: %v", k, first)
		}
	}
	if first["title"] != "First" || first["visibility"] != "public" || got[ids[1]]["visibility"] != "unlisted" || got[ids[2]] != nil {
		t.Errorf("items = %v", got)
	}
	if slug, _ := first["slug"].(string); slug == "" {
		t.Errorf("slug = %v", first["slug"])
	}
	if at, _ := first["publishedAt"].(float64); at < 1e12 {
		t.Errorf("publishedAt = %v, want epoch milliseconds", first["publishedAt"])
	}
	// paging
	r = alice.expect(alice.do("GET", "/published?page=2&pageSize=1", nil), 200)
	if items, _ := r.Body["items"].([]any); len(items) != 1 || r.Body["total"] != float64(2) || r.Body["page"] != float64(2) {
		t.Errorf("page 2 = %v", r.Body)
	}
	if r := alice.do("GET", "/published?pageSize=0", nil); r.Code != 400 {
		t.Errorf("pageSize 0: %d", r.Code)
	}
	// unpublishing removes it from the picker
	alice.expect(alice.do("DELETE", "/files/"+ids[0]+"/publish", nil), 204)
	if r := alice.expect(alice.do("GET", "/published", nil), 200); r.Body["total"] != float64(1) {
		t.Errorf("after unpublish: %v", r.Body)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// limiter exposes the MCP rate limit of the test server.
func (e tokenEnv) limiter() *rateWindow { return e.handler.mcpLimit }
