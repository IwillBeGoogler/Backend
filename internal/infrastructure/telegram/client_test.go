package telegram_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	domain "github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/telegram"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/telegram"
)

const token = "123456:test-token-not-real"

func fake(t *testing.T, h http.HandlerFunc) *telegram.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return telegram.New(token, srv.URL)
}

func TestCalls(t *testing.T) {
	got := map[string]map[string]any{}
	c := fake(t, func(w http.ResponseWriter, r *http.Request) {
		method, ok := strings.CutPrefix(r.URL.Path, "/bot"+token+"/")
		if !ok {
			t.Errorf("path = %s", r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		got[method] = body
		if method == "getUpdates" {
			fmt.Fprint(w, `{"ok":true,"result":[{"update_id":7,"message":{"text":"/stop","chat":{"id":5,"type":"private"},"from":{"language_code":"ru"}}},{"update_id":8}]}`)
			return
		}
		fmt.Fprint(w, `{"ok":true,"result":true}`)
	})
	ctx := context.Background()
	if err := c.SendMessage(ctx, 5, "<b>hi</b>"); err != nil {
		t.Fatal(err)
	}
	if m := got["sendMessage"]; m["text"] != "<b>hi</b>" || m["parse_mode"] != nil {
		t.Errorf("sendMessage = %v", m)
	}
	if err := c.SetWebhook(ctx, "https://x/api/telegram/webhook", "s"); err != nil || got["setWebhook"]["secret_token"] != "s" {
		t.Fatalf("setWebhook = %v, %v", got["setWebhook"], err)
	}
	u, err := c.Updates(ctx, 7, time.Second)
	if err != nil || len(u) != 2 || u[0] != (domain.Update{ID: 7, ChatID: 5, ChatType: "private", Text: "/stop", LanguageCode: "ru"}) || u[1].ChatID != 0 {
		t.Fatalf("updates = %+v, %v", u, err)
	}
}

func TestRefusals(t *testing.T) {
	for _, tc := range []struct {
		status    int
		body      string
		code      int
		permanent bool
		retry     time.Duration
	}{
		{429, `{"ok":false,"error_code":429,"description":"slow down","parameters":{"retry_after":7}}`, 429, false, 7 * time.Second},
		{403, `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`, 403, true, 0},
		{400, `{"ok":false,"error_code":400,"description":"bad ` + token + `"}`, 400, true, 0},
		{502, `<html>`, 502, false, 0},
	} {
		c := fake(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			fmt.Fprint(w, tc.body)
		})
		err := c.SendMessage(context.Background(), 1, "x")
		var te *domain.Error
		if !errors.As(err, &te) || te.Code != tc.code || te.Permanent() != tc.permanent || te.RetryAfter != tc.retry || te.Blocked() != (tc.code == 403) {
			t.Errorf("%d: %#v", tc.status, err)
		}
		if strings.Contains(err.Error(), token) {
			t.Errorf("%d: token in error", tc.status)
		}
	}
}

func TestTokenNeverInErrors(t *testing.T) {
	check := func(name string, c *telegram.Client, ctx context.Context) error {
		t.Helper()
		err := c.SetWebhook(ctx, "https://x", "s")
		if err == nil {
			t.Fatalf("%s: no error", name)
		}
		for e := err; e != nil; e = errors.Unwrap(e) {
			if s := fmt.Sprintf("%v %+v", e, e); strings.Contains(s, "test-token") {
				t.Errorf("%s: token leaked", name)
			}
		}
		if strings.Contains(fmt.Sprintf("%v %+v %#v", c, c, c), "test-token") {
			t.Errorf("%s: token printed with the client", name)
		}
		return err
	}
	closed := httptest.NewServer(nil)
	closed.Close()
	_ = check("refused", telegram.New(token, closed.URL), context.Background())
	_ = check("bad url", telegram.New(token, "http://a b"), context.Background())
	_ = check("bad scheme", telegram.New(token, "nope://x"), context.Background())
	_ = check("garbage", fake(t, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "garbage") }), context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := check("cancelled", telegram.New(token, closed.URL), ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("cancel cause lost: %v", err)
	}
}
