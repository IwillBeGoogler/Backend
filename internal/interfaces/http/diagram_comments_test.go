package http

// Comments, notifications, the commenter role, private-sticky scrubbing,
// share attribution, project thumbnails and the asset rate limit on the
// wire, against the in-memory repository.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram/diagramtest"
)

type commentFixture struct {
	srv                        *httptest.Server
	mem                        *diagramtest.Memory
	alice, bob, eve, cara, vic dgClient
	anon                       dgClient
	pid, fid                   string
}

// newCommentFixture is a project of alice with bob (editor), cara
// (commenter) and vic (viewer); eve is a stranger.
func newCommentFixture(t *testing.T, tune func(*application.Diagrams, *Deps)) commentFixture {
	t.Helper()
	srv, mem := newDiagramServerWith(t, func(d *application.Diagrams, deps *Deps) {
		d.Notices = diagramtest.NewNotices()
		if tune != nil {
			tune(d, deps)
		}
	})
	f := commentFixture{srv: srv, mem: mem, alice: dgClient{t, srv, 1}, bob: dgClient{t, srv, 2}, eve: dgClient{t, srv, 3},
		cara: dgClient{t, srv, 4}, vic: dgClient{t, srv, 5}, anon: dgClient{t, srv, 0}}
	made := f.alice.expect(f.alice.do("POST", "/projects", map[string]any{"name": "Notes"}), 201)
	f.pid, f.fid = made.obj("project")["id"].(string), made.obj("file")["id"].(string)
	for email, role := range map[string]string{"bob@example.com": "editor", "cara@example.com": "commenter", "vic@example.com": "viewer"} {
		f.alice.expect(f.alice.do("POST", "/projects/"+f.pid+"/members", map[string]any{"email": email, "role": role}), 201)
	}
	return f
}

var wirePoint = map[string]any{"type": "point", "x": 10, "y": 20}

func TestDiagramHTTPCommentsWire(t *testing.T) {
	f := newCommentFixture(t, nil)
	path := "/files/" + f.fid + "/comments"

	if r := f.anon.do("GET", path, nil); r.Code != 401 || r.errCode() != "UNAUTHENTICATED" {
		t.Errorf("anonymous: %d %v", r.Code, r.Body)
	}
	if r := f.eve.do("GET", path, nil); r.Code != 404 {
		t.Errorf("stranger read: %d", r.Code)
	}
	if r := f.eve.do("POST", path, map[string]any{"body": "hi", "anchor": wirePoint}); r.Code != 404 {
		t.Errorf("stranger write: %d", r.Code)
	}
	if r := f.vic.do("POST", path, map[string]any{"body": "hi", "anchor": wirePoint}); r.Code != 403 || r.errCode() != "FORBIDDEN" {
		t.Errorf("viewer write: %d %v", r.Code, r.Body)
	}
	if r := f.vic.do("GET", path, nil); r.Code != 200 {
		t.Errorf("viewer read: %d", r.Code)
	}

	c := f.cara.expect(f.cara.do("POST", path, map[string]any{"body": "Why is this a single instance?", "anchor": wirePoint}), 201).obj("comment")
	for _, k := range []string{"id", "fileId", "parentId", "author", "body", "anchor", "resolved", "resolvedAt", "resolvedBy", "mentions", "createdAt", "updatedAt"} {
		if _, ok := c[k]; !ok {
			t.Errorf("comment lacks %q: %v", k, c)
		}
	}
	author, _ := c["author"].(map[string]any)
	anchor, _ := c["anchor"].(map[string]any)
	if c["fileId"] != f.fid || c["parentId"] != nil || author["id"] != "4" || author["name"] != "cara" || anchor["type"] != "point" ||
		anchor["x"] != float64(10) || c["resolved"] != false || c["resolvedAt"] != nil || c["resolvedBy"] != nil ||
		len(c["mentions"].([]any)) != 0 || c["createdAt"].(float64) < 1e12 {
		t.Errorf("comment = %v", c)
	}
	node := f.bob.expect(f.bob.do("POST", path, map[string]any{"body": "Node note", "anchor": map[string]any{"type": "node", "nodeId": "a", "dx": 4, "dy": -6}}), 201).obj("comment")
	if a := node["anchor"].(map[string]any); a["nodeId"] != "a" || a["dx"] != float64(4) || a["dy"] != float64(-6) {
		t.Errorf("node anchor = %v", node["anchor"])
	}
	rep := f.bob.expect(f.bob.do("POST", path, map[string]any{"body": "Cost, for now.", "parentId": c["id"]}), 201).obj("comment")
	if rep["parentId"] != c["id"] || rep["anchor"] != nil {
		t.Errorf("reply = %v", rep)
	}

	// validation
	for name, b := range map[string]any{
		"no anchor":   map[string]any{"body": "x"},
		"empty":       map[string]any{"body": "", "anchor": wirePoint},
		"huge":        map[string]any{"body": strings.Repeat("x", 4001), "anchor": wirePoint},
		"bad anchor":  map[string]any{"body": "x", "anchor": map[string]any{"type": "point", "x": "a", "y": 1}},
		"to a reply":  map[string]any{"body": "x", "parentId": rep["id"]},
		"wrong types": map[string]any{"body": 5, "anchor": wirePoint},
	} {
		r := f.bob.do("POST", path, b)
		want := "VALIDATION_ERROR"
		switch name {
		case "no anchor":
			want = "ANCHOR_REQUIRED"
		case "to a reply":
			want = "REPLY_TO_REPLY"
		}
		if r.Code != 400 || r.errCode() != want {
			t.Errorf("%s: %d %v", name, r.Code, r.Body)
		}
	}
	if r := f.bob.do("POST", path, map[string]any{"body": "x", "parentId": "nope"}); r.Code != 404 {
		t.Errorf("unknown parent: %d", r.Code)
	}

	// list
	l := f.vic.expect(f.vic.do("GET", path, nil), 200)
	threads := l.Body["threads"].([]any)
	if len(threads) != 2 || l.Body["unread"] != float64(3) || l.Body["readAt"] != float64(0) {
		t.Errorf("list = %v", l.Body)
	}
	// comments of one millisecond order by id: look the threads up
	replies := map[any]int{}
	for _, th := range threads {
		replies[th.(map[string]any)["root"].(map[string]any)["id"]] = len(th.(map[string]any)["replies"].([]any))
	}
	if replies[c["id"]] != 1 || replies[node["id"]] != 0 || len(replies) != 2 {
		t.Errorf("threads = %v", threads)
	}
	if l := f.cara.expect(f.cara.do("GET", path, nil), 200); l.Body["unread"] != float64(2) { // not their own
		t.Errorf("cara unread = %v", l.Body["unread"])
	}

	// resolve, reopen, edit
	if r := f.vic.do("PATCH", "/comments/"+c["id"].(string), map[string]any{"resolved": true}); r.Code != 403 {
		t.Errorf("viewer resolve: %d", r.Code)
	}
	done := f.bob.expect(f.bob.do("PATCH", "/comments/"+c["id"].(string), map[string]any{"resolved": true}), 200).obj("comment")
	if rb, _ := done["resolvedBy"].(map[string]any); done["resolved"] != true || done["resolvedAt"] == nil || rb["id"] != "2" {
		t.Errorf("resolved = %v", done)
	}
	if open := f.cara.expect(f.cara.do("PATCH", "/comments/"+c["id"].(string), map[string]any{"resolved": false}), 200).obj("comment"); open["resolved"] != false || open["resolvedBy"] != nil {
		t.Errorf("reopened = %v", open)
	}
	if r := f.bob.do("PATCH", "/comments/"+rep["id"].(string), map[string]any{"resolved": true}); r.Code != 400 || r.errCode() != "RESOLVE_ROOT_ONLY" {
		t.Errorf("resolve a reply: %d %v", r.Code, r.Body)
	}
	if r := f.bob.do("PATCH", "/comments/"+c["id"].(string), map[string]any{}); r.Code != 400 {
		t.Errorf("nothing to change: %d", r.Code)
	}
	if r := f.bob.do("PATCH", "/comments/"+c["id"].(string), map[string]any{"body": "hijacked"}); r.Code != 403 {
		t.Errorf("edit by another: %d", r.Code)
	}
	if e := f.cara.expect(f.cara.do("PATCH", "/comments/"+c["id"].(string), map[string]any{"body": "edited"}), 200).obj("comment"); e["body"] != "edited" {
		t.Errorf("edited = %v", e)
	}
	if r := f.eve.do("PATCH", "/comments/"+c["id"].(string), map[string]any{"resolved": true}); r.Code != 404 {
		t.Errorf("stranger patch: %d", r.Code)
	}

	// read marks
	if r := f.bob.raw("POST", path+"/read", nil, ""); r.Code != 204 {
		t.Errorf("read: %d %s", r.Code, r.Body)
	}
	after := f.bob.expect(f.bob.do("GET", path, nil), 200)
	if after.Body["unread"] != float64(1) && after.Body["unread"] != float64(0) || after.Body["readAt"].(float64) < 1e12 {
		t.Errorf("after read = %v", after.Body)
	}
	if r := f.eve.raw("POST", path+"/read", nil, ""); r.Code != 404 {
		t.Errorf("stranger read mark: %d", r.Code)
	}

	// delete: the author or an owner; a root takes its replies
	if r := f.bob.do("DELETE", "/comments/"+c["id"].(string), nil); r.Code != 403 {
		t.Errorf("delete by another: %d", r.Code)
	}
	if r := f.eve.do("DELETE", "/comments/"+c["id"].(string), nil); r.Code != 404 {
		t.Errorf("stranger delete: %d", r.Code)
	}
	if r := f.alice.raw("DELETE", "/comments/"+c["id"].(string), nil, ""); r.Code != 204 || len(r.Body) != 0 {
		t.Errorf("owner delete: %d %q", r.Code, r.Body)
	}
	if got := fmt.Sprint(f.vic.expect(f.vic.do("GET", path, nil), 200).Body["threads"]); strings.Contains(got, "Cost, for now.") || strings.Contains(got, "edited") {
		t.Errorf("the thread survived: %s", got)
	}
	if r := f.alice.do("DELETE", "/comments/nope", nil); r.Code != 404 {
		t.Errorf("missing: %d", r.Code)
	}
}

func TestDiagramHTTPCommenterRole(t *testing.T) {
	f := newCommentFixture(t, nil)
	if r := f.cara.do("PUT", "/files/"+f.fid, map[string]any{"name": "x"}, "If-Match", `"1"`); r.Code != 403 {
		t.Errorf("commenter save: %d", r.Code)
	}
	if r := f.cara.do("GET", "/files/"+f.fid, nil); r.Code != 200 || r.obj("file")["role"] != "commenter" {
		t.Errorf("commenter read: %d %v", r.Code, r.obj("file"))
	}
	// member list, role change, per-file override and share link accept it
	ms := f.alice.expect(f.alice.do("GET", "/projects/"+f.pid+"/members", nil), 200).Body["items"].([]any)
	seen := map[string]bool{}
	for _, m := range ms {
		seen[m.(map[string]any)["role"].(string)] = true
	}
	if !seen["commenter"] {
		t.Errorf("members = %v", ms)
	}
	if r := f.alice.do("PATCH", "/projects/"+f.pid+"/members/5", map[string]any{"role": "commenter"}); r.Code != 200 || r.obj("member")["role"] != "commenter" {
		t.Errorf("member patch: %d %v", r.Code, r.Body)
	}
	if r := f.alice.do("PUT", "/files/"+f.fid+"/members/5", map[string]any{"role": "viewer"}); r.Code != 200 {
		t.Errorf("override: %d %v", r.Code, r.Body)
	}
	if r := f.vic.do("POST", "/files/"+f.fid+"/comments", map[string]any{"body": "x", "anchor": wirePoint}); r.Code != 403 {
		t.Errorf("a viewer override beats a commenter member: %d", r.Code)
	}
	if r := f.alice.do("PUT", "/files/"+f.fid+"/members/5", map[string]any{"role": "commenter"}); r.Code != 200 {
		t.Errorf("override commenter: %d %v", r.Code, r.Body)
	}
	if r := f.vic.do("POST", "/files/"+f.fid+"/comments", map[string]any{"body": "x", "anchor": wirePoint}); r.Code != 201 {
		t.Errorf("commenter override: %d %v", r.Code, r.Body)
	}
	link := f.alice.expect(f.alice.do("POST", "/projects/"+f.pid+"/shares", map[string]any{"role": "commenter", "fileId": f.fid}), 201)
	tok := link.Body["token"].(string)
	if link.obj("share")["role"] != "commenter" {
		t.Errorf("link = %v", link.Body)
	}
	if r := f.anon.do("GET", "/share/"+tok, nil); r.Code != 200 {
		t.Errorf("open: %d", r.Code)
	}
	if r := f.anon.do("PUT", "/share/"+tok, map[string]any{"name": "x"}, "If-Match", `"1"`); r.Code != 403 {
		t.Errorf("a commenter link edits: %d", r.Code)
	}
	if r := f.alice.do("POST", "/projects/"+f.pid+"/members", map[string]any{"email": "eve", "role": "admin"}); r.Code != 400 {
		t.Errorf("unknown role: %d", r.Code)
	}
}

func TestDiagramHTTPNotifications(t *testing.T) {
	f := newCommentFixture(t, nil)
	path := "/files/" + f.fid + "/comments"
	body := "ping @[Bob](user:2) @[Eve](user:3) @[Me](user:4)"
	c := f.cara.expect(f.cara.do("POST", path, map[string]any{"body": body, "anchor": wirePoint}), 201).obj("comment")
	if m := c["mentions"].([]any); len(m) != 1 || m[0] != "2" { // eve is no member, cara is the author
		t.Errorf("mentions = %v", m)
	}
	mine := f.bob.expect(f.bob.do("GET", "/notifications", nil), 200)
	items := mine.Body["items"].([]any)
	if len(items) != 1 || mine.Body["unread"] != float64(1) {
		t.Fatalf("notifications = %v", mine.Body)
	}
	n := items[0].(map[string]any)
	actor, _ := n["actor"].(map[string]any)
	for _, k := range []string{"id", "kind", "projectId", "fileId", "commentId", "actor", "text", "createdAt", "read"} {
		if _, ok := n[k]; !ok {
			t.Errorf("notification lacks %q: %v", k, n)
		}
	}
	if n["kind"] != "mention" || n["projectId"] != f.pid || n["fileId"] != f.fid || n["commentId"] != c["id"] || n["read"] != false ||
		n["text"] != "cara mentioned you in "+"Notes" || actor["id"] != "4" || actor["name"] != "cara" || n["createdAt"].(float64) < 1e12 {
		t.Errorf("notification = %v", n)
	}
	if eve := f.eve.expect(f.eve.do("GET", "/notifications", nil), 200); len(eve.Body["items"].([]any)) != 0 || eve.Body["unread"] != float64(0) {
		t.Errorf("eve = %v", eve.Body)
	}
	if cara := f.cara.expect(f.cara.do("GET", "/notifications", nil), 200); len(cara.Body["items"].([]any)) != 0 {
		t.Errorf("the author was notified: %v", cara.Body)
	}

	// a reply notifies the thread's author
	f.bob.expect(f.bob.do("POST", path, map[string]any{"body": "answer", "parentId": c["id"]}), 201)
	cara := f.cara.expect(f.cara.do("GET", "/notifications?unread=1&limit=10", nil), 200)
	ci := cara.Body["items"].([]any)
	if len(ci) != 1 || ci[0].(map[string]any)["kind"] != "reply" || ci[0].(map[string]any)["text"] != "bob replied to your comment in Notes" {
		t.Errorf("reply notification = %v", cara.Body)
	}
	for _, q := range []string{"limit=0", "limit=201", "limit=x", "unread=x"} {
		if r := f.bob.do("GET", "/notifications?"+q, nil); r.Code != 400 || r.errCode() != "VALIDATION_ERROR" {
			t.Errorf("%s: %d %v", q, r.Code, r.Body)
		}
	}
	if r := f.anon.do("GET", "/notifications", nil); r.Code != 401 {
		t.Errorf("anonymous: %d", r.Code)
	}

	// mark read: some, then all
	id := ci[0].(map[string]any)["id"].(string)
	if r := f.cara.raw("POST", "/notifications/read", []byte(`{"ids":["`+id+`"]}`), "application/json"); r.Code != 204 {
		t.Errorf("read ids: %d %s", r.Code, r.Body)
	}
	if got := f.cara.expect(f.cara.do("GET", "/notifications", nil), 200); got.Body["unread"] != float64(0) || got.Body["items"].([]any)[0].(map[string]any)["read"] != true {
		t.Errorf("after read = %v", got.Body)
	}
	if r := f.bob.raw("POST", "/notifications/read", []byte(`{"all":true}`), "application/json"); r.Code != 204 {
		t.Errorf("read all: %d", r.Code)
	}
	if got := f.bob.expect(f.bob.do("GET", "/notifications?unread=1", nil), 200); len(got.Body["items"].([]any)) != 0 || got.Body["unread"] != float64(0) {
		t.Errorf("bob after read all = %v", got.Body)
	}
	// no body: everything is marked
	if r := f.cara.raw("POST", "/notifications/read", nil, ""); r.Code != 204 {
		t.Errorf("read without body: %d %s", r.Code, r.Body)
	}
	if r := f.anon.raw("POST", "/notifications/read", []byte(`{"all":true}`), "application/json"); r.Code != 401 {
		t.Errorf("anonymous read: %d", r.Code)
	}
}

func TestDiagramHTTPPrivateStickiesNeverReachTheSharedDocument(t *testing.T) {
	f := newCommentFixture(t, nil)
	const secret = "my-secret-text"
	sticky := func(id string, author int, label string) map[string]any {
		return map[string]any{"id": id, "type": "sticky", "position": map[string]any{"x": 5, "y": 5},
			"data": map[string]any{"label": label, "private": true, "authorId": fmt.Sprint(author)}}
	}
	doc := func(nodes ...map[string]any) map[string]any {
		all := []any{map[string]any{"id": "a", "type": "service"}}
		for _, n := range nodes {
			all = append(all, n)
		}
		edges := []any{}
		if len(nodes) > 0 { // an edge to the first sticky
			edges = append(edges, map[string]any{"id": "e9", "source": "a", "target": "s1"})
		}
		return map[string]any{"version": 2, "activePageId": "pg",
			"pages": []any{map[string]any{"id": "pg", "name": "P", "nodes": all, "edges": edges}}}
	}
	// everything a reader can fetch from the shared file
	everywhere := func(c dgClient) string {
		var parts []string
		parts = append(parts, fmt.Sprint(c.expect(c.do("GET", "/files/"+f.fid, nil), 200).Body))
		vs := c.expect(c.do("GET", "/files/"+f.fid+"/versions", nil), 200).Body
		parts = append(parts, fmt.Sprint(vs))
		for _, v := range vs["items"].([]any) {
			n := int(v.(map[string]any)["version"].(float64))
			parts = append(parts, fmt.Sprint(c.expect(c.do("GET", fmt.Sprintf("/files/%s/versions/%d", f.fid, n), nil), 200).Body))
		}
		return strings.Join(parts, "\n")
	}
	check := func(when string) {
		t.Helper()
		for _, c := range []dgClient{f.alice, f.bob, f.cara, f.vic} {
			if got := everywhere(c); strings.Contains(got, secret) {
				t.Errorf("%s: user %d can read the private sticky: %s", when, c.user, got)
			}
		}
	}

	// PUT /files/{id}
	put := f.alice.expect(f.alice.do("PUT", "/files/"+f.fid, map[string]any{"data": doc(sticky("s1", 1, secret))}, "If-Match", `"1"`), 200)
	if strings.Contains(fmt.Sprint(put.Body), secret) {
		t.Errorf("the save response holds it: %v", put.Body)
	}
	check("PUT /files/{id}")
	mine := f.alice.expect(f.alice.do("GET", "/files/"+f.fid+"/private", nil), 200)
	if !strings.Contains(fmt.Sprint(mine.Body), secret) || !strings.Contains(fmt.Sprint(mine.Body), "e9") {
		t.Errorf("alice lost her sticky: %v", mine.Body)
	}
	if got := f.bob.expect(f.bob.do("GET", "/files/"+f.fid+"/private", nil), 200); len(got.Body["nodes"].([]any)) != 0 {
		t.Errorf("bob reads alice's stickies: %v", got.Body)
	}

	// an editor's own sticky lands in their slot
	f.bob.expect(f.bob.do("PUT", "/files/"+f.fid, map[string]any{"data": doc(sticky("s1", 2, "bob "+secret))}, "If-Match", `"2"`), 200)
	check("editor save")
	if got := f.bob.expect(f.bob.do("GET", "/files/"+f.fid+"/private", nil), 200); !strings.Contains(fmt.Sprint(got.Body), "bob "+secret) {
		t.Errorf("bob's sticky: %v", got.Body)
	}

	// PUT /share/{token}/files/{fileId}: scrubbed, and attributed to the link
	link := f.alice.expect(f.alice.do("POST", "/projects/"+f.pid+"/shares", map[string]any{"role": "editor"}), 201)
	tok, shareID := link.Body["token"].(string), link.obj("share")["id"].(string)
	saved := f.anon.expect(f.anon.do("PUT", "/share/"+tok+"/files/"+f.fid, map[string]any{"data": doc(sticky("s1", 999, "stranger "+secret))}, "If-Match", `"3"`), 200)
	if strings.Contains(fmt.Sprint(saved.Body), secret) {
		t.Errorf("the share save response holds it: %v", saved.Body)
	}
	if saved.obj("file")["updatedBy"] != "share:"+shareID {
		t.Errorf("updatedBy = %v, want share:%s", saved.obj("file")["updatedBy"], shareID)
	}
	check("share save")
	if got := f.alice.expect(f.alice.do("GET", "/files/"+f.fid+"/private", nil), 200); !strings.Contains(fmt.Sprint(got.Body), "stranger "+secret) {
		t.Errorf("an unknown author's sticky goes to the project owner: %v", got.Body)
	}
	if shared := f.anon.expect(f.anon.do("GET", "/share/"+tok+"/files/"+f.fid, nil), 200); strings.Contains(fmt.Sprint(shared.Body), secret) {
		t.Errorf("the share view holds it: %v", shared.Body)
	}
	// the version list attributes it too
	vs := f.alice.expect(f.alice.do("GET", "/files/"+f.fid+"/versions", nil), 200).Body["items"].([]any)
	if vs[0].(map[string]any)["createdBy"] != "share:"+shareID || vs[1].(map[string]any)["createdBy"] != "2" {
		t.Errorf("versions = %v", vs[:2])
	}
	if got := f.alice.expect(f.alice.do("GET", "/files/"+f.fid, nil), 200).obj("file"); got["updatedBy"] != "share:"+shareID {
		t.Errorf("file updatedBy = %v", got["updatedBy"])
	}

	// restore: the version a legacy snapshot would have
	legacy := doc(sticky("s1", 1, secret))
	legacyJSON, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cur, _ := f.mem.File(ctx, f.fid)
	prep, err := diagram.Prepare(legacyJSON, cur.ID, cur.Name, cur.CreatedAt, cur.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	next := cur
	next.Version, next.Data = cur.Version+1, prep.Data
	if ok, err := f.mem.SaveFile(ctx, next, cur.Version, diagram.Version{Version: next.Version, Name: cur.Name, CreatedAt: cur.UpdatedAt, Data: prep.Data}); err != nil || !ok {
		t.Fatal(ok, err)
	}
	f.alice.expect(f.alice.do("PUT", "/files/"+f.fid, map[string]any{"data": doc()}, "If-Match", fmt.Sprintf(`"%d"`, next.Version)), 200)
	r := f.alice.expect(f.alice.do("POST", fmt.Sprintf("/files/%s/versions/%d/restore", f.fid, next.Version), nil), 200)
	if strings.Contains(fmt.Sprint(r.Body), secret) {
		t.Errorf("restore response: %v", r.Body)
	}
	// the legacy snapshot stays in history as it was written (a boot-time job
	// would clean those); the new head and its snapshot are clean
	head := f.alice.expect(f.alice.do("GET", "/files/"+f.fid, nil), 200)
	snap := f.alice.expect(f.alice.do("GET", fmt.Sprintf("/files/%s/versions/%.0f", f.fid, r.obj("file")["version"]), nil), 200)
	if strings.Contains(fmt.Sprint(head.Body, snap.Body), secret) {
		t.Errorf("after restore: %v %v", head.Body, snap.Body)
	}
	if got := f.alice.expect(f.alice.do("GET", "/files/"+f.fid+"/private", nil), 200); !strings.Contains(fmt.Sprint(got.Body), secret) {
		t.Errorf("the restored sticky went to its author: %v", got.Body)
	}
}

func TestDiagramHTTPProjectsHaveThumbnails(t *testing.T) {
	f := newCommentFixture(t, nil)
	list := func() map[string]any {
		return f.alice.expect(f.alice.do("GET", "/projects", nil), 200).Body["items"].([]any)[0].(map[string]any)
	}
	if list()["thumbnailUrl"] != nil {
		t.Errorf("no preview yet: %v", list()["thumbnailUrl"])
	}
	put := f.alice.raw("PUT", "/files/"+f.fid+"/thumbnail", testPNG, "image/png")
	url, _ := put.json().Body["thumbnailUrl"].(string)
	if put.Code != 200 || url == "" {
		t.Fatalf("thumbnail: %d %s", put.Code, put.Body)
	}
	if got := list()["thumbnailUrl"]; got != url {
		t.Errorf("thumbnailUrl = %v, want %s", got, url)
	}
	if got := f.alice.expect(f.alice.do("GET", "/projects/"+f.pid, nil), 200).obj("project")["thumbnailUrl"]; got != url {
		t.Errorf("project thumbnailUrl = %v", got)
	}
	// the first file by position wins
	f.alice.expect(f.alice.do("POST", "/projects/"+f.pid+"/files", map[string]any{"name": "Zero", "position": -5}), 201)
	if got := list()["thumbnailUrl"]; got != nil {
		t.Errorf("the first file has no preview: %v", got)
	}
}

func TestDiagramHTTPAssetUploadsAreRateLimited(t *testing.T) {
	var mu sync.Mutex
	hits := map[string]int{}
	f := newCommentFixture(t, func(_ *application.Diagrams, deps *Deps) {
		deps.Limit = func(_ context.Context, key string, n int, window time.Duration) (bool, time.Duration) {
			mu.Lock()
			defer mu.Unlock()
			if !strings.HasPrefix(key, "dg-asset:") {
				return true, 0 // other limited writes (the fixture's invites) are not under test
			}
			hits[key]++
			if n != assetUploadsPerMinute || window != time.Minute {
				t.Errorf("limit %d per %v", n, window)
			}
			return hits[key] <= 2, 7 * time.Second
		}
	})
	for i := 0; i < 2; i++ {
		if r := f.alice.upload("file", append([]byte("\x89PNG\r\n\x1a\n"), byte(i), 1, 2)); r.Code != 201 {
			t.Fatalf("upload %d: %d %s", i, r.Code, r.Body)
		}
	}
	r := f.alice.upload("file", append([]byte("\x89PNG\r\n\x1a\n"), 9, 9, 9))
	if r.Code != 429 || r.json().errCode() != "RATE_LIMITED" || r.Header.Get("Retry-After") != "7" {
		t.Errorf("over the limit: %d %s retry-after %q", r.Code, r.Body, r.Header.Get("Retry-After"))
	}
	// the limit is per user
	if r := f.bob.upload("file", append([]byte("\x89PNG\r\n\x1a\n"), 5, 5, 5)); r.Code != 201 {
		t.Errorf("another user: %d %s", r.Code, r.Body)
	}
	if r := f.anon.upload("file", testPNG); r.Code != 401 {
		t.Errorf("anonymous: %d", r.Code)
	}
}

// Every write that fans out (comment, share link, invite, new file) is limited per user and named after its action.
func TestDiagramHTTPWriteEndpointsAreRateLimited(t *testing.T) {
	var mu sync.Mutex
	deny := false
	var seen []string
	f := newCommentFixture(t, func(_ *application.Diagrams, deps *Deps) {
		deps.Limit = func(_ context.Context, key string, _ int, _ time.Duration) (bool, time.Duration) {
			mu.Lock()
			defer mu.Unlock()
			if deny {
				seen = append(seen, key)
			}
			return !deny, 3 * time.Second
		}
	})
	mu.Lock()
	deny = true
	mu.Unlock()
	cases := []struct {
		name, path string
		body       map[string]any
	}{
		{"dg-comment", "/files/" + f.fid + "/comments", map[string]any{"body": "hi", "anchor": wirePoint}},
		{"dg-share-create", "/projects/" + f.pid + "/shares", map[string]any{"role": "viewer"}},
		{"dg-member-invite", "/projects/" + f.pid + "/members", map[string]any{"email": "eve@example.com", "role": "viewer"}},
		{"dg-file-create", "/projects/" + f.pid + "/files", map[string]any{"name": "X", "kind": "sdd"}},
	}
	for _, c := range cases {
		r := f.alice.do("POST", c.path, c.body)
		if r.Code != 429 || r.errCode() != "RATE_LIMITED" {
			t.Errorf("%s: %d %v", c.name, r.Code, r.Body)
		}
		if got := seen[len(seen)-1]; got != c.name+":user:1" {
			t.Errorf("%s: limiter key %q", c.name, got)
		}
	}
}
