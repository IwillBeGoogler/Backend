package http

import (
	"net/http"
	"strconv"
)

// siteStats returns the public site statistics: unique view totals of posts
// and diagrams, distinct visitors, counts of posts, public diagrams and
// users, and the five most viewed posts and public diagrams. Nothing
// private is included. Cached for a minute.
//
// Response: {"data": {totals: {post_views, diagram_views, visitors, posts,
// diagrams, users}, top_posts: [{id, slug, title, author, views}],
// top_diagrams: [{slug, title, author, views}]}}.
// Auth: none.
//
// spector:tags stats
func (h *Handler) siteStats(w http.ResponseWriter, r *http.Request) {
	if h.SiteStats == nil {
		writeError(w, r, http.StatusNotFound, "stats disabled")
		return
	}
	s, err := h.SiteStats.Public(r.Context())
	if err == nil {
		w.Header().Set("Cache-Control", "public, max-age=60, stale-while-revalidate=300")
	}
	respond(w, r, http.StatusOK, s, err, "")
}

// adminSiteStats returns the public site statistics with longer top lists
// and the last 30 days of post views, diagram views and page views.
//
// Query: limit (top list length, 1-100, default 20).
// Response: {"data": {totals, top_posts, top_diagrams, days: [{day,
// post_views, diagram_views, page_views}]}}.
// Auth: stats.read.
//
// spector:tags admin
func (h *Handler) adminSiteStats(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUser(w, r); !ok {
		return
	}
	if h.SiteStats == nil {
		writeError(w, r, http.StatusNotFound, "stats disabled")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	s, err := h.SiteStats.Admin(r.Context(), limit)
	respond(w, r, http.StatusOK, s, err, "")
}
