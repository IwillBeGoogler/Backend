package application

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
)

func page(t *testing.T, raw string) Obj {
	t.Helper()
	doc, err := DecodeDoc(json.RawMessage(`{"version":2,"activePageId":"pg","pages":[` + raw + `]}`))
	if err != nil {
		t.Fatal(err)
	}
	return Pages(doc)[0]
}

func fptr(f float64) *float64 { return &f }

func TestAddNodesPlacesAndIdentifies(t *testing.T) {
	p := page(t, `{"id":"pg","name":"P","nodes":[
		{"id":"a","type":"icon","position":{"x":40,"y":10},"width":100,"height":50,"data":{"label":"A"}},
		{"id":"g","type":"group","position":{"x":300,"y":200},"data":{"label":"G"}},
		{"id":"child","type":"icon","parentId":"g","position":{"x":0,"y":900},"data":{"label":"C"}}],"edges":[]}`)
	added, errs := AddNodes(p, []NodeSpec{
		{Label: "API Gateway", Icon: "aws-api-gateway"},
		{Label: "api gateway"},
		{Label: "Fixed", X: fptr(7), Y: fptr(8), ID: "fixed", Subtitle: "sub", Data: Obj{"tier": "gold", "label": "ignored"}},
		{Label: ""},
		{Label: "x", ID: "a"},
		{Label: "x", Parent: "a"},
		{Label: "x", Parent: "ghost"},
		{Label: "Inner 1", Parent: "g"},
		{Label: "Inner 2", Parent: "g"},
		{Label: "Zone", Type: "group", Icon: "aws-group-aws-cloud"},
		{Label: "Sized", Width: fptr(50)},
	})
	ids := []string{}
	for _, n := range added {
		ids = append(ids, n["id"].(string))
	}
	if strings.Join(ids, ",") != "api-gateway,api-gateway-2,fixed,inner-1,inner-2,zone,sized" {
		t.Errorf("ids = %v", ids)
	}
	if len(errs) != 4 || errs[0].Index != 3 || errs[0].Message != "label is required" || errs[1].Message != `node id "a" already exists` ||
		errs[2].Message != `parent "a" is not a group on this page` || errs[3].Message != `parent "ghost" is not a group on this page` {
		t.Errorf("errors = %+v", errs)
	}
	x, y := position(added[0])
	// below the lowest top-level node (the group: y 200 + the default height 96), 60 gap; x at the leftmost
	// top-level node; the child at y 900 is not top level
	if x != 40 || y != 200+96+60 {
		t.Errorf("first auto position = %v,%v", x, y)
	}
	if x2, y2 := position(added[1]); x2 != 40+120+60 || y2 != y {
		t.Errorf("second auto position = %v,%v", x2, y2)
	}
	if x3, y3 := position(added[2]); x3 != 7 || y3 != 8 {
		t.Errorf("explicit position = %v,%v", x3, y3)
	}
	fixed := added[2]["data"].(Obj)
	if fixed["label"] != "Fixed" || fixed["tier"] != "gold" || fixed["subtitle"] != "sub" {
		t.Errorf("data = %v", fixed)
	}
	// like the TypeScript core, the slot counts every node added so far (3 before it)
	if px, py := position(added[3]); px != 24 || py != 60+120 {
		t.Errorf("first child position = %v,%v", px, py)
	}
	if px, _ := position(added[4]); px != 24+150 {
		t.Errorf("second child position x = %v", px)
	}
	if added[3]["parentId"] != "g" || added[0]["data"].(Obj)["iconId"] != "aws-api-gateway" {
		t.Errorf("parent/icon = %v / %v", added[3]["parentId"], added[0]["data"])
	}
	zone := added[5]
	if zone["type"] != "group" || zone["width"] != 360.0 || zone["height"] != 240.0 || zone["data"].(Obj)["groupIcon"] != "aws-group-aws-cloud" || zone["data"].(Obj)["iconId"] != nil {
		t.Errorf("group = %v", zone)
	}
	if w := added[6]["width"]; w != 50.0 || added[6]["height"] != nil {
		t.Errorf("sized = %v", added[6])
	}
	if len(Nodes(p)) != 3+len(added) {
		t.Errorf("page has %d nodes", len(Nodes(p)))
	}
	// the first row holds five, then wraps
	empty := page(t, `{"id":"pg","name":"P","nodes":[],"edges":[]}`)
	var specs []NodeSpec
	for range 7 {
		specs = append(specs, NodeSpec{Label: "n"})
	}
	rows, _ := AddNodes(empty, specs)
	if x, y := position(rows[0]); x != 0 || y != 0 {
		t.Errorf("origin of an empty page = %v,%v", x, y)
	}
	if x, y := position(rows[5]); x != 0 || y != 96+60 {
		t.Errorf("sixth node wraps to the next row: %v,%v", x, y)
	}
}

func TestAddEdgesAndDelete(t *testing.T) {
	p := page(t, `{"id":"pg","name":"P","nodes":[
		{"id":"g","type":"group","position":{"x":0,"y":0},"data":{"label":"G"}},
		{"id":"in","type":"icon","parentId":"g","position":{"x":0,"y":0},"data":{"label":"In"}},
		{"id":"a","type":"icon","position":{"x":0,"y":0},"data":{"label":"A"}},
		{"id":"b","type":"icon","position":{"x":0,"y":0},"data":{"label":"B"}}],
		"edges":[{"id":"e0","source":"a","target":"b"}]}`)
	added, errs := AddEdges(p, []EdgeSpec{
		{Source: "a", Target: "b", Label: "calls", Preset: "traces", Dashed: true},
		{Source: "a", Target: "b"},
		{Source: "a", Target: "ghost"},
		{Source: "ghost", Target: "b"},
		{Source: "a", Target: "in", ID: "e0"},
		{Source: "in", Target: "b", Data: Obj{"w": 3}},
	})
	ids := []string{}
	for _, e := range added {
		ids = append(ids, e["id"].(string))
	}
	if strings.Join(ids, ",") != "e-a-b,e-a-b-2,e-in-b" || len(errs) != 3 || errs[0].Message != `node "ghost" not found` || errs[2].Message != `edge id "e0" already exists` {
		t.Errorf("edges %v errors %+v", ids, errs)
	}
	if d := added[0]["data"].(Obj); d["preset"] != "traces" || d["dashed"] != true || added[0]["label"] != "calls" {
		t.Errorf("edge = %v", added[0])
	}
	if _, has := added[1]["data"]; has {
		t.Errorf("an edge with nothing to say has no data: %v", added[1])
	}

	// deleting a group takes its children and every edge touching them
	del := DeleteElements(p, []string{"g", "e0", "ghost"})
	if del.Nodes != 2 || del.Edges != 2 {
		t.Errorf("deleted = %+v", del)
	}
	left := []string{}
	for _, n := range Nodes(p) {
		left = append(left, n["id"].(string))
	}
	if strings.Join(left, ",") != "a,b" {
		t.Errorf("nodes left = %v", left)
	}
	for _, e := range Edges(p) {
		if e["source"] == "in" || e["target"] == "in" {
			t.Errorf("edge %v outlived its node", e)
		}
	}
	if got := DeleteElements(p, []string{"nothing"}); got.Nodes != 0 || got.Edges != 0 {
		t.Errorf("deleting nothing = %+v", got)
	}
}

func TestUpdateNodeAndPickPage(t *testing.T) {
	doc, _ := DecodeDoc(json.RawMessage(`{"pages":[
		{"id":"p1","name":"First","nodes":[{"id":"a","type":"icon","position":{"x":1,"y":2},"data":{"label":"A","keep":1}},{"id":"g","type":"group","position":{"x":0,"y":0},"data":{"label":"G"}}],"edges":[]},
		{"id":"p2","name":"Second","nodes":[],"edges":[]}]}`))
	p1, _ := PickPage(doc, nil)
	label, icon := "Renamed", "redis"
	n, err := UpdateNode(p1, "a", NodePatch{Label: &label, Icon: &icon, Y: fptr(50), Width: fptr(200), Data: Obj{"extra": true}})
	if err != nil {
		t.Fatal(err)
	}
	d := n["data"].(Obj)
	if d["label"] != "Renamed" || d["iconId"] != "redis" || d["keep"] != json.Number("1") || d["extra"] != true || n["width"] != 200.0 {
		t.Errorf("node = %v", n)
	}
	if x, y := position(n); x != 1 || y != 50 {
		t.Errorf("a partial move keeps the other axis: %v,%v", x, y)
	}
	groupIcon := "aws-group-aws-cloud"
	if g, _ := UpdateNode(p1, "g", NodePatch{Icon: &groupIcon}); g["data"].(Obj)["groupIcon"] != groupIcon {
		t.Errorf("a group icon goes to groupIcon: %v", g)
	}
	if _, err := UpdateNode(p1, "g", NodePatch{Icon: fstr("no-such-icon")}); err == nil {
		t.Error("an unknown icon is refused")
	}
	if _, err := UpdateNode(p1, "nope", NodePatch{}); err == nil || err.Error() != `node "nope" not found` {
		t.Errorf("unknown node: %v", err)
	}
	for ref, want := range map[any]string{nil: "p1", "p2": "p2", "Second": "p2", 1.0: "p2", json.Number("0"): "p1"} {
		if p, err := PickPage(doc, ref); err != nil || p["id"] != want {
			t.Errorf("PickPage(%v) = %v, %v", ref, p["id"], err)
		}
	}
	for _, ref := range []any{"nope", 5.0, -1.0, 0.5} {
		if _, err := PickPage(doc, ref); err == nil {
			t.Errorf("PickPage(%v) found a page", ref)
		}
	}
}

func TestDecodeDocKeepsNumbersExact(t *testing.T) {
	doc, err := DecodeDoc(json.RawMessage(`{"big":12345678901234567890,"f":1.50,"pages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(doc)
	if !strings.Contains(string(out), `"big":12345678901234567890`) || !strings.Contains(string(out), `"f":1.50`) {
		t.Errorf("round trip changed numbers: %s", out)
	}
	if _, err := DecodeDoc(json.RawMessage(`[1]`)); err == nil {
		t.Error("an array is not a document")
	}
	if _, err := DecodeDoc(json.RawMessage(`null`)); err == nil {
		t.Error("null is not a document")
	}
}

func TestSummarizeArgs(t *testing.T) {
	long := strings.Repeat("x", 200)
	got := SummarizeArgs(map[string]any{
		"fileId": "fil_1", "dsl": "node a", "data": map[string]any{"k": 1}, "password": "p", "text": long,
		"nodes": []any{1, 2, 3, 4, 5, 6, 7}, "nested": map[string]any{"a": map[string]any{"b": map[string]any{"c": 1}}}, "ok": true,
	})
	for _, bad := range []string{"node a", long, `"p"`} {
		if strings.Contains(got, bad) {
			t.Errorf("summary keeps %q: %s", bad, got)
		}
	}
	for _, want := range []string{`"dsl":"<redacted>"`, `"data":"<redacted>"`, `"password":"<redacted>"`, `"text":"<200 chars>"`, `+2 more`, `"<object>"`, `"fileId":"fil_1"`, `"ok":true`} {
		if !strings.Contains(got, want) {
			t.Errorf("summary lacks %s: %s", want, got)
		}
	}
	if n := len(SummarizeArgs(map[string]any{"a": []any{strings.Repeat("y", 70), strings.Repeat("y", 70), strings.Repeat("y", 70), strings.Repeat("y", 70), strings.Repeat("y", 70), strings.Repeat("y", 70)}, "b": strings.Repeat("z", 79)})); n > 1000 {
		t.Errorf("summary is %d bytes", n)
	}
	if SummarizeArgs(nil) != "null" {
		t.Errorf("nil = %q", SummarizeArgs(nil))
	}
}

// EditFile retries a concurrent save and refuses a viewer; Undo only goes
// back from an MCP change that is the latest.
func TestEditFileAndUndo(t *testing.T) {
	e := newHooks(t)
	d := e.Diagrams
	mark := func(label string) func(Obj) error {
		return func(doc Obj) error {
			p, _ := PickPage(doc, nil)
			AddNodes(p, []NodeSpec{{Label: label}})
			return nil
		}
	}
	nodes := func() int {
		f := must1[DiagramFile](t)(d.GetFile(dctx, alice, e.fid))
		doc, _ := DecodeDoc(f.Data)
		p, _ := PickPage(doc, nil)
		return len(Nodes(p))
	}

	v, err := d.EditFile(dctx, alice, e.fid, "add_nodes", mark("one"))
	if err != nil || v != 2 || nodes() != 1 {
		t.Fatalf("edit: v%d %v", v, err)
	}
	vs := must1[[]diagram.Version](t)(d.Versions(dctx, alice, e.fid))
	if vs[0].Label == nil || *vs[0].Label != "alice via MCP: add_nodes" {
		t.Errorf("label = %v", vs[0].Label)
	}
	// fn errors stop the save
	boom := errors.New("boom")
	if _, err := d.EditFile(dctx, alice, e.fid, "x", func(Obj) error { return boom }); !errors.Is(err, boom) || nodes() != 1 {
		t.Errorf("fn error: %v (%d nodes)", err, nodes())
	}
	// a save that lands between the read and the write is retried on top of it
	raced := false
	v, err = d.EditFile(dctx, alice, e.fid, "add_nodes", func(doc Obj) error {
		if !raced {
			raced = true
			must1[DiagramFile](t)(d.SaveFile(dctx, alice, e.fid, nil, docOf("theirs"), `"2"`))
		}
		return mark("two")(doc)
	})
	if err != nil || v != 4 {
		t.Fatalf("raced edit: v%d %v", v, err)
	}
	f := must1[DiagramFile](t)(d.GetFile(dctx, alice, e.fid))
	if !strings.Contains(string(f.Data), "theirs") || !strings.Contains(string(f.Data), `"two"`) {
		t.Errorf("the retry lost a side: %s", f.Data)
	}
	// a viewer cannot edit
	must1[diagram.Member](t)(d.AddMember(dctx, alice, e.pid, "bob", diagram.RoleViewer))
	if _, err := d.EditFile(dctx, bob, e.fid, "x", mark("no")); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("viewer edit: %v", err)
	}

	// undo
	from, v, err := d.UndoMCPChange(dctx, alice, e.fid)
	if err != nil || from != 3 || v != 5 {
		t.Fatalf("undo: from %d v%d %v", from, v, err)
	}
	if nodes() != 1 || strings.Contains(string(must1[DiagramFile](t)(d.GetFile(dctx, alice, e.fid)).Data), "theirs") == false {
		t.Errorf("after undo: %d nodes", nodes())
	}
	vs = must1[[]diagram.Version](t)(d.Versions(dctx, alice, e.fid))
	if vs[0].Label == nil || *vs[0].Label != "alice via MCP: undo of add_nodes" {
		t.Errorf("undo label = %v", vs[0].Label)
	}
	if _, _, err := d.UndoMCPChange(dctx, alice, e.fid); !errors.Is(err, ErrNothingToUndo) {
		t.Errorf("undoing an undo: %v", err)
	}
	// a named version of somebody's own after the MCP change blocks the undo
	if _, err := d.EditFile(dctx, alice, e.fid, "add_nodes", mark("three")); err != nil {
		t.Fatal(err)
	}
	must1[diagram.Version](t)(d.SaveNamedVersion(dctx, alice, e.fid, "milestone"))
	if _, _, err := d.UndoMCPChange(dctx, alice, e.fid); !errors.Is(err, ErrNothingToUndo) {
		t.Errorf("undo past a named version: %v", err)
	}
	// somebody else's MCP change cannot be undone by another editor
	theirs := project(t, d, alice, "Theirs")
	must1[diagram.Member](t)(d.AddMember(dctx, alice, theirs.Project.ID, "bob", diagram.RoleEditor))
	if _, err := d.EditFile(dctx, alice, theirs.File.ID, "add_nodes", mark("a")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.UndoMCPChange(dctx, bob, theirs.File.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("undo of another user's change: %v", err)
	}
	// nothing MCP made: nothing to undo
	other := project(t, d, alice, "Plain")
	if _, _, err := d.UndoMCPChange(dctx, alice, other.File.ID); !errors.Is(err, ErrNothingToUndo) {
		t.Errorf("file with no MCP change: %v", err)
	}
	if _, _, err := d.UndoMCPChange(dctx, bob, other.File.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("stranger: %v", err)
	}
}

func fstr(s string) *string { return &s }
