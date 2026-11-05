package core

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Merges the UML blocks of a parsed document into a page (core/dsl/build2.ts).

type umlEnv struct {
	added, fresh *oset
	removed      *[]string
	nodeIDs      map[string]bool
	err          func(line int, msg string)
	warn         func(line int, msg string)
}

const (
	umlColor    = "#7b8499"
	stateAccent = "#2f6fdb"
	ucAccent    = "#0f9d8f"
	compAccent  = "#8e5bd6"
	actAccent   = "#d9822b"
	depAccent   = "#3f8f6b"
	objAccent   = "#0e7490"
)

var kindAccent = map[string]string{"class": "#2f6fdb", "interface": "#0f9d8f", "abstract": "#8e5bd6", "enum": "#e8710a"}

func snap10(n float64) float64 { return jsRound(n/10) * 10 }

func trimmedStr(v any) string { return strings.TrimSpace(str(v)) }

func classSizeOf(d Obj) (float64, float64) {
	label := jsStr(d["label"])
	if g := trimmedStr(d["generics"]); g != "" {
		label = label + "<" + g + ">"
	}
	w := float64(u16len(label))*8.1 + 40
	attrs, ops := objs(d["attributes"]), objs(d["operations"])
	for _, m := range attrs {
		w = math.Max(w, float64(u16len(memberLine(m, false)))*6.9+28)
	}
	for _, m := range ops {
		w = math.Max(w, float64(u16len(memberLine(m, true)))*6.9+28)
	}
	width := jsRound(math.Min(420, math.Max(160, w))/10) * 10
	kind := d["kind"]
	stereo := kind == "interface" || kind == "abstract" || kind == "enum" || trimmedStr(d["stereotype"]) != ""
	var shown []int
	switch {
	case kind == "enum":
		shown = []int{len(attrs)}
	case kind == "interface" && len(attrs) == 0:
		shown = []int{len(ops)}
	default:
		shown = []int{len(attrs), len(ops)}
	}
	h := 36.0
	if stereo {
		h += 16
	}
	for _, rows := range shown {
		h += 8 + float64(rows)*22
	}
	return width, h + 2
}

func stateSizeOf(kind, name, activity string, vertical bool) (float64, float64) {
	switch kind {
	case "initial":
		return 20, 20
	case "final":
		return 28, 28
	case "choice":
		return 34, 34
	case "fork", "join":
		if vertical {
			return 8, 96
		}
		return 96, 8
	case "history", "deepHistory":
		return 30, 30
	case "entryPoint", "exitPoint":
		return 16, 16
	}
	n := 0
	widest := float64(u16len(name))
	if activity != "" {
		for _, l := range strings.Split(activity, "\n") {
			if l != "" {
				n++
				widest = math.Max(widest, float64(u16len(l))*0.92)
			}
		}
	}
	h := 40.0
	if n > 0 {
		h += float64(n)*15 + 10
	}
	return jsRound(math.Max(110, widest*7.4+36)/10) * 10, h
}

func shapeSizeOf(typ string, d Obj) (float64, float64) {
	label := jsStr(d["label"])
	if d["label"] == nil {
		label = ""
	}
	ll := float64(u16len(label))
	if typ == "usecase" {
		if d["ucKind"] == "actor" {
			return math.Max(70, snap10(ll*7.6+20)), 100
		}
		eps := list(d["extensionPoints"])
		maxEp := 0.0
		for _, e := range eps {
			maxEp = math.Max(maxEp, float64(u16len(jsStr(e))))
		}
		epW := maxEp*6.6 + 40
		h := 64.0
		if ll > 28 {
			h = 80
		}
		if len(eps) > 0 {
			h += 22 + float64(len(eps))*15
		}
		return math.Max(150, snap10(math.Max(ll*7.6+56, epW))), h
	}
	per := map[string]float64{}
	for _, p := range objs(d["ports"]) {
		per[str(p["side"])]++
	}
	portH := 30*math.Max(per["left"], per["right"]) + 14
	portW := 40*math.Max(per["top"], per["bottom"]) + 20
	fit := func(w, h float64) (float64, float64) { return math.Max(w, portW), math.Max(h, portH) }
	switch d["compKind"] {
	case "node":
		return fit(math.Max(150, snap10(ll*7.6+60)), 90)
	case "artifact":
		return fit(math.Max(130, snap10(ll*7.6+50)), 72)
	}
	return fit(math.Max(150, snap10(ll*7.6+70)), 72)
}

// actSizeOf mirrors src/lib/activity/sizes.ts.
func actSizeOf(d Obj) (float64, float64) {
	label := ""
	if d["label"] != nil {
		label = jsStr(d["label"])
	}
	longest := 0.0
	for _, l := range strings.Split(label, "\n") {
		longest = math.Max(longest, float64(u16len(l)))
	}
	lines := 1.0
	if label != "" {
		lines = float64(len(strings.Split(label, "\n")))
	}
	switch d["actKind"] {
	case "initial":
		return 28, 28
	case "final":
		return 36, 36
	case "flowFinal":
		return 30, 30
	case "decision":
		return 56, 56
	case "fork":
		if truthy(d["vertical"]) {
			return 8, 120
		}
		return 120, 8
	case "object":
		h := 46.0
		if truthy(d["objectState"]) {
			h = 62
		}
		return math.Max(110, snap10(longest*7.6+32)), h
	case "send", "accept":
		return math.Max(130, snap10(longest*7.6+56)), 48
	case "note":
		return math.Max(140, snap10(longest*6.8+28)), math.Max(56, 28+lines*17)
	}
	return math.Max(120, snap10(longest*7.6+36)), math.Max(48, 24+lines*20)
}

// depSizeOf mirrors src/lib/deployment/sizes.ts.
func depSizeOf(d Obj) (float64, float64) {
	ll := 0.0
	if d["label"] != nil {
		ll = float64(u16len(jsStr(d["label"])))
	}
	switch d["depKind"] {
	case "device":
		return math.Max(160, snap10(ll*7.6+70)), 110
	case "executionEnvironment":
		return math.Max(150, snap10(ll*7.6+60)), 80
	case "artifact":
		return math.Max(130, snap10(ll*7.6+50)), 72
	case "component":
		return math.Max(150, snap10(ll*7.6+70)), 72
	}
	return math.Max(160, snap10(ll*7.6+70)), 96
}

// objSizeOf mirrors src/lib/object/sizes.ts.
func objSizeOf(d Obj) (float64, float64) {
	slots := objs(d["slots"])
	head := labelStr(d["label"])
	if cn := labelStr(d["className"]); cn != "" {
		head += " : " + cn
	}
	w := float64(u16len(head))*7.6 + 32
	for _, sl := range slots {
		t := labelStr(sl["name"])
		if v := labelStr(sl["value"]); v != "" {
			t += " = " + v
		}
		w = math.Max(w, float64(u16len(t))*6.9+28)
	}
	h := 36.0
	if len(slots) > 0 {
		h += 8 + float64(len(slots))*22
	}
	return jsRound(math.Min(420, math.Max(150, w))/10) * 10, h + 2
}

func catOf(n Obj) string {
	switch {
	case str(n["type"]) == "class":
		return "cls"
	case isGroupOf(n, "package"):
		return "pkg"
	case isStateLike(n):
		return "state"
	case isUcNode(n) || isGroupOf(n, "boundary"):
		return "uc"
	case isCompNode(n):
		return "comp"
	case isActLike(n):
		return "act"
	case isDepNode(n):
		return "dep"
	case isObjNode(n):
		return "obj"
	}
	return ""
}

type umlWant struct {
	id, cat, typ, variant, parent string
	line                          int
	groupW, groupH                float64
	data                          func(old Obj) Obj
	size                          func(d Obj) (float64, float64)
}

func setOrDel(o Obj, k string, v any) {
	switch x := v.(type) {
	case nil:
		delete(o, k)
		return
	case bool:
		if !x {
			delete(o, k)
			return
		}
	case string:
		if x == "" {
			delete(o, k)
			return
		}
	}
	o[k] = v
}

func strOrNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

var reMemberID = regexp.MustCompile(`^[ao](\d+)$`)
var rePortID = regexp.MustCompile(`^p(\d+)$`)
var reSlotID = regexp.MustCompile(`^s(\d+)$`)

func buildMembers(defs []*DslMember, prefix string, oldList []Obj) []any {
	used := map[string]bool{}
	next := 0
	for _, m := range oldList {
		if x := reMemberID.FindStringSubmatch(str(m["id"])); x != nil {
			n, _ := strconv.Atoi(x[1])
			if n+1 > next {
				next = n + 1
			}
		}
	}
	out := []any{}
	for _, def := range defs {
		var old Obj
		for _, m := range oldList {
			if m["name"] == def.Name && !used[str(m["id"])] && (m["params"] != nil) == def.Op {
				old = m
				break
			}
		}
		id := ""
		if old != nil && old["id"] != nil {
			id = str(old["id"])
		} else {
			id = prefix + strconv.Itoa(next)
			next++
		}
		used[id] = true
		m := Obj{}
		if old != nil {
			m = copyObj(old)
		}
		m["id"], m["visibility"], m["name"] = id, def.Vis, def.Name
		setOrDel(m, "type", strOrNil(def.Type))
		setOrDel(m, "default", strOrNil(def.Default))
		setOrDel(m, "static", def.Static)
		setOrDel(m, "abstract", def.Abstract)
		if def.Op {
			ps := []any{}
			for _, p := range def.Params {
				po := Obj{"name": p.Name}
				if p.Type != "" {
					po["type"] = p.Type
				}
				ps = append(ps, po)
			}
			m["params"] = ps
		} else {
			delete(m, "params")
		}
		out = append(out, m)
	}
	return out
}

func archRefObj(r *DslArchRef) Obj {
	o := Obj{"nodeId": r.NodeID}
	if r.FileID != "" {
		o["fileId"] = r.FileID
	}
	if r.PageID != "" {
		o["pageId"] = r.PageID
	}
	return o
}

func withDefaults(old Obj, def Obj) Obj {
	if old != nil {
		return copyObj(old)
	}
	return copyObj(def)
}

func dataKey(n Obj) string {
	b, _ := json.Marshal(n["data"])
	return string(b)
}

type umlWantEdge struct {
	s, t, label string
	uml         Obj
	strip       []string
}

func applyUml(page Obj, doc *DslDoc, env *umlEnv) map[string]bool {
	added, fresh, nodeIDs, err := env.added, env.fresh, env.nodeIDs, env.err
	owned := map[string]bool{}
	var oldNodes []Obj
	for _, n := range nodesOf(page) {
		if catOf(n) != "" {
			oldNodes = append(oldNodes, n)
			owned[str(n["id"])] = true
		}
	}
	oldCls, oldState, oldUc, oldComp := ownedClassEdges(page), ownedTransitions(page), ownedUcEdges(page), ownedCompEdges(page)
	oldAct, oldDep := ownedActEdges(page), ownedDepEdges(page)
	oldPkg, oldObj := ownedPkgEdges(page), ownedObjEdges(page)

	var wants []*umlWant
	classID := map[string]string{}
	usedOld := map[string]bool{}
	pkgLabel := func(id string) (string, bool) {
		for _, p := range doc.Packages {
			if p.ID == id {
				return p.Label, true
			}
		}
		return "", false
	}
	// classes are known by name: match those first; when exactly one class of the text and one node are left over, it is a rename
	oldOf := map[*DslClass]Obj{}
	for _, dc := range doc.Classes {
		for _, n := range nodesOf(page) {
			if str(n["type"]) == "class" && dataOf(n)["label"] == dc.Name && !usedOld[str(n["id"])] {
				usedOld[str(n["id"])] = true
				oldOf[dc] = n
				break
			}
		}
	}
	var newOnes []*DslClass
	for _, dc := range doc.Classes {
		if oldOf[dc] == nil {
			newOnes = append(newOnes, dc)
		}
	}
	var goneOnes []Obj
	for _, n := range nodesOf(page) {
		if str(n["type"]) == "class" && !usedOld[str(n["id"])] {
			goneOnes = append(goneOnes, n)
		}
	}
	if len(newOnes) == 1 && len(goneOnes) == 1 {
		oldOf[newOnes[0]] = goneOnes[0]
		usedOld[str(goneOnes[0]["id"])] = true
	}
	for _, dc := range doc.Classes {
		dc := dc
		old := oldOf[dc]
		id := ""
		if old != nil {
			id = str(old["id"])
		} else {
			id = uniq("cls-"+slug(dc.Name), nodeIDs)
		}
		classID[dc.Name] = id
		wants = append(wants, &umlWant{id: id, cat: "cls", typ: "class", parent: dc.Pkg, line: dc.Line,
			data: func(o Obj) Obj {
				d := Obj{}
				if o != nil {
					d = copyObj(o)
				}
				d["label"], d["kind"] = dc.Name, dc.Kind
				setOrDel(d, "generics", strOrNil(dc.Generics))
				if dc.ArchRef != nil {
					d["archRef"] = archRefObj(dc.ArchRef)
				} else {
					delete(d, "archRef")
				}
				setOrDel(d, "modelId", strOrNil(dc.ModelID))
				if dc.Pkg != "" {
					if l, ok := pkgLabel(dc.Pkg); ok {
						d["package"] = l
					} else {
						delete(d, "package")
					}
				}
				setOrDel(d, "stereotype", strOrNil(dc.Stereotype))
				var oa, oo []Obj
				if o != nil {
					oa, oo = objs(o["attributes"]), objs(o["operations"])
				}
				d["attributes"] = buildMembers(dc.Attrs, "a", oa)
				d["operations"] = buildMembers(dc.Ops, "o", oo)
				if o != nil && o["accent"] != nil {
					d["accent"] = o["accent"]
				} else {
					d["accent"] = kindAccent[dc.Kind]
				}
				return d
			},
			size: classSizeOf})
	}
	for _, p := range doc.Packages {
		p := p
		wants = append(wants, &umlWant{id: p.ID, cat: "pkg", typ: "group", variant: "package", parent: p.Parent, line: p.Line, groupW: 360, groupH: 240,
			data: func(o Obj) Obj {
				d := withDefaults(o, Obj{"stroke": "#4b5563", "accent": "#4b5563", "fillOpacity": 0.0, "border": true, "strokeWidth": 1.5, "strokeStyle": "solid"})
				d["label"], d["variant"] = p.Label, "package"
				return d
			}})
	}
	for _, s := range doc.States {
		s := s
		var regionV any
		if s.Region > 0 {
			regionV = float64(s.Region)
		}
		if s.Composite {
			wants = append(wants, &umlWant{id: s.ID, cat: "state", typ: "group", variant: "composite", parent: s.Parent, line: s.Line, groupW: 360, groupH: 220,
				data: func(o Obj) Obj {
					d := withDefaults(o, Obj{"accent": stateAccent, "stroke": stateAccent, "fillOpacity": 6.0, "border": true})
					d["label"], d["variant"] = s.Label, "composite"
					setOrDel(d, "region", regionV)
					return d
				}})
		} else {
			wants = append(wants, &umlWant{id: s.ID, cat: "state", typ: "state", parent: s.Parent, line: s.Line,
				data: func(o Obj) Obj {
					d := withDefaults(o, Obj{"accent": stateAccent})
					d["label"], d["stateKind"] = s.Label, s.Kind
					setOrDel(d, "activity", strOrNil(s.Activity))
					setOrDel(d, "vertical", s.Vertical && (s.Kind == "fork" || s.Kind == "join"))
					setOrDel(d, "region", regionV)
					return d
				},
				size: func(d Obj) (float64, float64) {
					return stateSizeOf(jsStr(d["stateKind"]), jsStr(d["label"]), str(d["activity"]), truthy(d["vertical"]))
				}})
		}
	}
	for _, u := range doc.UcItems {
		u := u
		if u.Kind == "boundary" {
			wants = append(wants, &umlWant{id: u.ID, cat: "uc", typ: "group", variant: "boundary", line: u.Line, groupW: 420, groupH: 320,
				data: func(o Obj) Obj {
					d := withDefaults(o, Obj{"accent": ucAccent, "stroke": ucAccent, "fillOpacity": 3.0, "border": true})
					d["label"], d["variant"] = u.Label, "boundary"
					return d
				}})
		} else {
			wants = append(wants, &umlWant{id: u.ID, cat: "uc", typ: "usecase", parent: u.Parent, line: u.Line,
				data: func(o Obj) Obj {
					d := Obj{"accent": ucAccent}
					for k, v := range o {
						d[k] = v
					}
					d["label"], d["ucKind"] = u.Label, u.Kind
					if len(u.Points) > 0 && u.Kind == "usecase" {
						ps := []any{}
						for _, p := range u.Points {
							ps = append(ps, p)
						}
						d["extensionPoints"] = ps
					} else {
						delete(d, "extensionPoints")
					}
					return d
				},
				size: func(d Obj) (float64, float64) { return shapeSizeOf("usecase", d) }})
		}
	}
	containers := map[string]bool{}
	for _, c := range doc.Comps {
		if c.Parent != "" {
			containers[c.Parent] = true
		}
	}
	for _, c := range doc.Comps {
		c := c
		wants = append(wants, &umlWant{id: c.ID, cat: "comp", typ: "component", parent: c.Parent, line: c.Line,
			data: func(o Obj) Obj {
				d := Obj{"accent": compAccent}
				for k, v := range o {
					d[k] = v
				}
				d["label"], d["compKind"] = c.Label, c.Kind
				setOrDel(d, "stereotype", strOrNil(c.Stereotype))
				if c.ArchRef != nil {
					d["archRef"] = archRefObj(c.ArchRef)
				} else {
					delete(d, "archRef")
				}
				var old []Obj
				if o != nil {
					old = objs(o["ports"])
				}
				used := map[string]bool{}
				next := 0
				for _, p := range old {
					if x := rePortID.FindStringSubmatch(str(p["id"])); x != nil {
						n, _ := strconv.Atoi(x[1])
						if n > next {
							next = n
						}
					}
				}
				type pk struct {
					p       Obj
					fromOld bool
				}
				pick := func(kind string, names []string, side string) []pk {
					var res []pk
					for _, name := range names {
						var hit Obj
						for _, p := range old {
							if p["kind"] == kind && p["name"] == name && !used[str(p["id"])] {
								hit = p
								break
							}
						}
						if hit != nil {
							used[str(hit["id"])] = true
							res = append(res, pk{hit, true})
							continue
						}
						next++
						id := "p" + strconv.Itoa(next)
						used[id] = true
						res = append(res, pk{Obj{"id": id, "name": name, "kind": kind, "side": side}, false})
					}
					return res
				}
				ports := append(pick("provided", c.Provides, "left"), pick("required", c.Requires, "right")...)
				count := map[string]int{}
				for _, p := range ports {
					count[str(p.p["side"])]++
				}
				seen := map[string]int{}
				spread := []any{}
				for _, p := range ports {
					side := str(p.p["side"])
					i := seen[side]
					seen[side] = i + 1
					if p.fromOld {
						spread = append(spread, p.p)
					} else {
						np := copyObj(p.p)
						np["offset"] = float64(i+1) / float64(count[side]+1)
						spread = append(spread, np)
					}
				}
				if len(spread) > 0 {
					d["ports"] = spread
				} else {
					delete(d, "ports")
				}
				setOrDel(d, "container", containers[c.ID])
				return d
			},
			size: func(d Obj) (float64, float64) { return shapeSizeOf("component", d) }})
	}
	for _, l := range doc.ActLanes {
		l := l
		w, h := 240.0, 520.0
		if l.Orient == "h" {
			w, h = 640, 200
		}
		wants = append(wants, &umlWant{id: l.ID, cat: "act", typ: "group", variant: "lane", line: l.Line, groupW: w, groupH: h,
			data: func(o Obj) Obj {
				d := withDefaults(o, Obj{"accent": actAccent, "stroke": actAccent, "fillOpacity": 3.0, "border": true})
				d["label"], d["variant"], d["laneOrient"] = l.Label, "lane", l.Orient
				return d
			}})
	}
	for _, a := range doc.ActNodes {
		a := a
		wants = append(wants, &umlWant{id: a.ID, cat: "act", typ: "activity", parent: a.Lane, line: a.Line,
			data: func(o Obj) Obj {
				d := Obj{"accent": actAccent}
				for k, v := range o {
					d[k] = v
				}
				d["label"], d["actKind"] = a.Label, a.Kind
				setOrDel(d, "vertical", a.Vertical && a.Kind == "fork")
				if a.Kind == "object" {
					setOrDel(d, "objectState", strOrNil(a.State))
				} else {
					delete(d, "objectState")
				}
				return d
			},
			size: actSizeOf})
	}
	depContainers := map[string]bool{}
	for _, c := range doc.DepNodes {
		if c.Parent != "" {
			depContainers[c.Parent] = true
		}
	}
	for _, c := range doc.DepNodes {
		c := c
		var hit *CatalogHit
		if c.Icon != "" {
			if h, ok := ResolveIcon(c.Icon); ok {
				hit = &h
			} else {
				env.warn(c.Line, fmt.Sprintf(`unknown icon "%s" (autocomplete lists the catalog)`, c.Icon))
			}
		}
		if hit != nil && hit.Kind == "cluster" {
			env.warn(c.Line, fmt.Sprintf(`"%s" is a cluster profile; use an icon id`, c.Icon))
			hit = nil
		}
		wants = append(wants, &umlWant{id: c.ID, cat: "dep", typ: "deployment", parent: c.Parent, line: c.Line,
			data: func(o Obj) Obj {
				d := Obj{"accent": depAccent}
				for k, v := range o {
					d[k] = v
				}
				d["label"], d["depKind"] = c.Label, c.Kind
				setOrDel(d, "stereotype", strOrNil(c.Stereotype))
				if hit != nil {
					setOrDel(d, "iconId", strOrNil(hit.ID))
				} else {
					delete(d, "iconId")
				}
				setOrDel(d, "container", depContainers[c.ID])
				return d
			},
			size: depSizeOf})
	}
	for _, o := range doc.Objects {
		o := o
		wants = append(wants, &umlWant{id: o.ID, cat: "obj", typ: "object", line: o.Line,
			data: func(old Obj) Obj {
				d := withDefaults(old, Obj{"accent": objAccent})
				d["label"] = o.Label
				setOrDel(d, "className", strOrNil(o.ClassName))
				setOrDel(d, "modelId", strOrNil(o.ModelID))
				prev := objs(d["slots"])
				used := map[string]bool{}
				next := 0
				for _, x := range prev {
					if m := reSlotID.FindStringSubmatch(str(x["id"])); m != nil {
						n, _ := strconv.Atoi(m[1])
						if n+1 > next {
							next = n + 1
						}
					}
				}
				slots := []any{}
				for _, sl := range o.Slots {
					var hit Obj
					for _, x := range prev {
						if jsStr(x["name"]) == sl.Name && !used[str(x["id"])] {
							hit = x
							break
						}
					}
					id := ""
					m := Obj{}
					if hit != nil {
						id = str(hit["id"])
						m = copyObj(hit)
					} else {
						id = "s" + strconv.Itoa(next)
						next++
					}
					used[id] = true
					m["id"], m["name"], m["value"] = id, sl.Name, sl.Value
					slots = append(slots, m)
				}
				d["slots"] = slots
				return d
			},
			size: objSizeOf})
	}

	// ---- nodes ----
	byID := map[string]Obj{}
	for _, n := range nodesOf(page) {
		byID[str(n["id"])] = n
	}
	wanted := map[string]bool{}
	for _, w := range wants {
		old := byID[w.id]
		if old != nil && catOf(old) != w.cat {
			err(w.line, fmt.Sprintf(`id "%s" is used by a node the text cannot edit as this kind (%s)`, w.id, jsStr(old["type"])))
			continue
		}
		wanted[w.id] = true
		n := old
		if n != nil && (str(n["type"]) != w.typ || (w.typ == "group" && dataOf(n)["variant"] != w.variant)) {
			rebuilt := Obj{"id": w.id, "type": w.typ, "position": n["position"], "data": w.data(nil)}
			if p := str(n["parentId"]); p != "" {
				rebuilt["parentId"] = p
			}
			nodes := list(page["nodes"])
			for i, x := range nodes {
				if o, ok := x.(Obj); ok && str(o["id"]) == w.id {
					nodes[i] = rebuilt
				}
			}
			byID[w.id] = rebuilt
			if w.groupW > 0 {
				rebuilt["width"], rebuilt["height"] = numOr(n["width"], w.groupW), numOr(n["height"], w.groupH)
			} else if w.size != nil {
				rebuilt["width"], rebuilt["height"] = w.size(rebuilt["data"].(Obj))
			}
			n = rebuilt
		} else if n == nil {
			n = Obj{"id": w.id, "type": w.typ, "position": Obj{"x": 0.0, "y": 0.0}, "data": w.data(nil)}
			if w.groupW > 0 {
				n["width"], n["height"] = w.groupW, w.groupH
			} else if w.size != nil {
				n["width"], n["height"] = w.size(n["data"].(Obj))
			}
			page["nodes"] = append(list(page["nodes"]), n)
			byID[w.id] = n
			added.add(w.id)
			fresh.add(w.id)
		} else {
			before := dataKey(n)
			wasContainer := truthy(dataOf(n)["container"])
			n["data"] = w.data(dataOf(n))
			if w.cat == "dep" && truthy(dataOf(n)["container"]) {
				// a deployment container keeps the size it was laid out with; a node that just became one gets room for its content
				if !wasContainer {
					n["width"], n["height"] = 320.0, 220.0
				}
			} else if w.size != nil && dataKey(n) != before {
				n["width"], n["height"] = w.size(dataOf(n))
			}
		}
		if w.cat == "comp" && containers[w.id] && !truthy(n["width"]) {
			n["width"], n["height"] = 300.0, 200.0
		}
		if w.cat == "dep" && depContainers[w.id] && added.has(w.id) {
			n["width"], n["height"] = 320.0, 220.0
		}
		if str(n["parentId"]) != w.parent {
			if w.parent != "" {
				n["parentId"] = w.parent
			} else {
				delete(n, "parentId")
			}
			if !added.has(w.id) {
				fresh.add(w.id)
			}
		}
	}
	for _, w := range wants {
		if w.parent != "" && byID[w.parent] == nil {
			err(w.line, fmt.Sprintf(`unknown parent "%s"`, w.parent))
		}
	}
	for _, n := range oldNodes {
		if !wanted[str(n["id"])] {
			*env.removed = append(*env.removed, str(n["id"]))
		}
	}

	// ---- edges ----
	edgeIDs := map[string]bool{}
	for _, e := range edgesOf(page) {
		edgeIDs[str(e["id"])] = true
	}
	type endSpec struct {
		end    string
		dashed bool
	}
	ends := map[string]endSpec{"inheritance": {"hollowTriangle", false}, "realization": {"hollowTriangle", true}, "composition": {"filledDiamond", false}, "aggregation": {"hollowDiamond", false}, "dependency": {"open", true}, "transition": {"arrow", false}, "include": {"open", true}, "extend": {"open", true}, "generalization": {"hollowTriangle", false}, "assembly": {"none", false}, "note": {"none", true}, "communication": {"none", false}, "deploy": {"open", true}, "manifest": {"open", true}, "import": {"open", true}, "access": {"open", true}, "merge": {"open", true}, "link": {"none", false}}
	sync := func(old []Obj, wl []umlWantEdge, prefix string) {
		pool := map[string][]Obj{}
		for _, e := range old {
			k := str(e["source"]) + "|" + str(e["target"]) + "|" + jsStr(umlOf(e)["kind"])
			pool[k] = append(pool[k], e)
		}
		seenPair := map[string]int{}
		keep := map[string]bool{}
		for _, w := range wl {
			key := w.s + "|" + w.t + "|" + jsStr(w.uml["kind"])
			k := seenPair[key]
			seenPair[key] = k + 1
			var e Obj
			if k < len(pool[key]) {
				e = pool[key][k]
			}
			isNew := e == nil
			if isNew {
				e = Obj{"id": uniq(prefix+"-"+slug(w.s)+"-"+slug(w.t), edgeIDs), "source": w.s, "target": w.t}
				page["edges"] = append(list(page["edges"]), e)
				added.add(str(e["id"]))
			}
			keep[str(e["id"])] = true
			if w.label != "" {
				e["label"] = w.label
			} else {
				delete(e, "label")
			}
			u := Obj{}
			for k, v := range umlOf(e) {
				u[k] = v
			}
			for k, v := range w.uml {
				u[k] = v
			}
			fields := append([]string{"directed", "sourceMult", "targetMult", "sourceRole", "targetRole"}, w.strip...)
			for _, f := range fields {
				if _, ok := w.uml[f]; !ok {
					delete(u, f)
				}
			}
			spec, ok := ends[jsStr(u["kind"])]
			if !ok {
				spec = endSpec{"none", false}
				if truthy(u["directed"]) {
					spec.end = "open"
				}
			}
			endMarker := spec.end
			if u["kind"] == "association" {
				endMarker = "none"
				if truthy(u["directed"]) {
					endMarker = "open"
				}
			}
			data := Obj{}
			for k, v := range obj(e["data"]) {
				data[k] = v
			}
			data["uml"] = u
			if isNew || data["color"] == nil {
				data["color"] = umlColor
			}
			if isNew || data["arrows"] == nil {
				data["arrows"] = "none"
			}
			data["startMarker"] = "none"
			data["endMarker"] = endMarker
			if spec.dashed {
				data["strokeStyle"] = "dashed"
			} else {
				data["strokeStyle"] = "solid"
			}
			data["dashed"] = spec.dashed
			e["data"] = data
		}
		for _, e := range old {
			if !keep[str(e["id"])] {
				*env.removed = append(*env.removed, str(e["id"]))
			}
		}
	}
	stereo := func(kind string) string {
		if kind == "include" || kind == "extend" || kind == "deploy" || kind == "manifest" || kind == "import" || kind == "access" || kind == "merge" {
			return "«" + kind + "»"
		}
		return ""
	}
	has := func(id string) bool { return id != "" && byID[id] != nil }
	var wl []umlWantEdge
	for _, r := range doc.ClassRels {
		s, t := classID[r.From], classID[r.To]
		if !has(s) || !has(t) {
			continue
		}
		u := Obj{"kind": r.Kind}
		if r.Directed {
			u["directed"] = true
		}
		if r.SrcMult != "" {
			u["sourceMult"] = r.SrcMult
		}
		if r.DstMult != "" {
			u["targetMult"] = r.DstMult
		}
		if r.SrcRole != "" {
			u["sourceRole"] = r.SrcRole
		}
		if r.DstRole != "" {
			u["targetRole"] = r.DstRole
		}
		wl = append(wl, umlWantEdge{s: s, t: t, label: r.Label, uml: u})
	}
	sync(oldCls, wl, "uml")
	wl = nil
	for _, t := range doc.Transitions {
		if has(t.From) && has(t.To) {
			wl = append(wl, umlWantEdge{s: t.From, t: t.To, label: t.Label, uml: Obj{"kind": "transition"}})
		}
	}
	sync(oldState, wl, "te")
	wl = nil
	for _, r := range doc.UcRels {
		if !has(r.From) || !has(r.To) {
			continue
		}
		label := r.Label
		if label == "" {
			label = stereo(r.Kind)
		}
		u := Obj{"kind": r.Kind}
		if r.SrcMult != "" {
			u["sourceMult"] = r.SrcMult
		}
		if r.DstMult != "" {
			u["targetMult"] = r.DstMult
		}
		wl = append(wl, umlWantEdge{s: r.From, t: r.To, label: label, uml: u})
	}
	sync(oldUc, wl, "ue")
	wl = nil
	for _, l := range doc.CompLinks {
		if has(l.From) && has(l.To) {
			wl = append(wl, umlWantEdge{s: l.From, t: l.To, label: l.Label, uml: Obj{"kind": l.Kind}, strip: []string{"sourcePort", "targetPort"}})
		}
	}
	for _, r := range doc.Comps {
		for _, name := range r.Requires {
			for _, p := range doc.Comps {
				prov := false
				for _, x := range p.Provides {
					if x == name {
						prov = true
					}
				}
				if p.ID == r.ID || !prov || !has(r.ID) || !has(p.ID) {
					continue
				}
				var sp, tp Obj
				for _, x := range objs(dataOf(byID[r.ID])["ports"]) {
					if x["kind"] == "required" && x["name"] == name {
						sp = x
						break
					}
				}
				for _, x := range objs(dataOf(byID[p.ID])["ports"]) {
					if x["kind"] == "provided" && x["name"] == name {
						tp = x
						break
					}
				}
				if sp != nil && tp != nil {
					wl = append(wl, umlWantEdge{s: r.ID, t: p.ID, uml: Obj{"kind": "assembly", "sourcePort": sp["id"], "targetPort": tp["id"]}})
				}
			}
		}
	}
	sync(oldComp, wl, "ce")
	wl = nil
	for _, f := range doc.ActFlows {
		if has(f.From) && has(f.To) {
			kind, label := "transition", f.Label
			if f.Note {
				kind, label = "note", ""
			}
			wl = append(wl, umlWantEdge{s: f.From, t: f.To, label: label, uml: Obj{"kind": kind}})
		}
	}
	sync(oldAct, wl, "af")
	wl = nil
	for _, l := range doc.DepLinks {
		if has(l.From) && has(l.To) {
			label := l.Label
			if label == "" {
				label = stereo(l.Kind)
			}
			wl = append(wl, umlWantEdge{s: l.From, t: l.To, label: label, uml: Obj{"kind": l.Kind}})
		}
	}
	sync(oldDep, wl, "de")
	wl = nil
	for _, r := range doc.PkgRels {
		if has(r.From) && has(r.To) {
			label := r.Label
			if label == "" {
				label = stereo(r.Kind)
			}
			wl = append(wl, umlWantEdge{s: r.From, t: r.To, label: label, uml: Obj{"kind": r.Kind}})
		}
	}
	sync(oldPkg, wl, "pe")
	wl = nil
	// communication diagram: each numbered message goes to the first link between its two objects; the link text is the messages
	taken := map[*DslCommMsg]bool{}
	for _, l := range doc.ObjLinks {
		if has(l.From) && has(l.To) {
			u := Obj{"kind": "link"}
			if l.SrcRole != "" {
				u["sourceRole"] = l.SrcRole
			}
			if l.DstRole != "" {
				u["targetRole"] = l.DstRole
			}
			label := l.Label
			var msgs []any
			var lines []string
			for i, m := range doc.CommMsgs {
				if taken[m] || !((m.From == l.From && m.To == l.To) || (m.From == l.To && m.To == l.From)) {
					continue
				}
				taken[m] = true
				dir := "back"
				if m.From == l.From {
					dir = "fwd"
				}
				mo := Obj{"id": "m" + strconv.Itoa(i+1), "seq": m.Seq, "text": m.Text, "dir": dir}
				arrow := "→"
				switch {
				case m.Async && dir == "fwd":
					arrow = "⇢"
				case m.Async:
					arrow = "⇠"
				case dir == "back":
					arrow = "←"
				}
				if m.Async {
					mo["async"] = true
				}
				sp := ""
				if m.Text != "" {
					sp = " "
				}
				lines = append(lines, m.Seq+": "+m.Text+sp+arrow)
				msgs = append(msgs, mo)
			}
			if len(msgs) > 0 {
				u["messages"] = msgs
				label = strings.Join(lines, "\n")
			}
			wl = append(wl, umlWantEdge{s: l.From, t: l.To, label: label, uml: u, strip: []string{"messages"}})
		}
	}
	sync(oldObj, wl, "oe")
	return owned
}
