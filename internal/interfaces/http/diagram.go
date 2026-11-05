package http

// The diagram API under /api/diagrams. It is a port of the Node diagram
// server, and clients written for that server must keep working: bodies are
// bare JSON objects (no {"data": ...} envelope), field names are camelCase,
// times are epoch milliseconds, ids are strings and errors are
// {"error": {"code", "message", "details"}}.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/iBlog/iblog-monolith-go/internal/application"
	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
	"github.com/iBlog/iblog-monolith-go/internal/interfaces/http/middleware"
)

// DiagramPrefix is where the diagram API is mounted.
const DiagramPrefix = "/api/diagrams"

// diagramRoutes registers the diagram API; without Diagrams it is off.
func (h *Handler) diagramRoutes(root *http.ServeMux) {
	if h.Diagrams == nil {
		return
	}
	h.mqttRoutes(root)
	mux := dgRouter{root}
	p := DiagramPrefix
	mux.HandleFunc("GET "+p+"/projects", h.dgListProjects)
	mux.HandleFunc("POST "+p+"/projects", h.dgCreateProject)
	mux.HandleFunc("GET "+p+"/projects/{id}", h.dgGetProject)
	mux.HandleFunc("PATCH "+p+"/projects/{id}", h.dgRenameProject)
	mux.HandleFunc("DELETE "+p+"/projects/{id}", h.dgDeleteProject)
	mux.HandleFunc("GET "+p+"/projects/{id}/tree", h.dgTree)
	mux.HandleFunc("POST "+p+"/projects/{id}/folders", h.dgCreateFolder)
	mux.HandleFunc("POST "+p+"/projects/{id}/files", h.dgCreateFile)

	mux.HandleFunc("PATCH "+p+"/folders/{id}", h.dgUpdateFolder)
	mux.HandleFunc("DELETE "+p+"/folders/{id}", h.dgDeleteFolder)
	mux.HandleFunc("POST "+p+"/folders/{id}/duplicate", h.dgDuplicateFolder)

	mux.HandleFunc("GET "+p+"/files/{id}", h.dgGetFile)
	mux.HandleFunc("PUT "+p+"/files/{id}", h.dgSaveFile)
	mux.HandleFunc("PATCH "+p+"/files/{id}", h.dgUpdateFile)
	mux.HandleFunc("DELETE "+p+"/files/{id}", h.dgDeleteFile)
	mux.HandleFunc("POST "+p+"/files/{id}/duplicate", h.dgDuplicateFile)
	mux.HandleFunc("GET "+p+"/files/{id}/versions", h.dgVersions)
	mux.HandleFunc("POST "+p+"/files/{id}/versions", h.dgNameVersion)
	mux.HandleFunc("GET "+p+"/files/{id}/versions/{v}", h.dgVersion)
	mux.HandleFunc("POST "+p+"/files/{id}/versions/{v}/restore", h.dgRestore)

	mux.HandleFunc("GET "+p+"/me/prefs", h.dgGetPrefs)
	mux.HandleFunc("PATCH "+p+"/me/prefs", h.dgPatchPrefs)

	h.tokenRoutes(mux)
	h.mcpRoutes(mux)
	h.schemaRoutes(mux)
	h.sddRoutes(mux)
	h.sharingRoutes(mux)
	h.publishRoutes(mux)
	h.commentRoutes(mux)
	h.realtimeRoutes(mux)
}

// dgRouter registers the diagram routes. Every handler sees the caller's
// realtime connection id (X-Client-Id) in its context, so the events a
// request causes skip that connection.
type dgRouter struct{ mux *http.ServeMux }

func (m dgRouter) HandleFunc(pattern string, f func(http.ResponseWriter, *http.Request)) {
	m.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		if id := r.Header.Get("X-Client-Id"); id != "" {
			r = r.WithContext(application.WithClientID(r.Context(), id))
		}
		f(w, r)
	})
}

// Wire shapes

func ms(t time.Time) int64 { return t.UnixMilli() }

// userRef is a user id as the API shows it: a string, null when unknown.
func userRef(id int) *string {
	if id == 0 {
		return nil
	}
	s := strconv.Itoa(id)
	return &s
}

// actorRef is who made a change as the API shows it: the user's id, or
// "share:<id>" for a save through a share link, null when unknown.
func actorRef(user int, share string) *string {
	if share != "" {
		s := "share:" + share
		return &s
	}
	return userRef(user)
}

type dgProject struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Role         string  `json:"role"`
	TreeRev      int     `json:"treeRev"`
	CreatedAt    int64   `json:"createdAt"`
	UpdatedAt    int64   `json:"updatedAt"`
	UpdatedBy    *string `json:"updatedBy"`
	FileCount    int     `json:"fileCount"`
	FolderCount  int     `json:"folderCount"`
	ThumbnailURL *string `json:"thumbnailUrl"`
	OwnerID      string  `json:"ownerId,omitempty"` // left out for share links
}

func projectJSON(p diagram.ProjectSummary) dgProject {
	var thumb *string // the preview of the project's first file
	if p.ThumbnailHash != nil && p.ThumbFileID != "" {
		u := thumbURL(p.ThumbFileID, *p.ThumbnailHash)
		thumb = &u
	}
	return dgProject{ID: p.ID, Name: p.Name, Role: string(p.Role), TreeRev: p.TreeRev, CreatedAt: ms(p.CreatedAt),
		UpdatedAt: ms(p.UpdatedAt), UpdatedBy: userRef(p.UpdatedBy), FileCount: p.FileCount, FolderCount: p.FolderCount,
		ThumbnailURL: thumb, OwnerID: strconv.Itoa(p.OwnerID)}
}

type dgFolder struct {
	ID        string  `json:"id"`
	ProjectID string  `json:"projectId"`
	ParentID  *string `json:"parentId"`
	Name      string  `json:"name"`
	Position  float64 `json:"position"`
	CreatedAt int64   `json:"createdAt"`
	UpdatedAt int64   `json:"updatedAt"`
}

func folderJSON(f diagram.Folder) dgFolder {
	return dgFolder{f.ID, f.ProjectID, f.ParentID, f.Name, f.Position, ms(f.CreatedAt), ms(f.UpdatedAt)}
}

func foldersJSON(fs []diagram.Folder) []dgFolder {
	out := make([]dgFolder, len(fs))
	for i, f := range fs {
		out[i] = folderJSON(f)
	}
	return out
}

type dgFile struct {
	ID           string          `json:"id"`
	ProjectID    string          `json:"projectId"`
	FolderID     *string         `json:"folderId"`
	Name         string          `json:"name"`
	Kind         string          `json:"kind"`
	Position     float64         `json:"position"`
	Version      int             `json:"version"`
	PageCount    int             `json:"pageCount"`
	ThumbnailURL *string         `json:"thumbnailUrl"`
	CreatedAt    int64           `json:"createdAt"`
	UpdatedAt    int64           `json:"updatedAt"`
	UpdatedBy    *string         `json:"updatedBy"`
	Role         string          `json:"role,omitempty"`
	Data         json.RawMessage `json:"data,omitempty"`
}

func fileJSON(f diagram.File, role diagram.Role) dgFile {
	var thumb *string
	if f.ThumbnailHash != nil {
		u := thumbURL(f.ID, *f.ThumbnailHash)
		thumb = &u
	}
	return dgFile{ID: f.ID, ProjectID: f.ProjectID, FolderID: f.FolderID, Name: f.Name, Kind: f.Kind, Position: f.Position,
		Version: f.Version, PageCount: f.PageCount, ThumbnailURL: thumb, CreatedAt: ms(f.CreatedAt), UpdatedAt: ms(f.UpdatedAt),
		UpdatedBy: actorRef(f.UpdatedBy, f.UpdatedByShare), Role: string(role)}
}

// thumbURL is where a file's preview is served; ?v= changes with the image,
// so a cached preview is never stale for long.
func thumbURL(fileID, hash string) string {
	return DiagramPrefix + "/files/" + fileID + "/thumbnail?v=" + hash[:min(12, len(hash))]
}

func filesJSON(fs []diagram.File) []dgFile {
	out := make([]dgFile, len(fs))
	for i, f := range fs {
		out[i] = fileJSON(f, "")
	}
	return out
}

// docJSON is a file with its document. The document always carries the
// file's identity: a rename through the tree does not rewrite it.
func docJSON(f application.DiagramFile) dgFile {
	out := fileJSON(f.File, f.Role)
	out.Data = withIdentity(f.Data, f.ID, f.Name)
	return out
}

func withIdentity(data json.RawMessage, id, name string) json.RawMessage {
	var doc map[string]json.RawMessage
	if json.Unmarshal(data, &doc) != nil {
		return data
	}
	doc["id"], _ = json.Marshal(id)
	doc["name"], _ = json.Marshal(name)
	out, err := json.Marshal(doc)
	if err != nil {
		return data
	}
	return out
}

type dgVersion struct {
	Version   int             `json:"version"`
	Name      string          `json:"name"`
	Label     *string         `json:"label"`
	Named     bool            `json:"named"`
	CreatedBy *string         `json:"createdBy"`
	Author    string          `json:"createdByName,omitempty"`
	CreatedAt int64           `json:"createdAt"`
	Data      json.RawMessage `json:"data,omitempty"`
}

func versionJSON(v diagram.Version, withData bool) dgVersion {
	out := dgVersion{v.Version, v.Name, v.Label, v.Named, actorRef(v.CreatedBy, v.CreatedByShare), v.CreatedByName, ms(v.CreatedAt), nil}
	if withData {
		out.Data = v.Data
	}
	return out
}

// Responses and errors

func dgWrite(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		zap.L().Warn("write json", zap.Error(err))
	}
}

func etag(w http.ResponseWriter, version int) {
	w.Header().Set("ETag", `"`+strconv.Itoa(version)+`"`)
}

type dgErrorBody struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		Details   any    `json:"details,omitempty"`
		RequestID string `json:"requestId,omitempty"`
	} `json:"error"`
}

// dgFail writes an error in the diagram API's format.
func dgFail(w http.ResponseWriter, r *http.Request, status int, code, msg string, details any) {
	var b dgErrorBody
	b.Error.Code, b.Error.Message, b.Error.Details = code, msg, details
	b.Error.RequestID = middleware.RequestIDFrom(r.Context())
	dgWrite(w, status, b)
}

// dgValidation is a 400 with the Node server's details shape: one issue
// per "path: message".
func dgValidation(w http.ResponseWriter, r *http.Request, in, msg string) {
	path, text, ok := strings.Cut(msg, ": ")
	if !ok {
		path, text = "", msg
	}
	dgFail(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Request validation failed",
		[]map[string]string{{"in": in, "path": path, "message": text, "code": "custom"}})
}

// dgError maps a use case error to a response.
func dgError(w http.ResponseWriter, r *http.Request, err error) {
	var e *diagram.Error
	var lc *diagram.LibraryConflict
	var invalid *domain.ValidationError
	switch {
	case errors.As(err, &lc):
		dgLibraryConflict(w, r, lc)
	case errors.As(err, &e):
		switch {
		case errors.Is(err, domain.ErrNotFound):
			dgFail(w, r, http.StatusNotFound, e.Code, e.Msg, nil)
		case errors.Is(err, domain.ErrForbidden):
			dgFail(w, r, http.StatusForbidden, e.Code, e.Msg, nil)
		case errors.Is(err, domain.ErrConflict):
			dgFail(w, r, http.StatusConflict, e.Code, e.Msg, nil)
		case errors.Is(err, diagram.ErrPrecondition):
			etag(w, e.Current)
			dgFail(w, r, http.StatusPreconditionFailed, e.Code, e.Msg, map[string]any{"currentVersion": e.Current, "restored": e.Restored})
		case errors.Is(err, diagram.ErrTooLarge):
			dgFail(w, r, http.StatusRequestEntityTooLarge, e.Code, e.Msg, nil)
		case errors.Is(err, diagram.ErrGone):
			dgFail(w, r, http.StatusGone, e.Code, e.Msg, nil)
		case errors.Is(err, diagram.ErrUnsupportedMedia):
			dgFail(w, r, http.StatusUnsupportedMediaType, e.Code, e.Msg, nil)
		case e.Code == "VALIDATION_ERROR":
			dgValidation(w, r, "body", e.Msg)
		default:
			dgFail(w, r, http.StatusBadRequest, e.Code, e.Msg, nil)
		}
	case errors.As(err, &invalid):
		dgValidation(w, r, "body", invalid.Msg)
	case errors.Is(err, domain.ErrUnauthorized):
		dgFail(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication required", nil)
	default:
		zap.L().Error("request failed", zap.Error(err))
		dgFail(w, r, http.StatusInternalServerError, "INTERNAL", "Internal server error", nil)
	}
}

// dgUser is the caller's id, or a 401.
func (h *Handler) dgUser(w http.ResponseWriter, r *http.Request) (int, bool) {
	id := h.viewerID(r)
	if id == 0 {
		dgFail(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication required", nil)
	}
	return id, id != 0
}

// dgReq is the caller's id and the {id} path value, or the error response.
func (h *Handler) dgReq(w http.ResponseWriter, r *http.Request) (int, string, bool) {
	uid, ok := h.dgUser(w, r)
	if !ok {
		return 0, "", false
	}
	id, ok := dgID(w, r, "id")
	return uid, id, ok
}

// dgID reads a path id (1-64 characters).
func dgID(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
	id := r.PathValue(name)
	if id == "" || len(id) > 64 {
		dgValidation(w, r, "params", name+": must be 1-64 characters")
		return "", false
	}
	return id, true
}

// dgDecode reads a JSON body of at most limit bytes into v. An empty body
// is allowed when optional.
func dgDecode(w http.ResponseWriter, r *http.Request, v any, limit int64, optional bool) bool {
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit)).Decode(v)
	var tooBig *http.MaxBytesError
	var syntax *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case err == nil, optional && errors.Is(err, io.EOF):
		return true
	case errors.As(err, &tooBig):
		dgFail(w, r, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "Request body or file is too large", nil)
	case errors.As(err, &typ):
		dgValidation(w, r, "body", typ.Field+": expected "+typ.Type.String())
	case errors.As(err, &syntax), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		dgFail(w, r, http.StatusBadRequest, "INVALID_JSON", "Request body is not valid JSON", nil)
	default:
		dgValidation(w, r, "body", err.Error())
	}
	return false
}

const smallBody = 1 << 20

// docLimit is the body limit of routes that carry a document.
func (h *Handler) docLimit() int64 { return int64(h.Diagrams.MaxDataBytes) + smallBody }

// Projects

// dgListProjects pages through the caller's projects.
//
// Query: page (1+), pageSize (1-100, default 20), q, sort (updated|name),
// order (asc|desc). Response: {"items", "total", "page", "pageSize"}.
//
// spector:tags diagrams
func (h *Handler) dgListProjects(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.dgUser(w, r)
	if !ok {
		return
	}
	q, ok := dgProjectQuery(w, r)
	if !ok {
		return
	}
	page, err := h.Diagrams.ListProjects(r.Context(), uid, q)
	if err != nil {
		dgError(w, r, err)
		return
	}
	items := make([]dgProject, len(page.Items))
	for i, p := range page.Items {
		items[i] = projectJSON(p)
	}
	dgWrite(w, http.StatusOK, map[string]any{"items": items, "total": page.Total, "page": page.Page, "pageSize": page.PageSize})
}

func dgProjectQuery(w http.ResponseWriter, r *http.Request) (diagram.ProjectQuery, bool) {
	v := r.URL.Query()
	q := diagram.ProjectQuery{Page: 1, PageSize: 20, Search: strings.TrimSpace(v.Get("q"))}
	bad := func(path, msg string) (diagram.ProjectQuery, bool) {
		dgValidation(w, r, "querystring", path+": "+msg)
		return q, false
	}
	if s := v.Get("page"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			return bad("page", "must be an integer >= 1")
		}
		q.Page = n
	}
	if s := v.Get("pageSize"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 100 {
			return bad("pageSize", "must be an integer between 1 and 100")
		}
		q.PageSize = n
	}
	if len([]rune(q.Search)) > 100 {
		return bad("q", "must be at most 100 characters")
	}
	switch v.Get("sort") {
	case "", "updated":
	case "name":
		q.SortByName = true
	default:
		return bad("sort", "must be updated or name")
	}
	switch v.Get("order") {
	case "":
		q.Asc = q.SortByName
	case "asc":
		q.Asc = true
	case "desc":
	default:
		return bad("order", "must be asc or desc")
	}
	return q, true
}

type dgFileBody struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Data json.RawMessage `json:"data"`
}

// dgCreateProject creates a project together with its first file.
//
// Body: {"id"?, "name"?, "file"?: {"id"?, "name"?, "data"?}, "data"?}. A
// client-chosen id (6-64 of A-Z a-z 0-9 _ -) makes the call idempotent: a
// replay answers 200 instead of 201. Response: {"project", "file"} with
// the file's version in ETag.
//
// spector:tags diagrams
func (h *Handler) dgCreateProject(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.dgUser(w, r)
	if !ok {
		return
	}
	var b struct {
		dgFileBody
		File *dgFileBody `json:"file"`
	}
	if !dgDecode(w, r, &b, h.docLimit(), false) {
		return
	}
	in := application.NewDiagramProject{ID: b.ID, Name: b.Name, Data: b.Data}
	if b.File != nil {
		in.File = &application.NewDiagramFile{ID: b.File.ID, Name: b.File.Name, Data: b.File.Data}
	}
	res, err := h.Diagrams.CreateProject(r.Context(), uid, in)
	if err != nil {
		dgError(w, r, err)
		return
	}
	code := http.StatusCreated
	if !res.Created {
		code = http.StatusOK
	}
	etag(w, res.File.Version)
	w.Header().Set("Location", DiagramPrefix+"/projects/"+url.PathEscape(res.Project.ID))
	dgWrite(w, code, map[string]any{"project": projectJSON(res.Project), "file": docJSON(res.File)})
}

// dgGetProject returns a project without its tree.
//
// spector:tags diagrams
func (h *Handler) dgGetProject(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	p, err := h.Diagrams.GetProject(r.Context(), uid, id)
	if err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusOK, map[string]any{"project": projectJSON(p)})
}

// dgRenameProject renames a project. Body: {"name"}. Auth: editor.
//
// spector:tags diagrams
func (h *Handler) dgRenameProject(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	var b struct {
		Name string `json:"name"`
	}
	if !dgDecode(w, r, &b, smallBody, false) {
		return
	}
	p, err := h.Diagrams.RenameProject(r.Context(), uid, id, b.Name)
	if err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusOK, map[string]any{"project": projectJSON(p)})
}

// dgDeleteProject deletes a project with all its folders and files.
// Auth: owner.
//
// spector:tags diagrams
func (h *Handler) dgDeleteProject(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	if err := h.Diagrams.DeleteProject(r.Context(), uid, id); err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusNoContent, nil)
}

// dgTree returns the project tree: folders and file metadata (no
// documents) plus treeRev.
//
// spector:tags diagrams
func (h *Handler) dgTree(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	t, err := h.Diagrams.Tree(r.Context(), uid, id)
	if err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusOK, map[string]any{"project": projectJSON(t.Project), "treeRev": t.Project.TreeRev,
		"folders": foldersJSON(t.Folders), "files": filesJSON(t.Files)})
}

// Folders

// dgCreateFolder adds a folder. Body: {"id"?, "name", "parentId"?,
// "position"?}. Auth: editor.
//
// spector:tags diagrams
func (h *Handler) dgCreateFolder(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	var b struct {
		ID       string   `json:"id"`
		Name     string   `json:"name"`
		ParentID *string  `json:"parentId"`
		Position *float64 `json:"position"`
	}
	if !dgDecode(w, r, &b, smallBody, false) {
		return
	}
	f, created, err := h.Diagrams.CreateFolder(r.Context(), uid, id, application.NewFolder{ID: b.ID, Name: b.Name, ParentID: b.ParentID, Position: b.Position})
	if err != nil {
		dgError(w, r, err)
		return
	}
	code := http.StatusCreated
	if !created {
		code = http.StatusOK
	}
	dgWrite(w, code, map[string]any{"folder": folderJSON(f)})
}

// dgUpdateFolder renames and/or moves a folder (409 CYCLE when moved into
// itself or a descendant). Body: {"name"?, "parentId"?, "position"?}.
//
// spector:tags diagrams
func (h *Handler) dgUpdateFolder(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	var b struct {
		Name     *string              `json:"name"`
		ParentID diagram.Opt[*string] `json:"parentId"`
		Position *float64             `json:"position"`
	}
	if !dgDecode(w, r, &b, smallBody, false) {
		return
	}
	f, err := h.Diagrams.UpdateFolder(r.Context(), uid, id, application.FolderPatch{Name: b.Name, ParentID: b.ParentID, Position: b.Position})
	if err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusOK, map[string]any{"folder": folderJSON(f)})
}

// dgDeleteFolder deletes a folder with everything inside it. Response:
// {"deletedFolders", "deletedFiles"}.
//
// spector:tags diagrams
func (h *Handler) dgDeleteFolder(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	folders, files, err := h.Diagrams.DeleteFolder(r.Context(), uid, id)
	if err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusOK, map[string]int{"deletedFolders": folders, "deletedFiles": files})
}

// dgDuplicateFolder deep-copies a folder next to the original. Response:
// {"folder", "folders", "files"}.
//
// spector:tags diagrams
func (h *Handler) dgDuplicateFolder(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	c, err := h.Diagrams.DuplicateFolder(r.Context(), uid, id)
	if err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusCreated, map[string]any{"folder": folderJSON(c.Folder), "folders": foldersJSON(c.Folders), "files": filesJSON(c.Files)})
}

// Files

// dgCreateFile adds a file. Body: {"id"?, "name", "kind"?, "folderId"?,
// "position"?, "data"?}. Auth: editor.
//
// spector:tags diagrams
func (h *Handler) dgCreateFile(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok || !h.dgAllow(w, r, "dg-file-create", 120, time.Minute) {
		return
	}
	var b struct {
		dgFileBody
		Kind     string   `json:"kind"`
		FolderID *string  `json:"folderId"`
		Position *float64 `json:"position"`
	}
	if !dgDecode(w, r, &b, h.docLimit(), false) {
		return
	}
	f, created, err := h.Diagrams.CreateFile(r.Context(), uid, id, application.NewDiagramFile{
		ID: b.ID, Name: b.Name, Kind: b.Kind, FolderID: b.FolderID, Position: b.Position, Data: b.Data})
	if err != nil {
		dgError(w, r, err)
		return
	}
	code := http.StatusCreated
	if !created {
		code = http.StatusOK
	}
	etag(w, f.Version)
	dgWrite(w, code, map[string]any{"file": docJSON(f)})
}

// dgGetFile returns a file with its document; ETag is its version. Besides
// a session, an X-Share-Token header opens the file with the link's role.
//
// spector:tags diagrams
func (h *Handler) dgGetFile(w http.ResponseWriter, r *http.Request) {
	uid, token, ok := h.dgActor(w, r)
	if !ok {
		return
	}
	id, ok := dgID(w, r, "id")
	if !ok {
		return
	}
	f, err := h.Diagrams.GetFileVia(r.Context(), uid, token, id)
	if err != nil {
		dgError(w, r, err)
		return
	}
	etag(w, f.Version)
	dgWrite(w, http.StatusOK, map[string]any{"file": docJSON(f)})
}

// dgSaveFile saves a file. Body: {"name"?, "data"?} (at least one).
// Requires If-Match: "<version>"; 412 VERSION_CONFLICT (or
// IF_MATCH_REQUIRED) with {"currentVersion"} and the current ETag.
//
// spector:tags diagrams
func (h *Handler) dgSaveFile(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	var b struct {
		Name *string         `json:"name"`
		Data json.RawMessage `json:"data"`
	}
	if !dgDecode(w, r, &b, h.docLimit(), false) {
		return
	}
	var label *string
	if tokenFrom(r.Context()) != nil {
		l := "Saved through the API"
		label = &l
	}
	f, err := h.Diagrams.SaveFileLabeled(r.Context(), uid, id, b.Name, b.Data, r.Header.Get("If-Match"), label)
	if err != nil {
		dgError(w, r, err)
		return
	}
	etag(w, f.Version)
	dgWrite(w, http.StatusOK, map[string]any{"file": docJSON(f)})
}

// dgUpdateFile renames and/or moves a file; there is no If-Match, tree
// operations are last-writer-wins. Body: {"name"?, "folderId"?,
// "position"?, "kind"?}. Response: {"file"} without the document.
//
// spector:tags diagrams
func (h *Handler) dgUpdateFile(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	var b struct {
		Name     *string              `json:"name"`
		FolderID diagram.Opt[*string] `json:"folderId"`
		Position *float64             `json:"position"`
		Kind     *string              `json:"kind"`
	}
	if !dgDecode(w, r, &b, smallBody, false) {
		return
	}
	f, role, err := h.Diagrams.UpdateFileMeta(r.Context(), uid, id, application.FilePatch{Name: b.Name, FolderID: b.FolderID, Position: b.Position, Kind: b.Kind})
	if err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusOK, map[string]any{"file": fileJSON(f, role)})
}

// dgDeleteFile deletes a file with its versions.
//
// spector:tags diagrams
func (h *Handler) dgDeleteFile(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	if err := h.Diagrams.DeleteFile(r.Context(), uid, id); err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusNoContent, nil)
}

// dgDuplicateFile copies a file next to the original, or into
// {"folderId"}. The body is optional. Auth: editor of the project.
//
// spector:tags diagrams
func (h *Handler) dgDuplicateFile(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	var b struct {
		FolderID diagram.Opt[*string] `json:"folderId"`
	}
	if !dgDecode(w, r, &b, smallBody, true) {
		return
	}
	f, err := h.Diagrams.DuplicateFile(r.Context(), uid, id, b.FolderID)
	if err != nil {
		dgError(w, r, err)
		return
	}
	etag(w, f.Version)
	dgWrite(w, http.StatusCreated, map[string]any{"file": docJSON(f)})
}

// Versions

// dgVersions lists a file's snapshots, newest first.
//
// spector:tags diagrams
func (h *Handler) dgVersions(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	vs, err := h.Diagrams.Versions(r.Context(), uid, id)
	if err != nil {
		dgError(w, r, err)
		return
	}
	items := make([]dgVersion, len(vs))
	for i, v := range vs {
		items[i] = versionJSON(v, false)
	}
	dgWrite(w, http.StatusOK, map[string]any{"items": items})
}

// dgVersionNumber reads the {v} path value.
func dgVersionNumber(w http.ResponseWriter, r *http.Request) (int, bool) {
	n, err := strconv.Atoi(r.PathValue("v"))
	if err != nil || n < 1 {
		dgValidation(w, r, "params", "v: must be an integer >= 1")
		return 0, false
	}
	return n, true
}

// dgVersion returns one snapshot with its document.
//
// spector:tags diagrams
func (h *Handler) dgVersion(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	n, ok := dgVersionNumber(w, r)
	if !ok {
		return
	}
	v, err := h.Diagrams.Version(r.Context(), uid, id, n)
	if err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusOK, map[string]any{"version": versionJSON(v, true)})
}

// dgNameVersion saves the file as it is now under a name ("Save
// version..."). Body: {"name"} (1-120). Named versions are never pruned.
// Auth: editor.
//
// spector:tags diagrams
func (h *Handler) dgNameVersion(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	var b struct {
		Name string `json:"name"`
	}
	if !dgDecode(w, r, &b, smallBody, false) {
		return
	}
	v, err := h.Diagrams.SaveNamedVersion(r.Context(), uid, id, b.Name)
	if err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusCreated, map[string]any{"version": versionJSON(v, false)})
}

// dgRestore restores a snapshot as a NEW version. Response: {"file"}.
// Auth: editor.
//
// spector:tags diagrams
func (h *Handler) dgRestore(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	n, ok := dgVersionNumber(w, r)
	if !ok {
		return
	}
	f, err := h.Diagrams.RestoreVersion(r.Context(), uid, id, n)
	if err != nil {
		dgError(w, r, err)
		return
	}
	etag(w, f.Version)
	dgWrite(w, http.StatusOK, map[string]any{"file": docJSON(f)})
}

// Preferences

// dgGetPrefs returns the caller's preferences: {"prefs": {...}}.
//
// spector:tags diagrams
func (h *Handler) dgGetPrefs(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.dgUser(w, r)
	if !ok {
		return
	}
	prefs, err := h.Diagrams.GetPrefs(r.Context(), uid)
	if err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusOK, map[string]any{"prefs": prefs})
}

// dgPatchPrefs merges top-level keys into the preferences; a null value
// deletes the key. At most 64 KiB in total (413).
//
// spector:tags diagrams
func (h *Handler) dgPatchPrefs(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.dgUser(w, r)
	if !ok {
		return
	}
	var patch json.RawMessage
	if !dgDecode(w, r, &patch, diagram.MaxPrefsBytes, false) {
		return
	}
	prefs, err := h.Diagrams.PatchPrefs(r.Context(), uid, patch)
	if err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusOK, map[string]any{"prefs": prefs})
}
