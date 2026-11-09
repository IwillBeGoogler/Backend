package http

// Personal access tokens, the public API under /api/diagrams/v1 and the
// webhooks of a project. A token ("Authorization: Bearer sdd_pat_...") acts
// as its owner, so the owner's project role still applies; the token's
// scopes and project list only narrow it. Tokens are accepted on /v1 and
// MCP only; they cannot manage tokens.

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
)

// DiagramV1Prefix is where the public API is mounted.
const DiagramV1Prefix = DiagramPrefix + "/v1"

// DiagramMCPPath is where the MCP server is mounted.
const DiagramMCPPath = DiagramPrefix + "/mcp"

type tokenCtxKey struct{}

// TokenAuth is what an authenticated access token is in a request.
type tokenAuth struct {
	diagram.AccessToken
}

func tokenFrom(ctx context.Context) *tokenAuth {
	t, _ := ctx.Value(tokenCtxKey{}).(*tokenAuth)
	return t
}

// tokenPath reports whether a token may be used on path: the public API
// and MCP.
func tokenPath(path string) bool {
	return strings.HasPrefix(path, DiagramV1Prefix+"/") || path == DiagramMCPPath || strings.HasPrefix(path, DiagramMCPPath+"/")
}

// TokenAuth authenticates "Authorization: Bearer sdd_pat_..." on the
// public API and MCP and makes the request act as the token's owner,
// limited to the token's projects. Other credentials pass through to the
// session layer untouched. A token on any other diagram route is refused
// (403 TOKEN_SCOPE): those routes are for a browser session. active
// reports whether the owner's account may still sign in (not banned); nil
// means always.
func TokenAuth(d *application.Diagrams, active func(ctx context.Context, userID int) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if d == nil || !strings.HasPrefix(r.URL.Path, DiagramPrefix+"/") {
				next.ServeHTTP(w, r)
				return
			}
			raw := diagram.ParseBearer(r.Header.Get("Authorization"))
			if raw == "" {
				if strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")), diagram.TokenPrefix) {
					// shaped like a token but malformed: say so instead of falling back to "unauthenticated"
					if tokenPath(r.URL.Path) {
						dgFail(w, r, http.StatusUnauthorized, "INVALID_TOKEN", "The access token is invalid, expired or revoked", nil)
						return
					}
				}
				next.ServeHTTP(w, r)
				return
			}
			if !tokenPath(r.URL.Path) {
				dgFail(w, r, http.StatusForbidden, "TOKEN_SCOPE", "This endpoint cannot be used with an access token", nil)
				return
			}
			t, err := d.AuthenticateToken(r.Context(), raw)
			if err == nil && active != nil && !active(r.Context(), t.UserID) {
				err = domain.ErrUnauthorized
			}
			if err != nil {
				if err == domain.ErrUnauthorized {
					dgFail(w, r, http.StatusUnauthorized, "INVALID_TOKEN", "The access token is invalid, expired or revoked", nil)
				} else {
					dgError(w, r, err)
				}
				return
			}
			ctx := context.WithValue(r.Context(), tokenCtxKey{}, &tokenAuth{t})
			ctx = application.WithTokenLimit(ctx, t.ProjectIDs)
			r = r.WithContext(ctx)
			r.Header.Del("X-Share-Token") // a share link must not reach past the token's projects
			r.Header.Del("Authorization") // the session layer has no use for it
			next.ServeHTTP(w, r)
		})
	}
}

// needScope answers 403 TOKEN_SCOPE when the request carries a token
// without scope; a browser session passes.
func needScope(w http.ResponseWriter, r *http.Request, scope string) bool {
	t := tokenFrom(r.Context())
	if t == nil || t.Allows(scope) {
		return true
	}
	dgFail(w, r, http.StatusForbidden, "TOKEN_SCOPE", `The access token needs the "`+scope+`" scope`, map[string]string{"required": scope})
	return false
}

// scoped wraps a handler with a scope check.
func scoped(scope string, f func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if needScope(w, r, scope) {
			f(w, r)
		}
	}
}

func (h *Handler) tokenRoutes(mux dgRouter) {
	p := DiagramPrefix
	mux.HandleFunc("GET "+p+"/tokens", h.dgListTokens)
	mux.HandleFunc("POST "+p+"/tokens", h.dgCreateToken)
	mux.HandleFunc("DELETE "+p+"/tokens/{id}", h.dgRevokeToken)

	v := DiagramV1Prefix
	mux.HandleFunc("GET "+v+"/projects", scoped(diagram.ScopeProjectsRead, h.v1Projects))
	mux.HandleFunc("GET "+v+"/projects/{id}/files", scoped(diagram.ScopeFilesRead, h.v1Files))
	mux.HandleFunc("GET "+v+"/files/{id}", scoped(diagram.ScopeFilesRead, h.dgGetFile))
	mux.HandleFunc("PUT "+v+"/files/{id}", scoped(diagram.ScopeFilesWrite, h.dgSaveFile))
	mux.HandleFunc("GET "+v+"/projects/{id}/library", scoped(diagram.ScopeLibraryRead, h.dgGetLibrary))
	mux.HandleFunc("PUT "+v+"/projects/{id}/library", scoped(diagram.ScopeLibraryWrite, h.dgPutLibrary))

	// webhooks: the same handlers for a session (original path) and a token
	for _, base := range []string{p, v} {
		mux.HandleFunc("GET "+base+"/projects/{id}/webhooks", scoped(diagram.ScopeWebhooksAdmin, h.dgListWebhooks))
		mux.HandleFunc("POST "+base+"/projects/{id}/webhooks", scoped(diagram.ScopeWebhooksAdmin, h.dgCreateWebhook))
		mux.HandleFunc("PATCH "+base+"/projects/{id}/webhooks/{wid}", scoped(diagram.ScopeWebhooksAdmin, h.dgUpdateWebhook))
		mux.HandleFunc("DELETE "+base+"/projects/{id}/webhooks/{wid}", scoped(diagram.ScopeWebhooksAdmin, h.dgDeleteWebhook))
		mux.HandleFunc("GET "+base+"/projects/{id}/webhooks/{wid}/deliveries", scoped(diagram.ScopeWebhooksAdmin, h.dgDeliveries))
		mux.HandleFunc("POST "+base+"/projects/{id}/webhooks/{wid}/test", scoped(diagram.ScopeWebhooksAdmin, h.dgTestWebhook))
	}
}

// Tokens

type dgAccessToken struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Prefix     string   `json:"prefix"`
	Scopes     []string `json:"scopes"`
	ProjectIDs []string `json:"projectIds"`
	ExpiresAt  *int64   `json:"expiresAt"`
	LastUsedAt *int64   `json:"lastUsedAt"`
	CreatedAt  int64    `json:"createdAt"`
}

func msPtr(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	v := ms(*t)
	return &v
}

func tokenJSON(t diagram.AccessToken) dgAccessToken {
	return dgAccessToken{ID: t.ID, Name: t.Name, Prefix: t.Prefix, Scopes: t.Scopes, ProjectIDs: t.ProjectIDs,
		ExpiresAt: msPtr(t.ExpiresAt), LastUsedAt: msPtr(t.LastUsedAt), CreatedAt: ms(t.CreatedAt)}
}

// dgCreateToken makes a personal access token. Body: {"name", "scopes",
// "expiresInDays"?, "projectIds"?}; scopes are projects:read, files:read,
// files:write, library:read, library:write, webhooks:manage. Response 201:
// {"token": "sdd_pat_...", "item"}; the secret is shown this once. Browser
// session only: a token cannot make tokens.
//
// spector:tags diagrams
func (h *Handler) dgCreateToken(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.dgUser(w, r)
	if !ok {
		return
	}
	var b struct {
		Name          string   `json:"name"`
		Scopes        []string `json:"scopes"`
		ExpiresInDays int      `json:"expiresInDays"`
		ProjectIDs    []string `json:"projectIds"`
	}
	if !dgDecode(w, r, &b, smallBody, false) {
		return
	}
	secret, t, err := h.Diagrams.CreateToken(r.Context(), uid, application.NewAccessToken{Name: b.Name, Scopes: b.Scopes,
		ExpiresInDays: b.ExpiresInDays, ProjectIDs: b.ProjectIDs})
	if err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusCreated, map[string]any{"token": secret, "item": tokenJSON(t)})
}

// dgListTokens lists the caller's tokens, newest first, never with secrets.
// Response: {"items"}.
//
// spector:tags diagrams
func (h *Handler) dgListTokens(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.dgUser(w, r)
	if !ok {
		return
	}
	ts, err := h.Diagrams.ListTokens(r.Context(), uid)
	if err != nil {
		dgError(w, r, err)
		return
	}
	items := make([]dgAccessToken, len(ts))
	for i, t := range ts {
		items[i] = tokenJSON(t)
	}
	dgWrite(w, http.StatusOK, map[string]any{"items": items})
}

// dgRevokeToken revokes one of the caller's tokens. Response: 204.
//
// spector:tags diagrams
func (h *Handler) dgRevokeToken(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	if err := h.Diagrams.RevokeToken(r.Context(), uid, id); err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusNoContent, nil)
}

// Public API

// v1Projects lists every project the caller can reach (within the token's
// project list). Response: {"items"} with the project shape of GET
// /projects, not paged. Scope: projects:read.
//
// spector:tags diagrams
func (h *Handler) v1Projects(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.dgUser(w, r)
	if !ok {
		return
	}
	ps, err := h.Diagrams.ListAllProjects(r.Context(), uid)
	if err != nil {
		dgError(w, r, err)
		return
	}
	items := make([]dgProject, len(ps))
	for i, p := range ps {
		items[i] = projectJSON(p)
	}
	dgWrite(w, http.StatusOK, map[string]any{"items": items})
}

// v1Files lists the file metadata of a project (no documents). Response:
// {"items"}. Scope: files:read.
//
// spector:tags diagrams
func (h *Handler) v1Files(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	t, err := h.Diagrams.Tree(r.Context(), uid, id)
	if err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusOK, map[string]any{"items": filesJSON(t.Files)})
}

// Webhooks

type dgWebhook struct {
	ID        string   `json:"id"`
	URL       string   `json:"url"`
	Events    []string `json:"events"`
	Active    bool     `json:"active"`
	CreatedAt int64    `json:"createdAt"`
}

func webhookJSON(w diagram.Webhook) dgWebhook {
	return dgWebhook{ID: w.ID, URL: w.URL, Events: w.Events, Active: w.Active, CreatedAt: ms(w.CreatedAt)}
}

type dgDelivery struct {
	ID            string  `json:"id"`
	Event         string  `json:"event"`
	Status        string  `json:"status"`
	Attempt       int     `json:"attempt"`
	ResponseCode  *int    `json:"responseCode"`
	Error         *string `json:"error"`
	NextAttemptAt *int64  `json:"nextAttemptAt"`
	CreatedAt     int64   `json:"createdAt"`
}

func deliveryJSON(d diagram.Delivery) dgDelivery {
	var e *string
	if d.Error != "" {
		e = &d.Error
	}
	return dgDelivery{ID: d.ID, Event: d.Event, Status: d.Status, Attempt: d.Attempt, ResponseCode: d.ResponseCode, Error: e,
		NextAttemptAt: msPtr(d.NextAttemptAt), CreatedAt: ms(d.CreatedAt)}
}

func (h *Handler) webhookIDs(w http.ResponseWriter, r *http.Request) (uid int, project, hook string, ok bool) {
	uid, project, ok = h.dgReq(w, r)
	if !ok {
		return
	}
	hook, ok = dgID(w, r, "wid")
	return
}

// dgListWebhooks lists a project's webhooks, never with secrets. Response:
// {"items": [{id, url, events, active, createdAt}]}. Auth: editor (or a
// token with webhooks:manage, at /v1/projects/{id}/webhooks).
//
// spector:tags diagrams
func (h *Handler) dgListWebhooks(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	hooks, err := h.Diagrams.ListWebhooks(r.Context(), uid, id)
	if err != nil {
		dgError(w, r, err)
		return
	}
	items := make([]dgWebhook, len(hooks))
	for i, k := range hooks {
		items[i] = webhookJSON(k)
	}
	dgWrite(w, http.StatusOK, map[string]any{"items": items})
}

// dgCreateWebhook adds a webhook. Body: {"url", "events"}; events are
// file.created, file.updated, file.deleted, library.updated. The URL must
// be a public http(s) address (400 WEBHOOK_URL_REJECTED otherwise).
// Response 201: {"webhook", "secret"}: the signing secret (whsec_...) is
// shown this once. Deliveries carry X-SDD-Event, X-SDD-Delivery,
// X-SDD-Timestamp and X-SDD-Signature = sha256=HMAC(secret, "<timestamp>.<body>").
//
// spector:tags diagrams
func (h *Handler) dgCreateWebhook(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	var b struct {
		URL    *string  `json:"url"`
		Events []string `json:"events"`
	}
	if !dgDecode(w, r, &b, smallBody, false) {
		return
	}
	if b.URL == nil {
		dgValidation(w, r, "body", "url: Required")
		return
	}
	hook, err := h.Diagrams.CreateWebhook(r.Context(), uid, id, *b.URL, b.Events)
	if err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusCreated, map[string]any{"webhook": webhookJSON(hook), "secret": hook.Secret})
}

// dgUpdateWebhook changes url, events or active. Response: {"webhook"}.
//
// spector:tags diagrams
func (h *Handler) dgUpdateWebhook(w http.ResponseWriter, r *http.Request) {
	uid, project, id, ok := h.webhookIDs(w, r)
	if !ok {
		return
	}
	var b struct {
		URL    *string  `json:"url"`
		Events []string `json:"events"`
		Active *bool    `json:"active"`
	}
	if !dgDecode(w, r, &b, smallBody, false) {
		return
	}
	hook, err := h.Diagrams.UpdateWebhook(r.Context(), uid, project, id, application.WebhookPatch{URL: b.URL, Events: b.Events, Active: b.Active})
	if err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusOK, map[string]any{"webhook": webhookJSON(hook)})
}

// dgDeleteWebhook removes a webhook and its delivery log. Response: 204.
//
// spector:tags diagrams
func (h *Handler) dgDeleteWebhook(w http.ResponseWriter, r *http.Request) {
	uid, project, id, ok := h.webhookIDs(w, r)
	if !ok {
		return
	}
	if err := h.Diagrams.DeleteWebhook(r.Context(), uid, project, id); err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusNoContent, nil)
}

// dgDeliveries lists the last 50 deliveries of a webhook. Response:
// {"items": [{id, event, status, attempt, responseCode, error,
// nextAttemptAt, createdAt}]}; status is pending, ok or failed.
//
// spector:tags diagrams
func (h *Handler) dgDeliveries(w http.ResponseWriter, r *http.Request) {
	uid, project, id, ok := h.webhookIDs(w, r)
	if !ok {
		return
	}
	ds, err := h.Diagrams.WebhookDeliveries(r.Context(), uid, project, id)
	if err != nil {
		dgError(w, r, err)
		return
	}
	items := make([]dgDelivery, len(ds))
	for i, d := range ds {
		items[i] = deliveryJSON(d)
	}
	dgWrite(w, http.StatusOK, map[string]any{"items": items})
}

// dgTestWebhook sends a signed ping now (one attempt, no retries) and
// returns the delivery: {"delivery"}.
//
// spector:tags diagrams
func (h *Handler) dgTestWebhook(w http.ResponseWriter, r *http.Request) {
	uid, project, id, ok := h.webhookIDs(w, r)
	if !ok {
		return
	}
	if !h.dgAllow(w, r, "dg-webhook-test", 30, time.Minute) {
		return
	}
	d, err := h.Diagrams.TestWebhook(r.Context(), uid, project, id)
	if err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusOK, map[string]any{"delivery": deliveryJSON(d)})
}
