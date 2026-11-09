package http

// Diagrams on the blog: publishing a file, the public page and listings,
// and the template gallery. The owner's routes (publish, unpublish, fork)
// speak the diagram API's dialect (bare camelCase JSON, epoch milliseconds,
// {"error": ...}); the public reads follow the blog's ({data, meta},
// snake_case, RFC 3339, problem details), since the blog's pages consume
// them.

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
)

func (h *Handler) publishRoutes(mux dgRouter) {
	p := DiagramPrefix
	mux.HandleFunc("GET "+p+"/files/{id}/publish", h.dgGetPublication)
	mux.HandleFunc("POST "+p+"/files/{id}/publish", h.dgPublish)
	mux.HandleFunc("DELETE "+p+"/files/{id}/publish", h.dgUnpublish)

	mux.HandleFunc("GET "+p+"/published", h.dgPublished)
	mux.HandleFunc("GET "+p+"/public", h.publicDiagrams)
	mux.HandleFunc("GET "+p+"/public/{slug}", h.publicDiagram)
	mux.HandleFunc("GET /api/users/{username}/diagrams", h.userDiagrams)

	mux.HandleFunc("GET "+p+"/public/{slug}/me", h.publicDiagramMe)
	mux.HandleFunc("POST "+p+"/public/{slug}/clap", h.clapDiagram)
	mux.HandleFunc("POST "+p+"/public/{slug}/bookmark", h.bookmarkDiagram)
	mux.HandleFunc("DELETE "+p+"/public/{slug}/bookmark", h.unbookmarkDiagram)
	mux.HandleFunc("GET /api/me/diagram-bookmarks", h.myDiagramBookmarks)

	mux.HandleFunc("GET "+p+"/templates", h.templateGallery)
	mux.HandleFunc("POST "+p+"/templates/{id}/fork", h.dgForkTemplate)
}

// PublicDiagramPath is where the client shows a published diagram.
const PublicDiagramPath = "/d/v/"

type dgPublication struct {
	FileID      string   `json:"fileId"`
	Visibility  string   `json:"visibility"`
	Slug        *string  `json:"slug"`
	URL         *string  `json:"url"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	PublishedAt *int64   `json:"publishedAt"`
}

func publicationJSON(l diagram.Listing) dgPublication {
	out := dgPublication{FileID: l.FileID, Visibility: string(l.Visibility), Title: l.Title, Description: l.Description, Tags: l.Tags}
	if out.Tags == nil {
		out.Tags = []string{}
	}
	if l.Slug != "" {
		out.Slug = &l.Slug
		if l.Visibility.Published() {
			u := PublicDiagramPath + l.Slug
			out.URL = &u
		}
	}
	if !l.PublishedAt.IsZero() {
		at := ms(l.PublishedAt)
		out.PublishedAt = &at
	}
	return out
}

// dgGetPublication returns a file's blog page: visibility private and no
// url for a file that is not published. Auth: any role on the file.
//
// spector:tags diagrams
func (h *Handler) dgGetPublication(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	l, err := h.Diagrams.FilePublication(r.Context(), uid, id)
	if err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusOK, publicationJSON(l))
}

// dgPublish publishes a file to the blog, or edits its page.
//
// Body: {"visibility": "unlisted"|"public", "title"?, "description"?,
// "tags"?}. title defaults to the file's name (max 120), description max
// 500, at most 10 tags. The slug is made on the first publish and kept
// for good. The page shows the file's current document, so later edits
// show up. Response: the publication, {fileId, visibility, slug, url, title,
// description, tags, publishedAt}. Auth: owner of the project.
//
// spector:tags diagrams
func (h *Handler) dgPublish(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok || !h.dgAllow(w, r, "dg-publish", 60, time.Hour) {
		return
	}
	var b struct {
		Visibility  diagram.Visibility `json:"visibility"`
		Title       string             `json:"title"`
		Description string             `json:"description"`
		Tags        []string           `json:"tags"`
	}
	if !dgDecode(w, r, &b, smallBody, false) {
		return
	}
	l, err := h.Diagrams.PublishFile(r.Context(), uid, id, application.PublishRequest{Visibility: b.Visibility,
		Title: b.Title, Description: b.Description, Tags: b.Tags})
	if err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusOK, publicationJSON(l))
}

// dgUnpublish makes a file private again; the slug is kept for a later
// republish. Response: 204, also for a file that was not published.
// Auth: owner of the project.
//
// spector:tags diagrams
func (h *Handler) dgUnpublish(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	if err := h.Diagrams.UnpublishFile(r.Context(), uid, id); err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusNoContent, nil)
}

type dgPublished struct {
	FileID       string  `json:"fileId"`
	Slug         string  `json:"slug"`
	Title        string  `json:"title"`
	Visibility   string  `json:"visibility"`
	ThumbnailURL *string `json:"thumbnailUrl"`
	PublishedAt  int64   `json:"publishedAt"`
}

// dgPublished lists the caller's own unlisted and public files, newest
// published first: what the story editor's diagram picker offers.
//
// Query: page (1+), pageSize (1-100, default 20). Response: {"items":
// [{fileId, slug, title, visibility, thumbnailUrl, publishedAt}], "total",
// "page", "pageSize"}. Auth: any signed-in user (only their own files).
//
// spector:tags diagrams
func (h *Handler) dgPublished(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.dgUser(w, r)
	if !ok {
		return
	}
	q, ok := dgProjectQuery(w, r)
	if !ok {
		return
	}
	page, err := h.Diagrams.PublishedDiagrams(r.Context(), uid, q.Page, q.PageSize)
	if err != nil {
		dgError(w, r, err)
		return
	}
	items := make([]dgPublished, len(page.Items))
	for i, l := range page.Items {
		var thumb *string
		if l.ThumbnailHash != nil {
			u := thumbURL(l.FileID, *l.ThumbnailHash)
			thumb = &u
		}
		items[i] = dgPublished{FileID: l.FileID, Slug: l.Slug, Title: l.Title, Visibility: string(l.Visibility),
			ThumbnailURL: thumb, PublishedAt: ms(l.PublishedAt)}
	}
	dgWrite(w, http.StatusOK, map[string]any{"items": items, "total": page.Total, "page": page.Page, "pageSize": page.Limit})
}

// Public reads

type publicAuthor struct {
	ID          int    `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	AvatarURL   string `json:"avatar_url"`
}

type publicDiagramJSON struct {
	FileID       string          `json:"file_id"`
	Slug         string          `json:"slug"`
	URL          string          `json:"url"`
	Title        string          `json:"title"`
	Description  string          `json:"description"`
	Tags         []string        `json:"tags"`
	Visibility   string          `json:"visibility"`
	Kind         string          `json:"kind"`
	PageCount    int             `json:"page_count"`
	ThumbnailURL *string         `json:"thumbnail_url"`
	Claps        int             `json:"claps"`
	PublishedAt  string          `json:"published_at"`
	UpdatedAt    string          `json:"updated_at"`
	Author       publicAuthor    `json:"author"`
	Data         json.RawMessage `json:"data,omitempty"`
}

func publicJSON(l diagram.Listing) publicDiagramJSON {
	tags := l.Tags
	if tags == nil {
		tags = []string{}
	}
	return publicDiagramJSON{FileID: l.FileID, Slug: l.Slug, URL: PublicDiagramPath + l.Slug, Title: l.Title, Description: l.Description,
		Tags: tags, Visibility: string(l.Visibility), Kind: l.Kind, PageCount: l.PageCount, ThumbnailURL: assetThumb(l.ThumbnailHash), Claps: l.Claps,
		PublishedAt: rfc3339(l.PublishedAt), UpdatedAt: rfc3339(l.UpdatedAt),
		Author: publicAuthor{l.AuthorID, l.AuthorUsername, l.AuthorName, l.AuthorAvatar}}
}

type publicDiagramPage struct{ page application.PublicDiagramPage }

func (p publicDiagramPage) Paginated() (any, any) {
	items := make([]publicDiagramJSON, len(p.page.Items))
	for i, l := range p.page.Items {
		items[i] = publicJSON(l)
	}
	return items, domain.NewMeta(p.page.Page, p.page.Limit, p.page.Total, "")
}

// publicDiagram returns a published diagram by slug: its document, author
// and thumbnail. Unlisted diagrams open too (their page is noindex). The
// answer is cacheable by anyone for a minute and revalidates by ETag.
//
// Response: {"data": {file_id, slug, url, title, description, tags,
// visibility, kind, page_count, thumbnail_url, published_at, updated_at,
// author: {id, username, display_name, avatar_url}, data}} where data is
// the diagram document. 404 for an unknown or unpublished slug.
// Auth: none.
//
// spector:tags diagrams
func (h *Handler) publicDiagram(w http.ResponseWriter, r *http.Request) {
	doc, err := h.Diagrams.PublicDiagram(r.Context(), r.PathValue("slug"))
	if err != nil {
		diagramProblem(w, r, err)
		return
	}
	hd := w.Header()
	tag := `"` + strconv.Itoa(doc.Version) + "-" + strconv.FormatInt(ms(doc.UpdatedAt), 36) + "-" + string(doc.Visibility) + `"`
	hd.Set("ETag", tag)
	hd.Set("Cache-Control", "public, max-age=60, stale-while-revalidate=300")
	if doc.Visibility != diagram.VisPublic {
		hd.Set("X-Robots-Tag", "noindex")
	}
	if r.Header.Get("If-None-Match") == tag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	out := publicJSON(doc.Listing)
	out.Data = withIdentity(doc.Data, doc.FileID, doc.Name)
	writeJSON(w, http.StatusOK, out)
}

// publicDiagrams searches public diagrams, newest first.
//
// Query: q (title or description), tag, page, limit (default 10, max 50).
// Response: {"data": [diagram], "meta"}; diagrams are as in the public
// page, without data. Auth: none.
//
// spector:tags diagrams
func (h *Handler) publicDiagrams(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, err := h.Diagrams.PublicDiagrams(r.Context(), q.Get("q"), q.Get("tag"), paging(r))
	h.publicList(w, r, page, err)
}

// userDiagrams lists a user's public diagrams for their profile tab.
//
// Query: page, limit. Response: {"data": [diagram], "meta"}.
// Auth: none.
//
// spector:tags users diagrams
func (h *Handler) userDiagrams(w http.ResponseWriter, r *http.Request) {
	if h.Diagrams == nil {
		writeError(w, r, http.StatusNotFound, "no such endpoint")
		return
	}
	page, err := h.Diagrams.UserDiagrams(r.Context(), r.PathValue("username"), paging(r))
	h.publicList(w, r, page, err)
}

func (h *Handler) publicList(w http.ResponseWriter, r *http.Request, page application.PublicDiagramPage, err error) {
	if err != nil {
		respond(w, r, 0, nil, err, "user not found")
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=30, stale-while-revalidate=120")
	writeJSON(w, http.StatusOK, publicDiagramPage{page})
}

// Templates

type publicTemplateJSON struct {
	ID           string  `json:"id"`
	Title        string  `json:"title"`
	Kind         string  `json:"kind"`
	Description  string  `json:"description"`
	ThumbnailURL *string `json:"thumbnail_url"`
	PageCount    int     `json:"page_count"`
}

// templateGallery lists the published templates in gallery order.
//
// Response: {"data": [{id, title, kind: system|db|ood, description,
// thumbnail_url, page_count}]}. Auth: none.
//
// spector:tags diagrams
func (h *Handler) templateGallery(w http.ResponseWriter, r *http.Request) {
	ts, err := h.Diagrams.PublicTemplates(r.Context())
	if err != nil {
		respond(w, r, 0, nil, err, "")
		return
	}
	out := make([]publicTemplateJSON, len(ts))
	for i, t := range ts {
		out[i] = publicTemplateJSON{t.ID, t.Title, t.Kind, t.Description, assetThumb(t.ThumbnailHash), t.PageCount}
	}
	w.Header().Set("Cache-Control", "public, max-age=60, stale-while-revalidate=300")
	writeJSON(w, http.StatusOK, out)
}

// dgForkTemplate copies a published template into a new project of the
// caller (named after the template), with one file holding the template's
// document and preview.
//
// Response: 201 {"project", "file"} as for POST /projects, with the file's
// version in ETag. 404 for an unknown or unpublished template. At most 30
// forks an hour. Auth: any user.
//
// spector:tags diagrams
func (h *Handler) dgForkTemplate(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok || !h.dgAllow(w, r, "dg-fork", 30, time.Hour) {
		return
	}
	res, err := h.Diagrams.ForkTemplate(r.Context(), uid, id)
	if err != nil {
		dgError(w, r, err)
		return
	}
	etag(w, res.File.Version)
	w.Header().Set("Location", DiagramPrefix+"/projects/"+url.PathEscape(res.Project.ID))
	dgWrite(w, http.StatusCreated, map[string]any{"project": projectJSON(res.Project), "file": docJSON(res.File)})
}

// Claps and bookmarks

// publicDiagramMe tells what the caller did to a published diagram. It is
// apart from the public page, which anyone's cache may keep.
//
// Response: {"data": {"my_claps": 0-50, "bookmarked": bool}}; zeros without
// a session. Auth: optional.
//
// spector:tags diagrams
func (h *Handler) publicDiagramMe(w http.ResponseWriter, r *http.Request) {
	v, err := h.Diagrams.DiagramViewer(r.Context(), h.viewerID(r), r.PathValue("slug"))
	if err != nil {
		diagramProblem(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, map[string]any{"my_claps": v.MyClaps, "bookmarked": v.Bookmarked})
}

// clapDiagram adds claps to a published diagram, like a post's.
//
// Body: {"count": 1-50}. A reader has at most 50 claps per diagram; extra
// claps are ignored. The author is notified on the first clap. Response:
// {"data": {"claps": total, "my_claps": n}}. Rate limit: 60 per minute.
// Auth: user.
//
// spector:tags likes diagrams
func (h *Handler) clapDiagram(w http.ResponseWriter, r *http.Request) {
	uid := h.viewerID(r)
	if uid == 0 {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !h.allow(w, r, "like", 60, time.Minute) {
		return
	}
	var in struct {
		Count int `json:"count"`
	}
	if !decode(w, r, &in) {
		return
	}
	res, err := h.Diagrams.ClapDiagram(r.Context(), uid, r.PathValue("slug"), in.Count)
	if err != nil {
		diagramProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"claps": res.Claps, "my_claps": res.MyClaps})
}

// bookmarkDiagram saves a published diagram to the caller's library.
//
// Idempotent; 204. Rate limit: 60 per minute. Auth: user.
//
// spector:tags bookmarks diagrams
func (h *Handler) bookmarkDiagram(w http.ResponseWriter, r *http.Request) {
	uid := h.viewerID(r)
	if uid == 0 {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !h.allow(w, r, "bookmark", 60, time.Minute) {
		return
	}
	if err := h.Diagrams.BookmarkDiagram(r.Context(), uid, r.PathValue("slug")); err != nil {
		diagramProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// unbookmarkDiagram removes a published diagram from the caller's library.
//
// 204, also when it was not saved. Auth: user.
//
// spector:tags bookmarks diagrams
func (h *Handler) unbookmarkDiagram(w http.ResponseWriter, r *http.Request) {
	uid := h.viewerID(r)
	if uid == 0 {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err := h.Diagrams.UnbookmarkDiagram(r.Context(), uid, r.PathValue("slug")); err != nil {
		diagramProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// myDiagramBookmarks lists the caller's saved diagrams (the blog's
// /api/me/bookmarks lists posts only), newest saved first.
//
// Query: page, limit. Response: {"data": [diagram], "meta"}. Auth: user.
//
// spector:tags bookmarks diagrams
func (h *Handler) myDiagramBookmarks(w http.ResponseWriter, r *http.Request) {
	uid := h.viewerID(r)
	if uid == 0 {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	if h.Diagrams == nil {
		writeError(w, r, http.StatusNotFound, "no such endpoint")
		return
	}
	page, err := h.Diagrams.BookmarkedDiagrams(r.Context(), uid, paging(r))
	if err != nil {
		diagramProblem(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, publicDiagramPage{page})
}
