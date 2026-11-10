package http

// Every MCP tool with arguments it must refuse: missing or malformed ids,
// out-of-range values, unknown files, pages and nodes. A refusal is a tool
// error in a 200 JSON-RPC reply, never a crash.

import (
	"strings"
	"testing"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
)

func TestMCPToolsRefuseBadArguments(t *testing.T) {
	env := newTokenServer(t)
	alice := env.session(t, 1)
	all := bearerHdr(env.mint(t, alice, diagram.Scopes, nil))
	made := alice.expect(alice.do("POST", "/projects", map[string]any{"name": "Errs"}), 201)
	fid := made.obj("file")["id"].(string)
	pid := made.obj("project")["id"].(string)

	long := strings.Repeat("x", 100)
	refuse := func(tool string, args any) {
		t.Helper()
		r := env.tool(t, all, tool, args)
		if r.Code != 200 {
			t.Errorf("%s %v: HTTP %d", tool, args, r.Code)
			return
		}
		if s, isErr := r.text(); !isErr {
			t.Errorf("%s %v accepted: %s", tool, args, s)
		}
	}
	m := func(kv ...any) map[string]any {
		out := map[string]any{}
		for i := 0; i+1 < len(kv); i += 2 {
			out[kv[i].(string)] = kv[i+1]
		}
		return out
	}

	// malformed argument objects and missing ids, for every tool that takes arguments
	for _, tool := range []string{"list_files", "read_file", "list_pages", "create_file", "add_nodes", "add_edges",
		"update_node", "delete_elements", "undo_last_change", "apply_layout", "export", "import_dsl", "run_lint"} {
		refuse(tool, "not-an-object")
		refuse(tool, m())
		refuse(tool, m("fileId", long, "projectId", long))
		refuse(tool, m("fileId", "no-such-file", "projectId", "no-such-project", "nodeId", "n", "name", "x",
			"nodes", []any{map[string]any{"label": "a"}}, "edges", []any{map[string]any{"source": "a", "target": "b"}},
			"ids", []any{"a"}, "algorithm", "grid", "format", "json", "dsl", "node a \"A\""))
	}

	// value limits
	refuse("create_file", m("projectId", pid, "name", ""))
	refuse("create_file", m("projectId", pid, "name", strings.Repeat("n", 201)))
	refuse("add_nodes", m("fileId", fid, "nodes", []any{}))
	refuse("add_nodes", m("fileId", fid, "nodes", make([]any, 201)))
	refuse("add_nodes", m("fileId", fid, "nodes", []any{m("label", strings.Repeat("l", 201))}))
	refuse("add_nodes", m("fileId", fid, "nodes", []any{m("label", "a", "id", strings.Repeat("i", 65))}))
	refuse("add_edges", m("fileId", fid, "edges", []any{}))
	refuse("add_edges", m("fileId", fid, "edges", make([]any, 401)))
	refuse("delete_elements", m("fileId", fid, "ids", []any{}))
	refuse("delete_elements", m("fileId", fid, "ids", make([]any, 501)))
	refuse("update_node", m("fileId", fid, "nodeId", "ghost", "label", "x"))
	refuse("update_node", m("fileId", fid, "nodeId", "ghost", "width", -5))
	refuse("update_node", m("fileId", fid, "nodeId", "ghost", "x", 1e20))
	refuse("update_node", m("fileId", fid))
	refuse("apply_layout", m("fileId", fid, "algorithm", "grid", "direction", "UP"))
	refuse("apply_layout", m("fileId", fid, "algorithm", "grid", "gap", 500))
	refuse("apply_layout", m("fileId", fid, "algorithm", "grid", "gap", 1e20))
	refuse("apply_layout", m("fileId", fid, "algorithm", "grid", "seed", 1.5))
	refuse("apply_layout", m("fileId", fid, "algorithm", "grid", "only", make([]string, 501)))
	refuse("apply_layout", m("fileId", fid, "algorithm", "grid", "rootId", long))
	refuse("apply_layout", m("fileId", fid, "algorithm", "grid", "only", []any{long}))
	refuse("search_catalog", m("query", strings.Repeat("q", 101)))
	refuse("search_catalog", m("query", "a", "kind", "bogus"))
	refuse("search_catalog", m("query", "a", "limit", 0))
	refuse("search_catalog", "not-an-object")
	refuse("export", m("fileId", fid, "format", "pdf"))
	refuse("import_dsl", m("fileId", fid, "dsl", "", "mode", "bogus"))
	refuse("run_lint", m("fileId", fid, "severity", m("arch/spof", "loud")))
	refuse("run_lint", m("fileId", fid, "disable", make([]any, 600)))

	// an unknown page of a real file
	for _, tool := range []string{"read_file", "export", "run_lint", "apply_layout", "add_nodes", "add_edges", "delete_elements", "update_node"} {
		refuse(tool, m("fileId", fid, "page", "no-such-page", "pageId", "no-such-page", "format", "json", "algorithm", "grid",
			"nodes", []any{m("label", "a")}, "edges", []any{m("source", "a", "target", "b")}, "ids", []any{"a"}, "nodeId", "a"))
	}

	// undo with nothing to undo is refused; list_projects takes no arguments
	refuse("undo_last_change", m("fileId", fid))
	env.tool(t, all, "list_projects", nil).data(t)
	// pages of a fresh file, then reading a page past the end
	if pages := env.tool(t, all, "list_pages", m("fileId", fid)).data(t)["pages"].([]any); len(pages) == 0 {
		t.Error("a new file has a page")
	}
	refuse("read_file", m("fileId", fid, "page", 99))
}
