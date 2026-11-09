package http

// The diagrams section of the admin API under /api/admin/diagrams. Unlike
// the public /api/diagrams API it follows the blog's conventions: {data,
// meta} bodies, snake_case fields, RFC 3339 times, RFC 9457 problems and 204
// for writes without a body. Permissions: diagrams.read (or diagrams.manage)
// to look, diagrams.manage to delete and revoke, templates.manage for the
// gallery.

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
)

func (h *Handler) adminDiagramRoutes(mux *http.ServeMux) {
	p := "/api/admin/diagrams"
	mux.HandleFunc("GET "+p+"/stats", h.adminDiagramStats)
	mux.HandleFunc("GET "+p+"/projects", h.adminDiagramProjects)
	mux.HandleFunc("GET "+p+"/projects/{id}", h.adminDiagramProject)
	mux.HandleFunc("DELETE "+p+"/projects/{id}", h.adminDeleteDiagramProject)
	mux.HandleFunc("POST "+p+"/shares/{shareId}/revoke", h.adminRevokeDiagramShare)
	mux.HandleFunc("GET "+p+"/templates", h.adminDiagramTemplates)
	mux.HandleFunc("POST "+p+"/templates", h.adminCreateDiagramTemplate)
	mux.HandleFunc("POST "+p+"/templates/reorder", h.adminReorderDiagramTemplates)
	mux.HandleFunc("PATCH "+p+"/templates/{id}", h.adminUpdateDiagramTemplate)
	mux.HandleFunc("DELETE "+p+"/templates/{id}", h.adminDeleteDiagramTemplate)
}

// adminActor is the caller's id, or a 401. The permission itself is checked
// by the use case.
func (h *Handler) adminActor(w http.ResponseWriter, r *http.Request) (int, bool) {
	id := h.viewerID(r)
	if id == 0 {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
	}
	return id, id != 0
}

// diagramProblem answers err as problem details; a diagram error names what
// was missing in its 404 detail.
func diagramProblem(w http.ResponseWriter, r *http.Request, err error) {
	notFound := ""
	var e *diagram.Error
	if errors.As(err, &e) {
		notFound = e.Msg
	}
	respond(w, r, 0, nil, err, notFound)
}

// rfc3339 formats a time in UTC.
func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func rfc3339Ptr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := rfc3339(*t)
	return &s
}

// assetThumb is the URL of a preview by its hash: assets are served to
// anyone, so an <img> in the admin needs no credentials.
func assetThumb(hash *string) *string {
	if hash == nil {
		return nil
	}
	u := DiagramPrefix + "/assets/" + *hash
	return &u
}

type adminUserJSON struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

func adminUser(u diagram.AdminUser) adminUserJSON {
	return adminUserJSON{ID: strconv.Itoa(u.ID), Username: u.Username}
}

// adminDiagramStats returns the workspace totals.
//
// Response: {"data": {projects, files, files_by_kind: {sdd, erd, uml,
// board}, active_editors_7d, storage_bytes}}. active_editors_7d is the
// distinct people who saved an edit in the last 7 days.
// Auth: diagrams.read.
//
// spector:tags admin diagrams
func (h *Handler) adminDiagramStats(w http.ResponseWriter, r *http.Request) {
	if h.Diagrams == nil {
		writeError(w, r, http.StatusNotFound, "no such endpoint")
		return
	}
	if _, ok := h.adminActor(w, r); !ok {
		return
	}
	st, err := h.Diagrams.AdminStats(r.Context())
	if err != nil {
		diagramProblem(w, r, err)
		return
	}
	kinds := map[string]int{"sdd": 0, "erd": 0, "uml": 0, "board": 0}
	for k, n := range st.FilesByKind {
		kinds[k] = n
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": st.Projects, "files": st.Files, "files_by_kind": kinds,
		"active_editors_7d": st.ActiveEditors7d, "storage_bytes": st.StorageBytes})
}

type adminProjectJSON struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Owner        adminUserJSON `json:"owner"`
	Files        int           `json:"files"`
	UpdatedAt    string        `json:"updated_at"`
	ThumbnailURL *string       `json:"thumbnail_url"`
	Shared       bool          `json:"shared"`
}

// adminDiagramProjects lists every project, last updated first.
//
// Query: q (substring of the name), owner (exact username), cursor, limit
// (default 10, max 50). Response: {"data": [{id, name, owner: {id,
// username}, files, updated_at, thumbnail_url, shared}], "meta": {limit,
// has_more, next_cursor}}. shared: at least one active share link.
// Auth: diagrams.read.
//
// spector:tags admin diagrams
func (h *Handler) adminDiagramProjects(w http.ResponseWriter, r *http.Request) {
	if h.Diagrams == nil {
		writeError(w, r, http.StatusNotFound, "no such endpoint")
		return
	}
	if _, ok := h.adminActor(w, r); !ok {
		return
	}
	q := r.URL.Query()
	page, err := h.Diagrams.AdminProjects(r.Context(), q.Get("q"), q.Get("owner"), paging(r))
	if err != nil {
		diagramProblem(w, r, err)
		return
	}
	items := make([]adminProjectJSON, len(page.Items))
	for i, p := range page.Items {
		items[i] = adminProjectJSON{ID: p.ID, Name: p.Name, Owner: adminUser(p.Owner), Files: p.Files,
			UpdatedAt: rfc3339(p.UpdatedAt), ThumbnailURL: assetThumb(p.ThumbnailHash), Shared: p.Shared}
	}
	writeJSON(w, http.StatusOK, adminProjectPage{items: items, page: page})
}

type adminProjectPage struct {
	items []adminProjectJSON
	page  application.AdminProjectPage
}

func (p adminProjectPage) Paginated() (any, any) {
	_, meta := p.page.Paginated()
	return p.items, meta
}

// adminDiagramProject returns one project with its files, members and share
// links. It never carries a share token or a file's document; revoked and
// expired links are listed so the admin can show their status.
//
// Auth: diagrams.read.
//
// spector:tags admin diagrams
func (h *Handler) adminDiagramProject(w http.ResponseWriter, r *http.Request) {
	if h.Diagrams == nil {
		writeError(w, r, http.StatusNotFound, "no such endpoint")
		return
	}
	if _, ok := h.adminActor(w, r); !ok {
		return
	}
	d, err := h.Diagrams.AdminProject(r.Context(), r.PathValue("id"))
	if err != nil {
		diagramProblem(w, r, err)
		return
	}
	type fileJSON struct {
		ID           string  `json:"id"`
		Name         string  `json:"name"`
		Kind         string  `json:"kind"`
		Version      int     `json:"version"`
		UpdatedAt    string  `json:"updated_at"`
		ThumbnailURL *string `json:"thumbnail_url"`
	}
	type memberJSON struct {
		UserID   string `json:"user_id"`
		Username string `json:"username"`
		Role     string `json:"role"`
		JoinedAt string `json:"joined_at"`
	}
	type shareJSON struct {
		ID        string  `json:"id"`
		Role      string  `json:"role"`
		CreatedBy string  `json:"created_by"`
		CreatedAt string  `json:"created_at"`
		ExpiresAt *string `json:"expires_at"`
		RevokedAt *string `json:"revoked_at"`
	}
	files := make([]fileJSON, len(d.FileList))
	for i, f := range d.FileList {
		files[i] = fileJSON{f.ID, f.Name, f.Kind, f.Version, rfc3339(f.UpdatedAt), assetThumb(f.ThumbnailHash)}
	}
	members := make([]memberJSON, len(d.Members))
	for i, m := range d.Members {
		members[i] = memberJSON{strconv.Itoa(m.User.ID), m.User.Username, string(m.Role), rfc3339(m.JoinedAt)}
	}
	shares := make([]shareJSON, len(d.Shares))
	for i, s := range d.Shares {
		shares[i] = shareJSON{s.ID, string(s.Role), s.CreatedBy, rfc3339(s.CreatedAt), rfc3339Ptr(s.ExpiresAt), rfc3339Ptr(s.RevokedAt)}
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": d.ID, "name": d.Name, "owner": adminUser(d.Owner),
		"created_at": rfc3339(d.CreatedAt), "updated_at": rfc3339(d.UpdatedAt), "thumbnail_url": assetThumb(d.ThumbnailHash),
		"shared": d.Shared, "files": files, "members": members, "shares": shares})
}

// adminDeleteDiagramProject hard-deletes a project with its files,
// versions, members and share links. Audited as diagram.project.delete.
//
// Response: 204. 404 for an unknown id. Auth: diagrams.manage.
//
// spector:tags admin diagrams
func (h *Handler) adminDeleteDiagramProject(w http.ResponseWriter, r *http.Request) {
	if h.Diagrams == nil {
		writeError(w, r, http.StatusNotFound, "no such endpoint")
		return
	}
	actor, ok := h.adminActor(w, r)
	if !ok {
		return
	}
	err := h.Diagrams.AdminDeleteProject(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		diagramProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// adminRevokeDiagramShare revokes a share link of any project at once; a
// link that is already revoked stays revoked (204 again). Audited as
// diagram.share.revoke.
//
// Response: 204. 404 for an unknown link. Auth: diagrams.manage.
//
// spector:tags admin diagrams
func (h *Handler) adminRevokeDiagramShare(w http.ResponseWriter, r *http.Request) {
	if h.Diagrams == nil {
		writeError(w, r, http.StatusNotFound, "no such endpoint")
		return
	}
	actor, ok := h.adminActor(w, r)
	if !ok {
		return
	}
	if err := h.Diagrams.AdminRevokeShare(r.Context(), actor, r.PathValue("shareId")); err != nil {
		diagramProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Templates

type adminTemplateJSON struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Kind        string `json:"kind"`
	Description string `json:"description"`
	FileID      string `json:"file_id"`
	Published   bool   `json:"published"`
	Position    int    `json:"position"`
}

func adminTemplate(t diagram.Template) adminTemplateJSON {
	return adminTemplateJSON{t.ID, t.Title, t.Kind, t.Description, t.FileID, t.Published, t.Position}
}

func adminTemplates(ts []diagram.Template) []adminTemplateJSON {
	out := make([]adminTemplateJSON, len(ts))
	for i, t := range ts {
		out[i] = adminTemplate(t)
	}
	return out
}

// adminDiagramTemplates lists every template, drafts included, by
// position then title.
//
// Response: {"data": [{id, title, kind: system|db|ood, description,
// file_id, published, position}]}. Auth: templates.manage.
//
// spector:tags admin diagrams
func (h *Handler) adminDiagramTemplates(w http.ResponseWriter, r *http.Request) {
	if h.Diagrams == nil {
		writeError(w, r, http.StatusNotFound, "no such endpoint")
		return
	}
	if _, ok := h.adminActor(w, r); !ok {
		return
	}
	ts, err := h.Diagrams.AdminTemplates(r.Context())
	if err != nil {
		diagramProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, adminTemplates(ts))
}

// adminCreateDiagramTemplate adds a template.
//
// Body: {title (1-120), kind (system|db|ood), description (<= 500), file_id
// (an existing diagram file, else 400), published, position}. Response: 201
// {"data": template}. Auth: templates.manage.
//
// spector:tags admin diagrams
func (h *Handler) adminCreateDiagramTemplate(w http.ResponseWriter, r *http.Request) {
	if h.Diagrams == nil {
		writeError(w, r, http.StatusNotFound, "no such endpoint")
		return
	}
	actor, ok := h.adminActor(w, r)
	if !ok {
		return
	}
	var in struct {
		Title       string `json:"title"`
		Kind        string `json:"kind"`
		Description string `json:"description"`
		FileID      string `json:"file_id"`
		Published   bool   `json:"published"`
		Position    int    `json:"position"`
	}
	if !decode(w, r, &in) {
		return
	}
	t, err := h.Diagrams.CreateTemplate(r.Context(), actor, application.NewTemplate{Title: in.Title, Kind: in.Kind,
		Description: in.Description, FileID: in.FileID, Published: in.Published, Position: in.Position})
	if err != nil {
		diagramProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, adminTemplate(t))
}

// adminUpdateDiagramTemplate changes the fields that are sent: the admin
// toggles {published} and swaps {position}.
//
// Body: any of title, kind, description, file_id, published, position.
// Response: 200 {"data": template}. 404 for an unknown id.
// Auth: templates.manage.
//
// spector:tags admin diagrams
func (h *Handler) adminUpdateDiagramTemplate(w http.ResponseWriter, r *http.Request) {
	if h.Diagrams == nil {
		writeError(w, r, http.StatusNotFound, "no such endpoint")
		return
	}
	actor, ok := h.adminActor(w, r)
	if !ok {
		return
	}
	var in struct {
		Title       *string `json:"title"`
		Kind        *string `json:"kind"`
		Description *string `json:"description"`
		FileID      *string `json:"file_id"`
		Published   *bool   `json:"published"`
		Position    *int    `json:"position"`
	}
	if !decode(w, r, &in) {
		return
	}
	t, err := h.Diagrams.UpdateTemplate(r.Context(), actor, r.PathValue("id"), application.TemplatePatch{Title: in.Title,
		Kind: in.Kind, Description: in.Description, FileID: in.FileID, Published: in.Published, Position: in.Position})
	if err != nil {
		diagramProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, adminTemplate(t))
}

// adminDeleteDiagramTemplate removes a template from the gallery; the
// source file stays.
//
// Response: 204. 404 for an unknown id. Auth: templates.manage.
//
// spector:tags admin diagrams
func (h *Handler) adminDeleteDiagramTemplate(w http.ResponseWriter, r *http.Request) {
	if h.Diagrams == nil {
		writeError(w, r, http.StatusNotFound, "no such endpoint")
		return
	}
	actor, ok := h.adminActor(w, r)
	if !ok {
		return
	}
	if err := h.Diagrams.DeleteTemplate(r.Context(), actor, r.PathValue("id")); err != nil {
		diagramProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// adminReorderDiagramTemplates orders the gallery in one transaction: the
// listed templates take positions 0..n-1 in the order of ids, any others
// follow in their current order.
//
// Body: {"ids": ["tpl_...", ...]} (1-500, unknown id: 404 and nothing
// changes). Response: 200 {"data": [template...]} in the new order.
// Auth: templates.manage.
//
// spector:tags admin diagrams
func (h *Handler) adminReorderDiagramTemplates(w http.ResponseWriter, r *http.Request) {
	if h.Diagrams == nil {
		writeError(w, r, http.StatusNotFound, "no such endpoint")
		return
	}
	actor, ok := h.adminActor(w, r)
	if !ok {
		return
	}
	var in struct {
		IDs []string `json:"ids"`
	}
	if !decode(w, r, &in) {
		return
	}
	ts, err := h.Diagrams.ReorderTemplates(r.Context(), actor, in.IDs)
	if err != nil {
		diagramProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, adminTemplates(ts))
}
