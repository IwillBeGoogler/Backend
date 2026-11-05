package application

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram/core"
)

// Editing a diagram page as plain JSON, for the MCP tools: add nodes and
// edges, change a node, delete elements. It is the TypeScript core's
// graph.ts. A page is a decoded JSON object (numbers kept as json.Number);
// fields this code does not know stay untouched. An icon is a catalog id,
// name or alias (core.ResolveIcon); layout, lint, export and the text
// format live in the core package.

// Obj is a JSON object.
type Obj = map[string]any

// NodeSpec is a node to add.
type NodeSpec struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	Icon     string   `json:"icon"`
	Type     string   `json:"type"`
	Subtitle string   `json:"subtitle"`
	X        *float64 `json:"x"`
	Y        *float64 `json:"y"`
	Width    *float64 `json:"width"`
	Height   *float64 `json:"height"`
	Parent   string   `json:"parent"`
	Data     Obj      `json:"data"`
}

// EdgeSpec is an edge to add.
type EdgeSpec struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Target string `json:"target"`
	Label  string `json:"label"`
	Preset string `json:"preset"`
	Dashed bool   `json:"dashed"`
	Data   Obj    `json:"data"`
}

// NodePatch changes a node; nil fields stay.
type NodePatch struct {
	Label    *string
	Subtitle *string
	Icon     *string
	X, Y     *float64
	Width    *float64
	Height   *float64
	Data     Obj
}

// ItemError is one rejected item of a batch.
type ItemError struct {
	Index   int    `json:"index"`
	Message string `json:"message"`
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// SlugID makes an id from a label: lower-case words joined by dashes.
func SlugID(s, fallback string) string {
	id := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(id) > 48 {
		id = id[:48]
	}
	if id == "" {
		return fallback
	}
	return id
}

// UniqueID is base, base-2, base-3, ... the first not taken.
func UniqueID(base string, taken map[string]bool) string {
	if !taken[base] {
		return base
	}
	for i := 2; ; i++ {
		if id := fmt.Sprintf("%s-%d", base, i); !taken[id] {
			return id
		}
	}
}

const (
	autoGap  = 60
	autoCols = 5
	defW     = 120
	defH     = 96
)

func num(v any) (float64, bool) {
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case float64:
		return n, true
	case int:
		return float64(n), true
	}
	return 0, false
}

func str(o Obj, k string) string {
	s, _ := o[k].(string)
	return s
}

func objs(v any) []Obj {
	list, _ := v.([]any)
	out := make([]Obj, 0, len(list))
	for _, e := range list {
		if o, ok := e.(Obj); ok {
			out = append(out, o)
		}
	}
	return out
}

func asAny(os []Obj) []any {
	out := make([]any, len(os))
	for i, o := range os {
		out[i] = o
	}
	return out
}

// Pages of a document.
func Pages(doc Obj) []Obj { return objs(doc["pages"]) }

// Nodes and Edges of a page.
func Nodes(page Obj) []Obj { return objs(page["nodes"]) }
func Edges(page Obj) []Obj { return objs(page["edges"]) }

func nodeSize(n Obj) (w, h float64) {
	w, h = defW, defH
	if v, ok := num(n["width"]); ok {
		w = v
	} else if m, ok := n["measured"].(Obj); ok {
		if v, ok := num(m["width"]); ok {
			w = v
		}
	}
	if v, ok := num(n["height"]); ok {
		h = v
	} else if m, ok := n["measured"].(Obj); ok {
		if v, ok := num(m["height"]); ok {
			h = v
		}
	}
	return
}

func position(n Obj) (x, y float64) {
	if p, ok := n["position"].(Obj); ok {
		x, _ = num(p["x"])
		y, _ = num(p["y"])
	}
	return
}

// autoOrigin is where the next auto-placed top-level node goes: a row under
// everything, five per row, deterministic.
func autoOrigin(page Obj) (x, y float64) {
	first := true
	var minX, maxY float64
	for _, n := range Nodes(page) {
		if _, child := n["parentId"]; child {
			continue
		}
		px, py := position(n)
		_, h := nodeSize(n)
		if first || px < minX {
			minX = px
		}
		if first || py+h > maxY {
			maxY = py + h
		}
		first = false
	}
	if first {
		return 0, 0
	}
	return minX, maxY + autoGap
}

// AddNodes adds nodes to a page. Bad items are reported by index and the
// rest are added.
func AddNodes(page Obj, specs []NodeSpec) (added []Obj, errs []ItemError) {
	nodes := Nodes(page)
	taken := map[string]bool{}
	byID := map[string]Obj{}
	for _, n := range nodes {
		taken[str(n, "id")] = true
		byID[str(n, "id")] = n
	}
	ox, oy := autoOrigin(page)
	placed, auto := 0, 0
	for i, s := range specs {
		label := strings.TrimSpace(s.Label)
		if label == "" {
			errs = append(errs, ItemError{i, "label is required"})
			continue
		}
		if s.ID != "" && taken[s.ID] {
			errs = append(errs, ItemError{i, fmt.Sprintf("node id %q already exists", s.ID)})
			continue
		}
		if s.Parent != "" {
			if p, ok := byID[s.Parent]; !ok || (str(p, "type") != "group" && str(p, "type") != "cluster") {
				errs = append(errs, ItemError{i, fmt.Sprintf("parent %q is not a group on this page", s.Parent)})
				continue
			}
		}
		typ := s.Type
		if typ == "" {
			typ = "icon"
		}
		data := Obj{}
		for k, v := range s.Data {
			data[k] = v
		}
		data["label"] = label
		if s.Subtitle != "" {
			data["subtitle"] = s.Subtitle
		}
		if s.Icon != "" {
			hit, ok := core.ResolveIcon(s.Icon)
			switch {
			case !ok:
				errs = append(errs, ItemError{i, fmt.Sprintf("unknown icon %q (use search_catalog)", s.Icon)})
				continue
			case hit.Kind == "cluster":
				errs = append(errs, ItemError{i, fmt.Sprintf("%q is a cluster profile; add its member icons instead", s.Icon)})
				continue
			case hit.Kind == "group":
				typ = "group"
				data["groupIcon"] = hit.ID
			default:
				data["iconId"] = hit.ID
			}
		}
		id := s.ID
		if id == "" {
			id = UniqueID(SlugID(label, "node"), taken)
		}
		taken[id] = true
		var px, py float64
		switch {
		case s.X != nil && s.Y != nil:
			px, py = *s.X, *s.Y
		case s.Parent != "":
			px, py = float64(24+(placed%3)*150), float64(60+(placed/3)*120)
		default:
			px = ox + float64(auto%autoCols)*(defW+autoGap)
			py = oy + float64(auto/autoCols)*(defH+autoGap)
			auto++
		}
		node := Obj{"id": id, "type": typ, "position": Obj{"x": px, "y": py}, "data": data}
		w, h := s.Width, s.Height
		if w == nil && typ == "group" {
			v := 360.0
			w = &v
		}
		if h == nil && typ == "group" {
			v := 240.0
			h = &v
		}
		if w != nil {
			node["width"] = *w
		}
		if h != nil {
			node["height"] = *h
		}
		if s.Parent != "" {
			node["parentId"] = s.Parent
		}
		placed++
		nodes = append(nodes, node)
		byID[id] = node
		added = append(added, node)
	}
	page["nodes"] = asAny(nodes)
	return added, errs
}

// AddEdges connects existing nodes.
func AddEdges(page Obj, specs []EdgeSpec) (added []Obj, errs []ItemError) {
	ids := map[string]bool{}
	for _, n := range Nodes(page) {
		ids[str(n, "id")] = true
	}
	edges := Edges(page)
	taken := map[string]bool{}
	for _, e := range edges {
		taken[str(e, "id")] = true
	}
	for i, s := range specs {
		if bad := firstMissing(ids, s.Source, s.Target); bad != "" {
			errs = append(errs, ItemError{i, fmt.Sprintf("node %q not found", bad)})
			continue
		}
		if s.ID != "" && taken[s.ID] {
			errs = append(errs, ItemError{i, fmt.Sprintf("edge id %q already exists", s.ID)})
			continue
		}
		id := s.ID
		if id == "" {
			id = UniqueID("e-"+s.Source+"-"+s.Target, taken)
		}
		taken[id] = true
		data := Obj{}
		for k, v := range s.Data {
			data[k] = v
		}
		if s.Preset != "" {
			data["preset"] = s.Preset
		}
		if s.Dashed {
			data["dashed"] = true
		}
		edge := Obj{"id": id, "source": s.Source, "target": s.Target}
		if s.Label != "" {
			edge["label"] = s.Label
		}
		if len(data) > 0 {
			edge["data"] = data
		}
		edges = append(edges, edge)
		added = append(added, edge)
	}
	page["edges"] = asAny(edges)
	return added, errs
}

func firstMissing(ids map[string]bool, ends ...string) string {
	for _, e := range ends {
		if !ids[e] {
			return e
		}
	}
	return ""
}

// UpdateNode changes a node and returns it.
func UpdateNode(page Obj, id string, p NodePatch) (Obj, error) {
	var n Obj
	for _, c := range Nodes(page) {
		if str(c, "id") == id {
			n = c
		}
	}
	if n == nil {
		return nil, fmt.Errorf("node %q not found", id)
	}
	data, _ := n["data"].(Obj)
	if data == nil {
		data = Obj{}
		n["data"] = data
	}
	if p.Icon != nil {
		hit, ok := core.ResolveIcon(*p.Icon)
		if !ok || hit.Kind == "cluster" {
			return nil, fmt.Errorf("unknown icon %q", *p.Icon)
		}
		if hit.Kind == "group" {
			data["groupIcon"] = hit.ID
		} else {
			data["iconId"] = hit.ID
		}
	}
	for k, v := range p.Data {
		data[k] = v
	}
	if p.Label != nil {
		data["label"] = *p.Label
	}
	if p.Subtitle != nil {
		data["subtitle"] = *p.Subtitle
	}
	if p.X != nil || p.Y != nil {
		x, y := position(n)
		if p.X != nil {
			x = *p.X
		}
		if p.Y != nil {
			y = *p.Y
		}
		n["position"] = Obj{"x": x, "y": y}
	}
	if p.Width != nil {
		n["width"] = *p.Width
	}
	if p.Height != nil {
		n["height"] = *p.Height
	}
	return n, nil
}

// Deleted counts what DeleteElements removed.
type Deleted struct {
	Nodes int `json:"nodes"`
	Edges int `json:"edges"`
}

// DeleteElements deletes nodes (with their descendants and every attached
// edge) and edges by id.
func DeleteElements(page Obj, ids []string) Deleted {
	nodes, edges := Nodes(page), Edges(page)
	nodeIDs, edgeIDs := map[string]bool{}, map[string]bool{}
	for _, n := range nodes {
		nodeIDs[str(n, "id")] = true
	}
	for _, e := range edges {
		edgeIDs[str(e, "id")] = true
	}
	kill, edgeKill := map[string]bool{}, map[string]bool{}
	for _, id := range ids {
		if nodeIDs[id] {
			kill[id] = true
		}
		if edgeIDs[id] {
			edgeKill[id] = true
		}
	}
	for grew := true; grew; {
		grew = false
		for _, n := range nodes {
			if p := str(n, "parentId"); p != "" && kill[p] && !kill[str(n, "id")] {
				kill[str(n, "id")], grew = true, true
			}
		}
	}
	keptN := make([]Obj, 0, len(nodes))
	for _, n := range nodes {
		if !kill[str(n, "id")] {
			keptN = append(keptN, n)
		}
	}
	keptE := make([]Obj, 0, len(edges))
	for _, e := range edges {
		if !edgeKill[str(e, "id")] && !kill[str(e, "source")] && !kill[str(e, "target")] {
			keptE = append(keptE, e)
		}
	}
	out := Deleted{Nodes: len(nodes) - len(keptN), Edges: len(edges) - len(keptE)}
	page["nodes"], page["edges"] = asAny(keptN), asAny(keptE)
	return out
}

// PickPage finds a page by id, name or zero-based index (nil: the first).
func PickPage(doc Obj, ref any) (Obj, error) {
	pages := objs(doc["pages"])
	switch r := ref.(type) {
	case nil:
		if len(pages) > 0 {
			return pages[0], nil
		}
	case string:
		for _, p := range pages {
			if str(p, "id") == r {
				return p, nil
			}
		}
		for _, p := range pages {
			if str(p, "name") == r {
				return p, nil
			}
		}
	default:
		if f, ok := num(r); ok && f >= 0 && f == math.Trunc(f) && int(f) < len(pages) {
			return pages[int(f)], nil
		}
	}
	return nil, fmt.Errorf("page %s not found", describe(ref))
}

func describe(ref any) string {
	b, _ := json.Marshal(ref)
	return string(b)
}

// DecodeDoc decodes a document keeping numbers exact.
func DecodeDoc(raw json.RawMessage) (Obj, error) {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	var doc Obj
	if err := dec.Decode(&doc); err != nil || doc == nil {
		return nil, fmt.Errorf("the document is not an object")
	}
	return doc, nil
}
