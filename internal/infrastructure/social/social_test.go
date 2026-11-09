package social

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

func redisClient(t *testing.T) *redis.Client {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test")
	}
	ctx := context.Background()
	rd, err := tcredis.Run(ctx, "redis:8-alpine")
	if err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	t.Cleanup(func() { testcontainers.TerminateContainer(rd) })
	u, err := rd.ConnectionString(ctx)
	if err != nil {
		t.Fatal(err)
	}
	opt, err := redis.ParseURL(u)
	if err != nil {
		t.Fatal(err)
	}
	c := redis.NewClient(opt)
	t.Cleanup(func() { c.Close() })
	return c
}

func stateOf(t *testing.T, authURL string) (state string, q url.Values) {
	t.Helper()
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	q = u.Query()
	if q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
		t.Errorf("no PKCE in %s", authURL)
	}
	return q.Get("state"), q
}

func TestTruthy(t *testing.T) {
	for _, c := range []struct {
		in   any
		want bool
	}{{true, true}, {false, false}, {"true", true}, {"TRUE", true}, {"no", false}, {nil, false}, {1, false}} {
		if got := truthy(c.in); got != c.want {
			t.Errorf("truthy(%v) = %v", c.in, got)
		}
	}
}

func TestTakeBranches(t *testing.T) {
	rdb := redisClient(t)
	ctx := context.Background()
	b := newBase("x", rdb)
	if _, err := b.take(ctx, "", "c"); err != ErrFlow {
		t.Errorf("empty state: %v", err)
	}
	if _, err := b.take(ctx, "s", ""); err != ErrFlow {
		t.Errorf("empty code: %v", err)
	}
	if _, err := b.take(ctx, "missing", "c"); err != ErrFlow {
		t.Errorf("missing: %v", err)
	}
	rdb.Set(ctx, b.key("bad"), "{not json", time.Minute)
	if _, err := b.take(ctx, "bad", "c"); err != ErrFlow {
		t.Errorf("bad json: %v", err)
	}
	// Redis error (cancelled context) is propagated, not ErrFlow.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := b.take(cctx, "s", "c"); err == nil || err == ErrFlow {
		t.Errorf("redis error: %v", err)
	}
	if _, _, err := b.begin(cctx); err == nil {
		t.Error("begin with dead redis succeeded")
	}
}

func TestGitHub(t *testing.T) {
	rdb := redisClient(t)
	ctx := context.Background()
	var userStatus, emailsStatus = 200, 200
	var user, emails any
	mux := http.NewServeMux()
	mux.HandleFunc("/login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("code") == "badcode" {
			http.Error(w, "nope", 400)
			return
		}
		if r.Form.Get("code_verifier") == "" {
			http.Error(w, "no verifier", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"tok","token_type":"bearer"}`))
	})
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "auth", 401)
			return
		}
		if userStatus != 200 {
			http.Error(w, "x", userStatus)
			return
		}
		json.NewEncoder(w).Encode(user)
	})
	mux.HandleFunc("/user/emails", func(w http.ResponseWriter, r *http.Request) {
		if emailsStatus != 200 {
			http.Error(w, "x", emailsStatus)
			return
		}
		json.NewEncoder(w).Encode(emails)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	gh := NewGitHub(Config{ClientID: "cid", ClientSecret: "sec", RedirectURL: "http://cb",
		AuthURL: srv.URL + "/login/oauth/authorize", TokenURL: srv.URL + "/login/oauth/access_token", APIURL: srv.URL + "/"}, rdb)

	begin := func() string {
		a, err := gh.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		s, q := stateOf(t, a)
		if q.Get("client_id") != "cid" || s == "" {
			t.Errorf("auth url %s", a)
		}
		return s
	}

	user = map[string]any{"id": 77, "login": "octo", "name": "  "}
	emails = []map[string]any{{"email": "a@x.io", "primary": false, "verified": true}, {"email": "p@x.io", "primary": true, "verified": true}}
	id, err := gh.Finish(ctx, begin(), "good")
	if err != nil || id.Subject != "77" || id.Email != "p@x.io" || !id.EmailVerified || id.Name != "octo" {
		t.Fatalf("id %+v err %v", id, err)
	}

	// state is single use
	s := begin()
	if _, err := gh.Finish(ctx, s, "good"); err != nil {
		t.Fatal(err)
	}
	if _, err := gh.Finish(ctx, s, "good"); err != ErrFlow {
		t.Errorf("replay: %v", err)
	}

	// no verified primary email
	emails = []map[string]any{{"email": "p@x.io", "primary": true, "verified": false}}
	user = map[string]any{"id": 1, "login": "o", "name": "Full Name"}
	id, err = gh.Finish(ctx, begin(), "good")
	if err != nil || id.Email != "" || id.EmailVerified || id.Name != "Full Name" {
		t.Errorf("unverified: %+v %v", id, err)
	}

	if _, err := gh.Finish(ctx, begin(), "badcode"); err == nil || !strings.Contains(err.Error(), "exchange") {
		t.Errorf("exchange: %v", err)
	}
	userStatus = 500
	if _, err := gh.Finish(ctx, begin(), "good"); err == nil || !strings.Contains(err.Error(), "/user") {
		t.Errorf("user: %v", err)
	}
	userStatus, emailsStatus = 200, 500
	if _, err := gh.Finish(ctx, begin(), "good"); err == nil || !strings.Contains(err.Error(), "/user/emails") {
		t.Errorf("emails: %v", err)
	}
	if _, err := gh.Finish(ctx, "", "x"); err != ErrFlow {
		t.Errorf("empty: %v", err)
	}

	// get: transport error, bad request URL, bad JSON
	if err := gh.get(ctx, "tok", "/\x7f", nil); err == nil {
		t.Error("bad url accepted")
	}
	dead := &GitHub{base: newBase("github", rdb), cfg: Config{APIURL: "http://127.0.0.1:1"}}
	if err := dead.get(ctx, "t", "/user", &struct{}{}); err == nil {
		t.Error("dead server")
	}
	mux.HandleFunc("/junk", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("not json")) })
	if err := gh.get(ctx, "tok", "/junk", &struct{}{}); err == nil {
		t.Error("junk json accepted")
	}

	// Begin with dead redis
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := gh.Begin(cctx); err == nil {
		t.Error("begin dead redis")
	}
}

func TestGitHubDefaults(t *testing.T) {
	g := NewGitHub(Config{}, nil)
	if g.cfg.APIURL != "https://api.github.com" || g.cfg.AuthURL == "" || g.cfg.TokenURL == "" {
		t.Errorf("defaults %+v", g.cfg)
	}
}

type fakeOIDC struct {
	srv      *httptest.Server
	key      *rsa.PrivateKey
	nonce    string // empty: echo nonce stored by test
	claims   map[string]any
	noToken  bool
	badSig   bool
	tokenErr bool
	discErr  bool
}

func newFakeOIDC(t *testing.T) *fakeOIDC {
	f := &fakeOIDC{}
	f.key, _ = rsa.GenerateKey(rand.Reader, 2048)
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		if f.discErr {
			http.Error(w, "x", 500)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"issuer": f.srv.URL, "authorization_endpoint": f.srv.URL + "/auth",
			"token_endpoint": f.srv.URL + "/token", "jwks_uri": f.srv.URL + "/jwks", "id_token_signing_alg_values_supported": []string{"RS256"}})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &f.key.PublicKey, KeyID: "k", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if f.tokenErr || r.Form.Get("code_verifier") == "" {
			http.Error(w, "bad", 400)
			return
		}
		out := map[string]any{"access_token": "at", "token_type": "Bearer"}
		if !f.noToken {
			signKey := f.key
			if f.badSig {
				signKey, _ = rsa.GenerateKey(rand.Reader, 2048)
			}
			sig, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: signKey}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "k"))
			cl := jwt.Claims{Issuer: f.srv.URL, Subject: "sub1", Audience: jwt.Audience{"cid"}, Expiry: jwt.NewNumericDate(time.Now().Add(time.Hour)), IssuedAt: jwt.NewNumericDate(time.Now())}
			raw, _ := jwt.Signed(sig).Claims(cl).Claims(f.claims).Serialize()
			out["id_token"] = raw
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func TestGoogle(t *testing.T) {
	rdb := redisClient(t)
	ctx := context.Background()
	f := newFakeOIDC(t)
	g := NewGoogle(Config{ClientID: "cid", ClientSecret: "s", RedirectURL: "http://cb", Issuer: f.srv.URL}, rdb)

	// Begin returns state; fetch the stored nonce to craft a matching token.
	begin := func() string {
		a, err := g.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		s, q := stateOf(t, a)
		f.nonce = q.Get("nonce")
		return s
	}

	s := begin()
	f.claims = map[string]any{"nonce": f.nonce, "email": "g@x.io", "email_verified": "true", "name": "Gee"}
	id, err := g.Finish(ctx, s, "code")
	if err != nil || id.Subject != "sub1" || id.Email != "g@x.io" || !id.EmailVerified || id.Name != "Gee" {
		t.Fatalf("%+v %v", id, err)
	}
	// provider cached
	if g.provider == nil {
		t.Error("provider not cached")
	}

	s = begin()
	f.claims = map[string]any{"nonce": "other", "email": "g@x.io"}
	if _, err := g.Finish(ctx, s, "code"); err == nil || !strings.Contains(err.Error(), "nonce") {
		t.Errorf("nonce: %v", err)
	}

	s = begin()
	f.noToken = true
	if _, err := g.Finish(ctx, s, "code"); err == nil || !strings.Contains(err.Error(), "no id_token") {
		t.Errorf("no token: %v", err)
	}
	f.noToken = false

	s = begin()
	f.badSig = true
	f.claims = map[string]any{"nonce": f.nonce}
	if _, err := g.Finish(ctx, s, "code"); err == nil || !strings.Contains(err.Error(), "verify") {
		t.Errorf("sig: %v", err)
	}
	f.badSig = false

	s = begin()
	f.tokenErr = true
	if _, err := g.Finish(ctx, s, "code"); err == nil || !strings.Contains(err.Error(), "exchange") {
		t.Errorf("exchange: %v", err)
	}
	f.tokenErr = false

	// claims decode failure: email is a number
	s = begin()
	f.claims = map[string]any{"nonce": f.nonce, "email": 5}
	if _, err := g.Finish(ctx, s, "code"); err == nil {
		t.Error("claims type error not surfaced")
	}

	if _, err := g.Finish(ctx, "unknown", "code"); err != ErrFlow {
		t.Errorf("unknown: %v", err)
	}

	// discovery failure in Begin and Finish
	f.discErr = true
	bad := NewGoogle(Config{ClientID: "cid", Issuer: f.srv.URL}, rdb)
	if _, err := bad.Begin(ctx); err == nil || !strings.Contains(err.Error(), "discovery") {
		t.Errorf("begin discovery: %v", err)
	}
	st, _, _ := bad.begin(ctx)
	if _, err := bad.Finish(ctx, st, "c"); err == nil || !strings.Contains(err.Error(), "discovery") {
		t.Errorf("finish discovery: %v", err)
	}
	f.discErr = false

	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := g.Begin(cctx); err == nil {
		t.Error("begin dead redis")
	}
}

func TestGoogleDefaultIssuer(t *testing.T) {
	g := NewGoogle(Config{}, nil)
	if g.cfg.Issuer != "https://accounts.google.com" {
		t.Error(g.cfg.Issuer)
	}
}
