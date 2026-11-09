package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/markdown"
)

func previewServer(t *testing.T, limit Limiter) *httptest.Server {
	t.Helper()
	blog := &application.Blog{Ports: application.Ports{Markdown: markdown.New()}}
	h := NewHandler(Deps{
		Blog:  blog,
		Limit: limit,
		UserID: func(ctx context.Context) int {
			id, _ := ctx.Value(testUser{}).(int)
			return id
		},
	})
	mux := http.NewServeMux()
	h.Routes(mux)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test-User") != "" {
			r = r.WithContext(context.WithValue(r.Context(), testUser{}, 1))
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func previewReq(t *testing.T, srv *httptest.Server, user bool, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("POST", srv.URL+"/api/posts/preview", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if user {
		req.Header.Set("X-Test-User", "1")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out struct {
		Data struct {
			BodyHTML string `json:"body_html"`
		} `json:"data"`
	}
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out.Data.BodyHTML
}

func TestPreviewPost(t *testing.T) {
	allowAll := func(context.Context, string, int, time.Duration) (bool, time.Duration) { return true, 0 }
	srv := previewServer(t, allowAll)

	if code, _ := previewReq(t, srv, false, `{"body":"hi"}`); code != http.StatusUnauthorized {
		t.Errorf("anonymous = %d, want 401", code)
	}
	code, html := previewReq(t, srv, true, `{"body":"# Hi\n\n<script>alert(1)</script>\n\n**b**\n\n`+"```html-live height=200\\n<b>x</b>\\n```"+`"}`)
	if code != 200 || !strings.Contains(html, "<h1") || !strings.Contains(html, "<strong>b</strong>") {
		t.Fatalf("render = %d %q", code, html)
	}
	if strings.Contains(html, "<script") {
		t.Errorf("script survived: %q", html)
	}
	if !strings.Contains(html, "language-html-live-200") {
		t.Errorf("live block not rendered like a published post: %q", html)
	}
	big := `{"body":"` + strings.Repeat("a", application.MaxPreviewBody+1) + `"}`
	if code, _ := previewReq(t, srv, true, big); code != http.StatusUnprocessableEntity && code != http.StatusBadRequest {
		t.Errorf("oversize = %d, want 4xx", code)
	}
	huge := `{"body":"` + strings.Repeat("a", 5*application.MaxPreviewBody) + `"}`
	if code, _ := previewReq(t, srv, true, huge); code != http.StatusBadRequest {
		t.Errorf("huge = %d, want 400", code)
	}

	limited := previewServer(t, func(context.Context, string, int, time.Duration) (bool, time.Duration) { return false, time.Second })
	if code, _ := previewReq(t, limited, true, `{"body":"x"}`); code != http.StatusTooManyRequests {
		t.Errorf("limited = %d, want 429", code)
	}
}
