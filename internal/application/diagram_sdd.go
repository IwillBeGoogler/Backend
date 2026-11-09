package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"slices"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram/sddfile"
)

// Export and import of .sdd files (docs/sdd-format.md): a file as a ZIP
// container, a whole project as a container of type "project". The text
// flavour of the format is not supported (see sddfile).

// SDDInvalid is an upload that is not a usable .sdd: Reason is the Node
// server's code (corrupt-zip, checksum, schema, unsupported, ...) and Entry
// the failing entry of the archive.
type SDDInvalid struct {
	Reason, Entry, Msg string
}

func (e *SDDInvalid) Error() string { return e.Msg }

func sddInvalid(err error) error {
	var se *sddfile.Error
	if errors.As(err, &se) {
		return &SDDInvalid{Reason: se.Code, Entry: se.Entry, Msg: se.Msg}
	}
	return err
}

// SDDMaxBytes is the largest .sdd upload accepted.
func (d *Diagrams) SDDMaxBytes() int64 { return d.sddLimits().MaxFileBytes }

func (d *Diagrams) sddLimits() sddfile.Limits {
	l := sddfile.Limits{MaxFileBytes: d.MaxSDDBytes, MaxUncompressedBytes: d.MaxSDDUnzipBytes}
	if l.MaxFileBytes <= 0 {
		l.MaxFileBytes = 100 << 20
	}
	if l.MaxUncompressedBytes <= 0 {
		l.MaxUncompressedBytes = 300 << 20
	}
	return l
}

// SDDExport is a file ready to download.
type SDDExport struct {
	Name    string
	Version int // the file's version, for the ETag (0 for a project)
	Data    []byte
}

func (d *Diagrams) docOf(f diagram.Doc) sddfile.Doc {
	var m map[string]json.RawMessage
	data := f.Data
	if json.Unmarshal(data, &m) == nil && m != nil {
		m["id"], _ = json.Marshal(f.ID)
		m["name"], _ = json.Marshal(f.Name)
		if b, err := json.Marshal(m); err == nil {
			data = b
		}
	}
	return sddfile.Doc{ID: f.ID, Name: f.Name, CreatedAt: ms(f.CreatedAt), UpdatedAt: ms(f.UpdatedAt), Data: data}
}

// thumbnailPNG is a file's stored preview when it is a PNG.
func (d *Diagrams) thumbnailPNG(ctx context.Context, f diagram.Doc) []byte {
	if f.ThumbnailHash == nil || d.Blobs == nil {
		return nil
	}
	obj, err := d.Blobs.Get(ctx, blobKey(*f.ThumbnailHash))
	if err != nil {
		return nil
	}
	defer obj.Body.Close()
	b, err := io.ReadAll(io.LimitReader(obj.Body, MaxThumbnailBytes+1))
	if err != nil || len(b) > MaxThumbnailBytes || diagram.DetectMime(b) != diagram.MimePNG {
		return nil
	}
	return b
}

// withAssetBytes fills the assets of a document that live in the asset store
// (an entry with no "data": the embedded editor keeps only {mime}) so the
// container can pack them by hash. An entry whose bytes are gone is dropped
// (the importer then warns about the missing icon) rather than packed empty.
func (d *Diagrams) withAssetBytes(ctx context.Context, doc sddfile.Doc) sddfile.Doc {
	var m map[string]json.RawMessage
	if json.Unmarshal(doc.Data, &m) != nil || m["assets"] == nil {
		return doc
	}
	var assets map[string]map[string]any
	if json.Unmarshal(m["assets"], &assets) != nil {
		return doc
	}
	changed := false
	for h, a := range assets {
		if _, ok := a["data"].(string); ok {
			continue
		}
		changed = true
		b := d.assetBytes(ctx, h)
		if b == nil {
			delete(assets, h)
			continue
		}
		a["data"] = base64.StdEncoding.EncodeToString(b)
	}
	if !changed {
		return doc
	}
	raw, err := json.Marshal(assets)
	if err != nil {
		return doc
	}
	m["assets"] = raw
	if out, err := json.Marshal(m); err == nil {
		doc.Data = out
	}
	return doc
}

func (d *Diagrams) assetBytes(ctx context.Context, hash string) []byte {
	if d.Blobs == nil || !diagram.ValidHash(hash) {
		return nil
	}
	obj, err := d.Blobs.Get(ctx, blobKey(hash))
	if err != nil {
		return nil
	}
	defer obj.Body.Close()
	b, err := io.ReadAll(io.LimitReader(obj.Body, int64(d.MaxAssetBytes)+1))
	if err != nil || len(b) > d.MaxAssetBytes {
		return nil
	}
	return b
}

// ExportFile packs a file as a type "file" container. The caller needs the
// viewer role; a share token works for a non-member.
func (d *Diagrams) ExportFile(ctx context.Context, actor int, token, id string) (SDDExport, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.ExportFile")
	defer span.End()
	_, f, err := d.fileAccessVia(ctx, actor, token, id, diagram.RoleViewer)
	if err != nil {
		return SDDExport{}, err
	}
	doc := d.withAssetBytes(ctx, d.docOf(f))
	var thumbs map[string][]byte
	if t := d.thumbnailPNG(ctx, f); t != nil {
		var m struct {
			Active string `json:"activePageId"`
		}
		_ = json.Unmarshal(f.Data, &m)
		thumbs = map[string][]byte{m.Active: t}
	}
	b, err := sddfile.BuildFile(doc, thumbs)
	return SDDExport{Name: f.Name, Version: f.Version, Data: b}, err
}

// ExportProject packs a whole project (folders and files) as a type
// "project" container. The viewer role is needed; a project-wide share
// token works for a non-member.
func (d *Diagrams) ExportProject(ctx context.Context, actor int, token, id string) (SDDExport, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.ExportProject")
	defer span.End()
	a, err := d.projectAccessVia(ctx, actor, token, id, diagram.RoleViewer)
	if err != nil {
		return SDDExport{}, err
	}
	folders, files, err := d.Repo.Tree(ctx, id)
	if err != nil {
		return SDDExport{}, err
	}
	p := sddfile.Project{Name: a.project.Name, CreatedAt: ms(a.project.CreatedAt), UpdatedAt: ms(a.project.UpdatedAt)}
	for _, f := range folders {
		p.Folders = append(p.Folders, sddfile.Folder{ID: f.ID, ParentID: f.ParentID, Name: f.Name, Position: f.Position})
	}
	for _, m := range files {
		f, err := d.Repo.File(ctx, m.ID)
		if err != nil {
			return SDDExport{}, err
		}
		p.Files = append(p.Files, sddfile.File{Doc: d.withAssetBytes(ctx, d.docOf(f)), FolderID: f.FolderID, Kind: f.Kind, Position: f.Position, Thumbnail: d.thumbnailPNG(ctx, f)})
	}
	b, err := sddfile.BuildProject(p)
	return SDDExport{Name: a.project.Name, Data: b}, err
}

// SDDImport is the outcome of an import: the new project (or the one
// imported into), its files, and what was dropped on the way.
type SDDImport struct {
	// Kind is "file", "project" or "text" (what the upload was).
	Kind     string
	Project  diagram.ProjectSummary
	File     DiagramFile // a single-file import
	Folders  []diagram.Folder
	Files    []diagram.File // a project import
	Warnings []string
}

// thumbOf stores an embedded preview; a bad one is dropped with a warning.
func (d *Diagrams) thumbOf(ctx context.Context, actor int, png []byte, warnings *[]string, label string) *string {
	if len(png) == 0 {
		return nil
	}
	a, err := d.StoreAsset(ctx, actor, png, diagram.MimePNG)
	if err != nil {
		*warnings = append(*warnings, label+" was not a valid PNG and was ignored")
		return nil
	}
	if len(png) > MaxThumbnailBytes {
		*warnings = append(*warnings, label+" is larger than 200 KB and was ignored")
		return nil
	}
	return &a.Hash
}

func (d *Diagrams) readAny(name string, b []byte) (sddfile.Sniffed, error) {
	s, err := sddfile.Sniff(b, name, d.sddLimits())
	if err != nil {
		return s, sddInvalid(err)
	}
	return s, nil
}

// ImportAny creates a NEW project from any .sdd: a file container becomes
// a project with one file, a project container a project with its tree.
// name is the upload's file name (for sniffing legacy spellings).
func (d *Diagrams) ImportAny(ctx context.Context, actor int, name string, b []byte) (SDDImport, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.ImportAny")
	defer span.End()
	s, err := d.readAny(name, b)
	if err != nil {
		return SDDImport{}, err
	}
	if s.Kind == sddfile.KindProject {
		return d.importProject(ctx, actor, b)
	}
	return d.importFileAsProject(ctx, actor, s, name, b)
}

// ImportFileAsProject creates a new project with one file from a file
// container; a project container is refused (use ImportAny).
func (d *Diagrams) ImportFileAsProject(ctx context.Context, actor int, name string, b []byte) (SDDImport, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.ImportFileAsProject")
	defer span.End()
	s, err := d.readAny(name, b)
	if err != nil {
		return SDDImport{}, err
	}
	if s.Kind == sddfile.KindProject {
		return SDDImport{}, &SDDInvalid{Reason: sddfile.CodeUnsupported, Msg: "This .sdd holds a whole project: use POST /import"}
	}
	return d.importFileAsProject(ctx, actor, s, name, b)
}

// ImportProject creates a new project from a project container (also the
// legacy .sddproj); a file container is refused.
func (d *Diagrams) ImportProject(ctx context.Context, actor int, name string, b []byte) (SDDImport, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.ImportProject")
	defer span.End()
	s, err := d.readAny(name, b)
	if err != nil {
		return SDDImport{}, err
	}
	if s.Kind != sddfile.KindProject {
		return SDDImport{}, &SDDInvalid{Reason: sddfile.CodeUnsupported, Msg: "This .sdd holds a single file, not a project: use POST /import"}
	}
	return d.importProject(ctx, actor, b)
}

func (d *Diagrams) importFileAsProject(ctx context.Context, actor int, s sddfile.Sniffed, upload string, b []byte) (SDDImport, error) {
	r, label, err := d.readOne(s, upload, b)
	if err != nil {
		return SDDImport{}, err
	}
	warnings := slices.Clone(r.Warnings)
	thumb := d.thumbOf(ctx, actor, pickThumb(r.Data, r.Thumbnails), &warnings, "The embedded thumbnail")
	name, err := diagram.CleanName(r.Name, "Imported project")
	if err != nil {
		return SDDImport{}, err
	}
	made, err := d.CreateProject(ctx, actor, NewDiagramProject{Name: name,
		File: &NewDiagramFile{Name: name, Data: r.Data, Label: label, ThumbnailHash: thumb}})
	if err != nil {
		return SDDImport{}, importFailure(err)
	}
	return SDDImport{Kind: string(s.Kind), Project: made.Project, File: made.File, Warnings: warnings}, nil
}

// importFailure turns a document the file model refuses into an invalid
// upload (422), as the Node server does.
func importFailure(err error) error {
	var de *diagram.Error
	var ve *domain.ValidationError
	switch {
	case errors.As(err, &de) && de.Code == "VALIDATION_ERROR":
		return &SDDInvalid{Reason: sddfile.CodeSchema, Msg: de.Msg}
	case errors.As(err, &ve):
		return &SDDInvalid{Reason: sddfile.CodeSchema, Msg: ve.Msg}
	}
	return err
}

func pickThumb(data json.RawMessage, thumbs map[string][]byte) []byte {
	var m struct {
		Active string `json:"activePageId"`
	}
	_ = json.Unmarshal(data, &m)
	if t, ok := thumbs[m.Active]; ok {
		return t
	}
	for _, t := range thumbs {
		return t
	}
	return nil
}

func (d *Diagrams) importProject(ctx context.Context, actor int, b []byte) (SDDImport, error) {
	proj, warnings, err := sddfile.ReadProject(b, d.sddLimits())
	if err != nil {
		return SDDImport{}, sddInvalid(err)
	}
	name, err := diagram.CleanName(proj.Name, "Imported project")
	if err != nil {
		return SDDImport{}, err
	}
	now := d.clock()
	p := diagram.Project{ID: diagram.NewID(diagram.PrefixProject), OwnerID: actor, Name: name, CreatedAt: now, UpdatedAt: now, UpdatedBy: actor}

	// fresh ids for everything: the archive's ids are untrusted and could collide with existing rows
	folderIDs := map[string]string{}
	for _, f := range proj.Folders {
		folderIDs[f.ID] = diagram.NewID(diagram.PrefixFolder)
	}
	var folders []diagram.Folder
	for _, f := range orderedFolders(proj.Folders) { // parents before children, as the foreign key needs
		fname, err := diagram.CleanName(f.Name, "Folder")
		if err != nil {
			return SDDImport{}, err
		}
		nf := diagram.Folder{ID: folderIDs[f.ID], ProjectID: p.ID, Name: fname, Position: f.Position, CreatedAt: now, UpdatedAt: now}
		if f.ParentID != nil {
			parent := folderIDs[*f.ParentID]
			nf.ParentID = &parent
		}
		folders = append(folders, nf)
	}
	w := writer{user: actor, owner: actor}
	var news []diagram.NewFile
	var priv []map[int]diagram.PrivateSet
	for _, f := range proj.Files {
		fname, err := diagram.CleanName(f.Name, "Untitled")
		if err != nil {
			return SDDImport{}, err
		}
		in := NewDiagramFile{Name: fname, Kind: f.Kind, Position: &f.Position, Data: f.Data, Label: "Imported from .sdd"}
		if f.FolderID != nil {
			id := folderIDs[*f.FolderID]
			in.FolderID = &id
		}
		in.ThumbnailHash = d.thumbOf(ctx, actor, f.Thumbnail, &warnings, f.Name+": the embedded thumbnail")
		nf, pv, err := d.buildFile(ctx, p.ID, w, in, f.Position, now)
		if err != nil {
			return SDDImport{}, importFailure(withEntry(err, f.Name))
		}
		news = append(news, nf)
		priv = append(priv, pv)
	}
	if err := d.Repo.CreateProject(ctx, p); err != nil {
		return SDDImport{}, err
	}
	if err := d.Repo.InsertTree(ctx, folders, news); err != nil {
		return SDDImport{}, err
	}
	for i, nf := range news {
		if err := d.filePrivate(ctx, nf.Doc.ID, priv[i], now); err != nil {
			return SDDImport{}, err
		}
	}
	if _, err := d.touch(ctx, p.ID, actor); err != nil {
		return SDDImport{}, err
	}
	fresh, err := d.Repo.Project(ctx, p.ID)
	if err != nil {
		return SDDImport{}, err
	}
	sum, err := d.summary(ctx, fresh, diagram.RoleOwner)
	if err != nil {
		return SDDImport{}, err
	}
	out := SDDImport{Kind: string(sddfile.KindProject), Project: sum, Folders: folders, Warnings: warnings}
	for _, nf := range news {
		out.Files = append(out.Files, nf.Doc.File)
	}
	return out, nil
}

// withEntry names the file an error came from.
func withEntry(err error, file string) error {
	var de *diagram.Error
	if errors.As(err, &de) && de.Code == "VALIDATION_ERROR" {
		return &diagram.Error{Kind: de.Kind, Code: de.Code, Msg: file + ": " + de.Msg}
	}
	return err
}

// orderedFolders sorts folders so that every parent comes before its children.
func orderedFolders(fs []sddfile.Folder) []sddfile.Folder {
	placed := map[string]bool{}
	var out []sddfile.Folder
	for len(out) < len(fs) {
		before := len(out)
		for _, f := range fs {
			if !placed[f.ID] && (f.ParentID == nil || placed[*f.ParentID]) {
				placed[f.ID] = true
				out = append(out, f)
			}
		}
		if len(out) == before { // a cycle cannot get here (ValidateTree), but never loop forever
			break
		}
	}
	return out
}

// ImportIntoProject adds a file container as a new file of an existing
// project (editors), optionally inside a folder.
func (d *Diagrams) ImportIntoProject(ctx context.Context, actor int, projectID string, folderID *string, name string, b []byte) (SDDImport, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.ImportIntoProject")
	defer span.End()
	if _, err := d.projectAccess(ctx, actor, projectID, diagram.RoleEditor); err != nil {
		return SDDImport{}, err
	}
	if err := d.folderInProject(ctx, projectID, folderID); err != nil {
		return SDDImport{}, err
	}
	s, err := d.readAny(name, b)
	if err != nil {
		return SDDImport{}, err
	}
	if s.Kind == sddfile.KindProject {
		return SDDImport{}, &SDDInvalid{Reason: sddfile.CodeUnsupported, Msg: "This .sdd holds a whole project: use POST /import to create a project from it"}
	}
	r, label, err := d.readOne(s, name, b)
	if err != nil {
		return SDDImport{}, err
	}
	warnings := slices.Clone(r.Warnings)
	thumb := d.thumbOf(ctx, actor, pickThumb(r.Data, r.Thumbnails), &warnings, "The embedded thumbnail")
	f, _, err := d.CreateFile(ctx, actor, projectID, NewDiagramFile{Name: r.Name, Data: r.Data, FolderID: folderID, Label: label, ThumbnailHash: thumb})
	if err != nil {
		return SDDImport{}, importFailure(err)
	}
	return SDDImport{Kind: string(s.Kind), File: f, Warnings: warnings}, nil
}
