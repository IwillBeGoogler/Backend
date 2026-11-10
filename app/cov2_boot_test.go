package app_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/app"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/config"
)

// TestBootOptions builds the app with the optional features switched on and
// no worker, then checks the pieces they mount.
func TestBootOptions(t *testing.T) {
	e := start(t, func(c *config.Config) {
		c.Worker = false
		c.TurnstileSecret = "secret"
		c.TurnstileSiteKey = "site"
		c.SchemaIntrospect = true
		c.SchemaAllowPrivate = true
		c.WebhookAllowPrivate = true
		c.GoogleClientID, c.GoogleClientSecret = "gid", "gsecret"
		c.GitHubClientID, c.GitHubClientSecret = "hid", "hsecret"
		c.MetricsToken = "0123456789abcdef0123456789abcdef"
		c.DocsKey = "dk"
		c.MQTTURL = ""
	})
	if err := e.app.Start(); err != nil {
		t.Fatalf("Start without worker: %v", err)
	}
	get := func(path, auth string) int {
		req, _ := http.NewRequest(http.MethodGet, e.srv.URL+path, nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		must(t, err)
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode
	}
	if got := get("/metrics", ""); got != http.StatusNotFound {
		t.Fatalf("metrics without token = %d", got)
	}
	if got := get("/metrics", "Bearer 0123456789abcdef0123456789abcdef"); got != http.StatusOK {
		t.Fatalf("metrics with token = %d", got)
	}
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noRedirect.Get(e.srv.URL + "/api/docs?key=dk")
	must(t, err)
	resp.Body.Close()
	if resp.StatusCode != http.StatusMovedPermanently || !strings.HasSuffix(resp.Header.Get("Location"), "/api/docs/?key=dk") {
		t.Fatalf("docs redirect = %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if e.app.Metrics == nil {
		t.Fatal("no metrics handler")
	}
	rec := httptest.NewRecorder()
	e.app.Metrics.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics handler = %d", rec.Code)
	}
}

// TestNewErrors drives the config failures New reports after core is built.
func TestNewErrors(t *testing.T) {
	var cfg config.Config
	got := false
	start(t, func(c *config.Config) { cfg = *c; got = true })
	if !got {
		t.Skip("no config")
	}
	for name, mod := range map[string]func(*config.Config){
		"proxies":  func(c *config.Config) { c.TrustedProxies = "10.0.0.1" },
		"timezone": func(c *config.Config) { c.AccessTimezone = "Not/AZone" },
		"ice": func(c *config.Config) {
			c.MQTTURL = "tcp://127.0.0.1:1"
			c.ICEServers = "{bad"
		},
		"mqtt-lazy": func(c *config.Config) { // the broker connects in the background: New succeeds
			c.MQTTURL = "tcp://127.0.0.1:1"
			c.MQTTUsername, c.MQTTPassword = "api", "pw"
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := cfg
			mod(&c)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			a, err := app.New(ctx, c)
			if name == "mqtt-lazy" {
				must(t, err)
				a.Close()
				return
			}
			if err == nil {
				a.Close()
				t.Fatal("New succeeded")
			}
		})
	}
}
