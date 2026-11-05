package core

import (
	"math"
	"sort"
)

// Layouts are the algorithms of ApplyLayout (core/layout.ts).
var Layouts = []string{"layered", "orthogonal", "radial", "tree", "force", "grid"}

// LayoutOptions are the options of ApplyLayout.
type LayoutOptions struct {
	Direction string   // LR (default) or TB
	Gap       *float64 // default 60
	Seed      *float64 // force layout PRNG seed, default 1
	Only      []string // tidy selection: only these node ids move
	RootID    string   // tree / radial root
}

type box struct {
	id   string
	w, h float64
}

type pt struct{ x, y float64 }

type pair [2]string

const snapGrid = 20

var groupPad = struct{ left, top, right, bottom float64 }{24, 56, 24, 24}

func mulberry32(seed float64) func() float64 {
	a := toUint32(seed)
	return func() float64 {
		a += 0x6d2b79f5
		t := a
		t = (t ^ (t >> 15)) * (t | 1)
		t ^= t + (t^(t>>7))*(t|61)
		return float64(t^(t>>14)) / 4294967296
	}
}

// toUint32 is JS ToUint32.
func toUint32(f float64) uint32 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return uint32(int64(math.Mod(math.Trunc(f), 4294967296)))
}

// hypot2 is V8's Math.hypot for two arguments (scaled, compensated sum).
func hypot2(a, b float64) float64 {
	if math.IsInf(a, 0) || math.IsInf(b, 0) {
		return math.Inf(1)
	}
	a, b = math.Abs(a), math.Abs(b)
	m := math.Max(a, b)
	if m == 0 {
		return 0
	}
	sum, comp := 0.0, 0.0
	for _, v := range [2]float64{a, b} {
		n := v / m
		summand := float64(n*n) - comp
		prelim := sum + summand
		comp = (prelim - sum) - summand
		sum = prelim
	}
	return math.Sqrt(sum) * m
}

// AbsRects are the absolute rectangles of every node (parents resolved).
func AbsRects(page Obj) map[string]rect {
	byID := map[string]Obj{}
	for _, n := range nodesOf(page) {
		byID[str(n["id"])] = n
	}
	var abs func(n Obj, seen map[string]bool) pt
	abs = func(n Obj, seen map[string]bool) pt {
		x, y := posOf(n)
		p, ok := byID[str(n["parentId"])]
		if !ok || seen[str(n["id"])] {
			return pt{x, y}
		}
		seen[str(n["id"])] = true
		pp := abs(p, seen)
		return pt{pp.x + x, pp.y + y}
	}
	out := map[string]rect{}
	for _, n := range nodesOf(page) {
		a := abs(n, map[string]bool{})
		w, h := nodeSize(n)
		out[str(n["id"])] = rect{a.x, a.y, w, h}
	}
	return out
}

type rect struct{ x, y, w, h float64 }

// ---------- graph helpers ----------

type adj struct {
	keys     []string
	out, inn map[string][]string
}

func adjacency(boxes []box, edges []pair) adj {
	a := adj{out: map[string][]string{}, inn: map[string][]string{}}
	ids := map[string]bool{}
	for _, b := range boxes {
		ids[b.id] = true
		a.keys = append(a.keys, b.id)
		a.out[b.id] = []string{}
		a.inn[b.id] = []string{}
	}
	seen := map[pair]bool{}
	for _, e := range edges {
		if e[0] == e[1] || !ids[e[0]] || !ids[e[1]] || seen[e] {
			continue
		}
		seen[e] = true
		a.out[e[0]] = append(a.out[e[0]], e[1])
		a.inn[e[1]] = append(a.inn[e[1]], e[0])
	}
	return a
}

// acyclic reverses DFS back edges; deterministic (input order).
func acyclic(boxes []box, out map[string][]string) map[string][]string {
	state := map[string]int{}
	dag := map[string][]string{}
	for _, b := range boxes {
		dag[b.id] = []string{}
	}
	var visit func(id string)
	visit = func(id string) {
		state[id] = 1
		for _, t := range out[id] {
			s := state[t]
			if s == 1 {
				dag[t] = append(dag[t], id)
			} else {
				dag[id] = append(dag[id], t)
				if s == 0 {
					visit(t)
				}
			}
		}
		state[id] = 2
	}
	for _, b := range boxes {
		if state[b.id] == 0 {
			visit(b.id)
		}
	}
	return dag
}

func maxF(init float64, vs ...float64) float64 {
	m := init
	for _, v := range vs {
		if v > m {
			m = v
		}
	}
	return m
}

// ---------- algorithms (top-left positions, un-anchored) ----------

func layered(boxes []box, edges []pair, lr bool, gap float64) map[string]pt {
	a := adjacency(boxes, edges)
	dag := acyclic(boxes, a.out)
	preds := map[string][]string{}
	for _, b := range boxes {
		preds[b.id] = []string{}
	}
	for _, k := range a.keys {
		for _, t := range dag[k] {
			preds[t] = append(preds[t], k)
		}
	}
	layer := map[string]int{}
	var rank func(id string) int
	rank = func(id string) int {
		if l, ok := layer[id]; ok {
			return l
		}
		layer[id] = 0
		l := 0
		if len(preds[id]) > 0 {
			m := -1
			for _, p := range preds[id] {
				if r := rank(p); r > m {
					m = r
				}
			}
			l = m + 1
		}
		layer[id] = l
		return l
	}
	for _, b := range boxes {
		rank(b.id)
	}
	nLayers := 1
	for _, l := range layer {
		if l+1 > nLayers {
			nLayers = l + 1
		}
	}
	layers := make([][]string, nLayers)
	for _, b := range boxes {
		layers[layer[b.id]] = append(layers[layer[b.id]], b.id)
	}
	pos := map[string]int{}
	reindex := func() {
		for _, l := range layers {
			for i, id := range l {
				pos[id] = i
			}
		}
	}
	reindex()
	bary := func(id string, nb map[string][]string) float64 {
		ns := nb[id]
		if len(ns) == 0 {
			return float64(pos[id])
		}
		s := 0.0
		for _, n := range ns {
			s += float64(pos[n])
		}
		return s / float64(len(ns))
	}
	for sweep := 0; sweep < 4; sweep++ {
		down := sweep%2 == 0
		for k := 0; k < nLayers; k++ {
			li := k
			if !down {
				li = nLayers - 1 - k
			}
			nb := dag
			if down {
				nb = preds
			}
			key := map[string]float64{}
			for _, id := range layers[li] {
				key[id] = bary(id, nb)
			}
			l := layers[li]
			sort.SliceStable(l, func(i, j int) bool {
				if d := key[l[i]] - key[l[j]]; d != 0 {
					return d < 0
				}
				return pos[l[i]] < pos[l[j]]
			})
			reindex()
		}
	}
	byID := map[string]box{}
	for _, b := range boxes {
		byID[b.id] = b
	}
	along := func(b box) float64 {
		if lr {
			return b.w
		}
		return b.h
	}
	across := func(b box) float64 {
		if lr {
			return b.h
		}
		return b.w
	}
	extents := make([]float64, nLayers)
	widest := 0.0
	for i, l := range layers {
		s := 0.0
		for _, id := range l {
			s += across(byID[id])
		}
		extents[i] = s + float64(gap*float64(max(0, len(l)-1)))
		widest = math.Max(widest, extents[i])
	}
	res := map[string]pt{}
	flow := 0.0
	for li, l := range layers {
		depth := 0.0
		for _, id := range l {
			depth = math.Max(depth, along(byID[id]))
		}
		cross := (widest - extents[li]) / 2
		for _, id := range l {
			b := byID[id]
			av := flow + (depth-along(b))/2
			if lr {
				res[id] = pt{av, cross}
			} else {
				res[id] = pt{cross, av}
			}
			cross += across(b) + gap
		}
		flow += depth + float64(gap*1.6)
	}
	return res
}

func gridLayout(boxes []box, gap float64) map[string]pt {
	n := len(boxes)
	cols := max(1, int(math.Ceil(math.Sqrt(float64(n)))))
	rows := (n + cols - 1) / cols
	colW := make([]float64, cols)
	rowH := make([]float64, rows)
	for i, b := range boxes {
		colW[i%cols] = math.Max(colW[i%cols], b.w)
		rowH[i/cols] = math.Max(rowH[i/cols], b.h)
	}
	xs, ys := make([]float64, cols), make([]float64, rows)
	x := 0.0
	for c, w := range colW {
		xs[c] = x
		x += w + gap
	}
	y := 0.0
	for r, h := range rowH {
		ys[r] = y
		y += h + gap
	}
	res := map[string]pt{}
	for i, b := range boxes {
		res[b.id] = pt{xs[i%cols], ys[i/cols]}
	}
	return res
}

type treeNode struct {
	id    string
	kids  []*treeNode
	depth int
}

func spanningForest(boxes []box, edges []pair, rootID string) []*treeNode {
	a := adjacency(boxes, edges)
	seen := map[string]bool{}
	var roots []string
	has := false
	for _, b := range boxes {
		if b.id == rootID {
			has = true
		}
	}
	if rootID != "" && has {
		roots = append(roots, rootID)
	}
	for _, b := range boxes {
		if len(a.inn[b.id]) == 0 && b.id != rootID {
			roots = append(roots, b.id)
		}
	}
	for _, b := range boxes {
		roots = append(roots, b.id)
	}
	var build func(id string, depth int) *treeNode
	build = func(id string, depth int) *treeNode {
		seen[id] = true
		t := &treeNode{id: id, depth: depth}
		for _, k := range a.out[id] {
			if !seen[k] {
				t.kids = append(t.kids, build(k, depth+1))
			}
		}
		return t
	}
	var trees []*treeNode
	for _, r := range roots {
		if !seen[r] {
			trees = append(trees, build(r, 0))
		}
	}
	return trees
}

func treeLayout(boxes []box, edges []pair, lr bool, gap float64, rootID string) map[string]pt {
	byID := map[string]box{}
	for _, b := range boxes {
		byID[b.id] = b
	}
	across := func(id string) float64 {
		if lr {
			return byID[id].h
		}
		return byID[id].w
	}
	along := func(id string) float64 {
		if lr {
			return byID[id].w
		}
		return byID[id].h
	}
	forest := spanningForest(boxes, edges, rootID)
	crossPos := map[string]float64{}
	var depthMax []float64
	cursor := 0.0
	var walk func(t *treeNode) float64
	walk = func(t *treeNode) float64 {
		for len(depthMax) <= t.depth {
			depthMax = append(depthMax, 0)
		}
		depthMax[t.depth] = math.Max(depthMax[t.depth], along(t.id))
		var c float64
		if len(t.kids) == 0 {
			c = cursor + across(t.id)/2
			cursor += across(t.id) + gap
		} else {
			var cs []float64
			for _, k := range t.kids {
				cs = append(cs, walk(k))
			}
			c = (cs[0] + cs[len(cs)-1]) / 2
			cursor = math.Max(cursor, c+across(t.id)/2+gap)
		}
		crossPos[t.id] = c
		return c
	}
	for _, t := range forest {
		walk(t)
	}
	flowAt := make([]float64, len(depthMax))
	f := 0.0
	for i, d := range depthMax {
		flowAt[i] = f
		f += d + float64(gap*1.6)
	}
	res := map[string]pt{}
	var place func(t *treeNode)
	place = func(t *treeNode) {
		a := flowAt[t.depth] + (depthMax[t.depth]-along(t.id))/2
		c := crossPos[t.id] - across(t.id)/2
		if lr {
			res[t.id] = pt{a, c}
		} else {
			res[t.id] = pt{c, a}
		}
		for _, k := range t.kids {
			place(k)
		}
	}
	for _, t := range forest {
		place(t)
	}
	return res
}

const virtualRoot = "\x00"

func radial(boxes []box, edges []pair, gap float64, rootID string) map[string]pt {
	byID := map[string]box{}
	for _, b := range boxes {
		byID[b.id] = b
	}
	trees := spanningForest(boxes, edges, rootID)
	var root *treeNode
	if len(trees) == 1 {
		root = trees[0]
	} else {
		var bump func(n *treeNode)
		bump = func(n *treeNode) {
			n.depth++
			for _, k := range n.kids {
				bump(k)
			}
		}
		for _, t := range trees {
			bump(t)
		}
		root = &treeNode{id: virtualRoot, kids: trees}
	}
	leaves := map[string]int{}
	var count func(t *treeNode) int
	count = func(t *treeNode) int {
		if len(t.kids) == 0 {
			leaves[t.id] = 1
		} else {
			s := 0
			for _, k := range t.kids {
				s += count(k)
			}
			leaves[t.id] = s
		}
		return leaves[t.id]
	}
	count(root)
	maxDim := 0.0
	for _, b := range boxes {
		maxDim = math.Max(maxDim, hypot2(b.w, b.h))
	}
	centers := map[string]pt{}
	var order []string
	placeWith := func(step float64) {
		var rec func(t *treeNode, a0, a1 float64)
		rec = func(t *treeNode, a0, a1 float64) {
			mid := (a0 + a1) / 2
			if t.id != virtualRoot {
				if _, ok := centers[t.id]; !ok {
					order = append(order, t.id)
				}
				if t.depth == 0 && len(trees) == 1 {
					centers[t.id] = pt{0, 0}
				} else {
					d := float64(t.depth)
					centers[t.id] = pt{jsCos(mid) * d * step, jsSin(mid) * d * step}
				}
			}
			a := a0
			for _, k := range t.kids {
				span := ((a1 - a0) * float64(leaves[k.id])) / float64(leaves[t.id])
				rec(k, a, a+span)
				a += span
			}
		}
		pi := math.Pi
		rec(root, -pi/2, float64(3*pi)/2)
	}
	clash := func() bool {
		for i := 0; i < len(order); i++ {
			for j := i + 1; j < len(order); j++ {
				a, b := centers[order[i]], centers[order[j]]
				A, B := byID[order[i]], byID[order[j]]
				if math.Abs(a.x-b.x) < (A.w+B.w)/2+gap/2 && math.Abs(a.y-b.y) < (A.h+B.h)/2+gap/2 {
					return true
				}
			}
		}
		return false
	}
	step := maxDim + gap
	placeWith(step)
	for i := 0; i < 60 && clash(); i++ {
		step *= 1.15
		placeWith(step)
	}
	res := map[string]pt{}
	for _, b := range boxes {
		c := centers[b.id]
		res[b.id] = pt{c.x - b.w/2, c.y - b.h/2}
	}
	return res
}

func resolveOverlaps(boxes []box, pos map[string]*pt, gap float64) {
	for iter := 0; iter < 200; iter++ {
		moved := false
		for i := 0; i < len(boxes); i++ {
			for j := i + 1; j < len(boxes); j++ {
				a, b := boxes[i], boxes[j]
				pa, pb := pos[a.id], pos[b.id]
				dx := pb.x + b.w/2 - (pa.x + a.w/2)
				dy := pb.y + b.h/2 - (pa.y + a.h/2)
				ox := (a.w+b.w)/2 + gap/2 - math.Abs(dx)
				oy := (a.h+b.h)/2 + gap/2 - math.Abs(dy)
				if ox <= 0 || oy <= 0 {
					continue
				}
				moved = true
				if ox < oy {
					s := ox/2 + 0.5
					if dx < 0 {
						s = -s
					}
					pa.x -= s
					pb.x += s
				} else {
					s := oy/2 + 0.5
					if dy < 0 {
						s = -s
					}
					pa.y -= s
					pb.y += s
				}
			}
		}
		if !moved {
			return
		}
	}
}

func ptrs(m map[string]pt) map[string]*pt {
	out := make(map[string]*pt, len(m))
	for k, v := range m {
		v := v
		out[k] = &v
	}
	return out
}

func unptrs(m map[string]*pt) map[string]pt {
	out := make(map[string]pt, len(m))
	for k, v := range m {
		out[k] = *v
	}
	return out
}

func force(boxes []box, edges []pair, gap, seed float64) map[string]pt {
	n := len(boxes)
	rnd := mulberry32(seed)
	avg := 0.0
	for _, b := range boxes {
		avg += math.Max(b.w, b.h)
	}
	avg /= float64(max(1, n))
	k := avg + gap
	pos := map[string]*pt{}
	for i, b := range boxes {
		ang := float64(i) / float64(max(1, n)) * math.Pi * 2
		r := k * math.Sqrt(float64(n)) * 0.5
		x := float64(jsCos(ang)*r) + float64((rnd()-0.5)*k*0.2)
		y := float64(jsSin(ang)*r) + float64((rnd()-0.5)*k*0.2)
		pos[b.id] = &pt{x, y}
	}
	a := adjacency(boxes, edges)
	var links []pair
	for _, key := range a.keys {
		for _, t := range a.out[key] {
			links = append(links, pair{key, t})
		}
	}
	temp := k * 2
	for it := 0; it < 250; it++ {
		disp := map[string]*pt{}
		for _, b := range boxes {
			disp[b.id] = &pt{}
		}
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				pa, pb := pos[boxes[i].id], pos[boxes[j].id]
				dx, dy := pa.x-pb.x, pa.y-pb.y
				d := hypot2(dx, dy)
				if d < 0.01 {
					dx = rnd() - 0.5
					dy = rnd() - 0.5
					d = hypot2(dx, dy)
					if d == 0 {
						d = 1
					}
				}
				f := (k * k) / d
				da, db := disp[boxes[i].id], disp[boxes[j].id]
				da.x += float64((dx / d) * f)
				da.y += float64((dy / d) * f)
				db.x -= float64((dx / d) * f)
				db.y -= float64((dy / d) * f)
			}
		}
		for _, l := range links {
			pa, pb := pos[l[0]], pos[l[1]]
			dx, dy := pa.x-pb.x, pa.y-pb.y
			d := hypot2(dx, dy)
			if d == 0 {
				d = 1
			}
			f := (d * d) / k
			ds, dt := disp[l[0]], disp[l[1]]
			ds.x -= float64((dx / d) * f)
			ds.y -= float64((dy / d) * f)
			dt.x += float64((dx / d) * f)
			dt.y += float64((dy / d) * f)
		}
		for _, b := range boxes {
			d := disp[b.id]
			l := hypot2(d.x, d.y)
			if l == 0 {
				l = 1
			}
			p := pos[b.id]
			p.x += float64((d.x / l) * math.Min(l, temp))
			p.y += float64((d.y / l) * math.Min(l, temp))
		}
		temp *= 0.97
	}
	res := map[string]*pt{}
	for _, b := range boxes {
		p := pos[b.id]
		res[b.id] = &pt{p.x - b.w/2, p.y - b.h/2}
	}
	resolveOverlaps(boxes, res, gap)
	return unptrs(res)
}

func placeBoxes(algo string, boxes []box, edges []pair, o LayoutOptions) map[string]pt {
	gap := 60.0
	if o.Gap != nil {
		gap = *o.Gap
	}
	lr := o.Direction != "TB"
	if len(boxes) == 0 {
		return map[string]pt{}
	}
	switch algo {
	case "grid":
		return gridLayout(boxes, gap)
	case "tree":
		return treeLayout(boxes, edges, lr, gap, o.RootID)
	case "radial":
		r := ptrs(radial(boxes, edges, gap, o.RootID))
		resolveOverlaps(boxes, r, gap)
		return unptrs(r)
	case "force":
		seed := 1.0
		if o.Seed != nil {
			seed = *o.Seed
		}
		return force(boxes, edges, gap, seed)
	case "orthogonal":
		r := layered(boxes, edges, lr, math.Max(gap, 3*snapGrid))
		for k, p := range r {
			r[k] = pt{jsRound(p.x/snapGrid) * snapGrid, jsRound(p.y/snapGrid) * snapGrid}
		}
		return r
	}
	return layered(boxes, edges, lr, gap)
}

// ApplyLayout lays page out in place and returns how many nodes moved.
func ApplyLayout(page Obj, algo string, o LayoutOptions) int {
	nodes := nodesOf(page)
	byID := map[string]Obj{}
	for _, n := range nodes {
		byID[str(n["id"])] = n
	}
	moved := map[string]bool{}
	childrenOf := func(pid string) []Obj {
		var out []Obj
		for _, n := range nodes {
			if str(n["parentId"]) == pid {
				out = append(out, n)
			}
		}
		return out
	}
	ancestorIn := func(id string, scope map[string]bool) string {
		cur := id
		for i := 0; cur != "" && i < 64; i++ {
			if scope[cur] {
				return cur
			}
			cur = str(byID[cur]["parentId"])
		}
		return ""
	}
	run := func(members []Obj, anchor *pt, padLeft, padTop float64) {
		if len(members) == 0 {
			return
		}
		scope := map[string]bool{}
		boxes := make([]box, len(members))
		for i, m := range members {
			scope[str(m["id"])] = true
			w, h := nodeSize(m)
			boxes[i] = box{str(m["id"]), w, h}
		}
		var edges []pair
		for _, e := range edgesOf(page) {
			a, b := ancestorIn(str(e["source"]), scope), ancestorIn(str(e["target"]), scope)
			if a != "" && b != "" && a != b {
				edges = append(edges, pair{a, b})
			}
		}
		pos := placeBoxes(algo, boxes, edges, o)
		minX, minY := math.Inf(1), math.Inf(1)
		for _, b := range boxes {
			minX, minY = math.Min(minX, pos[b.id].x), math.Min(minY, pos[b.id].y)
		}
		origin := pt{padLeft, padTop}
		if anchor != nil {
			origin = *anchor
		}
		for _, m := range members {
			p := pos[str(m["id"])]
			nx, ny := round2(origin.x+p.x-minX), round2(origin.y+p.y-minY)
			if ox, oy := posOf(m); ox != nx || oy != ny {
				moved[str(m["id"])] = true
			}
			setPos(m, nx, ny)
		}
	}

	if len(o.Only) > 0 {
		var first Obj
		for _, id := range o.Only {
			if n, ok := byID[id]; ok {
				first = n
				break
			}
		}
		if first == nil {
			return 0
		}
		var sel []Obj
		for _, id := range o.Only {
			if n, ok := byID[id]; ok && str(n["parentId"]) == str(first["parentId"]) {
				sel = append(sel, n)
			}
		}
		ax, ay := math.Inf(1), math.Inf(1)
		for _, n := range sel {
			x, y := posOf(n)
			ax, ay = math.Min(ax, x), math.Min(ay, y)
		}
		run(sel, &pt{ax, ay}, 0, 0)
		return len(moved)
	}

	depthOf := func(n Obj) int {
		d := 0
		for c := n; str(c["parentId"]) != "" && d < 64; {
			d++
			p, ok := byID[str(c["parentId"])]
			if !ok {
				break
			}
			c = p
		}
		return d
	}
	hasKid := map[string]bool{}
	for _, n := range nodes {
		if p := str(n["parentId"]); p != "" {
			hasKid[p] = true
		}
	}
	var groups []Obj
	for _, n := range nodes {
		if hasKid[str(n["id"])] {
			groups = append(groups, n)
		}
	}
	sort.SliceStable(groups, func(i, j int) bool { return depthOf(groups[i]) > depthOf(groups[j]) })
	for _, g := range groups {
		kids := childrenOf(str(g["id"]))
		run(kids, nil, groupPad.left, groupPad.top)
		right, bottom := math.Inf(-1), math.Inf(-1)
		for _, k := range kids {
			x, y := posOf(k)
			w, h := nodeSize(k)
			right, bottom = math.Max(right, x+w), math.Max(bottom, y+h)
		}
		w, h := math.Max(right+groupPad.right, 0), math.Max(bottom+groupPad.bottom, 0)
		if ow, ok := num(g["width"]); !ok || ow != w {
			moved[str(g["id"])] = true
		} else if oh, ok := num(g["height"]); !ok || oh != h {
			moved[str(g["id"])] = true
		}
		g["width"], g["height"] = w, h
	}
	if top := childrenOf(""); len(top) > 0 {
		ax, ay := math.Inf(1), math.Inf(1)
		for _, n := range top {
			x, y := posOf(n)
			ax, ay = math.Min(ax, x), math.Min(ay, y)
		}
		run(top, &pt{ax, ay}, 0, 0)
	}
	if algo == "orthogonal" {
		for _, e := range edgesOf(page) {
			dataOf(e)["lineType"] = "orthogonal"
		}
	}
	return len(moved)
}
