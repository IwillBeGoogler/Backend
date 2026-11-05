package core

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Printers of the UML blocks and the ownership rules shared with the builder (core/dsl/print2.ts).

var (
	classRelKinds = map[string]bool{"inheritance": true, "realization": true, "composition": true, "aggregation": true, "association": true, "dependency": true}
	ucRelKinds    = map[string]bool{"association": true, "include": true, "extend": true, "generalization": true}
	groupVariants = map[string]bool{"package": true, "composite": true, "boundary": true, "lane": true}
	depLinkKinds  = map[string]bool{"communication": true, "deploy": true, "manifest": true}
	pkgRelKinds   = map[string]bool{"import": true, "access": true, "merge": true, "dependency": true}
	reSlotName    = regexp.MustCompile(`^[^\s=]+$`)
	reAttrValStar = regexp.MustCompile(`^[A-Za-z0-9_.#/+*-]+$`)
	reMemberName  = regexp.MustCompile(`^[^\s:(=]+$`)
)

func attrValStar(s string) string {
	if reAttrValStar.MatchString(s) {
		return s
	}
	return quote(s)
}

func umlOf(e Obj) Obj { return obj(obj(e["data"])["uml"]) }

func isGroupOf(n Obj, variant string) bool {
	return str(n["type"]) == "group" && dataOf(n)["variant"] == variant
}
func isStateLike(n Obj) bool { return str(n["type"]) == "state" || isGroupOf(n, "composite") }
func isUcNode(n Obj) bool    { return str(n["type"]) == "usecase" }
func isIfaceNode(n Obj) bool {
	if str(n["type"]) != "component" {
		return false
	}
	k := dataOf(n)["compKind"]
	return k == "provided" || k == "required"
}
func isCompNode(n Obj) bool { return str(n["type"]) == "component" && !isIfaceNode(n) }
func isActNode(n Obj) bool  { return str(n["type"]) == "activity" }
func isActLike(n Obj) bool  { return isActNode(n) || isGroupOf(n, "lane") }
func isDepNode(n Obj) bool  { return str(n["type"]) == "deployment" }
func isObjNode(n Obj) bool  { return str(n["type"]) == "object" }

// actLabelDefault is the label the text implies when none is written: empty for pseudo elements, else the id.
func actLabelDefault(kind, id string) string {
	if actNoDefault[kind] {
		return ""
	}
	return id
}

func nodeIDSet(page Obj, pred func(Obj) bool) map[string]bool {
	ids := map[string]bool{}
	for _, n := range nodesOf(page) {
		if pred(n) {
			ids[str(n["id"])] = true
		}
	}
	return ids
}

func ownedEdgesBy(page Obj, pred func(Obj) bool, kinds func(kind string) bool) []Obj {
	ids := nodeIDSet(page, pred)
	var out []Obj
	for _, e := range edgesOf(page) {
		if ids[str(e["source"])] && ids[str(e["target"])] && kinds(str(umlOf(e)["kind"])) {
			out = append(out, e)
		}
	}
	return out
}

func ownedClassEdges(page Obj) []Obj {
	return ownedEdgesBy(page, func(n Obj) bool { return str(n["type"]) == "class" }, func(k string) bool { return classRelKinds[k] })
}
func ownedTransitions(page Obj) []Obj {
	return ownedEdgesBy(page, isStateLike, func(k string) bool { return k == "transition" })
}
func ownedUcEdges(page Obj) []Obj {
	return ownedEdgesBy(page, isUcNode, func(k string) bool { return ucRelKinds[k] })
}
func ownedCompEdges(page Obj) []Obj {
	return ownedEdgesBy(page, isCompNode, func(k string) bool { return k == "dependency" || k == "assembly" })
}

func ownedActEdges(page Obj) []Obj {
	return ownedEdgesBy(page, isActNode, func(k string) bool { return k == "transition" || k == "note" })
}
func ownedDepEdges(page Obj) []Obj {
	return ownedEdgesBy(page, isDepNode, func(k string) bool { return depLinkKinds[k] })
}

// ownedPkgEdges are the relations between package groups.
func ownedPkgEdges(page Obj) []Obj {
	return ownedEdgesBy(page, func(n Obj) bool { return isGroupOf(n, "package") }, func(k string) bool { return pkgRelKinds[k] })
}
func ownedObjEdges(page Obj) []Obj {
	return ownedEdgesBy(page, isObjNode, func(k string) bool { return k == "link" })
}

func findPort(n Obj, id any) Obj {
	for _, p := range objs(dataOf(n)["ports"]) {
		if p["id"] == id {
			return p
		}
	}
	return nil
}

func isImpliedAssembly(e Obj, byID map[string]Obj) bool {
	u := umlOf(e)
	if u["kind"] != "assembly" || !truthy(u["sourcePort"]) || !truthy(u["targetPort"]) {
		return false
	}
	s, t := byID[str(e["source"])], byID[str(e["target"])]
	if s == nil || t == nil {
		return false
	}
	sp, tp := findPort(s, u["sourcePort"]), findPort(t, u["targetPort"])
	return sp != nil && tp != nil && sp["kind"] == "required" && tp["kind"] == "provided" && sp["name"] == tp["name"]
}

func archAttrs(r any) string {
	o := obj(r)
	if !truthy(o["nodeId"]) {
		return ""
	}
	s := " archRef=" + attrValStar(jsStr(o["nodeId"]))
	if truthy(o["fileId"]) {
		s += " archFile=" + attrValStar(jsStr(o["fileId"]))
	}
	if truthy(o["pageId"]) {
		s += " archPage=" + attrValStar(jsStr(o["pageId"]))
	}
	return s
}

// kidsOf groups nodes by parent (key "" = root); a parent outside nodes counts as root.
func kidsOf(nodes []Obj) map[string][]Obj {
	ids := map[string]bool{}
	for _, n := range nodes {
		ids[str(n["id"])] = true
	}
	kids := map[string][]Obj{}
	for _, n := range nodes {
		key := ""
		if p := str(n["parentId"]); p != "" && ids[p] {
			key = p
		}
		kids[key] = append(kids[key], n)
	}
	return kids
}

func memberLine(m Obj, op bool) string {
	pre := ""
	if truthy(m["visibility"]) {
		pre += jsStr(m["visibility"]) + " "
	}
	if truthy(m["static"]) {
		pre += "static "
	}
	if truthy(m["abstract"]) {
		pre += "abstract "
	}
	name := jsStr(m["name"])
	if !reMemberName.MatchString(name) {
		name = quote(name)
	}
	if op {
		var ps []string
		for _, p := range objs(m["params"]) {
			if truthy(p["type"]) {
				ps = append(ps, jsStr(p["name"])+": "+jsStr(p["type"]))
			} else {
				ps = append(ps, jsStr(p["name"]))
			}
		}
		s := pre + name + "(" + strings.Join(ps, ", ") + ")"
		if truthy(m["type"]) {
			s += ": " + jsStr(m["type"])
		}
		return s
	}
	s := pre + name
	if truthy(m["type"]) {
		s += ": " + jsStr(m["type"])
	}
	if truthy(m["default"]) {
		s += " = " + jsStr(m["default"])
	}
	return s
}

func labelOf(n Obj) string {
	if v := dataOf(n)["label"]; v != nil {
		return jsStr(v)
	}
	return ""
}

func printClasses(page Obj, out *[]string) {
	var classes, pkgs []Obj
	for _, n := range nodesOf(page) {
		if str(n["type"]) == "class" {
			classes = append(classes, n)
		}
	}
	for _, n := range nodesOf(page) {
		if isGroupOf(n, "package") {
			pkgs = append(pkgs, n)
		}
	}
	if len(classes) == 0 && len(pkgs) == 0 {
		return
	}
	kids := kidsOf(append(append([]Obj{}, pkgs...), classes...))
	*out = append(*out, "", "classes {")
	var emit func(n Obj, ind string)
	emit = func(n Obj, ind string) {
		if str(n["type"]) == "group" {
			*out = append(*out, ind+"package "+word(str(n["id"]))+" "+quote(labelOf(n))+" {")
			for _, k := range kids[str(n["id"])] {
				emit(k, ind+"  ")
			}
			*out = append(*out, ind+"}")
			return
		}
		d := dataOf(n)
		kw := "class"
		switch d["kind"] {
		case "abstract":
			kw = "abstract class"
		case "interface":
			kw = "interface"
		case "enum":
			kw = "enum"
		}
		head := ind + kw + " " + word(jsStr(d["label"]))
		if truthy(d["generics"]) {
			head += "<" + jsStr(d["generics"]) + ">"
		}
		if truthy(d["stereotype"]) {
			head += " " + quote(jsStr(d["stereotype"]))
		}
		head += archAttrs(d["archRef"])
		if truthy(d["modelId"]) {
			head += " model=" + attrValStar(jsStr(d["modelId"]))
		}
		attrs := objs(d["attributes"])
		var ops []Obj
		if d["kind"] != "enum" {
			ops = objs(d["operations"])
		}
		if len(attrs) == 0 && len(ops) == 0 {
			*out = append(*out, head)
			return
		}
		*out = append(*out, head+" {")
		for _, a := range attrs {
			*out = append(*out, ind+"  "+memberLine(a, false))
		}
		for _, o := range ops {
			*out = append(*out, ind+"  "+memberLine(o, true))
		}
		*out = append(*out, ind+"}")
	}
	for _, n := range kids[""] {
		emit(n, "  ")
	}
	name := map[string]string{}
	for _, n := range classes {
		name[str(n["id"])] = jsStr(dataOf(n)["label"])
	}
	edges := ownedClassEdges(page)
	if len(edges) > 0 {
		*out = append(*out, "")
	}
	for _, e := range edges {
		u := umlOf(e)
		var op string
		switch u["kind"] {
		case "inheritance":
			op = "--|>"
		case "realization":
			op = "..|>"
		case "composition":
			op = "--*"
		case "aggregation":
			op = "--o"
		case "dependency":
			op = "..>"
		default:
			if truthy(u["directed"]) {
				op = "-->"
			} else {
				op = "--"
			}
		}
		var a []string
		if truthy(u["sourceMult"]) {
			a = append(a, "srcMult="+attrValStar(jsStr(u["sourceMult"])))
		}
		if truthy(u["targetMult"]) {
			a = append(a, "dstMult="+attrValStar(jsStr(u["targetMult"])))
		}
		if truthy(u["sourceRole"]) {
			a = append(a, "srcRole="+attrValStar(jsStr(u["sourceRole"])))
		}
		if truthy(u["targetRole"]) {
			a = append(a, "dstRole="+attrValStar(jsStr(u["targetRole"])))
		}
		line := "  " + word(name[str(e["source"])]) + " " + op + " " + word(name[str(e["target"])])
		if truthy(e["label"]) {
			line += " " + quote(jsStr(e["label"]))
		}
		if len(a) > 0 {
			line += " " + strings.Join(a, " ")
		}
		*out = append(*out, line)
	}
	*out = append(*out, "}")
}

func regionOf(n Obj) float64 { return numOr(dataOf(n)["region"], 0) }

func printStates(page Obj, out *[]string) {
	var states []Obj
	for _, n := range nodesOf(page) {
		if isStateLike(n) {
			states = append(states, n)
		}
	}
	if len(states) == 0 {
		return
	}
	kids := kidsOf(states)
	*out = append(*out, "", "states {")
	var emit func(n Obj, ind string)
	emit = func(n Obj, ind string) {
		d := dataOf(n)
		id := str(n["id"])
		if str(n["type"]) == "group" {
			label := labelOf(n)
			head := ind + "state " + word(id)
			if label != "" && label != id {
				head += " " + quote(label)
			}
			*out = append(*out, head+" {")
			ch := append([]Obj{}, kids[id]...)
			stableSort(ch, func(a, b Obj) bool { return regionOf(a) < regionOf(b) })
			region := 0.0
			for _, k := range ch {
				for ; region < regionOf(k); region++ {
					*out = append(*out, ind+"  region")
				}
				emit(k, ind+"  ")
			}
			*out = append(*out, ind+"}")
			return
		}
		kind := "state"
		if truthy(d["stateKind"]) {
			kind = jsStr(d["stateKind"])
		}
		label := labelOf(n)
		var a []string
		if truthy(d["activity"]) {
			a = append(a, "activity="+quote(jsStr(d["activity"])))
		}
		if truthy(d["vertical"]) && (kind == "fork" || kind == "join") {
			a = append(a, "vertical")
		}
		line := ind + kind + " " + word(id)
		if label != "" && !(kind == "state" && label == id) {
			line += " " + quote(label)
		}
		if len(a) > 0 {
			line += " " + strings.Join(a, " ")
		}
		*out = append(*out, line)
	}
	for _, n := range kids[""] {
		emit(n, "  ")
	}
	edges := ownedTransitions(page)
	if len(edges) > 0 {
		*out = append(*out, "")
	}
	for _, e := range edges {
		line := "  " + word(str(e["source"])) + " -> " + word(str(e["target"]))
		if truthy(e["label"]) {
			line += " " + quote(jsStr(e["label"]))
		}
		*out = append(*out, line)
	}
	*out = append(*out, "}")
}

func printUseCases(page Obj, out *[]string) {
	var nodes []Obj
	for _, n := range nodesOf(page) {
		if isUcNode(n) || isGroupOf(n, "boundary") {
			nodes = append(nodes, n)
		}
	}
	if len(nodes) == 0 {
		return
	}
	kids := kidsOf(nodes)
	*out = append(*out, "", "usecases {")
	var emit func(n Obj, ind string)
	emit = func(n Obj, ind string) {
		d := dataOf(n)
		label := quote(labelOf(n))
		id := word(str(n["id"]))
		switch {
		case str(n["type"]) == "group":
			*out = append(*out, ind+"boundary "+id+" "+label+" {")
			for _, k := range kids[str(n["id"])] {
				emit(k, ind+"  ")
			}
			*out = append(*out, ind+"}")
		case d["ucKind"] == "actor":
			*out = append(*out, ind+"actor "+id+" "+label)
		case len(list(d["extensionPoints"])) > 0:
			*out = append(*out, ind+"usecase "+id+" "+label+" {")
			for _, p := range list(d["extensionPoints"]) {
				*out = append(*out, ind+"  point "+quote(jsStr(p)))
			}
			*out = append(*out, ind+"}")
		default:
			*out = append(*out, ind+"usecase "+id+" "+label)
		}
	}
	for _, n := range kids[""] {
		emit(n, "  ")
	}
	edges := ownedUcEdges(page)
	if len(edges) > 0 {
		*out = append(*out, "")
	}
	for _, e := range edges {
		u := umlOf(e)
		kind := jsStr(u["kind"])
		op := kind
		if kind == "association" {
			op = "--"
		} else if kind == "generalization" {
			op = "generalizes"
		}
		label := ""
		if truthy(e["label"]) && jsStr(e["label"]) != "«"+kind+"»" {
			label = " " + quote(jsStr(e["label"]))
		}
		var a []string
		if truthy(u["sourceMult"]) {
			a = append(a, "srcMult="+attrValStar(jsStr(u["sourceMult"])))
		}
		if truthy(u["targetMult"]) {
			a = append(a, "dstMult="+attrValStar(jsStr(u["targetMult"])))
		}
		line := "  " + word(str(e["source"])) + " " + op + " " + word(str(e["target"])) + label
		if len(a) > 0 {
			line += " " + strings.Join(a, " ")
		}
		*out = append(*out, line)
	}
	*out = append(*out, "}")
}

func printComponents(page Obj, out *[]string) {
	var comps []Obj
	for _, n := range nodesOf(page) {
		if isCompNode(n) {
			comps = append(comps, n)
		}
	}
	if len(comps) == 0 {
		return
	}
	byID := map[string]Obj{}
	for _, n := range nodesOf(page) {
		byID[str(n["id"])] = n
	}
	kids := kidsOf(comps)
	*out = append(*out, "", "components {")
	var emit func(n Obj, ind string)
	emit = func(n Obj, ind string) {
		d := dataOf(n)
		var provides, requires []string
		for _, p := range objs(d["ports"]) {
			if p["kind"] == "provided" {
				provides = append(provides, word(jsStr(p["name"])))
			} else if p["kind"] == "required" {
				requires = append(requires, word(jsStr(p["name"])))
			}
		}
		children := kids[str(n["id"])]
		kind := "component"
		if truthy(d["compKind"]) {
			kind = jsStr(d["compKind"])
		}
		head := ind + kind + " " + word(str(n["id"])) + " " + quote(labelOf(n))
		if truthy(d["stereotype"]) {
			head += " stereotype=" + attrValStar(jsStr(d["stereotype"]))
		}
		head += archAttrs(d["archRef"])
		if len(provides) == 0 && len(requires) == 0 && len(children) == 0 {
			*out = append(*out, head)
			return
		}
		*out = append(*out, head+" {")
		if len(provides) > 0 {
			*out = append(*out, ind+"  provides "+strings.Join(provides, ", "))
		}
		if len(requires) > 0 {
			*out = append(*out, ind+"  requires "+strings.Join(requires, ", "))
		}
		for _, k := range children {
			emit(k, ind+"  ")
		}
		*out = append(*out, ind+"}")
	}
	for _, n := range kids[""] {
		emit(n, "  ")
	}
	var edges []Obj
	for _, e := range ownedCompEdges(page) {
		if !isImpliedAssembly(e, byID) {
			edges = append(edges, e)
		}
	}
	if len(edges) > 0 {
		*out = append(*out, "")
	}
	for _, e := range edges {
		op := "..>"
		if umlOf(e)["kind"] == "assembly" {
			op = "--"
		}
		line := "  " + word(str(e["source"])) + " " + op + " " + word(str(e["target"]))
		if truthy(e["label"]) {
			line += " " + quote(jsStr(e["label"]))
		}
		*out = append(*out, line)
	}
	*out = append(*out, "}")
}

func printActivity(page Obj, out *[]string) {
	var nodes []Obj
	for _, n := range nodesOf(page) {
		if isActLike(n) {
			nodes = append(nodes, n)
		}
	}
	if len(nodes) == 0 {
		return
	}
	kids := kidsOf(nodes)
	*out = append(*out, "", "activity {")
	var emit func(n Obj, ind string)
	emit = func(n Obj, ind string) {
		d := dataOf(n)
		id := str(n["id"])
		if str(n["type"]) == "group" {
			head := ind + "lane " + word(id) + " " + quote(labelOf(n))
			if d["laneOrient"] == "h" {
				head += " orient=h"
			}
			*out = append(*out, head+" {")
			for _, k := range kids[id] {
				if str(k["type"]) == "activity" {
					emit(k, ind+"  ")
				}
			}
			*out = append(*out, ind+"}")
			return
		}
		kind := "action"
		if v := d["actKind"]; v != nil {
			kind = jsStr(v)
		}
		label := labelOf(n)
		var a []string
		if truthy(d["vertical"]) && kind == "fork" {
			a = append(a, "vertical")
		}
		if truthy(d["objectState"]) && kind == "object" {
			a = append(a, "state="+attrValStar(jsStr(d["objectState"])))
		}
		line := ind + kind + " " + word(id)
		if label != actLabelDefault(kind, id) {
			line += " " + quote(label)
		}
		if len(a) > 0 {
			line += " " + strings.Join(a, " ")
		}
		*out = append(*out, line)
	}
	for _, n := range kids[""] {
		emit(n, "  ")
	}
	edges := ownedActEdges(page)
	if len(edges) > 0 {
		*out = append(*out, "")
	}
	for _, e := range edges {
		note := umlOf(e)["kind"] == "note"
		op := "->"
		if note {
			op = "--"
		}
		line := "  " + word(str(e["source"])) + " " + op + " " + word(str(e["target"]))
		if !note && truthy(e["label"]) {
			line += " " + quote(jsStr(e["label"]))
		}
		*out = append(*out, line)
	}
	*out = append(*out, "}")
}

func printDeployment(page Obj, out *[]string) {
	var nodes []Obj
	for _, n := range nodesOf(page) {
		if isDepNode(n) {
			nodes = append(nodes, n)
		}
	}
	if len(nodes) == 0 {
		return
	}
	kids := kidsOf(nodes)
	*out = append(*out, "", "deployment {")
	var emit func(n Obj, ind string)
	emit = func(n Obj, ind string) {
		d := dataOf(n)
		kind := "node"
		if v := d["depKind"]; v != nil {
			kind = jsStr(v)
		}
		head := ind + kind + " " + word(str(n["id"])) + " " + quote(labelOf(n))
		if truthy(d["stereotype"]) {
			head += " stereotype=" + attrValStar(jsStr(d["stereotype"]))
		}
		if truthy(d["iconId"]) {
			head += " icon=" + attrValStar(jsStr(d["iconId"]))
		}
		children := kids[str(n["id"])]
		if len(children) == 0 {
			*out = append(*out, head)
			return
		}
		*out = append(*out, head+" {")
		for _, k := range children {
			emit(k, ind+"  ")
		}
		*out = append(*out, ind+"}")
	}
	for _, n := range kids[""] {
		emit(n, "  ")
	}
	edges := ownedDepEdges(page)
	if len(edges) > 0 {
		*out = append(*out, "")
	}
	for _, e := range edges {
		kind := jsStr(umlOf(e)["kind"])
		op := "manifests"
		switch kind {
		case "communication":
			op = "--"
		case "deploy":
			op = "..>"
		}
		line := "  " + word(str(e["source"])) + " " + op + " " + word(str(e["target"]))
		if truthy(e["label"]) && jsStr(e["label"]) != "«"+kind+"»" {
			line += " " + quote(jsStr(e["label"]))
		}
		*out = append(*out, line)
	}
	*out = append(*out, "}")
}

func stableSort(l []Obj, less func(a, b Obj) bool) {
	sort.SliceStable(l, func(i, j int) bool { return less(l[i], l[j]) })
}

// printPackages prints only the relations between packages; the packages themselves are printed by classes.
func printPackages(page Obj, out *[]string) {
	edges := ownedPkgEdges(page)
	if len(edges) == 0 {
		return
	}
	*out = append(*out, "", "packages {")
	for _, e := range edges {
		kind := jsStr(umlOf(e)["kind"])
		op := "..>"
		switch kind {
		case "import":
			op = "imports"
		case "access":
			op = "access"
		case "merge":
			op = "merges"
		}
		line := "  " + word(str(e["source"])) + " " + op + " " + word(str(e["target"]))
		if truthy(e["label"]) && jsStr(e["label"]) != "«"+kind+"»" {
			line += " " + quote(jsStr(e["label"]))
		}
		*out = append(*out, line)
	}
	*out = append(*out, "}")
}

func commSeqParts(s string) []int {
	var out []int
	for _, p := range strings.Split(s, ".") {
		n, _ := strconv.Atoi(p)
		out = append(out, n)
	}
	return out
}

func commCompare(a, b string) int {
	x, y := commSeqParts(a), commSeqParts(b)
	for i := 0; i < len(x) || i < len(y); i++ {
		xi, yi := -1, -1
		if i < len(x) {
			xi = x[i]
		}
		if i < len(y) {
			yi = y[i]
		}
		if xi != yi {
			return xi - yi
		}
	}
	return 0
}

// printObjects writes `objects { }`, or `communication { }` when a link carries numbered messages.
func printObjects(page Obj, out *[]string) {
	var nodes []Obj
	for _, n := range nodesOf(page) {
		if isObjNode(n) {
			nodes = append(nodes, n)
		}
	}
	if len(nodes) == 0 {
		return
	}
	edges := ownedObjEdges(page)
	comm := false
	for _, e := range edges {
		if len(list(umlOf(e)["messages"])) > 0 {
			comm = true
		}
	}
	if comm {
		*out = append(*out, "", "communication {")
	} else {
		*out = append(*out, "", "objects {")
	}
	for _, n := range nodes {
		d := dataOf(n)
		id := str(n["id"])
		head := "  object " + word(id)
		if l := labelOf(n); l != id {
			head += " " + quote(l)
		}
		if truthy(d["className"]) {
			head += " class=" + attrValStar(jsStr(d["className"]))
		}
		if truthy(d["modelId"]) {
			head += " model=" + attrValStar(jsStr(d["modelId"]))
		}
		slots := objs(d["slots"])
		if len(slots) == 0 {
			*out = append(*out, head)
			continue
		}
		*out = append(*out, head+" {")
		for _, sl := range slots {
			name := jsStr(sl["name"])
			if !reSlotName.MatchString(name) {
				name = quote(name)
			}
			line := "    " + name
			if truthy(sl["value"]) {
				line += " = " + jsStr(sl["value"])
			}
			*out = append(*out, line)
		}
		*out = append(*out, "  }")
	}
	// a link whose messages are listed below needs no line of its own, unless it has roles
	var lines []Obj
	for _, e := range edges {
		u := umlOf(e)
		if len(list(u["messages"])) == 0 || truthy(u["sourceRole"]) || truthy(u["targetRole"]) {
			lines = append(lines, e)
		}
	}
	if len(lines) > 0 {
		*out = append(*out, "")
	}
	for _, e := range lines {
		u := umlOf(e)
		line := "  " + word(str(e["source"])) + " -- " + word(str(e["target"]))
		if len(list(u["messages"])) == 0 && truthy(e["label"]) {
			line += " " + quote(jsStr(e["label"]))
		}
		if truthy(u["sourceRole"]) {
			line += " srcRole=" + attrValStar(jsStr(u["sourceRole"]))
		}
		if truthy(u["targetRole"]) {
			line += " dstRole=" + attrValStar(jsStr(u["targetRole"]))
		}
		*out = append(*out, line)
	}
	if comm {
		type pm struct {
			seq, from, to, text string
			async               bool
		}
		var msgs []pm
		for _, e := range edges {
			for _, m := range objs(umlOf(e)["messages"]) {
				from, to := str(e["source"]), str(e["target"])
				if str(m["dir"]) == "back" {
					from, to = to, from
				}
				msgs = append(msgs, pm{str(m["seq"]), from, to, jsStr(m["text"]), truthy(m["async"])})
			}
		}
		sort.SliceStable(msgs, func(i, j int) bool { return commCompare(msgs[i].seq, msgs[j].seq) < 0 })
		*out = append(*out, "")
		for _, m := range msgs {
			line := "  " + m.seq + " " + word(m.from) + " -> " + word(m.to)
			if m.text != "" {
				line += " " + quote(m.text)
			}
			if m.async {
				line += " async"
			}
			*out = append(*out, line)
		}
	}
	*out = append(*out, "}")
}

func tmNumOf(v any) float64 {
	f, _ := v.(float64)
	return f
}

var reTmWord = regexp.MustCompile(`^[A-Za-z0-9_.+-]+$`)

func tmFmt(v float64) string { return strconv.FormatFloat(tmRound(v), 'f', -1, 64) }

func tmState(x string) string {
	if reTmWord.MatchString(x) {
		return x
	}
	return quote(x)
}

// printTiming writes one `timing` block per timing node.
func printTiming(page Obj, out *[]string) {
	for _, n := range nodesOf(page) {
		if str(n["type"]) != "timing" {
			continue
		}
		d := dataOf(n)
		label := "Timing"
		if d["label"] != nil {
			label = jsStr(d["label"])
		}
		*out = append(*out, "", "timing "+word(str(n["id"]))+" "+quote(label)+" {")
		if truthy(d["unit"]) {
			*out = append(*out, "  unit "+word(jsStr(d["unit"])))
		}
		end := 100.0
		if f, ok := d["end"].(float64); ok {
			end = f
		}
		*out = append(*out, "  end "+tmFmt(end))
		lifeIdx := map[string]int{}
		for i, l := range objs(d["lifelines"]) {
			id := str(l["id"])
			lifeIdx[id] = i
			line := "  " + str(l["kind"]) + " " + word(id)
			if str(l["name"]) != id {
				line += " " + quote(str(l["name"]))
			}
			kind := str(l["kind"])
			if st := list(l["states"]); (kind == "robust" || kind == "concise") && len(st) > 0 {
				var names []string
				for _, x := range st {
					names = append(names, tmState(jsStr(x)))
				}
				line += " states " + strings.Join(names, ", ")
			}
			if p, ok := l["period"].(float64); ok && kind == "clock" && p != 0 {
				line += " period=" + tmFmt(p)
			}
			*out = append(*out, line)
		}
		type chg struct {
			c Obj
			i int
		}
		var changes []chg
		for i, c := range objs(d["changes"]) {
			changes = append(changes, chg{c, i})
		}
		sort.SliceStable(changes, func(a, b int) bool {
			ta, tb := tmNumOf(changes[a].c["t"]), tmNumOf(changes[b].c["t"])
			if ta != tb {
				return ta < tb
			}
			if lifeIdx[str(changes[a].c["life"])] != lifeIdx[str(changes[b].c["life"])] {
				return lifeIdx[str(changes[a].c["life"])] < lifeIdx[str(changes[b].c["life"])]
			}
			return changes[a].i < changes[b].i
		})
		for _, x := range changes {
			*out = append(*out, "  "+word(str(x.c["life"]))+" @"+tmFmt(tmNumOf(x.c["t"]))+" "+tmState(jsStr(x.c["state"])))
		}
		msgs := objs(d["messages"])
		sort.SliceStable(msgs, func(a, b int) bool { return tmNumOf(msgs[a]["t"]) < tmNumOf(msgs[b]["t"]) })
		for _, m := range msgs {
			line := "  " + word(str(m["from"])) + " -> " + word(str(m["to"])) + " @" + tmFmt(tmNumOf(m["t"]))
			if t2, ok := m["t2"].(float64); ok && t2 != tmNumOf(m["t"]) {
				line += ".." + tmFmt(t2)
			}
			if truthy(m["text"]) {
				line += " " + quote(jsStr(m["text"]))
			}
			*out = append(*out, line)
		}
		for _, k := range objs(d["durations"]) {
			line := "  " + word(str(k["life"])) + " " + tmFmt(tmNumOf(k["t1"])) + ".." + tmFmt(tmNumOf(k["t2"]))
			if truthy(k["text"]) {
				line += " " + quote(jsStr(k["text"]))
			}
			*out = append(*out, line)
		}
		*out = append(*out, "}")
	}
}
