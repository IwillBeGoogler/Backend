package config

import (
	"strings"
	"testing"
	"time"
)

func prodConfig() Config {
	return Config{
		Env:         "production",
		DatabaseURL: "postgres://u:s3cret-long-password@db/blog",
		S3SecretKey: "a-real-s3-secret-value",
		MFAKey:      strings.Repeat("m", 32),
		S3AccessKey: "AKIAreal0example", AuditEmailKey: strings.Repeat("ab", 32),

		MQTTURL: "tcp://emqx:1883", MQTTPassword: strings.Repeat("p", 32), MQTTInternalSecret: strings.Repeat("s", 32),
	}
}

func TestValidateMQTTSecrets(t *testing.T) {
	for _, name := range []string{"MQTT_PASSWORD", "MQTT_INTERNAL_SECRET"} {
		c := prodConfig()
		if name == "MQTT_PASSWORD" {
			c.MQTTPassword = DevMQTTPassword
		} else {
			c.MQTTInternalSecret = "short"
		}
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("weak %s: err = %v", name, err)
		}
	}
	c := prodConfig()
	c.MQTTURL, c.MQTTPassword, c.MQTTInternalSecret = "", "", ""
	if err := c.Validate(); err != nil {
		t.Errorf("realtime off needs no MQTT secrets: %v", err)
	}
	d := Config{Env: "dev"}
	if err := d.Validate(); err != nil || d.MQTTPassword != DevMQTTPassword || d.MQTTInternalSecret != DevMQTTSecret {
		t.Errorf("dev defaults: %+v, %v", d, err)
	}
}

func TestValidateProductionRequiresMFAKey(t *testing.T) {
	c := prodConfig()
	c.MFAKey = ""
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "MFA_KEY") {
		t.Fatalf("missing MFA_KEY: err = %v", err)
	}
	c.MFAKey = DevMFAKey
	if err := c.Validate(); err == nil {
		t.Fatal("dev MFA_KEY accepted in production")
	}
}

func TestValidateUnsubscribeKeyOptionalButStrong(t *testing.T) {
	c := prodConfig()
	if err := c.Validate(); err != nil {
		t.Fatalf("empty UNSUBSCRIBE_KEY (legacy fallback): %v", err)
	}
	c.UnsubscribeKey = "short"
	if err := c.Validate(); err == nil {
		t.Fatal("weak UNSUBSCRIBE_KEY accepted")
	}
}

func TestValidateMetricsToken(t *testing.T) {
	c := prodConfig()
	c.MetricsToken = "short"
	if err := c.Validate(); err == nil {
		t.Fatal("weak METRICS_TOKEN accepted")
	}
}

func TestValidateDevFillsKeys(t *testing.T) {
	c := Config{Env: "dev"}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.MFAKey != DevMFAKey {
		t.Fatalf("MFAKey = %q", c.MFAKey)
	}
}

func TestAudioPeersClamp(t *testing.T) {
	for _, c := range []struct{ rtc, audio, want int }{{6, 0, 6}, {0, 0, 2}, {1, 0, 2}, {20, 0, 6}, {6, 3, 3}, {6, 40, 6}, {4, 1, 2}} {
		if got := (Config{RTCMaxPeers: c.rtc, AudioMaxPeers: c.audio}).AudioPeers(); got != c.want {
			t.Errorf("RTC_MAX_PEERS=%d AUDIO_MAX_PARTICIPANTS=%d: %d, want %d", c.rtc, c.audio, got, c.want)
		}
	}
}

func TestICEServers(t *testing.T) {
	got, err := Config{}.ICE()
	if err != nil || len(got) != 1 || got[0].URLs[0] != "stun:stun.l.google.com:19302" {
		t.Fatalf("default = %+v, %v", got, err)
	}
	got, err = Config{ICEServers: `[{"urls":"stun:a:3478"},{"urls":["turn:b:3478","turns:b:5349"],"username":"u","credential":"p"}]`}.ICE()
	if err != nil || len(got) != 2 || got[0].URLs[0] != "stun:a:3478" || len(got[1].URLs) != 2 || got[1].Username != "u" || got[1].Credential != "p" {
		t.Fatalf("parsed = %+v, %v", got, err)
	}
	for _, bad := range []string{`nope`, `{"urls":"stun:a"}`, `[{"urls":"http://a"}]`, `[{"urls":[]}]`, `[{"urls":5}]`, `[{}]`} {
		if _, err := (Config{ICEServers: bad}).ICE(); err == nil {
			t.Errorf("ICE_SERVERS=%s accepted", bad)
		}
	}
	c := Config{Env: "dev", ICEServers: "nope"}
	if err := c.Validate(); err == nil {
		t.Error("Validate accepted a bad ICE_SERVERS")
	}
}

func TestValidateProductionRefusesDevDefaults(t *testing.T) {
	cases := map[string]func(*Config){
		"AUDIT_EMAIL_KEY":       func(c *Config) { c.AuditEmailKey = "" },
		"AUDIT_EMAIL_KEY ":      func(c *Config) { c.AuditEmailKey = "not-hex-and-long-enough-0123456789abcdef" },
		"S3_ACCESS_KEY":         func(c *Config) { c.S3AccessKey = "minio" },
		"WEBHOOK_ALLOW_PRIVATE": func(c *Config) { c.WebhookAllowPrivate = true },
		"SCHEMA_ALLOW_PRIVATE":  func(c *Config) { c.SchemaAllowPrivate = true },
		"IMPORT_ALLOW_PRIVATE":  func(c *Config) { c.ImportAllowPrivate = true },
		"MQTT_PASSWORD":         func(c *Config) { c.MQTTPassword = DevMQTTPassword },
		"MFA_KEY":               func(c *Config) { c.MFAKey = DevMFAKey },
	}
	for name, mut := range cases {
		c := prodConfig()
		mut(&c)
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), strings.TrimSpace(name)) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestSocialLoginNeedsIDAndSecret(t *testing.T) {
	c := Config{GoogleClientID: "id", GitHubClientID: "id", GitHubClientSecret: "s"}
	if c.GoogleEnabled() || !c.GitHubEnabled() {
		t.Fatalf("google=%v github=%v", c.GoogleEnabled(), c.GitHubEnabled())
	}
}

func TestValidateSessionTimeouts(t *testing.T) {
	c := prodConfig()
	c.SessionIdleTimeout = time.Second
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "SESSION_IDLE_TIMEOUT") {
		t.Errorf("short idle timeout: err = %v", err)
	}
	c = prodConfig()
	c.SessionIdleTimeout, c.SessionAbsoluteTimeout = time.Hour, time.Minute
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "SESSION_ABSOLUTE_TIMEOUT") {
		t.Errorf("absolute shorter than idle: err = %v", err)
	}
	c = prodConfig()
	c.SessionIdleTimeout, c.SessionAbsoluteTimeout = 24*time.Hour, 30*24*time.Hour
	if err := c.Validate(); err != nil {
		t.Errorf("valid timeouts: %v", err)
	}
}

func TestValidateProductionURLs(t *testing.T) {
	for name, set := range map[string]func(*Config){
		"SITE_URL":   func(c *Config) { c.SiteURL = "http://localhost:8081" },
		"PUBLIC_URL": func(c *Config) { c.PublicURL = "http://127.0.0.1:8080" },
	} {
		c := prodConfig()
		set(&c)
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("local %s in production: err = %v", name, err)
		}
	}
	c := prodConfig()
	c.SiteURL, c.PublicURL = "https://blog.example", "https://blog.example"
	if err := c.Validate(); err != nil {
		t.Errorf("public URLs: %v", err)
	}
	d := Config{Env: "dev", SiteURL: "http://localhost:8081", PublicURL: "http://localhost:8080"}
	if err := d.Validate(); err != nil {
		t.Errorf("dev keeps local URLs: %v", err)
	}
}
