package diagram

import (
	"encoding/json"
	"strings"
	"testing"
)

const secret = "my-secret-text"

func sticky(id, author, label string, private bool) string {
	p := ""
	if private {
		p = `"private":true,"authorId":"` + author + `",`
	}
	return `{"id":"` + id + `","type":"sticky","position":{"x":5,"y":5},"data":{` + p + `"label":"` + label + `","fill":"#fef08a"}}`
}

func docWith(nodes []string, edges ...string) json.RawMessage {
	return json.RawMessage(`{"version":2,"activePageId":"pg","extra":{"keep":1.50},"pages":[{"id":"pg","name":"P","nodes":[` +
		strings.Join(nodes, ",") + `],"edges":[` + strings.Join(edges, ",") + `]}]}`)
}

func ids(t *testing.T, raws []json.RawMessage) []string {
	t.Helper()
	var out []string
	for _, r := range raws {
		var n struct{ ID string }
		if err := json.Unmarshal(r, &n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n.ID)
	}
	return out
}

func known(allowed ...string) func(string) bool {
	return func(id string) bool {
		for _, a := range allowed {
			if a == id {
				return true
			}
		}
		return false
	}
}

func TestScrubPrivateMovesStickiesToTheirAuthors(t *testing.T) {
	doc := docWith(
		[]string{`{"id":"a","type":"service"}`, sticky("s1", "2", secret, true), sticky("open", "", "visible", false),
			sticky("s2", "3", "other secret", true), sticky("s3", "ghost", "unknown author", true)},
		`{"id":"e1","source":"a","target":"s1"}`, `{"id":"e2","source":"a","target":"open"}`, `{"id":"e3","source":"s2","target":"a"}`)
	clean, by, removed := ScrubPrivate(doc, known("2", "3"), "1")
	if removed != 3 {
		t.Fatalf("removed = %d", removed)
	}
	if strings.Contains(string(clean), secret) || strings.Contains(string(clean), "other secret") || strings.Contains(string(clean), "unknown author") {
		t.Errorf("private text left in the document: %s", clean)
	}
	var out struct {
		Extra json.RawMessage
		Pages []struct {
			Nodes, Edges []json.RawMessage
		}
	}
	if err := json.Unmarshal(clean, &out); err != nil {
		t.Fatal(err)
	}
	if got := ids(t, out.Pages[0].Nodes); strings.Join(got, ",") != "a,open" {
		t.Errorf("shared nodes = %v", got)
	}
	if got := ids(t, out.Pages[0].Edges); strings.Join(got, ",") != "e2" {
		t.Errorf("shared edges = %v (edges touching a private sticky go with it)", got)
	}
	if string(out.Extra) != `{"keep":1.50}` {
		t.Errorf("unknown fields must survive untouched: %s", out.Extra)
	}
	if got := ids(t, by["2"].Nodes); strings.Join(got, ",") != "s1" || strings.Join(ids(t, by["2"].Edges), ",") != "e1" {
		t.Errorf("user 2 = %v / %v", got, ids(t, by["2"].Edges))
	}
	if got := ids(t, by["3"].Nodes); strings.Join(got, ",") != "s2" || strings.Join(ids(t, by["3"].Edges), ",") != "e3" {
		t.Errorf("user 3 = %v", got)
	}
	// an unknown author's sticky goes to the fallback, stamped as theirs
	if got := ids(t, by["1"].Nodes); strings.Join(got, ",") != "s3" || !strings.Contains(string(by["1"].Nodes[0]), `"authorId":"1"`) {
		t.Errorf("fallback = %s", by["1"].Nodes)
	}
}

func TestScrubPrivateLeavesCleanDocumentsAlone(t *testing.T) {
	for name, doc := range map[string]json.RawMessage{
		"no stickies":         docWith([]string{`{"id":"a","type":"service"}`}),
		"public sticky":       docWith([]string{sticky("s", "", "hi", false)}),
		"private not boolean": docWith([]string{`{"id":"s","type":"sticky","data":{"private":"true","label":"x"}}`}),
		"private non-sticky":  docWith([]string{`{"id":"s","type":"service","data":{"private":true}}`}),
		"not a document":      json.RawMessage(`[1,2]`),
		"no pages":            json.RawMessage(`{"version":2}`),
	} {
		clean, by, removed := ScrubPrivate(doc, known(), "1")
		if removed != 0 || len(by) != 0 || string(clean) != string(doc) {
			t.Errorf("%s: removed %d, changed %v", name, removed, string(clean) != string(doc))
		}
	}
}

func TestScrubPrivateEveryPage(t *testing.T) {
	doc := json.RawMessage(`{"version":2,"activePageId":"p1","pages":[
		{"id":"p1","name":"A","nodes":[` + sticky("a1", "2", "page one "+secret, true) + `],"edges":[]},
		{"id":"p2","name":"B","nodes":[` + sticky("b1", "2", "page two "+secret, true) + `,{"id":"n","type":"service"}],"edges":[]}]}`)
	clean, by, removed := ScrubPrivate(doc, known("2"), "1")
	if removed != 2 || strings.Contains(string(clean), secret) || len(by["2"].Nodes) != 2 {
		t.Errorf("removed %d by %v clean %s", removed, by, clean)
	}
}

func TestMergePrivate(t *testing.T) {
	cur := json.RawMessage(`{"nodes":[{"id":"a","v":1},{"id":"b","v":1}],"edges":[{"id":"e","v":1}]}`)
	got := MergePrivate(cur, PrivateSet{
		Nodes: []json.RawMessage{json.RawMessage(`{"id":"b","v":2}`), json.RawMessage(`{"id":"c","v":2}`)},
		Edges: []json.RawMessage{json.RawMessage(`{"id":"e2","v":2}`)}})
	want := `{"edges":[{"id":"e","v":1},{"id":"e2","v":2}],"nodes":[{"id":"a","v":1},{"id":"b","v":2},{"id":"c","v":2}]}`
	if string(got) != want {
		t.Errorf("merge = %s", got)
	}
	if got := MergePrivate(nil, PrivateSet{}); string(got) != `{"edges":[],"nodes":[]}` {
		t.Errorf("empty merge = %s", got)
	}
}
