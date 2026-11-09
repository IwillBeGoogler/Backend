package diagram_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
)

func TestRoles(t *testing.T) {
	if !diagram.RoleOwner.Allows(diagram.RoleEditor) || !diagram.RoleEditor.Allows(diagram.RoleViewer) {
		t.Error("higher roles must allow lower ones")
	}
	if diagram.RoleViewer.Allows(diagram.RoleEditor) || diagram.Role("").Allows(diagram.RoleViewer) {
		t.Error("viewer must not edit; no role must not view")
	}
	if diagram.RoleOwner.OverrideRole() || !diagram.RoleViewer.OverrideRole() || !diagram.RoleEditor.OverrideRole() {
		t.Error("only viewer and editor can be per-file overrides")
	}
}

func TestNewID(t *testing.T) {
	id := diagram.NewID(diagram.PrefixProject)
	if !strings.HasPrefix(id, "prj_") || len(id) != len("prj_")+16 {
		t.Errorf("id = %q, want prj_ + 16 hex chars", id)
	}
	if err := diagram.CheckClientID(id); err != nil {
		t.Errorf("our own ids must be valid client ids: %v", err)
	}
}

func TestClientID(t *testing.T) {
	for _, ok := range []string{"", "fil_client001", "abcdef", strings.Repeat("a", 64)} {
		if err := diagram.CheckClientID(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"short", "bad id!", "../x", strings.Repeat("a", 65)} {
		var e *diagram.Error
		if err := diagram.CheckClientID(bad); !errors.As(err, &e) || e.Code != "INVALID_ID" {
			t.Errorf("%q: err = %v, want INVALID_ID", bad, err)
		}
	}
}

func TestCleanName(t *testing.T) {
	if n, err := diagram.CleanName("  spaced  ", "x"); err != nil || n != "spaced" {
		t.Errorf("trim: %q, %v", n, err)
	}
	if n, _ := diagram.CleanName("   ", "Untitled"); n != "Untitled" {
		t.Errorf("fallback: %q", n)
	}
	if n, _ := diagram.CleanName(strings.Repeat("é", 300), "x"); len([]rune(n)) != diagram.MaxNameLen {
		t.Errorf("cut to %d runes, got %d", diagram.MaxNameLen, len([]rune(n)))
	}
	for _, bad := range []string{"a\nb", "a\x07b", "a\x7fb"} {
		var e *diagram.Error
		if _, err := diagram.CleanName(bad, "x"); !errors.As(err, &e) || e.Code != "INVALID_NAME" {
			t.Errorf("%q: err = %v, want INVALID_NAME", bad, err)
		}
	}
	if err := diagram.CheckNameSize("", true); err == nil {
		t.Error("required name accepted empty")
	}
	if err := diagram.CheckNameSize(strings.Repeat("x", 201), false); err == nil {
		t.Error("201 characters accepted")
	}
}

func TestParseIfMatch(t *testing.T) {
	for in, want := range map[string]int{`"3"`: 3, `W/"3"`: 3, `3`: 3, ` "12" `: 12} {
		if n, ok := diagram.ParseIfMatch(in); !ok || n != want {
			t.Errorf("%q = %d, %v; want %d", in, n, ok, want)
		}
	}
	for _, bad := range []string{"", "abc", `"1"x`, "-1", "*"} {
		if _, ok := diagram.ParseIfMatch(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestErrorKinds(t *testing.T) {
	cases := []struct {
		err  error
		kind error
		code string
	}{
		{diagram.NotFound("File"), domain.ErrNotFound, "NOT_FOUND"},
		{diagram.NeedRole(diagram.RoleEditor), domain.ErrForbidden, "FORBIDDEN"},
		{diagram.ErrIDTaken, domain.ErrConflict, "ID_TAKEN"},
		{diagram.ErrCycle, domain.ErrConflict, "CYCLE"},
		{diagram.VersionConflict(4, false), diagram.ErrPrecondition, "VERSION_CONFLICT"},
		{diagram.IfMatchRequired(1), diagram.ErrPrecondition, "IF_MATCH_REQUIRED"},
		{diagram.TooLarge("big"), diagram.ErrTooLarge, "PAYLOAD_TOO_LARGE"},
	}
	for _, c := range cases {
		var e *diagram.Error
		if !errors.Is(c.err, c.kind) || !errors.As(c.err, &e) || e.Code != c.code {
			t.Errorf("%v: want kind %v, code %s", c.err, c.kind, c.code)
		}
	}
	var v *domain.ValidationError
	if !errors.As(diagram.ErrParentNotFound, &v) {
		t.Error("PARENT_NOT_FOUND must be a validation error")
	}
	if e := diagram.VersionConflict(4, false).(*diagram.Error); e.Current != 4 {
		t.Errorf("Current = %d", e.Current)
	}
}

func fld(id string, parent *string) diagram.Folder { return diagram.Folder{ID: id, ParentID: parent} }

func TestSubtreeIDs(t *testing.T) {
	a, b := "a", "b"
	folders := []diagram.Folder{fld("a", nil), fld("b", &a), fld("c", &b), fld("d", nil)}
	got := diagram.SubtreeIDs(folders, "a")
	if len(got) != 3 || !got["a"] || !got["b"] || !got["c"] || got["d"] {
		t.Errorf("subtree of a = %v", got)
	}
	// a corrupt cycle must terminate
	c := "c"
	cyc := []diagram.Folder{fld("a", &c), fld("b", &a), fld("c", &b)}
	if got := diagram.SubtreeIDs(cyc, "a"); len(got) != 3 {
		t.Errorf("cycle = %v", got)
	}
}

func TestKeepVersions(t *testing.T) {
	const day = 24 * time.Hour
	now := time.Unix(100*86400, 5)
	v := func(n int, age time.Duration, named bool) diagram.Version {
		return diagram.Version{Version: n, CreatedAt: now.Add(-age), Named: named}
	}
	keys := func(m map[int]bool) (out []int) {
		for k := 1; k < 20; k++ {
			if m[k] {
				out = append(out, k)
			}
		}
		return
	}
	t.Run("last N", func(t *testing.T) {
		vs := []diagram.Version{v(6, time.Second, false), v(5, 2*time.Second, false), v(4, 3*time.Second, false), v(3, 4*time.Second, false)}
		if got := keys(diagram.KeepVersions(vs, now, 2, 0)); len(got) != 2 || got[0] != 5 || got[1] != 6 {
			t.Errorf("kept %v, want [5 6]", got)
		}
	})
	t.Run("newest of each day inside the window", func(t *testing.T) {
		vs := []diagram.Version{
			v(1, 10*day, false), v(2, 10*day-time.Second, false), // one day: keep v2
			v(3, 5*day, false),
			v(4, 40*day, false), // outside the window
			v(5, time.Second, false), v(6, 500*time.Millisecond, false), v(7, 100*time.Millisecond, false),
		}
		got := keys(diagram.KeepVersions(vs, now, 2, 30))
		want := []int{2, 3, 6, 7}
		if len(got) != len(want) {
			t.Fatalf("kept %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("kept %v, want %v", got, want)
			}
		}
	})
	t.Run("named and latest are kept", func(t *testing.T) {
		vs := []diagram.Version{v(2, 80*day, false), v(1, 90*day, true)}
		if got := diagram.KeepVersions(vs, now, 1, 0); !got[2] || !got[1] {
			t.Errorf("kept %v, want 1 (named) and 2 (latest)", got)
		}
	})
}

func TestMergePrefs(t *testing.T) {
	cur := json.RawMessage(`{"a":1,"b":[1,2]}`)
	got, err := diagram.MergePrefs(cur, json.RawMessage(`{"b":null,"c":{"x":true}}`))
	if err != nil || string(got) != `{"a":1,"c":{"x":true}}` {
		t.Errorf("merge = %s, %v", got, err)
	}
	if got, _ := diagram.MergePrefs(json.RawMessage(`[1]`), json.RawMessage(`{"a":1}`)); string(got) != `{"a":1}` {
		t.Errorf("non-object current counts as empty: %s", got)
	}
	if got, _ := diagram.MergePrefs(cur, json.RawMessage(`{"__proto__":1}`)); string(got) != `{"a":1,"b":[1,2]}` {
		t.Errorf("__proto__ must be ignored: %s", got)
	}
	if _, err := diagram.MergePrefs(cur, json.RawMessage(`[1]`)); err == nil {
		t.Error("array patch accepted")
	}
	if _, err := diagram.MergePrefs(cur, json.RawMessage(`{"":1}`)); err == nil {
		t.Error("empty key accepted")
	}
	big, _ := json.Marshal(map[string]string{"big": strings.Repeat("x", diagram.MaxPrefsBytes)})
	if _, err := diagram.MergePrefs(cur, big); !errors.Is(err, diagram.ErrTooLarge) {
		t.Errorf("oversize err = %v", err)
	}
}

func page(id string, nodes, edges string) string {
	return `{"id":"` + id + `","name":"P","nodes":` + nodes + `,"edges":` + edges + `}`
}

func doc(active string, pages ...string) json.RawMessage {
	return json.RawMessage(`{"version":2,"activePageId":"` + active + `","pages":[` + strings.Join(pages, ",") + `],"futureKey":{"x":1}}`)
}

func TestPrepare(t *testing.T) {
	created, updated := time.UnixMilli(1000), time.UnixMilli(2000)
	n := func(id, typ string) string { return `{"id":"` + id + `","type":"` + typ + `"}` }

	t.Run("owns identity, keeps unknown fields, strips transient state", func(t *testing.T) {
		nodes := `[{"id":"a","type":"service","selected":true,"dragging":true,"measured":{"w":1},"resizing":false,"extra":7}]`
		edges := `[{"id":"e","source":"a","target":"a","selected":true,"className":"edge-new"}]`
		p, err := diagram.Prepare(doc("p1", page("p1", nodes, edges)), "fil_1", "Name", created, updated)
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		if err := json.Unmarshal(p.Data, &out); err != nil {
			t.Fatal(err)
		}
		if out["id"] != "fil_1" || out["name"] != "Name" || out["createdAt"] != float64(1000) || out["updatedAt"] != float64(2000) {
			t.Errorf("identity not enforced: %v", out)
		}
		if out["futureKey"] == nil {
			t.Error("unknown field dropped")
		}
		node := out["pages"].([]any)[0].(map[string]any)["nodes"].([]any)[0].(map[string]any)
		for _, k := range []string{"selected", "dragging", "measured", "resizing"} {
			if _, has := node[k]; has {
				t.Errorf("node kept %s", k)
			}
		}
		if node["extra"] != float64(7) {
			t.Error("unknown node field dropped")
		}
		edge := out["pages"].([]any)[0].(map[string]any)["edges"].([]any)[0].(map[string]any)
		if _, has := edge["selected"]; has || edge["className"] != nil {
			t.Errorf("edge kept transient state: %v", edge)
		}
		if p.PageCount != 1 {
			t.Errorf("page count = %d", p.PageCount)
		}
	})

	t.Run("dominant kind", func(t *testing.T) {
		cases := []struct {
			nodes string
			want  string
		}{
			{`[]`, "sdd"},
			{`[` + n("a", "service") + `]`, "sdd"},
			{`[` + n("a", "sticky") + `,` + n("b", "sticky") + `,` + n("c", "service") + `]`, "board"},
			{`[` + n("a", "table") + `,` + n("b", "erdNote") + `]`, "erd"},
			{`[` + n("a", "class") + `,` + n("b", "umlThing") + `,` + n("c", "service") + `]`, "uml"},
			{`[` + n("a", "deployment") + `,` + n("b", "deployment") + `,` + n("c", "service") + `]`, "uml"},
			{`[` + n("a", "sticky") + `,` + n("b", "service") + `]`, "sdd"}, // a tie stays sdd
			// a package diagram is groups (and a class or two): the UML-only group variants count as UML, a plain group does not
			{`[{"id":"g1","type":"group","data":{"variant":"package"}},{"id":"g2","type":"group","data":{"variant":"package"}},` + n("c", "class") + `,` + n("d", "service") + `]`, "uml"},
			{`[{"id":"g1","type":"group","data":{"variant":"lane"}},{"id":"g2","type":"group","data":{"variant":"composite"}},{"id":"g3","type":"group","data":{"variant":"boundary"}}]`, "uml"},
			{`[{"id":"g1","type":"group","data":{"variant":"card"}},{"id":"g2","type":"group"},` + n("c", "class") + `]`, "sdd"},
		}
		for _, c := range cases {
			p, err := diagram.Prepare(doc("p1", page("p1", c.nodes, `[]`)), "f", "n", created, updated)
			if err != nil || p.Kind != c.want {
				t.Errorf("%s: kind = %q, %v; want %q", c.nodes, p.Kind, err, c.want)
			}
		}
	})

	t.Run("group parents are fine", func(t *testing.T) {
		nodes := `[` + n("g", "group") + `,{"id":"a","type":"service","parentId":"g"}]`
		if _, err := diagram.Prepare(doc("p1", page("p1", nodes, `[]`)), "f", "n", created, updated); err != nil {
			t.Error(err)
		}
	})

	t.Run("deployment and component containers can hold nested nodes", func(t *testing.T) {
		for _, typ := range []string{"deployment", "component"} {
			nodes := `[` + n("g", typ) + `,{"id":"a","type":"` + typ + `","parentId":"g"}]`
			if _, err := diagram.Prepare(doc("p1", page("p1", nodes, `[]`)), "f", "n", created, updated); err != nil {
				t.Errorf("%s: %v", typ, err)
			}
		}
	})

	t.Run("package groups hold classes and object diagrams are uml", func(t *testing.T) {
		nodes := `[{"id":"g","type":"group","data":{"variant":"package"}},{"id":"c","type":"class","parentId":"g"},{"id":"o","type":"object","data":{"label":"a","className":"A","slots":[{"id":"s0","name":"x","value":"1"}]}}]`
		p, err := diagram.Prepare(doc("p1", page("p1", nodes, `[{"id":"e","source":"g","target":"c","data":{"uml":{"kind":"import"}}}]`)), "f", "n", created, updated)
		if err != nil || p.Kind != "uml" {
			t.Errorf("kind = %q, %v", p.Kind, err)
		}
	})

	t.Run("timing diagrams and communication links are uml", func(t *testing.T) {
		tm := `{"id":"t","type":"timing","data":{"label":"T","end":100,"unit":"ms","lifelines":[{"id":"a","name":"A","kind":"robust","states":["x"]},{"id":"b","name":"B","kind":"clock","states":[],"period":10}],"changes":[{"id":"c1","life":"a","t":0,"state":"x"}],"messages":[{"id":"m1","from":"a","to":"b","t":5,"text":"go"}],"durations":[{"id":"k1","life":"a","t1":0,"t2":5,"text":"{5}"}]}}`
		o := `{"id":"o1","type":"object","data":{"label":"a","slots":[]}},{"id":"o2","type":"object","data":{"label":"b","slots":[]}}`
		e := `[{"id":"e","source":"o1","target":"o2","data":{"uml":{"kind":"link","messages":[{"id":"m","seq":"1","text":"x","dir":"fwd"}]}}}]`
		p, err := diagram.Prepare(doc("p1", page("p1", `[`+tm+`,`+o+`]`, e)), "f", "n", created, updated)
		if err != nil || p.Kind != "uml" {
			t.Errorf("kind = %q, %v", p.Kind, err)
		}
	})

	bad := map[string]json.RawMessage{
		"timing without lifelines":   doc("p1", page("p1", `[{"id":"t","type":"timing","data":{"label":"T","end":10,"unit":"","changes":[],"messages":[],"durations":[]}}]`, `[]`)),
		"timing end zero":            doc("p1", page("p1", `[{"id":"t","type":"timing","data":{"label":"T","end":0,"unit":"","lifelines":[],"changes":[],"messages":[],"durations":[]}}]`, `[]`)),
		"timing change unknown life": doc("p1", page("p1", `[{"id":"t","type":"timing","data":{"label":"T","end":10,"unit":"","lifelines":[{"id":"a","name":"A","kind":"robust","states":[]}],"changes":[{"id":"c","life":"zz","t":0,"state":"x"}],"messages":[],"durations":[]}}]`, `[]`)),
		"timing bad kind":            doc("p1", page("p1", `[{"id":"t","type":"timing","data":{"label":"T","end":10,"unit":"","lifelines":[{"id":"a","name":"A","kind":"fast","states":[]}],"changes":[],"messages":[],"durations":[]}}]`, `[]`)),
		"timing duplicate item id":   doc("p1", page("p1", `[{"id":"t","type":"timing","data":{"label":"T","end":10,"unit":"","lifelines":[{"id":"a","name":"A","kind":"robust","states":[]}],"changes":[{"id":"c","life":"a","t":0,"state":"x"}],"messages":[],"durations":[{"id":"c","life":"a","t1":0,"t2":1,"text":""}]}}]`, `[]`)),
		"object without slots":       doc("p1", page("p1", `[{"id":"o","type":"object","data":{"label":"a"}}]`, `[]`)),
		"object duplicate slot id":   doc("p1", page("p1", `[{"id":"o","type":"object","data":{"label":"a","slots":[{"id":"s","name":"x","value":""},{"id":"s","name":"y","value":""}]}}]`, `[]`)),
		"object slot value number":   doc("p1", page("p1", `[{"id":"o","type":"object","data":{"label":"a","slots":[{"id":"s","name":"x","value":1}]}}]`, `[]`)),
		"not an object":              json.RawMessage(`[]`),
		"version 1":                  json.RawMessage(`{"version":1,"activePageId":"p1","pages":[` + page("p1", `[]`, `[]`) + `]}`),
		"no pages":                   json.RawMessage(`{"version":2,"activePageId":"p1","pages":[]}`),
		"unknown active":             doc("nope", page("p1", `[]`, `[]`)),
		"duplicate page":             doc("p1", page("p1", `[]`, `[]`), page("p1", `[]`, `[]`)),
		"duplicate node":             doc("p1", page("p1", `[`+n("a", "service")+`,`+n("a", "service")+`]`, `[]`)),
		"dangling edge":              doc("p1", page("p1", `[`+n("a", "service")+`]`, `[{"id":"e","source":"a","target":"ghost"}]`)),
		"duplicate edge":             doc("p1", page("p1", `[`+n("a", "service")+`]`, `[{"id":"e","source":"a","target":"a"},{"id":"e","source":"a","target":"a"}]`)),
		"parent not a group":         doc("p1", page("p1", `[`+n("b", "service")+`,{"id":"a","type":"service","parentId":"b"}]`, `[]`)),
		"missing parent":             doc("p1", page("p1", `[{"id":"a","type":"service","parentId":"zz"}]`, `[]`)),
		"node without type":          doc("p1", page("p1", `[{"id":"a"}]`, `[]`)),
		"NUL character":              doc("p1", page("p1", `[{"id":"a","type":"service","label":"x\u0000y"}]`, `[]`)),
	}
	for name, d := range bad {
		var v *domain.ValidationError
		if _, err := diagram.Prepare(d, "f", "n", created, updated); !errors.As(err, &v) {
			t.Errorf("%s: err = %v, want a validation error", name, err)
		}
	}
}

func TestEmptyData(t *testing.T) {
	d := diagram.EmptyData("Blank", time.UnixMilli(5))
	p, err := diagram.Prepare(d, "f", "Blank", time.UnixMilli(5), time.UnixMilli(5))
	if err != nil || p.PageCount != 1 || p.Kind != diagram.KindSDD {
		t.Fatalf("empty doc: %+v, %v", p, err)
	}
}
