package config

import (
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

type fakeVault struct {
	logins, reads, revokes atomic.Int32
	failRead               bool
}

func (f *fakeVault) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/approle/login":
			var b map[string]string
			_ = json.NewDecoder(r.Body).Decode(&b)
			if b["role_id"] != "role" || b["secret_id"] != "sid" {
				w.WriteHeader(400)
				_, _ = w.Write([]byte(`{"errors":["invalid"]}`))
				return
			}
			f.logins.Add(1)
			_, _ = w.Write([]byte(`{"auth":{"client_token":"tok"}}`))
		case "/v1/kv/data/app/prod":
			if r.Header.Get("X-Vault-Token") != "tok" {
				w.WriteHeader(403)
				return
			}
			f.reads.Add(1)
			if f.failRead {
				w.WriteHeader(500)
				_, _ = w.Write([]byte(`{"errors":["boom"]}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":{"data":{"MFA_KEY":"from-vault","PORT":"1111","N":5}}}`))
		case "/v1/auth/token/revoke-self":
			if r.Header.Get("X-Vault-Token") == "tok" {
				f.revokes.Add(1)
			}
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	})
}

func vaultEnv(t *testing.T, srv *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	ca := filepath.Join(dir, "ca.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(ca, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	sid := filepath.Join(dir, "sid")
	if err := os.WriteFile(sid, []byte("sid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VAULT_ADDR", srv.URL)
	t.Setenv("VAULT_ROLE_ID", "role")
	t.Setenv("VAULT_SECRET_ID_FILE", sid)
	t.Setenv("VAULT_KV_PATH", "kv/data/app/prod")
	t.Setenv("VAULT_CACERT", ca)
}

func TestVaultLookupOrder(t *testing.T) {
	f := &fakeVault{}
	srv := httptest.NewTLSServer(f.handler())
	defer srv.Close()
	vaultEnv(t, srv)
	t.Setenv("APP_ENV", "production")
	t.Setenv("PORT", "2222") // env beats vault
	keyFile := filepath.Join(t.TempDir(), "k")
	_ = os.WriteFile(keyFile, []byte("x"), 0o600)
	t.Setenv("OTHER_FILE", keyFile) // KEY_FILE beats vault

	l := newFileLookuper()
	if v, _ := l.Lookup("PORT"); v != "2222" {
		t.Fatalf("PORT=%q", v)
	}
	if f.logins.Load() != 0 {
		t.Fatal("vault hit despite env")
	}
	if v, _ := l.Lookup("OTHER"); v != "x" {
		t.Fatalf("OTHER=%q", v)
	}
	if f.logins.Load() != 0 {
		t.Fatal("vault hit despite file")
	}
	if v, ok := l.Lookup("MFA_KEY"); !ok || v != "from-vault" {
		t.Fatalf("MFA_KEY=%q %v", v, ok)
	}
	if v, _ := l.Lookup("N"); v != "5" {
		t.Fatalf("N=%q", v)
	}
	if _, ok := l.Lookup("MISSING"); ok {
		t.Fatal("missing key found")
	}
	if f.logins.Load() != 1 || f.reads.Load() != 1 || f.revokes.Load() != 1 {
		t.Fatalf("logins=%d reads=%d revokes=%d", f.logins.Load(), f.reads.Load(), f.revokes.Load())
	}
	if err := l.err(); err != nil {
		t.Fatal(err)
	}
}

func TestVaultRevokeOnReadFailure(t *testing.T) {
	f := &fakeVault{failRead: true}
	srv := httptest.NewTLSServer(f.handler())
	defer srv.Close()
	vaultEnv(t, srv)
	l := newFileLookuper()
	if _, ok := l.Lookup("MFA_KEY"); ok {
		t.Fatal("found")
	}
	if err := l.err(); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err=%v", err)
	}
	if f.revokes.Load() != 1 {
		t.Fatal("token not revoked")
	}
}

func TestVaultBadLogin(t *testing.T) {
	f := &fakeVault{}
	srv := httptest.NewTLSServer(f.handler())
	defer srv.Close()
	vaultEnv(t, srv)
	t.Setenv("VAULT_ROLE_ID", "wrong")
	l := newFileLookuper()
	l.Lookup("MFA_KEY")
	if err := l.err(); err == nil || !strings.Contains(err.Error(), "approle login") {
		t.Fatalf("err=%v", err)
	}
}

func TestVaultUntrustedCA(t *testing.T) {
	f := &fakeVault{}
	srv := httptest.NewTLSServer(f.handler())
	defer srv.Close()
	vaultEnv(t, srv)
	t.Setenv("VAULT_CACERT", "")
	l := newFileLookuper()
	l.Lookup("MFA_KEY")
	if l.err() == nil {
		t.Fatal("want TLS error without CA")
	}
}

func TestVaultHTTPSRequiredInProduction(t *testing.T) {
	f := &fakeVault{}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	vaultEnv(t, httptest.NewTLSServer(f.handler()))
	t.Setenv("VAULT_ADDR", srv.URL)
	t.Setenv("VAULT_CACERT", "")

	t.Setenv("APP_ENV", "production")
	l := newFileLookuper()
	l.Lookup("MFA_KEY")
	if err := l.err(); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("err=%v", err)
	}
	if f.logins.Load() != 0 {
		t.Fatal("contacted plain http vault in production")
	}

	t.Setenv("APP_ENV", "dev")
	l = newFileLookuper()
	if v, _ := l.Lookup("MFA_KEY"); v != "from-vault" {
		t.Fatalf("dev http: %q err=%v", v, l.err())
	}
}

func TestVaultDisabledWithoutConfig(t *testing.T) {
	t.Setenv("VAULT_ADDR", "https://x")
	t.Setenv("VAULT_ROLE_ID", "r")
	t.Setenv("VAULT_SECRET_ID_FILE", "")
	t.Setenv("VAULT_SECRET_ID", "")
	if l := newFileLookuper(); l.vault != nil {
		t.Fatal("vault enabled without secret id")
	}
}

func TestVaultLoadIntegration(t *testing.T) {
	f := &fakeVault{}
	srv := httptest.NewTLSServer(f.handler())
	defer srv.Close()
	vaultEnv(t, srv)
	t.Setenv("APP_ENV", "dev")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != "1111" {
		t.Fatalf("port=%q", c.Port)
	}
}
