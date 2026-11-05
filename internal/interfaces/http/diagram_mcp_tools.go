package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/iBlog/iblog-monolith-go/internal/application"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram/core"
)

// JSON Schema helpers for the tools' inputs.

type props = map[string]any

func jstr(desc string) props   { return props{"type": "string", "description": desc} }
func jid() props               { return props{"type": "string", "minLength": 1, "maxLength": 64} }
func jbool() props             { return props{"type": "boolean"} }
func jnum() props              { return props{"type": "number"} }
func jint(lo, hi int) props    { return props{"type": "integer", "minimum": lo, "maximum": hi} }
func jenum(vs ...string) props { return props{"type": "string", "enum": vs} }
func jarr(item props, lo, hi int) props {
	return props{"type": "array", "items": item, "minItems": lo, "maxItems": hi}
}
func jobj(p props, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": p}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

var (
	pageProp   = props{"description": "page id, name or zero-based index (default: the first page); see list_pages", "oneOf": []any{props{"type": "string", "minLength": 1, "maxLength": 64}, props{"type": "integer", "minimum": 0}}}
	pageIDProp = props{"type": "string", "minLength": 1, "maxLength": 64, "description": "page id (same as page; wins when both are given); see list_pages"}
	freeObject = props{"type": "object", "additionalProperties": true}
)

func withPage(p props) props {
	p["page"], p["pageId"] = pageProp, pageIDProp
	return p
}

var nodeSpecSchema = jobj(props{
	"id": jid(), "label": props{"type": "string", "minLength": 1, "maxLength": 200},
	"icon":     jstr("catalog id, name or alias, see search_catalog (e.g. \"redis\", \"load balancer\", \"aws-group-aws-cloud\" for a group)"),
	"type":     jenum("icon", "group", "text", "shape", "sticky", "card"),
	"subtitle": props{"type": "string", "maxLength": 200},
	"x":        jnum(), "y": jnum(), "width": jnum(), "height": jnum(),
	"parent": jstr("id of a group node to put this node in; x/y are then relative to the group"),
	"data":   freeObject,
}, "label")

var edgeSpecSchema = jobj(props{
	"id": jid(), "source": jid(), "target": jid(), "label": props{"type": "string", "maxLength": 200},
	"preset": jenum("metrics", "logs", "traces", "control", "backup"), "dashed": jbool(), "data": freeObject,
}, "source", "target")

// mcpTools are the tools, in the Node server's order.
var mcpTools = []mcpTool{
	{name: "list_projects", title: "List projects", scope: diagram.ScopeProjectsRead,
		description: "Projects the token owner can access (id, name, role). Start here to find a projectId.",
		schema:      jobj(props{}), run: toolListProjects},
	{name: "list_files", title: "List files", scope: diagram.ScopeFilesRead,
		description: "Files (diagrams) of a project: id, name, kind (sdd, erd, uml, board), version.",
		schema:      jobj(props{"projectId": jid()}, "projectId"), run: toolListFiles},
	{name: "read_file", title: "Read a diagram", scope: diagram.ScopeFilesRead,
		description: "The nodes and edges of one page of a file as JSON (default: first page). Set compact=true for a short summary (id, type, label, icon, position) instead of full node data.",
		schema:      jobj(withPage(props{"fileId": jid(), "compact": jbool(), "maxNodes": jint(1, 2000)}), "fileId"), run: toolReadFile},
	{name: "list_pages", title: "List pages", scope: diagram.ScopeFilesRead,
		description: "The pages of a file (id, name, node and edge counts). Pass the id as pageId to read_file, add_nodes and the other tools; without it they use the first page.",
		schema:      jobj(props{"fileId": jid()}, "fileId"), run: toolListPages},
	{name: "create_file", title: "Create a file", scope: diagram.ScopeFilesWrite, write: true,
		description: "Create an empty diagram file in a project. Returns its id; then use add_nodes / add_edges.",
		schema:      jobj(props{"projectId": jid(), "name": props{"type": "string", "minLength": 1, "maxLength": 200}, "kind": jenum("sdd", "erd", "uml", "board"), "folderId": jid()}, "projectId", "name"), run: toolCreateFile},
	{name: "add_nodes", title: "Add nodes", scope: diagram.ScopeFilesWrite, write: true,
		description: "Add nodes. Each: label (required), icon (an icon id, used as given), optional id, x/y, parent group id. Omitted positions are auto-placed in a free row. Bad items are reported per index and the rest are added.",
		schema:      jobj(withPage(props{"fileId": jid(), "nodes": jarr(nodeSpecSchema, 1, 200)}), "fileId", "nodes"), run: toolAddNodes},
	{name: "add_edges", title: "Add edges", scope: diagram.ScopeFilesWrite, write: true,
		description: "Connect existing nodes by id. Each: source, target, optional label, preset (metrics|logs|traces|control|backup), dashed.",
		schema:      jobj(withPage(props{"fileId": jid(), "edges": jarr(edgeSpecSchema, 1, 400)}), "fileId", "edges"), run: toolAddEdges},
	{name: "update_node", title: "Update a node", scope: diagram.ScopeFilesWrite, write: true,
		description: "Change a node: label, subtitle, icon, position, size, or merge extra data fields.",
		schema: jobj(withPage(props{"fileId": jid(), "nodeId": jid(), "label": props{"type": "string", "maxLength": 200}, "subtitle": props{"type": "string", "maxLength": 200},
			"icon": props{"type": "string", "maxLength": 100}, "x": jnum(), "y": jnum(), "width": jnum(), "height": jnum(), "data": freeObject}), "fileId", "nodeId"), run: toolUpdateNode},
	{name: "delete_elements", title: "Delete nodes / edges", scope: diagram.ScopeFilesWrite, write: true, destructive: true,
		description: "Delete nodes (with their children and attached edges) and/or edges by id. Undo with undo_last_change.",
		schema:      jobj(withPage(props{"fileId": jid(), "ids": jarr(jid(), 1, 500)}), "fileId", "ids"), run: toolDeleteElements},
	{name: "apply_layout", title: "Auto-layout", scope: diagram.ScopeFilesWrite, write: true,
		description: "Lay the page out deterministically: " + strings.Join(core.Layouts, ", ") + ". Pass only=[node ids] to tidy a selection.",
		schema: jobj(withPage(props{"fileId": jid(), "algorithm": jenum(core.Layouts...), "direction": jenum("LR", "TB"), "gap": props{"type": "number", "minimum": 0, "maximum": 400},
			"seed": props{"type": "integer"}, "only": jarr(jid(), 0, 500), "rootId": jid()}), "fileId", "algorithm"), run: toolApplyLayout},
	{name: "search_catalog", title: "Search icon catalog", scope: diagram.ScopeProjectsRead,
		description: fmt.Sprintf("Search the icon catalog (%d entries: generic, AWS, GCP, Azure, Kubernetes, CNCF) and cluster profiles. Use the returned id as add_nodes.icon.", core.CatalogSize()),
		schema:      jobj(props{"query": props{"type": "string", "maxLength": 100}, "kind": jenum("icon", "group", "cluster"), "limit": jint(1, 100)}, "query"), run: toolSearchCatalog},
	{name: "export", title: "Export a page", scope: diagram.ScopeFilesRead,
		description: "Export one page as text: mermaid (flowchart / erDiagram / classDiagram), plantuml, sql (tables to CREATE TABLE), svg, json, or sdd (the \"#!sdd\" diagrams-as-code text, the same as import_dsl reads).",
		schema:      jobj(withPage(props{"fileId": jid(), "format": jenum(core.ExportFormats...)}), "fileId", "format"), run: toolExport},
	{name: "undo_last_change", title: "Undo the last MCP change", scope: diagram.ScopeFilesWrite, write: true,
		description: "Restores the file to the version before the most recent MCP change (only if that change was made through MCP by you). Collaborators see it live.",
		schema:      jobj(props{"fileId": jid()}, "fileId"), run: toolUndo},
	{name: "import_dsl", title: "Import diagram text", scope: diagram.ScopeFilesWrite, write: true,
		description: "Apply `.sdd` diagram text (nodes, groups, edges, ER tables, sequence diagrams; first line \"#!sdd 1\") to a file. mode=sync (default) makes the page match the text: existing ids keep their positions, new ones are auto-laid out, ids missing from the text are removed. mode=append only adds. Nothing is written when the text has syntax errors; they are returned with line numbers.",
		schema:      jobj(withPage(props{"fileId": jid(), "dsl": props{"type": "string", "minLength": 1, "maxLength": 500000}, "mode": jenum("sync", "append")}), "fileId", "dsl"), run: toolImportDSL},
	{name: "run_lint", title: "Lint a diagram", scope: diagram.ScopeFilesRead,
		description: "Rule-based checks (no AI) of one page: architecture (single point of failure, database without replica or backup, public service without WAF, synchronous call cycles, missing monitoring, cluster profile warnings), ERD (foreign key without index, no primary key, type mismatch, nullable foreign key on a required relation, naming) and UML (inheritance cycles, interface without implementation, unused classes). Rule ids: " + lintRuleIDs() + ".",
		schema:      jobj(withPage(props{"fileId": jid(), "disable": jarr(jstr("rule id"), 0, 50), "severity": props{"type": "object", "additionalProperties": jenum("error", "warning", "info"), "description": "rule id -> severity override"}, "naming": jenum("snake_case", "camelCase", "any")}), "fileId"), run: toolRunLint},
}

// Tool arguments

func badArgs(err error) error {
	return &toolError{msg: "invalid arguments: " + err.Error()}
}

func checkID(name, v string) error {
	if v == "" || len(v) > 64 {
		return &toolError{msg: "invalid arguments: " + name + " must be 1-64 characters"}
	}
	return nil
}

// pageRef folds pageId into page, as the Node server does.
func pageRef(page any, pageID string) any {
	if pageID != "" {
		return pageID
	}
	return page
}

func decodeArgs(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return badArgs(err)
	}
	return nil
}

func summarizeNode(n application.Obj) application.Obj {
	data, _ := n["data"].(application.Obj)
	icon := data["iconId"]
	if icon == nil {
		icon = data["groupIcon"]
	}
	pos, _ := n["position"].(application.Obj)
	return application.Obj{"id": n["id"], "type": n["type"], "label": data["label"], "icon": icon, "parent": n["parentId"],
		"x": pos["x"], "y": pos["y"], "width": n["width"], "height": n["height"]}
}

// Tools

func toolListProjects(c *mcpCall, _ json.RawMessage) (any, error) {
	ps, err := c.h.Diagrams.ListAllProjects(c.ctx, c.uid)
	if err != nil {
		return nil, err
	}
	items := make([]dgProject, len(ps))
	for i, p := range ps {
		items[i] = projectJSON(p)
	}
	return map[string]any{"projects": items}, nil
}

func toolListFiles(c *mcpCall, raw json.RawMessage) (any, error) {
	var a struct {
		ProjectID string `json:"projectId"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if err := checkID("projectId", a.ProjectID); err != nil {
		return nil, err
	}
	t, err := c.h.Diagrams.Tree(c.ctx, c.uid, a.ProjectID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"files": filesJSON(t.Files)}, nil
}

type pageArgs struct {
	FileID string `json:"fileId"`
	Page   any    `json:"page"`
	PageID string `json:"pageId"`
}

func (a pageArgs) ref() any { return pageRef(a.Page, a.PageID) }

func toolReadFile(c *mcpCall, raw json.RawMessage) (any, error) {
	var a struct {
		pageArgs
		Compact  bool `json:"compact"`
		MaxNodes int  `json:"maxNodes"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if err := checkID("fileId", a.FileID); err != nil {
		return nil, err
	}
	f, err := c.h.Diagrams.GetFile(c.ctx, c.uid, a.FileID)
	if err != nil {
		return nil, err
	}
	doc, err := application.DecodeDoc(f.Data)
	if err != nil {
		return nil, err
	}
	page, err := application.PickPage(doc, a.ref())
	if err != nil {
		return nil, &toolError{msg: err.Error()}
	}
	max := a.MaxNodes
	if max < 1 || max > 2000 {
		max = 500
	}
	all := application.Nodes(page)
	nodes := all[:min(max, len(all))]
	var outNodes, outEdges []any
	for _, n := range nodes {
		if a.Compact {
			outNodes = append(outNodes, summarizeNode(n))
		} else {
			outNodes = append(outNodes, n)
		}
	}
	for _, e := range application.Edges(page) {
		if a.Compact {
			outEdges = append(outEdges, application.Obj{"id": e["id"], "source": e["source"], "target": e["target"], "label": e["label"]})
		} else {
			outEdges = append(outEdges, e)
		}
	}
	pages := []any{}
	for _, p := range application.Pages(doc) {
		pages = append(pages, application.Obj{"id": p["id"], "name": p["name"]})
	}
	return map[string]any{
		"file":      application.Obj{"id": f.ID, "projectId": f.ProjectID, "name": f.Name, "kind": f.Kind, "version": f.Version},
		"pages":     pages,
		"page":      application.Obj{"id": page["id"], "name": page["name"]},
		"truncated": len(all) > max,
		"nodes":     orEmpty(outNodes),
		"edges":     orEmpty(outEdges),
	}, nil
}

func orEmpty(v []any) []any {
	if v == nil {
		return []any{}
	}
	return v
}

func toolListPages(c *mcpCall, raw json.RawMessage) (any, error) {
	var a pageArgs
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if err := checkID("fileId", a.FileID); err != nil {
		return nil, err
	}
	f, err := c.h.Diagrams.GetFile(c.ctx, c.uid, a.FileID)
	if err != nil {
		return nil, err
	}
	doc, err := application.DecodeDoc(f.Data)
	if err != nil {
		return nil, err
	}
	out := []any{}
	for i, p := range application.Pages(doc) {
		out = append(out, application.Obj{"id": p["id"], "name": p["name"], "index": i, "nodes": len(application.Nodes(p)), "edges": len(application.Edges(p))})
	}
	return map[string]any{"pages": out}, nil
}

func toolCreateFile(c *mcpCall, raw json.RawMessage) (any, error) {
	var a struct {
		ProjectID string `json:"projectId"`
		Name      string `json:"name"`
		Kind      string `json:"kind"`
		FolderID  string `json:"folderId"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if err := checkID("projectId", a.ProjectID); err != nil {
		return nil, err
	}
	if a.Name == "" || len([]rune(a.Name)) > 200 {
		return nil, &toolError{msg: "invalid arguments: name must be 1-200 characters"}
	}
	in := application.NewDiagramFile{Name: a.Name, Kind: a.Kind, Label: c.h.Diagrams.MCPLabel(c.ctx, c.uid, "created")}
	if a.FolderID != "" {
		in.FolderID = &a.FolderID
	}
	f, _, err := c.h.Diagrams.CreateFile(c.ctx, c.uid, a.ProjectID, in)
	if err != nil {
		return nil, err
	}
	return map[string]any{"file": application.Obj{"id": f.ID, "name": f.Name, "kind": f.Kind, "version": f.Version}}, nil
}

// edit runs an edit on one page of a file and returns the new version.
func (c *mcpCall) edit(a pageArgs, label string, fn func(page application.Obj) error) (int, error) {
	if err := checkID("fileId", a.FileID); err != nil {
		return 0, err
	}
	return c.h.Diagrams.EditFile(c.ctx, c.uid, a.FileID, label, func(doc application.Obj) error {
		page, err := application.PickPage(doc, a.ref())
		if err != nil {
			return &toolError{msg: err.Error()}
		}
		return fn(page)
	})
}

func finite(vs ...*float64) bool {
	for _, v := range vs {
		if v != nil && (*v != *v || *v > 1e15 || *v < -1e15) {
			return false
		}
	}
	return true
}

func toolAddNodes(c *mcpCall, raw json.RawMessage) (any, error) {
	var a struct {
		pageArgs
		Nodes []application.NodeSpec `json:"nodes"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if len(a.Nodes) < 1 || len(a.Nodes) > 200 {
		return nil, &toolError{msg: "invalid arguments: nodes must have 1-200 items"}
	}
	for i, n := range a.Nodes {
		if len([]rune(n.Label)) > 200 || len(n.ID) > 64 || !finite(n.X, n.Y, n.Width, n.Height) {
			return nil, &toolError{msg: fmt.Sprintf("invalid arguments: nodes.%d is out of range", i)}
		}
	}
	var added []application.Obj
	var errs []application.ItemError
	version, err := c.edit(a.pageArgs, "add_nodes", func(page application.Obj) error {
		added, errs = application.AddNodes(page, a.Nodes)
		return nil
	})
	if err != nil {
		return nil, editFailure(err)
	}
	out := map[string]any{"added": summaries(added), "version": version}
	if len(errs) > 0 {
		out["errors"] = errs
	}
	return out, nil
}

func summaries(ns []application.Obj) []any {
	out := []any{}
	for _, n := range ns {
		out = append(out, summarizeNode(n))
	}
	return out
}

func toolAddEdges(c *mcpCall, raw json.RawMessage) (any, error) {
	var a struct {
		pageArgs
		Edges []application.EdgeSpec `json:"edges"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if len(a.Edges) < 1 || len(a.Edges) > 400 {
		return nil, &toolError{msg: "invalid arguments: edges must have 1-400 items"}
	}
	var added []application.Obj
	var errs []application.ItemError
	version, err := c.edit(a.pageArgs, "add_edges", func(page application.Obj) error {
		added, errs = application.AddEdges(page, a.Edges)
		return nil
	})
	if err != nil {
		return nil, editFailure(err)
	}
	list := []any{}
	for _, e := range added {
		list = append(list, application.Obj{"id": e["id"], "source": e["source"], "target": e["target"], "label": e["label"]})
	}
	out := map[string]any{"added": list, "version": version}
	if len(errs) > 0 {
		out["errors"] = errs
	}
	return out, nil
}

func toolUpdateNode(c *mcpCall, raw json.RawMessage) (any, error) {
	var a struct {
		pageArgs
		NodeID   string          `json:"nodeId"`
		Label    *string         `json:"label"`
		Subtitle *string         `json:"subtitle"`
		Icon     *string         `json:"icon"`
		X        *float64        `json:"x"`
		Y        *float64        `json:"y"`
		Width    *float64        `json:"width"`
		Height   *float64        `json:"height"`
		Data     application.Obj `json:"data"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if err := checkID("nodeId", a.NodeID); err != nil {
		return nil, err
	}
	if !finite(a.X, a.Y, a.Width, a.Height) || (a.Width != nil && *a.Width <= 0) || (a.Height != nil && *a.Height <= 0) {
		return nil, &toolError{msg: "invalid arguments: position or size out of range"}
	}
	var node application.Obj
	version, err := c.edit(a.pageArgs, "update_node", func(page application.Obj) error {
		n, err := application.UpdateNode(page, a.NodeID, application.NodePatch{Label: a.Label, Subtitle: a.Subtitle, Icon: a.Icon,
			X: a.X, Y: a.Y, Width: a.Width, Height: a.Height, Data: a.Data})
		if err != nil {
			return &toolError{msg: err.Error()}
		}
		node = summarizeNode(n)
		return nil
	})
	if err != nil {
		return nil, editFailure(err)
	}
	return map[string]any{"node": node, "version": version}, nil
}

func toolDeleteElements(c *mcpCall, raw json.RawMessage) (any, error) {
	var a struct {
		pageArgs
		IDs []string `json:"ids"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if len(a.IDs) < 1 || len(a.IDs) > 500 {
		return nil, &toolError{msg: "invalid arguments: ids must have 1-500 items"}
	}
	var deleted application.Deleted
	version, err := c.edit(a.pageArgs, "delete_elements", func(page application.Obj) error {
		deleted = application.DeleteElements(page, a.IDs)
		return nil
	})
	if err != nil {
		return nil, editFailure(err)
	}
	return map[string]any{"deleted": deleted, "version": version}, nil
}

func toolUndo(c *mcpCall, raw json.RawMessage) (any, error) {
	var a pageArgs
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if err := checkID("fileId", a.FileID); err != nil {
		return nil, err
	}
	from, version, err := c.h.Diagrams.UndoMCPChange(c.ctx, c.uid, a.FileID)
	if err != nil {
		return nil, editFailure(err)
	}
	return map[string]any{"restoredFromVersion": from, "version": version}, nil
}

// editFailure makes an edit's error one the client can read: a document
// the file model refuses says why.
func editFailure(err error) error {
	var te *toolError
	var de *diagram.Error
	switch {
	case errors.As(err, &te):
		return err
	case errors.As(err, &de) && de.Code == "VALIDATION_ERROR":
		return &toolError{msg: "the change makes the diagram invalid: " + de.Msg, details: []string{de.Msg}}
	}
	return err
}
