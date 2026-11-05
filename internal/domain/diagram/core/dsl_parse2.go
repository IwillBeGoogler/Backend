package core

import (
	"regexp"
	"strings"
	"unicode"
)

// UML blocks of the ".sdd" text: classes, states, usecases, components, activity, deployment, packages, objects (core/dsl/parse2.ts, types.ts).

type DslArchRef struct{ NodeID, FileID, PageID string }

type DslParam struct{ Name, Type string }

type DslMember struct {
	Vis              string
	Static, Abstract bool
	Name, Type       string
	Default          string
	Params           []DslParam
	Op               bool
}

type DslClass struct {
	ArchRef                   *DslArchRef
	ModelID                   string
	Name, Kind                string
	Generics, Stereotype, Pkg string
	Attrs, Ops                []*DslMember
	Line                      int
}

type DslPackage struct {
	ID, Label, Parent string
	Line              int
}

type DslClassRel struct {
	From, To, Kind                            string
	Directed                                  bool
	Label, SrcMult, DstMult, SrcRole, DstRole string
	Line                                      int
}

type DslState struct {
	ID, Label, Kind string
	Composite       bool
	Parent          string
	Region          int
	Activity        string
	Vertical        bool
	Line            int
}

type DslTransition struct {
	From, To, Label string
	Line            int
}

type DslUcItem struct {
	ID, Label, Kind, Parent string
	Points                  []string
	Line                    int
}

type DslUcRel struct {
	From, To, Kind, Label, SrcMult, DstMult string
	Line                                    int
}

type DslComp struct {
	ArchRef            *DslArchRef
	ID, Label, Kind    string
	Stereotype, Parent string
	Provides, Requires []string
	Line               int
}

type DslCompLink struct {
	From, To, Kind, Label string
	Line                  int
}

type DslActNode struct {
	ID, Label, Kind, Lane, State string
	Vertical                     bool
	Line                         int
}

type DslActLane struct {
	ID, Label, Orient string
	Line              int
}

// DslActFlow is a control flow (Label = the "[guard]" text as written) or, with Note, a dashed link from a note.
type DslActFlow struct {
	From, To, Label string
	Note            bool
	Line            int
}

type DslDepNode struct {
	ID, Label, Kind, Stereotype, Icon, Parent string
	Line                                      int
}

type DslDepLink struct {
	From, To, Kind, Label string
	Line                  int
}

// DslPkgRel is a relation between packages (import, access, merge, dependency); the packages are the class packages.
type DslPkgRel struct {
	From, To, Kind, Label string
	Line                  int
}

type DslSlot struct{ Name, Value string }

type DslObject struct {
	ID, Label, ClassName string
	ModelID              string
	Slots                []*DslSlot
	Line                 int
}

type DslObjLink struct {
	From, To, Label, SrcRole, DstRole string
	Line                              int
}

// DslCommMsg is a numbered message of a communication diagram (a message on the link between From and To).
type DslCommMsg struct {
	Seq, From, To, Text string
	Async               bool
	Line                int
}

var reCommSeq = regexp.MustCompile(`^\d+(\.\d+)*$`)

type umlFrame struct {
	k      string // package, class, state, boundary, ucase, comp, lane, dep, object
	id     string
	obj    *DslObject
	cls    *DslClass
	region int
	item   *DslUcItem
}

type umlCtx struct {
	block  string // classes, states, usecases, components, activity, deployment, packages, objects
	frames []*umlFrame
}

var umlBlocks = map[string]bool{"classes": true, "states": true, "usecases": true, "components": true, "activity": true, "deployment": true, "packages": true, "objects": true, "communication": true}

type classOp struct {
	kind     string
	directed bool
}

var classRelOps = map[string]classOp{
	"--|>": {"inheritance", false},
	"..|>": {"realization", false},
	"--*":  {"composition", false},
	"--o":  {"aggregation", false},
	"-->":  {"association", true},
	"--":   {"association", false},
	"..>":  {"dependency", false},
}
var stateKinds = map[string]bool{"state": true, "initial": true, "final": true, "choice": true, "fork": true, "join": true, "history": true, "deepHistory": true, "entryPoint": true, "exitPoint": true}
var ucRelWords = map[string]string{"include": "include", "extend": "extend", "generalizes": "generalization"}
var compKinds = map[string]bool{"component": true, "node": true, "artifact": true}
var actKinds = map[string]bool{"initial": true, "final": true, "flowFinal": true, "action": true, "decision": true, "fork": true, "object": true, "send": true, "accept": true, "note": true}

// activity elements whose label defaults to empty (the others default to their id)
var actNoDefault = map[string]bool{"initial": true, "final": true, "flowFinal": true, "fork": true, "decision": true}
var depKinds = map[string]bool{"node": true, "device": true, "executionEnvironment": true, "artifact": true, "component": true}
var depContainers = map[string]bool{"node": true, "device": true, "executionEnvironment": true}
var pkgRelWords = map[string]string{"imports": "import", "access": "access", "merges": "merge", "..>": "dependency"}
var reSlot = regexp.MustCompile(`^("[^"]+"|[^\s=]+)\s*(?:=\s*(.*))?$`)
var depLinkWords = map[string]string{"--": "communication", "..>": "deploy", "manifests": "manifest"}

func (c *umlCtx) inClassBody() bool {
	return len(c.frames) > 0 && (c.frames[len(c.frames)-1].k == "class" || c.frames[len(c.frames)-1].k == "object")
}

func splitTopLevel(s string) []string {
	var out []string
	depth := 0
	var cur strings.Builder
	var prev rune
	for _, ch := range s {
		if strings.ContainsRune("<([{", ch) {
			depth++
		} else if strings.ContainsRune(">)]}", ch) && !(ch == '>' && prev == '=') {
			if depth > 0 {
				depth--
			}
		}
		if ch == ',' && depth == 0 {
			out = append(out, strings.TrimSpace(cur.String()))
			cur.Reset()
		} else {
			cur.WriteRune(ch)
		}
		prev = ch
	}
	if strings.TrimSpace(cur.String()) != "" {
		out = append(out, strings.TrimSpace(cur.String()))
	}
	return out
}

func unquoteDq(s string) string {
	t := strings.TrimSpace(s)
	if len(t) > 1 && t[0] == '"' && t[len(t)-1] == '"' {
		return reUnqEsc.ReplaceAllString(t[1:len(t)-1], "$1")
	}
	return t
}

func trimStartJS(s string) string { return strings.TrimLeftFunc(s, unicode.IsSpace) }

func firstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}

var (
	reMemMod    = regexp.MustCompile(`^(static|abstract)\s+`)
	reMemVis    = regexp.MustCompile(`^([+\-#~])\s*`)
	reMemName   = regexp.MustCompile(`^("[^"]+"|[^\s:(=]+)`)
	reParam     = regexp.MustCompile(`^([^:\s]+)\s*(?::\s*(.+))?$`)
	reOpTail    = regexp.MustCompile(`^\s*(?::\s*(.*))?$`)
	reAttrTail  = regexp.MustCompile(`^(?::\s*(.*?))?\s*(?:=\s*(.*))?$`)
	reClassHead = regexp.MustCompile(`^(abstract\s+class|class|interface|enum)\s+("[^"]+"|[^\s<{"]+)(.*)$`)
)

// parseMember parses one member line; the second result is an error text.
func parseMember(body string) (*DslMember, string) {
	t := strings.TrimSpace(body)
	vis := ""
	st, ab := false, false
	for {
		if w := reMemMod.FindStringSubmatch(t); w != nil {
			if w[1] == "static" {
				st = true
			} else {
				ab = true
			}
			t = t[len(w[0]):]
			continue
		}
		if v := reMemVis.FindStringSubmatch(t); v != nil && vis == "" {
			vis = v[1]
			t = t[len(v[0]):]
			continue
		}
		break
	}
	nm := reMemName.FindStringSubmatch(t)
	if nm == nil {
		return nil, "expected a member:  + name: Type  or  + name(a: A): R"
	}
	name := unquoteDq(nm[1])
	t = trimStartJS(t[len(nm[0]):])
	if strings.HasPrefix(t, "(") {
		depth, end := 0, -1
		for i := 0; i < len(t); i++ {
			if t[i] == '(' {
				depth++
			} else if t[i] == ')' {
				depth--
				if depth == 0 {
					end = i
					break
				}
			}
		}
		if end < 0 {
			return nil, `missing ")" in the operation`
		}
		params := []DslParam{}
		for _, p := range splitTopLevel(t[1:end]) {
			if m := reParam.FindStringSubmatch(p); m != nil {
				pp := DslParam{Name: m[1]}
				if m[2] != "" {
					pp.Type = strings.TrimSpace(m[2])
				}
				params = append(params, pp)
			} else {
				params = append(params, DslParam{Name: p})
			}
		}
		rest := reOpTail.FindStringSubmatch(t[end+1:])
		if rest == nil {
			return nil, "unexpected text after the operation"
		}
		return &DslMember{Vis: vis, Static: st, Abstract: ab, Name: name, Op: true, Params: params, Type: strings.TrimSpace(rest[1])}, ""
	}
	m := reAttrTail.FindStringSubmatch(t)
	if m == nil || (t != "" && !strings.HasPrefix(t, ":") && !strings.HasPrefix(t, "=")) {
		return nil, `unexpected "` + firstRunes(t, 20) + `" after the member name`
	}
	return &DslMember{Vis: vis, Static: st, Abstract: ab, Name: name, Type: strings.TrimSpace(m[1]), Default: strings.TrimSpace(m[2])}, ""
}

func archRefOf(a map[string]string) *DslArchRef {
	if a["archRef"] == "" {
		return nil
	}
	return &DslArchRef{NodeID: a["archRef"], FileID: a["archFile"], PageID: a["archPage"]}
}

func attrsOf(toks []tok, from, to int, err func(t tok, m string)) map[string]string {
	m, _ := attrsOfK(toks, from, to, err)
	return m
}

// attrsOfK also returns the keys in insertion order (JS object order).
func attrsOfK(toks []tok, from, to int, err func(t tok, m string)) (map[string]string, []string) {
	out := map[string]string{}
	var keys []string
	set := func(k, v string) {
		if _, ok := out[k]; !ok {
			keys = append(keys, k)
		}
		out[k] = v
	}
	i := from
	for i < to {
		t := toks[i]
		if t.k == "sym" && t.v == "," {
			i++
			continue
		}
		if t.k != "word" && t.k != "str" {
			err(t, `unexpected "`+t.v+`"`)
			i++
			continue
		}
		if i+1 < len(toks) && toks[i+1].k == "sym" && toks[i+1].v == "=" && i+2 < to && toks[i+2].k != "sym" {
			set(t.v, toks[i+2].v)
			i += 3
		} else {
			set(t.v, "true")
			i++
		}
	}
	return out, keys
}

func (c *umlCtx) parentOf(k string) *umlFrame {
	for i := len(c.frames) - 1; i >= 0; i-- {
		if c.frames[i].k == k {
			return c.frames[i]
		}
	}
	return nil
}

func parseUmlLine(c *umlCtx, src string, ln int, doc *DslDoc, diag func(l, col int, m, sev string), ids map[string]bool, stack *[]*ctx) {
	derr := func(l, col int, m string) { diag(l, col, m, "error") }
	s := strings.TrimSpace(src)
	var frame *umlFrame
	if len(c.frames) > 0 {
		frame = c.frames[len(c.frames)-1]
	}
	if s == "}" {
		if len(c.frames) > 0 {
			c.frames = c.frames[:len(c.frames)-1]
		} else {
			*stack = (*stack)[:len(*stack)-1]
		}
		return
	}
	if frame != nil && frame.k == "class" {
		m, e := parseMember(s)
		if e != "" {
			derr(ln, 1, e)
			return
		}
		cls := frame.cls
		if cls.Kind == "enum" {
			m.Op = false
			cls.Attrs = append(cls.Attrs, m)
			return
		}
		if m.Op {
			cls.Ops = append(cls.Ops, m)
		} else {
			cls.Attrs = append(cls.Attrs, m)
		}
		return
	}
	if frame != nil && frame.k == "object" {
		m := reSlot.FindStringSubmatch(s)
		if m == nil {
			derr(ln, 1, "expected a slot:  attribute = value")
			return
		}
		frame.obj.Slots = append(frame.obj.Slots, &DslSlot{Name: unquoteDq(m[1]), Value: strings.TrimSpace(m[2])})
		return
	}
	toks, lerr := tokenize(src)
	if lerr != nil {
		derr(ln, lerr.col, lerr.msg)
		return
	}
	if len(toks) == 0 {
		return
	}
	terr := func(t tok, m string) { derr(ln, t.col, m) }
	last := toks[len(toks)-1]
	opens := last.k == "sym" && last.v == "{"
	end := len(toks)
	if opens {
		end--
	}
	first := toks[0]
	kw := ""
	if first.k == "word" {
		kw = first.v
	}
	claim := func(t tok) bool {
		if ids[t.v] {
			derr(ln, t.col, `duplicate id "`+t.v+`"`)
			return false
		}
		ids[t.v] = true
		return true
	}
	tokAt := func(i int) *tok {
		if i < len(toks) {
			return &toks[i]
		}
		return nil
	}

	switch c.block {
	case "classes":
		if kw == "package" {
			idTok := tokAt(1)
			if idTok == nil || idTok.k == "sym" {
				terr(first, "package needs an id, e.g. package shop \"Shop\" {")
				return
			}
			if !opens {
				terr(last, `expected "{" after the package`)
				return
			}
			label := idTok.v
			if t2 := tokAt(2); t2 != nil && t2.k == "str" {
				label = t2.v
			}
			if claim(*idTok) {
				p := &DslPackage{ID: idTok.v, Label: label, Line: ln}
				if pp := c.parentOf("package"); pp != nil {
					p.Parent = pp.id
				}
				doc.Packages = append(doc.Packages, p)
			}
			c.frames = append(c.frames, &umlFrame{k: "package", id: idTok.v})
			return
		}
		if hm := reClassHead.FindStringSubmatch(s); hm != nil {
			kind := hm[1]
			if strings.HasPrefix(kind, "abstract") {
				kind = "abstract"
			}
			name := unquoteDq(hm[2])
			rest := hm[3]
			generics := ""
			if strings.HasPrefix(rest, "<") {
				depth, i := 0, 0
				for ; i < len(rest); i++ {
					if rest[i] == '<' {
						depth++
					} else if rest[i] == '>' && !(i > 0 && rest[i-1] == '=') {
						depth--
						if depth == 0 {
							break
						}
					}
				}
				if i >= len(rest) {
					derr(ln, 1, `missing ">" in the type parameters`)
					return
				}
				generics = strings.TrimSpace(rest[1:i])
				rest = rest[i+1:]
			}
			rt, rl := tokenize(rest)
			if rl != nil {
				derr(ln, 1, rl.msg)
				return
			}
			ropens := len(rt) > 0 && rt[len(rt)-1].k == "sym" && rt[len(rt)-1].v == "{"
			if ropens {
				rt = rt[:len(rt)-1]
			}
			stereotype := ""
			cls := &DslClass{Name: name, Kind: kind, Line: ln}
			type sugarRel struct{ to, kind string }
			var sugar []sugarRel
			mode := ""
			hattrs := map[string]string{}
			for ti := 0; ti < len(rt); ti++ {
				t := rt[ti]
				switch {
				case t.k == "word" && ti+2 < len(rt) && rt[ti+1].k == "sym" && rt[ti+1].v == "=" && rt[ti+2].k != "sym":
					hattrs[t.v] = rt[ti+2].v
					ti += 2
				case t.k == "str":
					stereotype = t.v
				case t.k == "word" && (t.v == "extends" || t.v == "implements"):
					mode = t.v
				case t.k == "word" && mode != "":
					k := "realization"
					if mode == "extends" {
						k = "inheritance"
					}
					sugar = append(sugar, sugarRel{t.v, k})
				case t.k == "sym" && t.v == "," && mode != "":
				default:
					derr(ln, t.col, `unexpected "`+t.v+`" in the class header`)
				}
			}
			cls.ArchRef = archRefOf(hattrs)
			cls.ModelID = hattrs["model"]
			dup := false
			for _, x := range doc.Classes {
				if x.Name == name {
					dup = true
				}
			}
			if dup {
				derr(ln, 1, `duplicate class "`+name+`"`)
			} else {
				pkg := c.parentOf("package")
				cls.Generics = generics
				cls.Stereotype = stereotype
				if pkg != nil {
					cls.Pkg = pkg.id
				}
				doc.Classes = append(doc.Classes, cls)
				for _, x := range sugar {
					doc.ClassRels = append(doc.ClassRels, &DslClassRel{From: name, To: x.to, Kind: x.kind, Line: ln})
				}
			}
			if ropens {
				c.frames = append(c.frames, &umlFrame{k: "class", cls: cls})
			}
			return
		}
		var op classOp
		isOp := false
		if t1 := tokAt(1); t1 != nil && t1.k == "word" {
			op, isOp = classRelOps[t1.v]
		}
		if isOp && len(toks) >= 3 && toks[0].k != "sym" {
			i := 3
			label := ""
			if t := tokAt(i); t != nil && t.k == "str" {
				label = t.v
				i++
			}
			a, keys := attrsOfK(toks, i, len(toks), terr)
			for _, k := range keys {
				if k != "srcMult" && k != "dstMult" && k != "srcRole" && k != "dstRole" {
					diag(ln, 1, `unknown relation attribute "`+k+`"`, "warning")
				}
			}
			doc.ClassRels = append(doc.ClassRels, &DslClassRel{From: toks[0].v, To: toks[2].v, Kind: op.kind, Directed: op.directed, Label: label, SrcMult: a["srcMult"], DstMult: a["dstMult"], SrcRole: a["srcRole"], DstRole: a["dstRole"], Line: ln})
			return
		}
		terr(first, `unknown statement "`+first.v+`" in classes (expected class, interface, enum, abstract class, package or a relation like A --|> B)`)
		return

	case "states":
		if kw == "region" {
			st := c.parentOf("state")
			if st == nil {
				terr(first, `"region" belongs inside a composite state`)
				return
			}
			st.region++
			return
		}
		if stateKinds[kw] {
			idTok := tokAt(1)
			if idTok == nil || idTok.k == "sym" {
				terr(first, kw+" needs an id, e.g. "+kw+` s1 "Name"`)
				return
			}
			i := 2
			label := ""
			if t := tokAt(i); t != nil && t.k == "str" {
				label = t.v
				i++
			} else if kw == "state" {
				label = idTok.v
			}
			a := attrsOf(toks, i, end, terr)
			par := c.parentOf("state")
			if opens && kw != "state" {
				terr(last, "only a plain state can hold other states")
			}
			if claim(*idTok) {
				st := &DslState{ID: idTok.v, Label: label, Kind: kw, Composite: opens && kw == "state", Activity: a["activity"], Vertical: a["vertical"] != "", Line: ln}
				if par != nil {
					st.Parent = par.id
					st.Region = par.region
					if kw == "entryPoint" || kw == "exitPoint" {
						st.Region = 0
					}
				}
				doc.States = append(doc.States, st)
			}
			if opens && kw == "state" {
				c.frames = append(c.frames, &umlFrame{k: "state", id: idTok.v})
			}
			return
		}
		if len(toks) >= 3 && toks[1].v == "->" && toks[1].k == "word" {
			t := &DslTransition{From: toks[0].v, To: toks[2].v, Line: ln}
			if t3 := tokAt(3); t3 != nil && t3.k == "str" {
				t.Label = t3.v
			}
			doc.Transitions = append(doc.Transitions, t)
			return
		}
		terr(first, `unknown statement "`+first.v+`" in states (expected state, initial, final, choice, fork, join, history, deepHistory, entryPoint, exitPoint, region or a -> b "event")`)
		return

	case "usecases":
		if frame != nil && frame.k == "ucase" {
			if t1 := tokAt(1); kw == "point" && t1 != nil && t1.k != "sym" {
				frame.item.Points = append(frame.item.Points, t1.v)
				return
			}
			terr(first, `expected: point "Extension point"`)
			return
		}
		if kw == "actor" || kw == "usecase" || kw == "boundary" {
			idTok := tokAt(1)
			if idTok == nil || idTok.k == "sym" {
				terr(first, kw+" needs an id, e.g. "+kw+` x "Name"`)
				return
			}
			label := idTok.v
			if t2 := tokAt(2); t2 != nil && t2.k == "str" {
				label = t2.v
			}
			if opens && kw == "actor" {
				terr(last, "an actor has no body")
			}
			par := c.parentOf("boundary")
			item := &DslUcItem{ID: idTok.v, Label: label, Kind: kw, Line: ln}
			if par != nil {
				item.Parent = par.id
			}
			if kw == "boundary" && par != nil {
				terr(first, "boundaries cannot be nested")
			}
			if claim(*idTok) {
				doc.UcItems = append(doc.UcItems, item)
			}
			if opens && kw == "boundary" {
				c.frames = append(c.frames, &umlFrame{k: "boundary", id: idTok.v})
			} else if opens && kw == "usecase" {
				c.frames = append(c.frames, &umlFrame{k: "ucase", item: item})
			}
			return
		}
		w := ""
		if t1 := tokAt(1); t1 != nil && t1.k == "word" {
			w = t1.v
		}
		if _, isWord := ucRelWords[w]; len(toks) >= 3 && (w == "--" || isWord) {
			i := 3
			label := ""
			if t := tokAt(i); t != nil && t.k == "str" {
				label = t.v
				i++
			}
			a := attrsOf(toks, i, len(toks), terr)
			kind := "association"
			if w != "--" {
				kind = ucRelWords[w]
			}
			doc.UcRels = append(doc.UcRels, &DslUcRel{From: toks[0].v, To: toks[2].v, Kind: kind, Label: label, SrcMult: a["srcMult"], DstMult: a["dstMult"], Line: ln})
			return
		}
		terr(first, `unknown statement "`+first.v+`" in usecases (expected actor, usecase, boundary, or a -- b / a include b / a extend b / a generalizes b)`)
		return

	case "activity":
		if kw == "lane" {
			idTok := tokAt(1)
			if idTok == nil || idTok.k == "sym" {
				terr(first, `lane needs an id, e.g. lane shop "Shop" {`)
				return
			}
			if !opens {
				terr(last, `expected "{" after the lane`)
				return
			}
			if c.parentOf("lane") != nil {
				terr(first, "lanes cannot be nested")
			}
			i := 2
			label := idTok.v
			if t := tokAt(i); t != nil && t.k == "str" {
				label = t.v
				i++
			}
			a, keys := attrsOfK(toks, i, end, terr)
			for _, k := range keys {
				if k != "orient" {
					diag(ln, 1, `unknown lane attribute "`+k+`"`, "warning")
				}
			}
			if o, ok := a["orient"]; ok && o != "" && o != "v" && o != "h" {
				diag(ln, 1, "orient is v (columns) or h (rows)", "warning")
			}
			if claim(*idTok) {
				orient := "v"
				if a["orient"] == "h" {
					orient = "h"
				}
				doc.ActLanes = append(doc.ActLanes, &DslActLane{ID: idTok.v, Label: label, Orient: orient, Line: ln})
			}
			c.frames = append(c.frames, &umlFrame{k: "lane", id: idTok.v})
			return
		}
		if actKinds[kw] {
			idTok := tokAt(1)
			if idTok == nil || idTok.k == "sym" {
				terr(first, kw+" needs an id, e.g. "+kw+` x "Name"`)
				return
			}
			if opens {
				terr(last, "an activity element has no body")
			}
			i := 2
			label := idTok.v
			if actNoDefault[kw] {
				label = ""
			}
			if t := tokAt(i); t != nil && t.k == "str" {
				label = t.v
				i++
			}
			a := attrsOf(toks, i, end, terr)
			par := c.parentOf("lane")
			if claim(*idTok) {
				n := &DslActNode{ID: idTok.v, Label: label, Kind: kw, Line: ln}
				if par != nil {
					n.Lane = par.id
				}
				_, vert := a["vertical"]
				n.Vertical = vert && kw == "fork"
				if st, ok := a["state"]; ok && kw == "object" {
					n.State = st
				}
				doc.ActNodes = append(doc.ActNodes, n)
			}
			return
		}
		w := ""
		if t1 := tokAt(1); t1 != nil && t1.k == "word" {
			w = t1.v
		}
		if len(toks) >= 3 && (w == "->" || w == "--") {
			f := &DslActFlow{From: toks[0].v, To: toks[2].v, Note: w == "--", Line: ln}
			if t3 := tokAt(3); t3 != nil && t3.k == "str" {
				f.Label = t3.v
			}
			doc.ActFlows = append(doc.ActFlows, f)
			return
		}
		terr(first, `unknown statement "`+first.v+`" in activity (expected lane, initial, final, flowFinal, action, decision, fork, object, send, accept, note, or a -> b "[guard]" / note -- a)`)
		return

	case "packages":
		if kw == "package" {
			idTok := tokAt(1)
			if idTok == nil || idTok.k == "sym" {
				terr(first, "package needs an id, e.g. package shop \"Shop\" {")
				return
			}
			label := idTok.v
			if t2 := tokAt(2); t2 != nil && t2.k == "str" {
				label = t2.v
			}
			if claim(*idTok) {
				p := &DslPackage{ID: idTok.v, Label: label, Line: ln}
				if pp := c.parentOf("package"); pp != nil {
					p.Parent = pp.id
				}
				doc.Packages = append(doc.Packages, p)
			}
			if opens {
				c.frames = append(c.frames, &umlFrame{k: "package", id: idTok.v})
			}
			return
		}
		w := ""
		if t1 := tokAt(1); t1 != nil && t1.k == "word" {
			w = t1.v
		}
		if kind, ok := pkgRelWords[w]; ok && len(toks) >= 3 {
			r := &DslPkgRel{From: toks[0].v, To: toks[2].v, Kind: kind, Line: ln}
			if t3 := tokAt(3); t3 != nil && t3.k == "str" {
				r.Label = t3.v
			}
			doc.PkgRels = append(doc.PkgRels, r)
			return
		}
		terr(first, `unknown statement "`+first.v+`" in packages (expected package, or a imports b / a access b / a merges b / a ..> b)`)
		return

	case "objects", "communication":
		comm := c.block == "communication"
		// communication: `1.1 a -> b "text" async` is a numbered message on the link between a and b
		if t1, t2, t3 := tokAt(1), tokAt(2), tokAt(3); comm && first.k == "word" && reCommSeq.MatchString(first.v) && len(toks) >= 4 && t1 != nil && t2 != nil && t3 != nil && t2.v == "->" {
			i := 4
			text := ""
			if t := tokAt(i); t != nil && t.k == "str" {
				text = t.v
				i++
			}
			a, keys := attrsOfK(toks, i, len(toks), terr)
			for _, k := range keys {
				if k != "async" {
					diag(ln, 1, `unknown message attribute "`+k+`"`, "warning")
				}
			}
			doc.CommMsgs = append(doc.CommMsgs, &DslCommMsg{Seq: first.v, From: t1.v, To: t3.v, Text: text, Async: a["async"] != "", Line: ln})
			return
		}
		if kw == "object" {
			idTok := tokAt(1)
			if idTok == nil || idTok.k == "sym" {
				terr(first, "object needs an id, e.g. object o1 \"alice\" class=User {")
				return
			}
			i := 2
			label := idTok.v
			if t := tokAt(i); t != nil && t.k == "str" {
				label = t.v
				i++
			}
			a, keys := attrsOfK(toks, i, end, terr)
			for _, k := range keys {
				if k != "class" && k != "model" {
					diag(ln, 1, `unknown object attribute "`+k+`"`, "warning")
				}
			}
			obj := &DslObject{ID: idTok.v, Label: label, Line: ln}
			if a["class"] != "" && a["class"] != "true" {
				obj.ClassName = a["class"]
			}
			if a["model"] != "" && a["model"] != "true" {
				obj.ModelID = a["model"]
			}
			if claim(*idTok) {
				doc.Objects = append(doc.Objects, obj)
			}
			if opens {
				c.frames = append(c.frames, &umlFrame{k: "object", obj: obj})
			}
			return
		}
		if t1 := tokAt(1); len(toks) >= 3 && t1 != nil && t1.k == "word" && t1.v == "--" {
			i := 3
			l := &DslObjLink{From: toks[0].v, To: toks[2].v, Line: ln}
			if t := tokAt(i); t != nil && t.k == "str" {
				l.Label = t.v
				i++
			}
			a, keys := attrsOfK(toks, i, len(toks), terr)
			for _, k := range keys {
				if k != "srcRole" && k != "dstRole" {
					diag(ln, 1, `unknown link attribute "`+k+`"`, "warning")
				}
			}
			l.SrcRole = a["srcRole"]
			l.DstRole = a["dstRole"]
			doc.ObjLinks = append(doc.ObjLinks, l)
			return
		}
		if comm {
			terr(first, `unknown statement "`+first.v+`" in communication (expected object, a link like a -- b, or a message like 1.1 a -> b "text" async)`)
		} else {
			terr(first, `unknown statement "`+first.v+`" in objects (expected object, or a link like a -- b "name")`)
		}
		return

	case "deployment":
		if depKinds[kw] {
			idTok := tokAt(1)
			if idTok == nil || idTok.k == "sym" {
				terr(first, kw+" needs an id, e.g. "+kw+` x "Name"`)
				return
			}
			i := 2
			label := idTok.v
			if t := tokAt(i); t != nil && t.k == "str" {
				label = t.v
				i++
			}
			a, keys := attrsOfK(toks, i, end, terr)
			for _, k := range keys {
				if k != "stereotype" && k != "icon" {
					diag(ln, 1, `unknown deployment attribute "`+k+`"`, "warning")
				}
			}
			if opens && !depContainers[kw] {
				terr(last, "only a node, device or executionEnvironment can hold other elements")
			}
			par := c.parentOf("dep")
			if claim(*idTok) {
				n := &DslDepNode{ID: idTok.v, Label: label, Kind: kw, Stereotype: a["stereotype"], Icon: a["icon"], Line: ln}
				if par != nil {
					n.Parent = par.id
				}
				doc.DepNodes = append(doc.DepNodes, n)
			}
			if opens {
				c.frames = append(c.frames, &umlFrame{k: "dep", id: idTok.v})
			}
			return
		}
		w := ""
		if t1 := tokAt(1); t1 != nil && t1.k == "word" {
			w = t1.v
		}
		if kind, ok := depLinkWords[w]; ok && len(toks) >= 3 {
			l := &DslDepLink{From: toks[0].v, To: toks[2].v, Kind: kind, Line: ln}
			if t3 := tokAt(3); t3 != nil && t3.k == "str" {
				l.Label = t3.v
			}
			doc.DepLinks = append(doc.DepLinks, l)
			return
		}
		terr(first, `unknown statement "`+first.v+`" in deployment (expected node, device, executionEnvironment, artifact, component, or a -- b "protocol" / a ..> b / a manifests b)`)
		return
	}

	// components
	if frame != nil && frame.k == "comp" && (kw == "provides" || kw == "requires") {
		var comp *DslComp
		for _, x := range doc.Comps {
			if x.ID == frame.id {
				comp = x
				break
			}
		}
		if comp == nil {
			return
		}
		var names []string
		for _, t := range toks[1:] {
			if !(t.k == "sym" && t.v == ",") {
				names = append(names, t.v)
			}
		}
		if len(names) == 0 {
			terr(first, kw+" needs interface names, e.g. "+kw+" IPayment")
			return
		}
		if kw == "provides" {
			comp.Provides = append(comp.Provides, names...)
		} else {
			comp.Requires = append(comp.Requires, names...)
		}
		return
	}
	if compKinds[kw] {
		idTok := tokAt(1)
		if idTok == nil || idTok.k == "sym" {
			terr(first, kw+" needs an id, e.g. "+kw+` api "API"`)
			return
		}
		i := 2
		label := idTok.v
		if t := tokAt(i); t != nil && t.k == "str" {
			label = t.v
			i++
		}
		a := attrsOf(toks, i, end, terr)
		par := c.parentOf("comp")
		if claim(*idTok) {
			cp := &DslComp{ID: idTok.v, Label: label, Kind: kw, Stereotype: a["stereotype"], ArchRef: archRefOf(a), Line: ln}
			if par != nil {
				cp.Parent = par.id
			}
			doc.Comps = append(doc.Comps, cp)
		}
		if opens {
			c.frames = append(c.frames, &umlFrame{k: "comp", id: idTok.v})
		}
		return
	}
	w := ""
	if t1 := tokAt(1); t1 != nil && t1.k == "word" {
		w = t1.v
	}
	if len(toks) >= 3 && (w == "..>" || w == "--") {
		l := &DslCompLink{From: toks[0].v, To: toks[2].v, Kind: "dependency", Line: ln}
		if w == "--" {
			l.Kind = "assembly"
		}
		if t3 := tokAt(3); t3 != nil && t3.k == "str" {
			l.Label = t3.v
		}
		doc.CompLinks = append(doc.CompLinks, l)
		return
	}
	terr(first, `unknown statement "`+first.v+`" in components (expected component, node, artifact, provides, requires, or a ..> b / a -- b)`)
}

func validateUml(doc *DslDoc, derr func(l, c int, m string)) {
	classNames := map[string]bool{}
	for _, c := range doc.Classes {
		classNames[c.Name] = true
	}
	var rels []*DslClassRel
	for _, r := range doc.ClassRels {
		ok := true
		for _, e := range []string{r.From, r.To} {
			if !classNames[e] {
				derr(r.Line, 1, `unknown class "`+e+`"`)
				ok = false
				break
			}
		}
		if ok {
			rels = append(rels, r)
		}
	}
	doc.ClassRels = rels
	check := func(from, to string, line int, known map[string]bool, what string) bool {
		for _, e := range []string{from, to} {
			if !known[e] {
				derr(line, 1, `unknown `+what+` "`+e+`"`)
				return false
			}
		}
		return true
	}
	sset := map[string]bool{}
	for _, s := range doc.States {
		sset[s.ID] = true
	}
	var ts []*DslTransition
	for _, t := range doc.Transitions {
		if check(t.From, t.To, t.Line, sset, "state") {
			ts = append(ts, t)
		}
	}
	doc.Transitions = ts
	uset := map[string]bool{}
	for _, u := range doc.UcItems {
		if u.Kind != "boundary" {
			uset[u.ID] = true
		}
	}
	var us []*DslUcRel
	for _, r := range doc.UcRels {
		if check(r.From, r.To, r.Line, uset, "actor or use case") {
			us = append(us, r)
		}
	}
	doc.UcRels = us
	cset := map[string]bool{}
	for _, c := range doc.Comps {
		cset[c.ID] = true
	}
	var cs []*DslCompLink
	for _, l := range doc.CompLinks {
		if check(l.From, l.To, l.Line, cset, "component") {
			cs = append(cs, l)
		}
	}
	doc.CompLinks = cs
	aset := map[string]bool{}
	for _, n := range doc.ActNodes {
		aset[n.ID] = true
	}
	var fs []*DslActFlow
	for _, f := range doc.ActFlows {
		if check(f.From, f.To, f.Line, aset, "activity element") {
			fs = append(fs, f)
		}
	}
	doc.ActFlows = fs
	dset := map[string]bool{}
	for _, n := range doc.DepNodes {
		dset[n.ID] = true
	}
	var ds []*DslDepLink
	for _, l := range doc.DepLinks {
		if check(l.From, l.To, l.Line, dset, "deployment element") {
			ds = append(ds, l)
		}
	}
	doc.DepLinks = ds
	pset := map[string]bool{}
	for _, p := range doc.Packages {
		pset[p.ID] = true
	}
	var ps []*DslPkgRel
	for _, r := range doc.PkgRels {
		if check(r.From, r.To, r.Line, pset, "package") {
			ps = append(ps, r)
		}
	}
	doc.PkgRels = ps
	oset := map[string]bool{}
	for _, o := range doc.Objects {
		oset[o.ID] = true
	}
	var os []*DslObjLink
	for _, l := range doc.ObjLinks {
		if check(l.From, l.To, l.Line, oset, "object") {
			os = append(os, l)
		}
	}
	doc.ObjLinks = os
	// a message needs the link between its two objects: declare it when the text does not
	var cm []*DslCommMsg
	for _, m := range doc.CommMsgs {
		if check(m.From, m.To, m.Line, oset, "object") {
			cm = append(cm, m)
		}
	}
	doc.CommMsgs = cm
	for _, m := range doc.CommMsgs {
		found := false
		for _, l := range doc.ObjLinks {
			if (l.From == m.From && l.To == m.To) || (l.From == m.To && l.To == m.From) {
				found = true
				break
			}
		}
		if !found {
			doc.ObjLinks = append(doc.ObjLinks, &DslObjLink{From: m.From, To: m.To, Line: m.Line})
		}
	}
}

func describeFrame(c *umlCtx) string {
	if len(c.frames) == 0 {
		return c.block + " block"
	}
	f := c.frames[len(c.frames)-1]
	switch f.k {
	case "class":
		return `class "` + f.cls.Name + `"`
	case "ucase":
		return `usecase "` + f.item.ID + `"`
	case "object":
		return `object "` + f.obj.ID + `"`
	}
	return f.k + ` "` + f.id + `"`
}
