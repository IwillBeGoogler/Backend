// Package sddfile reads and writes the .sdd container of the Node diagram
// server (docs/sdd-format.md): a ZIP with, in this order, mimetype (stored)
// / manifest.json / project.json / pages/<id>.json / assets/<sha256>.<ext>
// / thumbnails/<pageId>.png, and the "project" flavour that holds one such
// archive per file. Reading is defensive: entry names, counts and sizes are
// bounded before anything is unpacked, declared sizes must be true, and the
// checksum and asset hashes must match.
//
// The diagrams-as-code text format ("#!sdd") needs the diagram language
// parser and printer of the TypeScript core, which this server does not
// have: text files are recognized (Sniff) but not read, and no source.sdd is
// written (readers treat it as optional and informational).
package sddfile

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Container constants.
const (
	Mimetype       = "application/vnd.sdd+zip"
	LegacyProjMIME = "application/vnd.sddproj+zip"
	FormatVersion  = 1
	SchemaVersion  = 8 // CURRENT_SCHEMA of the Node server's project schema
	AppVersion     = "0.1.0"

	MaxProjectFiles   = 500
	MaxProjectFolders = 1000
	MaxFolderDepth    = 64
)

// Error codes, as in the Node server's SddError.
const (
	CodeTooLarge     = "too-large"
	CodeCorruptZip   = "corrupt-zip"
	CodeZipBomb      = "zip-bomb"
	CodeTooManyEntry = "too-many-entries"
	CodeUnsafePath   = "unsafe-path"
	CodeBadMimetype  = "bad-mimetype"
	CodeMissingEntry = "missing-entry"
	CodeBadJSON      = "bad-json"
	CodeSchema       = "schema"
	CodeChecksum     = "checksum"
	CodeAsset        = "asset"
	CodeUnsupported  = "unsupported"
	CodeBadTree      = "bad-tree"
)

// Error is a file that cannot be read, with the failing entry.
type Error struct {
	Code  string
	Msg   string
	Entry string
}

func (e *Error) Error() string { return e.Msg }

func fail(code, entry, format string, a ...any) *Error {
	return &Error{Code: code, Entry: entry, Msg: fmt.Sprintf(format, a...)}
}

// Limits bound what is unpacked. Zero fields take the defaults (100 MB
// file, 300 MB unpacked, 2000 entries).
type Limits struct {
	MaxFileBytes         int64
	MaxUncompressedBytes int64
	MaxEntries           int
}

func (l Limits) withDefaults() Limits {
	if l.MaxFileBytes <= 0 {
		l.MaxFileBytes = 100 << 20
	}
	if l.MaxUncompressedBytes <= 0 {
		l.MaxUncompressedBytes = 300 << 20
	}
	if l.MaxEntries <= 0 {
		l.MaxEntries = 2000
	}
	return l
}

var mimeByExt = map[string]string{"png": "image/png", "jpg": "image/jpeg", "jpeg": "image/jpeg", "webp": "image/webp", "gif": "image/gif", "svg": "image/svg+xml"}
var extByMime = map[string]string{"image/png": "png", "image/jpeg": "jpg", "image/webp": "webp", "image/gif": "gif", "image/svg+xml": "svg"}

// Doc is a document with its identity, as one .sdd file holds it. Data is
// the project document (version 2): pages, activePageId, settings, assets.
type Doc struct {
	ID        string
	Name      string
	CreatedAt int64 // epoch ms
	UpdatedAt int64
	Data      json.RawMessage
}

func sha256Hex(parts ...[]byte) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write(p)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func indented(v any) []byte {
	b, _ := json.MarshalIndent(v, "", "  ")
	return b
}

type zipFile struct {
	name   string
	data   []byte
	stored bool
}

func writeZip(files []zipFile) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		h := &zip.FileHeader{Name: f.name, Method: zip.Deflate}
		if f.stored {
			h.Method = zip.Store
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(f.data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type pageRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	File string `json:"file"`
}

type manifest struct {
	Format        string    `json:"format"`
	FormatVersion int       `json:"formatVersion"`
	Type          string    `json:"type,omitempty"`
	AppVersion    string    `json:"appVersion"`
	CreatedAt     int64     `json:"createdAt"`
	UpdatedAt     int64     `json:"updatedAt"`
	ProjectID     string    `json:"projectId"`
	Name          string    `json:"name"`
	Pages         []pageRef `json:"pages"`
	Checksum      string    `json:"checksum"`
}

func rawString(m map[string]json.RawMessage, k string) string {
	var s string
	_ = json.Unmarshal(m[k], &s)
	return s
}

// BuildFile writes one document as a type "file" archive. thumbnails are
// PNG previews by page id.
func BuildFile(d Doc, thumbnails map[string][]byte) ([]byte, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(d.Data, &doc); err != nil || doc == nil {
		return nil, fmt.Errorf("sddfile: the document is not an object")
	}
	var pages []map[string]json.RawMessage
	if err := json.Unmarshal(doc["pages"], &pages); err != nil || len(pages) == 0 {
		return nil, fmt.Errorf("sddfile: the document has no pages")
	}
	type pf struct {
		id, name, path string
		bytes          []byte
	}
	var pfs []pf
	order := make([]string, 0, len(pages))
	for _, p := range pages {
		p["schemaVersion"] = json.RawMessage(fmt.Sprint(SchemaVersion))
		id := rawString(p, "id")
		order = append(order, id)
		b, err := marshalSorted(p)
		if err != nil {
			return nil, err
		}
		pfs = append(pfs, pf{id: id, name: rawString(p, "name"), path: "pages/" + id + ".json", bytes: b})
	}
	proj := map[string]any{
		"schemaVersion": SchemaVersion, "id": d.ID, "name": d.Name, "createdAt": d.CreatedAt, "updatedAt": d.UpdatedAt,
		"activePageId": rawString(doc, "activePageId"), "pageOrder": order,
	}
	if s, ok := doc["settings"]; ok && string(s) != "null" {
		proj["settings"] = s
	}
	projBytes := indented(proj)
	sumParts := [][]byte{projBytes}
	refs := make([]pageRef, len(pfs))
	for i, f := range pfs {
		sumParts = append(sumParts, f.bytes)
		refs[i] = pageRef{ID: f.id, Name: f.name, File: f.path}
	}
	mf := manifest{Format: "sdd", FormatVersion: FormatVersion, Type: "file", AppVersion: AppVersion, CreatedAt: d.CreatedAt,
		UpdatedAt: d.UpdatedAt, ProjectID: d.ID, Name: d.Name, Pages: refs, Checksum: sha256Hex(sumParts...)}

	files := []zipFile{{"mimetype", []byte(Mimetype), true}, {"manifest.json", indented(mf), false}, {"project.json", projBytes, false}}
	for _, f := range pfs {
		files = append(files, zipFile{f.path, f.bytes, false})
	}
	if raw, ok := doc["assets"]; ok {
		var assets map[string]struct {
			Mime string `json:"mime"`
			Data string `json:"data"`
		}
		if json.Unmarshal(raw, &assets) == nil {
			hashes := make([]string, 0, len(assets))
			for h := range assets {
				hashes = append(hashes, h)
			}
			sort.Strings(hashes)
			for _, h := range hashes {
				a := assets[h]
				ext := extByMime[a.Mime]
				if ext == "" {
					ext = "bin"
				}
				data, err := base64.StdEncoding.DecodeString(a.Data)
				if err != nil {
					return nil, fmt.Errorf("sddfile: asset %s is not base64", h)
				}
				files = append(files, zipFile{"assets/" + h + "." + ext, data, ext != "svg"})
			}
		}
	}
	ids := make([]string, 0, len(thumbnails))
	for id := range thumbnails {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		files = append(files, zipFile{"thumbnails/" + id + ".png", thumbnails[id], true})
	}
	return writeZip(files)
}

// marshalSorted encodes a JSON object with its keys sorted, values kept
// byte for byte.
func marshalSorted(m map[string]json.RawMessage) ([]byte, error) {
	b, err := json.MarshalIndent(m, "", "  ")
	return b, err
}

// unpacked is an archive's entries, checked.
type unpacked struct {
	files map[string][]byte
	order []string
	total int64
}

var (
	unsafeSeg = regexp.MustCompile(`(^|/)\.\.(/|$)`)
	hashRe    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	assetRe   = regexp.MustCompile(`^assets/([0-9a-f]{64})\.(\w+)$`)
	thumbRe   = regexp.MustCompile(`^thumbnails/([^/]+)\.png$`)
)

// SafeName reports whether an entry name stays inside the archive.
func SafeName(n string) bool {
	return n != "" && !strings.HasPrefix(n, "/") && !unsafeSeg.MatchString(n) && !strings.ContainsAny(n, "\\\x00:")
}

func unpack(b []byte, lim Limits) (*unpacked, error) {
	if int64(len(b)) > lim.MaxFileBytes {
		return nil, fail(CodeTooLarge, "", "File is too large (limit %d MB)", lim.MaxFileBytes>>20)
	}
	if len(b) < 22 {
		return nil, fail(CodeCorruptZip, "", "File is not a valid .sdd archive")
	}
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return nil, fail(CodeCorruptZip, "", "File is not a valid .sdd archive (corrupt or truncated)")
	}
	if len(zr.File) > lim.MaxEntries {
		return nil, fail(CodeTooManyEntry, "", "Archive has more than %d entries", lim.MaxEntries)
	}
	u := &unpacked{files: map[string][]byte{}}
	var declared int64
	for _, f := range zr.File { // check every header before unpacking any entry
		if !SafeName(f.Name) {
			return nil, fail(CodeUnsafePath, f.Name, "Unsafe entry name %q (path traversal)", f.Name)
		}
		declared += int64(f.UncompressedSize64)
		if declared > lim.MaxUncompressedBytes || declared < 0 {
			return nil, fail(CodeZipBomb, "", "Archive expands beyond the allowed size")
		}
		if f.Name == "mimetype" && f.Method != zip.Store {
			return nil, fail(CodeBadMimetype, "mimetype", "mimetype entry must be stored uncompressed")
		}
	}
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "/") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fail(CodeCorruptZip, f.Name, "File is not a valid .sdd archive (corrupt or truncated)")
		}
		data, err := io.ReadAll(io.LimitReader(rc, int64(f.UncompressedSize64)+1))
		rc.Close()
		if err != nil {
			return nil, fail(CodeCorruptZip, f.Name, "File is not a valid .sdd archive (corrupt or truncated)")
		}
		// a lying header (claimed size != real size) is corruption or a bomb attempt
		if uint64(len(data)) != f.UncompressedSize64 {
			return nil, fail(CodeZipBomb, f.Name, "Entry %s does not match its declared size", f.Name)
		}
		if _, dup := u.files[f.Name]; dup {
			return nil, fail(CodeCorruptZip, f.Name, "Entry %s appears twice", f.Name)
		}
		u.files[f.Name] = data
		u.order = append(u.order, f.Name)
		u.total += int64(len(data))
	}
	if len(u.order) == 0 || u.order[0] != "mimetype" {
		return nil, fail(CodeBadMimetype, "mimetype", "Not an .sdd file (missing or wrong mimetype entry)")
	}
	return u, nil
}

func (u *unpacked) json(entry string) (json.RawMessage, error) {
	raw, ok := u.files[entry]
	if !ok {
		return nil, fail(CodeMissingEntry, entry, "%s is missing from the file", entry)
	}
	if !json.Valid(raw) {
		return nil, fail(CodeBadJSON, entry, "%s is not valid JSON", entry)
	}
	return raw, nil
}

// FileResult is a type "file" archive read.
type FileResult struct {
	Doc
	// Warnings are problems that did not stop the import.
	Warnings []string
	// Thumbnails are PNG previews by page id.
	Thumbnails map[string][]byte
	// Uncompressed is the unpacked size, to budget nested archives.
	Uncompressed int64
}

func requireString(m map[string]json.RawMessage, entry, key string, nonEmpty bool) (string, error) {
	var s string
	if err := json.Unmarshal(m[key], &s); err != nil {
		return "", fail(CodeSchema, entry, "%s: %s: expected string", entry, key)
	}
	if nonEmpty && s == "" {
		return "", fail(CodeSchema, entry, "%s: %s: must not be empty", entry, key)
	}
	return s, nil
}

func requireNumber(m map[string]json.RawMessage, entry, key string) (int64, error) {
	var f float64
	if err := json.Unmarshal(m[key], &f); err != nil {
		return 0, fail(CodeSchema, entry, "%s: %s: expected number", entry, key)
	}
	return int64(f), nil
}

func object(raw json.RawMessage, entry string) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return nil, fail(CodeSchema, entry, "%s: (root): expected object", entry)
	}
	return m, nil
}

// ReadFile reads a type "file" archive.
func ReadFile(b []byte, lim Limits) (FileResult, error) {
	lim = lim.withDefaults()
	u, err := unpack(b, lim)
	if err != nil {
		return FileResult{}, err
	}
	if string(u.files["mimetype"]) != Mimetype {
		return FileResult{}, fail(CodeBadMimetype, "mimetype", "Not an .sdd file (missing or wrong mimetype entry)")
	}
	rawMF, err := u.json("manifest.json")
	if err != nil {
		return FileResult{}, err
	}
	mfm, err := object(rawMF, "manifest.json")
	if err != nil {
		return FileResult{}, err
	}
	if t := rawString(mfm, "type"); t == "project" || rawString(mfm, "format") == "sddproj" {
		return FileResult{}, fail(CodeUnsupported, "manifest.json", "This .sdd holds a whole project, not one file: import it as a project")
	}
	var mf manifest
	if err := parseManifest(mfm, &mf); err != nil {
		return FileResult{}, err
	}
	if mf.FormatVersion > FormatVersion {
		return FileResult{}, fail(CodeUnsupported, "manifest.json", "File format v%d is newer than this server supports", mf.FormatVersion)
	}

	projRaw, err := u.json("project.json")
	if err != nil {
		return FileResult{}, err
	}
	pj, err := migrateProject(projRaw)
	if err != nil {
		return FileResult{}, err
	}
	pid, err := requireString(pj, "project.json", "id", true)
	if err != nil {
		return FileResult{}, err
	}
	pname, err := requireString(pj, "project.json", "name", false)
	if err != nil {
		return FileResult{}, err
	}
	created, err := requireNumber(pj, "project.json", "createdAt")
	if err != nil {
		return FileResult{}, err
	}
	updated, err := requireNumber(pj, "project.json", "updatedAt")
	if err != nil {
		return FileResult{}, err
	}
	active, err := requireString(pj, "project.json", "activePageId", true)
	if err != nil {
		return FileResult{}, err
	}
	var pageOrder []string
	if json.Unmarshal(pj["pageOrder"], &pageOrder) != nil || len(pageOrder) == 0 {
		return FileResult{}, fail(CodeSchema, "project.json", "project.json: pageOrder: expected a non-empty list of page ids")
	}

	sum := [][]byte{u.files["project.json"]}
	for _, p := range mf.Pages {
		if !SafeName(p.File) {
			return FileResult{}, fail(CodeUnsafePath, p.File, "Unsafe page path %q", p.File)
		}
		if _, ok := u.files[p.File]; !ok {
			return FileResult{}, fail(CodeMissingEntry, p.File, "%s is missing from the file", p.File)
		}
		sum = append(sum, u.files[p.File])
	}
	if sha256Hex(sum...) != mf.Checksum {
		return FileResult{}, fail(CodeChecksum, "", "Checksum mismatch: the file was modified or damaged after it was saved")
	}

	pageMap := map[string]json.RawMessage{}
	for _, p := range mf.Pages {
		raw, err := u.json(p.File)
		if err != nil {
			return FileResult{}, err
		}
		pm, err := migratePage(raw, p.File)
		if err != nil {
			return FileResult{}, err
		}
		id, err := requireString(pm, p.File, "id", true)
		if err != nil {
			return FileResult{}, err
		}
		delete(pm, "schemaVersion")
		if _, err := requireString(pm, p.File, "name", false); err != nil {
			return FileResult{}, err
		}
		for _, k := range []string{"nodes", "edges"} {
			var arr []json.RawMessage
			if json.Unmarshal(pm[k], &arr) != nil || arr == nil {
				return FileResult{}, fail(CodeSchema, p.File, "%s: %s: expected array", p.File, k)
			}
		}
		out, _ := json.Marshal(pm)
		pageMap[id] = out
	}
	pages := make([]json.RawMessage, 0, len(pageOrder))
	foundActive := false
	for _, id := range pageOrder {
		pg, ok := pageMap[id]
		if !ok {
			return FileResult{}, fail(CodeSchema, "project.json", "project.json: pageOrder references unknown page %q", id)
		}
		pages = append(pages, pg)
		foundActive = foundActive || id == active
	}
	if !foundActive {
		return FileResult{}, fail(CodeSchema, "project.json", "project.json: activePageId does not reference a page")
	}

	assets := map[string]map[string]string{}
	thumbs := map[string][]byte{}
	for _, name := range u.order {
		if m := assetRe.FindStringSubmatch(name); m != nil {
			if sha256Hex(u.files[name]) != m[1] {
				return FileResult{}, fail(CodeAsset, name, "Asset %s does not match its content hash", name)
			}
			mime := mimeByExt[strings.ToLower(m[2])]
			if mime == "" {
				mime = "application/octet-stream"
			}
			if !strings.HasPrefix(mime, "image/") {
				return FileResult{}, fail(CodeAsset, "assets/"+m[1], "Asset %s has an unsupported type", m[1])
			}
			assets[m[1]] = map[string]string{"mime": mime, "data": base64.StdEncoding.EncodeToString(u.files[name])}
		}
		if m := thumbRe.FindStringSubmatch(name); m != nil {
			thumbs[m[1]] = u.files[name]
		}
	}

	var warnings []string
	for _, pg := range pages {
		warnings = append(warnings, missingAssetWarnings(pg, assets)...)
	}
	data := map[string]any{"version": 2, "id": pid, "name": pname, "createdAt": created, "updatedAt": updated, "activePageId": active, "pages": pages}
	if s, ok := pj["settings"]; ok && string(s) != "null" {
		data["settings"] = s
	}
	if len(assets) > 0 {
		data["assets"] = assets
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return FileResult{}, err
	}
	return FileResult{Doc: Doc{ID: pid, Name: pname, CreatedAt: created, UpdatedAt: updated, Data: raw}, Warnings: warnings, Thumbnails: thumbs, Uncompressed: u.total}, nil
}

func parseManifest(m map[string]json.RawMessage, out *manifest) error {
	const e = "manifest.json"
	if rawString(m, "format") != "sdd" && rawString(m, "format") != "sddproj" {
		return fail(CodeSchema, e, "manifest.json: format: expected \"sdd\"")
	}
	out.Format = rawString(m, "format")
	var v float64
	if json.Unmarshal(m["formatVersion"], &v) != nil || v < 1 || v != float64(int(v)) {
		return fail(CodeSchema, e, "manifest.json: formatVersion: expected an integer >= 1")
	}
	out.FormatVersion = int(v)
	var err error
	if out.AppVersion, err = requireString(m, e, "appVersion", false); err != nil {
		return err
	}
	if out.CreatedAt, err = requireNumber(m, e, "createdAt"); err != nil {
		return err
	}
	if out.UpdatedAt, err = requireNumber(m, e, "updatedAt"); err != nil {
		return err
	}
	if out.Name, err = requireString(m, e, "name", false); err != nil {
		return err
	}
	if out.ProjectID, err = requireString(m, e, "projectId", true); err != nil {
		return err
	}
	if out.Checksum, err = requireString(m, e, "checksum", false); err != nil || !hashRe.MatchString(out.Checksum) {
		return fail(CodeSchema, e, "manifest.json: checksum: expected 64 hex characters")
	}
	if json.Unmarshal(m["pages"], &out.Pages) != nil || len(out.Pages) == 0 {
		return fail(CodeSchema, e, "manifest.json: pages: expected a non-empty list")
	}
	for i, p := range out.Pages {
		if p.ID == "" || p.File == "" {
			return fail(CodeSchema, e, "manifest.json: pages.%d: id and file are required", i)
		}
	}
	return nil
}

func missingAssetWarnings(page json.RawMessage, assets map[string]map[string]string) []string {
	var pg struct {
		Name  string `json:"name"`
		Nodes []struct {
			Data struct {
				Label  string `json:"label"`
				IconID string `json:"iconId"`
			} `json:"data"`
		} `json:"nodes"`
	}
	if json.Unmarshal(page, &pg) != nil {
		return nil
	}
	var out []string
	for _, n := range pg.Nodes {
		if h, ok := strings.CutPrefix(n.Data.IconID, "asset:"); ok {
			if _, have := assets[h]; !have {
				out = append(out, fmt.Sprintf("Page %q: custom icon asset for %q is missing", pg.Name, n.Data.Label))
			}
		}
	}
	return out
}

// migrateProject brings project.json up to the current schema (the same
// one-version steps as the Node server's).
func migrateProject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	m, err := object(raw, "project.json")
	if err != nil {
		return nil, err
	}
	v, err := schemaVersion(m, "project.json")
	if err != nil {
		return nil, err
	}
	if v == 1 { // activePage was renamed activePageId in v2
		if _, ok := m["activePageId"]; !ok {
			m["activePageId"] = m["activePage"]
		}
		delete(m, "activePage")
	}
	return m, nil
}

// migratePage is migrateProject for a page file (v1 nodes had data.icon,
// later data.iconId).
func migratePage(raw json.RawMessage, entry string) (map[string]json.RawMessage, error) {
	m, err := object(raw, entry)
	if err != nil {
		return nil, err
	}
	v, err := schemaVersion(m, entry)
	if err != nil {
		return nil, err
	}
	if v == 1 {
		var nodes []map[string]json.RawMessage
		if json.Unmarshal(m["nodes"], &nodes) == nil {
			for _, n := range nodes {
				var data map[string]json.RawMessage
				if json.Unmarshal(n["data"], &data) != nil {
					continue
				}
				if icon, ok := data["icon"]; ok {
					if _, has := data["iconId"]; !has {
						data["iconId"] = icon
					}
					delete(data, "icon")
					n["data"], _ = json.Marshal(data)
				}
			}
			m["nodes"], _ = json.Marshal(nodes)
		}
	}
	return m, nil
}

func schemaVersion(m map[string]json.RawMessage, entry string) (int, error) {
	v := 1
	if raw, ok := m["schemaVersion"]; ok {
		var f float64
		if json.Unmarshal(raw, &f) == nil {
			v = int(f)
		}
	}
	switch {
	case v > SchemaVersion:
		return v, fail(CodeUnsupported, entry, "%s uses schema v%d, which is newer than this server supports (v%d)", entry, v, SchemaVersion)
	case v < 1:
		return v, fail(CodeUnsupported, entry, "No migration from %s schema v%d", entry, v)
	}
	return v, nil
}

// Kind is what a .sdd turns out to be.
type Kind string

const (
	KindFile    Kind = "file"
	KindProject Kind = "project"
	KindText    Kind = "text"
)

// Sniffed is the content type of a file; Legacy names an old spelling
// (sddproj, sddt, no-type).
type Sniffed struct {
	Kind   Kind
	Legacy string
}

var textHeader = regexp.MustCompile(`^#!sdd(?:[ \t]+\d+)?[ \t]*$`)

// Sniff looks at the content, never at the extension: "PK" is a ZIP (its
// manifest says file or project), a first line "#!sdd" is the text format.
func Sniff(b []byte, name string, lim Limits) (Sniffed, error) {
	lim = lim.withDefaults()
	if int64(len(b)) > lim.MaxFileBytes {
		return Sniffed{}, fail(CodeTooLarge, "", "File is too large (limit %d MB)", lim.MaxFileBytes>>20)
	}
	if len(b) >= 4 && b[0] == 'P' && b[1] == 'K' {
		zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
		if err != nil {
			return Sniffed{}, fail(CodeCorruptZip, "", "File is not a valid .sdd archive (corrupt or truncated)")
		}
		for _, f := range zr.File {
			if f.Name != "manifest.json" {
				continue
			}
			if f.UncompressedSize64 > 5<<20 {
				break
			}
			rc, err := f.Open()
			if err != nil {
				break
			}
			raw, _ := io.ReadAll(io.LimitReader(rc, 5<<20))
			rc.Close()
			var m map[string]json.RawMessage
			if json.Unmarshal(raw, &m) != nil || m == nil {
				break
			}
			switch {
			case rawString(m, "format") == "sddproj":
				return Sniffed{Kind: KindProject, Legacy: "sddproj"}, nil
			case rawString(m, "type") == "project":
				return Sniffed{Kind: KindProject}, nil
			case m["type"] == nil:
				return Sniffed{Kind: KindFile, Legacy: "no-type"}, nil
			}
			return Sniffed{Kind: KindFile}, nil
		}
		return Sniffed{Kind: KindFile}, nil // the file reader says exactly what is wrong
	}
	if text, ok := DecodeText(b); ok {
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
			if line == "" {
				continue
			}
			if textHeader.MatchString(line) {
				return Sniffed{Kind: KindText}, nil
			}
			break
		}
		if strings.HasSuffix(strings.ToLower(name), ".sddt") && strings.TrimSpace(text) != "" {
			return Sniffed{Kind: KindText, Legacy: "sddt"}, nil
		}
	}
	return Sniffed{}, fail(CodeUnsupported, "", `Not a .sdd file: expected a ZIP archive or text starting with "#!sdd"`)
}

// DecodeText returns b as UTF-8 text without a BOM; ok is false for binary
// or invalid text.
func DecodeText(b []byte) (string, bool) {
	if bytes.IndexByte(b, 0) >= 0 || !utf8.Valid(b) {
		return "", false
	}
	return strings.TrimPrefix(string(b), "\xef\xbb\xbf"), true
}

// ErrTextUnsupported was the answer for the diagrams-as-code text format. The
// server now reads and writes it (internal/domain/diagram/core); the error is
// kept for callers that only handle containers.
func ErrTextUnsupported() error {
	return fail(CodeUnsupported, "", "The text .sdd format (diagrams as code) is not supported by this server; use the .sdd container")
}
