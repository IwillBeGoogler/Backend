package core

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// BuildResult is a page built from a text (core/dsl/build.ts applyDoc).
type BuildResult struct {
	Page        Obj
	Diagnostics []Diagnostic
	Added       []string
	Removed     []string
}

type oset struct {
	m map[string]bool
	l []string
}

func newSet() *oset { return &oset{m: map[string]bool{}} }
func (s *oset) add(v string) {
	if !s.m[v] {
		s.m[v] = true
		s.l = append(s.l, v)
	}
}
func (s *oset) has(v string) bool { return s.m[v] }
func (s *oset) del(v string) {
	if s.m[v] {
		delete(s.m, v)
		for i, x := range s.l {
			if x == v {
				s.l = append(s.l[:i:i], s.l[i+1:]...)
				break
			}
		}
	}
}
func (s *oset) size() int { return len(s.l) }

func uniq(base string, taken map[string]bool) string {
	id := base
	for i := 2; taken[id]; i++ {
		id = fmt.Sprintf("%s-%d", base, i)
	}
	taken[id] = true
	return id
}

func slug(s string) string { return slugOr(s, "x") }

func u16len(s string) int { return len(utf16.Encode([]rune(s))) }

// ER geometry (mirrors src/lib/er/geometry.ts).
const (
	erHeaderH   = 40.0
	erRowH      = 28.0
	erBottomPad = 6.0
	erMinW      = 220.0
	erMaxW      = 400.0
	erCharW     = 7.1
	erFooterL   = 18.0
	erFooterPad = 10.0
)

// TableSize is the size of a table node from its data.
func TableSize(d Obj) (float64, float64) {
	schema := jsStr(d["schema"])
	sl := 0
	if truthy(d["schema"]) {
		sl = u16len(schema) + 1
	}
	label := jsStr(d["label"])
	cols := objs(d["columns"])
	w := float64(float64(sl)*erCharW) + float64(float64(u16len(label))*8) + 110
	for _, c := range cols {
		t := ""
		if c["type"] != nil {
			t = jsStr(c["type"])
		}
		w = math.Max(w, float64(float64(u16len(jsStr(c["name"]))+min(u16len(t), 22))*erCharW)+96)
	}
	width := jsRound(math.Min(erMaxW, math.Max(erMinW, w))/10) * 10
	n := len(list(d["indexes"])) + len(list(d["checks"]))
	lines := n
	if n > 5 {
		lines = 6
	}
	foot := 0.0
	if lines > 0 {
		foot = erFooterPad + float64(float64(lines)*erFooterL)
	}
	return width, erHeaderH + float64(math.Max(1, float64(len(cols)))*erRowH) + erBottomPad + foot
}

func seqSize(d Obj) (float64, float64) {
	rows := 0
	var walk func(items []Obj)
	walk = func(items []Obj) {
		for _, it := range items {
			rows++
			if it["type"] == "fragment" {
				for _, s := range objs(it["sections"]) {
					rows++
					walk(objs(s["items"]))
				}
			}
		}
	}
	walk(objs(d["items"]))
	return math.Max(320, float64(float64(len(list(d["participants"])))*170)+40), 120 + float64(float64(rows)*46)
}

// timingSize mirrors timingSizeOf in core/dsl/build.ts.
func timingSize(d Obj) (float64, float64) {
	rows := 0.0
	for _, l := range objs(d["lifelines"]) {
		switch str(l["kind"]) {
		case "robust":
			rows += math.Max(2, float64(len(list(l["states"]))))*20 + 12
		case "binary":
			rows += 56
		default:
			rows += 40
		}
		rows += 8
	}
	return 16 + 120 + 560 + 32, 16 + 28 + rows + 36 + 12
}

const relColor = "#7b8499"

func relationEnds(card string, optional bool) (string, string) {
	one := "one"
	if optional {
		one = "zeroOrOne"
	}
	switch card {
	case "1:1":
		return one, "zeroOrOne"
	case "1:N":
		return one, "zeroOrMany"
	case "N:1":
		return "zeroOrMany", one
	}
	return "zeroOrMany", "zeroOrMany"
}

type seqCounter struct{ m, n, f, s int }

func seqItems(items []*DslSeqItem, c *seqCounter) []any {
	out := []any{}
	for _, it := range items {
		switch it.Type {
		case "message":
			c.m++
			out = append(out, Obj{"type": "message", "id": fmt.Sprintf("m%d", c.m), "from": it.From, "to": it.To, "text": it.Text, "kind": it.Kind})
		case "note":
			c.n++
			over := make([]any, len(it.Over))
			for i, o := range it.Over {
				over[i] = o
			}
			n := Obj{"type": "note", "id": fmt.Sprintf("n%d", c.n), "text": it.Text, "over": over}
			if it.Side != "" {
				n["side"] = it.Side
			}
			out = append(out, n)
		default:
			c.f++
			id := fmt.Sprintf("f%d", c.f)
			secs := []any{}
			for _, s := range it.Sections {
				c.s++
				sid := fmt.Sprintf("s%d", c.s)
				secs = append(secs, Obj{"id": sid, "guard": s.Guard, "items": seqItems(s.Items, c)})
			}
			out = append(out, Obj{"type": "fragment", "id": id, "op": it.Op, "sections": secs})
		}
	}
	return out
}

func copyObj(o Obj) Obj {
	n := make(Obj, len(o))
	for k, v := range o {
		n[k] = v
	}
	return n
}

func delKeys(o Obj, ks ...string) {
	for _, k := range ks {
		delete(o, k)
	}
}

// ApplyDoc merges a parsed document into a page: existing ids keep their
// position, size and every property the text does not express. existing may
// be nil (a new page). It does not change existing.
func ApplyDoc(existing Obj, doc *DslDoc, layout bool) BuildResult {
	var diags []Diagnostic
	var page Obj
	if existing != nil {
		existing = NormalizePage(existing)
		page = NormalizePage(existing)
	} else {
		name := "Page 1"
		if doc.Title != nil {
			name = *doc.Title
		}
		page = Obj{"id": "page-1", "name": name, "nodes": []any{}, "edges": []any{}}
	}
	if doc.Title != nil && existing != nil {
		page["name"] = existing["name"]
	}
	added, fresh := newSet(), newSet()
	var removed []string
	hadContent := len(nodesOf(page)) > 0
	warn := func(line int, msg string) { diags = append(diags, Diagnostic{line, 1, msg, "warning"}) }
	derr := func(line int, msg string) { diags = append(diags, Diagnostic{line, 1, msg, "error"}) }
	pushNode := func(n Obj) { page["nodes"] = append(list(page["nodes"]), n) }
	pushEdge := func(e Obj) { page["edges"] = append(list(page["edges"]), e) }

	// ---------- architecture ----------
	owned := OwnedArchNodes(page)
	ownedIDs := map[string]bool{}
	for _, n := range owned {
		ownedIDs[str(n["id"])] = true
	}
	byID := map[string]Obj{}
	for _, n := range nodesOf(page) {
		byID[str(n["id"])] = n
	}
	dslIDs := map[string]bool{}
	for _, n := range doc.Nodes {
		dslIDs[n.ID] = true
	}
	for _, dn := range doc.Nodes {
		other := byID[dn.ID]
		if other != nil && !ownedIDs[dn.ID] {
			derr(dn.Line, fmt.Sprintf(`id "%s" is used by a node the text cannot edit (%s)`, dn.ID, jsStr(other["type"])))
			continue
		}
		if dn.Parent != "" && !dslIDs[dn.Parent] {
			derr(dn.Line, fmt.Sprintf(`unknown parent "%s"`, dn.Parent))
			continue
		}
		var hit *CatalogHit
		if dn.Icon != "" {
			if h, ok := ResolveIcon(dn.Icon); ok {
				hit = &h
			} else {
				warn(dn.Line, fmt.Sprintf(`unknown icon "%s" (autocomplete lists the catalog)`, dn.Icon))
			}
		}
		if hit != nil && hit.Kind == "cluster" {
			warn(dn.Line, fmt.Sprintf(`"%s" is a cluster profile; use an icon id`, dn.Icon))
			hit = nil
		}
		typ := "icon"
		if dn.Kind == "group" {
			typ = "group"
		}
		n := other
		if n == nil {
			n = Obj{"id": dn.ID, "type": typ, "position": Obj{"x": 0.0, "y": 0.0}, "data": Obj{"label": dn.Label}}
			if typ == "group" {
				n["width"], n["height"] = 360.0, 240.0
			}
			pushNode(n)
			byID[dn.ID] = n
			added.add(dn.ID)
			fresh.add(dn.ID)
		} else if str(n["type"]) != typ {
			n["type"] = typ
			if typ == "group" {
				if n["width"] == nil {
					n["width"] = 360.0
				}
				if n["height"] == nil {
					n["height"] = 240.0
				}
			}
		}
		d := dataOf(n)
		d["label"] = dn.Label
		if dn.Sub != "" {
			d["subtitle"] = dn.Sub
		} else {
			delete(d, "subtitle")
		}
		key, otherKey := "iconId", "groupIcon"
		if typ == "group" {
			key, otherKey = "groupIcon", "iconId"
		}
		delete(d, otherKey)
		if hit != nil {
			d[key] = hit.ID
		} else {
			delete(d, key)
		}
		if str(n["parentId"]) != dn.Parent {
			if dn.Parent != "" {
				n["parentId"] = dn.Parent
			} else {
				delete(n, "parentId")
			}
			if !added.has(dn.ID) {
				fresh.add(dn.ID)
			}
		}
	}
	for _, n := range owned {
		if !dslIDs[str(n["id"])] {
			removed = append(removed, str(n["id"]))
		}
	}

	// edges: k-th text edge between a pair keeps the k-th existing edge between that pair
	var pairKeys []string
	oldEdges := map[string][]Obj{}
	srcNodes := page
	if existing != nil {
		srcNodes = existing
	}
	for _, e := range ownedArchEdgesWith(srcNodes, page) {
		k := str(e["source"]) + "|" + str(e["target"])
		if _, ok := oldEdges[k]; !ok {
			pairKeys = append(pairKeys, k)
		}
		oldEdges[k] = append(oldEdges[k], e)
	}
	keepEdge := map[string]bool{}
	edgeIDs := map[string]bool{}
	for _, e := range edgesOf(page) {
		edgeIDs[str(e["id"])] = true
	}
	seenPair := map[string]int{}
	var origEdges map[string]Obj
	if existing != nil {
		origEdges = map[string]Obj{}
		for _, e := range edgesOf(existing) {
			origEdges[str(e["id"])] = e
		}
	}
	for _, de := range doc.Edges {
		if byID[de.From] == nil || byID[de.To] == nil {
			continue
		}
		key := de.From + "|" + de.To
		k := seenPair[key]
		seenPair[key] = k + 1
		var e Obj
		if l := oldEdges[key]; k < len(l) {
			e = l[k]
		}
		if e == nil {
			e = Obj{"id": uniq("e-"+de.From+"-"+de.To, edgeIDs), "source": de.From, "target": de.To}
			pushEdge(e)
			added.add(str(e["id"]))
		}
		keepEdge[str(e["id"])] = true
		if de.Label != "" {
			e["label"] = de.Label
		} else {
			delete(e, "label")
		}
		data := copyObj(obj(e["data"]))
		switch de.Arrow {
		case "--":
			data["arrows"] = "none"
		case "<->":
			data["arrows"] = "both"
		case "<-":
			data["arrows"] = "start"
		default:
			delete(data, "arrows")
		}
		if v := de.Attrs["preset"]; v != "" {
			data["preset"] = v
		} else {
			delete(data, "preset")
		}
		if de.Attrs["dashed"] != "" {
			data["dashed"] = true
			data["strokeStyle"] = "dashed"
		} else {
			delete(data, "dashed")
			if data["strokeStyle"] == "dashed" {
				delete(data, "strokeStyle")
			}
		}
		if v := de.Attrs["color"]; v != "" {
			data["color"] = v
		} else if de.Attrs["preset"] == "" && truthy(data["color"]) && origEdges != nil && truthy(obj(origEdges[str(e["id"])]["data"])["preset"]) {
			delete(data, "color")
		}
		if len(data) > 0 {
			e["data"] = data
		} else {
			delete(e, "data")
		}
	}
	for _, k := range pairKeys {
		for _, e := range oldEdges[k] {
			if !keepEdge[str(e["id"])] {
				removed = append(removed, str(e["id"]))
			}
		}
	}

	// ---------- ER ----------
	var tableNodes []Obj
	tableByName := map[string]Obj{}
	for _, n := range nodesOf(page) {
		if str(n["type"]) == "table" {
			tableNodes = append(tableNodes, n)
			tableByName[jsStr(dataOf(n)["label"])] = n
		}
	}
	dslTableNames := map[string]bool{}
	for _, t := range doc.Tables {
		dslTableNames[t.Name] = true
	}
	nodeIDs := map[string]bool{}
	for _, n := range nodesOf(page) {
		nodeIDs[str(n["id"])] = true
	}
	type resolvedT struct {
		dt   *DslTable
		node Obj
	}
	var resolved []resolvedT
	for _, dt := range doc.Tables {
		node := tableByName[dt.Name]
		if node == nil {
			id := uniq("tbl-"+slug(dt.Name), nodeIDs)
			node = Obj{"id": id, "type": "table", "position": Obj{"x": 0.0, "y": 0.0},
				"data": Obj{"label": dt.Name, "columns": []any{}, "indexes": []any{}, "checks": []any{}, "accent": "#2f6fdb", "collapsed": false}}
			pushNode(node)
			added.add(id)
			fresh.add(id)
		}
		resolved = append(resolved, resolvedT{dt, node})
	}
	for _, n := range tableNodes {
		if !dslTableNames[jsStr(dataOf(n)["label"])] {
			removed = append(removed, str(n["id"]))
		}
	}
	nodeOf := map[string]Obj{}
	for _, r := range resolved {
		nodeOf[r.dt.Name] = r.node
	}

	type rel struct {
		child, parent DslRefEnd
		card          string
		attrs         map[string]string
	}
	norm := func(a DslRefEnd, op string, b DslRefEnd, attrs map[string]string) rel {
		switch op {
		case "<":
			return rel{b, a, "1:N", attrs}
		case "-":
			return rel{a, b, "1:1", attrs}
		case "<>":
			return rel{a, b, "N:M", attrs}
		}
		return rel{a, b, "1:N", attrs}
	}
	var rels []rel
	for _, dt := range doc.Tables {
		for _, c := range dt.Columns {
			if c.Ref != nil {
				rels = append(rels, norm(DslRefEnd{dt.Name, c.Name}, c.Ref.Op, c.Ref.To, nil))
			}
		}
	}
	for _, r := range doc.Refs {
		rels = append(rels, norm(r.From, r.Op, r.To, r.Attrs))
	}
	fkOf := map[string]rel{}
	for _, r := range rels {
		if r.card != "N:M" {
			fkOf[r.child.Table+"."+r.child.Column] = r
		}
	}

	for _, r := range resolved {
		dt, node := r.dt, r.node
		d := dataOf(node)
		oldCols := map[string]Obj{}
		nextID := 0
		for _, c := range objs(d["columns"]) {
			oldCols[jsStr(c["name"])] = c
			if m := regexp.MustCompile(`^c(\d+)$`).FindStringSubmatch(str(c["id"])); m != nil {
				v, _ := strconv.Atoi(m[1])
				nextID = max(nextID, v+1)
			}
		}
		cols := []any{}
		for _, c := range dt.Columns {
			old := oldCols[c.Name]
			col := copyObj(old)
			if old == nil || old["id"] == nil {
				col["id"] = fmt.Sprintf("c%d", nextID)
				nextID++
			}
			col["name"], col["type"], col["pk"] = c.Name, c.Type, c.PK
			col["unique"] = c.Unique && !c.PK
			if dt.Kind == "enum" {
				col["nullable"] = false
			} else {
				col["nullable"] = !c.NotNull && !c.PK
			}
			if c.Default != nil {
				col["default"] = *c.Default
			} else {
				delete(col, "default")
			}
			if c.Increment {
				col["generated"] = "autoincrement"
			} else if col["generated"] == "autoincrement" {
				delete(col, "generated")
			}
			if c.Note != nil && *c.Note != "" {
				col["comment"] = *c.Note
			} else {
				delete(col, "comment")
			}
			if rl, ok := fkOf[dt.Name+"."+c.Name]; ok && nodeOf[rl.parent.Table] != nil {
				fk := copyObj(obj(old["fk"]))
				fk["table"], fk["column"] = rl.parent.Table, rl.parent.Column
				if v := rl.attrs["delete"]; v != "" {
					fk["onDelete"] = strings.ToUpper(v)
				}
				if v := rl.attrs["update"]; v != "" {
					fk["onUpdate"] = strings.ToUpper(v)
				}
				col["fk"] = fk
			} else {
				delete(col, "fk")
			}
			cols = append(cols, col)
		}
		d["columns"] = cols
		d["tableKind"] = dt.Kind
		ixs := []any{}
		for _, ix := range dt.Indexes {
			o := Obj{}
			if ix.Name != "" {
				o["name"] = ix.Name
			}
			cs := make([]any, len(ix.Columns))
			for i, c := range ix.Columns {
				cs[i] = c
			}
			o["columns"], o["unique"] = cs, ix.Unique
			ixs = append(ixs, o)
		}
		d["indexes"] = ixs
		if dt.Note != nil {
			d["comment"] = *dt.Note
		} else {
			delete(d, "comment")
		}
		node["width"], node["height"] = TableSize(d)
	}

	relEdgeIDs := map[string]bool{}
	for _, e := range edgesOf(page) {
		relEdgeIDs[str(e["id"])] = true
	}
	oldRel := map[string]Obj{}
	var oldRelAll []Obj
	for _, e := range edgesOf(page) {
		if r := obj(obj(e["data"])["relation"]); r != nil && !truthy(r["group"]) {
			oldRelAll = append(oldRelAll, e)
		}
	}
	nodeByID2 := map[string]Obj{}
	for _, n := range nodesOf(page) {
		nodeByID2[str(n["id"])] = n
	}
	labelOf := func(n Obj) string {
		if n == nil {
			return "undefined"
		}
		return jsStr(dataOf(n)["label"])
	}
	for _, e := range oldRelAll {
		child, parent := nodeByID2[str(e["target"])], nodeByID2[str(e["source"])]
		cn := ""
		if child != nil {
			if c := findCol(child, obj(obj(e["data"])["relation"])["toCol"]); c != nil {
				cn = jsStr(c["name"])
			}
		}
		oldRel[labelOf(parent)+"|"+labelOf(child)+"|"+cn] = e
	}
	keepRel := map[string]bool{}
	for _, r := range rels {
		child, parent := nodeOf[r.child.Table], nodeOf[r.parent.Table]
		if child == nil || parent == nil {
			continue
		}
		var cc, pc Obj
		for _, c := range objs(dataOf(child)["columns"]) {
			if c["name"] == r.child.Column {
				cc = c
				break
			}
		}
		for _, c := range objs(dataOf(parent)["columns"]) {
			if c["name"] == r.parent.Column {
				pc = c
				break
			}
		}
		if cc == nil || pc == nil {
			continue
		}
		colKey := r.child.Column
		if r.card == "N:M" {
			colKey = ""
		}
		e := oldRel[r.parent.Table+"|"+r.child.Table+"|"+colKey]
		relation := copyObj(obj(obj(obj(e)["data"])["relation"]))
		relation["fromCol"], relation["toCol"], relation["cardinality"] = pc["id"], cc["id"], r.card
		relation["identifying"] = truthy(cc["pk"])
		if r.card == "N:M" {
			relation["optional"] = false
		} else {
			relation["optional"] = truthy(cc["nullable"]) && !truthy(cc["pk"])
		}
		if r.card == "N:M" {
			delete(relation, "fromCol")
			delete(relation, "toCol")
		}
		start, end := relationEnds(r.card, relation["optional"] == true)
		if e == nil {
			e = Obj{"id": uniq("rel-"+slug(r.parent.Table)+"-"+slug(r.child.Table)+"-"+slug(r.child.Column), relEdgeIDs), "source": parent["id"], "target": child["id"]}
			pushEdge(e)
			added.add(str(e["id"]))
		}
		data := copyObj(obj(e["data"]))
		data["relation"] = relation
		if !truthy(data["color"]) {
			data["color"] = relColor
		}
		data["arrows"], data["startMarker"], data["endMarker"] = "none", start, end
		if relation["identifying"] == true {
			data["strokeStyle"], data["dashed"] = "solid", false
		} else {
			data["strokeStyle"], data["dashed"] = "dashed", true
		}
		e["data"] = data
		keepRel[str(e["id"])] = true
	}
	for _, e := range oldRelAll {
		if !keepRel[str(e["id"])] {
			removed = append(removed, str(e["id"]))
		}
	}

	// ---------- sequences ----------
	var seqNodes []Obj
	for _, n := range nodesOf(page) {
		if str(n["type"]) == "sequence" {
			seqNodes = append(seqNodes, n)
		}
	}
	dslSeqIDs := map[string]bool{}
	for _, s := range doc.Seqs {
		dslSeqIDs[s.ID] = true
	}
	matched := map[string]bool{}
	for _, ds := range doc.Seqs {
		var node Obj
		for _, n := range seqNodes {
			if str(n["id"]) == ds.ID && !matched[str(n["id"])] {
				node = n
				break
			}
		}
		if node == nil {
			for _, n := range seqNodes {
				if dataOf(n)["label"] == ds.Title && !matched[str(n["id"])] && !dslSeqIDs[str(n["id"])] {
					node = n
					break
				}
			}
		}
		counter := &seqCounter{}
		accent := any("#2f6fdb")
		if node != nil && dataOf(node)["accent"] != nil {
			accent = dataOf(node)["accent"]
		}
		parts := []any{}
		for _, p := range ds.Participants {
			po := Obj{"id": p.ID, "name": p.Name, "kind": p.Kind}
			if p.ModelID != "" {
				po["modelId"] = p.ModelID
			}
			parts = append(parts, po)
		}
		data := Obj{"label": ds.Title, "accent": accent, "participants": parts, "items": seqItems(ds.Items, counter), "autonumber": ds.Autonumber}
		if node == nil {
			node = Obj{"id": uniq(ds.ID, nodeIDs), "type": "sequence", "position": Obj{"x": 0.0, "y": 0.0}, "data": data}
			pushNode(node)
			added.add(str(node["id"]))
			fresh.add(str(node["id"]))
		} else {
			nd := copyObj(dataOf(node))
			for k, v := range data {
				nd[k] = v
			}
			node["data"] = nd
		}
		matched[str(node["id"])] = true
		node["width"], node["height"] = seqSize(dataOf(node))
	}
	for _, n := range seqNodes {
		if !matched[str(n["id"])] {
			removed = append(removed, str(n["id"]))
		}
	}

	// ---------- timing diagrams ----------
	var tmNodes []Obj
	for _, n := range nodesOf(page) {
		if str(n["type"]) == "timing" {
			tmNodes = append(tmNodes, n)
		}
	}
	dslTmIDs := map[string]bool{}
	for _, t := range doc.Timings {
		dslTmIDs[t.ID] = true
	}
	matchedTm := map[string]bool{}
	for _, dt := range doc.Timings {
		var node Obj
		for _, n := range tmNodes {
			if str(n["id"]) == dt.ID && !matchedTm[str(n["id"])] {
				node = n
				break
			}
		}
		if node == nil {
			for _, n := range tmNodes {
				if dataOf(n)["label"] == dt.Title && !matchedTm[str(n["id"])] && !dslTmIDs[str(n["id"])] {
					node = n
					break
				}
			}
		}
		maxT := 0.0
		for _, c := range dt.Changes {
			maxT = math.Max(maxT, c.T)
		}
		for _, m := range dt.Messages {
			if m.HasT2 {
				maxT = math.Max(maxT, m.T2)
			} else {
				maxT = math.Max(maxT, m.T)
			}
		}
		for _, c := range dt.Constraints {
			maxT = math.Max(maxT, c.T2)
		}
		end := 100.0
		if dt.HasEnd {
			end = dt.End
		} else if maxT > 0 {
			end = maxT
		}
		accent := any("#c2410c")
		if node != nil && dataOf(node)["accent"] != nil {
			accent = dataOf(node)["accent"]
		}
		lifes, changes, msgs, cons := []any{}, []any{}, []any{}, []any{}
		for _, l := range dt.Lifelines {
			states := []any{}
			for _, st := range l.States {
				states = append(states, st)
			}
			lo := Obj{"id": l.ID, "name": l.Name, "kind": l.Kind, "states": states}
			if l.Period != 0 {
				lo["period"] = l.Period
			}
			lifes = append(lifes, lo)
		}
		for i, c := range dt.Changes {
			changes = append(changes, Obj{"id": "c" + strconv.Itoa(i+1), "life": c.Life, "t": c.T, "state": c.State})
		}
		for i, m := range dt.Messages {
			mo := Obj{"id": "m" + strconv.Itoa(i+1), "from": m.From, "to": m.To, "t": m.T, "text": m.Text}
			if m.HasT2 {
				mo["t2"] = m.T2
			}
			msgs = append(msgs, mo)
		}
		for i, c := range dt.Constraints {
			cons = append(cons, Obj{"id": "k" + strconv.Itoa(i+1), "life": c.Life, "t1": c.T1, "t2": c.T2, "text": c.Text})
		}
		data := Obj{"label": dt.Title, "accent": accent, "unit": dt.Unit, "end": end, "lifelines": lifes, "changes": changes, "messages": msgs, "durations": cons}
		if node == nil {
			node = Obj{"id": uniq(dt.ID, nodeIDs), "type": "timing", "position": Obj{"x": 0.0, "y": 0.0}, "data": data}
			pushNode(node)
			added.add(str(node["id"]))
			fresh.add(str(node["id"]))
		} else {
			nd := copyObj(dataOf(node))
			for k, v := range data {
				nd[k] = v
			}
			node["data"] = nd
		}
		matchedTm[str(node["id"])] = true
		node["width"], node["height"] = timingSize(data)
	}
	for _, n := range tmNodes {
		if !matchedTm[str(n["id"])] {
			removed = append(removed, str(n["id"]))
		}
	}

	// ---------- UML: classes, states, use cases, components ----------
	umlOwned := applyUml(page, doc, &umlEnv{added: added, fresh: fresh, removed: &removed, nodeIDs: nodeIDs, err: derr, warn: warn})

	// ---------- removals (a removed node takes its children and edges with it) ----------
	gone := newSet()
	for _, r := range removed {
		gone.add(r)
	}
	for grew := true; grew; {
		grew = false
		for _, n := range nodesOf(page) {
			if p := str(n["parentId"]); p != "" && gone.has(p) && !gone.has(str(n["id"])) && (ownedIDs[str(n["id"])] || umlOwned[str(n["id"])]) {
				gone.add(str(n["id"]))
				grew = true
			}
		}
	}
	var keepN, keepE []Obj
	for _, n := range nodesOf(page) {
		if !gone.has(str(n["id"])) {
			keepN = append(keepN, n)
		}
	}
	for _, e := range edgesOf(page) {
		if !gone.has(str(e["id"])) && !gone.has(str(e["source"])) && !gone.has(str(e["target"])) {
			keepE = append(keepE, e)
		}
	}
	page["nodes"], page["edges"] = anys(keepN), anys(keepE)
	for _, id := range gone.l {
		fresh.del(id)
		added.del(id)
	}

	// parents before children
	nodes := nodesOf(page)
	order := map[string]int{}
	byIDf := map[string]Obj{}
	for i, n := range nodes {
		order[str(n["id"])] = i
		byIDf[str(n["id"])] = n
	}
	var depth func(n Obj, seen int) int
	depth = func(n Obj, seen int) int {
		if str(n["parentId"]) != "" && seen < 32 {
			p := byIDf[str(n["parentId"])]
			if p == nil {
				p = Obj{"id": ""}
			}
			return 1 + depth(p, seen+1)
		}
		return 0
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		di, dj := depth(nodes[i], 0), depth(nodes[j], 0)
		if di != dj {
			return di < dj
		}
		return order[str(nodes[i]["id"])] < order[str(nodes[j]["id"])]
	})
	page["nodes"] = anys(nodes)

	if layout && fresh.size() > 0 {
		placeFresh(page, fresh, hadContent)
	}
	return BuildResult{Page: page, Diagnostics: diags, Added: added.l, Removed: gone.l}
}

// placeFresh gives positions to nodes that have none: lay the new blocks out
// and put them to the right of what is already there.
func placeFresh(page Obj, fresh *oset, hadContent bool) {
	nodes := nodesOf(page)
	if !hadContent || fresh.size() == len(nodes) {
		ApplyLayout(page, "layered", LayoutOptions{})
		return
	}
	byID := map[string]Obj{}
	for _, n := range nodes {
		byID[str(n["id"])] = n
	}
	var isFreshChain func(n Obj) bool
	isFreshChain = func(n Obj) bool {
		if fresh.has(str(n["id"])) {
			return true
		}
		p := byID[str(n["parentId"])]
		return p != nil && isFreshChain(p)
	}
	var blockNodes []Obj
	blockIDs := map[string]bool{}
	for _, n := range nodes {
		if isFreshChain(n) {
			blockNodes = append(blockNodes, n)
			blockIDs[str(n["id"])] = true
		}
	}
	var settled []Obj
	for _, n := range nodes {
		if !blockIDs[str(n["id"])] && str(n["parentId"]) == "" {
			settled = append(settled, n)
		}
	}
	rects := AbsRects(Obj{"nodes": anys(settled)})
	right, top := 0.0, 0.0
	if len(rects) > 0 {
		right, top = math.Inf(-1), math.Inf(1)
		for _, r := range rects {
			right, top = math.Max(right, r.x+r.w), math.Min(top, r.y)
		}
	}
	for _, n := range blockNodes {
		pid := str(n["parentId"])
		if pid == "" || blockIDs[pid] {
			continue
		}
		var sibs []Obj
		for _, x := range nodes {
			if str(x["parentId"]) == pid && !blockIDs[str(x["id"])] {
				sibs = append(sibs, x)
			}
		}
		parent := byID[pid]
		x, y := 24.0, 56.0
		if len(sibs) > 0 {
			mx, my := math.Inf(-1), math.Inf(1)
			for _, s := range sibs {
				sx, sy := posOf(s)
				sw, _ := nodeSize(s)
				mx, my = math.Max(mx, sx+sw), math.Min(my, sy)
			}
			x, y = mx+30, my
		}
		setPos(n, x, y)
		nw, _ := nodeSize(n)
		need := x + nw + 24
		if pw, _ := nodeSize(parent); need > pw {
			parent["width"] = need
		}
		delete(blockIDs, str(n["id"]))
	}
	var subNodes []Obj
	for _, n := range blockNodes {
		if blockIDs[str(n["id"])] || (str(n["parentId"]) != "" && blockIDs[str(n["parentId"])]) {
			subNodes = append(subNodes, n)
		}
	}
	var subEdges []Obj
	for _, e := range edgesOf(page) {
		if blockIDs[str(e["source"])] && blockIDs[str(e["target"])] {
			subEdges = append(subEdges, e)
		}
	}
	if len(subNodes) == 0 {
		return
	}
	for _, n := range subNodes {
		if p := str(n["parentId"]); p != "" && !blockIDs[p] {
			delete(n, "parentId")
		}
	}
	sub := Obj{"id": page["id"], "name": page["name"], "nodes": anys(subNodes), "edges": anys(subEdges)}
	ApplyLayout(sub, "layered", LayoutOptions{})
	minX, minY := math.Inf(1), math.Inf(1)
	for _, n := range subNodes {
		if str(n["parentId"]) == "" {
			x, y := posOf(n)
			minX, minY = math.Min(minX, x), math.Min(minY, y)
		}
	}
	for _, n := range subNodes {
		if str(n["parentId"]) == "" {
			x, y := posOf(n)
			setPos(n, x-minX+right+120, y-minY+top)
		}
	}
}

// ApplyDSL parses text and merges it into page (nil: a new page). Errors
// never fail: they come back as diagnostics.
func ApplyDSL(page Obj, text string, layout bool) (BuildResult, []Diagnostic) {
	pr := ParseDSL(text)
	b := ApplyDoc(page, pr.Doc, layout)
	all := append(append([]Diagnostic{}, pr.Diagnostics...), b.Diagnostics...)
	b.Diagnostics = all
	return b, pr.Diagnostics
}
