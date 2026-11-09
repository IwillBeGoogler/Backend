package diagram_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
)

func xprep(raw string) (diagram.Prepared, error) {
	t := time.UnixMilli(1000)
	return diagram.Prepare(json.RawMessage(raw), "id1", "name1", t, t)
}

// page wraps nodes and edges (JSON arrays) in a valid single-page document.
func xpage(nodes, edges string) string {
	return fmt.Sprintf(`{"version":2,"activePageId":"p1","pages":[{"id":"p1","name":"P","nodes":%s,"edges":%s}]}`, nodes, edges)
}

func xnode(id, typ, extra string) string {
	if extra != "" {
		extra = "," + extra
	}
	return fmt.Sprintf(`{"id":%q,"type":%q%s}`, id, typ, extra)
}

func TestPrepareRejects(t *testing.T) {
	obj := func(data string) string { return xpage("["+xnode("n", "object", `"data":`+data)+"]", "[]") }
	cls := func(data string) string { return xpage("["+xnode("n", "class", `"data":`+data)+"]", "[]") }
	seq := func(data string) string { return xpage("["+xnode("n", "sequence", `"data":`+data)+"]", "[]") }
	tim := func(data string) string { return xpage("["+xnode("n", "timing", `"data":`+data)+"]", "[]") }
	good := `{"end":10,"unit":"ms","lifelines":[{"id":"a","name":"A","kind":"clock","states":["x"]},{"id":"b","name":"B","kind":"binary","states":[]}],"changes":[],"messages":[],"durations":[]}`
	withTiming := func(field, v string) string {
		var m map[string]any
		_ = json.Unmarshal([]byte(good), &m)
		var val any
		_ = json.Unmarshal([]byte(v), &val)
		m[field] = val
		b, _ := json.Marshal(m)
		return tim(string(b))
	}
	cases := map[string]string{
		"nul":                  `{"version":2,"x":"\u0000"}`,
		"not object":           `[1]`,
		"null":                 `null`,
		"bad version":          `{"version":3}`,
		"no active":            `{"version":2,"pages":[]}`,
		"empty active":         `{"version":2,"activePageId":"","pages":[]}`,
		"no pages":             `{"version":2,"activePageId":"a","pages":[]}`,
		"bad pages":            `{"version":2,"activePageId":"a","pages":5}`,
		"dup page":             `{"version":2,"activePageId":"p","pages":[{"id":"p","name":"n","nodes":[],"edges":[]},{"id":"p","name":"n","nodes":[],"edges":[]}]}`,
		"active unknown":       `{"version":2,"activePageId":"zz","pages":[{"id":"p","name":"n","nodes":[],"edges":[]}]}`,
		"page no id":           `{"version":2,"activePageId":"p","pages":[{"name":"n","nodes":[],"edges":[]}]}`,
		"page no name":         `{"version":2,"activePageId":"p","pages":[{"id":"p","nodes":[],"edges":[]}]}`,
		"page no nodes":        `{"version":2,"activePageId":"p","pages":[{"id":"p","name":"n","edges":[]}]}`,
		"page no edges":        `{"version":2,"activePageId":"p","pages":[{"id":"p","name":"n","nodes":[]}]}`,
		"node no id":           xpage(`[{"type":"x"}]`, "[]"),
		"node no type":         xpage(`[{"id":"a"}]`, "[]"),
		"dup node":             xpage("["+xnode("a", "x", "")+","+xnode("a", "x", "")+"]", "[]"),
		"parent unknown":       xpage("["+xnode("a", "x", `"parentId":"zz"`)+"]", "[]"),
		"parent not group":     xpage("["+xnode("p", "x", "")+","+xnode("a", "x", `"parentId":"p"`)+"]", "[]"),
		"edge no id":           xpage("["+xnode("a", "x", "")+"]", `[{"source":"a","target":"a"}]`),
		"edge dup":             xpage("["+xnode("a", "x", "")+"]", `[{"id":"e","source":"a","target":"a"},{"id":"e","source":"a","target":"a"}]`),
		"edge no source":       xpage("["+xnode("a", "x", "")+"]", `[{"id":"e","target":"a"}]`),
		"edge no target":       xpage("["+xnode("a", "x", "")+"]", `[{"id":"e","source":"a"}]`),
		"edge unknown source":  xpage("["+xnode("a", "x", "")+"]", `[{"id":"e","source":"zz","target":"a"}]`),
		"edge unknown target":  xpage("["+xnode("a", "x", "")+"]", `[{"id":"e","source":"a","target":"zz"}]`),
		"object data array":    obj(`[]`),
		"object class num":     obj(`{"className":1,"slots":[]}`),
		"object no slots":      obj(`{}`),
		"object bad slot":      obj(`{"slots":[{"id":"a","name":"n"}]}`),
		"object dup slot":      obj(`{"slots":[{"id":"a","name":"n","value":"v"},{"id":"a","name":"n","value":"v"}]}`),
		"class modelId num":    cls(`{"modelId":3}`),
		"seq modelId num":      seq(`{"participants":[{"modelId":3}]}`),
		"timing data array":    tim(`[]`),
		"timing end":           withTiming("end", `0`),
		"timing unit":          withTiming("unit", `5`),
		"timing lifelines":     withTiming("lifelines", `5`),
		"lifeline no id":       withTiming("lifelines", `[{"name":"A","kind":"clock","states":[]}]`),
		"lifeline kind":        withTiming("lifelines", `[{"id":"a","name":"A","kind":"zzz","states":[]}]`),
		"lifeline states":      withTiming("lifelines", `[{"id":"a","name":"A","kind":"clock","states":5}]`),
		"lifeline dup":         withTiming("lifelines", `[{"id":"a","name":"A","kind":"clock","states":[]},{"id":"a","name":"A","kind":"clock","states":[]}]`),
		"changes not array":    withTiming("changes", `5`),
		"change no id":         withTiming("changes", `[{"life":"a"}]`),
		"change dup id":        withTiming("changes", `[{"id":"c","life":"a"},{"id":"c","life":"a"}]`),
		"change unknown life":  withTiming("changes", `[{"id":"c","life":"zz"}]`),
		"message unknown from": withTiming("messages", `[{"id":"m","from":"zz","to":"a"}]`),
		"message unknown to":   withTiming("messages", `[{"id":"m","from":"a","to":"zz"}]`),
	}
	for name, raw := range cases {
		if _, err := xprep(raw); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestPrepareAccepts(t *testing.T) {
	timing := `{"end":10,"unit":"ms","lifelines":[{"id":"a","name":"A","kind":"robust","states":["x"]},{"id":"b","name":"B","kind":"concise","states":[]}],` +
		`"changes":[{"id":"c","life":"a"}],"messages":[{"id":"m","from":"a","to":"b"}],"durations":[{"id":"d","life":"b"}]}`
	nodes := "[" + strings.Join([]string{
		xnode("g", "group", `"data":{"variant":"package"}`),
		xnode("g2", "group", `"data":{"variant":"other"}`),
		xnode("g3", "group", `"data":5`),
		xnode("a", "class", `"parentId":"g","selected":true,"dragging":true,"data":{"modelId":"m1"}`),
		xnode("o", "object", `"data":{"className":"C","slots":[{"id":"s","name":"n","value":"v"}]}`),
		xnode("s", "sequence", `"data":{"participants":[{"modelId":"m"},{}]}`),
		xnode("s2", "sequence", `"data":{"participants":5}`),
		xnode("t", "timing", `"data":`+timing),
		xnode("tb", "table", ""), xnode("ub", "umlnote", ""), xnode("st", "sticky", ""),
	}, ",") + "]"
	edges := `[{"id":"e","source":"a","target":"o","selected":true,"className":"edge-new"},{"id":"e2","source":"o","target":"a","className":"keep"}]`
	p, err := xprep(xpage(nodes, edges))
	if err != nil {
		t.Fatal(err)
	}
	if p.PageCount != 1 || p.Kind != diagram.KindUML {
		t.Fatalf("%+v", p)
	}
	s := string(p.Data)
	if strings.Contains(s, `"selected"`) || strings.Contains(s, "edge-new") || !strings.Contains(s, `"keep"`) || !strings.Contains(s, `"id":"id1"`) {
		t.Fatalf("not normalized: %s", s)
	}
	// parent that is a component / deployment / cluster node is fine
	for _, pt := range []string{"component", "deployment", "cluster"} {
		if _, err := xprep(xpage("["+xnode("p", pt, "")+","+xnode("a", "x", `"parentId":"p"`)+"]", "[]")); err != nil {
			t.Errorf("%s parent: %v", pt, err)
		}
	}
}

func TestKindsAndEmptyData(t *testing.T) {
	for _, k := range []string{diagram.KindSDD, diagram.KindERD, diagram.KindUML, diagram.KindBoard} {
		if !diagram.ValidKind(k) {
			t.Errorf("%s", k)
		}
	}
	if diagram.ValidKind("nope") {
		t.Error("nope valid")
	}
	// board and erd dominate
	if p, _ := xprep(xpage("["+xnode("a", "sticky", "")+","+xnode("b", "draw", "")+"]", "[]")); p.Kind != diagram.KindBoard {
		t.Errorf("board: %s", p.Kind)
	}
	if p, _ := xprep(xpage("["+xnode("a", "erd-table", "")+"]", "[]")); p.Kind != diagram.KindERD {
		t.Errorf("erd: %s", p.Kind)
	}
	if p, _ := xprep(xpage("[]", "[]")); p.Kind != diagram.KindSDD {
		t.Errorf("empty: %s", p.Kind)
	}
	raw := diagram.EmptyData("N", time.UnixMilli(5))
	if p, err := diagram.Prepare(raw, "i", "N", time.UnixMilli(5), time.UnixMilli(5)); err != nil || p.PageCount != 1 {
		t.Fatalf("EmptyData invalid: %v", err)
	}
}
