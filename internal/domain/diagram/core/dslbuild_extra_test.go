package core

import (
	"strings"
	"testing"
)

func emptyPage() Obj {
	return Obj{"id": "p1", "name": "P", "nodes": []any{}, "edges": []any{}}
}

// applyAll applies each text in turn to one page and returns the page and the
// results of the steps.
func applyAll(t *testing.T, layout bool, texts ...string) (Obj, []BuildResult) {
	t.Helper()
	page := emptyPage()
	var out []BuildResult
	for i, text := range texts {
		res, _ := ApplyDSL(page, text, layout)
		if res.Page != nil {
			page = res.Page
		}
		out = append(out, res)
		_ = i
	}
	return page, out
}

func hasDiag(res BuildResult, sub string) bool {
	for _, d := range res.Diagnostics {
		if strings.Contains(d.Message, sub) {
			return true
		}
	}
	return false
}

func containsID(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

func TestApplyArchitectureLifecycle(t *testing.T) {
	_, res := applyAll(t, true,
		"node a\nnode b \"B\" sub=s\na -> b \"x\" dashed color=red\na -> b",
		// a becomes a group (width/height defaults), b leaves, edge arrows change
		"group a \"A\" {\nnode c\n}\nnode b\na <-> b preset=p",
		// group back to a plain node, child leaves group
		"node a\nnode c\nnode b\na -- b\nb <- a",
		// the second edge between a pair goes away, then everything
		"node a\nnode b\na -> b",
		"node a \"Again\" icon=bogus-icon\nnode b icon=aws-ec2",
		"",
	)
	if !containsID(res[1].Added, "c") {
		t.Errorf("step 2 added %v", res[1].Added)
	}
	if len(res[3].Removed) == 0 {
		t.Errorf("step 4 removed %v", res[3].Removed)
	}
	if !hasDiag(res[4], "unknown icon") {
		t.Errorf("step 5 diagnostics %v", res[4].Diagnostics)
	}
	if len(res[5].Removed) == 0 {
		t.Errorf("step 6 removed %v", res[5].Removed)
	}
}

func TestApplyDocDirect(t *testing.T) {
	page := emptyPage()
	page["nodes"] = []any{Obj{"id": "t", "type": "table", "position": Obj{"x": 0.0, "y": 0.0}, "data": Obj{"label": "t"}}}
	doc := &DslDoc{Version: 1, Nodes: []*DslNode{
		{ID: "t", Label: "T", Kind: "node", Line: 1},
		{ID: "a", Label: "A", Kind: "node", Parent: "ghost", Line: 2},
		{ID: "c", Label: "C", Kind: "node", Icon: "kubernetes", Line: 3},
	}}
	res := ApplyDoc(page, doc, false)
	if !hasDiag(res, "cannot edit") || !hasDiag(res, "unknown parent") {
		t.Errorf("%v", res.Diagnostics)
	}
}
