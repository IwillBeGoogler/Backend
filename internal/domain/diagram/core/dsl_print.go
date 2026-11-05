package core

import (
	"fmt"
	"regexp"
	"strings"
)

// PrintPage is the canonical ".sdd" text of a page (core/dsl/print.ts).
// Deterministic: the same page gives the same text.

func isRel(e Obj) bool {
	d := obj(e["data"])
	return truthy(d["relation"]) || truthy(d["uml"])
}

// OwnedArchNodes are the nodes the text format owns: icon/group nodes whose
// whole parent chain is also icon/group.
func OwnedArchNodes(page Obj) []Obj {
	byID := map[string]Obj{}
	for _, n := range nodesOf(page) {
		byID[str(n["id"])] = n
	}
	var ok func(n Obj, seen map[string]bool) bool
	ok = func(n Obj, seen map[string]bool) bool {
		t := str(n["type"])
		id := str(n["id"])
		if (t != "icon" && t != "group") || seen[id] || (t == "group" && groupVariants[jsStr(dataOf(n)["variant"])]) {
			return false
		}
		seen[id] = true
		if str(n["parentId"]) == "" {
			return true
		}
		p, found := byID[str(n["parentId"])]
		return found && ok(p, seen)
	}
	var out []Obj
	for _, n := range nodesOf(page) {
		if ok(n, map[string]bool{}) {
			out = append(out, n)
		}
	}
	return out
}

func ownedArchEdgesWith(nodes Obj, page Obj) []Obj {
	ids := map[string]bool{}
	for _, n := range OwnedArchNodes(nodes) {
		ids[str(n["id"])] = true
	}
	var out []Obj
	for _, e := range edgesOf(page) {
		if ids[str(e["source"])] && ids[str(e["target"])] && !isRel(e) {
			out = append(out, e)
		}
	}
	return out
}

func ownedArchEdges(page Obj) []Obj { return ownedArchEdgesWith(page, page) }

func printArch(page Obj, out *[]string) {
	owned := OwnedArchNodes(page)
	ids := map[string]bool{}
	for _, n := range owned {
		ids[str(n["id"])] = true
	}
	kids := map[string][]Obj{}
	for _, n := range owned {
		key := ""
		if p := str(n["parentId"]); p != "" && ids[p] {
			key = p
		}
		kids[key] = append(kids[key], n)
	}
	attrs := func(n Obj) string {
		d := dataOf(n)
		var a []string
		icon := d["iconId"]
		if icon == nil {
			icon = d["groupIcon"]
		}
		if truthy(icon) {
			a = append(a, "icon="+attrVal(jsStr(icon)))
		}
		if truthy(d["subtitle"]) {
			a = append(a, "sub="+quote(jsStr(d["subtitle"])))
		}
		if len(a) > 0 {
			return " " + strings.Join(a, " ")
		}
		return ""
	}
	var emit func(n Obj, ind string)
	emit = func(n Obj, ind string) {
		kw := "node"
		if str(n["type"]) == "group" {
			kw = "group"
		}
		lbl := ""
		if v := dataOf(n)["label"]; v != nil {
			lbl = jsStr(v)
		}
		head := ind + kw + " " + word(str(n["id"])) + " " + quote(lbl) + attrs(n)
		if kw == "group" {
			*out = append(*out, head+" {")
			for _, k := range kids[str(n["id"])] {
				emit(k, ind+"  ")
			}
			*out = append(*out, ind+"}")
		} else {
			*out = append(*out, head)
		}
	}
	for _, n := range kids[""] {
		emit(n, "")
	}
	edges := ownedArchEdges(page)
	if len(edges) > 0 {
		*out = append(*out, "")
	}
	for _, e := range edges {
		d := obj(e["data"])
		op := "->"
		switch d["arrows"] {
		case "none":
			op = "--"
		case "both":
			op = "<->"
		case "start":
			op = "<-"
		}
		var a []string
		if truthy(d["preset"]) {
			a = append(a, "preset="+attrVal(jsStr(d["preset"])))
		}
		if truthy(d["dashed"]) || d["strokeStyle"] == "dashed" {
			a = append(a, "dashed")
		}
		if truthy(d["color"]) && !truthy(d["preset"]) {
			a = append(a, "color="+attrVal(jsStr(d["color"])))
		}
		line := word(str(e["source"])) + " " + op + " " + word(str(e["target"]))
		if truthy(e["label"]) {
			line += " " + quote(jsStr(e["label"]))
		}
		if len(a) > 0 {
			line += " " + strings.Join(a, " ")
		}
		*out = append(*out, line)
	}
}

var reDefaultPlain = regexp.MustCompile(`^[-\w.()]+$`)

func colAttrs(c Obj, refText string) string {
	var a []string
	if truthy(c["pk"]) {
		a = append(a, "pk")
	}
	if g := c["generated"]; g == "autoincrement" || g == "identity" {
		a = append(a, "increment")
	}
	if !truthy(c["nullable"]) && !truthy(c["pk"]) {
		a = append(a, "not null")
	}
	if truthy(c["unique"]) && !truthy(c["pk"]) {
		a = append(a, "unique")
	}
	if d, has := c["default"]; has && d != nil && d != "" {
		s := jsStr(d)
		if reDefaultPlain.MatchString(s) {
			a = append(a, "default: "+s)
		} else {
			a = append(a, "default: "+quote(s))
		}
	}
	if truthy(c["comment"]) {
		a = append(a, "note: "+quote(jsStr(c["comment"])))
	}
	if refText != "" {
		a = append(a, "ref: "+refText)
	}
	if len(a) > 0 {
		return " [" + strings.Join(a, ", ") + "]"
	}
	return ""
}

var cardOps = map[string]string{"1:N": ">", "N:1": "<", "1:1": "-", "N:M": "<>"}

type relOp struct {
	child, parent       Obj
	childCol, parentCol Obj
	op                  string
	edge                Obj
	inline              bool
}

func findCol(t Obj, id any) Obj {
	for _, c := range objs(dataOf(t)["columns"]) {
		if c["id"] == id {
			return c
		}
	}
	return nil
}

func relationOps(page Obj) []relOp {
	byID := map[string]Obj{}
	for _, n := range nodesOf(page) {
		byID[str(n["id"])] = n
	}
	var out []relOp
	for _, e := range edgesOf(page) {
		r := obj(obj(e["data"])["relation"])
		if r == nil {
			continue
		}
		parent, child := byID[str(e["source"])], byID[str(e["target"])]
		if parent == nil || child == nil || str(parent["type"]) != "table" || str(child["type"]) != "table" || truthy(r["group"]) {
			continue
		}
		childCol, parentCol := findCol(child, r["toCol"]), findCol(parent, r["fromCol"])
		card := jsStr(r["cardinality"])
		inline := false
		if childCol != nil && parentCol != nil {
			if fk := obj(childCol["fk"]); fk != nil && fk["table"] == dataOf(parent)["label"] && (card == "1:N" || card == "1:1") {
				inline = true
			}
		}
		op, ok := cardOps[card]
		if card == "N:1" {
			op = ">"
		} else if !ok {
			op = "undefined"
		}
		out = append(out, relOp{child, parent, childCol, parentCol, op, e, inline})
	}
	return out
}

func printTables(page Obj, out *[]string) {
	var tables []Obj
	for _, n := range nodesOf(page) {
		if str(n["type"]) == "table" {
			tables = append(tables, n)
		}
	}
	if len(tables) == 0 {
		return
	}
	rels := relationOps(page)
	inlineFor := map[string]string{}
	for _, r := range rels {
		if r.inline {
			inlineFor[str(r.child["id"])+":"+jsStr(r.childCol["id"])] = r.op + " " + word(jsStr(dataOf(r.parent)["label"])) + "." + word(jsStr(r.parentCol["name"]))
		}
	}
	for _, t := range tables {
		d := dataOf(t)
		kind := "table"
		if d["tableKind"] == "enum" {
			kind = "enum"
		} else if d["tableKind"] == "view" {
			kind = "view"
		}
		*out = append(*out, "", kind+" "+word(jsStr(d["label"]))+" {")
		for _, c := range objs(d["columns"]) {
			if kind == "enum" {
				*out = append(*out, "  "+word(jsStr(c["name"])))
			} else {
				typ := jsStr(c["type"])
				if !truthy(c["type"]) {
					typ = "text"
				}
				*out = append(*out, "  "+word(jsStr(c["name"]))+" "+typ+colAttrs(c, inlineFor[str(t["id"])+":"+jsStr(c["id"])]))
			}
		}
		if ixs := objs(d["indexes"]); len(ixs) > 0 {
			*out = append(*out, "  indexes {")
			for _, ix := range ixs {
				var cols []string
				for _, c := range list(ix["columns"]) {
					cols = append(cols, jsStr(c))
				}
				name := ""
				if len(cols) > 1 {
					name = "(" + strings.Join(cols, ", ") + ")"
				} else if len(cols) == 1 {
					name = cols[0]
				}
				line := "    " + name
				if truthy(ix["unique"]) || truthy(ix["name"]) {
					var parts []string
					if truthy(ix["unique"]) {
						parts = append(parts, "unique")
					}
					if truthy(ix["name"]) {
						parts = append(parts, "name: "+quote(jsStr(ix["name"])))
					}
					line += " [" + strings.Join(parts, ", ") + "]"
				}
				*out = append(*out, line)
			}
			*out = append(*out, "  }")
		}
		*out = append(*out, "}")
	}
	var standalone []relOp
	for _, r := range rels {
		if !r.inline {
			standalone = append(standalone, r)
		}
	}
	if len(standalone) > 0 {
		*out = append(*out, "")
	}
	for _, r := range standalone {
		if r.childCol == nil || r.parentCol == nil {
			op, ok := cardOps[jsStr(obj(obj(r.edge["data"])["relation"])["cardinality"])]
			if !ok {
				op = "<>"
			}
			*out = append(*out, fmt.Sprintf("// relation %s %s %s has no columns; not expressible", jsStr(dataOf(r.parent)["label"]), op, jsStr(dataOf(r.child)["label"])))
			continue
		}
		*out = append(*out, "ref: "+word(jsStr(dataOf(r.child)["label"]))+"."+word(jsStr(r.childCol["name"]))+" "+r.op+" "+word(jsStr(dataOf(r.parent)["label"]))+"."+word(jsStr(r.parentCol["name"])))
	}
}

var seqArrow = map[string]string{"sync": "->", "async": "->>", "return": "-->", "create": "-->>", "destroy": "-x"}
var seqSwitch = map[string]string{"alt": "else", "opt": "else", "loop": "else", "par": "and", "break": "else", "critical": "option"}

func printSeq(n Obj, out *[]string) {
	d := dataOf(n)
	label := "Sequence"
	if d["label"] != nil {
		label = jsStr(d["label"])
	}
	*out = append(*out, "", "seq "+word(str(n["id"]))+" "+quote(label)+" {")
	if truthy(d["autonumber"]) {
		*out = append(*out, "  autonumber")
	}
	for _, p := range objs(d["participants"]) {
		line := "  " + jsStr(p["kind"]) + " " + word(jsStr(p["id"]))
		if p["name"] != p["id"] {
			line += " as " + quote(jsStr(p["name"]))
		}
		if truthy(p["modelId"]) {
			line += " model=" + jsStr(p["modelId"])
		}
		*out = append(*out, line)
	}
	var walk func(items []Obj, ind string)
	walk = func(items []Obj, ind string) {
		for _, it := range items {
			switch it["type"] {
			case "message":
				arrow, ok := seqArrow[jsStr(it["kind"])]
				if !ok {
					arrow = "->"
				}
				line := ind + word(jsStr(it["from"])) + " " + arrow + " " + word(jsStr(it["to"]))
				if truthy(it["text"]) {
					line += ": " + jsStr(it["text"])
				}
				*out = append(*out, line)
			case "note":
				side := "over"
				if it["side"] == "left" {
					side = "left of"
				} else if it["side"] == "right" {
					side = "right of"
				}
				var over []string
				for _, o := range list(it["over"]) {
					over = append(over, word(jsStr(o)))
				}
				*out = append(*out, ind+"note "+side+" "+strings.Join(over, ", ")+": "+jsStr(it["text"]))
			case "fragment":
				op := jsStr(it["op"])
				for i, s := range objs(it["sections"]) {
					head := op
					if i > 0 {
						head = seqSwitch[op]
					}
					if truthy(s["guard"]) {
						head += " " + jsStr(s["guard"])
					}
					*out = append(*out, ind+head)
					walk(objs(s["items"]), ind+"  ")
				}
				*out = append(*out, ind+"end")
			}
		}
	}
	walk(objs(d["items"]), "  ")
	*out = append(*out, "}")
}

var reBlankRuns = regexp.MustCompile(`\n{3,}`)

// PrintPage is the canonical text of a page; title defaults to its name.
func PrintPage(page Obj, title string) string {
	out := []string{fmt.Sprintf("#!sdd %d", DSLVersion), "diagram " + quote(title), ""}
	printArch(page, &out)
	printTables(page, &out)
	for _, n := range nodesOf(page) {
		if str(n["type"]) == "sequence" {
			printSeq(n, &out)
		}
	}
	printClasses(page, &out)
	printStates(page, &out)
	printUseCases(page, &out)
	printComponents(page, &out)
	printActivity(page, &out)
	printDeployment(page, &out)
	printPackages(page, &out)
	printObjects(page, &out)
	printTiming(page, &out)
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return reBlankRuns.ReplaceAllString(strings.Join(out, "\n"), "\n\n") + "\n"
}
