package application

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"go.uber.org/zap"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
)

// Personal access tokens for the public API and MCP. A token acts as its
// owner: the owner's project role still decides what a call may do, the
// token's scopes and project list only narrow it.

type tokenLimitKey struct{}

type tokenLimit struct{ projects []string }

// WithTokenLimit marks ctx as acting through an access token limited to
// the given projects (nil: not limited). Every project access check then
// treats other projects as not found.
func WithTokenLimit(ctx context.Context, projectIDs []string) context.Context {
	if projectIDs == nil {
		return ctx
	}
	return context.WithValue(ctx, tokenLimitKey{}, tokenLimit{projectIDs})
}

// tokenReaches reports whether the token in ctx (if any) may touch the project.
func tokenReaches(ctx context.Context, projectID string) bool {
	l, ok := ctx.Value(tokenLimitKey{}).(tokenLimit)
	return !ok || slices.Contains(l.projects, projectID)
}

// NewAccessToken is a token request.
type NewAccessToken struct {
	Name          string
	Scopes        []string
	ExpiresInDays int // 0: never
	ProjectIDs    []string
}

func (d *Diagrams) needTokens() error {
	if d.Tokens == nil {
		return errors.New("diagram token repository not configured")
	}
	return nil
}

// CreateToken makes a token for the caller and returns it with its secret,
// which is not stored and cannot be shown again. Every project in the
// limit must be one the caller can read.
func (d *Diagrams) CreateToken(ctx context.Context, actor int, in NewAccessToken) (string, diagram.AccessToken, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.CreateToken")
	defer span.End()
	if err := d.needTokens(); err != nil {
		return "", diagram.AccessToken{}, err
	}
	name := strings.TrimSpace(in.Name)
	switch {
	case name == "":
		return "", diagram.AccessToken{}, diagram.Validation("name: required")
	case utf8.RuneCountInString(name) > diagram.MaxTokenNameLen:
		return "", diagram.AccessToken{}, diagram.Validation("name: at most 100 characters")
	case len(in.Scopes) == 0 || len(in.Scopes) > len(diagram.Scopes):
		return "", diagram.AccessToken{}, diagram.Validation("scopes: choose at least one scope")
	case in.ExpiresInDays < 0 || in.ExpiresInDays > diagram.MaxTokenDays:
		return "", diagram.AccessToken{}, diagram.Validation("expiresInDays: must be between 1 and 3650")
	case in.ProjectIDs != nil && (len(in.ProjectIDs) == 0 || len(in.ProjectIDs) > diagram.MaxTokenProject):
		return "", diagram.AccessToken{}, diagram.Validation("projectIds: 1-100 projects")
	}
	var scopes []string
	for _, s := range in.Scopes {
		if !diagram.ValidScope(s) {
			return "", diagram.AccessToken{}, diagram.Validation("scopes: unknown scope " + s)
		}
		if !slices.Contains(scopes, s) {
			scopes = append(scopes, s)
		}
	}
	var projects []string
	for _, id := range in.ProjectIDs {
		if id == "" || len(id) > 64 {
			return "", diagram.AccessToken{}, diagram.Validation("projectIds: each id is 1-64 characters")
		}
		if slices.Contains(projects, id) {
			continue
		}
		if _, err := d.projectAccess(ctx, actor, id, diagram.RoleViewer); err != nil {
			return "", diagram.AccessToken{}, err
		}
		projects = append(projects, id)
	}
	secret, hash, prefix := diagram.GenerateToken()
	now := d.clock()
	t := diagram.AccessToken{ID: diagram.NewID(diagram.PrefixToken), UserID: actor, Name: name, Hash: hash, Prefix: prefix,
		Scopes: scopes, ProjectIDs: projects, CreatedAt: now}
	if in.ExpiresInDays > 0 {
		at := now.Add(time.Duration(in.ExpiresInDays) * 24 * time.Hour)
		t.ExpiresAt = &at
	}
	if err := d.Tokens.CreateToken(ctx, t); err != nil {
		return "", diagram.AccessToken{}, err
	}
	return secret, t, nil
}

// ListTokens lists the caller's tokens that are not revoked.
func (d *Diagrams) ListTokens(ctx context.Context, actor int) ([]diagram.AccessToken, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.ListTokens")
	defer span.End()
	if err := d.needTokens(); err != nil {
		return nil, err
	}
	return d.Tokens.Tokens(ctx, actor)
}

// RevokeToken revokes one of the caller's tokens.
func (d *Diagrams) RevokeToken(ctx context.Context, actor int, id string) error {
	ctx, span := tracer.Start(ctx, "Diagrams.RevokeToken")
	defer span.End()
	if err := d.needTokens(); err != nil {
		return err
	}
	if err := d.Tokens.RevokeToken(ctx, actor, id, d.clock()); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return diagram.NotFound("Token")
		}
		return err
	}
	return nil
}

// AuthenticateToken resolves a bearer token. It returns
// domain.ErrUnauthorized for an unknown, revoked or expired one. The last
// use is noted at most once a minute.
func (d *Diagrams) AuthenticateToken(ctx context.Context, raw string) (diagram.AccessToken, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.AuthenticateToken")
	defer span.End()
	if d.Tokens == nil || !diagram.TokenLooksValid(raw) {
		return diagram.AccessToken{}, domain.ErrUnauthorized
	}
	t, err := d.Tokens.TokenByHash(ctx, diagram.HashToken(raw))
	if errors.Is(err, domain.ErrNotFound) {
		return t, domain.ErrUnauthorized
	}
	if err != nil {
		return t, err
	}
	now := d.clock()
	if !t.Usable(now) {
		return diagram.AccessToken{}, domain.ErrUnauthorized
	}
	if err := d.Tokens.TouchToken(ctx, t.ID, now); err != nil {
		zap.L().Warn("diagram: note token use", zap.String("token", t.ID), zap.Error(err))
	}
	return t, nil
}

// ListAllProjects lists every project the caller owns or belongs to, within
// the access token's limit when ctx has one (the public API is not paged).
func (d *Diagrams) ListAllProjects(ctx context.Context, actor int) ([]diagram.ProjectSummary, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.ListAllProjects")
	defer span.End()
	var out []diagram.ProjectSummary
	for page := 1; ; page++ {
		items, total, err := d.Repo.ListProjects(ctx, actor, diagram.ProjectQuery{Page: page, PageSize: 100})
		if err != nil {
			return nil, err
		}
		for _, p := range items {
			if tokenReaches(ctx, p.ID) {
				out = append(out, p)
			}
		}
		if len(items) == 0 || page*100 >= total {
			return out, nil
		}
	}
}
