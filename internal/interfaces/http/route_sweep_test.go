package http

import (
	"context"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// diagramRoutes reads the route table from the sources, so a new route is
// swept without touching this test.
func diagramRoutes(t *testing.T) []string {
	t.Helper()
	files, _ := filepath.Glob("*.go")
	re := regexp.MustCompile(`HandleFunc\("([A-Z]+) (/[^"]*)"|HandleFunc\("([A-Z]+) "\+(p|v|base|DiagramPrefix|DiagramMCPPath)(?:\+"([^"]*)")?`)
	seen := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || !(strings.HasPrefix(f, "diagram") || f == "admin_diagrams.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(src), -1) {
			if m[1] != "" {
				seen[m[1]+" "+m[2]] = true
				continue
			}
			var prefixes []string
			switch m[4] {
			case "v":
				prefixes = []string{DiagramV1Prefix}
			case "DiagramMCPPath":
				prefixes = []string{DiagramMCPPath}
			default:
				prefixes = []string{DiagramPrefix, DiagramV1Prefix}
			}
			for _, p := range prefixes {
				seen[m[3]+" "+p+m[5]] = true
			}
		}
	}
	var out []string
	for r := range seen {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

// TestDiagramRouteSweep calls every diagram route as nobody, a stranger and
// the owner, with no body, a broken body and an empty object: the handlers
// must answer (never drop the connection) whatever they are sent.
func TestDiagramRouteSweep(t *testing.T) {
	srv, _ := newDiagramServerWith(t, func(_ *application.Diagrams, d *Deps) {
		d.Limit = func(context.Context, string, int, time.Duration) (bool, time.Duration) { return true, 0 }
	})
	alice := dgClient{t, srv, 1}
	pid := alice.expect(alice.do("POST", "/projects", map[string]any{"name": "P"}), 201).obj("project")["id"].(string)
	tree := alice.expect(alice.do("GET", "/projects/"+pid+"/tree", nil), 200)
	fid := tree.Body["files"].([]any)[0].(map[string]any)["id"].(string)
	folder := alice.expect(alice.do("POST", "/projects/"+pid+"/folders", map[string]any{"name": "F"}), 201).Body["id"]
	folderID, _ := folder.(string)
	if folderID == "" {
		folderID = "fld_x"
	}
	com := alice.do("POST", "/files/"+fid+"/comments", map[string]any{"body": "hi"})
	cid := "cmt_x"
	if c := com.obj("comment"); c != nil {
		cid, _ = c["id"].(string)
	}

	repl := strings.NewReplacer("{id}", "ID", "{v}", "1", "{userId}", "2", "{wid}", "whk_x", "{username}", "alice",
		"{slug}", "slug-x", "{token}", "tok", "{ticket}", "tk", "{hash}", strings.Repeat("a", 64))
	resolve := func(route string) string {
		method, path, _ := strings.Cut(route, " ")
		id := "x"
		switch {
		case strings.Contains(path, "/projects/{id}"):
			id = pid
		case strings.Contains(path, "/files/{id}"):
			id = fid
		case strings.Contains(path, "/folders/{id}"):
			id = folderID
		case strings.Contains(path, "/comments/{id}"):
			id = cid
		}
		return method + " " + strings.Replace(repl.Replace(strings.Replace(path, "{id}", id, 1)), "ID", id, 1)
	}

	bodies := []any{nil, "{broken", map[string]any{}}
	n := 0
	for _, route := range diagramRoutes(t) {
		method, path, _ := strings.Cut(resolve(route), " ")
		if strings.Contains(path, "/ws") || strings.HasSuffix(path, "/stream") || strings.Contains(path, "/mcp") {
			continue // upgrades and streams have their own tests
		}
		path = strings.TrimPrefix(path, DiagramPrefix)
		for _, user := range []int{0, 3, 1} {
			c := dgClient{t, srv, user}
			for _, body := range bodies {
				if method == "GET" && body != nil {
					continue
				}
				r := c.do(method, path, body)
				if r.Code == 0 {
					t.Errorf("%s as %d: no answer", route, user)
				}
				n++
			}
		}
	}
	if n < 200 {
		t.Fatalf("swept only %d calls: route table not read?", n)
	}
}
