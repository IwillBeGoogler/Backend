// Package social signs readers in with Google (OpenID Connect) and GitHub
// (plain OAuth2). Both use the authorization code flow with PKCE and a
// random single-use state; flow secrets live in Redis for a few minutes, so
// it works behind several API instances.
package social

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/redis/go-redis/v9"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
)

const flowTTL = 10 * time.Minute

// ErrFlow means the state is unknown, expired or already used.
var ErrFlow = errors.New("social: unknown or expired sign-in")

// Config configures one provider. Endpoint fields default to the real
// provider; tests point them at fakes.
type Config struct {
	ClientID, ClientSecret, RedirectURL string
	Issuer                              string // Google
	AuthURL, TokenURL, APIURL           string // GitHub
}

// Provider is one social login.
type Provider interface {
	Begin(ctx context.Context) (authURL string, err error)
	Finish(ctx context.Context, state, code string) (application.ExternalIdentity, error)
}

type base struct {
	name string
	rdb  *redis.Client
	http *http.Client
}

type flow struct {
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
}

func random() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (b base) key(state string) string {
	sum := sha256.Sum256([]byte(state))
	return "social:" + b.name + ":" + hex.EncodeToString(sum[:])
}

// begin stores a new flow and returns its state, nonce and PKCE verifier.
func (b base) begin(ctx context.Context) (state string, f flow, err error) {
	if state, err = random(); err != nil {
		return
	}
	if f.Nonce, err = random(); err != nil {
		return
	}
	f.Verifier = oauth2.GenerateVerifier()
	raw, _ := json.Marshal(f)
	err = b.rdb.Set(ctx, b.key(state), raw, flowTTL).Err()
	return
}

// take redeems a state exactly once.
func (b base) take(ctx context.Context, state, code string) (flow, error) {
	var f flow
	if state == "" || code == "" {
		return f, ErrFlow
	}
	raw, err := b.rdb.GetDel(ctx, b.key(state)).Result()
	if errors.Is(err, redis.Nil) {
		return f, ErrFlow
	}
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal([]byte(raw), &f); err != nil {
		return f, ErrFlow
	}
	return f, nil
}

func newBase(name string, rdb *redis.Client) base {
	return base{name: name, rdb: rdb, http: &http.Client{Timeout: 10 * time.Second}}
}

// Google is OpenID Connect with discovery; the ID token is verified
// (signature, issuer, audience, expiry, nonce) and must carry a verified email.
type Google struct {
	base
	cfg Config

	mu       sync.Mutex
	provider *oidc.Provider
}

func NewGoogle(cfg Config, rdb *redis.Client) *Google {
	if cfg.Issuer == "" {
		cfg.Issuer = "https://accounts.google.com"
	}
	return &Google{base: newBase("google", rdb), cfg: cfg}
}

func (g *Google) discover(ctx context.Context) (*oidc.Provider, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.provider != nil {
		return g.provider, nil
	}
	p, err := oidc.NewProvider(oidc.ClientContext(ctx, g.http), g.cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("google discovery: %w", err)
	}
	g.provider = p
	return p, nil
}

func (g *Google) oauth(p *oidc.Provider) *oauth2.Config {
	return &oauth2.Config{ClientID: g.cfg.ClientID, ClientSecret: g.cfg.ClientSecret, Endpoint: p.Endpoint(),
		RedirectURL: g.cfg.RedirectURL, Scopes: []string{oidc.ScopeOpenID, "email", "profile"}}
}

func (g *Google) Begin(ctx context.Context) (string, error) {
	p, err := g.discover(ctx)
	if err != nil {
		return "", err
	}
	state, f, err := g.begin(ctx)
	if err != nil {
		return "", err
	}
	return g.oauth(p).AuthCodeURL(state, oauth2.S256ChallengeOption(f.Verifier), oidc.Nonce(f.Nonce)), nil
}

func (g *Google) Finish(ctx context.Context, state, code string) (application.ExternalIdentity, error) {
	var zero application.ExternalIdentity
	f, err := g.take(ctx, state, code)
	if err != nil {
		return zero, err
	}
	p, err := g.discover(ctx)
	if err != nil {
		return zero, err
	}
	hctx := oidc.ClientContext(ctx, g.http)
	tok, err := g.oauth(p).Exchange(hctx, code, oauth2.VerifierOption(f.Verifier))
	if err != nil {
		return zero, fmt.Errorf("google exchange: %w", err)
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		return zero, errors.New("google: no id_token")
	}
	idt, err := p.Verifier(&oidc.Config{ClientID: g.cfg.ClientID}).Verify(hctx, raw)
	if err != nil {
		return zero, fmt.Errorf("google verify: %w", err)
	}
	if idt.Nonce != f.Nonce {
		return zero, errors.New("google: nonce mismatch")
	}
	var cl struct {
		Email         string `json:"email"`
		EmailVerified any    `json:"email_verified"`
		Name          string `json:"name"`
	}
	if err := idt.Claims(&cl); err != nil {
		return zero, err
	}
	return application.ExternalIdentity{Subject: idt.Subject, Email: cl.Email, EmailVerified: truthy(cl.EmailVerified), Name: cl.Name}, nil
}

// truthy accepts a JSON true or the string "true".
func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return strings.EqualFold(x, "true")
	}
	return false
}

// GitHub is OAuth2 without OpenID Connect: after the code exchange it reads
// /user and /user/emails and uses the primary verified email.
type GitHub struct {
	base
	cfg Config
}

func NewGitHub(cfg Config, rdb *redis.Client) *GitHub {
	if cfg.AuthURL == "" {
		cfg.AuthURL = github.Endpoint.AuthURL
	}
	if cfg.TokenURL == "" {
		cfg.TokenURL = github.Endpoint.TokenURL
	}
	if cfg.APIURL == "" {
		cfg.APIURL = "https://api.github.com"
	}
	cfg.APIURL = strings.TrimRight(cfg.APIURL, "/")
	return &GitHub{base: newBase("github", rdb), cfg: cfg}
}

func (g *GitHub) oauth() *oauth2.Config {
	return &oauth2.Config{ClientID: g.cfg.ClientID, ClientSecret: g.cfg.ClientSecret, RedirectURL: g.cfg.RedirectURL,
		Endpoint: oauth2.Endpoint{AuthURL: g.cfg.AuthURL, TokenURL: g.cfg.TokenURL}, Scopes: []string{"read:user", "user:email"}}
}

func (g *GitHub) Begin(ctx context.Context) (string, error) {
	state, f, err := g.begin(ctx)
	if err != nil {
		return "", err
	}
	return g.oauth().AuthCodeURL(state, oauth2.S256ChallengeOption(f.Verifier)), nil
}

func (g *GitHub) get(ctx context.Context, tok, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.cfg.APIURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")
	res, err := g.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("github %s: %s", path, res.Status)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(out)
}

func (g *GitHub) Finish(ctx context.Context, state, code string) (application.ExternalIdentity, error) {
	var zero application.ExternalIdentity
	f, err := g.take(ctx, state, code)
	if err != nil {
		return zero, err
	}
	tok, err := g.oauth().Exchange(context.WithValue(ctx, oauth2.HTTPClient, g.http), code, oauth2.VerifierOption(f.Verifier))
	if err != nil {
		return zero, fmt.Errorf("github exchange: %w", err)
	}
	var u struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
		Name  string `json:"name"`
	}
	if err := g.get(ctx, tok.AccessToken, "/user", &u); err != nil {
		return zero, err
	}
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := g.get(ctx, tok.AccessToken, "/user/emails", &emails); err != nil {
		return zero, err
	}
	id := application.ExternalIdentity{Subject: strconv.FormatInt(u.ID, 10), Name: u.Name}
	if strings.TrimSpace(id.Name) == "" {
		id.Name = u.Login
	}
	for _, e := range emails {
		if e.Primary && e.Verified {
			id.Email, id.EmailVerified = e.Email, true
		}
	}
	return id, nil
}
