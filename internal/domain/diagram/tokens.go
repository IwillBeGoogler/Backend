package diagram

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Scopes a personal access token may hold. A route declares the one scope it
// needs; routes that declare none are for a browser session only.
const (
	ScopeProjectsRead  = "projects:read"
	ScopeFilesRead     = "files:read"
	ScopeFilesWrite    = "files:write"
	ScopeLibraryRead   = "library:read"
	ScopeLibraryWrite  = "library:write"
	ScopeWebhooksAdmin = "webhooks:manage"
)

// Scopes lists every scope.
var Scopes = []string{ScopeProjectsRead, ScopeFilesRead, ScopeFilesWrite, ScopeLibraryRead, ScopeLibraryWrite, ScopeWebhooksAdmin}

// TokenPrefix starts every personal access token.
const TokenPrefix = "sdd_pat_"

var tokenRe = regexp.MustCompile(`^sdd_pat_[A-Za-z0-9_-]{32}$`)

// Limits of a token request, as in the Node server.
const (
	MaxTokenNameLen = 100
	MaxTokenDays    = 3650
	MaxTokenProject = 100
)

// AccessToken is a personal access token as stored: the secret itself is
// shown once, when it is made, and only its sha256 is kept.
type AccessToken struct {
	ID     string
	UserID int
	Name   string
	Hash   string
	Prefix string
	Scopes []string
	// ProjectIDs limits the token to these projects; nil reaches every
	// project its owner can.
	ProjectIDs []string
	ExpiresAt  *time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
	CreatedAt  time.Time
}

// PrefixToken is the id prefix of tokens.
const PrefixToken = "tok"

// GenerateToken makes a token: "sdd_pat_" + 32 url-safe characters (192
// bits). The caller keeps token only long enough to show it once.
func GenerateToken() (token, hash, prefix string) {
	var b [24]byte
	_, _ = rand.Read(b[:])
	token = TokenPrefix + base64.RawURLEncoding.EncodeToString(b[:])
	return token, HashToken(token), token[:8]
}

// TokenLooksValid reports whether s has the shape of a token.
func TokenLooksValid(s string) bool { return tokenRe.MatchString(s) }

// ParseBearer returns the token of an "Authorization: Bearer sdd_pat_..."
// header, or "" when the header is absent or holds anything else (a
// session token, an API key).
func ParseBearer(header string) string {
	f := strings.Fields(header)
	if len(f) != 2 || !strings.EqualFold(f[0], "bearer") || !TokenLooksValid(f[1]) {
		return ""
	}
	return f[1]
}

// Allows reports whether the token holds scope.
func (t AccessToken) Allows(scope string) bool {
	return scope != "" && slices.Contains(t.Scopes, scope)
}

// Reaches reports whether the token may touch the project.
func (t AccessToken) Reaches(projectID string) bool {
	return t.ProjectIDs == nil || slices.Contains(t.ProjectIDs, projectID)
}

// Usable reports whether the token is neither revoked nor expired at now.
func (t AccessToken) Usable(now time.Time) bool {
	return t.RevokedAt == nil && (t.ExpiresAt == nil || t.ExpiresAt.After(now))
}

// ValidScope reports whether s is a scope.
func ValidScope(s string) bool { return slices.Contains(Scopes, s) }

// TokenRepository stores personal access tokens. It returns
// domain.ErrNotFound for an unknown (or already revoked) token.
type TokenRepository interface {
	CreateToken(ctx context.Context, t AccessToken) error
	// Tokens lists a user's tokens that are not revoked, newest first.
	Tokens(ctx context.Context, userID int) ([]AccessToken, error)
	// TokenByHash returns the token, revoked and expired ones too.
	TokenByHash(ctx context.Context, hash string) (AccessToken, error)
	// RevokeToken revokes the user's own token.
	RevokeToken(ctx context.Context, userID int, id string, at time.Time) error
	// TouchToken sets last_used_at when it is older than a minute.
	TouchToken(ctx context.Context, id string, at time.Time) error
}
