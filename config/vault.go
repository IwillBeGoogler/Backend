package config

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/bakhod1r/oneenv"
)

// vaultTimeout bounds the whole login + read + revoke sequence.
const vaultTimeout = 15 * time.Second

// vaultSource reads secrets from one HashiCorp Vault KV v2 path. It is
// optional: enabled only when VAULT_ADDR, VAULT_ROLE_ID and a secret id
// (VAULT_SECRET_ID_FILE or VAULT_SECRET_ID) are set. One AppRole login and one
// KV read happen lazily on the first lookup that env and KEY_FILE cannot
// answer; the token is revoked right after the read.
//
//	VAULT_ADDR           https://vault.example:8200 (HTTPS required when APP_ENV=production)
//	VAULT_ROLE_ID        AppRole role id
//	VAULT_SECRET_ID_FILE file holding the AppRole secret id (preferred)
//	VAULT_SECRET_ID      secret id literal
//	VAULT_KV_PATH        KV v2 data path, e.g. kv/data/iwillbegoogler/production
//	VAULT_NAMESPACE      optional Enterprise namespace
//	VAULT_CACERT         optional PEM CA bundle file
//	VAULT_APPROLE_MOUNT  AppRole auth mount, default approle
type vaultSource struct {
	addr, roleID, secretID, path, namespace, caCert, mount string
	production                                             bool

	loaded bool
	data   map[string]string
	err    error
}

// newVaultSource returns nil when Vault is not configured.
func newVaultSource(env oneenv.Lookuper) *vaultSource {
	get := func(k string) string { v, _ := env.Lookup(k); return strings.TrimSpace(v) }
	addr, role := get("VAULT_ADDR"), get("VAULT_ROLE_ID")
	secretFile, secret := get("VAULT_SECRET_ID_FILE"), get("VAULT_SECRET_ID")
	if addr == "" || role == "" || (secretFile == "" && secret == "") {
		return nil
	}
	v := &vaultSource{
		addr: strings.TrimRight(addr, "/"), roleID: role, secretID: secret,
		path: strings.Trim(get("VAULT_KV_PATH"), "/"), namespace: get("VAULT_NAMESPACE"),
		caCert: get("VAULT_CACERT"), mount: strings.Trim(get("VAULT_APPROLE_MOUNT"), "/"),
		production: get("APP_ENV") == "production" || get("APP_ENV") == "",
	}
	if v.mount == "" {
		v.mount = "approle"
	}
	if secretFile != "" {
		b, err := os.ReadFile(secretFile)
		if err != nil {
			v.loaded, v.err = true, fmt.Errorf("config: vault: VAULT_SECRET_ID_FILE: %w", err)
			return v
		}
		v.secretID = strings.TrimSpace(string(b))
	}
	return v
}

// lookup returns a key from the KV secret, loading it on first use.
func (v *vaultSource) lookup(key string) (string, bool) {
	if !v.loaded {
		v.loaded = true
		v.data, v.err = v.fetch()
	}
	val, ok := v.data[key]
	return val, ok
}

func (v *vaultSource) fetch() (map[string]string, error) {
	fail := func(err error) (map[string]string, error) { return nil, fmt.Errorf("config: vault: %w", err) }
	if v.path == "" {
		return fail(errors.New("VAULT_KV_PATH is not set"))
	}
	u, err := url.Parse(v.addr)
	if err != nil || u.Host == "" {
		return fail(errors.New("VAULT_ADDR is not a valid URL"))
	}
	if u.Scheme != "https" && (v.production || u.Scheme != "http") {
		if v.production {
			return fail(errors.New("VAULT_ADDR must be https when APP_ENV=production"))
		}
		return fail(errors.New("VAULT_ADDR must be http or https"))
	}
	client, err := v.client()
	if err != nil {
		return fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), vaultTimeout)
	defer cancel()

	var login struct {
		Auth struct {
			ClientToken string `json:"client_token"`
		} `json:"auth"`
	}
	body := map[string]string{"role_id": v.roleID, "secret_id": v.secretID}
	if err := v.call(ctx, client, http.MethodPost, "auth/"+v.mount+"/login", "", body, &login); err != nil {
		return fail(fmt.Errorf("approle login: %w", err))
	}
	token := login.Auth.ClientToken
	if token == "" {
		return fail(errors.New("approle login: empty token"))
	}
	var kv struct {
		Data struct {
			Data map[string]any `json:"data"`
		} `json:"data"`
	}
	readErr := v.call(ctx, client, http.MethodGet, v.path, token, nil, &kv)
	// Revoke right away, even when the read failed.
	revokeErr := v.call(ctx, client, http.MethodPost, "auth/token/revoke-self", token, nil, nil)
	if readErr != nil {
		return fail(fmt.Errorf("read %s: %w", v.path, readErr))
	}
	if revokeErr != nil {
		return fail(fmt.Errorf("revoke token: %w", revokeErr))
	}
	out := make(map[string]string, len(kv.Data.Data))
	for k, val := range kv.Data.Data {
		switch s := val.(type) {
		case string:
			out[k] = s
		case nil:
		default:
			out[k] = fmt.Sprint(s)
		}
	}
	return out, nil
}

func (v *vaultSource) client() (*http.Client, error) {
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment}
	if v.caCert != "" {
		pem, err := os.ReadFile(v.caCert)
		if err != nil {
			return nil, fmt.Errorf("VAULT_CACERT: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("VAULT_CACERT: no certificates found")
		}
		tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	return &http.Client{Transport: tr, Timeout: vaultTimeout}, nil
}

// call performs one Vault API request. Error text never includes secrets:
// only the HTTP status and Vault's own error strings.
func (v *vaultSource) call(ctx context.Context, c *http.Client, method, path, token string, in, out any) error {
	var rd io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, v.addr+"/v1/"+path, rd)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("X-Vault-Token", token)
	}
	if v.namespace != "" {
		req.Header.Set("X-Vault-Namespace", v.namespace)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode/100 != 2 {
		var e struct {
			Errors []string `json:"errors"`
		}
		_ = json.Unmarshal(data, &e)
		return fmt.Errorf("status %d: %s", resp.StatusCode, strings.Join(e.Errors, "; "))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}
