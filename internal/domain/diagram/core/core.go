// Package core is the diagram toolkit of the TypeScript client's core:
// the icon catalog search, the deterministic auto-layout, the ".sdd" text
// format (diagrams as code), the lint rules and the text exporters. Pages are
// plain decoded JSON objects (Obj), so fields the code does not know survive
// untouched. Every function is pure and deterministic; the golden tests in
// testdata compare the output with the TypeScript original.
//
//go:generate node --no-warnings gen/gen-catalog.mjs
package core

import (
	"encoding/json"
	"math"
	"strconv"
)

// Obj is a JSON object.
type Obj = map[string]any

// Normalize deep-copies v through JSON so every number is a float64 and every
// object an Obj: the shape all functions of this package expect.
func Normalize(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if json.Unmarshal(b, &out) != nil {
		return v
	}
	return out
}

// NormalizePage is Normalize for a page.
func NormalizePage(p Obj) Obj {
	o, _ := Normalize(p).(Obj)
	if o == nil {
		o = Obj{}
	}
	return o
}

func num(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, !math.IsNaN(n)
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func numOr(v any, d float64) float64 {
	if f, ok := num(v); ok {
		return f
	}
	return d
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// jsStr renders a value the way JS String(v) does for the shapes used here.
func jsStr(v any) string {
	switch x := v.(type) {
	case nil:
		return "undefined"
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return jsNum(x)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// jsNum formats a number like JS Number#toString for the ranges used here.
func jsNum(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e21 {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// truthy is JS truthiness.
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0 && !math.IsNaN(x)
	}
	return true
}

func obj(v any) Obj { o, _ := v.(Obj); return o }

func list(v any) []any { l, _ := v.([]any); return l }

func objs(v any) []Obj {
	var out []Obj
	for _, x := range list(v) {
		if o, ok := x.(Obj); ok {
			out = append(out, o)
		}
	}
	return out
}

func anys(os []Obj) []any {
	out := make([]any, len(os))
	for i, o := range os {
		out[i] = o
	}
	return out
}

func nodesOf(p Obj) []Obj { return objs(p["nodes"]) }
func edgesOf(p Obj) []Obj { return objs(p["edges"]) }

func dataOf(n Obj) Obj {
	d, ok := n["data"].(Obj)
	if !ok {
		d = Obj{}
		n["data"] = d
	}
	return d
}

func posOf(n Obj) (float64, float64) {
	p := obj(n["position"])
	return numOr(p["x"], 0), numOr(p["y"], 0)
}

func setPos(n Obj, x, y float64) { n["position"] = Obj{"x": x, "y": y} }

// Default node size (types.ts DEFAULT_SIZE).
const defW, defH = 120.0, 96.0

func nodeSize(n Obj) (float64, float64) {
	m := obj(n["measured"])
	w := numOr(n["width"], numOr(m["width"], defW))
	h := numOr(n["height"], numOr(m["height"], defH))
	return w, h
}

// round2 rounds like Math.round(v*100)/100.
func round2(v float64) float64 { return jsRound(float64(v*100)) / 100 }

// jsRound is Math.round: halves go up.
func jsRound(v float64) float64 { return math.Floor(v + 0.5) }
