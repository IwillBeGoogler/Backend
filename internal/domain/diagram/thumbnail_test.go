package diagram_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
)

// testdata/thumbnail_golden.json is the editor's buildThumbnail output
// (Web/editor/src/lib/thumbnail.ts) for one fixture page; the Go twin must
// draw the same bytes. Regenerate it when the editor's drawing changes.
func TestRenderThumbnailSVGMatchesEditor(t *testing.T) {
	raw, err := os.ReadFile("testdata/thumbnail_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var g struct {
		Nodes json.RawMessage   `json:"nodes"`
		Edges json.RawMessage   `json:"edges"`
		Out   map[string]string `json:"out"`
	}
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	for name, bg := range map[string]string{"light": "", "dark": "#101418"} {
		doc := `{"version":2,"activePageId":"p2","pages":[{"id":"p1","nodes":[]},{"id":"p2","nodes":` + string(g.Nodes) + `,"edges":` + string(g.Edges) + `}],"settings":{"canvas":{"background":"` + bg + `"}}}`
		got, err := diagram.RenderThumbnailSVG(json.RawMessage(doc))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != g.Out[name] {
			t.Errorf("%s:\n got %s\nwant %s", name, got, g.Out[name])
		}
	}
}

func TestRenderThumbnailSVGEdgeCases(t *testing.T) {
	if _, err := diagram.RenderThumbnailSVG(json.RawMessage(`not json`)); err == nil {
		t.Error("bad JSON: want error")
	}
	if _, err := diagram.RenderThumbnailSVG(json.RawMessage(`{"pages":[]}`)); err == nil {
		t.Error("no pages: want error")
	}
	// unknown active page falls back to the first; an empty page is a bare svg
	got, err := diagram.RenderThumbnailSVG(json.RawMessage(`{"activePageId":"x","pages":[{"id":"p","nodes":[]}]}`))
	if err != nil || !strings.HasSuffix(string(got), `aria-hidden="true"></svg>`) {
		t.Errorf("empty page: %s %v", got, err)
	}
	// a parent cycle must not hang
	got, err = diagram.RenderThumbnailSVG(json.RawMessage(`{"pages":[{"id":"p","nodes":[
		{"id":"a","type":"icon","parentId":"b","position":{"x":1,"y":1},"data":{}},
		{"id":"b","type":"icon","parentId":"a","position":{"x":5,"y":5},"data":{}}]}]}`))
	if err != nil || strings.Count(string(got), "<rect") != 2 {
		t.Errorf("cycle: %s %v", got, err)
	}
}
