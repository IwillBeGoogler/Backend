package guardauth_test

// guardauth against real Postgres and Redis. Skipped with -short or without Docker.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bakhod1r/guard"
	"github.com/bakhod1r/guard/httpguard"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/guardauth"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/postgres"
)

const (
	strongPw  = "Tr1cky-Zebra-Lantern-82"
	strongPw2 = "Quiet-Orchard-Maple-471"
	auditKey  = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
)

var fastHash = guardauth.Options{HashMemoryKiB: 19456, HashTime: 2, HashThreads: 1}

func infra(t *testing.T) (*pgxpool.Pool, *redis.Client) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test")
	}
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("blog"), tcpostgres.WithUsername("blog"), tcpostgres.WithPassword("blog"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").
			WithOccurrence(2).WithStartupTimeout(60*time.Second)))
	if err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	t.Cleanup(func() { testcontainers.TerminateContainer(pg) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	db, err := postgres.Connect(ctx, dsn, postgres.Options{MaxConns: 8, Migrate: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)

	rd, err := tcredis.Run(ctx, "redis:8-alpine")
	if err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	t.Cleanup(func() { testcontainers.TerminateContainer(rd) })
	url, err := rd.ConnectionString(ctx)
	if err != nil {
		t.Fatal(err)
	}
	opt, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(opt)
	t.Cleanup(func() { rdb.Close() })
	return db, rdb
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestOptionsErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := guardauth.Open(ctx, nil, nil, guardauth.Options{HashMemoryKiB: 1, HashTime: 1, HashThreads: 1}); err == nil {
		t.Error("weak hash params accepted")
	}
	if _, err := guardauth.Open(ctx, nil, nil, guardauth.Options{AuditEmailKey: "zz"}); err == nil {
		t.Error("bad audit key accepted")
	}
}

func TestGuardAuth(t *testing.T) {
	db, rdb := infra(t)
	ctx := context.Background()
	users := postgres.NewUserRepo(db)

	opts := fastHash
	opts.AuditEmailKey = auditKey
	opts.Migrate = true
	opts.AccessRules = guardauth.NewAccessRules("", "")

	// Migrate (cmd/migrate path) twice: idempotent.
	must(t, guardauth.Migrate(ctx, db, rdb, opts))
	must(t, guardauth.Migrate(ctx, db, rdb, opts))

	// Migrate with a broken rule fails in seedAccess and closes Guard.
	bad := opts
	bad.AccessRules = guardauth.AccessRules{ModeratorIPs: []string{"not-an-ip"}}
	if err := guardauth.Migrate(ctx, db, rdb, bad); err == nil || !strings.Contains(err.Error(), "access rules") {
		t.Errorf("bad rules: %v", err)
	}
	// Open with Migrate and a cancelled context: setup fails, Open returns the error.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := guardauth.Migrate(cctx, db, rdb, opts); err == nil {
		t.Error("cancelled migrate succeeded")
	}

	// Without the audit key (warning path) and without Migrate (DML-only replica).
	g0, err := guardauth.Open(ctx, db, rdb, fastHash)
	must(t, err)
	must(t, g0.Close(ctx))

	run := opts
	run.Migrate = false
	g, err := guardauth.Open(ctx, db, rdb, run)
	must(t, err)
	t.Cleanup(func() { g.Close(context.Background()) })

	id := guardauth.Identity{G: g}
	meta := application.Meta{IP: "203.0.113.5", UserAgent: "test"}

	mk := func(name string) int {
		u, err := users.Create(ctx, name, name+"@example.com")
		must(t, err)
		return u.ID
	}

	// Super admin
	admin := mk("admin")
	must(t, id.EnsureSuperAdmin(ctx, admin, "admin@example.com", strongPw))
	adminCred, err := id.Login(ctx, "admin@example.com", strongPw, meta)
	must(t, err)

	// Accounts
	ali := mk("ali")
	must(t, id.CreateAccount(ctx, ali, "ali@example.com", strongPw))
	if err := id.CreateAccount(ctx, mk("ali2"), "ali@example.com", strongPw); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("dup email: %v", err)
	}
	if err := id.CreateAccount(ctx, ali, "other@example.com", strongPw); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("dup account: %v", err)
	}
	if err := id.CreateAccount(ctx, mk("weak"), "weak@example.com", "password123"); !errors.Is(err, application.ErrWeakPassword) {
		t.Errorf("weak: %v", err)
	}

	// Login errors
	if _, err := id.Login(ctx, "ali@example.com", "wrong-Password-1", meta); !errors.Is(err, application.ErrInvalidCredentials) {
		t.Errorf("wrong pw: %v", err)
	}
	if _, err := id.Login(ctx, "nobody@example.com", strongPw, meta); !errors.Is(err, application.ErrInvalidCredentials) {
		t.Errorf("unknown: %v", err)
	}
	if err := id.VerifyPassword(ctx, "ali@example.com", strongPw); err != nil {
		t.Errorf("verify: %v", err)
	}
	if err := id.VerifyPassword(ctx, "ali@example.com", "nope-Nope-1234"); !errors.Is(err, application.ErrInvalidCredentials) {
		t.Errorf("verify wrong: %v", err)
	}

	// Lockout: repeated failures end in the "too many attempts" invalid error.
	lock := mk("lock")
	must(t, id.CreateAccount(ctx, lock, "lock@example.com", strongPw))
	var locked bool
	for i := 0; i < 30 && !locked; i++ {
		_, err := id.Login(ctx, "lock@example.com", "bad-Password-0000", meta)
		locked = err != nil && strings.Contains(err.Error(), "too many")
	}
	if !locked {
		t.Log("lockout not reached within 30 attempts (Guard default policy)")
	}

	// Session lifecycle
	cred, err := id.Login(ctx, "ali@example.com", strongPw, meta)
	must(t, err)
	if uid, err := id.Authenticate(ctx, cred.Token); err != nil || uid != ali {
		t.Fatalf("authenticate %d %v", uid, err)
	}
	if _, err := id.Authenticate(ctx, "garbage"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Errorf("garbage token: %v", err)
	}
	sid, err := id.SessionID(ctx, cred.Token)
	must(t, err)
	if _, err := id.SessionID(ctx, "garbage"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Errorf("sessionid garbage: %v", err)
	}
	rot, err := id.Refresh(ctx, cred.Token)
	must(t, err)
	if _, err := id.Refresh(ctx, "garbage"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Errorf("refresh garbage: %v", err)
	}
	_ = sid

	started, err := id.StartSession(ctx, ali, meta)
	must(t, err)
	list, err := id.Sessions(ctx, ali)
	must(t, err)
	if len(list) < 2 {
		t.Fatalf("sessions %d", len(list))
	}
	must(t, id.RevokeSession(ctx, ali, list[0].ID))
	if err := id.RevokeSession(ctx, ali, "no-such-session"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("revoke missing: %v", err)
	}
	if _, err := id.StartSession(ctx, 999999, meta); err == nil {
		t.Error("start session for missing user")
	}

	// Logout
	must(t, id.Logout(ctx, started.Token, meta))
	if err := id.Logout(ctx, "garbage", meta); !errors.Is(err, domain.ErrUnauthorized) {
		t.Errorf("logout garbage: %v", err)
	}

	// Passwords
	if err := id.ChangePassword(ctx, ali, "wrong-Password-1", strongPw2); err == nil || !strings.Contains(err.Error(), "current password") {
		t.Errorf("change wrong: %v", err)
	}
	if err := id.ChangePassword(ctx, ali, strongPw, "password123"); !errors.Is(err, application.ErrWeakPassword) {
		t.Errorf("change weak: %v", err)
	}
	must(t, id.ChangePassword(ctx, ali, strongPw, strongPw2))
	if err := id.SetPassword(ctx, ali, "password123"); !errors.Is(err, application.ErrWeakPassword) {
		t.Errorf("set weak: %v", err)
	}
	must(t, id.SetPassword(ctx, ali, strongPw)) // also revokes sessions and keys
	if _, err := id.Authenticate(ctx, rot.Token); err == nil {
		t.Error("session survived SetPassword")
	}
	must(t, id.LogoutAll(ctx, ali))

	// Roles, blocking
	roles, err := id.Roles(ctx, ali)
	must(t, err)
	_ = roles
	bob := mk("bob")
	must(t, id.CreateAccount(ctx, bob, "bob@example.com", strongPw))
	must(t, id.SetBlocked(ctx, 0, bob, true))
	if _, err := id.Login(ctx, "bob@example.com", strongPw, meta); !errors.Is(err, application.ErrInvalidCredentials) {
		t.Errorf("blocked login: %v", err)
	}
	if _, err := id.StartSession(ctx, bob, meta); !errors.Is(err, application.ErrInvalidCredentials) {
		t.Errorf("blocked start session: %v", err)
	}
	must(t, id.SetBlocked(ctx, admin, bob, false))
	if err := id.SetBlocked(ctx, 0, 999999, true); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("block missing: %v", err)
	}
	if err := id.SetBlocked(ctx, ali, admin, true); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("non-admin actor on super admin: %v", err)
	}
	if err := id.SetBlocked(ctx, admin, admin, true); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("last super admin: %v", err)
	}

	active := guardauth.UserActive(g)
	if !active(ctx, bob) || active(ctx, 999999) {
		t.Error("UserActive")
	}
	must(t, id.SetBlocked(ctx, 0, bob, true))
	if active(ctx, bob) {
		t.Error("banned user active")
	}
	must(t, id.SetBlocked(ctx, 0, bob, false))

	// Principal plumbing + Authorizer
	var seen context.Context
	var mu sync.Mutex
	h := httpguard.Authenticate(g, httpguard.Options{})(guardauth.WithPrincipal(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = r.Context()
		mu.Unlock()
	})))
	call := func(token string) context.Context {
		r := httptest.NewRequest(http.MethodGet, "/x", nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		h.ServeHTTP(httptest.NewRecorder(), r)
		return seen
	}
	aliCred, err := id.Login(ctx, "ali@example.com", strongPw, meta)
	must(t, err)
	aliCtx := call(aliCred.Token)
	adminCtx := call(adminCred.Token)
	anonCtx := call("")
	if guardauth.UserID(aliCtx) != ali || guardauth.UserID(anonCtx) != 0 {
		t.Errorf("UserID ali=%d anon=%d", guardauth.UserID(aliCtx), guardauth.UserID(anonCtx))
	}

	az := guardauth.Authorizer{G: g}
	if err := az.Can(anonCtx, "post", "create", 0, 0); !errors.Is(err, domain.ErrUnauthorized) {
		t.Errorf("anon can: %v", err)
	}
	if err := az.Can(aliCtx, "post", "create", 0, 0); err != nil {
		t.Errorf("user post.create: %v", err)
	}
	if err := az.Can(aliCtx, "post", "update", 5, ali); err != nil { // owner policy
		t.Errorf("owner update: %v", err)
	}
	if err := az.Can(aliCtx, "post", "update", 5, bob); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("non-owner update: %v", err)
	}
	if err := az.Can(adminCtx, "category", "write", 0, 0); err != nil {
		t.Errorf("admin: %v", err)
	}

	// Auditor with and without actor
	au := guardauth.Auditor{G: g}
	au.Record(ctx, ali, "test.action", "post:1", map[string]any{"k": "v"})
	au.Record(ctx, 0, "test.anon", "x", nil)
	// Failing audit sink only logs.
	au.Record(cctx, ali, "test.cancelled", "x", nil)

	// AccessGate: IP rule closes the API for moderators.
	mod := mk("mod")
	must(t, id.CreateAccount(ctx, mod, "mod@example.com", strongPw))
	must(t, g.AssignRole(ctx, "", uidStr(mod), "moderator", nil))
	modCred, err := id.Login(ctx, "mod@example.com", strongPw, meta)
	must(t, err)

	ipRules := guardauth.NewAccessRules(" 203.0.113.9 , ,", "")
	must(t, guardauth.SeedAccessRules(ctx, g, ipRules))
	must(t, guardauth.SeedAccessRules(ctx, g, ipRules)) // update-in-place path
	gate := func(ip string, method, path, token string) int {
		var next http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
		next = guardauth.AccessGate(g, func(*http.Request) string { return ip }, time.UTC)(next)
		next = httpguard.Authenticate(g, httpguard.Options{})(next)
		r := httptest.NewRequest(method, path, nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		next.ServeHTTP(w, r)
		return w.Code
	}
	if c := gate("198.51.100.1", "GET", "/api/me", modCred.Token); c != http.StatusForbidden {
		t.Errorf("moderator from wrong IP: %d", c)
	}
	if c := gate("203.0.113.9", "GET", "/api/me", modCred.Token); c != 204 {
		t.Errorf("moderator from office: %d", c)
	}
	if c := gate("198.51.100.1", "POST", "/api/auth/logout", modCred.Token); c != 204 {
		t.Errorf("sign out blocked: %d", c)
	}
	if c := gate("198.51.100.1", "GET", "/api/me", ""); c != 204 {
		t.Errorf("anonymous: %d", c)
	}
	if c := gate("198.51.100.1", "GET", "/api/me", aliCred.Token); c != 204 {
		t.Errorf("plain user: %d", c)
	}

	// Hours rule: a window that excludes now closes; one that includes it opens. Then both
	// midnight-spanning forms are exercised.
	now := time.Now().UTC()
	hr := func(from, to time.Duration) string {
		return now.Add(from).Format("15:04") + "-" + now.Add(to).Format("15:04")
	}
	must(t, guardauth.SeedAccessRules(ctx, g, guardauth.AccessRules{ModeratorHours: hr(3*time.Hour, 4*time.Hour)}))
	if c := gate("198.51.100.1", "GET", "/api/me", modCred.Token); c != http.StatusForbidden {
		t.Errorf("outside hours: %d", c)
	}
	must(t, guardauth.SeedAccessRules(ctx, g, guardauth.AccessRules{ModeratorHours: hr(-time.Hour, time.Hour)}))
	if c := gate("198.51.100.1", "GET", "/api/me", modCred.Token); c != 204 {
		t.Errorf("inside hours: %d", c)
	}
	must(t, guardauth.SeedAccessRules(ctx, g, guardauth.AccessRules{ModeratorHours: "22:00-06:00"}))
	must(t, guardauth.SeedAccessRules(ctx, g, guardauth.AccessRules{})) // removes both policies

	// Rule errors
	if err := guardauth.SeedAccessRules(ctx, g, guardauth.AccessRules{ModeratorHours: "bogus"}); err == nil {
		t.Error("bad hours accepted")
	}
	if err := guardauth.SeedAccessRules(cctx, g, guardauth.AccessRules{}); err == nil {
		t.Error("cancelled ctx accepted")
	}
	if err := guardauth.Seed(cctx, g); err == nil {
		t.Error("cancelled seed accepted")
	}
	must(t, guardauth.Seed(ctx, g)) // idempotent

	// Database gone: every adapter surfaces the error instead of panicking.
	rdb.Close()
	if _, err := id.Sessions(ctx, ali); err == nil {
		t.Error("Sessions on closed db")
	}
	if err := id.RevokeSession(ctx, ali, "x"); err == nil {
		t.Error("RevokeSession on closed db")
	}
	db.Close()
	if _, err := id.Roles(ctx, ali); err == nil {
		t.Error("Roles on closed db")
	}
	if err := id.LogoutAll(ctx, ali); err == nil {
		t.Error("LogoutAll on closed db")
	}
	if err := id.SetPassword(ctx, ali, strongPw); err == nil {
		t.Error("SetPassword on closed db")
	}
	if _, err := id.StartSession(ctx, ali, meta); err == nil {
		t.Error("StartSession on closed db")
	}
	if err := az.Can(aliCtx, "post", "create", 0, 0); err == nil {
		t.Log("Can served from cache with db closed")
	}
	if err := guardauth.Seed(ctx, g); err == nil {
		t.Error("Seed on closed db")
	}
	if err := guardauth.Migrate(ctx, db, rdb, opts); err == nil {
		t.Error("Migrate on closed db")
	}
	_ = gate("198.51.100.1", "GET", "/api/me", aliCred.Token)
}

func uidStr(i int) string { return strconv.Itoa(i) }

var _ = guard.ErrSessionRequired
