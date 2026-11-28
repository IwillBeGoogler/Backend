package app_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/config"
)

func TestTelegramLink(t *testing.T) {
	bot := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	t.Cleanup(bot.Close)
	const secret = "test-webhook-secret-0123456789abcdef"
	e := start(t, func(c *config.Config) {
		c.TelegramBotToken, c.TelegramBotUsername, c.TelegramMode = "1:fake", "testbot", "webhook"
		c.TelegramWebhookSecret, c.TelegramAPIURL = secret, bot.URL
	})
	anon := e.anon(t)
	ali := anon.register("ali")
	anon.do("GET", "/api/me/telegram", nil, 401, nil)
	anon.do("POST", "/api/me/telegram/link", nil, 401, nil)

	var status struct {
		Enabled bool `json:"enabled"`
		Linked  bool `json:"linked"`
	}
	ali.do("GET", "/api/me/telegram", nil, 200, &status)
	if !status.Enabled || status.Linked {
		t.Fatalf("status = %+v", status)
	}
	var link struct {
		URL string `json:"url"`
	}
	ali.do("POST", "/api/me/telegram/link", nil, 200, &link)
	u, err := url.Parse(link.URL)
	must(t, err)
	token := u.Query().Get("start")

	update := func(key, text string, chat int, want int) {
		t.Helper()
		body := `{"update_id":1,"message":{"text":` + strconv.Quote(text) + `,"chat":{"id":` + strconv.Itoa(chat) + `,"type":"private"}}}`
		req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/api/telegram/webhook", bytes.NewReader([]byte(body)))
		req.Header.Set("X-Telegram-Bot-Api-Secret-Token", key)
		res, err := http.DefaultClient.Do(req)
		must(t, err)
		res.Body.Close()
		if res.StatusCode != want {
			t.Fatalf("webhook = %d, want %d", res.StatusCode, want)
		}
	}
	if token == "" || len(token) > 64 || !strings.HasPrefix(link.URL, "https://t.me/testbot?start=") {
		t.Fatal("bad link")
	}
	update("wrong", "/start "+token, 5, 401)
	update(secret, "/start "+token, 5, 200)
	ali.do("GET", "/api/me/telegram", nil, 200, &status)
	if !status.Linked {
		t.Fatal("not linked")
	}
	ali.do("DELETE", "/api/me/telegram", nil, 204, nil)
	update(secret, "/start "+token, 5, 200) // single use
	ali.do("GET", "/api/me/telegram", nil, 200, &status)
	if status.Linked {
		t.Fatal("token reused")
	}
}
