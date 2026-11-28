package http

import "net/http"

const certificateNotFound = "certificate not found"

// claimCertificate issues the certificate of a completed challenge.
//
// The challenge must be at least 30 days long, 90% done, and ended (or on its
// last day with every day done); 400 says which rule failed. 201 when issued,
// 200 with the existing one on a repeat; a revoked one is never reissued.
// Auth: challenge owner.
//
// spector:tags challenges
func (h *Handler) claimCertificate(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	if id, ok := pathID(w, r); ok {
		c, created, err := h.Blog.ClaimCertificate(r.Context(), actor, id)
		code := http.StatusOK
		if created {
			code = http.StatusCreated
		}
		respond(w, r, code, c, err, challengeNotFound)
	}
}

// getCertificate verifies a certificate. A revoked one is still 200, with
// revoked_at and revoke_reason.
//
// Auth: none.
//
// spector:tags challenges
func (h *Handler) getCertificate(w http.ResponseWriter, r *http.Request) {
	c, err := h.Blog.Certificate(r.Context(), r.PathValue("id"))
	respond(w, r, http.StatusOK, c, err, certificateNotFound)
}

// adminCertificates lists certificates, newest first.
//
// Query: page, limit.
// Auth: certificate.revoke.
//
// spector:tags moderation
func (h *Handler) adminCertificates(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUser(w, r); ok {
		p, err := h.Blog.AdminCertificates(r.Context(), paging(r))
		respond(w, r, http.StatusOK, p, err, "")
	}
}

// revokeCertificate withdraws a certificate; 400 if already revoked.
//
// Body: {"reason": "..."} (required, shown publicly).
// Auth: certificate.revoke.
//
// spector:tags moderation
func (h *Handler) revokeCertificate(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if decode(w, r, &in) {
		c, err := h.Blog.RevokeCertificate(r.Context(), actor, r.PathValue("id"), in.Reason)
		respond(w, r, http.StatusOK, c, err, certificateNotFound)
	}
}
