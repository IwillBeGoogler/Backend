package http

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/clienterror"
)

// reportClientError stores a JavaScript error from the web app, grouped by a
// fingerprint the server computes.
//
// Body: {"message": "1-1000 chars", "stack", "page_url", "user_agent" (all
// optional, cut to 8000/2000/500 chars)}. Email addresses, tokens and URL
// query strings are scrubbed before storing; a client-sent fingerprint is
// ignored. Nothing about the sender (user id, IP) is stored. Always 204 for
// valid input, also when a full table drops a new error. Body limit 32 KB.
// Rate limit: 20 per 10 minutes per IP.
// Auth: none.
//
// spector:tags feedback
func (h *Handler) reportClientError(w http.ResponseWriter, r *http.Request) {
	if !h.allow(w, r, "client-errors", 20, 10*time.Minute) {
		return
	}
	var in struct {
		Message   string `json:"message"`
		Stack     string `json:"stack"`
		PageURL   string `json:"page_url"`
		UserAgent string `json:"user_agent"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid json")
		return
	}
	err := h.Blog.ReportClientError(r.Context(), clienterror.Input{
		Message: in.Message, Stack: in.Stack, PageURL: in.PageURL, UserAgent: in.UserAgent})
	noContent(w, r, err, "")
}

// adminClientErrors lists reported JavaScript errors, last seen first.
//
// Query: status (open, resolved, ignored; empty for all), page, limit.
// Auth: report.moderate.
//
// spector:tags moderation
func (h *Handler) adminClientErrors(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUser(w, r); ok {
		p, err := h.Blog.ListClientErrors(r.Context(), clienterror.Filter{
			Status: clienterror.Status(r.URL.Query().Get("status")), Paging: paging(r)})
		respond(w, r, http.StatusOK, p, err, "")
	}
}

// decideClientError sets a reported error's status.
//
// Body: {"status": "open"|"resolved"|"ignored"}; open reopens it. A new
// occurrence reopens a resolved error but not an ignored one.
// Auth: report.moderate.
//
// spector:tags moderation
func (h *Handler) decideClientError(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	var in struct {
		Status clienterror.Status `json:"status"`
	}
	if ok && decode(w, r, &in) {
		e, err := h.Blog.DecideClientError(r.Context(), actor.ID, id, in.Status)
		respond(w, r, http.StatusOK, e, err, "client error not found")
	}
}
