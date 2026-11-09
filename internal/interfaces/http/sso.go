package http

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"go.uber.org/zap"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
)

// SocialProvider is one social login (Google, GitHub).
type SocialProvider interface {
	Begin(ctx context.Context) (authURL string, err error)
	Finish(ctx context.Context, state, code string) (application.ExternalIdentity, error)
}

func (h *Handler) socialProvider(w http.ResponseWriter, r *http.Request) (string, SocialProvider, bool) {
	name := r.PathValue("provider")
	p := h.Social[name]
	if p == nil {
		writeError(w, r, http.StatusNotFound, "social login is not enabled")
		return "", nil, false
	}
	return name, p, h.allow(w, r, "sso", 20, time.Minute)
}

// socialLogin sends the browser to the provider (google or github).
//
// Rate limit: 20 per minute per IP.
// Auth: none.
//
// spector:tags auth
func (h *Handler) socialLogin(w http.ResponseWriter, r *http.Request) {
	name, p, ok := h.socialProvider(w, r)
	if !ok {
		return
	}
	u, err := p.Begin(r.Context())
	if err != nil {
		zap.L().Error("social begin", zap.Any("ctx", r.Context()), zap.String("provider", name), zap.Error(err))
		h.Accounts.RecordSSOFailure(r.Context(), name, "begin", h.meta(r))
		h.ssoRedirect(w, r, url.Values{"error": {"failed"}})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, u, http.StatusFound)
}

// socialCallback finishes the sign-in and redirects to the client with the
// session token (or an error code) in the URL fragment, never the query.
//
// Rate limit: 20 per minute per IP.
// Auth: none.
//
// spector:tags auth
func (h *Handler) socialCallback(w http.ResponseWriter, r *http.Request) {
	name, p, ok := h.socialProvider(w, r)
	if !ok {
		return
	}
	ctx, m, q := r.Context(), h.meta(r), r.URL.Query()
	fail := func(code, reason string, err error) {
		if err != nil {
			zap.L().Warn("social sign-in", zap.Any("ctx", ctx), zap.String("provider", name), zap.String("reason", reason), zap.Error(err))
		}
		h.Accounts.RecordSSOFailure(ctx, name, reason, m)
		h.ssoRedirect(w, r, url.Values{"error": {code}})
	}
	if q.Get("error") != "" {
		fail("failed", "provider_error", nil)
		return
	}
	id, err := p.Finish(ctx, q.Get("state"), q.Get("code"))
	if err != nil {
		fail("failed", "rejected", err)
		return
	}
	s, err := h.Accounts.LoginExternal(ctx, name, id, m)
	var se *application.SSOError
	switch {
	case errors.As(err, &se):
		fail(se.Code, se.Code, nil)
		return
	case err != nil:
		fail("failed", "login", err)
		return
	}
	h.ssoRedirect(w, r, url.Values{"token": {s.Token}, "expires_at": {s.ExpiresAt.UTC().Format(time.RFC3339)}})
}

// ssoRedirect sends the browser to SITE_URL/auth/callback#<values>.
func (h *Handler) ssoRedirect(w http.ResponseWriter, r *http.Request, v url.Values) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, trimSlash(h.SiteURL)+"/auth/callback#"+v.Encode(), http.StatusFound)
}

func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}
