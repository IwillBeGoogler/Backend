package http

// .sdd export and import: a file or a whole project as the Node server's
// ZIP container (docs/sdd-format.md). The routes live at their original
// paths for a session and under /v1 for access tokens (files:read to
// export, files:write to import). The diagrams-as-code text flavour
// (?type=text, "#!sdd" uploads) is the diagrams-as-code text of the active
// page; the language is in internal/domain/diagram/core.

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/application"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram/sddfile"
)

// SDDMime is the type of a .sdd download.
const SDDMime = sddfile.Mimetype

func (h *Handler) sddRoutes(mux dgRouter) {
	for _, base := range []string{DiagramPrefix, DiagramV1Prefix} {
		mux.HandleFunc("GET "+base+"/files/{id}/export", scoped(diagram.ScopeFilesRead, h.dgExportFile))
		mux.HandleFunc("GET "+base+"/files/{id}/export.sdd", scoped(diagram.ScopeFilesRead, h.dgExportFile))
		mux.HandleFunc("GET "+base+"/projects/{id}/export", scoped(diagram.ScopeFilesRead, h.dgExportProject))
		mux.HandleFunc("GET "+base+"/projects/{id}/export.sddproj", scoped(diagram.ScopeFilesRead, h.dgExportProject))
		mux.HandleFunc("POST "+base+"/import", scoped(diagram.ScopeFilesWrite, h.dgImport))
		mux.HandleFunc("POST "+base+"/projects/import", scoped(diagram.ScopeFilesWrite, h.dgImportOne))
		mux.HandleFunc("POST "+base+"/projects/import-project", scoped(diagram.ScopeFilesWrite, h.dgImportProject))
		mux.HandleFunc("POST "+base+"/projects/{id}/import", scoped(diagram.ScopeFilesWrite, h.dgImportInto))
	}
}

var unsafeFileChars = regexp.MustCompile(`[^\w .()-]+`)

// sddFileName is a safe download name: the name with odd characters
// replaced, at most 100 of them, plus ".sdd".
func sddFileName(name, fallback string) string {
	n := strings.TrimSpace(unsafeFileChars.ReplaceAllString(name, "_"))
	if r := []rune(n); len(r) > 100 {
		n = string(r[:100])
	}
	if n == "" {
		n = fallback
	}
	return n + ".sdd"
}

func sddHeaders(w http.ResponseWriter, filename string) {
	hd := w.Header()
	hd.Set("Content-Type", SDDMime)
	hd.Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	hd.Set("Cache-Control", "private, no-cache")
	hd.Set("X-Content-Type-Options", "nosniff")
}

// sddFail answers an upload the server cannot use: 422 SDD_INVALID with
// {reason, entry} (the Node server's codes), or the usual error.
func sddFail(w http.ResponseWriter, r *http.Request, err error) {
	var bad *application.SDDInvalid
	if errors.As(err, &bad) {
		details := map[string]string{"reason": bad.Reason}
		if bad.Entry != "" {
			details["entry"] = bad.Entry
		}
		dgFail(w, r, http.StatusUnprocessableEntity, "SDD_INVALID", bad.Msg, details)
		return
	}
	dgError(w, r, err)
}

// dgExportFile downloads a file as a .sdd container (a ZIP of type "file").
//
// Query: type=file (default) or type=text, the "#!sdd" diagrams-as-code
// text of the active page. Response: application/vnd.sdd+zip (text/vnd.sdd
// for text) with ETag = the file's version. Auth: viewer, or X-Share-Token.
//
// spector:tags diagrams
func (h *Handler) dgExportFile(w http.ResponseWriter, r *http.Request) {
	uid, token, ok := h.dgActor(w, r)
	if !ok {
		return
	}
	id, ok := dgID(w, r, "id")
	if !ok {
		return
	}
	text := false
	switch t := r.URL.Query().Get("type"); t {
	case "", "file":
	case "text":
		text = true
	default:
		dgValidation(w, r, "querystring", "type: must be file or text")
		return
	}
	export := h.Diagrams.ExportFile
	if text {
		export = h.Diagrams.ExportFileText
	}
	out, err := export(r.Context(), uid, token, id)
	if err != nil {
		dgError(w, r, err)
		return
	}
	sddHeaders(w, sddFileName(out.Name, "file"))
	if text {
		w.Header().Set("Content-Type", application.SDDTextMime)
	}
	etag(w, out.Version)
	_, _ = w.Write(out.Data)
}

// dgExportProject downloads a whole project (folders and files) as one .sdd
// container of type "project". Auth: viewer, or X-Share-Token of a
// project-wide link.
//
// spector:tags diagrams
func (h *Handler) dgExportProject(w http.ResponseWriter, r *http.Request) {
	uid, token, ok := h.dgActor(w, r)
	if !ok {
		return
	}
	id, ok := dgID(w, r, "id")
	if !ok {
		return
	}
	if t := r.URL.Query().Get("type"); t != "" && t != "project" {
		dgValidation(w, r, "querystring", "type: must be project")
		return
	}
	out, err := h.Diagrams.ExportProject(r.Context(), uid, token, id)
	if err != nil {
		dgError(w, r, err)
		return
	}
	sddHeaders(w, sddFileName(out.Name, "project"))
	_, _ = w.Write(out.Data)
}

// readSDD reads the "file" field of a multipart upload.
func (h *Handler) readSDD(w http.ResponseWriter, r *http.Request) (name string, data []byte, ok bool) {
	limit := h.Diagrams.SDDMaxBytes()
	r.Body = http.MaxBytesReader(w, r.Body, limit+smallBody)
	mr, err := r.MultipartReader()
	if err != nil {
		dgError(w, r, diagram.UnsupportedMedia(`Send multipart/form-data with a "file" field`))
		return
	}
	part, err := mr.NextPart()
	if err != nil || part.FormName() != "file" {
		if tooLarge(err) {
			dgError(w, r, diagram.TooLarge("The file is too large"))
			return
		}
		dgError(w, r, diagram.MissingFile())
		return
	}
	data, err = readPart(part, limit)
	switch {
	case errors.Is(err, errPartTooLarge) || tooLarge(err):
		dgError(w, r, diagram.TooLarge("The file is too large"))
		return
	case err != nil:
		dgFail(w, r, http.StatusBadRequest, "INVALID_MULTIPART", "Request body is not valid multipart/form-data", nil)
		return
	}
	return part.FileName(), data, true
}

func (h *Handler) importPrologue(w http.ResponseWriter, r *http.Request) (int, string, []byte, bool) {
	uid, ok := h.dgUser(w, r)
	if !ok || !h.dgAllow(w, r, "dg-import", 30, time.Minute) {
		return 0, "", nil, false
	}
	name, data, ok := h.readSDD(w, r)
	return uid, name, data, ok
}

func importedProject(res application.SDDImport) map[string]any {
	return map[string]any{"project": projectJSON(res.Project), "folders": foldersJSON(res.Folders), "files": filesJSON(res.Files), "warnings": warnList(res.Warnings)}
}

func warnList(w []string) []string {
	if w == nil {
		return []string{}
	}
	return w
}

// dgImport creates a NEW project from any .sdd: a file container (a project
// with one file) or a project container (with its folders and files). The
// content is sniffed, not the extension; the legacy .sddproj opens too.
// Multipart field "file". Response 201: {"kind": "file", project, file,
// warnings} with an ETag, or {"kind": "project", project, folders, files,
// warnings}. A malformed file is 422 SDD_INVALID with {reason, entry}.
//
// spector:tags diagrams
func (h *Handler) dgImport(w http.ResponseWriter, r *http.Request) {
	uid, name, data, ok := h.importPrologue(w, r)
	if !ok {
		return
	}
	res, err := h.Diagrams.ImportAny(r.Context(), uid, name, data)
	if err != nil {
		sddFail(w, r, err)
		return
	}
	if res.Kind == string(sddfile.KindProject) {
		body := importedProject(res)
		body["kind"] = res.Kind
		dgWrite(w, http.StatusCreated, body)
		return
	}
	etag(w, res.File.Version)
	dgWrite(w, http.StatusCreated, map[string]any{"kind": res.Kind, "project": projectJSON(res.Project), "file": docJSON(res.File), "warnings": warnList(res.Warnings)})
}

// dgImportOne creates a NEW project with one file from a single-file .sdd.
// A project container is 422 SDD_INVALID (use POST /import). Response 201:
// {"project", "file", "warnings"}.
//
// spector:tags diagrams
func (h *Handler) dgImportOne(w http.ResponseWriter, r *http.Request) {
	uid, name, data, ok := h.importPrologue(w, r)
	if !ok {
		return
	}
	res, err := h.Diagrams.ImportFileAsProject(r.Context(), uid, name, data)
	if err != nil {
		sddFail(w, r, err)
		return
	}
	etag(w, res.File.Version)
	dgWrite(w, http.StatusCreated, map[string]any{"project": projectJSON(res.Project), "file": docJSON(res.File), "warnings": warnList(res.Warnings)})
}

// dgImportProject creates a NEW project from a project container (or the
// legacy .sddproj). A single-file .sdd is 422 SDD_INVALID. Response 201:
// {"project", "folders", "files", "warnings"}.
//
// spector:tags diagrams
func (h *Handler) dgImportProject(w http.ResponseWriter, r *http.Request) {
	uid, name, data, ok := h.importPrologue(w, r)
	if !ok {
		return
	}
	res, err := h.Diagrams.ImportProject(r.Context(), uid, name, data)
	if err != nil {
		sddFail(w, r, err)
		return
	}
	dgWrite(w, http.StatusCreated, importedProject(res))
}

// dgImportInto adds a single-file .sdd as a new file of a project, in the
// folder ?folderId= when given. Response 201: {"file", "warnings"}.
// Auth: editor.
//
// spector:tags diagrams
func (h *Handler) dgImportInto(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok || !h.dgAllow(w, r, "dg-import", 30, time.Minute) {
		return
	}
	var folder *string
	if f := r.URL.Query().Get("folderId"); f != "" {
		if len(f) > 64 {
			dgValidation(w, r, "querystring", "folderId: must be 1-64 characters")
			return
		}
		folder = &f
	}
	name, data, ok := h.readSDD(w, r)
	if !ok {
		return
	}
	res, err := h.Diagrams.ImportIntoProject(r.Context(), uid, id, folder, name, data)
	if err != nil {
		sddFail(w, r, err)
		return
	}
	etag(w, res.File.Version)
	dgWrite(w, http.StatusCreated, map[string]any{"file": docJSON(res.File), "warnings": warnList(res.Warnings)})
}
