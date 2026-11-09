package diagramhub

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram/diagramtest"
)

// prefixes are, after updates("compaction"), the merge of its first 1, 2, ... updates:
// what a client holding them would send as a compacted snapshot.
var prefixes [][]byte

// updates are real Yjs updates (yjs 13), by fixture name.
func updates(t *testing.T, name string) [][]byte {
	t.Helper()
	raw, err := os.ReadFile("../../domain/diagram/yjs/testdata/updates.json")
	if err != nil {
		t.Fatal(err)
	}
	var fs []struct {
		Name    string
		Updates []string
		Merged  string
		Prefix  []string
	}
	if err := json.Unmarshal(raw, &fs); err != nil {
		t.Fatal(err)
	}
	for _, f := range fs {
		if f.Name == name {
			var out [][]byte
			for _, u := range f.Updates {
				b, _ := hex.DecodeString(u)
				out = append(out, b)
			}
			prefixes = nil
			for _, p := range f.Prefix { // the merge of the first 1, 2, ... updates
				b, _ := hex.DecodeString(p)
				prefixes = append(prefixes, b)
			}
			return out
		}
	}
	t.Fatalf("no fixture %q", name)
	return nil
}

// store is the in-memory repository with files in project "p".
func store(t *testing.T, files ...string) *diagramtest.Memory {
	t.Helper()
	mem := diagramtest.NewMemory()
	now := time.Now()
	if err := mem.CreateProject(context.Background(), diagram.Project{ID: "p", OwnerID: 1, Name: "P", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		doc := diagram.Doc{File: diagram.File{ID: f, ProjectID: "p", Name: f, Kind: "sdd", Version: 1, CreatedAt: now, UpdatedAt: now}, Data: json.RawMessage(`{}`)}
		if err := mem.InsertFile(context.Background(), diagram.NewFile{Doc: doc, First: diagram.Version{Version: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	return mem
}
