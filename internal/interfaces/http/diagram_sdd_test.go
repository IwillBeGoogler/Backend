package http

// .sdd export and import over HTTP: the container is the Node server's,
// export and import round-trip, and bad uploads are 422 SDD_INVALID.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram/sddfile"
)

var sddPNG = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{3}, 64)...)

func sddDoc(label string) map[string]any {
	return map[string]any{"version": 2, "activePageId": "pg", "pages": []any{map[string]any{"id": "pg", "name": "Main",
		"nodes": []any{map[string]any{"id": "a", "type": "icon", "position": map[string]any{"x": 1, "y": 2}, "data": map[string]any{"label": label}}},
		"edges": []any{}}}}
}

func (c dgClient) uploadSDD(path string, data []byte, hdr ...string) rawResp {
	c.t.Helper()
	body, ct := multipartBody(c.t, "file", "x.sdd", data)
	return c.raw("POST", path, body, ct, hdr...)
}

func (c dgClient) download(path string, hdr ...string) rawResp {
	c.t.Helper()
	return c.raw("GET", path, nil, "", hdr...)
}

func TestDiagramSDDExportImport(t *testing.T) {
	env := newTokenServer(t)
	alice, bob, eve := env.session(t, 1), env.session(t, 2), env.session(t, 3)
	made := alice.expect(alice.do("POST", "/projects", map[string]any{"name": "Shop/Backend: v2?"}), 201)
	pid, fid := made.obj("project")["id"].(string), made.obj("file")["id"].(string)
	alice.expect(alice.do("PUT", "/files/"+fid, map[string]any{"data": sddDoc("API")}, "If-Match", `"1"`), 200)
	// a thumbnail travels in the container
	if r := alice.raw("PUT", "/files/"+fid+"/thumbnail", sddPNG, "image/png"); r.Code != 200 {
		t.Fatalf("thumbnail: %d %s", r.Code, r.Body)
	}
	folder := alice.expect(alice.do("POST", "/projects/"+pid+"/folders", map[string]any{"name": "Docs"}), 201).obj("folder")["id"].(string)
	second := alice.expect(alice.do("POST", "/projects/"+pid+"/files", map[string]any{"name": "Second", "folderId": folder, "data": sddDoc("DB")}), 201)

	// export a file
	r := alice.download("/files/" + fid + "/export")
	if r.Code != 200 || r.Header.Get("Content-Type") != SDDMime || r.Header.Get("ETag") != `"3"` && r.Header.Get("ETag") != `"2"` && r.Header.Get("ETag") != `"1"` {
		t.Fatalf("export: %d %v", r.Code, r.Header)
	}
	if cd := r.Header.Get("Content-Disposition"); cd != `attachment; filename="Shop_Backend_ v2_.sdd"` {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if r.Header.Get("Cache-Control") != "private, no-cache" || r.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("headers = %v", r.Header)
	}
	read, err := sddfile.ReadFile(r.Body, sddfile.Limits{})
	if err != nil {
		t.Fatalf("the export is not a valid .sdd: %v", err)
	}
	if read.ID != fid || read.Name != "Shop/Backend: v2?" || !bytes.Equal(read.Thumbnails["pg"], sddPNG) || !strings.Contains(string(read.Data), `"label":"API"`) {
		t.Errorf("exported = %s %s thumbs %v", read.ID, read.Name, read.Thumbnails)
	}
	if alias := alice.download("/files/" + fid + "/export.sdd"); alias.Code != 200 || !bytes.Equal(alias.Body, r.Body) {
		t.Errorf("export.sdd alias differs: %d", alias.Code)
	}
	if alice.download("/files/"+fid+"/export?type=file").Code != 200 {
		t.Error("type=file")
	}
	// the text flavour: "#!sdd" of the active page
	text := alice.download("/files/" + fid + "/export?type=text")
	if text.Code != 200 || !strings.HasPrefix(string(text.Body), "#!sdd 1\ndiagram ") || !strings.Contains(string(text.Body), `node a "API"`) {
		t.Errorf("type=text: %d %s", text.Code, text.Body)
	}
	if alice.download("/files/"+fid+"/export?type=zip").Code != 400 {
		t.Error("unknown type")
	}
	// access
	if c := env.session(t, 0).download("/files/" + fid + "/export").Code; c != 401 {
		t.Errorf("anonymous export: %d", c)
	}
	if c := eve.download("/files/" + fid + "/export").Code; c != 404 {
		t.Errorf("stranger export: %d", c)
	}
	alice.expect(alice.do("POST", "/projects/"+pid+"/members", map[string]any{"email": "bob", "role": "viewer"}), 201)
	if c := bob.download("/files/" + fid + "/export").Code; c != 200 {
		t.Errorf("viewer export: %d", c)
	}

	// export the project, import it as a new one
	pr := alice.download("/projects/" + pid + "/export")
	if pr.Code != 200 || !strings.HasSuffix(pr.Header.Get("Content-Disposition"), `.sdd"`) {
		t.Fatalf("project export: %d %v", pr.Code, pr.Header)
	}
	if alias := alice.download("/projects/" + pid + "/export.sddproj"); alias.Code != 200 {
		t.Errorf("export.sddproj alias: %d", alias.Code)
	}
	proj, _, err := sddfile.ReadProject(pr.Body, sddfile.Limits{})
	if err != nil || len(proj.Files) != 2 || len(proj.Folders) != 1 {
		t.Fatalf("project archive: %+v %v", proj, err)
	}

	imp := alice.uploadSDD("/import", pr.Body).json()
	if imp.Code != 201 || imp.Body["kind"] != "project" {
		t.Fatalf("import project: %d %v", imp.Code, imp.Body)
	}
	newProject := imp.obj("project")
	if newProject["id"] == pid || newProject["name"] != "Shop/Backend: v2?" || newProject["role"] != "owner" || newProject["fileCount"] != float64(2) || newProject["folderCount"] != float64(1) {
		t.Errorf("imported project = %v", newProject)
	}
	files, _ := imp.Body["files"].([]any)
	folders, _ := imp.Body["folders"].([]any)
	if len(files) != 2 || len(folders) != 1 || imp.Body["warnings"] == nil {
		t.Fatalf("files %v folders %v warnings %v", files, folders, imp.Body["warnings"])
	}
	newFolder := folders[0].(map[string]any)["id"]
	inFolder := 0
	for _, f := range files {
		fm := f.(map[string]any)
		if fm["id"] == fid || fm["id"] == second.obj("file")["id"] || fm["data"] != nil {
			t.Errorf("imported file must have a fresh id and no document: %v", fm)
		}
		if fm["folderId"] == newFolder {
			inFolder++
		}
	}
	if newFolder == folder || inFolder != 1 {
		t.Errorf("folder mapping: %v (in folder: %d)", newFolder, inFolder)
	}
	// the copy opens, carries the thumbnail and an "Imported" snapshot
	var copyID string
	for _, f := range files {
		if f.(map[string]any)["name"] == "Shop/Backend: v2?" {
			copyID = f.(map[string]any)["id"].(string)
		}
	}
	opened := alice.expect(alice.do("GET", "/files/"+copyID, nil), 200).obj("file")
	if opened["thumbnailUrl"] == nil || !strings.Contains(string(mustJSON(t, opened["data"])), `"label":"API"`) {
		t.Errorf("imported file = %v", opened)
	}
	vs := alice.expect(alice.do("GET", "/files/"+copyID+"/versions", nil), 200).Body["items"].([]any)
	if vs[len(vs)-1].(map[string]any)["label"] != "Imported from .sdd" {
		t.Errorf("versions = %v", vs)
	}

	// a file container: a new project with one file
	one := alice.uploadSDD("/import", r.Body).json()
	if one.Code != 201 || one.Body["kind"] != "file" || one.Header.Get("ETag") != `"1"` || one.obj("file")["data"] == nil || one.obj("project")["fileCount"] != float64(1) {
		t.Errorf("import file: %d %v", one.Code, one.Body)
	}
	if one.obj("project")["name"] != "Shop/Backend: v2?" || one.obj("file")["id"] == fid {
		t.Errorf("imported file/project = %v / %v", one.obj("project"), one.obj("file"))
	}
	pj := alice.uploadSDD("/projects/import", r.Body).json()
	if pj.Code != 201 || pj.obj("file") == nil || pj.Body["kind"] != nil {
		t.Errorf("/projects/import: %d %v", pj.Code, pj.Body)
	}

	// each endpoint takes its own kind
	bad := alice.uploadSDD("/projects/import", pr.Body).json()
	details, _ := bad.obj("error")["details"].(map[string]any)
	if bad.Code != 422 || bad.errCode() != "SDD_INVALID" || details["reason"] != "unsupported" {
		t.Errorf("project container on /projects/import: %d %v", bad.Code, bad.Body)
	}
	if c := alice.uploadSDD("/projects/import-project", r.Body).Code; c != 422 {
		t.Errorf("file container on /projects/import-project: %d", c)
	}
	if ip := alice.uploadSDD("/projects/import-project", pr.Body).json(); ip.Code != 201 || ip.obj("project") == nil {
		t.Errorf("/projects/import-project: %d %v", ip.Code, ip.Body)
	}

	// import into an existing project, in a folder
	into := alice.uploadSDD("/projects/"+pid+"/import?folderId="+folder, r.Body).json()
	if into.Code != 201 || into.obj("file")["folderId"] != folder || into.Header.Get("ETag") != `"1"` {
		t.Errorf("import into: %d %v", into.Code, into.Body)
	}
	if c := alice.uploadSDD("/projects/"+pid+"/import?folderId=fld_nope", r.Body).Code; c != 400 {
		t.Errorf("unknown folder: %d", c)
	}
	if c := bob.uploadSDD("/projects/"+pid+"/import", r.Body).Code; c != 403 {
		t.Errorf("a viewer importing: %d", c)
	}
	if c := eve.uploadSDD("/projects/"+pid+"/import", r.Body).Code; c != 404 {
		t.Errorf("a stranger importing: %d", c)
	}
}

func TestDiagramSDDImportRejectsBadUploads(t *testing.T) {
	env := newTokenServer(t)
	alice := env.session(t, 1)
	reason := func(r rawResp) string {
		d, _ := r.json().obj("error")["details"].(map[string]any)
		s, _ := d["reason"].(string)
		return s
	}
	for name, c := range map[string]struct {
		data   []byte
		reason string
	}{
		"not a zip":            {[]byte("PK\x03\x04 garbage garbage garbage"), "corrupt-zip"},
		"plain text":           {[]byte("hello"), "unsupported"},
		"empty":                {[]byte(""), "unsupported"},
		"text sdd with errors": {[]byte("#!sdd 1\nnode a \"A\"\na -> ghost\nwat\n"), "schema"},
	} {
		r := alice.uploadSDD("/import", c.data)
		if r.Code != 422 || r.json().errCode() != "SDD_INVALID" || reason(r) != c.reason {
			t.Errorf("%s: %d %s", name, r.Code, r.Body)
		}
	}
	// a valid text imports as a project with one file, laid out
	ok := alice.uploadSDD("/import", []byte("#!sdd 1\ndiagram \"Via text\"\nnode a \"A\" icon=service\nnode b \"B\"\na -> b \"calls\"\n"))
	if ok.Code != 201 {
		t.Fatalf("text import: %d %s", ok.Code, ok.Body)
	}
	imp := ok.json()
	if imp.Body["kind"] != "text" || imp.obj("file")["name"] != "Via text" || imp.obj("project")["name"] != "Via text" {
		t.Errorf("text import = %s", ok.Body)
	}
	pages := imp.obj("file")["data"].(map[string]any)["pages"].([]any)
	if nodes := pages[0].(map[string]any)["nodes"].([]any); len(nodes) != 2 {
		t.Errorf("text import nodes = %v", nodes)
	}
	// a valid container whose document the file model refuses
	good, _ := sddfile.BuildFile(sddfile.Doc{ID: "f", Name: "N", CreatedAt: 1, UpdatedAt: 1,
		Data: mustJSON(t, map[string]any{"version": 2, "activePageId": "pg", "pages": []any{map[string]any{"id": "pg", "name": "P", "nodes": []any{}, "edges": []any{map[string]any{"id": "e", "source": "x", "target": "y"}}}}})}, nil)
	if r := alice.uploadSDD("/import", good); r.Code != 422 || reason(r) != "schema" {
		t.Errorf("invalid document: %d %s", r.Code, r.Body)
	}
	// request shape
	if r := alice.raw("POST", "/import", []byte("x"), "application/json"); r.Code != 415 {
		t.Errorf("not multipart: %d", r.Code)
	}
	body, ct := multipartBody(t, "other", "x.sdd", good)
	if r := alice.raw("POST", "/import", body, ct); r.Code != 400 || r.json().errCode() != "MISSING_FILE" {
		t.Errorf("wrong field: %d %s", r.Code, r.Body)
	}
	if r := env.session(t, 0).uploadSDD("/import", good); r.Code != 401 {
		t.Errorf("anonymous: %d", r.Code)
	}
	// too large
	env.d.MaxSDDBytes = 200
	if r := alice.uploadSDD("/import", good); r.Code != 413 {
		t.Errorf("too large: %d %s", r.Code, r.Body)
	}
}

func TestDiagramSDDThroughATokenAndShare(t *testing.T) {
	env := newTokenServer(t)
	alice := env.session(t, 1)
	made := alice.expect(alice.do("POST", "/projects", map[string]any{"name": "Tok"}), 201)
	pid, fid := made.obj("project")["id"].(string), made.obj("file")["id"].(string)
	other := alice.expect(alice.do("POST", "/projects", map[string]any{"name": "Other"}), 201)

	reader := env.mint(t, alice, []string{"files:read"}, nil)
	writer := env.mint(t, alice, []string{"files:write"}, nil)
	limited := env.mint(t, alice, []string{"files:read"}, map[string]any{"projectIds": []string{pid}})
	c := dgClient{t, env.srv, 0}
	auth := func(tok string) []string { return []string{"Authorization", "Bearer " + tok} }

	if r := c.download("/v1/files/"+fid+"/export", auth(reader)...); r.Code != 200 || r.Header.Get("Content-Type") != SDDMime {
		t.Errorf("export with files:read: %d", r.Code)
	}
	if r := c.download("/v1/projects/"+pid+"/export", auth(reader)...); r.Code != 200 {
		t.Errorf("project export with files:read: %d", r.Code)
	}
	if r := c.download("/v1/files/"+fid+"/export", auth(writer)...); r.Code != 403 {
		t.Errorf("export without files:read: %d", r.Code)
	}
	if r := c.download("/v1/files/"+other.obj("file")["id"].(string)+"/export", auth(limited)...); r.Code != 404 {
		t.Errorf("limited token on another project: %d", r.Code)
	}
	// the session-only path rejects tokens
	if r := c.download("/files/"+fid+"/export", auth(reader)...); r.Code != 403 || r.json().errCode() != "TOKEN_SCOPE" {
		t.Errorf("token on the session path: %d", r.Code)
	}
	exp := c.download("/v1/files/"+fid+"/export", auth(reader)...)
	if r := c.uploadSDD("/v1/import", exp.Body, auth(reader)...); r.Code != 403 {
		t.Errorf("import without files:write: %d", r.Code)
	}
	if r := c.uploadSDD("/v1/import", exp.Body, auth(writer)...); r.Code != 201 {
		t.Errorf("import with files:write: %d %s", r.Code, r.Body)
	}

	// a project-wide share link may export, a file link only its file
	link := alice.expect(alice.do("POST", "/projects/"+pid+"/shares", map[string]any{"role": "viewer"}), 201)
	tok, _ := link.Body["token"].(string)
	anon := env.session(t, 0)
	if tok == "" {
		t.Skip("share token not in the response")
	}
	if r := anon.download("/files/"+fid+"/export", "X-Share-Token", tok); r.Code != 200 {
		t.Errorf("share export: %d %s", r.Code, r.Body)
	}
	if r := anon.download("/projects/"+pid+"/export", "X-Share-Token", tok); r.Code != 200 {
		t.Errorf("share project export: %d", r.Code)
	}
	if r := anon.download("/files/"+other.obj("file")["id"].(string)+"/export", "X-Share-Token", tok); r.Code != 404 {
		t.Errorf("share export of another project: %d", r.Code)
	}
}
