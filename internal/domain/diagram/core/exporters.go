package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
)

// Text exporters (core/exporters.ts).

// ExportFormats are the formats of ExportPage.
var ExportFormats = []string{"mermaid", "plantuml", "sql", "svg", "json", "sdd"}

var (
	reNotID   = regexp.MustCompile(`[^A-Za-z0-9_]`)
	reNotWord = regexp.MustCompile(`\W+`)
)

func mID(s string) string { return reNotID.ReplaceAllString(s, "_") }
func mText(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, `"`, "#quot;"), "\n", " ")
}
func xmlEsc(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}
func lbl(n Obj) string { return labelStr(dataOf(n)["label"]) }

func ofType(p Obj, t string) []Obj {
	var out []Obj
	for _, n := range nodesOf(p) {
		if str(n["type"]) == t {
			out = append(out, n)
		}
	}
	return out
}

func childrenIndex(p Obj) map[string][]Obj {
	kids := map[string][]Obj{}
	for _, n := range nodesOf(p) {
		kids[str(n["parentId"])] = append(kids[str(n["parentId"])], n)
	}
	return kids
}

func mermaidFlow(p Obj) string {
	lines := []string{"flowchart LR"}
	kids := childrenIndex(p)
	var emit func(n Obj, ind string)
	emit = func(n Obj, ind string) {
		id := str(n["id"])
		if k, ok := kids[id]; ok && id != "" {
			lines = append(lines, fmt.Sprintf(`%ssubgraph %s["%s"]`, ind, mID(id), mText(lbl(n))))
			for _, c := range k {
				emit(c, ind+"  ")
			}
			lines = append(lines, ind+"end")
		} else {
			lines = append(lines, fmt.Sprintf(`%s%s["%s"]`, ind, mID(id), mText(lbl(n))))
		}
	}
	for _, n := range kids[""] {
		emit(n, "  ")
	}
	for _, e := range edgesOf(p) {
		l := ""
		if truthy(e["label"]) {
			l = "|" + mText(jsStr(e["label"])) + "|"
		}
		lines = append(lines, fmt.Sprintf("  %s -->%s %s", mID(str(e["source"])), l, mID(str(e["target"]))))
	}
	return strings.Join(lines, "\n") + "\n"
}

var cardArrows = map[string]string{"1:1": "||--||", "1:N": "||--o{", "N:1": "}o--||", "N:M": "}o--o{"}

func mermaidEr(p Obj) string {
	lines := []string{"erDiagram"}
	name := map[string]string{}
	for _, n := range nodesOf(p) {
		name[str(n["id"])] = lbl(n)
	}
	for _, n := range ofType(p, "table") {
		lines = append(lines, "  "+mID(lbl(n))+" {")
		for _, c := range objs(dataOf(n)["columns"]) {
			t := "text"
			if truthy(c["type"]) {
				t = jsStr(c["type"])
			}
			suffix := ""
			switch {
			case truthy(c["pk"]):
				suffix = " PK"
			case truthy(c["fk"]):
				suffix = " FK"
			case truthy(c["unique"]):
				suffix = " UK"
			}
			lines = append(lines, "    "+reNotWord.ReplaceAllString(t, "_")+" "+mID(jsStr(c["name"]))+suffix)
		}
		lines = append(lines, "  }")
	}
	for _, e := range edgesOf(p) {
		r := obj(obj(e["data"])["relation"])
		_, hs := name[str(e["source"])]
		_, ht := name[str(e["target"])]
		if r == nil || !hs || !ht {
			continue
		}
		card, ok := cardArrows[jsStr(r["cardinality"])]
		if !ok {
			card = "}o--||"
		}
		rn := any("rel")
		if r["name"] != nil {
			rn = r["name"]
		} else if r["fromCol"] != nil {
			rn = r["fromCol"]
		}
		lines = append(lines, fmt.Sprintf(`  %s %s %s : "%s"`, mID(name[str(e["source"])]), card, mID(name[str(e["target"])]), mText(jsStr(rn))))
	}
	return strings.Join(lines, "\n") + "\n"
}

var umlArrowsMermaid = map[string]string{"inheritance": "<|--", "realization": "<|..", "composition": "*--", "aggregation": "o--", "association": "-->", "dependency": "..>"}

func umlEdge(e Obj, label map[string]string) (string, string, string, bool) {
	k := jsStr(obj(obj(e["data"])["uml"])["kind"])
	a, ok := umlArrowsMermaid[k]
	if !ok {
		return "", "", "", false
	}
	s, t := str(e["source"]), str(e["target"])
	if k == "inheritance" || k == "realization" {
		s, t = t, s
	}
	pick := func(id string) string {
		if l, ok := label[id]; ok {
			return l
		}
		return id
	}
	return pick(s), a, pick(t), true
}

func mermaidClass(p Obj) string {
	lines := []string{"classDiagram"}
	for _, n := range ofType(p, "class") {
		d := dataOf(n)
		lines = append(lines, "  class "+mID(lbl(n))+" {")
		for _, a := range objs(d["attributes"]) {
			t := ""
			if truthy(a["type"]) {
				t = " " + jsStr(a["type"])
			}
			lines = append(lines, "    "+labelStr(a["visibility"])+jsStr(a["name"])+t)
		}
		for _, o := range objs(d["operations"]) {
			var ps []string
			for _, q := range objs(o["params"]) {
				ps = append(ps, jsStr(q["name"]))
			}
			lines = append(lines, "    "+labelStr(o["visibility"])+jsStr(o["name"])+"("+strings.Join(ps, ", ")+")")
		}
		lines = append(lines, "  }")
	}
	label := map[string]string{}
	for _, n := range nodesOf(p) {
		label[str(n["id"])] = lbl(n)
	}
	for _, e := range edgesOf(p) {
		if s, a, t, ok := umlEdge(e, label); ok {
			lines = append(lines, "  "+mID(s)+" "+a+" "+mID(t))
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

func toMermaid(p Obj) string {
	if len(ofType(p, "table")) > 0 {
		return mermaidEr(p)
	}
	if len(ofType(p, "class")) > 0 {
		return mermaidClass(p)
	}
	return mermaidFlow(p)
}

func toPlantUML(p Obj) string {
	lines := []string{"@startuml"}
	label := map[string]string{}
	for _, n := range nodesOf(p) {
		label[str(n["id"])] = lbl(n)
	}
	if classes := ofType(p, "class"); len(classes) > 0 {
		for _, n := range classes {
			d := dataOf(n)
			kw := "class"
			switch d["kind"] {
			case "interface":
				kw = "interface"
			case "abstract":
				kw = "abstract class"
			case "enum":
				kw = "enum"
			}
			lines = append(lines, kw+" "+mID(lbl(n))+" {")
			for _, a := range objs(d["attributes"]) {
				t := ""
				if truthy(a["type"]) {
					t = " : " + jsStr(a["type"])
				}
				lines = append(lines, "  "+labelStr(a["visibility"])+jsStr(a["name"])+t)
			}
			for _, o := range objs(d["operations"]) {
				lines = append(lines, "  "+labelStr(o["visibility"])+jsStr(o["name"])+"()")
			}
			lines = append(lines, "}")
		}
		for _, e := range edgesOf(p) {
			if s, a, t, ok := umlEdge(e, label); ok {
				lines = append(lines, mID(s)+" "+a+" "+mID(t))
			}
		}
	} else {
		kids := childrenIndex(p)
		var emit func(n Obj, ind string)
		emit = func(n Obj, ind string) {
			id := str(n["id"])
			name := strings.ReplaceAll(lbl(n), `"`, "'")
			if k, ok := kids[id]; ok && id != "" {
				lines = append(lines, fmt.Sprintf(`%srectangle "%s" as %s {`, ind, name, mID(id)))
				for _, c := range k {
					emit(c, ind+"  ")
				}
				lines = append(lines, ind+"}")
			} else {
				lines = append(lines, fmt.Sprintf(`%srectangle "%s" as %s`, ind, name, mID(id)))
			}
		}
		for _, n := range kids[""] {
			emit(n, "")
		}
		for _, e := range edgesOf(p) {
			l := ""
			if truthy(e["label"]) {
				l = " : " + jsStr(e["label"])
			}
			lines = append(lines, mID(str(e["source"]))+" --> "+mID(str(e["target"]))+l)
		}
	}
	lines = append(lines, "@enduml")
	return strings.Join(lines, "\n") + "\n"
}

func sqlQ(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

func toSQL(p Obj) string {
	tables := ofType(p, "table")
	if len(tables) == 0 {
		return "-- no tables on this page\n"
	}
	byName := map[string]Obj{}
	for _, t := range tables {
		byName[lbl(t)] = t
	}
	deps := func(t Obj) []string {
		var out []string
		for _, c := range objs(dataOf(t)["columns"]) {
			if fk := obj(c["fk"]); fk != nil {
				n := jsStr(fk["table"])
				if truthy(fk["table"]) && n != lbl(t) && byName[n] != nil {
					out = append(out, n)
				}
			}
		}
		return out
	}
	done := map[string]bool{}
	var order []Obj
	var visit func(t Obj, stack map[string]bool)
	visit = func(t Obj, stack map[string]bool) {
		l := lbl(t)
		if done[l] || stack[l] {
			return
		}
		stack[l] = true
		for _, d := range deps(t) {
			visit(byName[d], stack)
		}
		done[l] = true
		order = append(order, t)
	}
	for _, t := range tables {
		visit(t, map[string]bool{})
	}
	var out []string
	for _, t := range order {
		cols := objs(dataOf(t)["columns"])
		var lines, pks []string
		for _, c := range cols {
			typ := "text"
			if truthy(c["type"]) {
				typ = jsStr(c["type"])
			}
			s := "  " + sqlQ(jsStr(c["name"])) + " " + typ
			if !truthy(c["nullable"]) && !truthy(c["pk"]) {
				s += " NOT NULL"
			}
			if truthy(c["unique"]) && !truthy(c["pk"]) {
				s += " UNIQUE"
			}
			if truthy(c["default"]) {
				s += " DEFAULT " + jsStr(c["default"])
			}
			lines = append(lines, s)
			if truthy(c["pk"]) {
				pks = append(pks, sqlQ(jsStr(c["name"])))
			}
		}
		if len(pks) > 0 {
			lines = append(lines, "  PRIMARY KEY ("+strings.Join(pks, ", ")+")")
		}
		for _, c := range cols {
			if fk := obj(c["fk"]); fk != nil {
				s := fmt.Sprintf("  FOREIGN KEY (%s) REFERENCES %s (%s)", sqlQ(jsStr(c["name"])), sqlQ(jsStr(fk["table"])), sqlQ(jsStr(fk["column"])))
				if truthy(fk["onDelete"]) {
					s += " ON DELETE " + jsStr(fk["onDelete"])
				}
				lines = append(lines, s)
			}
		}
		out = append(out, fmt.Sprintf("CREATE TABLE %s (\n%s\n);", sqlQ(lbl(t)), strings.Join(lines, ",\n")))
		for _, ix := range objs(dataOf(t)["indexes"]) {
			var cs []string
			for _, c := range list(ix["columns"]) {
				cs = append(cs, jsStr(c))
			}
			name := lbl(t) + "_" + strings.Join(cs, "_") + "_idx"
			if ix["name"] != nil {
				name = jsStr(ix["name"])
			}
			u := ""
			if truthy(ix["unique"]) {
				u = "UNIQUE "
			}
			var qs []string
			for _, c := range cs {
				qs = append(qs, sqlQ(c))
			}
			out = append(out, fmt.Sprintf("CREATE %sINDEX %s ON %s (%s);", u, sqlQ(name), sqlQ(lbl(t)), strings.Join(qs, ", ")))
		}
	}
	return strings.Join(out, "\n\n") + "\n"
}

func jn(f float64) string {
	if f == 0 {
		return "0"
	}
	return jsNum(f)
}

func toSVG(p Obj) string {
	rects := AbsRects(p)
	nodes := nodesOf(p)
	const pad = 24.0
	minX, minY, w, h := 0.0, 0.0, 100.0, 100.0
	if len(rects) > 0 {
		mnx, mny, mxx, mxy := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
		for _, r := range rects {
			mnx, mny = math.Min(mnx, r.x), math.Min(mny, r.y)
			mxx, mxy = math.Max(mxx, r.x+r.w), math.Max(mxy, r.y+r.h)
		}
		minX, minY = mnx-pad, mny-pad
		w, h = mxx+pad-minX, mxy+pad-minY
	}
	out := []string{
		fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="%s %s %s %s" width="%s" height="%s" font-family="system-ui, sans-serif" font-size="13">`, jn(minX), jn(minY), jn(w), jn(h), jn(w), jn(h)),
		`<defs><marker id="a" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M0 0L10 5L0 10z" fill="#2d3340"/></marker></defs>`,
		fmt.Sprintf(`<rect x="%s" y="%s" width="%s" height="%s" fill="#fff"/>`, jn(minX), jn(minY), jn(w), jn(h)),
	}
	hasKid := map[string]bool{}
	for _, n := range nodes {
		if pid := str(n["parentId"]); pid != "" {
			hasKid[pid] = true
		}
	}
	isGroup := func(n Obj) bool { return hasKid[str(n["id"])] || str(n["type"]) == "group" }
	var order []Obj
	for _, n := range nodes {
		if isGroup(n) {
			order = append(order, n)
		}
	}
	for _, n := range nodes {
		if !isGroup(n) {
			order = append(order, n)
		}
	}
	for _, n := range order {
		r := rects[str(n["id"])]
		g := isGroup(n)
		fill, dash := "#ffffff", ""
		if g {
			fill, dash = "#f5f7fa", `stroke-dasharray="6 4" `
		}
		out = append(out, fmt.Sprintf(`<rect x="%s" y="%s" width="%s" height="%s" rx="8" fill="%s" stroke="#2d3340" %sstroke-width="1.5"/>`, jn(r.x), jn(r.y), jn(r.w), jn(r.h), fill, dash))
		tx, ty, anchor := r.x+r.w/2, r.y+r.h/2+4, "middle"
		if g {
			tx, ty, anchor = r.x+12, r.y+22, "start"
		}
		out = append(out, fmt.Sprintf(`<text x="%s" y="%s" text-anchor="%s" fill="#1f2430">%s</text>`, jn(tx), jn(ty), anchor, xmlEsc(lbl(n))))
	}
	for _, e := range edgesOf(p) {
		a, aok := rects[str(e["source"])]
		b, bok := rects[str(e["target"])]
		if !aok || !bok {
			continue
		}
		ax, ay, bx, by := a.x+a.w/2, a.y+a.h/2, b.x+b.w/2, b.y+b.h/2
		clip := func(r rect, tx, ty, cx, cy float64) (float64, float64) {
			dx, dy := tx-cx, ty-cy
			s := math.Inf(1)
			if dx != 0 {
				s = r.w / 2 / math.Abs(dx)
			}
			if dy != 0 {
				s = math.Min(s, r.h/2/math.Abs(dy))
			}
			s = math.Min(s, 1)
			return cx + float64(dx*s), cy + float64(dy*s)
		}
		x1, y1 := clip(a, bx, by, ax, ay)
		x2, y2 := clip(b, ax, ay, bx, by)
		out = append(out, fmt.Sprintf(`<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="#2d3340" stroke-width="1.5" marker-end="url(#a)"/>`, jn(x1), jn(y1), jn(x2), jn(y2)))
		if truthy(e["label"]) {
			out = append(out, fmt.Sprintf(`<text x="%s" y="%s" text-anchor="middle" fill="#2d3340">%s</text>`, jn((x1+x2)/2), jn((y1+y2)/2-4), xmlEsc(jsStr(e["label"]))))
		}
	}
	out = append(out, "</svg>")
	return strings.Join(out, "\n") + "\n"
}

func toJSON(p Obj) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	nodes, edges := p["nodes"], p["edges"]
	if nodes == nil {
		nodes = []any{}
	}
	if edges == nil {
		edges = []any{}
	}
	_ = enc.Encode(Obj{"id": p["id"], "name": p["name"], "nodes": nodes, "edges": edges})
	return strings.TrimRight(buf.String(), "\n")
}

// ExportPage renders a page (normalized JSON) in one of ExportFormats.
func ExportPage(page Obj, format string) (string, error) {
	switch format {
	case "mermaid":
		return toMermaid(page), nil
	case "plantuml":
		return toPlantUML(page), nil
	case "sql":
		return toSQL(page), nil
	case "svg":
		return toSVG(page), nil
	case "sdd":
		return PrintPage(page, jsStr(page["name"])), nil
	case "json":
		return toJSON(page), nil
	}
	return "", fmt.Errorf("unknown export format %q", format)
}
