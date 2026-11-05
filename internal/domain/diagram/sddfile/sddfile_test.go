package sddfile

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var png = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{7}, 40)...)

func hashHex(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func sampleDoc(t *testing.T) Doc {
	t.Helper()
	asset := []byte("<svg xmlns='http://www.w3.org/2000/svg'/>")
	data, err := json.Marshal(map[string]any{
		"version": 2, "id": "fil_1", "name": "Shop", "createdAt": 1700000000000, "updatedAt": 1700000001000,
		"activePageId": "pg_b", "settings": map[string]any{"grid": true},
		"pages": []any{
			map[string]any{"id": "pg_a", "name": "Overview", "nodes": []any{
				map[string]any{"id": "n1", "type": "icon", "position": map[string]any{"x": 1, "y": 2}, "data": map[string]any{"label": "API", "iconId": "asset:" + hashHex(asset)}},
				map[string]any{"id": "n2", "type": "icon", "position": map[string]any{"x": 3, "y": 4}, "data": map[string]any{"label": "Logo", "iconId": "asset:" + strings.Repeat("0", 64)}},
			}, "edges": []any{map[string]any{"id": "e1", "source": "n1", "target": "n2"}}, "future": map[string]any{"keep": []int{1, 2}}},
			map[string]any{"id": "pg_b", "name": "ER", "nodes": []any{}, "edges": []any{}},
		},
		"assets": map[string]any{hashHex(asset): map[string]any{"mime": "image/svg+xml", "data": base64.StdEncoding.EncodeToString(asset)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return Doc{ID: "fil_1", Name: "Shop", CreatedAt: 1700000000000, UpdatedAt: 1700000001000, Data: data}
}

func TestFileRoundTrip(t *testing.T) {
	d := sampleDoc(t)
	b, err := BuildFile(d, map[string][]byte{"pg_b": png})
	if err != nil {
		t.Fatal(err)
	}
	// layout: mimetype first and stored
	zr, _ := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if zr.File[0].Name != "mimetype" || zr.File[0].Method != zip.Store {
		t.Errorf("first entry = %s (method %d)", zr.File[0].Name, zr.File[0].Method)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	for _, want := range []string{"manifest.json", "project.json", "pages/pg_a.json", "pages/pg_b.json", "thumbnails/pg_b.png"} {
		found := false
		for _, n := range names {
			found = found || n == want
		}
		if !found {
			t.Errorf("entry %s missing from %v", want, names)
		}
	}
	if s, _ := Sniff(b, "x.sdd", Limits{}); s.Kind != KindFile || s.Legacy != "" {
		t.Errorf("sniff = %+v", s)
	}

	r, err := ReadFile(b, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if r.ID != "fil_1" || r.Name != "Shop" || r.CreatedAt != d.CreatedAt || r.UpdatedAt != d.UpdatedAt {
		t.Errorf("doc = %+v", r.Doc)
	}
	var got, want map[string]any
	_ = json.Unmarshal(r.Data, &got)
	_ = json.Unmarshal(d.Data, &want)
	if !jsonEqual(got["pages"], want["pages"]) || !jsonEqual(got["assets"], want["assets"]) || !jsonEqual(got["settings"], want["settings"]) || got["activePageId"] != "pg_b" {
		t.Errorf("document changed in the round trip:\n got %s\nwant %s", r.Data, d.Data)
	}
	if !bytes.Equal(r.Thumbnails["pg_b"], png) {
		t.Error("thumbnail lost")
	}
	if len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], `custom icon asset for "Logo" is missing`) {
		t.Errorf("warnings = %v", r.Warnings)
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

func TestProjectRoundTrip(t *testing.T) {
	a, b := sampleDoc(t), sampleDoc(t)
	b.ID, b.Name = "fil_2", "Second"
	root := "fld_root"
	p := Project{Name: "Shop project", CreatedAt: 1, UpdatedAt: 2,
		Folders: []Folder{{ID: root, Name: "Docs", Position: 1}, {ID: "fld_sub", ParentID: &root, Name: "Sub", Position: 2}},
		Files:   []File{{Doc: a, Kind: "sdd", Position: 1, Thumbnail: png}, {Doc: b, FolderID: &root, Kind: "erd", Position: 3}}}
	bytesOut, err := BuildProject(p)
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := Sniff(bytesOut, "", Limits{}); s.Kind != KindProject {
		t.Fatalf("sniff = %+v", s)
	}
	if _, err := ReadFile(bytesOut, Limits{}); !isCode(err, CodeUnsupported) {
		t.Errorf("reading a project as a file: %v", err)
	}
	got, warnings, err := ReadProject(bytesOut, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != p.Name || len(got.Folders) != 2 || len(got.Files) != 2 || got.Files[1].FolderID == nil || *got.Files[1].FolderID != root || got.Files[1].Kind != "erd" {
		t.Errorf("project = %+v", got)
	}
	if !bytes.Equal(got.Files[0].Thumbnail, png) || got.Files[0].Position != 1 || len(warnings) != 2 {
		t.Errorf("file 0 thumbnail/position or warnings: %d %v", len(got.Files[0].Thumbnail), warnings)
	}
	// a single-file archive is not a project
	one, _ := BuildFile(a, nil)
	if _, _, err := ReadProject(one, Limits{}); !isCode(err, CodeUnsupported) {
		t.Errorf("reading a file as a project: %v", err)
	}
}

func isCode(err error, code string) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

func raw(t *testing.T, entries ...[2]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e[0], Method: zip.Store}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(e[1]))
	}
	_ = zw.Close()
	return buf.Bytes()
}

func TestReadRejectsBadArchives(t *testing.T) {
	good, _ := BuildFile(sampleDoc(t), nil)
	cases := map[string]struct {
		in   []byte
		lim  Limits
		code string
	}{
		"not a zip":        {[]byte("PK\x03\x04 nope nope nope nope nope"), Limits{}, CodeCorruptZip},
		"tiny":             {[]byte("PK"), Limits{}, CodeCorruptZip},
		"too large":        {good, Limits{MaxFileBytes: 100}, CodeTooLarge},
		"too many entries": {good, Limits{MaxEntries: 3}, CodeTooManyEntry},
		"zip bomb":         {good, Limits{MaxUncompressedBytes: 50}, CodeZipBomb},
		"path traversal":   {raw(t, [2]string{"mimetype", Mimetype}, [2]string{"../evil", "x"}), Limits{}, CodeUnsafePath},
		"absolute path":    {raw(t, [2]string{"mimetype", Mimetype}, [2]string{"/etc/passwd", "x"}), Limits{}, CodeUnsafePath},
		"backslash":        {raw(t, [2]string{"mimetype", Mimetype}, [2]string{`a\b`, "x"}), Limits{}, CodeUnsafePath},
		"no mimetype":      {raw(t, [2]string{"manifest.json", "{}"}), Limits{}, CodeBadMimetype},
		"wrong mimetype":   {raw(t, [2]string{"mimetype", "text/plain"}, [2]string{"manifest.json", "{}"}), Limits{}, CodeBadMimetype},
		"no manifest":      {raw(t, [2]string{"mimetype", Mimetype}), Limits{}, CodeMissingEntry},
		"bad json":         {raw(t, [2]string{"mimetype", Mimetype}, [2]string{"manifest.json", "{"}), Limits{}, CodeBadJSON},
		"bad manifest":     {raw(t, [2]string{"mimetype", Mimetype}, [2]string{"manifest.json", `{"format":"nope"}`}), Limits{}, CodeSchema},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ReadFile(c.in, c.lim)
			if !isCode(err, c.code) {
				t.Errorf("err = %v, want %s", err, c.code)
			}
		})
	}
	// a stored mimetype is required (deflated is refused)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Deflate})
	_, _ = w.Write([]byte(Mimetype))
	_ = zw.Close()
	if _, err := ReadFile(buf.Bytes(), Limits{}); !isCode(err, CodeBadMimetype) {
		t.Errorf("deflated mimetype: %v", err)
	}
}

// rewrite builds the good archive again with one entry changed.
func rewrite(t *testing.T, src []byte, change func(name string, data []byte) []byte) []byte {
	t.Helper()
	zr, _ := zip.NewReader(bytes.NewReader(src), int64(len(src)))
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		rc, _ := f.Open()
		var data bytes.Buffer
		_, _ = data.ReadFrom(rc)
		rc.Close()
		out := change(f.Name, data.Bytes())
		w, _ := zw.CreateHeader(&zip.FileHeader{Name: f.Name, Method: f.Method})
		_, _ = w.Write(out)
	}
	_ = zw.Close()
	return buf.Bytes()
}

func TestReadDetectsTampering(t *testing.T) {
	good, _ := BuildFile(sampleDoc(t), nil)
	page := rewrite(t, good, func(n string, d []byte) []byte {
		if n == "pages/pg_a.json" {
			return append(d, ' ')
		}
		return d
	})
	if _, err := ReadFile(page, Limits{}); !isCode(err, CodeChecksum) {
		t.Errorf("edited page: %v", err)
	}
	asset := rewrite(t, good, func(n string, d []byte) []byte {
		if strings.HasPrefix(n, "assets/") {
			return []byte("<svg/>")
		}
		return d
	})
	if _, err := ReadFile(asset, Limits{}); !isCode(err, CodeAsset) {
		t.Errorf("edited asset: %v", err)
	}
	missing := rewrite(t, good, func(n string, d []byte) []byte {
		if n == "manifest.json" {
			return []byte(strings.Replace(string(d), "pages/pg_b.json", "pages/gone.json", 1))
		}
		return d
	})
	if _, err := ReadFile(missing, Limits{}); !isCode(err, CodeMissingEntry) {
		t.Errorf("missing page: %v", err)
	}
	newer := rewrite(t, good, func(n string, d []byte) []byte {
		if n == "manifest.json" {
			return []byte(strings.Replace(string(d), `"formatVersion": 1`, `"formatVersion": 9`, 1))
		}
		return d
	})
	if _, err := ReadFile(newer, Limits{}); !isCode(err, CodeUnsupported) {
		t.Errorf("newer format: %v", err)
	}
}

func TestSafeName(t *testing.T) {
	for n, want := range map[string]bool{"pages/a.json": true, "files/x/source.sdd": true, "a..b/c": true, "": false, "/abs": false, "../up": false,
		"a/../b": false, "a/..": false, `a\b`: false, "c:evil": false, "nul\x00": false} {
		if SafeName(n) != want {
			t.Errorf("SafeName(%q) = %v", n, !want)
		}
	}
}

func TestSniff(t *testing.T) {
	for _, c := range []struct {
		name, in string
		want     Sniffed
		fail     bool
	}{
		{"text", "#!sdd 1\nnode a \"A\"\n", Sniffed{Kind: KindText}, false},
		{"text no version", "\n\n#!sdd\n", Sniffed{Kind: KindText}, false},
		{"text bom", "\xef\xbb\xbf#!sdd 1\n", Sniffed{Kind: KindText}, false},
		{"legacy sddt", "node a\n", Sniffed{Kind: KindText, Legacy: "sddt"}, false},
		{"plain text", "hello", Sniffed{}, true},
		{"binary", "\x00\x01\x02\x03", Sniffed{}, true},
		{"empty", "", Sniffed{}, true},
	} {
		name := "x.sdd"
		if c.name == "legacy sddt" {
			name = "x.SDDT"
		}
		got, err := Sniff([]byte(c.in), name, Limits{})
		if (err != nil) != c.fail || got != c.want {
			t.Errorf("%s: %+v %v", c.name, got, err)
		}
	}
	if err := ErrTextUnsupported(); !isCode(err, CodeUnsupported) {
		t.Error("text error code")
	}
	legacy := raw(t, [2]string{"mimetype", LegacyProjMIME}, [2]string{"manifest.json", `{"format":"sddproj"}`})
	if s, _ := Sniff(legacy, "", Limits{}); s.Kind != KindProject || s.Legacy != "sddproj" {
		t.Errorf("legacy project = %+v", s)
	}
	notype := raw(t, [2]string{"mimetype", Mimetype}, [2]string{"manifest.json", `{"format":"sdd"}`})
	if s, _ := Sniff(notype, "", Limits{}); s.Kind != KindFile || s.Legacy != "no-type" {
		t.Errorf("no type = %+v", s)
	}
}

func TestValidateTree(t *testing.T) {
	p := func(s string) *string { return &s }
	bad := map[string][]Folder{
		"duplicate":      {{ID: "a"}, {ID: "a"}},
		"unknown parent": {{ID: "a", ParentID: p("zz")}},
		"cycle":          {{ID: "a", ParentID: p("b")}, {ID: "b", ParentID: p("a")}},
		"self":           {{ID: "a", ParentID: p("a")}},
	}
	for name, fs := range bad {
		if err := ValidateTree(fs, nil); !isCode(err, CodeBadTree) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := ValidateTree([]Folder{{ID: "a"}, {ID: "b", ParentID: p("a")}}, []projManifestFile{{ID: "f", FolderID: p("b")}}); err != nil {
		t.Errorf("valid tree: %v", err)
	}
	if err := ValidateTree(nil, []projManifestFile{{ID: "f", FolderID: p("nope")}}); !isCode(err, CodeBadTree) {
		t.Errorf("file in unknown folder: %v", err)
	}
	if err := ValidateTree(nil, []projManifestFile{{ID: "f"}, {ID: "f"}}); !isCode(err, CodeBadTree) {
		t.Errorf("duplicate file: %v", err)
	}
}

func TestMigrateV1(t *testing.T) {
	m, err := migrateProject([]byte(`{"schemaVersion":1,"activePage":"p"}`))
	if err != nil || string(m["activePageId"]) != `"p"` || m["activePage"] != nil {
		t.Errorf("project v1: %v %v", m, err)
	}
	pg, err := migratePage([]byte(`{"schemaVersion":1,"nodes":[{"id":"n","data":{"icon":"redis"}}]}`), "p")
	if err != nil || !strings.Contains(string(pg["nodes"]), `"iconId":"redis"`) || strings.Contains(string(pg["nodes"]), `"icon":`) {
		t.Errorf("page v1: %s %v", pg["nodes"], err)
	}
	if _, err := migratePage([]byte(`{"schemaVersion":99}`), "p"); !isCode(err, CodeUnsupported) {
		t.Errorf("newer schema: %v", err)
	}
}

// TestNodeInterop checks the container against the Node server's reader and
// writer. It runs when SDD_INTEROP_DIR names a directory: it writes go-*.sdd
// there for the Node reader, and reads node-*.sdd the Node writer made
// (see the comment in the test body for the commands).
func TestNodeInterop(t *testing.T) {
	dir := os.Getenv("SDD_INTEROP_DIR")
	if dir == "" {
		t.Skip("SDD_INTEROP_DIR not set")
	}
	d := sampleDoc(t)
	file, err := BuildFile(d, map[string][]byte{"pg_b": png})
	if err != nil {
		t.Fatal(err)
	}
	root := "fld_root"
	proj, err := BuildProject(Project{Name: "P", CreatedAt: 1, UpdatedAt: 2, Folders: []Folder{{ID: root, Name: "Docs"}},
		Files: []File{{Doc: d, FolderID: &root, Kind: "sdd", Position: 1, Thumbnail: png}}})
	if err != nil {
		t.Fatal(err)
	}
	for name, b := range map[string][]byte{"go-file.sdd": file, "go-project.sdd": proj} {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if b, err := os.ReadFile(filepath.Join(dir, "node-file.sdd")); err == nil {
		r, err := ReadFile(b, Limits{})
		if err != nil {
			t.Fatalf("Node file: %v", err)
		}
		fmt.Println("node file:", r.Name, len(r.Warnings), "warnings")
	}
	if b, err := os.ReadFile(filepath.Join(dir, "node-project.sdd")); err == nil {
		p, _, err := ReadProject(b, Limits{})
		if err != nil {
			t.Fatalf("Node project: %v", err)
		}
		if len(p.Files) == 0 {
			t.Error("Node project has no files")
		}
	}
}
