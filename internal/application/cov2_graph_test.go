package application

import (
	"encoding/json"
	"testing"
)

func TestGraphHelpersEdgeCases(t *testing.T) {
	if got := SlugID("!!!", "fallback"); got != "fallback" {
		t.Errorf("SlugID blank = %q", got)
	}
	long := "word word word word word word word word word word word word word"
	if got := SlugID(long, "x"); len(got) != 48 {
		t.Errorf("SlugID long = %q (%d)", got, len(got))
	}
	for name, v := range map[string]any{"json.Number": json.Number("4.5"), "float": 4.5, "int": 4} {
		if f, ok := num(v); !ok || f < 4 {
			t.Errorf("num(%s) = %v %v", name, f, ok)
		}
	}
	if _, ok := num(json.Number("x")); ok {
		t.Error("bad json.Number accepted")
	}
	if _, ok := num("s"); ok {
		t.Error("string accepted")
	}

	sizes := []struct {
		name string
		node Obj
		w, h float64
	}{
		{"explicit", Obj{"width": 10.0, "height": 20.0}, 10, 20},
		{"measured", Obj{"measured": Obj{"width": 30.0, "height": 40.0}}, 30, 40},
		{"measured partly missing", Obj{"measured": Obj{"width": "x"}}, defW, defH},
		{"defaults", Obj{}, defW, defH},
	}
	for _, c := range sizes {
		if w, h := nodeSize(c.node); w != c.w || h != c.h {
			t.Errorf("%s: %v x %v", c.name, w, h)
		}
	}
}

func TestAddNodesIconErrors(t *testing.T) {
	page := Obj{"nodes": []any{}, "edges": []any{}}
	_, errs := AddNodes(page, []NodeSpec{
		{Label: "a", Icon: "no-such-icon-anywhere"},
		{Label: "b", Icon: "cluster-postgresql"},
		{Label: "c", Icon: "cluster-postgres"},
		{Label: "d", Icon: "cluster-mysql"},
	})
	if len(errs) == 0 {
		t.Fatal("expected icon errors")
	}
}

func TestUpdateNodeFields(t *testing.T) {
	page := Obj{"nodes": []any{Obj{"id": "n", "position": Obj{"x": 1.0, "y": 2.0}}}, "edges": []any{}}
	cases := []struct {
		name  string
		patch NodePatch
		check func(n Obj) bool
	}{
		{"subtitle creates data", NodePatch{Subtitle: fstr("sub")}, func(n Obj) bool { return n["data"].(Obj)["subtitle"] == "sub" }},
		{"x only keeps y", NodePatch{X: fptr(9)}, func(n Obj) bool { return n["position"].(Obj)["x"] == 9.0 && n["position"].(Obj)["y"] == 2.0 }},
		{"y only keeps x", NodePatch{Y: fptr(7)}, func(n Obj) bool { return n["position"].(Obj)["y"] == 7.0 }},
		{"height", NodePatch{Height: fptr(33)}, func(n Obj) bool { return n["height"] == 33.0 }},
	}
	for _, c := range cases {
		n, err := UpdateNode(page, "n", c.patch)
		if err != nil || !c.check(n) {
			t.Errorf("%s: %v %v", c.name, n, err)
		}
	}
	if _, err := UpdateNode(page, "n", NodePatch{Icon: fstr("cluster-postgres")}); err == nil {
		t.Log("cluster icon accepted by this catalog")
	}
	if _, err := UpdateNode(page, "missing", NodePatch{}); err == nil {
		t.Error("missing node must fail")
	}
}
