package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTelegramSettings(t *testing.T) {
	for _, tc := range []struct {
		mode, token, secret string
		ok, on              bool
	}{
		{"", "", "", true, false},
		{"off", "t", "", true, false},
		{"poll", "", "", true, false},
		{"poll", "t", "", true, true},
		{"webhook", "", "", true, false},
		{"webhook", "t", "", false, false},
		{"webhook", "t", "has space", false, false},
		{"webhook", "t", "abc_-123", true, true},
		{"push", "t", "", false, false},
	} {
		c := Config{Env: "dev", TelegramMode: tc.mode, TelegramBotToken: tc.token, TelegramWebhookSecret: tc.secret, TelegramBotUsername: "testbot"}
		if err := c.Validate(); (err == nil) != tc.ok || (tc.ok && c.TelegramEnabled() != tc.on) {
			t.Errorf("%+v: %v", tc, err)
		}
	}
}

func TestTelegramSecretsFromFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	t.Setenv("APP_ENV", "dev")
	t.Setenv("TELEGRAM_MAIN_MODE", "poll")
	for _, k := range []string{"TELEGRAM_MAIN_BOT_TOKEN", "TELEGRAM_MAIN_WEBHOOK_SECRET"} {
		t.Setenv(k, "") // restored after the test
		if err := os.Unsetenv(k); err != nil {
			t.Fatal(err)
		}
		t.Setenv(k+"_FILE", path)
	}
	c, err := Load()
	if err != nil || c.TelegramBotToken != "from-file" || !strings.HasPrefix(c.TelegramWebhookSecret, "from-file") {
		t.Fatalf("load: %v", err)
	}
}
