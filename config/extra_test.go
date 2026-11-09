package config

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type mapEnv map[string]string

func (m mapEnv) Lookup(k string) (string, bool) { v, ok := m[k]; return v, ok }

func TestFileLookuperRaw(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s")
	os.WriteFile(p, []byte("$HOME-literal\n"), 0o600)
	l := &fileLookuper{next: mapEnv{"SECRET_FILE": p}}
	if v, ok := l.LookupRaw("SECRET"); !ok || v != "$HOME-literal" {
		t.Errorf("raw = %q %v", v, ok)
	}
}

func TestVaultSourceConstruction(t *testing.T) {
	if newVaultSource(mapEnv{}) != nil {
		t.Error("unconfigured vault must be nil")
	}
	v := newVaultSource(mapEnv{"VAULT_ADDR": "http://v:8200/", "VAULT_ROLE_ID": "r", "VAULT_SECRET_ID": "s", "VAULT_APPROLE_MOUNT": "/custom/", "APP_ENV": "dev"})
	if v == nil || v.mount != "custom" || v.addr != "http://v:8200" || v.production {
		t.Errorf("vault = %+v", v)
	}
	bad := newVaultSource(mapEnv{"VAULT_ADDR": "http://v", "VAULT_ROLE_ID": "r", "VAULT_SECRET_ID_FILE": "/no/such/file"})
	if bad == nil || bad.err == nil || !strings.Contains(bad.err.Error(), "VAULT_SECRET_ID_FILE") {
		t.Errorf("unreadable secret file: %+v", bad)
	}
	if _, ok := bad.lookup("X"); ok {
		t.Error("lookup after failure")
	}
}

func TestVaultFetchFailures(t *testing.T) {
	mk := func(env map[string]string) *vaultSource {
		env["VAULT_ROLE_ID"], env["VAULT_SECRET_ID"] = "r", "s"
		return newVaultSource(mapEnv(env))
	}
	cases := map[string]struct {
		env  map[string]string
		want string
	}{
		"no path":        {map[string]string{"VAULT_ADDR": "http://v", "APP_ENV": "dev"}, "VAULT_KV_PATH"},
		"bad addr":       {map[string]string{"VAULT_ADDR": "not a url", "VAULT_KV_PATH": "kv/x", "APP_ENV": "dev"}, "valid URL"},
		"http in prod":   {map[string]string{"VAULT_ADDR": "http://v", "VAULT_KV_PATH": "kv/x", "APP_ENV": "production"}, "https"},
		"ftp in dev":     {map[string]string{"VAULT_ADDR": "ftp://v", "VAULT_KV_PATH": "kv/x", "APP_ENV": "dev"}, "http or https"},
		"missing cacert": {map[string]string{"VAULT_ADDR": "https://v", "VAULT_KV_PATH": "kv/x", "VAULT_CACERT": "/no/ca", "APP_ENV": "dev"}, "VAULT_CACERT"},
	}
	for name, c := range cases {
		if _, err := mk(c.env).fetch(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	junk := filepath.Join(t.TempDir(), "junk.pem")
	os.WriteFile(junk, []byte("not a cert"), 0o600)
	if _, err := mk(map[string]string{"VAULT_ADDR": "https://v", "VAULT_KV_PATH": "kv/x", "VAULT_CACERT": junk, "APP_ENV": "dev"}).fetch(); err == nil || !strings.Contains(err.Error(), "no certificates") {
		t.Errorf("junk ca: %v", err)
	}
}

func TestVaultFetchServerBehaviours(t *testing.T) {
	run := func(handler http.HandlerFunc) error {
		srv := httptest.NewServer(handler)
		defer srv.Close()
		v := newVaultSource(mapEnv{"VAULT_ADDR": srv.URL, "VAULT_ROLE_ID": "r", "VAULT_SECRET_ID": "s", "VAULT_KV_PATH": "kv/x", "APP_ENV": "dev", "VAULT_NAMESPACE": "ns"})
		_, err := v.fetch()
		return err
	}
	if err := run(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"auth":{"client_token":""}}`)) }); err == nil || !strings.Contains(err.Error(), "empty token") {
		t.Errorf("empty token: %v", err)
	}
	if err := run(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "revoke-self") {
			w.WriteHeader(500)
			return
		}
		if strings.Contains(r.URL.Path, "login") {
			w.Write([]byte(`{"auth":{"client_token":"t"}}`))
			return
		}
		w.Write([]byte(`{"data":{"data":{"A":"b","N":null,"I":5}}}`))
	}); err == nil || !strings.Contains(err.Error(), "revoke") {
		t.Errorf("revoke failure: %v", err)
	}
	if err := run(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }); err == nil || !strings.Contains(err.Error(), "login") {
		t.Errorf("login refused: %v", err)
	}
}

func TestValidateBranches(t *testing.T) {
	good := func() *Config {
		return &Config{Env: "dev", RateLimitMultiplier: 1}
	}
	c := good()
	c.Env = "staging"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "APP_ENV") {
		t.Errorf("env: %v", err)
	}
	c = good()
	c.TurnstileSiteKey = "x"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "TURNSTILE") {
		t.Errorf("turnstile: %v", err)
	}
	c = good()
	c.RateLimitMultiplier = -1
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "RATE_LIMIT_MULTIPLIER") {
		t.Errorf("multiplier: %v", err)
	}
	c = good()
	c.RateLimitMultiplier = 0
	if err := c.Validate(); err != nil || c.RateLimitMultiplier != 1 {
		t.Errorf("multiplier default: %v %d", err, c.RateLimitMultiplier)
	}
	// production collects every weakness
	p := &Config{Env: "production", RateLimitMultiplier: 3, AdminPassword: "short", S3SecretKey: "minioadmin", S3AccessKey: "minioadmin",
		WebhookAllowPrivate: true, SchemaAllowPrivate: true, ImportAllowPrivate: true, AuditEmailKey: "zz", MetricsToken: "x", UnsubscribeKey: "y"}
	err := p.Validate()
	if err == nil {
		t.Fatal("weak production config accepted")
	}
	for _, want := range []string{"MFA_KEY", "METRICS_TOKEN", "UNSUBSCRIBE_KEY", "ADMIN_PASSWORD", "RATE_LIMIT_MULTIPLIER must be 1", "AUDIT_EMAIL_KEY", "WEBHOOK_ALLOW_PRIVATE"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
}
