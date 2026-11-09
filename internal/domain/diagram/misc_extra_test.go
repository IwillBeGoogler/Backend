package diagram_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
)

func TestNotSQLiteError(t *testing.T) {
	if diagram.NotSQLiteError.Error(diagram.NotSQLiteError{}) == "" {
		t.Fatal("empty")
	}
}

func TestOptUnmarshal(t *testing.T) {
	var s struct {
		A diagram.Opt[string] `json:"a"`
		B diagram.Opt[int]    `json:"b"`
		C diagram.Opt[string] `json:"c"`
	}
	if err := json.Unmarshal([]byte(`{"a":"x","b":null}`), &s); err != nil {
		t.Fatal(err)
	}
	if !s.A.Set || s.A.Val != "x" || !s.B.Set || s.B.Val != 0 || s.C.Set {
		t.Fatalf("%+v", s)
	}
	if err := json.Unmarshal([]byte(`{"a":5}`), &s); err == nil {
		t.Fatal("type mismatch accepted")
	}
}

func TestYStateLastSeq(t *testing.T) {
	if (diagram.YState{SnapshotSeq: 4}).LastSeq() != 4 {
		t.Fatal("snapshot")
	}
	if (diagram.YState{SnapshotSeq: 4, Updates: []diagram.YUpdate{{Seq: 5}, {Seq: 6}}}).LastSeq() != 6 {
		t.Fatal("updates")
	}
}

func TestWebhookHelpers(t *testing.T) {
	w := diagram.Webhook{Active: true, Events: []string{"a", "b"}}
	if !w.Subscribed("a") || w.Subscribed("z") {
		t.Fatal("Subscribed")
	}
	w.Active = false
	if w.Subscribed("a") {
		t.Fatal("inactive subscribed")
	}
	s1, s2 := diagram.NewWebhookSecret(), diagram.NewWebhookSecret()
	if !strings.HasPrefix(s1, "whsec_") || len(s1) != 6+32 || s1 == s2 {
		t.Fatalf("%q", s1)
	}
}

func TestValidScope(t *testing.T) {
	if len(diagram.Scopes) == 0 || !diagram.ValidScope(diagram.Scopes[0]) || diagram.ValidScope("nope") {
		t.Fatal("ValidScope")
	}
}

func TestCleanTagsAndPublication(t *testing.T) {
	got, err := diagram.CleanTags([]string{" Go ", "go", "", "Rust"})
	if err != nil || len(got) != 2 || got[0] != "go" {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := diagram.CleanTags([]string{strings.Repeat("a", diagram.MaxTagLen+1)}); err == nil {
		t.Error("long tag accepted")
	}
	if _, err := diagram.CleanTags([]string{"a\x01b"}); err == nil {
		t.Error("control char accepted")
	}
	many := make([]string, diagram.MaxPublishTags+1)
	for i := range many {
		many[i] = "t" + string(rune('a'+i))
	}
	if _, err := diagram.CleanTags(many); err == nil {
		t.Error("too many tags accepted")
	}

	vis := diagram.VisPublic
	p, err := diagram.CleanPublication(vis, "  ", " d ", nil, "File")
	if err != nil || p.Title != "File" || p.Description != "d" {
		t.Fatalf("%+v %v", p, err)
	}
	bad := []struct {
		vis             diagram.Visibility
		title, desc, fn string
		tags            []string
	}{
		{"nope", "t", "", "f", nil},
		{vis, "a\x01", "", "f", nil},
		{vis, "", "", "", nil},
		{vis, strings.Repeat("a", diagram.MaxPublishTitle+1), "", "f", nil},
		{vis, "t", strings.Repeat("a", diagram.MaxPublishDesc+1), "f", nil},
		{vis, "t", "", "f", []string{"a\x01"}},
	}
	for i, b := range bad {
		if _, err := diagram.CleanPublication(b.vis, b.title, b.desc, b.tags, b.fn); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
}

func TestCheckNameSizeAndPrefs(t *testing.T) {
	if diagram.CheckNameSize("", true) == nil {
		t.Error("required empty accepted")
	}
	if diagram.CheckNameSize("", false) != nil || diagram.CheckNameSize("ok", true) != nil {
		t.Error("valid rejected")
	}
	if diagram.CheckNameSize(strings.Repeat("a", diagram.MaxNameLen+1), false) == nil {
		t.Error("long accepted")
	}

	got, err := diagram.MergePrefs(json.RawMessage(`{"a":1,"b":2}`), json.RawMessage(`{"a":null,"c":3,"__proto__":{"x":1}}`))
	if err != nil || string(got) != `{"b":2,"c":3}` {
		t.Fatalf("%s %v", got, err)
	}
	if got, _ := diagram.MergePrefs(json.RawMessage(`5`), json.RawMessage(`{"a":1}`)); string(got) != `{"a":1}` {
		t.Errorf("non-object current: %s", got)
	}
	for _, p := range []string{`5`, `null`, `{"":1}`, `{"` + strings.Repeat("k", 101) + `":1}`} {
		if _, err := diagram.MergePrefs(nil, json.RawMessage(p)); err == nil {
			t.Errorf("%.20s accepted", p)
		}
	}
	big := `{"k":"` + strings.Repeat("x", diagram.MaxPrefsBytes) + `"}`
	if _, err := diagram.MergePrefs(nil, json.RawMessage(big)); err == nil {
		t.Error("big prefs accepted")
	}
}

func TestCleanTemplateControlChars(t *testing.T) {
	if _, err := diagram.CleanTemplate(diagram.Template{Title: "a\x01b", Kind: diagram.KindSDD}); err == nil {
		t.Error("control char title accepted")
	}
}


func TestScrubPrivateBadPages(t *testing.T) {
	raw := json.RawMessage(`{"pages":5}`)
	out, by, n := diagram.ScrubPrivate(raw, func(string) bool { return true }, "u")
	if string(out) != string(raw) || by != nil || n != 0 {
		t.Fatalf("%s %v %d", out, by, n)
	}
}

func TestScrubPrivatePageWithoutNodes(t *testing.T) {
	raw := json.RawMessage(`{"pages":[{"id":"p"}]}`)
	out, _, n := diagram.ScrubPrivate(raw, func(string) bool { return true }, "u")
	if string(out) != string(raw) || n != 0 {
		t.Fatalf("%s %d", out, n)
	}
}
