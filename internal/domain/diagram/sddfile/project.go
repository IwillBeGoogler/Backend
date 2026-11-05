package sddfile

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// A .sdd of type "project" is a whole project in one ZIP: mimetype (stored)
// / manifest.json (type "project", the folder tree and the file list) /
// files/<fileId>.sdd (one type "file" archive per file, stored) /
// files/<fileId>/source.sdd. Folder and file names are data only, never
// paths. The legacy .sddproj (format "sddproj", its own mimetype) is read
// and never written.

// Folder is a folder of an archive.
type Folder struct {
	ID       string  `json:"id"`
	ParentID *string `json:"parentId"`
	Name     string  `json:"name"`
	Position float64 `json:"position"`
}

// File is a file of a project archive. Kind is the dominant diagram kind.
type File struct {
	Doc
	FolderID  *string
	Kind      string
	Position  float64
	Thumbnail []byte
	Warnings  []string
}

// Project is a whole project.
type Project struct {
	Name      string
	CreatedAt int64
	UpdatedAt int64
	Folders   []Folder
	Files     []File
}

type projManifestFile struct {
	ID       string  `json:"id"`
	FolderID *string `json:"folderId"`
	Name     string  `json:"name"`
	Kind     string  `json:"kind"`
	Position float64 `json:"position"`
	Path     string  `json:"path"`
	Source   string  `json:"source,omitempty"`
}

type projManifest struct {
	Format        string             `json:"format"`
	FormatVersion int                `json:"formatVersion"`
	Type          string             `json:"type,omitempty"`
	AppVersion    string             `json:"appVersion"`
	CreatedAt     int64              `json:"createdAt"`
	UpdatedAt     int64              `json:"updatedAt"`
	Name          string             `json:"name"`
	Folders       []Folder           `json:"folders"`
	Files         []projManifestFile `json:"files"`
	Checksum      string             `json:"checksum"`
}

// BuildProject writes a project archive.
func BuildProject(p Project) ([]byte, error) {
	files := []zipFile{{"mimetype", []byte(Mimetype), true}}
	var sums []byte
	mf := projManifest{Format: "sdd", FormatVersion: 1, Type: "project", AppVersion: AppVersion, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
		Name: p.Name, Folders: p.Folders, Files: []projManifestFile{}}
	if mf.Folders == nil {
		mf.Folders = []Folder{}
	}
	var entries []zipFile
	for _, f := range p.Files {
		var thumbs map[string][]byte
		if len(f.Thumbnail) > 0 {
			thumbs = map[string][]byte{activePage(f.Data): f.Thumbnail}
		}
		b, err := BuildFile(f.Doc, thumbs)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Name, err)
		}
		path := "files/" + f.ID + ".sdd"
		sums = append(sums, b...)
		entries = append(entries, zipFile{path, b, true})
		mf.Files = append(mf.Files, projManifestFile{ID: f.ID, FolderID: f.FolderID, Name: f.Name, Kind: f.Kind, Position: f.Position, Path: path})
	}
	mf.Checksum = sha256Hex(sums)
	files = append(files, zipFile{"manifest.json", indented(mf), false})
	return writeZip(append(files, entries...))
}

func activePage(data json.RawMessage) string {
	var d map[string]json.RawMessage
	_ = json.Unmarshal(data, &d)
	return rawString(d, "activePageId")
}

var idRe = regexp.MustCompile(`^.{1,64}$`)

// ReadProject reads a project archive (or a legacy .sddproj). File
// documents are unpacked with a shared byte budget.
func ReadProject(b []byte, lim Limits) (Project, []string, error) {
	lim = lim.withDefaults()
	u, err := unpack(b, lim)
	if err != nil {
		return Project{}, nil, err
	}
	rawMF, err := u.json("manifest.json")
	if err != nil {
		return Project{}, nil, err
	}
	m, err := object(rawMF, "manifest.json")
	if err != nil {
		return Project{}, nil, err
	}
	if rawString(m, "format") == "sdd" && rawString(m, "type") != "project" && m["pages"] != nil {
		return Project{}, nil, fail(CodeUnsupported, "manifest.json", "This .sdd holds a single file, not a project: open it as a file")
	}
	var mf projManifest
	if err := parseProjManifest(m, &mf); err != nil {
		return Project{}, nil, err
	}
	legacy := mf.Format == "sddproj"
	want := Mimetype
	if legacy {
		want = LegacyProjMIME
	}
	if string(u.files["mimetype"]) != want {
		return Project{}, nil, fail(CodeBadMimetype, "mimetype", "Not an .sdd file (missing or wrong mimetype entry)")
	}
	if !legacy && mf.Type != "project" {
		return Project{}, nil, fail(CodeSchema, "manifest.json", `manifest.json: type: expected "project"`)
	}
	if mf.FormatVersion > 1 {
		return Project{}, nil, fail(CodeUnsupported, "manifest.json", "File format v%d is newer than this server supports", mf.FormatVersion)
	}
	if err := ValidateTree(mf.Folders, mf.Files); err != nil {
		return Project{}, nil, err
	}
	var all []byte
	for _, f := range mf.Files {
		if !SafeName(f.Path) || !strings.HasPrefix(f.Path, "files/") || !strings.HasSuffix(f.Path, ".sdd") {
			return Project{}, nil, fail(CodeUnsafePath, f.Path, "Unsafe file path %q", f.Path)
		}
		if _, ok := u.files[f.Path]; !ok {
			return Project{}, nil, fail(CodeMissingEntry, f.Path, "%s is missing from the file", f.Path)
		}
		if f.Source != "" && (!SafeName(f.Source) || !strings.HasPrefix(f.Source, "files/") || !strings.HasSuffix(f.Source, "/source.sdd")) {
			return Project{}, nil, fail(CodeUnsafePath, f.Source, "Unsafe source path %q", f.Source)
		}
		all = append(all, u.files[f.Path]...)
	}
	if sha256Hex(all) != mf.Checksum {
		return Project{}, nil, fail(CodeChecksum, "", "Checksum mismatch: the file was modified or damaged after it was saved")
	}

	budget := lim.MaxUncompressedBytes - u.total
	out := Project{Name: mf.Name, CreatedAt: mf.CreatedAt, UpdatedAt: mf.UpdatedAt, Folders: mf.Folders}
	var warnings []string
	for _, f := range mf.Files {
		if budget <= 0 { // a zero limit would mean "default" to ReadFile
			return Project{}, nil, fail(CodeZipBomb, "", "Archive expands beyond the allowed size")
		}
		r, err := ReadFile(u.files[f.Path], Limits{MaxFileBytes: lim.MaxFileBytes, MaxUncompressedBytes: budget, MaxEntries: lim.MaxEntries})
		if err != nil {
			if e, ok := err.(*Error); ok {
				return Project{}, nil, &Error{Code: e.Code, Entry: f.Path, Msg: f.Path + ": " + e.Msg}
			}
			return Project{}, nil, err
		}
		budget -= r.Uncompressed
		if budget < 0 {
			return Project{}, nil, fail(CodeZipBomb, "", "Archive expands beyond the allowed size")
		}
		for _, w := range r.Warnings {
			warnings = append(warnings, f.Name+": "+w)
		}
		file := File{Doc: Doc{ID: f.ID, Name: f.Name, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, Data: r.Data},
			FolderID: f.FolderID, Kind: f.Kind, Position: f.Position, Warnings: r.Warnings}
		if t, ok := r.Thumbnails[activePage(r.Data)]; ok {
			file.Thumbnail = t
		} else {
			for _, t := range r.Thumbnails {
				file.Thumbnail = t
				break
			}
		}
		out.Files = append(out.Files, file)
	}
	return out, warnings, nil
}

func parseProjManifest(m map[string]json.RawMessage, out *projManifest) error {
	const e = "manifest.json"
	out.Format = rawString(m, "format")
	if out.Format != "sdd" && out.Format != "sddproj" {
		return fail(CodeSchema, e, `manifest.json: format: expected "sdd" or "sddproj"`)
	}
	out.Type = rawString(m, "type")
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
	if len(out.Name) > 800 {
		return fail(CodeSchema, e, "manifest.json: name: too long")
	}
	if out.Checksum, err = requireString(m, e, "checksum", false); err != nil || !hashRe.MatchString(out.Checksum) {
		return fail(CodeSchema, e, "manifest.json: checksum: expected 64 hex characters")
	}
	if json.Unmarshal(m["folders"], &out.Folders) != nil || len(out.Folders) > MaxProjectFolders {
		return fail(CodeSchema, e, "manifest.json: folders: expected a list of at most %d", MaxProjectFolders)
	}
	if json.Unmarshal(m["files"], &out.Files) != nil || len(out.Files) > MaxProjectFiles {
		return fail(CodeSchema, e, "manifest.json: files: expected a list of at most %d", MaxProjectFiles)
	}
	for i, f := range out.Folders {
		if !idRe.MatchString(f.ID) || (f.ParentID != nil && !idRe.MatchString(*f.ParentID)) {
			return fail(CodeSchema, e, "manifest.json: folders.%d: ids are 1-64 characters", i)
		}
	}
	for i, f := range out.Files {
		if !idRe.MatchString(f.ID) || f.Path == "" || (f.FolderID != nil && !idRe.MatchString(*f.FolderID)) {
			return fail(CodeSchema, e, "manifest.json: files.%d: invalid id or path", i)
		}
		switch f.Kind {
		case "sdd", "erd", "uml", "board":
		default:
			return fail(CodeSchema, e, "manifest.json: files.%d.kind: expected sdd, erd, uml or board", i)
		}
	}
	return nil
}

// ValidateTree checks folder references: known parents, no cycles, bounded
// depth, unique ids.
func ValidateTree(folders []Folder, files []projManifestFile) error {
	byID := map[string]Folder{}
	for _, f := range folders {
		if _, dup := byID[f.ID]; dup {
			return fail(CodeBadTree, "manifest.json", "Duplicate folder id %q", f.ID)
		}
		byID[f.ID] = f
	}
	for _, f := range folders {
		depth := 0
		for cur, ok := f, true; ok; {
			if cur.ParentID != nil {
				if _, known := byID[*cur.ParentID]; !known {
					return fail(CodeBadTree, "manifest.json", "Folder %q references an unknown parent", cur.Name)
				}
			}
			if depth++; depth > MaxFolderDepth {
				return fail(CodeBadTree, "manifest.json", "Folders are nested too deeply or form a cycle")
			}
			if cur.ParentID == nil {
				break
			}
			cur, ok = byID[*cur.ParentID]
		}
	}
	seen := map[string]bool{}
	for _, f := range files {
		if seen[f.ID] {
			return fail(CodeBadTree, "manifest.json", "Duplicate file id %q", f.ID)
		}
		seen[f.ID] = true
		if f.FolderID != nil {
			if _, ok := byID[*f.FolderID]; !ok {
				return fail(CodeBadTree, "manifest.json", "File %q references an unknown folder", f.ID)
			}
		}
	}
	return nil
}
