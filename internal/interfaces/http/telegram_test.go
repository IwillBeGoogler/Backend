package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
)

func TestTelegramWebhookSecret(t *testing.T) {
	call := func(tg *application.Telegram, secret, body string) int {
		h := NewHandler(Deps{Telegram: tg})
		r := httptest.NewRequest(http.MethodPost, application.TelegramWebhookPath, strings.NewReader(body))
		if secret != "" {
			r.Header.Set("X-Telegram-Bot-Api-Secret-Token", secret)
		}
		w := httptest.NewRecorder()
		h.telegramWebhook(w, r)
		return w.Code
	}
	on := &application.Telegram{Mode: application.TelegramWebhook, WebhookSecret: "right"}
	for _, tc := range []struct {
		name         string
		tg           *application.Telegram
		secret, body string
		want         int
	}{
		{"disabled", nil, "right", "{}", 404},
		{"poll mode", &application.Telegram{Mode: application.TelegramPoll, WebhookSecret: "right"}, "right", "{}", 404},
		{"empty configured secret", &application.Telegram{Mode: application.TelegramWebhook}, "", "{}", 404},
		{"no header", on, "", "{}", 401},
		{"wrong", on, "wrong", "{}", 401},
		{"ignored update", on, "right", `{"update_id":1}`, 200},
		{"bad json", on, "right", "{", 200},
	} {
		if got := call(tc.tg, tc.secret, tc.body); got != tc.want {
			t.Errorf("%s = %d, want %d", tc.name, got, tc.want)
		}
	}
}
