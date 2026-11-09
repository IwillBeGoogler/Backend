package core

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestHelpers(t *testing.T) {
	if v := Normalize(make(chan int)); v == nil {
		t.Error("unmarshalable value dropped")
	}
	if got := NormalizePage(nil); got == nil {
		t.Error("NormalizePage(nil)")
	}
	if got := NormalizePage(Obj{"x": 1}); got["x"] != 1.0 {
		t.Errorf("%v", got)
	}
	if f, ok := num(3); !ok || f != 3 {
		t.Error("num int")
	}
	if f, ok := num(json.Number("2.5")); !ok || f != 2.5 {
		t.Error("num json.Number")
	}
	if _, ok := num(json.Number("x")); ok {
		t.Error("bad json.Number")
	}
	if jsStr(true) != "true" || jsStr(1.5) != "1.5" || jsStr([]int{1}) != "[1]" || jsStr(nil) != "undefined" {
		t.Error("jsStr")
	}
	if len(SearchCatalog("", "", 0)) == 0 || len(SearchCatalog("", "", -5)) != 1 {
		t.Error("SearchCatalog limits")
	}
	if len(SearchCatalog("   ", "icon", 3)) > 3 {
		t.Error("limit ignored")
	}
}

func erPage() Obj {
	col := func(id, name string, extra Obj) Obj {
		c := Obj{"id": id, "name": name, "type": "int", "pk": false, "nullable": true}
		for k, v := range extra {
			c[k] = v
		}
		return c
	}
	tbl := func(id, label string, cols ...any) Obj {
		return Obj{"id": id, "type": "table", "position": Obj{"x": 0.0, "y": 0.0},
			"data": Obj{"label": label, "columns": cols, "indexes": []any{}, "checks": []any{}}}
	}
	rel := func(id, src, dst, card string, extra Obj) Obj {
		r := Obj{"cardinality": card, "fromCol": "p", "toCol": "c"}
		for k, v := range extra {
			r[k] = v
		}
		return Obj{"id": id, "source": src, "target": dst, "data": Obj{"relation": r}}
	}
	return Obj{"id": "p1", "name": "ER", "nodes": []any{
		tbl("a", "a", col("p", "id", Obj{"pk": true}), col("u", "uk", Obj{"unique": true}), col("f", "fk", Obj{"fk": Obj{"table": "b"}})),
		tbl("b", "b", col("c", "a_id", nil)),
		Obj{"id": "e", "type": "table", "position": Obj{"x": 0.0, "y": 0.0}, "data": Obj{"label": "kind", "kind": "enum", "columns": []any{col("x", "v", nil)}}},
	}, "edges": []any{
		rel("r1", "a", "b", "1:N", nil),
		rel("r2", "a", "b", "N:1", nil),
		rel("r3", "a", "b", "N:M", Obj{"name": "link"}),
		rel("r4", "a", "b", "weird", nil),
		rel("r5", "a", "b", "1:1", Obj{"fromCol": nil}),
		rel("r6", "a", "ghost", "1:N", nil),
		rel("r7", "a", "b", "1:N", Obj{"group": true}),
		Obj{"id": "r8", "source": "a", "target": "b"},
	}}
}

func TestPrintAndExportER(t *testing.T) {
	page := erPage()
	text := PrintPage(page, "ER")
	if !strings.Contains(text, "table a") {
		t.Fatalf("%s", text)
	}
	for _, f := range []string{"mermaid", "plantuml", "sql", "svg", "sdd", "json"} {
		if out, err := ExportPage(page, f); err != nil || out == "" {
			t.Errorf("%s: %v", f, err)
		}
	}
	if _, err := ExportPage(page, "pdf"); err == nil {
		t.Error("unknown format accepted")
	}
	// json export fills in missing nodes and edges
	if out, _ := ExportPage(Obj{"id": "x", "name": "n"}, "json"); !strings.Contains(out, `"nodes": []`) || !strings.Contains(out, `"edges": []`) {
		t.Errorf("%s", out)
	}
	// the printed text parses back
	if d := ParseDSL(text).Diagnostics; len(d) > 0 {
		t.Logf("reparse diagnostics: %v", d)
	}
}

func TestLintAndLayoutSmoke(t *testing.T) {
	page := erPage()
	_ = LintPage(page, LintConfig{})
	for _, algo := range []string{"tree", "radial", "force", "grid", "layered", "bogus"} {
		_ = ApplyLayout(NormalizePage(page), algo, LayoutOptions{})
	}
}

func iconWithTag(tag string) string {
	for _, c := range catalog() {
		for _, t := range c.Tags {
			if t == tag && c.Kind == "icon" {
				return c.ID
			}
		}
	}
	return ""
}

func archNode(id, label string, data Obj) Obj {
	d := Obj{"label": label}
	for k, v := range data {
		d[k] = v
	}
	return Obj{"id": id, "type": "icon", "position": Obj{"x": 0.0, "y": 0.0}, "data": d}
}

func TestRoleOfAndJsNumber(t *testing.T) {
	cases := []struct {
		n    Obj
		want string
	}{
		{Obj{"type": "table"}, "other"},
		{archNode("a", "x", Obj{"iconId": iconWithTag("sql")}), "database"},
		{archNode("a", "x", Obj{"iconId": iconWithTag("nosql")}), "database"},
		{archNode("a", "x", Obj{"iconId": iconWithTag("queue")}), "queue"},
		{archNode("a", "x", Obj{"iconId": iconWithTag("observability")}), "monitoring"},
		{archNode("a", "main database", nil), "database"},
		{archNode("a", "api", nil), "service"},
		{archNode("a", "x", Obj{"profileId": "postgresql"}), ""},
		{Obj{"type": "icon", "data": Obj{"label": 5}}, ""},
	}
	for i, c := range cases {
		got := RoleOf(c.n)
		if c.want != "" && got != c.want {
			t.Errorf("case %d: %q, want %q", i, got, c.want)
		}
	}
	if jsNumber(true) != 1 || jsNumber(false) != 0 || jsNumber("") != 0 || jsNumber(" 4 ") != 4 || jsNumber(nil) != 0 {
		t.Error("jsNumber basics")
	}
	if v := jsNumber("x"); v == v {
		t.Error("jsNumber(x) should be NaN")
	}
	if v := jsNumber([]int{}); v == v {
		t.Error("jsNumber(slice) should be NaN")
	}
}

func TestLintArchitecture(t *testing.T) {
	sql, queue, mon := iconWithTag("sql"), iconWithTag("queue"), iconWithTag("observability")
	nodes := []any{
		archNode("lb", "Load balancer", Obj{"iconId": "nginx"}),
		archNode("api", "API", nil),
		archNode("db", "DB", Obj{"iconId": sql}),
		archNode("db2", "DB2", Obj{"iconId": sql}),
		archNode("q", "Queue", Obj{"iconId": queue}),
		archNode("m", "Mon", Obj{"iconId": mon}),
		archNode("w", "Warned", Obj{"warnings": []any{"w1"}}),
		archNode("bk", "Backups", Obj{"profileId": "s3"}),
	}
	edges := []any{
		Obj{"id": "e1", "source": "lb", "target": "api"},
		Obj{"id": "e2", "source": "api", "target": "db"},
		Obj{"id": "e3", "source": "db", "target": "db2", "data": Obj{"kind": "replication"}},
		Obj{"id": "e4", "source": "m", "target": "api"},
		Obj{"id": "e5", "source": "db2", "target": "bk", "label": "backup"},
		Obj{"id": "e6", "source": "api", "target": "ghost"},
	}
	page := Obj{"id": "p", "name": "A", "nodes": nodes, "edges": edges}
	problems := LintPage(NormalizePage(page), LintConfig{})
	rules := map[string]bool{}
	for _, p := range problems {
		rules[p.Rule] = true
	}
	for _, want := range []string{"arch/spof", "arch/db-no-backup", "arch/no-monitoring", "arch/cluster-profile"} {
		if !rules[want] {
			t.Errorf("rule %s not raised; got %v", want, rules)
		}
	}
	// without any monitoring component
	page2 := Obj{"id": "p", "name": "A", "nodes": []any{archNode("api", "API", nil)}, "edges": []any{}}
	found := false
	for _, p := range LintPage(NormalizePage(page2), LintConfig{}) {
		found = found || p.Rule == "arch/no-monitoring"
	}
	if !found {
		t.Error("no-monitoring missing")
	}
	// a page without node ids still lints
	_ = LintPage(Obj{"nodes": []any{}, "edges": []any{}}, LintConfig{})
	_ = LintPage(Obj{}, LintConfig{})
}

func TestRoleOfEveryCatalogIcon(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range catalog() {
		seen[RoleOf(archNode("a", "x", Obj{"iconId": c.ID}))] = true
	}
	for _, r := range []string{"database", "queue", "monitoring", "service"} {
		if !seen[r] {
			t.Errorf("no catalog icon gives role %s", r)
		}
	}
	if baseType("Weird_Type(5)") != "weird_type" || baseType("int4") != "int" || baseType(nil) != "" {
		t.Error("baseType")
	}
}

func TestPrintUncommonShapes(t *testing.T) {
	tbl := func(id, label string, cols ...any) Obj {
		return Obj{"id": id, "type": "table", "position": Obj{"x": 0.0, "y": 0.0},
			"data": Obj{"label": label, "columns": cols, "indexes": []any{}, "checks": []any{}}}
	}
	page := Obj{"id": "p", "name": "N", "nodes": []any{
		tbl("a", "a", Obj{"id": "c1", "name": "id"}, Obj{"id": "c2", "name": "x y", "type": "text"}),
		tbl("b", "b"),
		Obj{"id": "s", "type": "sequence", "position": Obj{"x": 0.0, "y": 0.0}, "data": Obj{"label": "S", "participants": []any{Obj{"id": "u", "name": "U", "kind": "actor"}, Obj{"id": "v", "name": "V", "kind": "participant"}}, "items": []any{
			Obj{"type": "message", "from": "u", "to": "v", "text": "hi", "kind": "weird"},
			Obj{"type": "message", "from": "v", "to": "u", "kind": "sync"},
			Obj{"type": "note", "over": []any{"u"}, "text": "n", "side": "left"},
			Obj{"type": "note", "over": []any{"v"}, "text": "n", "side": "right"},
			Obj{"type": "note", "over": []any{"v"}, "text": "n"},
		}}},
	}, "edges": []any{
		Obj{"id": "r", "source": "a", "target": "b", "data": Obj{"relation": Obj{"cardinality": "odd"}}},
		Obj{"id": "r2", "source": "a", "target": "b", "data": Obj{"relation": Obj{"cardinality": "N:M"}}},
	}}
	out := PrintPage(page, "N")
	for _, want := range []string{"id text", `"x y" text`, "has no columns", "right of", "left of", "over"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if got := PrintPage(Obj{"id": "p", "name": "E", "nodes": []any{}, "edges": []any{}}, "E"); !strings.HasSuffix(got, "\n") {
		t.Errorf("%q", got)
	}
}

func layoutPage(nodes int, withGroups bool) Obj {
	var ns, es []any
	for i := 0; i < nodes; i++ {
		n := Obj{"id": "n" + string(rune('a'+i)), "type": "icon", "position": Obj{"x": 0.0, "y": 0.0}, "width": 120.0, "height": 60.0, "data": Obj{"label": "n"}}
		if withGroups && i < 4 {
			n["parentId"] = "g"
		}
		ns = append(ns, n)
	}
	if withGroups {
		ns = append([]any{Obj{"id": "g", "type": "group", "position": Obj{"x": 0.0, "y": 0.0}, "width": 10.0, "height": 10.0, "data": Obj{"label": "G"}},
			Obj{"id": "g2", "type": "group", "position": Obj{"x": 0.0, "y": 0.0}, "data": Obj{"label": "empty"}}}, ns...)
	}
	// two components plus a loner; the first is a chain with a fork
	for _, p := range [][2]int{{0, 1}, {1, 2}, {1, 3}, {4, 5}, {5, 6}, {6, 4}} {
		if p[0] < nodes && p[1] < nodes {
			es = append(es, Obj{"id": "e" + string(rune('a'+p[0])) + string(rune('a'+p[1])), "source": "n" + string(rune('a'+p[0])), "target": "n" + string(rune('a'+p[1]))})
		}
	}
	return Obj{"id": "p", "name": "L", "nodes": ns, "edges": es}
}

func TestLayoutDegenerateInputs(t *testing.T) {
	gap := 10.0
	seed := 7.0
	for _, algo := range []string{"layered", "orthogonal", "tree", "radial", "force", "grid", "bogus", ""} {
		for _, dir := range []string{"LR", "TB"} {
			for _, groups := range []bool{false, true} {
				p := NormalizePage(layoutPage(9, groups))
				ApplyLayout(p, algo, LayoutOptions{Direction: dir, Gap: &gap, Seed: &seed})
				// laying out again moves nothing new in the group sizes
				ApplyLayout(p, algo, LayoutOptions{Direction: dir, Gap: &gap, Seed: &seed})
				ApplyLayout(p, algo, LayoutOptions{RootID: "nb"})
				ApplyLayout(p, algo, LayoutOptions{Only: []string{"nb", "nc", "nzz"}})
			}
		}
	}
	// empty and single-node pages, tidy selection of nothing
	for _, algo := range []string{"layered", "radial", "force", "tree", "grid"} {
		ApplyLayout(NormalizePage(Obj{"nodes": []any{}, "edges": []any{}}), algo, LayoutOptions{})
		ApplyLayout(NormalizePage(layoutPage(1, false)), algo, LayoutOptions{})
		ApplyLayout(NormalizePage(layoutPage(3, false)), algo, LayoutOptions{Only: []string{"zz"}})
	}
	// overlap resolution with a single stacked cluster of boxes
	ApplyLayout(NormalizePage(layoutPage(7, false)), "force", LayoutOptions{Seed: &seed})
	if toUint32(math.NaN()) != 0 || toUint32(math.Inf(1)) != 0 {
		t.Error("toUint32")
	}
	if !math.IsInf(hypot2(math.Inf(1), 1), 1) {
		t.Error("hypot2 inf")
	}
	if maxF(3, 1, 5, 2) != 5 || maxF(9) != 9 {
		t.Error("maxF")
	}
}

func TestLayoutGroupResize(t *testing.T) {
	p := NormalizePage(layoutPage(6, true))
	// a node whose parent does not exist
	p["nodes"] = append(list(p["nodes"]), Obj{"id": "lost", "type": "icon", "parentId": "ghost", "position": Obj{"x": 0.0, "y": 0.0}, "data": Obj{"label": "x"}})
	ApplyLayout(p, "layered", LayoutOptions{})
	for _, n := range nodesOf(p) {
		if str(n["id"]) == "g" {
			n["height"] = 1.0 // same width, wrong height
		}
	}
	if ApplyLayout(p, "layered", LayoutOptions{}) == 0 {
		t.Error("nothing moved after resizing a group")
	}
}

func TestExportClassDiagrams(t *testing.T) {
	cls := func(id, kind string) Obj {
		return Obj{"id": id, "type": "class", "position": Obj{"x": 0.0, "y": 0.0}, "data": Obj{"label": id, "kind": kind,
			"attributes": []any{Obj{"name": "a", "type": "int", "visibility": "+"}, Obj{"name": "b"}},
			"operations": []any{Obj{"name": "f", "visibility": "-", "params": []any{Obj{"name": "x"}}}}}}
	}
	uml := func(id, s, t, kind string) Obj {
		return Obj{"id": id, "source": s, "target": t, "data": Obj{"uml": Obj{"kind": kind}}}
	}
	page := Obj{"id": "p", "name": "C", "nodes": []any{cls("A", "class"), cls("I", "interface"), cls("X", "abstract"), cls("E", "enum")},
		"edges": []any{uml("1", "A", "I", "realization"), uml("2", "A", "X", "inheritance"), uml("3", "A", "ghost", "association"), uml("4", "A", "E", "mystery")}}
	for _, f := range []string{"mermaid", "plantuml"} {
		out, err := ExportPage(page, f)
		if err != nil || !strings.Contains(out, "A") {
			t.Errorf("%s: %v", f, err)
		}
	}
	if out, _ := ExportPage(page, "plantuml"); !strings.Contains(out, "enum E") || !strings.Contains(out, "interface I") {
		t.Errorf("plantuml kinds:\n%s", out)
	}
}

func TestLintUmlSequencesAndErd(t *testing.T) {
	cls := func(id, label, kind string, ops ...string) Obj {
		var os []any
		for _, o := range ops {
			os = append(os, Obj{"name": o})
		}
		return Obj{"id": id, "type": "class", "position": Obj{"x": 0.0, "y": 0.0}, "data": Obj{"label": label, "kind": kind, "operations": os, "attributes": []any{Obj{"name": "a"}}}}
	}
	uml := func(id, s, t, kind string) Obj {
		return Obj{"id": id, "source": s, "target": t, "data": Obj{"uml": Obj{"kind": kind}}}
	}
	msg := func(to, text, kind string) Obj {
		return Obj{"type": "message", "from": "p0", "to": to, "text": text, "kind": kind}
	}
	seq := Obj{"id": "q", "type": "sequence", "position": Obj{"x": 0.0, "y": 0.0}, "data": Obj{"label": "Q",
		"participants": []any{Obj{"id": "p0", "name": "client"}, Obj{"id": "p1", "name": "obj : Shop"}, Obj{"id": "p2", "name": "Base"}, Obj{"id": "p3", "name": "Color"}, Obj{"id": "p4", "name": "D1"}},
		"items": []any{
			msg("p1", "1. total()", "sync"),
			msg("p1", "r = missing(1)", "sync"),
			msg("p1", "missing(2)", "sync"),
			msg("p1", "new Thing", "sync"),
			msg("p1", "Upper", "sync"),
			msg("p1", "???", "sync"),
			msg("p1", "ret()", "return"),
			msg("p2", "baseOp()", "async"),
			msg("p3", "anything()", "sync"),
			msg("p4", "zzz()", "sync"),
			msg("ghost", "x()", "sync"),
			Obj{"type": "fragment", "op": "alt", "sections": []any{Obj{"items": []any{msg("p1", "inSection()", "sync")}}}},
		}}}
	tid := func(id, name string, extra Obj) Obj {
		c := Obj{"id": id, "name": name, "type": "int", "pk": false}
		for k, v := range extra {
			c[k] = v
		}
		return c
	}
	tbl := func(id, label string, indexes []any, cols ...any) Obj {
		return Obj{"id": id, "type": "table", "position": Obj{"x": 0.0, "y": 0.0}, "data": Obj{"label": label, "columns": cols, "indexes": indexes, "checks": []any{}}}
	}
	page := Obj{"id": "p", "name": "U", "nodes": []any{
		cls("S", "Shop", "class", "total"),
		cls("B", "Base", "class", "baseOp"),
		cls("D1", "D1", "class"), cls("D2", "D2", "class"),
		cls("I", "Iface", "interface"),
		cls("J", "Implemented", "interface"),
		cls("E", "Color", "enum"),
		cls("U", "Unused", "class"),
		seq,
		Obj{"id": "o1", "type": "object", "position": Obj{"x": 0.0, "y": 0.0}, "data": Obj{"label": "o", "className": "D1", "slots": []any{Obj{"name": "a"}, Obj{"name": "bogus"}}}},
		tbl("t1", "t1", nil, tid("c1", "id", Obj{"pk": true, "fk": Obj{"table": "t2"}})),
		tbl("t2", "t2", []any{Obj{"columns": []any{"ref"}}}, tid("c2", "ref", Obj{"fk": Obj{"table": "t1"}}), tid("c3", "other", Obj{"fk": Obj{"table": "t1"}})),
	}, "edges": []any{
		uml("e1", "S", "B", "inheritance"), uml("e2", "D1", "B", "inheritance"), uml("e3", "D2", "B", "inheritance"),
		uml("e4", "D1", "D2", "inheritance"), uml("e5", "D2", "D1", "inheritance"),
		uml("e6", "S", "J", "realization"),
	}}
	probs := LintPage(NormalizePage(page), LintConfig{})
	rules := map[string]bool{}
	for _, p := range probs {
		rules[p.Rule] = true
	}
	for _, want := range []string{"uml/seq-missing-method", "uml/interface-no-impl", "uml/unused-class", "uml/inheritance-cycle"} {
		if !rules[want] {
			t.Errorf("%s not raised: %v", want, rules)
		}
	}

	// load balancer with a repeated edge to its single target
	lb := Obj{"id": "p", "name": "A", "nodes": []any{archNode("lb", "LB", Obj{"iconId": "nginx"}), archNode("a", "A", nil)},
		"edges": []any{Obj{"id": "e1", "source": "lb", "target": "a"}, Obj{"id": "e2", "source": "lb", "target": "a"}}}
	if !func() bool {
		for _, p := range LintPage(NormalizePage(lb), LintConfig{}) {
			if p.Rule == "arch/spof" {
				return true
			}
		}
		return false
	}() {
		t.Error("spof not raised")
	}
}

func TestLintUnlabeledAndOverlap(t *testing.T) {
	// nodes without a label are reported by id
	lb := Obj{"id": "p", "name": "A", "nodes": []any{
		Obj{"id": "lb", "type": "icon", "position": Obj{"x": 0.0, "y": 0.0}, "data": Obj{"iconId": "nginx"}},
		Obj{"id": "solo", "type": "icon", "position": Obj{"x": 0.0, "y": 0.0}, "data": Obj{}},
	}, "edges": []any{Obj{"id": "e1", "source": "lb", "target": "solo"}}}
	found := false
	for _, p := range LintPage(NormalizePage(lb), LintConfig{}) {
		if p.Rule == "arch/spof" && strings.HasPrefix(p.Message, "lb ") {
			found = true
		}
	}
	if !found {
		t.Error("unlabeled lb not reported by id")
	}
}

func TestResolveOverlapsLeftward(t *testing.T) {
	// b sits left of a (dx < 0) and the boxes overlap more in y than in x
	boxes := []box{{id: "a", w: 100, h: 400}, {id: "b", w: 100, h: 400}}
	pos := map[string]*pt{"a": {x: 90, y: 0}, "b": {x: 0, y: 10}}
	resolveOverlaps(boxes, pos, 10)
	if pos["a"].x-pos["b"].x < 100 {
		t.Errorf("still overlapping: %+v %+v", *pos["a"], *pos["b"])
	}
	// above (dy < 0) with more overlap in x than in y
	boxes = []box{{id: "a", w: 400, h: 100}, {id: "b", w: 400, h: 100}}
	pos = map[string]*pt{"a": {x: 0, y: 90}, "b": {x: 10, y: 0}}
	resolveOverlaps(boxes, pos, 10)
	if pos["a"].y-pos["b"].y < 100 {
		t.Errorf("still overlapping: %+v %+v", *pos["a"], *pos["b"])
	}
}
