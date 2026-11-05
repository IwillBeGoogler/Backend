package app_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/iBlog/iblog-monolith-go/config"
)

// fakeIdP is a minimal Google-like OpenID provider: discovery, JWKS, authorize (driven
// by the test as the "browser") and a token endpoint that enforces PKCE.
type fakeIdP struct {
	srv *httptest.Server
	key *rsa.PrivateKey

	mu       sync.Mutex
	claims   map[string]any // claims of the next sign-in (besides iss, aud, exp, nonce)
	badNonce bool
	codes    map[string]authz
}

type authz struct {
	challenge, nonce, redirect string
	claims                     map[string]any
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	must(t, err)
	f := &fakeIdP{key: key, codes: map[string]authz{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issuer": f.srv.URL, "authorization_endpoint": f.srv.URL + "/authorize", "token_endpoint": f.srv.URL + "/token",
			"jwks_uri": f.srv.URL + "/jwks", "response_types_supported": []string{"code"},
			"subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("form: %v", err)
		}
		f.mu.Lock()
		a, ok := f.codes[r.Form.Get("code")]
		delete(f.codes, r.Form.Get("code"))
		bad := f.badNonce
		f.mu.Unlock()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		user, pass, _ := r.BasicAuth()
		if !ok || a.redirect != r.Form.Get("redirect_uri") || base64.RawURLEncoding.EncodeToString(sum[:]) != a.challenge ||
			user != "client-id" || pass != "client-secret" {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		nonce := a.nonce
		if bad {
			nonce = "other"
		}
		sig, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: &jose.JSONWebKey{Key: key, KeyID: "k1"}}, (&jose.SignerOptions{}).WithType("JWT"))
		if err != nil {
			t.Errorf("signer: %v", err)
		}
		claims := map[string]any{"nonce": nonce}
		for k, v := range a.claims {
			claims[k] = v
		}
		raw, err := jwt.Signed(sig).Claims(jwt.Claims{Issuer: f.srv.URL, Audience: jwt.Audience{"client-id"},
			Expiry: jwt.NewNumericDate(time.Now().Add(time.Hour)), IssuedAt: jwt.NewNumericDate(time.Now())}).Claims(claims).Serialize()
		if err != nil {
			t.Errorf("sign: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "id_token": raw})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// authorize plays the browser at the provider: checks the request and returns
// the callback URL (with a fresh code) the provider would redirect to.
func (f *fakeIdP) authorize(t *testing.T, loc string) string {
	t.Helper()
	u, err := url.Parse(loc)
	must(t, err)
	q := u.Query()
	if u.Path != "/authorize" || q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" ||
		q.Get("state") == "" || q.Get("nonce") == "" || q.Get("code_challenge") == "" || q.Get("client_id") != "client-id" {
		t.Fatalf("bad authorize request: %s", loc)
	}
	code := rand.Text()
	f.mu.Lock()
	f.codes[code] = authz{q.Get("code_challenge"), q.Get("nonce"), q.Get("redirect_uri"), f.claims}
	f.mu.Unlock()
	cb, _ := url.Parse(q.Get("redirect_uri"))
	cb.RawQuery = url.Values{"code": {code}, "state": {q.Get("state")}}.Encode()
	return cb.String()
}

// fakeGitHub is a minimal GitHub: authorize, token (PKCE enforced), /user and /user/emails.
type fakeGitHub struct {
	srv *httptest.Server

	mu     sync.Mutex
	id     int
	login  string
	name   string
	emails []map[string]any
	codes  map[string]authz
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{codes: map[string]authz{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		a, ok := f.codes[r.Form.Get("code")]
		delete(f.codes, r.Form.Get("code"))
		f.mu.Unlock()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		user, pass, _ := r.BasicAuth()
		id, secret := r.Form.Get("client_id"), r.Form.Get("client_secret")
		if user != "" {
			id, secret = user, pass
		}
		if !ok || id != "gh-id" || secret != "gh-secret" || base64.RawURLEncoding.EncodeToString(sum[:]) != a.challenge {
			http.Error(w, `{"error":"bad_verification_code"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"access_token": "gh-token", "token_type": "bearer", "scope": "read:user,user:email"})
	})
	authed := func(h func(w http.ResponseWriter)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer gh-token" {
				http.Error(w, "{}", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			f.mu.Lock()
			defer f.mu.Unlock()
			h(w)
		}
	}
	mux.HandleFunc("/user", authed(func(w http.ResponseWriter) {
		json.NewEncoder(w).Encode(map[string]any{"id": f.id, "login": f.login, "name": f.name})
	}))
	mux.HandleFunc("/user/emails", authed(func(w http.ResponseWriter) { json.NewEncoder(w).Encode(f.emails) }))
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGitHub) authorize(t *testing.T, loc string) string {
	t.Helper()
	u, err := url.Parse(loc)
	must(t, err)
	q := u.Query()
	if u.Path != "/authorize" || q.Get("code_challenge_method") != "S256" || q.Get("state") == "" || q.Get("client_id") != "gh-id" ||
		!strings.Contains(q.Get("scope"), "user:email") {
		t.Fatalf("bad authorize request: %s", loc)
	}
	code := rand.Text()
	f.mu.Lock()
	f.codes[code] = authz{challenge: q.Get("code_challenge")}
	f.mu.Unlock()
	cb, _ := url.Parse(q.Get("redirect_uri"))
	cb.RawQuery = url.Values{"code": {code}, "state": {q.Get("state")}}.Encode()
	return cb.String()
}

var noFollow = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// begin starts a sign-in and returns the provider URL the app redirected to.
func begin(t *testing.T, e *env, provider string) string {
	t.Helper()
	res, err := noFollow.Get(e.srv.URL + "/api/auth/oauth/" + provider + "/login")
	must(t, err)
	res.Body.Close()
	if res.StatusCode != 302 {
		t.Fatalf("login = %d", res.StatusCode)
	}
	return res.Header.Get("Location")
}

func (f *fakeIdP) signIn(t *testing.T, e *env, claims map[string]any) url.Values {
	t.Helper()
	f.mu.Lock()
	f.claims = claims
	f.mu.Unlock()
	return callback(t, e, "google", f.authorize(t, begin(t, e, "google")))
}

func (f *fakeGitHub) signIn(t *testing.T, e *env, id int, login, name string, emails ...map[string]any) url.Values {
	t.Helper()
	f.mu.Lock()
	f.id, f.login, f.name, f.emails = id, login, name, emails
	f.mu.Unlock()
	return callback(t, e, "github", f.authorize(t, begin(t, e, "github")))
}

func callback(t *testing.T, e *env, provider, cb string) url.Values {
	t.Helper()
	u, _ := url.Parse(cb)
	if u.Path != "/api/auth/oauth/"+provider+"/callback" {
		t.Fatalf("redirect_uri = %s", cb)
	}
	res, err := noFollow.Get(e.srv.URL + u.RequestURI())
	must(t, err)
	res.Body.Close()
	loc, err := url.Parse(res.Header.Get("Location"))
	must(t, err)
	if res.StatusCode != 302 || loc.Scheme+"://"+loc.Host+loc.Path != "http://site.test/auth/callback" || loc.RawQuery != "" {
		t.Fatalf("callback = %d %s", res.StatusCode, loc)
	}
	frag, err := url.ParseQuery(loc.Fragment)
	must(t, err)
	return frag
}

type me struct {
	ID            int    `json:"id"`
	Username      string `json:"username"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
}

func whoAmI(t *testing.T, c *client, f url.Values) me {
	t.Helper()
	if f.Get("token") == "" {
		t.Fatalf("no token: %v", f)
	}
	var m me
	c.as(session{Token: f.Get("token")}).do("GET", "/api/me", nil, 200, &m)
	return m
}

func email(addr string, primary, verified bool) map[string]any {
	return map[string]any{"email": addr, "primary": primary, "verified": verified}
}

func TestSocialLogin(t *testing.T) {
	idp, gh := newFakeIdP(t), newFakeGitHub(t)
	e := start(t, func(c *config.Config) {
		c.RateLimitMultiplier = 10 // the flows make many round trips from one IP
		c.GoogleClientID, c.GoogleClientSecret, c.GoogleIssuer = "client-id", "client-secret", idp.srv.URL
		c.GitHubClientID, c.GitHubClientSecret = "gh-id", "gh-secret"
		c.GitHubAuthURL, c.GitHubTokenURL, c.GitHubAPIURL = gh.srv.URL+"/authorize", gh.srv.URL+"/token", gh.srv.URL
	})
	anon := e.anon(t)

	var raw struct {
		SSO map[string]struct{ Enabled bool } `json:"sso"`
	}
	anon.do("GET", "/api/config", nil, 200, &raw)
	if !raw.SSO["google"].Enabled || !raw.SSO["github"].Enabled {
		t.Fatalf("config = %v", raw)
	}
	anon.do("GET", "/api/auth/oauth/facebook/login", nil, 404, nil)

	// Google: unknown verified email creates a verified account with a deduped-able username.
	m1 := whoAmI(t, anon, idp.signIn(t, e, map[string]any{"sub": "g1", "email": "New.Person@corp.test", "email_verified": true, "name": "New Person"}))
	if m1.Email != "new.person@corp.test" || m1.Username != "New_Person" || !m1.EmailVerified {
		t.Fatalf("me = %+v", m1)
	}
	// Later sign-in matches by subject even though the email changed: no second account.
	m2 := whoAmI(t, anon, idp.signIn(t, e, map[string]any{"sub": "g1", "email": "renamed@corp.test", "email_verified": true, "name": "New Person"}))
	if m2.ID != m1.ID || m2.Email != m1.Email {
		t.Fatalf("subject sign-in = %+v, want user %d", m2, m1.ID)
	}
	// The same name from someone else gets another username.
	m3 := whoAmI(t, anon, idp.signIn(t, e, map[string]any{"sub": "g3", "email": "other@corp.test", "email_verified": "true", "name": "New Person"}))
	if m3.ID == m1.ID || m3.Username == m1.Username || !strings.HasPrefix(m3.Username, "New_Person") {
		t.Fatalf("second person = %+v", m3)
	}

	// Existing unverified local account is linked by email; the sign-up password stops working.
	ali := anon.register("ali")
	m4 := whoAmI(t, anon, idp.signIn(t, e, map[string]any{"sub": "g4", "email": "ali@example.com", "email_verified": true}))
	if m4.ID != ali.user.ID || !m4.EmailVerified {
		t.Fatalf("linked = %+v, want user %d", m4, ali.user.ID)
	}
	anon.do("POST", "/api/auth/login", map[string]string{"login": "ali", "password": "secret123"}, 401, nil)

	// GitHub: the primary verified email is used and links to the same account.
	m5 := whoAmI(t, anon, gh.signIn(t, e, 4242, "octo", "", email("secondary@x.test", false, true), email("ali@example.com", true, true)))
	if m5.ID != ali.user.ID {
		t.Fatalf("github link = %+v, want user %d", m5, ali.user.ID)
	}
	// New GitHub user: username from the login, then found again by subject.
	m6 := whoAmI(t, anon, gh.signIn(t, e, 777, "octocat", "", email("octo@gh.test", true, true)))
	if m6.Username != "octocat" || !m6.EmailVerified || m6.Email != "octo@gh.test" {
		t.Fatalf("github new = %+v", m6)
	}
	m7 := whoAmI(t, anon, gh.signIn(t, e, 777, "octocat", "", email("changed@gh.test", true, true)))
	if m7.ID != m6.ID {
		t.Fatalf("github subject = %+v", m7)
	}

	// Refusals come back as an error code in the fragment, never a token.
	for name, f := range map[string]url.Values{
		"google unverified": idp.signIn(t, e, map[string]any{"sub": "u1", "email": "a@example.com", "email_verified": false}),
		"google no claim":   idp.signIn(t, e, map[string]any{"sub": "u2", "email": "a@example.com"}),
		"google no email":   idp.signIn(t, e, map[string]any{"sub": "u3", "email_verified": true}),
		"github unverified": gh.signIn(t, e, 1, "x", "", email("a@example.com", true, false)),
		"github no primary": gh.signIn(t, e, 2, "y", "", email("a@example.com", false, true)),
	} {
		if f.Get("error") == "" || f.Get("token") != "" {
			t.Fatalf("%s: fragment = %v", name, f)
		}
	}

	// State is single use; unknown states and provider errors are refused.
	idp.mu.Lock()
	idp.claims = map[string]any{"sub": "g1", "email": "x@corp.test", "email_verified": true}
	idp.mu.Unlock()
	cb := idp.authorize(t, begin(t, e, "google"))
	if f := callback(t, e, "google", cb); f.Get("token") == "" {
		t.Fatalf("first use: %v", f)
	}
	if f := callback(t, e, "google", cb); f.Get("error") != "failed" {
		t.Fatalf("replay: %v", f)
	}
	// A state of one provider is useless at the other.
	cb = gh.authorize(t, begin(t, e, "github"))
	if f := callback(t, e, "google", strings.Replace(cb, "/github/", "/google/", 1)); f.Get("error") != "failed" {
		t.Fatalf("cross provider: %v", f)
	}
	if f := callback(t, e, "google", "/api/auth/oauth/google/callback?code=x&state=nope"); f.Get("error") != "failed" {
		t.Fatalf("unknown state: %v", f)
	}
	if f := callback(t, e, "github", "/api/auth/oauth/github/callback?error=access_denied"); f.Get("error") != "failed" {
		t.Fatalf("provider error: %v", f)
	}

	// An ID token with the wrong nonce is rejected.
	idp.mu.Lock()
	idp.badNonce = true
	idp.mu.Unlock()
	if f := idp.signIn(t, e, map[string]any{"sub": "g1", "email": "x@corp.test", "email_verified": true}); f.Get("error") != "failed" || f.Get("token") != "" {
		t.Fatalf("bad nonce: %v", f)
	}
}
