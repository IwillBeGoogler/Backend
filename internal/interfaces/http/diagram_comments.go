package http

// Comments and notifications in the diagram API: the Node server's wire
// shapes. Notifications are the blog's (they also show up in
// /api/me/notifications and its live stream); these routes are a thin view
// of the diagram ones.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
)

func (h *Handler) commentRoutes(mux dgRouter) {
	p := DiagramPrefix
	mux.HandleFunc("GET "+p+"/files/{id}/comments", h.dgComments)
	mux.HandleFunc("POST "+p+"/files/{id}/comments", h.dgAddComment)
	mux.HandleFunc("POST "+p+"/files/{id}/comments/read", h.dgReadComments)
	mux.HandleFunc("PATCH "+p+"/comments/{id}", h.dgPatchComment)
	mux.HandleFunc("DELETE "+p+"/comments/{id}", h.dgDeleteComment)

	mux.HandleFunc("GET "+p+"/notifications", h.dgNotifications)
	mux.HandleFunc("POST "+p+"/notifications/read", h.dgReadNotifications)
}

type dgPerson struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func personJSON(p application.Person) dgPerson {
	if p.ID == 0 {
		return dgPerson{ID: "", Name: p.Name}
	}
	return dgPerson{ID: strconv.Itoa(p.ID), Name: p.Name}
}

type dgComment struct {
	ID         string          `json:"id"`
	FileID     string          `json:"fileId"`
	ParentID   *string         `json:"parentId"`
	Author     dgPerson        `json:"author"`
	Body       string          `json:"body"`
	Anchor     json.RawMessage `json:"anchor"`
	Resolved   bool            `json:"resolved"`
	ResolvedAt *int64          `json:"resolvedAt"`
	ResolvedBy *dgPerson       `json:"resolvedBy"`
	Mentions   []string        `json:"mentions"`
	CreatedAt  int64           `json:"createdAt"`
	UpdatedAt  int64           `json:"updatedAt"`
}

func commentJSON(c application.CommentView) dgComment {
	out := dgComment{ID: c.ID, FileID: c.FileID, ParentID: c.ParentID, Author: personJSON(c.Author), Body: c.Body,
		Anchor: json.RawMessage("null"), Resolved: c.ResolvedAt != nil, Mentions: make([]string, len(c.Mentions)),
		CreatedAt: ms(c.CreatedAt), UpdatedAt: ms(c.UpdatedAt)}
	if len(c.Anchor) > 0 {
		out.Anchor = c.Anchor
	}
	if c.ResolvedAt != nil {
		at := ms(*c.ResolvedAt)
		out.ResolvedAt = &at
	}
	if c.ResolvedBy != nil {
		p := personJSON(*c.ResolvedBy)
		out.ResolvedBy = &p
	}
	for i, m := range c.Mentions {
		out.Mentions[i] = strconv.Itoa(m)
	}
	return out
}

// dgComments lists a file's comment threads: {"threads": [{"root",
// "replies"}], "unread", "readAt"}; unread counts other people's comments
// since you last marked them read (readAt, 0 for never). Auth: viewer.
//
// spector:tags diagrams
func (h *Handler) dgComments(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	l, err := h.Diagrams.FileComments(r.Context(), uid, id)
	if err != nil {
		dgError(w, r, err)
		return
	}
	type thread struct {
		Root    dgComment   `json:"root"`
		Replies []dgComment `json:"replies"`
	}
	threads := make([]thread, len(l.Threads))
	for i, t := range l.Threads {
		threads[i] = thread{Root: commentJSON(t.Root), Replies: make([]dgComment, len(t.Replies))}
		for j, c := range t.Replies {
			threads[i].Replies[j] = commentJSON(c)
		}
	}
	var readAt int64
	if !l.ReadAt.IsZero() {
		readAt = ms(l.ReadAt)
	}
	dgWrite(w, http.StatusOK, map[string]any{"threads": threads, "unread": l.Unread, "readAt": readAt})
}

// dgAddComment adds a comment. Body: {"body" (1-4000 characters),
// "anchor"?, "parentId"?}: a root needs an anchor ({"type":"point","x","y"}
// or {"type":"node","nodeId","dx","dy"}), a reply the id of a root of this
// file (replies to replies are 400 REPLY_TO_REPLY). Mention members with
// @[Name](user:<id>). Response 201 {"comment"}. Auth: commenter.
//
// spector:tags diagrams
func (h *Handler) dgAddComment(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok || !h.dgAllow(w, r, "dg-comment", 60, time.Minute) {
		return
	}
	var b struct {
		Body     string          `json:"body"`
		Anchor   json.RawMessage `json:"anchor"`
		ParentID string          `json:"parentId"`
	}
	if !dgDecode(w, r, &b, smallBody, false) {
		return
	}
	c, err := h.Diagrams.AddComment(r.Context(), uid, id, application.NewComment{Body: b.Body, Anchor: b.Anchor, ParentID: b.ParentID})
	if err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusCreated, map[string]any{"comment": commentJSON(c)})
}

// dgPatchComment edits your comment's text and/or resolves ("resolved":
// true) or reopens a thread root. Body: {"body"?, "resolved"?}. Response
// {"comment"}. Auth: commenter; only the author edits the text.
//
// spector:tags diagrams
func (h *Handler) dgPatchComment(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	var b struct {
		Body     *string `json:"body"`
		Resolved *bool   `json:"resolved"`
	}
	if !dgDecode(w, r, &b, smallBody, false) {
		return
	}
	c, err := h.Diagrams.UpdateComment(r.Context(), uid, id, application.CommentPatch{Body: b.Body, Resolved: b.Resolved})
	if err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusOK, map[string]any{"comment": commentJSON(c)})
}

// dgDeleteComment deletes a comment (a root takes its replies with it).
// 204. Auth: the author, or a project owner.
//
// spector:tags diagrams
func (h *Handler) dgDeleteComment(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	if _, err := h.Diagrams.DeleteComment(r.Context(), uid, id); err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusNoContent, nil)
}

// dgReadComments marks a file's comments read for you (resets unread). 204.
// Auth: viewer.
//
// spector:tags diagrams
func (h *Handler) dgReadComments(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	if err := h.Diagrams.MarkCommentsRead(r.Context(), uid, id); err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusNoContent, nil)
}

type dgNotification struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	ProjectID string    `json:"projectId"`
	FileID    string    `json:"fileId"`
	CommentID string    `json:"commentId"`
	Actor     *dgPerson `json:"actor"`
	Text      string    `json:"text"`
	CreatedAt int64     `json:"createdAt"`
	Read      bool      `json:"read"`
}

// dgNotifications lists your diagram notifications (kind "mention",
// "reply" or "comment"), newest first: {"items", "unread"}. They are the
// blog's notifications of the diagram_* types, so they also appear in
// /api/me/notifications. Query: unread=1 for unread only, limit (1-200,
// default 50). Auth: user.
//
// spector:tags diagrams
func (h *Handler) dgNotifications(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.dgUser(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	limit := 0
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > application.MaxNotifications {
			dgValidation(w, r, "query", "limit: must be an integer between 1 and 200")
			return
		}
		limit = n
	}
	unread := false
	if v := q.Get("unread"); v != "" {
		n, err := strconv.ParseFloat(v, 64)
		if err != nil {
			dgValidation(w, r, "query", "unread: expected a number")
			return
		}
		unread = n != 0
	}
	l, err := h.Diagrams.Notifications(r.Context(), uid, unread, limit)
	if err != nil {
		dgError(w, r, err)
		return
	}
	items := make([]dgNotification, len(l.Items))
	for i, n := range l.Items {
		items[i] = dgNotification{ID: strconv.FormatInt(n.ID, 10), Kind: strings.TrimPrefix(string(n.Type), "diagram_"),
			ProjectID: n.DiagramProjectID, FileID: n.DiagramFileID, CommentID: n.DiagramCommentID, Text: n.Text,
			CreatedAt: ms(n.CreatedAt), Read: n.ReadAt != nil}
		if n.By != nil {
			p := personJSON(*n.By)
			items[i].Actor = &p
		}
	}
	dgWrite(w, http.StatusOK, map[string]any{"items": items, "unread": l.Unread})
}

// dgReadNotifications marks diagram notifications read. Optional body:
// {"ids"? (at most 200), "all"?}; without ids (or an empty body) every
// diagram notification is marked. 204. Auth: user.
//
// spector:tags diagrams
func (h *Handler) dgReadNotifications(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.dgUser(w, r)
	if !ok {
		return
	}
	var b struct {
		IDs []string `json:"ids"`
		All bool     `json:"all"`
	}
	if !dgDecode(w, r, &b, smallBody, true) {
		return
	}
	if b.IDs == nil {
		b.All = true
	}
	if len(b.IDs) > 200 {
		dgValidation(w, r, "body", "ids: at most 200")
		return
	}
	if err := h.Diagrams.ReadNotifications(r.Context(), uid, b.IDs, b.All); err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusNoContent, nil)
}
