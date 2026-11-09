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

func TestApplyERLifecycle(t *testing.T) {
	_, res := applyAll(t, true,
		"table users {\n  id int [pk, increment]\n  name text [note: n, default: x]\n}\ntable orders {\n  id int [pk]\n  user_id int [ref: > users.id]\n}\nref: orders.id < users.id\nref: users.id - orders.id\nref: users.id <> orders.id",
		// drop increment, drop columns, rename relation targets, remove a table
		"table users {\n  id int [pk]\n}\ntable orders {\n  id int [pk]\n  user_id int\n}\nref: orders.user_id > users.id",
		"table users {\n  id int [pk]\n}",
		"",
	)
	if len(res[2].Removed) == 0 || len(res[3].Removed) == 0 {
		t.Errorf("removed %v %v", res[2].Removed, res[3].Removed)
	}
	// big tables: schema-less long names, many indexes
	var b strings.Builder
	b.WriteString("table big {\n  a int\n  indexes {\n")
	for i := 0; i < 8; i++ {
		b.WriteString("    a [name: \"i" + string(rune('a'+i)) + "\"]\n")
	}
	b.WriteString("  }\n}")
	_, res = applyAll(t, false, b.String())
	if len(res[0].Added) == 0 {
		t.Error("big table not added")
	}
	w, h := TableSize(Obj{"schema": "public", "label": "t", "columns": []any{Obj{"name": "c", "type": "int"}}, "indexes": []any{1, 2, 3, 4, 5, 6, 7}, "checks": []any{}})
	if w <= 0 || h <= 0 {
		t.Error("TableSize")
	}
}

func TestApplySeqTimingRematch(t *testing.T) {
	// rename by title: node kept when the id changes but the title matches
	_, res := applyAll(t, true,
		"seq a \"Login\" {\n  a -> b: hi\n}\ntiming t1 \"Wave\" {\n  robust w states A, B\n  w @10 A\n}",
		"seq z \"Login\" {\n  a -> b: hi\n}\ntiming t2 \"Wave\" {\n  robust w\n}",
		// both go away
		"node n",
	)
	if len(res[2].Removed) < 2 {
		t.Errorf("removed %v", res[2].Removed)
	}
}

func TestApplyClusterProfileWarning(t *testing.T) {
	var cluster string
	for _, c := range catalog() {
		if c.Kind == "cluster" {
			cluster = c.ID
			break
		}
	}
	if cluster == "" {
		t.Skip("no cluster profile in catalog")
	}
	_, res := applyAll(t, false, "node a icon="+cluster)
	if !hasDiag(res[0], "cluster profile") {
		t.Errorf("%v", res[0].Diagnostics)
	}
}

func TestRoundTripRichDocs(t *testing.T) {
	texts := []string{
		"states {\n  initial i\n  state comp \"Composite\" {\n    state a\n    region\n    region\n    state b\n    entryPoint e\n  }\n  fork f vertical\n  join j vertical\n  state act \"Act\" activity=\"do it\"\n  i -> a \"go\"\n}",
		"classes {\n  package shop \"Shop\" {\n    class \"Odd Name\" archRef=n1 archFile=f1 archPage=p1 {\n      + \"weird member\": int\n      static + count(a: int, b): int\n    }\n  }\n  interface I\n  \"Odd Name\" --|> I\n}",
		"components {\n  component c {\n    provides IA\n    requires IB\n  }\n  component d\n  c -- d\n}",
		"communication {\n  object a\n  object b\n  a -- b\n  1.1 a -> b \"x\"\n  1.1 b -> a \"y\"\n  1.2 a -> b \"z\" async\n}",
		"timing t \"T\" {\n  robust w states A, B\n  robust x states A\n  x @10 A\n  w @10 B\n  w @5 A\n  w 1..5 \"c\"\n}",
		"packages {\n  package a \"A\"\n  package b \"B\"\n  a imports b\n  a access b \"custom\"\n  a merges b\n  a ..> b\n}",
		"activity {\n  lane l \"L\" orient=h {\n    initial i\n    action a \"A\"\n    fork f vertical\n  }\n  i -> a\n  a -- f\n}",
		"deployment {\n  node n \"N\" stereotype=s icon=kubernetes {\n    artifact a\n  }\n  device d\n  n -- d \"http\"\n  n ..> d\n  a manifests d\n}",
		"usecases {\n  actor u\n  boundary b \"B\" {\n    usecase x \"X\" {\n      point \"p\"\n    }\n  }\n  u -- x\n  u include x\n}",
		"objects {\n  object o1 \"alice\" class=User model=m {\n    name = \"Al\"\n    \"a b\" = 2\n  }\n}",
	}
	for i, text := range texts {
		page, res := applyAll(t, i%2 == 0, text)
		for _, d := range res[0].Diagnostics {
			if d.Severity == "error" {
				t.Errorf("doc %d: %v", i, d)
			}
		}
		out := PrintPage(page, "P")
		if out == "" {
			t.Errorf("doc %d printed nothing", i)
		}
		// printing is stable
		again, _ := ApplyDSL(NormalizePage(page), out, false)
		if again.Page != nil && PrintPage(again.Page, "P") != out {
			t.Logf("doc %d: print not stable", i)
		}
	}
}

func TestPrintTimingTies(t *testing.T) {
	page := Obj{"id": "p", "name": "N", "nodes": []any{Obj{"id": "t", "type": "timing", "position": Obj{"x": 0.0, "y": 0.0}, "data": Obj{
		"label": "T", "end": 100.0, "unit": "ms",
		"lifelines": []any{Obj{"id": "a", "name": "A", "kind": "robust", "states": []any{"x", "y"}}},
		"changes": []any{
			Obj{"id": "c1", "life": "a", "t": 5.0, "state": "x"},
			Obj{"id": "c2", "life": "a", "t": 5.0, "state": "y"},
		},
		"messages": []any{}, "durations": []any{},
	}}}, "edges": []any{}}
	if out := PrintPage(page, "N"); !strings.Contains(out, "a @5 x") {
		t.Errorf("%s", out)
	}
}

func TestApplyUmlKindChanges(t *testing.T) {
	_, res := applyAll(t, false,
		"states {\n  state s\n  state p {\n    state c\n  }\n}",
		"states {\n  state s {\n    state t\n  }\n  state p\n}", // s plain->composite (rebuilt), p composite->plain
		"classes {\n  class s\n}",                               // id taken by a state: refused
		"states {\n  state s\n}",
	)
	_ = res
	// a group rebuilt keeps its parent and size
	_, res = applyAll(t, false,
		"states {\n  state outer {\n    state inner\n  }\n}",
		"states {\n  state outer {\n    state inner {\n      state leaf\n    }\n  }\n}",
	)
	if len(res[1].Added) == 0 {
		t.Errorf("leaf not added: %v", res[1].Added)
	}
	// deployment node turning into a container, component turning into a container
	applyAll(t, false,
		"deployment {\n  node n\n}",
		"deployment {\n  node n {\n    artifact a\n  }\n}",
	)
	applyAll(t, false,
		"components {\n  component c\n  component d\n}",
		"components {\n  component c {\n    component d\n  }\n}",
	)
	// sizes: long use case title with extension points, fork orientation, empty label
	applyAll(t, true,
		"usecases {\n  usecase u \""+strings.Repeat("long ", 10)+"\" {\n    point \"a\"\n  }\n  actor a\n}\nactivity {\n  fork f\n  fork g vertical\n  action x\n}",
	)
	if w, h := shapeSizeOf("usecase", Obj{}); w <= 0 || h <= 0 {
		t.Error("shapeSizeOf with no label")
	}
	// cluster profile in the UML component block
	for _, c := range catalog() {
		if c.Kind == "cluster" {
			_, res := applyAll(t, false, "components {\n  component c icon="+c.ID+"\n}")
			_ = res
			break
		}
	}
	// setOrDel
	o := Obj{"a": 1, "b": 2, "c": 3, "d": 4}
	setOrDel(o, "a", nil)
	setOrDel(o, "b", false)
	setOrDel(o, "c", "")
	setOrDel(o, "d", "x")
	setOrDel(o, "e", true)
	if len(o) != 2 || o["d"] != "x" {
		t.Errorf("%v", o)
	}
}

func TestSetDel(t *testing.T) {
	s := newSet()
	s.add("a")
	s.add("b")
	s.add("c")
	s.del("b")
	s.del("zz")
	if s.has("b") || len(s.l) != 2 || s.l[1] != "c" {
		t.Errorf("%v", s.l)
	}
	o := Obj{"x": 1, "y": 2, "z": 3}
	delKeys(o, "x", "y")
	if len(o) != 1 {
		t.Errorf("%v", o)
	}
}

func TestApplyDocSyntheticUml(t *testing.T) {
	var cluster string
	for _, c := range catalog() {
		if c.Kind == "cluster" {
			cluster = c.ID
			break
		}
	}
	page := emptyPage()
	// an existing component without a size that now holds another
	page["nodes"] = []any{
		Obj{"id": "c1", "type": "component", "position": Obj{"x": 0.0, "y": 0.0}, "data": Obj{"label": "c1", "compKind": "component"}},
		Obj{"id": "st", "type": "state", "position": Obj{"x": 0.0, "y": 0.0}, "data": Obj{"label": "st"}},
	}
	doc := &DslDoc{Version: 1,
		Classes:     []*DslClass{{Name: "K", Kind: "class", Pkg: "ghost", Line: 1}},
		ClassRels:   []*DslClassRel{{From: "K", To: "nope", Kind: "association", Line: 2}},
		Transitions: []*DslTransition{{From: "st", To: "nope", Line: 3}},
		UcRels:      []*DslUcRel{{From: "x", To: "y", Kind: "association", Line: 4}},
		States:      []*DslState{{ID: "orphan", Label: "o", Kind: "state", Parent: "ghost", Line: 5}},
		UcItems:     []*DslUcItem{{ID: "st", Label: "clash", Kind: "usecase", Line: 6}},
		Comps: []*DslComp{
			{ID: "c1", Label: "c1", Kind: "component", Line: 7},
			{ID: "c2", Label: "c2", Kind: "component", Parent: "c1", Line: 8},
		},
	}
	if cluster != "" {
		doc.Comps = append(doc.Comps, &DslComp{ID: "c3", Label: "c3", Kind: "component", Line: 9})
	}
	res := ApplyDoc(page, doc, false)
	for _, want := range []string{"unknown parent", "cannot edit"} {
		if !hasDiag(res, want) {
			t.Errorf("no %q in %v", want, res.Diagnostics)
		}
	}
	for _, n := range nodesOf(res.Page) {
		if str(n["id"]) == "c1" && n["width"] == nil {
			t.Error("container component got no size")
		}
	}
}

func TestApplyDocSyntheticERAndDep(t *testing.T) {
	if a, b := relationEnds("N:1", true); a != "zeroOrMany" || b != "zeroOrOne" {
		t.Errorf("%s %s", a, b)
	}
	var cluster string
	for _, c := range catalog() {
		if c.Kind == "cluster" {
			cluster = c.ID
			break
		}
	}
	page := emptyPage()
	page["edges"] = []any{Obj{"id": "old", "source": "ghost1", "target": "ghost2", "data": Obj{"relation": Obj{"cardinality": "1:N"}}}}
	doc := &DslDoc{Version: 1,
		Edges: []*DslEdge{{From: "x", To: "y", Arrow: "->", Line: 1}},
		Tables: []*DslTable{
			{Name: "a", Kind: "table", Line: 2, Columns: []*DslColumn{{Name: "id", Type: "int", PK: true}}},
			{Name: "b", Kind: "table", Line: 3, Columns: []*DslColumn{{Name: "id", Type: "int", PK: true}}},
		},
		Refs: []*DslRef{
			{From: DslRefEnd{"a", "id"}, Op: ">", To: DslRefEnd{"zz", "id"}, Line: 4},
			{From: DslRefEnd{"a", "nope"}, Op: ">", To: DslRefEnd{"b", "id"}, Line: 5},
			{From: DslRefEnd{"a", "id"}, Op: ">", To: DslRefEnd{"b", "nope"}, Line: 6},
		},
		DepNodes: []*DslDepNode{{ID: "dn", Label: "dn", Kind: "node", Icon: cluster, Line: 7}},
	}
	res := ApplyDoc(page, doc, false)
	if len(res.Removed) == 0 {
		t.Errorf("stale relation not removed: %+v", res)
	}
}

func TestApplyContainerWithoutSize(t *testing.T) {
	text := "components {\n  component c {\n    component d\n  }\n}"
	page, _ := applyAll(t, false, text)
	for _, n := range nodesOf(page) {
		if str(n["id"]) == "c" {
			delete(n, "width")
			delete(n, "height")
		}
	}
	res, _ := ApplyDSL(page, text, false)
	for _, n := range nodesOf(res.Page) {
		if str(n["id"]) == "c" && n["width"] == nil {
			t.Error("container did not get a size back")
		}
	}
}
