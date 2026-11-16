package diagram

import (
	"encoding/json"
	"fmt"
	"html"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Server-side twin of the editor's buildThumbnail (Web/editor/src/lib/thumbnail.ts):
// a small SVG preview of a file's active page, for files that never got one
// from the editor (seeded templates, API imports). Keep the two in step.

// ThumbWidth and ThumbHeight are the preview size, as the editor renders it.
const (
	ThumbWidth  = 480
	ThumbHeight = 300
	thumbPad    = 10
)

var (
	hex6     = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	hex3or6  = regexp.MustCompile(`^#[0-9a-fA-F]{3}([0-9a-fA-F]{3})?$`)
	hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{3,8}$`)
)

type thumbSize struct {
	Width  *float64 `json:"width"`
	Height *float64 `json:"height"`
}

type thumbNode struct {
	ID       string     `json:"id"`
	Type     string     `json:"type"`
	ParentID string     `json:"parentId"`
	Position struct{ X, Y float64 } `json:"position"`
	thumbSize
	Measured *thumbSize `json:"measured"`
	Style    *thumbSize `json:"style"`
	Data     struct {
		Fill any `json:"fill"`
	} `json:"data"`
}

type thumbEdge struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Target string `json:"target"`
	Data   struct {
		Color any `json:"color"`
	} `json:"data"`
}

type thumbDoc struct {
	ActivePageID string `json:"activePageId"`
	Pages        []struct {
		ID    string      `json:"id"`
		Nodes []thumbNode `json:"nodes"`
		Edges []thumbEdge `json:"edges"`
	} `json:"pages"`
	Settings struct {
		Canvas struct {
			Background string `json:"background"`
		} `json:"canvas"`
	} `json:"settings"`
}

// RenderThumbnailSVG draws the preview of a stored file document. An error
// means the document is not one it can read; a file with no pages or nodes
// gets an empty (or background-only) preview.
func RenderThumbnailSVG(data json.RawMessage) ([]byte, error) {
	var d thumbDoc
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("thumbnail: %w", err)
	}
	if len(d.Pages) == 0 {
		return nil, fmt.Errorf("thumbnail: no pages")
	}
	page := d.Pages[0]
	for _, p := range d.Pages {
		if p.ID == d.ActivePageID {
			page = p
			break
		}
	}
	return []byte(buildThumbnail(page.Nodes, page.Edges, ThumbWidth, ThumbHeight, thumbPad, d.Settings.Canvas.Background)), nil
}

func pick(vals ...*float64) float64 {
	for _, v := range vals {
		if v != nil {
			return *v
		}
	}
	return 0
}

func nodeSize(n thumbNode) (w, h float64) {
	var mw, mh, sw, sh *float64
	if n.Measured != nil {
		mw, mh = n.Measured.Width, n.Measured.Height
	}
	if n.Style != nil {
		sw, sh = n.Style.Width, n.Style.Height
	}
	defW, defH := 120.0, 96.0
	return pick(n.Width, mw, sw, &defW), pick(n.Height, mh, sh, &defH)
}

func absolutePosition(byID map[string]*thumbNode, n *thumbNode) (x, y float64) {
	x, y = n.Position.X, n.Position.Y
	seen := map[string]bool{}
	for cur := n; cur.ParentID != "" && !seen[cur.ParentID]; {
		seen[cur.ParentID] = true
		p, ok := byID[cur.ParentID]
		if !ok {
			break
		}
		x += p.Position.X
		y += p.Position.Y
		cur = p
	}
	return x, y
}

func hexRGB(c string) [3]float64 {
	m := strings.TrimPrefix(strings.TrimSpace(c), "#")
	if len(m) == 3 {
		m = string([]byte{m[0], m[0], m[1], m[1], m[2], m[2]})
	}
	var out [3]float64
	for i := range out {
		v, _ := strconv.ParseUint(m[i*2:i*2+2], 16, 8)
		out[i] = float64(v)
	}
	return out
}

func luminance(c string) float64 {
	lin := func(v float64) float64 {
		s := v / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	rgb := hexRGB(c)
	return 0.2126*lin(rgb[0]) + 0.7152*lin(rgb[1]) + 0.0722*lin(rgb[2])
}

func contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	return (math.Max(la, lb) + 0.05) / (math.Min(la, lb) + 0.05)
}

// isDarkBackground: a canvas colour that wants light ink.
func isDarkBackground(bg string) bool {
	if !hex3or6.MatchString(strings.TrimSpace(bg)) {
		return false
	}
	return contrast("#eceff5", bg) > contrast("#1f2430", bg)
}

// num prints like the editor: one decimal, no trailing ".0".
func num(v float64) string {
	r := math.Round(v*10) / 10
	if r == 0 {
		r = 0 // no "-0"
	}
	return strconv.FormatFloat(r, 'f', -1, 64)
}

func colorOr(v any, def string) string {
	if s, ok := v.(string); ok && hexColor.MatchString(s) {
		return s
	}
	return def
}

func buildThumbnail(nodes []thumbNode, edges []thumbEdge, W, H, pad float64, background string) string {
	bg := ""
	if hex6.MatchString(background) {
		bg = background
	}
	dark := bg != "" && isDarkBackground(bg)
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %s %s" width="%s" height="%s" role="img" aria-hidden="true">`, num(W), num(H), num(W), num(H))
	if bg != "" {
		fmt.Fprintf(&b, `<rect width="%s" height="%s" fill="%s"/>`, num(W), num(H), bg)
	}
	if len(nodes) == 0 {
		b.WriteString(`</svg>`)
		return b.String()
	}
	type rect struct {
		n          *thumbNode
		x, y, w, h float64
	}
	byNode := make(map[string]*thumbNode, len(nodes))
	for i := range nodes {
		if _, dup := byNode[nodes[i].ID]; !dup {
			byNode[nodes[i].ID] = &nodes[i]
		}
	}
	rects := make([]rect, len(nodes))
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for i := range nodes {
		x, y := absolutePosition(byNode, &nodes[i])
		w, h := nodeSize(nodes[i])
		rects[i] = rect{&nodes[i], x, y, w, h}
		minX, minY = math.Min(minX, x), math.Min(minY, y)
		maxX, maxY = math.Max(maxX, x+w), math.Max(maxY, y+h)
	}
	scale := math.Min((W-pad*2)/math.Max(1, maxX-minX), (H-pad*2)/math.Max(1, maxY-minY))
	ox := (W-(maxX-minX)*scale)/2 - minX*scale
	oy := (H-(maxY-minY)*scale)/2 - minY*scale
	esc := html.EscapeString
	byID := make(map[string]rect, len(rects))
	for _, r := range rects {
		byID[r.n.ID] = r // last wins, like the editor's Map
	}
	isBox := func(t string) bool { return t == "group" || t == "cluster" || t == "frame" }
	for _, r := range rects {
		if !isBox(r.n.Type) {
			continue
		}
		stroke, dash := "#8a93a3", ` stroke-dasharray="3 2"`
		if dark {
			stroke = "#b6bdcb"
		}
		if r.n.Type == "frame" {
			stroke, dash = "#a5a5f5", ""
		}
		fmt.Fprintf(&b, `<rect data-node="%s" x="%s" y="%s" width="%s" height="%s" rx="3" fill="none" stroke="%s" stroke-width="0.8"%s/>`,
			esc(r.n.ID), num(r.x*scale+ox), num(r.y*scale+oy), num(r.w*scale), num(r.h*scale), stroke, dash)
	}
	for _, e := range edges {
		a, okA := byID[e.Source]
		c, okB := byID[e.Target]
		if !okA || !okB {
			continue
		}
		fmt.Fprintf(&b, `<line data-edge="%s" x1="%s" y1="%s" x2="%s" y2="%s" stroke="%s" stroke-width="0.9"/>`,
			esc(e.ID), num((a.x+a.w/2)*scale+ox), num((a.y+a.h/2)*scale+oy), num((c.x+c.w/2)*scale+ox), num((c.y+c.h/2)*scale+oy), colorOr(e.Data.Color, "#94a3b8"))
	}
	for _, r := range rects {
		if isBox(r.n.Type) {
			continue
		}
		own := colorOr(r.n.Data.Fill, "")
		var fill string
		switch {
		case r.n.Type == "sticky":
			fill = own
			if fill == "" {
				fill = "#fde68a"
			}
		case r.n.Type == "text" && dark:
			fill = "#6b7385"
		case r.n.Type == "text":
			fill = "#cbd5e1"
		case dark && own != "":
			fill = own
		case dark:
			fill = "#a5b4fc"
		default:
			fill = "#6366f1"
		}
		k := 0.55
		if r.n.Type == "sticky" {
			k = 0.9
		}
		w := math.Max(3, r.w*scale*k)
		h := math.Max(3, r.h*scale*k)
		fmt.Fprintf(&b, `<rect data-node="%s" x="%s" y="%s" width="%s" height="%s" rx="2" fill="%s" fill-opacity="0.85"/>`,
			esc(r.n.ID), num((r.x+r.w/2)*scale+ox-w/2), num((r.y+r.h/2)*scale+oy-h/2), num(w), num(h), fill)
	}
	b.WriteString(`</svg>`)
	return b.String()
}
