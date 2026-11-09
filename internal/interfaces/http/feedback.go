package http

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/feedback"
)

// createFeedback stores a bug report or idea.
//
// Body: {"kind": "bug"|"idea"|"other", "message": "1-5000 chars",
// "contact_email": "..." (anonymous callers only), "page_url", "user_agent",
// "app_version", "error_message", "error_stack" (all optional, cut to
// 2000/500/100/2000/10000 chars), "captcha_token" (anonymous callers, when
// CAPTCHA is on), "website": honeypot, leave empty}. A filled honeypot gets
// 201 and stores nothing. The sender is the session user; a client-sent
// user id is ignored. Works without a session, since login may be what is
// broken. Rate limit: 5 per 10 minutes anonymous (per IP), 20 per hour
// logged in.
// Auth: optional.
//
// spector:tags feedback
func (h *Handler) createFeedback(w http.ResponseWriter, r *http.Request) {
	userID := h.viewerID(r)
	n, window := 5, 10*time.Minute
	if userID != 0 {
		n, window = 20, time.Hour
	}
	if !h.allow(w, r, "feedback", n, window) {
		return
	}
	var in struct {
		Kind         feedback.Kind `json:"kind"`
		Message      string        `json:"message"`
		ContactEmail string        `json:"contact_email"`
		PageURL      string        `json:"page_url"`
		UserAgent    string        `json:"user_agent"`
		AppVersion   string        `json:"app_version"`
		ErrorMessage string        `json:"error_message"`
		ErrorStack   string        `json:"error_stack"`
		CaptchaToken string        `json:"captcha_token"`
		Website      string        `json:"website"`
	}
	// The biggest honest body is about 20 KB of message plus 12 KB of error text.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid json")
		return
	}
	if in.Website != "" { // a bot: look like success
		writeJSON(w, http.StatusCreated, map[string]bool{"ok": true})
		return
	}
	if userID == 0 && !h.humanCheck(w, r, in.CaptchaToken, "") {
		return
	}
	f, err := h.Blog.SubmitFeedback(r.Context(), userID, feedback.Input{
		Kind: in.Kind, Message: in.Message, ContactEmail: in.ContactEmail, PageURL: in.PageURL, UserAgent: in.UserAgent,
		AppVersion: in.AppVersion, ErrorMessage: in.ErrorMessage, ErrorStack: in.ErrorStack,
	})
	if err != nil {
		respond(w, r, 0, nil, err, "")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int{"id": f.ID})
}

// adminFeedback lists feedback, newest first.
//
// Query: status (open, resolved, dismissed; empty for all), kind (bug, idea,
// other; empty for all), page, limit. username is empty for anonymous senders.
// Auth: report.moderate.
//
// spector:tags moderation
func (h *Handler) adminFeedback(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUser(w, r); ok {
		q := r.URL.Query()
		p, err := h.Blog.ListFeedback(r.Context(), feedback.Filter{
			Status: feedback.Status(q.Get("status")), Kind: feedback.Kind(q.Get("kind")), Paging: paging(r)})
		respond(w, r, http.StatusOK, p, err, "")
	}
}

// decideFeedback sets a feedback entry's status.
//
// Body: {"status": "open"|"resolved"|"dismissed"}; open reopens it.
// Auth: report.moderate.
//
// spector:tags moderation
func (h *Handler) decideFeedback(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	var in struct {
		Status feedback.Status `json:"status"`
	}
	if ok && decode(w, r, &in) {
		f, err := h.Blog.DecideFeedback(r.Context(), actor.ID, id, in.Status)
		respond(w, r, http.StatusOK, f, err, "feedback not found")
	}
}
