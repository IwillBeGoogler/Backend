package http

// The MCP server over HTTP (stateless JSON-RPC at /api/diagrams/mcp): auth,
// scopes and project limits per tool, rate limit, audit trail, the tools
// that edit a file, and the tools this server lists but cannot run.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
)

type rpcResp struct {
	Code   int
	Header http.Header
	Body   map[string]any
}

func (r rpcResp) result() map[string]any {
	m, _ := r.Body["result"].(map[string]any)
	return m
}

// text is the first text block of a tool result, and whether it is an error.
func (r rpcResp) text() (string, bool) {
	res := r.result()
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		return "", false
	}
	s, _ := content[0].(map[string]any)["text"].(string)
	isErr, _ := res["isError"].(bool)
	return s, isErr
}

func (r rpcResp) data(t *testing.T) map[string]any {
	t.Helper()
	s, isErr := r.text()
	if isErr {
		t.Fatalf("tool error: %s", s)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("tool result is not JSON: %q", s)
	}
	return m
}

var rpcID = 0

func (e tokenEnv) rpc(t *testing.T, auth []string, method string, params any) rpcResp {
	t.Helper()
	rpcID++
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": rpcID, "method": method, "params": params})
	req, _ := http.NewRequest("POST", e.srv.URL+DiagramMCPPath, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for i := 0; i+1 < len(auth); i += 2 {
		req.Header.Set(auth[i], auth[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := rpcResp{Code: res.StatusCode, Header: res.Header}
	_ = json.Unmarshal(raw, &out.Body)
	if out.Body == nil && len(raw) > 0 {
		t.Logf("non-JSON response %d: %s", res.StatusCode, raw)
	}
	return out
}

func (e tokenEnv) tool(t *testing.T, auth []string, name string, args any) rpcResp {
	t.Helper()
	return e.rpc(t, auth, "tools/call", map[string]any{"name": name, "arguments": args})
}

func bearerHdr(tok string) []string { return []string{"Authorization", "Bearer " + tok} }
func userHdr(id int) []string       { return []string{"X-Test-User", strconv.Itoa(id)} }

func TestMCPAuthAndTransport(t *testing.T) {
	env := newTokenServer(t)
	alice := env.session(t, 1)
	tok := env.mint(t, alice, []string{"projects:read"}, nil)
	noScope := env.mint(t, alice, []string{"files:read"}, nil)

	// anonymous is refused with a JSON-RPC error
	r := env.rpc(t, nil, "tools/list", nil)
	if r.Code != 401 || r.Body["error"].(map[string]any)["code"] != float64(-32001) {
		t.Errorf("anonymous: %d %v", r.Code, r.Body)
	}
	if r := env.rpc(t, bearerHdr("sdd_pat_"+strings.Repeat("A", 32)), "tools/list", nil); r.Code != 401 {
		t.Errorf("unknown token: %d", r.Code)
	}
	// the endpoint needs projects:read
	if r := env.rpc(t, bearerHdr(noScope), "tools/list", nil); r.Code != 403 {
		t.Errorf("a token without projects:read: %d %v", r.Code, r.Body)
	}
	// GET and DELETE: the endpoint is stateless
	for _, m := range []string{"GET", "DELETE"} {
		req, _ := http.NewRequest(m, env.srv.URL+DiagramMCPPath, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 405 || res.Header.Get("Allow") != "POST" {
			t.Errorf("%s: %d Allow %q", m, res.StatusCode, res.Header.Get("Allow"))
		}
	}

	// initialize and list
	init := env.rpc(t, bearerHdr(tok), "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "t", "version": "1"}})
	if init.Code != 200 || init.result()["serverInfo"].(map[string]any)["name"] != "system-design-diagram" {
		t.Errorf("initialize: %d %v", init.Code, init.Body)
	}
	if ins, _ := init.result()["instructions"].(string); !strings.Contains(ins, "list_projects") {
		t.Errorf("instructions = %q", ins)
	}
	list := env.rpc(t, bearerHdr(tok), "tools/list", nil)
	tools, _ := list.result()["tools"].([]any)
	names := map[string]map[string]any{}
	for _, x := range tools {
		m := x.(map[string]any)
		names[m["name"].(string)] = m
	}
	for _, n := range []string{"list_projects", "list_files", "read_file", "list_pages", "create_file", "add_nodes", "add_edges", "update_node", "delete_elements", "undo_last_change",
		"apply_layout", "search_catalog", "export", "import_dsl", "run_lint"} {
		if names[n] == nil {
			t.Errorf("tool %s is not listed", n)
		}
	}
	if len(names) != 15 {
		t.Errorf("%d tools listed", len(names))
	}
	ann := names["delete_elements"]["annotations"].(map[string]any)
	if ann["destructiveHint"] != true || ann["readOnlyHint"] != false || names["read_file"]["annotations"].(map[string]any)["readOnlyHint"] != true {
		t.Errorf("annotations = %v", ann)
	}
	for n, m := range names {
		if d, _ := m["description"].(string); strings.Contains(d, "NOT AVAILABLE") {
			t.Errorf("%s is marked unsupported: %q", n, d)
		}
	}
	schema := names["add_nodes"]["inputSchema"].(map[string]any)
	if schema["type"] != "object" || len(schema["required"].([]any)) != 2 {
		t.Errorf("add_nodes schema = %v", schema)
	}

	// a session works too
	if r := env.rpc(t, userHdr(1), "tools/list", nil); r.Code != 200 {
		t.Errorf("session: %d", r.Code)
	}
}

func TestMCPTools(t *testing.T) {
	env := newTokenServer(t)
	alice := env.session(t, 1)
	all := bearerHdr(env.mint(t, alice, diagram.Scopes, nil))
	made := alice.expect(alice.do("POST", "/projects", map[string]any{"name": "Shop"}), 201)
	pid, fid := made.obj("project")["id"].(string), made.obj("file")["id"].(string)

	// read
	if r := env.tool(t, all, "list_projects", map[string]any{}).data(t); len(r["projects"].([]any)) != 1 || r["projects"].([]any)[0].(map[string]any)["id"] != pid {
		t.Errorf("list_projects = %v", r)
	}
	files := env.tool(t, all, "list_files", map[string]any{"projectId": pid}).data(t)["files"].([]any)
	if len(files) != 1 || files[0].(map[string]any)["id"] != fid {
		t.Errorf("list_files = %v", files)
	}
	pages := env.tool(t, all, "list_pages", map[string]any{"fileId": fid}).data(t)["pages"].([]any)
	if len(pages) != 1 || pages[0].(map[string]any)["index"] != float64(0) || pages[0].(map[string]any)["nodes"] != float64(0) {
		t.Errorf("list_pages = %v", pages)
	}
	pageID := pages[0].(map[string]any)["id"].(string)

	// add nodes: auto ids, auto placement, per-index errors, the rest still added
	r := env.tool(t, all, "add_nodes", map[string]any{"fileId": fid, "nodes": []any{
		map[string]any{"label": "API Gateway", "icon": "aws-api-gateway"},
		map[string]any{"label": "API Gateway"},
		map[string]any{"label": "Database", "x": 400, "y": 10},
		map[string]any{"label": "  "},
		map[string]any{"label": "Dup", "id": "database"},
		map[string]any{"label": "Cloud", "type": "group", "icon": "aws-group-aws-cloud"},
	}}).data(t)
	added := r["added"].([]any)
	ids := []string{}
	for _, a := range added {
		ids = append(ids, a.(map[string]any)["id"].(string))
	}
	if strings.Join(ids, ",") != "api-gateway,api-gateway-2,database,cloud" || r["version"] != float64(2) {
		t.Errorf("added = %v, version %v", ids, r["version"])
	}
	errs := r["errors"].([]any)
	if len(errs) != 2 || errs[0].(map[string]any)["index"] != float64(3) || errs[1].(map[string]any)["message"] != `node id "database" already exists` {
		t.Errorf("errors = %v", errs)
	}
	first := added[0].(map[string]any)
	if first["icon"] != "aws-api-gateway" || first["x"] != float64(0) || first["y"] != float64(0) {
		t.Errorf("first node = %v", first)
	}
	if second := added[1].(map[string]any); second["x"] != float64(180) {
		t.Errorf("second node is placed in the same row, 180 to the right: %v", second)
	}
	if grp := added[3].(map[string]any); grp["type"] != "group" || grp["width"] != float64(360) || grp["icon"] != "aws-group-aws-cloud" {
		t.Errorf("group = %v", grp)
	}

	// it is a real save: version, snapshot label, the document
	got := alice.expect(alice.do("GET", "/files/"+fid, nil), 200).obj("file")
	if got["version"] != float64(2) || got["updatedBy"] != "1" {
		t.Errorf("file = %v", got)
	}
	vs := alice.expect(alice.do("GET", "/files/"+fid+"/versions", nil), 200).Body["items"].([]any)
	if lbl := vs[0].(map[string]any)["label"]; lbl != "alice via MCP: add_nodes" && lbl != "alice via MCP: add_nodes " {
		t.Errorf("snapshot label = %v", lbl)
	}

	// edges
	e := env.tool(t, all, "add_edges", map[string]any{"fileId": fid, "pageId": pageID, "edges": []any{
		map[string]any{"source": "api-gateway", "target": "database", "label": "SQL", "preset": "traces", "dashed": true},
		map[string]any{"source": "api-gateway", "target": "ghost"},
	}}).data(t)
	if len(e["added"].([]any)) != 1 || e["added"].([]any)[0].(map[string]any)["id"] != "e-api-gateway-database" || e["errors"].([]any)[0].(map[string]any)["message"] != `node "ghost" not found` || e["version"] != float64(3) {
		t.Errorf("add_edges = %v", e)
	}

	// read it back: full and compact
	full := env.tool(t, all, "read_file", map[string]any{"fileId": fid}).data(t)
	if len(full["nodes"].([]any)) != 4 || len(full["edges"].([]any)) != 1 || full["truncated"] != false || full["file"].(map[string]any)["version"] != float64(3) {
		t.Errorf("read_file = %v", full)
	}
	edge := full["edges"].([]any)[0].(map[string]any)
	if d := edge["data"].(map[string]any); d["preset"] != "traces" || d["dashed"] != true {
		t.Errorf("edge data = %v", edge)
	}
	compact := env.tool(t, all, "read_file", map[string]any{"fileId": fid, "compact": true, "maxNodes": 2}).data(t)
	if len(compact["nodes"].([]any)) != 2 || compact["truncated"] != true || compact["nodes"].([]any)[0].(map[string]any)["label"] != "API Gateway" {
		t.Errorf("compact = %v", compact)
	}
	if s, isErr := env.tool(t, all, "read_file", map[string]any{"fileId": fid, "page": "nope"}).text(); !isErr || !strings.Contains(s, `page "nope" not found`) {
		t.Errorf("unknown page: %q %v", s, isErr)
	}

	// update
	u := env.tool(t, all, "update_node", map[string]any{"fileId": fid, "nodeId": "database", "label": "Postgres", "x": 500, "data": map[string]any{"tier": "gold"}}).data(t)
	if n := u["node"].(map[string]any); n["label"] != "Postgres" || n["x"] != float64(500) || n["y"] != float64(10) || u["version"] != float64(4) {
		t.Errorf("update_node = %v", u)
	}
	if s, isErr := env.tool(t, all, "update_node", map[string]any{"fileId": fid, "nodeId": "ghost"}).text(); !isErr || !strings.Contains(s, `node "ghost" not found`) {
		t.Errorf("update unknown node: %q", s)
	}
	db := env.tool(t, all, "read_file", map[string]any{"fileId": fid}).data(t)["nodes"].([]any)[2].(map[string]any)
	if db["data"].(map[string]any)["tier"] != "gold" {
		t.Errorf("merged data = %v", db["data"])
	}

	// delete a node: attached edges go too
	d := env.tool(t, all, "delete_elements", map[string]any{"fileId": fid, "ids": []any{"database", "nothing"}}).data(t)
	if del := d["deleted"].(map[string]any); del["nodes"] != float64(1) || del["edges"] != float64(1) || d["version"] != float64(5) {
		t.Errorf("delete_elements = %v", d)
	}

	// undo: back to the version before the delete, as a new version
	un := env.tool(t, all, "undo_last_change", map[string]any{"fileId": fid}).data(t)
	if un["restoredFromVersion"] != float64(4) || un["version"] != float64(6) {
		t.Errorf("undo = %v", un)
	}
	if n := len(env.tool(t, all, "read_file", map[string]any{"fileId": fid}).data(t)["nodes"].([]any)); n != 4 {
		t.Errorf("after undo: %d nodes", n)
	}
	if s, isErr := env.tool(t, all, "undo_last_change", map[string]any{"fileId": fid}).text(); !isErr || !strings.Contains(s, "already undone") {
		t.Errorf("second undo: %q %v", s, isErr)
	}
	// somebody else's save in between: nothing to undo through MCP
	alice.expect(alice.do("PUT", "/files/"+fid, map[string]any{"name": "Renamed"}, "If-Match", `"6"`), 200)

	// create_file
	cf := env.tool(t, all, "create_file", map[string]any{"projectId": pid, "name": "Second", "kind": "erd"}).data(t)["file"].(map[string]any)
	if cf["name"] != "Second" || cf["kind"] != "erd" || cf["version"] != float64(1) || !strings.HasPrefix(cf["id"].(string), "fil_") {
		t.Errorf("create_file = %v", cf)
	}
	if s, isErr := env.tool(t, all, "create_file", map[string]any{"projectId": pid, "name": ""}).text(); !isErr || !strings.Contains(s, "invalid arguments") {
		t.Errorf("empty name: %q", s)
	}
}

func TestMCPEditsThroughTheRolesAndLimits(t *testing.T) {
	env := newTokenServer(t)
	alice, bob := env.session(t, 1), env.session(t, 2)
	mine := alice.expect(alice.do("POST", "/projects", map[string]any{"name": "Mine"}), 201)
	other := alice.expect(alice.do("POST", "/projects", map[string]any{"name": "Other"}), 201)
	pid, fid := mine.obj("project")["id"].(string), mine.obj("file")["id"].(string)
	ofid := other.obj("file")["id"].(string)
	alice.expect(alice.do("POST", "/projects/"+pid+"/members", map[string]any{"email": "bob", "role": "viewer"}), 201)

	limited := bearerHdr(env.mint(t, alice, diagram.Scopes, map[string]any{"projectIds": []string{pid}}))
	if s, isErr := env.tool(t, limited, "read_file", map[string]any{"fileId": ofid}).text(); !isErr || !strings.Contains(s, "not found") {
		t.Errorf("limited token on another project: %q", s)
	}
	if got := env.tool(t, limited, "list_projects", map[string]any{}).data(t)["projects"].([]any); len(got) != 1 {
		t.Errorf("limited list_projects = %v", got)
	}
	// a viewer's token can read but not write
	bobTok := bearerHdr(env.mint(t, bob, diagram.Scopes, nil))
	env.tool(t, bobTok, "read_file", map[string]any{"fileId": fid}).data(t)
	if s, isErr := env.tool(t, bobTok, "add_nodes", map[string]any{"fileId": fid, "nodes": []any{map[string]any{"label": "x"}}}).text(); !isErr || !strings.Contains(s, `needs the "editor" role`) {
		t.Errorf("viewer write: %q", s)
	}
	// scope per tool
	readOnly := bearerHdr(env.mint(t, alice, []string{"projects:read", "files:read"}, nil))
	env.tool(t, readOnly, "read_file", map[string]any{"fileId": fid}).data(t)
	if s, isErr := env.tool(t, readOnly, "add_nodes", map[string]any{"fileId": fid, "nodes": []any{map[string]any{"label": "x"}}}).text(); !isErr || !strings.Contains(s, `needs the "files:write" scope for add_nodes`) {
		t.Errorf("scope: %q", s)
	}
	if n := len(env.tool(t, bearerHdr(env.mint(t, alice, diagram.Scopes, nil)), "read_file", map[string]any{"fileId": fid}).data(t)["nodes"].([]any)); n != 0 {
		t.Errorf("a denied call changed the file: %d nodes", n)
	}
}

func TestMCPCoreTools(t *testing.T) {
	env := newTokenServer(t)
	alice := env.session(t, 1)
	all := bearerHdr(env.mint(t, alice, diagram.Scopes, nil))
	made := alice.expect(alice.do("POST", "/projects", map[string]any{"name": "Core"}), 201)
	fid := made.obj("file")["id"].(string)

	// search_catalog
	res := env.tool(t, all, "search_catalog", map[string]any{"query": "redis", "limit": 3}).data(t)["results"].([]any)
	if len(res) == 0 || res[0].(map[string]any)["id"] != "redis" {
		t.Fatalf("search_catalog = %v", res)
	}
	cl := env.tool(t, all, "search_catalog", map[string]any{"query": "kafka", "kind": "cluster"}).data(t)["results"].([]any)
	if len(cl) == 0 || cl[0].(map[string]any)["profileId"] != "kafka" {
		t.Errorf("cluster search = %v", cl)
	}
	if s, isErr := env.tool(t, all, "search_catalog", map[string]any{"query": "x", "limit": 500}).text(); !isErr || !strings.Contains(s, "invalid arguments") {
		t.Errorf("limit: %q", s)
	}

	// import_dsl: sync builds the page, errors change nothing
	text := "#!sdd 1\ndiagram \"Shop\"\nnode lb \"LB\" icon=load-balancer\nnode api \"API\" icon=service\nnode db \"DB\" icon=postgresql\nlb -> api\napi -> db \"SQL\"\n"
	imp := env.tool(t, all, "import_dsl", map[string]any{"fileId": fid, "dsl": text}).data(t)
	if len(imp["added"].([]any)) != 5 || len(imp["errors"].([]any)) != 0 || imp["version"] != float64(2) {
		t.Fatalf("import_dsl = %v", imp)
	}
	bad, isErr := env.tool(t, all, "import_dsl", map[string]any{"fileId": fid, "dsl": "node a \"A\"\na -> ghost\nwat\n"}).text()
	if !isErr || !strings.Contains(bad, "2 error(s); nothing was changed") || !strings.Contains(bad, `unknown node`) {
		t.Errorf("import errors: %q", bad)
	}
	app := env.tool(t, all, "import_dsl", map[string]any{"fileId": fid, "mode": "append", "dsl": "node cache \"Cache\" icon=redis\napi -> cache\n"}).data(t)
	if len(app["removed"].([]any)) != 0 || len(app["added"].([]any)) != 2 {
		t.Errorf("append = %v", app)
	}

	// export: the text round-trips, other formats render
	sdd := env.tool(t, all, "export", map[string]any{"fileId": fid, "format": "sdd"}).data(t)["content"].(string)
	for _, want := range []string{"#!sdd 1", `node cache "Cache" icon=redis`, `api -> db "SQL"`} {
		if !strings.Contains(sdd, want) {
			t.Errorf("sdd export misses %q:\n%s", want, sdd)
		}
	}
	mer := env.tool(t, all, "export", map[string]any{"fileId": fid, "format": "mermaid"}).data(t)["content"].(string)
	if !strings.HasPrefix(mer, "flowchart LR") || !strings.Contains(mer, "api -->|SQL| db") {
		t.Errorf("mermaid = %q", mer)
	}
	for _, f := range []string{"plantuml", "sql", "svg", "json"} {
		if c := env.tool(t, all, "export", map[string]any{"fileId": fid, "format": f}).data(t)["content"].(string); c == "" {
			t.Errorf("%s export is empty", f)
		}
	}
	if _, isErr := env.tool(t, all, "export", map[string]any{"fileId": fid, "format": "pdf"}).text(); !isErr {
		t.Error("an unknown format is refused")
	}

	// apply_layout moves nodes and saves a version
	read := func() map[string]map[string]any {
		out := map[string]map[string]any{}
		for _, n := range env.tool(t, all, "read_file", map[string]any{"fileId": fid, "compact": true}).data(t)["nodes"].([]any) {
			out[n.(map[string]any)["id"].(string)] = n.(map[string]any)
		}
		return out
	}
	before := read()["db"]["x"]
	lay := env.tool(t, all, "apply_layout", map[string]any{"fileId": fid, "algorithm": "layered", "direction": "TB"}).data(t)
	if lay["moved"].(float64) < 1 || lay["version"] != float64(4) {
		t.Errorf("apply_layout = %v", lay)
	}
	if after := read(); after["lb"]["y"].(float64) >= after["api"]["y"].(float64) || after["db"]["x"] == before && lay["moved"].(float64) == 0 {
		t.Errorf("top-to-bottom layout: %v", after)
	}
	if s, isErr := env.tool(t, all, "apply_layout", map[string]any{"fileId": fid, "algorithm": "spiral"}).text(); !isErr || !strings.Contains(s, "algorithm must be one of") {
		t.Errorf("unknown algorithm: %q", s)
	}

	// run_lint
	lint := env.tool(t, all, "run_lint", map[string]any{"fileId": fid, "disable": []any{"arch/db-no-backup"}}).data(t)
	counts := lint["counts"].(map[string]any)
	if counts["warning"].(float64) < 1 {
		t.Errorf("lint counts = %v", counts)
	}
	rules := map[string]bool{}
	for _, p := range lint["problems"].([]any) {
		rules[p.(map[string]any)["rule"].(string)] = true
	}
	if !rules["arch/db-no-replica"] || rules["arch/db-no-backup"] {
		t.Errorf("lint rules = %v", rules)
	}
	if s, isErr := env.tool(t, all, "run_lint", map[string]any{"fileId": fid, "severity": map[string]any{"arch/spof": "loud"}}).text(); !isErr || !strings.Contains(s, "must be error, warning or info") {
		t.Errorf("severity: %q", s)
	}

	// add_nodes now resolves icons through the catalog
	r := env.tool(t, all, "add_nodes", map[string]any{"fileId": fid, "nodes": []any{
		map[string]any{"label": "R", "icon": "Load Balancer"}, map[string]any{"label": "X", "icon": "no-such-icon-anywhere"},
	}}).data(t)
	if a := r["added"].([]any); len(a) != 1 || a[0].(map[string]any)["icon"] != "load-balancer" || len(r["errors"].([]any)) != 1 {
		t.Errorf("add_nodes icons = %v", r)
	}

	// read-only tokens cannot write but can lint and export
	ro := bearerHdr(env.mint(t, alice, []string{"projects:read", "files:read"}, nil))
	env.tool(t, ro, "run_lint", map[string]any{"fileId": fid}).data(t)
	env.tool(t, ro, "export", map[string]any{"fileId": fid, "format": "json"}).data(t)
	for _, n := range []string{"apply_layout", "import_dsl"} {
		if s, isErr := env.tool(t, ro, n, map[string]any{"fileId": fid, "algorithm": "grid", "dsl": "node a"}).text(); !isErr || !strings.Contains(s, "scope") {
			t.Errorf("%s with a read-only token: %q", n, s)
		}
	}
}

func TestMCPRateLimitAndAudit(t *testing.T) {
	env := newTokenServer(t)
	alice, bob := env.session(t, 1), env.session(t, 2)
	made := alice.expect(alice.do("POST", "/projects", map[string]any{"name": "Audit"}), 201)
	fid := made.obj("file")["id"].(string)
	tok := env.mint(t, alice, []string{"projects:read", "files:read"}, nil)
	auth := bearerHdr(tok)

	env.tool(t, auth, "list_projects", map[string]any{}).data(t)
	env.tool(t, auth, "read_file", map[string]any{"fileId": fid, "compact": true}).data(t)
	env.tool(t, auth, "add_nodes", map[string]any{"fileId": fid, "nodes": []any{map[string]any{"label": "x"}}}) // denied: scope
	env.tool(t, auth, "read_file", map[string]any{"fileId": "fil_nope"})                                        // error: not found
	env.tool(t, bearerHdr(env.mint(t, alice, diagram.Scopes, nil)), "add_nodes", map[string]any{"fileId": fid, "dsl": "secret", "nodes": []any{map[string]any{"label": strings.Repeat("L", 200)}}}).data(t)

	log := alice.expect(alice.do("GET", "/mcp/audit", nil), 200).Body["items"].([]any)
	if len(log) != 5 {
		t.Fatalf("audit has %d entries", len(log))
	}
	byTool := map[string]map[string]any{}
	statuses := []string{}
	for _, x := range log { // newest first
		m := x.(map[string]any)
		statuses = append(statuses, m["tool"].(string)+":"+m["status"].(string))
		byTool[m["tool"].(string)+":"+m["status"].(string)] = m
	}
	if strings.Join(statuses, " ") != "add_nodes:ok read_file:denied add_nodes:denied read_file:ok list_projects:ok" {
		t.Errorf("audit = %v", statuses)
	}
	ok := byTool["read_file:ok"]
	if ok["fileId"] != fid || ok["tokenId"] == nil || ok["userId"] != "1" || ok["durationMs"] == nil && ok["status"] == nil {
		t.Errorf("entry = %v", ok)
	}
	write := byTool["add_nodes:ok"]["detail"].(string)
	if strings.Contains(write, "secret") || strings.Contains(write, strings.Repeat("L", 100)) || !strings.Contains(write, "<redacted>") {
		t.Errorf("the audit detail keeps contents: %s", write)
	}
	// only your own trail, and not through a token
	if got := bob.expect(bob.do("GET", "/mcp/audit", nil), 200).Body["items"].([]any); len(got) != 0 {
		t.Errorf("bob sees %d entries of alice", len(got))
	}
	if r := env.bearer(t, tok)("GET", "/v1/mcp/audit", nil); r.Code != 404 && r.Code != 403 {
		t.Errorf("audit through a token: %d", r.Code)
	}
	req, _ := http.NewRequest("GET", env.srv.URL+DiagramMCPPath+"/audit", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	res, _ := http.DefaultClient.Do(req)
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Errorf("the audit through a token: %d", res.StatusCode)
	}
	if r := env.session(t, 0).do("GET", "/mcp/audit", nil); r.Code != 401 {
		t.Errorf("anonymous audit: %d", r.Code)
	}
}

func TestMCPRateLimiter(t *testing.T) {
	now := time.Unix(1000, 0)
	w := newRateWindow(3, time.Minute)
	w.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if w.take("a") != 0 {
			t.Fatalf("call %d limited", i)
		}
		now = now.Add(time.Second)
	}
	if wait := w.take("a"); wait != 57 {
		t.Errorf("4th call: wait %ds, want 57", wait)
	}
	if w.take("b") != 0 {
		t.Error("another key shares the bucket")
	}
	now = now.Add(58 * time.Second)
	if w.take("a") != 0 {
		t.Error("the window did not slide")
	}
	// a refused call is not recorded
	w2 := newRateWindow(1, time.Minute)
	w2.now = func() time.Time { return now }
	w2.take("k")
	for i := 0; i < 5; i++ {
		now = now.Add(10 * time.Second)
		w2.take("k")
	}
	now = now.Add(10 * time.Second)
	if w2.take("k") != 0 {
		t.Error("refused calls extended the window")
	}
}

func TestMCPRateLimitOverHTTP(t *testing.T) {
	env := newTokenServer(t)
	env.limiter().n = 3
	tok := bearerHdr(env.mint(t, env.session(t, 1), diagram.Scopes, nil))
	for i := 0; i < 3; i++ {
		env.tool(t, tok, "list_projects", map[string]any{}).data(t)
	}
	s, isErr := env.tool(t, tok, "list_projects", map[string]any{}).text()
	if !isErr || !strings.HasPrefix(s, "rate limit exceeded (3 calls per minute); retry in ") {
		t.Errorf("4th call: %q %v", s, isErr)
	}
	// another token has its own bucket
	other := bearerHdr(env.mint(t, env.session(t, 1), diagram.Scopes, nil))
	env.tool(t, other, "list_projects", map[string]any{}).data(t)
	log := env.session(t, 1).expect(env.session(t, 1).do("GET", "/mcp/audit", nil), 200).Body["items"].([]any)
	limited := 0
	for _, x := range log {
		if x.(map[string]any)["status"] == "rate_limited" {
			limited++
		}
	}
	if limited != 1 {
		t.Errorf("%d rate_limited entries", limited)
	}
}

func TestMCPIsOffWhenDisabled(t *testing.T) {
	srv, _ := newDiagramServer(t) // MCPEnabled is false
	req, _ := http.NewRequest("POST", srv.URL+DiagramMCPPath, strings.NewReader("{}"))
	req.Header.Set("X-Test-User", "1")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 404 && res.StatusCode != 405 {
		t.Errorf("disabled MCP answers %d", res.StatusCode)
	}
}

type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// A real MCP client (the SDK's) can connect, list the tools and call one.
func TestMCPWithTheSDKClient(t *testing.T) {
	env := newTokenServer(t)
	alice := env.session(t, 1)
	alice.expect(alice.do("POST", "/projects", map[string]any{"name": "Client"}), 201)
	tok := env.mint(t, alice, diagram.Scopes, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: env.srv.URL + DiagramMCPPath, HTTPClient: &http.Client{Transport: bearerTransport{tok}}}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()
	if info := session.InitializeResult().ServerInfo; info.Name != "system-design-diagram" {
		t.Errorf("server = %+v", info)
	}
	tools, err := session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 15 {
		t.Fatalf("tools: %d %v", len(tools.Tools), err)
	}
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "list_projects", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("call: %v %+v", err, res)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, `"name":"Client"`) {
		t.Errorf("result = %s", text)
	}
	bad, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "read_file", Arguments: map[string]any{"fileId": "fil_nope"}})
	if err != nil || !bad.IsError {
		t.Errorf("a missing file is a tool error: %v %+v", err, bad)
	}
}
