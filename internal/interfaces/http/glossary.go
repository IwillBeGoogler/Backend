package http

import (
	"net/http"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/glossary"
)

const glossaryNotFound = "glossary term not found"

// listGlossary lists the terms the reader explains in tooltips.
//
// With lang, returns that language's terms plus the language-neutral ones;
// without, every term. Cacheable for five minutes.
// Auth: none.
//
// spector:tags glossary
func (h *Handler) listGlossary(w http.ResponseWriter, r *http.Request) {
	t, err := h.Blog.ListGlossary(r.Context(), r.URL.Query().Get("lang"))
	if err == nil {
		w.Header().Set("Cache-Control", "public, max-age=300")
	}
	respond(w, r, http.StatusOK, t, err, "")
}

// createGlossaryTerm creates a term.
//
// A term is unique per language, case-insensitive. Audited.
// Auth: glossary.write (admin).
//
// spector:tags glossary
func (h *Handler) createGlossaryTerm(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	var in glossary.Input
	if ok && decode(w, r, &in) {
		t, err := h.Blog.CreateGlossaryTerm(r.Context(), actor, in)
		respond(w, r, http.StatusCreated, t, err, "")
	}
}

// updateGlossaryTerm replaces a term.
//
// Audited.
// Auth: glossary.write (admin).
//
// spector:tags glossary
func (h *Handler) updateGlossaryTerm(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	var in glossary.Input
	if id, ok := pathID(w, r); ok && decode(w, r, &in) {
		t, err := h.Blog.UpdateGlossaryTerm(r.Context(), actor, id, in)
		respond(w, r, http.StatusOK, t, err, glossaryNotFound)
	}
}

// deleteGlossaryTerm deletes a term.
//
// Audited.
// Auth: glossary.write (admin).
//
// spector:tags glossary
func (h *Handler) deleteGlossaryTerm(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	if id, ok := pathID(w, r); ok {
		noContent(w, r, h.Blog.DeleteGlossaryTerm(r.Context(), actor, id), glossaryNotFound)
	}
}
