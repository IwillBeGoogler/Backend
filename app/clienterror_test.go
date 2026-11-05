package app_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestClientErrors(t *testing.T) {
	e := start(t)
	anon := e.anon(t)
	admin := anon.login("admin", "admin-pass-123")
	ali := anon.register("ali")

	report := func(m map[string]any) map[string]any {
		out := map[string]any{"message": "boom for me@example.com", "page_url": "https://x.test/p?token=abc#h",
			"stack": "Error: boom\n at f (https://x.test/assets/index-Dk3jf92K.js:10:5)", "user_agent": "UA/1"}
		for k, v := range m {
			out[k] = v
		}
		return out
	}

	// Accepted input is 204; bad JSON and an empty message are 400.
	anon.do("POST", "/api/client-errors", report(nil), 204, nil)
	// Another build and line numbers: same group. A client fingerprint is ignored.
	anon.do("POST", "/api/client-errors", report(map[string]any{"fingerprint": "mine",
		"stack": "Error: boom\n at f (https://x.test/assets/index-Zq81PmLw.js:99:1)"}), 204, nil)
	anon.do("POST", "/api/client-errors", report(map[string]any{"message": "   "}), 400, nil)
	anon.do("POST", "/api/client-errors", report(map[string]any{"message": strings.Repeat("a", 40<<10)}), 400, nil) // body limit
	// A logged-in caller is not recorded either.
	ali.do("POST", "/api/client-errors", report(map[string]any{"message": "other"}), 204, nil)

	// Only moderators read and decide.
	ali.do("GET", "/api/admin/client-errors", nil, 403, nil)
	anon.do("GET", "/api/admin/client-errors", nil, 401, nil)
	type item struct {
		ID          int    `json:"id"`
		Fingerprint string `json:"fingerprint"`
		Message     string `json:"message"`
		PageURL     string `json:"page_url"`
		Count       int    `json:"count"`
		Status      string `json:"status"`
	}
	var pg struct {
		Items []item
		Total int
	}
	admin.do("GET", "/api/admin/client-errors", nil, 200, &pg)
	if len(pg.Items) != 2 {
		t.Fatalf("groups = %d, want 2: %+v", len(pg.Items), pg.Items)
	}
	other, boom := pg.Items[0], pg.Items[1] // newest last_seen first
	if other.Message != "other" || boom.Count != 2 || boom.Fingerprint == "mine" ||
		boom.Message != "boom for [redacted]" || boom.PageURL != "https://x.test/p" {
		t.Errorf("entries = %+v", pg.Items)
	}
	admin.do("GET", "/api/admin/client-errors?status=nope", nil, 400, nil)

	// Resolved reopens on recurrence; ignored stays.
	path := fmt.Sprintf("/api/admin/client-errors/%d", boom.ID)
	ali.do("PUT", path, map[string]any{"status": "resolved"}, 403, nil)
	admin.do("PUT", path, map[string]any{"status": "bogus"}, 400, nil)
	admin.do("PUT", "/api/admin/client-errors/99999", map[string]any{"status": "resolved"}, 404, nil)
	var done item
	admin.do("PUT", path, map[string]any{"status": "resolved"}, 200, &done)
	if done.Status != "resolved" {
		t.Errorf("status = %q", done.Status)
	}
	anon.do("POST", "/api/client-errors", report(nil), 204, nil)
	admin.do("GET", "/api/admin/client-errors?status=open", nil, 200, &pg)
	if len(pg.Items) != 2 || pg.Items[0].ID != boom.ID || pg.Items[0].Count != 3 {
		t.Errorf("after regression = %+v", pg.Items)
	}
	admin.do("PUT", path, map[string]any{"status": "ignored"}, 200, nil)
	anon.do("POST", "/api/client-errors", report(nil), 204, nil)
	admin.do("GET", "/api/admin/client-errors?status=ignored", nil, 200, &pg)
	if len(pg.Items) != 1 || pg.Items[0].Count != 4 {
		t.Errorf("ignored = %+v", pg.Items)
	}

	// 20 per 10 minutes per IP: 8 so far (6 accepted, 2 rejected count too), so 12 more fit.
	var limited int
	for range 20 {
		if anon.status("POST", "/api/client-errors", report(nil)) == 429 {
			limited++
		}
	}
	if limited == 0 {
		t.Error("no 429 after 20+ reports from one IP")
	}
}
