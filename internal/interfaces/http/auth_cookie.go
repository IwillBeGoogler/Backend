package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
)

// Tokens: login and refresh answer {access_token, expires_at, refresh_token,
// refresh_expires_at}. The client keeps both and sends the access token as
// Authorization: Bearer, the refresh token to POST /api/auth/refresh.

// respondSession answers with a session (access and refresh token).
func (h *Handler) respondSession(w http.ResponseWriter, r *http.Request, status int, s application.Session, err error) {
	if err == nil {
		w.Header().Set("Cache-Control", "no-store")
	}
	respond(w, r, status, s, err, "")
}

type refreshInput struct {
	RefreshToken string `json:"refresh_token"`
}

// refreshToken reads the refresh token from the JSON body {"refresh_token"}
// or Authorization: Bearer <refresh token> (an access token there is not a
// refresh token and is ignored).
func refreshToken(r *http.Request) string {
	if r.Body != nil && r.ContentLength != 0 {
		var in refreshInput
		if json.NewDecoder(http.MaxBytesReader(nil, r.Body, 4<<10)).Decode(&in) == nil && in.RefreshToken != "" {
			return in.RefreshToken
		}
	}
	if b := bearer(r); b != "" && !looksLikeJWT(b) {
		return b
	}
	return ""
}

func looksLikeJWT(s string) bool { return strings.HasPrefix(s, "eyJ") && strings.Count(s, ".") == 2 }

func unauthorized(err error) bool { return errors.Is(err, domain.ErrUnauthorized) }
