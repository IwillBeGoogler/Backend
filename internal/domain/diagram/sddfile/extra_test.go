package sddfile

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"strings"
	"testing"
)

type obj = map[string]any

func jb(v any) []byte { b, _ := json.MarshalIndent(v, "", "  "); return b }

// parts is a small hand-built "file" archive; tests change one thing.
type parts struct {
	mf, proj, page obj
	pageRaw        []byte // replaces the page file
	projRaw        []byte // replaces project.json
	extra          [][2]string
	skipProject    bool
	skipPage       bool
}

func newParts() *parts {
	return &parts{
		mf: obj{"format": "sdd", "formatVersion": 1, "type": "file", "appVersion": "1", "createdAt": 1, "updatedAt": 2,
			"projectId": "f", "name": "N", "pages": []any{obj{"id": "pa", "name": "A", "file": "pages/pa.json"}}},
		proj: obj{"schemaVersion": 8, "id": "f", "name": "N", "createdAt": 1, "updatedAt": 2, "activePageId": "pa", "pageOrder": []string{"pa"}},
		page: obj{"schemaVersion": 8, "id": "pa", "name": "A", "nodes": []any{}, "edges": []any{}},
	}
}

func (p *parts) build(t *testing.T) []byte {
	t.Helper()
	projB := p.projRaw
	if projB == nil {
		projB = jb(p.proj)
	}
	pageB := p.pageRaw
	if pageB == nil {
		pageB = jb(p.page)
	}
	if _, set := p.mf["checksum"]; !set {
		sum := [][]byte{projB}
		if !p.skipPage {
			sum = append(sum, pageB)
		}
		p.mf["checksum"] = sha256Hex(sum...)
	}
	entries := [][2]string{{"mimetype", Mimetype}, {"manifest.json", string(jb(p.mf))}}
	if !p.skipProject {
		entries = append(entries, [2]string{"project.json", string(projB)})
	}
	if !p.skipPage {
		entries = append(entries, [2]string{"pages/pa.json", string(pageB)})
	}
	return raw(t, append(entries, p.extra...)...)
}

func wantCode(t *testing.T, name string, b []byte, code string) {
	t.Helper()
	if _, err := ReadFile(b, Limits{}); !isCode(err, code) {
		t.Errorf("%s: err = %v, want %s", name, err, code)
	}
}

func TestReadFileMinimalOK(t *testing.T) {
	r, err := ReadFile(newParts().build(t), Limits{})
	if err != nil || r.ID != "f" || r.Uncompressed == 0 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestReadFileSchemaErrors(t *testing.T) {
	cases := map[string]struct {
		edit func(p *parts)
		code string
	}{
		"project type manifest":   {func(p *parts) { p.mf["type"] = "project" }, CodeUnsupported},
		"sddproj format":          {func(p *parts) { p.mf["format"] = "sddproj" }, CodeUnsupported},
		"newer format":            {func(p *parts) { p.mf["formatVersion"] = 2 }, CodeUnsupported},
		"manifest not object":     {nil, ""},
		"missing project":         {func(p *parts) { p.skipProject = true }, CodeMissingEntry},
		"project not object":      {func(p *parts) { p.projRaw = []byte(`[1]`) }, CodeSchema},
		"project newer schema":    {func(p *parts) { p.proj["schemaVersion"] = 99 }, CodeUnsupported},
		"project schema zero":     {func(p *parts) { p.proj["schemaVersion"] = 0 }, CodeUnsupported},
		"project id missing":      {func(p *parts) { delete(p.proj, "id") }, CodeSchema},
		"project id empty":        {func(p *parts) { p.proj["id"] = "" }, CodeSchema},
		"project name type":       {func(p *parts) { p.proj["name"] = 5 }, CodeSchema},
		"project createdAt":       {func(p *parts) { p.proj["createdAt"] = "x" }, CodeSchema},
		"project updatedAt":       {func(p *parts) { p.proj["updatedAt"] = "x" }, CodeSchema},
		"project active":          {func(p *parts) { delete(p.proj, "activePageId") }, CodeSchema},
		"project pageOrder empty": {func(p *parts) { p.proj["pageOrder"] = []string{} }, CodeSchema},
		"unsafe page path": {func(p *parts) {
			p.mf["pages"] = []any{obj{"id": "pa", "name": "A", "file": "../x.json"}}
		}, CodeUnsafePath},
		"missing page file": {func(p *parts) {
			p.mf["pages"] = []any{obj{"id": "pa", "name": "A", "file": "pages/zz.json"}}
		}, CodeMissingEntry},
		"checksum":       {func(p *parts) { p.mf["checksum"] = strings.Repeat("a", 64) }, CodeChecksum},
		"page bad json":  {func(p *parts) { p.pageRaw = []byte(`{`) }, CodeBadJSON},
		"page not obj":   {func(p *parts) { p.pageRaw = []byte(`[1]`) }, CodeSchema},
		"page schema":    {func(p *parts) { p.page["schemaVersion"] = 99 }, CodeUnsupported},
		"page id":        {func(p *parts) { delete(p.page, "id") }, CodeSchema},
		"page name":      {func(p *parts) { p.page["name"] = 5 }, CodeSchema},
		"page nodes":     {func(p *parts) { p.page["nodes"] = 5 }, CodeSchema},
		"page edges":     {func(p *parts) { delete(p.page, "edges") }, CodeSchema},
		"order unknown":  {func(p *parts) { p.proj["pageOrder"] = []string{"zz"} }, CodeSchema},
		"active unknown": {func(p *parts) { p.proj["activePageId"] = "zz" }, CodeSchema},
		"asset hash": {func(p *parts) {
			p.extra = [][2]string{{"assets/" + strings.Repeat("a", 64) + ".png", "data"}}
		}, CodeAsset},
		"asset type": {func(p *parts) {
			p.extra = [][2]string{{"assets/" + hashHex([]byte("data")) + ".bin", "data"}}
		}, CodeAsset},
	}
	for name, c := range cases {
		if c.edit == nil {
			continue
		}
		p := newParts()
		c.edit(p)
		wantCode(t, name, p.build(t), c.code)
	}
	// manifest root is not an object
	b := raw(t, [2]string{"mimetype", Mimetype}, [2]string{"manifest.json", `[1]`})
	wantCode(t, "manifest array", b, CodeSchema)
}

func TestParseManifestErrors(t *testing.T) {
	cases := map[string]func(p *parts){
		"format":        func(p *parts) { p.mf["format"] = "nope" },
		"formatVersion": func(p *parts) { p.mf["formatVersion"] = 1.5 },
		"fv zero":       func(p *parts) { p.mf["formatVersion"] = 0 },
		"appVersion":    func(p *parts) { p.mf["appVersion"] = 5 },
		"createdAt":     func(p *parts) { p.mf["createdAt"] = "x" },
		"updatedAt":     func(p *parts) { p.mf["updatedAt"] = "x" },
		"name":          func(p *parts) { p.mf["name"] = 5 },
		"projectId":     func(p *parts) { p.mf["projectId"] = "" },
		"checksum":      func(p *parts) { p.mf["checksum"] = "short" },
		"checksum type": func(p *parts) { p.mf["checksum"] = 5 },
		"pages":         func(p *parts) { p.mf["pages"] = []any{} },
		"page id":       func(p *parts) { p.mf["pages"] = []any{obj{"id": "", "file": "x"}} },
	}
	for name, edit := range cases {
		p := newParts()
		edit(p)
		wantCode(t, name, p.build(t), CodeSchema)
	}
}

func TestReadFileAssetsAndWarnings(t *testing.T) {
	jpg := []byte("jpegdata")
	h := hashHex(jpg)
	p := newParts()
	p.page["nodes"] = []any{
		obj{"id": "n", "data": obj{"label": "L", "iconId": "asset:" + strings.Repeat("0", 64)}},
		obj{"id": "m", "data": obj{"label": "M", "iconId": "builtin"}},
	}
	p.proj["settings"] = obj{"grid": true}
	p.extra = [][2]string{{"assets/" + h + ".JPG", string(jpg)}, {"thumbnails/pa.png", "png"}, {"thumbnails/other.txt", "x"}}
	r, err := ReadFile(p.build(t), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "missing") {
		t.Errorf("warnings %v", r.Warnings)
	}
	if string(r.Thumbnails["pa"]) != "png" {
		t.Errorf("thumbs %v", r.Thumbnails)
	}
	if !strings.Contains(string(r.Data), "image/jpeg") || !strings.Contains(string(r.Data), `"settings"`) {
		t.Errorf("data %s", r.Data)
	}
	// null settings are dropped
	p = newParts()
	p.proj["settings"] = nil
	r, err = ReadFile(p.build(t), Limits{})
	if err != nil || strings.Contains(string(r.Data), `"settings"`) {
		t.Errorf("null settings kept: %s %v", r.Data, err)
	}
}

func TestMissingAssetWarningsBadPage(t *testing.T) {
	if w := missingAssetWarnings(json.RawMessage(`[1]`), nil); w != nil {
		t.Fatalf("%v", w)
	}
}

func TestMigrateEdges(t *testing.T) {
	// v1 project that already has activePageId keeps it
	m, err := migrateProject(json.RawMessage(`{"schemaVersion":1,"activePageId":"a","activePage":"b"}`))
	if err != nil || string(m["activePageId"]) != `"a"` {
		t.Fatalf("%v %v", m, err)
	}
	// schemaVersion that is not a number counts as v1
	if _, err := migrateProject(json.RawMessage(`{"schemaVersion":"x"}`)); err != nil {
		t.Fatal(err)
	}
	// v1 page: icon migrates, existing iconId wins, bad data / bad nodes skipped
	pg, err := migratePage(json.RawMessage(`{"schemaVersion":1,"nodes":[{"data":{"icon":"a"}},{"data":{"icon":"b","iconId":"c"}},{"data":5},{}]}`), "p")
	if err != nil {
		t.Fatal(err)
	}
	s := string(pg["nodes"])
	if !strings.Contains(s, `"iconId":"a"`) || !strings.Contains(s, `"iconId":"c"`) || strings.Contains(s, `"icon":`) {
		t.Errorf("%s", s)
	}
	if _, err := migratePage(json.RawMessage(`{"schemaVersion":1,"nodes":5}`), "p"); err != nil {
		t.Error(err)
	}
	if _, err := migratePage(json.RawMessage(`5`), "p"); err == nil {
		t.Error("non-object page accepted")
	}
	if _, err := migratePage(json.RawMessage(`{"schemaVersion":0}`), "p"); err == nil {
		t.Error("v0 page accepted")
	}
}

func TestBuildFileErrors(t *testing.T) {
	for name, d := range map[string]string{
		"not object": `[1]`, "null": `null`, "no pages": `{"pages":[]}`, "bad pages": `{"pages":5}`,
		"bad asset": `{"pages":[{"id":"a","name":"A"}],"assets":{"h":{"mime":"image/png","data":"!!!"}}}`,
	} {
		if _, err := BuildFile(Doc{Data: json.RawMessage(d)}, nil); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// unknown mime -> .bin, null settings skipped, malformed assets ignored
	b, err := BuildFile(Doc{ID: "i", Data: json.RawMessage(`{"settings":null,"pages":[{"id":"a","name":"A"}],"assets":{"abc":{"mime":"x/y","data":"AAAA"}}}`)}, nil)
	if err != nil || !bytes.Contains(b, []byte("assets/abc.bin")) {
		t.Fatalf("%v", err)
	}
	if _, err := BuildFile(Doc{ID: "i", Data: json.RawMessage(`{"pages":[{"id":"a","name":"A"}],"assets":5}`)}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestErrorTypes(t *testing.T) {
	if (&Error{Msg: "m"}).Error() != "m" {
		t.Fatal("Error")
	}
	if e := ErrTextUnsupported(); e == nil || !strings.Contains(e.Error(), "text") {
		t.Fatal("ErrTextUnsupported")
	}
}

func TestUnpackExtras(t *testing.T) {
	// duplicate entries
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range []string{"mimetype", "mimetype"} {
		w, _ := zw.CreateHeader(&zip.FileHeader{Name: n, Method: zip.Store})
		_, _ = w.Write([]byte(Mimetype))
	}
	_ = zw.Close()
	wantCode(t, "dup entry", buf.Bytes(), CodeCorruptZip)

	// directory entries are skipped; size 22+ check
	buf.Reset()
	zw = zip.NewWriter(&buf)
	w, _ := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	_, _ = w.Write([]byte(Mimetype))
	_, _ = zw.CreateHeader(&zip.FileHeader{Name: "dir/", Method: zip.Store})
	_ = zw.Close()
	wantCode(t, "dir entry then missing manifest", buf.Bytes(), CodeMissingEntry)

	// a header that lies about the size
	good := newParts().build(t)
	lie := bytes.Replace(good, []byte{byte(len(Mimetype)), 0, 0, 0}, []byte{byte(len(Mimetype)) + 1, 0, 0, 0}, -1)
	if _, err := ReadFile(lie, Limits{}); err == nil {
		t.Error("lying header accepted")
	}
	// corrupt deflate data
	deflated := raw(t)
	_ = deflated
}

func TestSniffExtras(t *testing.T) {
	if _, err := Sniff(make([]byte, 20), "x", Limits{MaxFileBytes: 10}); !isCode(errOf(err), CodeTooLarge) {
		t.Errorf("too large: %v", err)
	}
	if _, err := Sniff([]byte("PK\x03\x04junkjunkjunkjunk"), "x", Limits{}); !isCode(errOf(err), CodeCorruptZip) {
		t.Errorf("corrupt: %v", err)
	}
	mk := func(manifest string) []byte {
		return raw(t, [2]string{"mimetype", Mimetype}, [2]string{"manifest.json", manifest})
	}
	cases := []struct {
		name string
		in   []byte
		want Sniffed
	}{
		{"sddproj", mk(`{"format":"sddproj"}`), Sniffed{KindProject, "sddproj"}},
		{"project", mk(`{"type":"project"}`), Sniffed{KindProject, ""}},
		{"no type", mk(`{"format":"sdd"}`), Sniffed{KindFile, "no-type"}},
		{"file", mk(`{"type":"file"}`), Sniffed{KindFile, ""}},
		{"bad manifest json", mk(`{`), Sniffed{KindFile, ""}},
		{"no manifest", raw(t, [2]string{"mimetype", Mimetype}), Sniffed{KindFile, ""}},
		{"text", []byte("\n#!sdd 1\nbody"), Sniffed{KindText, ""}},
		{"text crlf bom", []byte("\xef\xbb\xbf#!sdd\r\nbody"), Sniffed{KindText, ""}},
		{"sddt legacy", []byte("hello"), Sniffed{KindText, "sddt"}},
	}
	for _, c := range cases {
		name := "x.sdd"
		if c.name == "sddt legacy" {
			name = "A.SDDT"
		}
		got, err := Sniff(c.in, name, Limits{})
		if err != nil || got != c.want {
			t.Errorf("%s: %+v %v", c.name, got, err)
		}
	}
	for name, in := range map[string][]byte{"binary": {0, 1, 2, 3, 4}, "invalid utf8": {0xff, 0xfe}, "plain text": []byte("hello"), "empty sddt": []byte("  ")} {
		n := "x.txt"
		if name == "empty sddt" {
			n = "x.sddt"
		}
		if _, err := Sniff(in, n, Limits{}); !isCode(errOf(err), CodeUnsupported) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if s, ok := DecodeText([]byte("\xef\xbb\xbfhi")); !ok || s != "hi" {
		t.Error("DecodeText")
	}
	if _, ok := DecodeText([]byte{0}); ok {
		t.Error("DecodeText binary")
	}
}

func errOf(err error) error { return err }

// Project archives.

func fileSDD(t *testing.T) []byte {
	b, err := BuildFile(sampleDoc(t), map[string][]byte{"pg_b": png, "pg_a": png})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func projArchive(t *testing.T, mfEdit func(m obj), mime string, files map[string][]byte) []byte {
	t.Helper()
	var all []byte
	paths := []string{"files/fil_1.sdd"}
	if files == nil {
		files = map[string][]byte{"files/fil_1.sdd": fileSDD(t)}
	}
	for _, p := range paths {
		all = append(all, files[p]...)
	}
	mf := obj{"format": "sdd", "formatVersion": 1, "type": "project", "appVersion": "1", "createdAt": 1, "updatedAt": 2, "name": "P",
		"folders":  []any{obj{"id": "d1", "parentId": nil, "name": "D", "position": 0}},
		"files":    []any{obj{"id": "fil_1", "folderId": "d1", "name": "Shop", "kind": "sdd", "position": 0, "path": "files/fil_1.sdd"}},
		"checksum": sha256Hex(all)}
	if mfEdit != nil {
		mfEdit(mf)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(n string, d []byte) {
		w, _ := zw.CreateHeader(&zip.FileHeader{Name: n, Method: zip.Store})
		_, _ = w.Write(d)
	}
	add("mimetype", []byte(mime))
	add("manifest.json", jb(mf))
	for n, d := range files {
		add(n, d)
	}
	_ = zw.Close()
	return buf.Bytes()
}

func TestProjectReadOK(t *testing.T) {
	p, warns, err := ReadProject(projArchive(t, nil, Mimetype, nil), Limits{})
	if err != nil || len(p.Files) != 1 || len(warns) == 0 || len(p.Files[0].Thumbnail) == 0 {
		t.Fatalf("%+v %v %v", p, warns, err)
	}
	// legacy sddproj
	p, _, err = ReadProject(projArchive(t, func(m obj) { m["format"] = "sddproj"; delete(m, "type") }, LegacyProjMIME, nil), Limits{})
	if err != nil || len(p.Files) != 1 {
		t.Fatalf("legacy: %v", err)
	}
}

func TestProjectReadErrors(t *testing.T) {
	cases := map[string]struct {
		edit func(m obj)
		mime string
		code string
	}{
		"single file manifest": {func(m obj) { delete(m, "type"); m["pages"] = []any{1} }, Mimetype, CodeUnsupported},
		"format":               {func(m obj) { m["format"] = "x" }, Mimetype, CodeSchema},
		"formatVersion":        {func(m obj) { m["formatVersion"] = 0 }, Mimetype, CodeSchema},
		"appVersion":           {func(m obj) { m["appVersion"] = 1 }, Mimetype, CodeSchema},
		"createdAt":            {func(m obj) { m["createdAt"] = "x" }, Mimetype, CodeSchema},
		"updatedAt":            {func(m obj) { m["updatedAt"] = "x" }, Mimetype, CodeSchema},
		"name":                 {func(m obj) { m["name"] = 1 }, Mimetype, CodeSchema},
		"name long":            {func(m obj) { m["name"] = strings.Repeat("n", 801) }, Mimetype, CodeSchema},
		"checksum fmt":         {func(m obj) { m["checksum"] = "x" }, Mimetype, CodeSchema},
		"folders":              {func(m obj) { m["folders"] = 5 }, Mimetype, CodeSchema},
		"files":                {func(m obj) { m["files"] = 5 }, Mimetype, CodeSchema},
		"too many folders": {func(m obj) {
			f := make([]any, MaxProjectFolders+1)
			for i := range f {
				f[i] = obj{"id": fmt.Sprint(i), "name": "n"}
			}
			m["folders"] = f
		}, Mimetype, CodeSchema},
		"folder id":          {func(m obj) { m["folders"] = []any{obj{"id": "", "name": "n"}} }, Mimetype, CodeSchema},
		"folder parent id":   {func(m obj) { m["folders"] = []any{obj{"id": "a", "parentId": "", "name": "n"}} }, Mimetype, CodeSchema},
		"file id":            {func(m obj) { m["files"] = []any{obj{"id": "", "path": "files/a.sdd", "kind": "sdd"}} }, Mimetype, CodeSchema},
		"file path empty":    {func(m obj) { m["files"] = []any{obj{"id": "a", "path": "", "kind": "sdd"}} }, Mimetype, CodeSchema},
		"file folder id":     {func(m obj) { m["files"] = []any{obj{"id": "a", "folderId": "", "path": "p", "kind": "sdd"}} }, Mimetype, CodeSchema},
		"file kind":          {func(m obj) { m["files"] = []any{obj{"id": "a", "path": "files/a.sdd", "kind": "zzz"}} }, Mimetype, CodeSchema},
		"wrong mime":         {nil, "text/plain", CodeBadMimetype},
		"type not project":   {func(m obj) { m["type"] = "file" }, Mimetype, CodeSchema},
		"newer format":       {func(m obj) { m["formatVersion"] = 2 }, Mimetype, CodeUnsupported},
		"tree dup folder":    {func(m obj) { m["folders"] = []any{obj{"id": "a", "name": "n"}, obj{"id": "a", "name": "n"}} }, Mimetype, CodeBadTree},
		"unsafe path":        {func(m obj) { m["files"].([]any)[0].(obj)["path"] = "../x.sdd" }, Mimetype, CodeUnsafePath},
		"path outside files": {func(m obj) { m["files"].([]any)[0].(obj)["path"] = "x.sdd" }, Mimetype, CodeUnsafePath},
		"path not sdd":       {func(m obj) { m["files"].([]any)[0].(obj)["path"] = "files/x.txt" }, Mimetype, CodeUnsafePath},
		"missing file":       {func(m obj) { m["files"].([]any)[0].(obj)["path"] = "files/zz.sdd" }, Mimetype, CodeMissingEntry},
		"unsafe source":      {func(m obj) { m["files"].([]any)[0].(obj)["source"] = "files/x/other.sdd" }, Mimetype, CodeUnsafePath},
		"checksum":           {func(m obj) { m["checksum"] = strings.Repeat("a", 64) }, Mimetype, CodeChecksum},
	}
	for name, c := range cases {
		_, _, err := ReadProject(projArchive(t, c.edit, c.mime, nil), Limits{})
		if !isCode(err, c.code) {
			t.Errorf("%s: err = %v, want %s", name, err, c.code)
		}
	}
	// manifest not an object / not json / missing
	for _, mf := range []string{`[1]`, `{`} {
		b := raw(t, [2]string{"mimetype", Mimetype}, [2]string{"manifest.json", mf})
		if _, _, err := ReadProject(b, Limits{}); err == nil {
			t.Errorf("manifest %s accepted", mf)
		}
	}
	if _, _, err := ReadProject([]byte("junk"), Limits{}); err == nil {
		t.Error("junk accepted")
	}
	// a valid source path is allowed
	if _, _, err := ReadProject(projArchive(t, func(m obj) { m["files"].([]any)[0].(obj)["source"] = "files/fil_1/source.sdd" }, Mimetype, nil), Limits{}); err != nil {
		t.Errorf("source path: %v", err)
	}
}

func TestProjectNestedFileErrors(t *testing.T) {
	// the inner archive is damaged: error carries the file path
	bad := map[string][]byte{"files/fil_1.sdd": []byte("not a zip at all, long enough to pass")}
	_, _, err := ReadProject(projArchive(t, nil, Mimetype, bad), Limits{})
	var e *Error
	if !isCode(err, CodeCorruptZip) || !asErr(err, &e) || e.Entry != "files/fil_1.sdd" {
		t.Fatalf("%v", err)
	}
	// budget exhausted: outer archive eats the whole allowance
	arch := projArchive(t, nil, Mimetype, nil)
	zr, _ := zip.NewReader(bytes.NewReader(arch), int64(len(arch)))
	var total int64
	for _, f := range zr.File {
		total += int64(f.UncompressedSize64)
	}
	if _, _, err := ReadProject(arch, Limits{MaxUncompressedBytes: total}); !isCode(err, CodeZipBomb) {
		t.Errorf("budget zero: %v", err)
	}
	if _, _, err := ReadProject(arch, Limits{MaxUncompressedBytes: total + 10}); !isCode(err, CodeZipBomb) {
		t.Errorf("budget exceeded: %v", err)
	}
}

func asErr(err error, target **Error) bool {
	e, ok := err.(*Error)
	if ok {
		*target = e
	}
	return ok
}

func TestProjectThumbnailFallbackAndBuild(t *testing.T) {
	// only a thumbnail of a non-active page: it is used anyway
	d := sampleDoc(t)
	inner, _ := BuildFile(d, map[string][]byte{"pg_a": png})
	p, _, err := ReadProject(projArchive(t, nil, Mimetype, map[string][]byte{"files/fil_1.sdd": inner}), Limits{})
	if err != nil || len(p.Files[0].Thumbnail) == 0 {
		t.Fatalf("%v", err)
	}
	// BuildProject with nil folders and a thumbnail, then read back
	b, err := BuildProject(Project{Name: "P", Files: []File{{Doc: d, Kind: "sdd", Thumbnail: png}}})
	if err != nil {
		t.Fatal(err)
	}
	if got, _, err := ReadProject(b, Limits{}); err != nil || len(got.Files) != 1 || len(got.Folders) != 0 {
		t.Fatalf("%v", err)
	}
	// a file that cannot be built names itself
	_, err = BuildProject(Project{Files: []File{{Doc: Doc{Name: "Broken", Data: json.RawMessage(`[1]`)}}}})
	if err == nil || !strings.Contains(err.Error(), "Broken") {
		t.Fatalf("%v", err)
	}
}

func TestValidateTreeDeep(t *testing.T) {
	s := func(v string) *string { return &v }
	var chain []Folder
	for i := 0; i <= MaxFolderDepth+1; i++ {
		f := Folder{ID: fmt.Sprint(i), Name: "n"}
		if i > 0 {
			f.ParentID = s(fmt.Sprint(i - 1))
		}
		chain = append(chain, f)
	}
	if err := ValidateTree(chain, nil); !isCode(err, CodeBadTree) {
		t.Errorf("deep: %v", err)
	}
	cycle := []Folder{{ID: "a", ParentID: s("b")}, {ID: "b", ParentID: s("a")}}
	if err := ValidateTree(cycle, nil); !isCode(err, CodeBadTree) {
		t.Errorf("cycle: %v", err)
	}
	if err := ValidateTree([]Folder{{ID: "a", ParentID: s("zz")}}, nil); !isCode(err, CodeBadTree) {
		t.Errorf("unknown parent: %v", err)
	}
	if err := ValidateTree(nil, []projManifestFile{{ID: "x"}, {ID: "x"}}); !isCode(err, CodeBadTree) {
		t.Errorf("dup file: %v", err)
	}
	if err := ValidateTree(nil, []projManifestFile{{ID: "x", FolderID: s("zz")}}); !isCode(err, CodeBadTree) {
		t.Errorf("unknown folder: %v", err)
	}
	if err := ValidateTree([]Folder{{ID: "a"}}, []projManifestFile{{ID: "x", FolderID: s("a")}}); err != nil {
		t.Errorf("valid: %v", err)
	}
}

// rawEntry writes an entry as given, without compressing or checking it.
func rawZip(t *testing.T, entries ...struct {
	h    zip.FileHeader
	data []byte
}) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := e.h
		w, err := zw.CreateRaw(&h)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(e.data)
	}
	_ = zw.Close()
	return buf.Bytes()
}

type rawE = struct {
	h    zip.FileHeader
	data []byte
}

func TestUnpackBrokenEntries(t *testing.T) {
	mime := rawE{zip.FileHeader{Name: "mimetype", Method: zip.Store, CompressedSize64: uint64(len(Mimetype)), UncompressedSize64: uint64(len(Mimetype)), CRC32: crc(Mimetype)}, []byte(Mimetype)}
	// unsupported compression method
	odd := rawE{zip.FileHeader{Name: "manifest.json", Method: 99, CompressedSize64: 2, UncompressedSize64: 2}, []byte("{}")}
	wantCode(t, "unsupported method", rawZip(t, mime, odd), CodeCorruptZip)
	// garbage deflate stream
	var fb bytes.Buffer
	fw, _ := flate.NewWriter(&fb, flate.BestCompression)
	_, _ = fw.Write(bytes.Repeat([]byte("abcdefghij"), 200))
	_ = fw.Close()
	cut := fb.Bytes()[:fb.Len()/2]
	trunc := rawE{zip.FileHeader{Name: "manifest.json", Method: zip.Deflate, CompressedSize64: uint64(len(cut)), UncompressedSize64: 2000}, cut}
	wantCode(t, "truncated deflate", rawZip(t, mime, trunc), CodeCorruptZip)
	// a header that claims fewer bytes than the stream holds
	full := fb.Bytes()
	liar := rawE{zip.FileHeader{Name: "manifest.json", Method: zip.Deflate, CompressedSize64: uint64(len(full)), UncompressedSize64: 1000}, full}
	wantCode(t, "size lie", rawZip(t, mime, liar), CodeCorruptZip)
	// stored entry with a wrong CRC
	badCRC := rawE{zip.FileHeader{Name: "manifest.json", Method: zip.Store, CompressedSize64: 2, UncompressedSize64: 2, CRC32: 1}, []byte("{}")}
	wantCode(t, "bad crc", rawZip(t, mime, badCRC), CodeCorruptZip)

	// Sniff: manifest over 5 MB is not read; unreadable manifest is not read
	big := rawE{zip.FileHeader{Name: "manifest.json", Method: zip.Store, CompressedSize64: 5<<20 + 1, UncompressedSize64: 5<<20 + 1}, make([]byte, 5<<20+1)}
	if s, err := Sniff(rawZip(t, mime, big), "x", Limits{}); err != nil || s.Kind != KindFile {
		t.Errorf("big manifest: %+v %v", s, err)
	}
	if s, err := Sniff(rawZip(t, mime, odd), "x", Limits{}); err != nil || s.Kind != KindFile {
		t.Errorf("odd manifest: %+v %v", s, err)
	}
}

func crc(s string) uint32 { return crc32.ChecksumIEEE([]byte(s)) }
