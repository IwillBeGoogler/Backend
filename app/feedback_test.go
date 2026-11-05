package app_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestFeedback(t *testing.T) {
	e := start(t)
	anon := e.anon(t)
	admin := anon.login("admin", "admin-pass-123")
	ali := anon.register("ali")

	msg := func(m map[string]any) map[string]any {
		out := map[string]any{"kind": "bug", "message": "login is broken", "page_url": "https://x.test/login", "app_version": "1.2.3"}
		for k, v := range m {
			out[k] = v
		}
		return out
	}

	// Anonymous callers can send; a client-sent user id is ignored.
	anon.do("POST", "/api/feedback", msg(map[string]any{"contact_email": "me@example.com", "user_id": ali.user.ID}), 201, nil)
	anon.do("POST", "/api/feedback", msg(map[string]any{"kind": "nope"}), 400, nil)
	anon.do("POST", "/api/feedback", msg(map[string]any{"message": "   "}), 400, nil)
	anon.do("POST", "/api/feedback", msg(map[string]any{"contact_email": "not an email"}), 400, nil)
	// The honeypot looks like success and stores nothing.
	anon.do("POST", "/api/feedback", msg(map[string]any{"website": "http://spam"}), 201, nil)
	// 5 per 10 minutes per IP when anonymous.
	anon.do("POST", "/api/feedback", msg(nil), 429, nil)

	// A logged-in sender is the session user; their contact email is dropped.
	ali.do("POST", "/api/feedback", msg(map[string]any{"kind": "idea", "contact_email": "x@example.com",
		"error_message": "boom", "error_stack": strings.Repeat("s", 20000)}), 201, nil)
	ali.do("POST", "/api/feedback", msg(map[string]any{"message": strings.Repeat("a", 5001)}), 400, nil)
	ali.do("POST", "/api/feedback", msg(map[string]any{"message": strings.Repeat("a", 70<<10)}), 400, nil) // body limit

	// Only moderators read and decide.
	ali.do("GET", "/api/admin/feedback", nil, 403, nil)
	anon.do("GET", "/api/admin/feedback", nil, 401, nil)
	type item struct {
		ID           int    `json:"id"`
		UserID       int    `json:"user_id"`
		Username     string `json:"username"`
		Kind         string `json:"kind"`
		ContactEmail string `json:"contact_email"`
		ErrorStack   string `json:"error_stack"`
		Status       string `json:"status"`
	}
	var pg struct {
		Items []item
		Total int
	}
	admin.do("GET", "/api/admin/feedback", nil, 200, &pg)
	if len(pg.Items) != 2 { // honeypot and invalid ones were not stored
		t.Fatalf("stored = %d, want 2: %+v", len(pg.Items), pg.Items)
	}
	byAli, byAnon := pg.Items[0], pg.Items[1]
	if byAli.UserID != ali.user.ID || byAli.Username != "ali" || byAli.ContactEmail != "" || len(byAli.ErrorStack) != 10000 {
		t.Errorf("logged-in entry = %+v", byAli)
	}
	if byAnon.UserID != 0 || byAnon.Username != "" || byAnon.ContactEmail != "me@example.com" {
		t.Errorf("anonymous entry = %+v", byAnon)
	}
	admin.do("GET", "/api/admin/feedback?kind=idea", nil, 200, &pg)
	if len(pg.Items) != 1 || pg.Items[0].Kind != "idea" {
		t.Errorf("kind filter = %+v", pg.Items)
	}
	admin.do("GET", "/api/admin/feedback?kind=nope", nil, 400, nil)

	path := fmt.Sprintf("/api/admin/feedback/%d", byAnon.ID)
	ali.do("PUT", path, map[string]any{"status": "resolved"}, 403, nil)
	admin.do("PUT", path, map[string]any{"status": "bogus"}, 400, nil)
	admin.do("PUT", "/api/admin/feedback/99999", map[string]any{"status": "resolved"}, 404, nil)
	var done item
	admin.do("PUT", path, map[string]any{"status": "resolved"}, 200, &done)
	if done.Status != "resolved" {
		t.Errorf("status = %q", done.Status)
	}
	admin.do("GET", "/api/admin/feedback?status=open", nil, 200, &pg)
	if len(pg.Items) != 1 {
		t.Errorf("open = %d, want 1", len(pg.Items))
	}
}
