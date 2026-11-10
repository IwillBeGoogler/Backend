package app_test

// Error branches of the blog handlers that the scenario tests do not reach:
// upload validation, rate limits, bad paths and ids, feeds and reports.

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"testing"
)

// uploadRaw posts data as multipart field "file" (or no file when field is "").
func uploadRaw(c *client, field string, data []byte) int {
	c.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if field != "" {
		fw, _ := mw.CreateFormFile(field, "f.bin")
		fw.Write(data)
	}
	mw.Close()
	req, _ := http.NewRequest("POST", c.base+"/api/uploads", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	res, err := http.DefaultClient.Do(req)
	must(c.t, err)
	res.Body.Close()
	return res.StatusCode
}

// limited reports whether any of n calls to fn answered 429.
func limited(n int, fn func() int) bool {
	for range n {
		if fn() == 429 {
			return true
		}
	}
	return false
}

func TestBlogHandlerErrorBranches(t *testing.T) {
	e := start(t)
	anon := e.anon(t)
	ali := anon.register("ali")
	vali := anon.register("vali")

	var p post
	ali.do("POST", "/api/posts", map[string]any{"title": "first", "body": "hello world", "status": "published"}, 201, &p)
	pid := fmt.Sprint(p.ID)

	// uploads
	if c := uploadRaw(anon, "file", []byte("x")); c != 401 {
		t.Errorf("anon upload = %d", c)
	}
	if c := uploadRaw(ali, "", nil); c != 400 {
		t.Errorf("no file = %d", c)
	}
	if c := uploadRaw(ali, "other", []byte("x")); c != 400 {
		t.Errorf("wrong field = %d", c)
	}
	if c := uploadRaw(ali, "file", []byte("plain text")); c != 400 {
		t.Errorf("text = %d", c)
	}
	// PNG signature followed by garbage: right type, not a decodable image
	if c := uploadRaw(ali, "file", append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{1}, 64)...)); c != 400 {
		t.Errorf("fake png = %d", c)
	}
	if c := uploadRaw(ali, "file", append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 6<<20)...)); c != 400 {
		t.Errorf("too large = %d", c)
	}
	anon.do("GET", "/api/uploads/does-not-exist.png", nil, 404, nil)
	anon.do("GET", "/api/uploads/img/nope-640.webp", nil, 404, nil)
	if !limited(12, func() int { return uploadRaw(vali, "", nil) }) {
		t.Error("upload never rate limited")
	}

	// ids and missing posts
	anon.do("POST", "/api/posts/abc/view", nil, 400, nil)
	anon.do("POST", "/api/posts/999999/view", nil, 404, nil)
	anon.do("GET", "/api/posts/999999/comments/stream", nil, 404, nil)
	anon.do("GET", "/api/posts/abc/comments/stream", nil, 400, nil)
	ali.do("PUT", "/api/posts/999999", map[string]any{"title": "x"}, 404, nil)
	ali.do("PUT", "/api/posts/abc", map[string]any{"title": "x"}, 400, nil)
	anon.do("PUT", "/api/posts/"+pid, map[string]any{"title": "x"}, 401, nil)

	// pin
	ali.do("PUT", "/api/me/pin", map[string]any{"post_id": 0}, 400, nil)
	ali.do("PUT", "/api/me/pin", map[string]any{"post_id": 999999}, 404, nil)
	anon.do("PUT", "/api/me/pin", map[string]any{"post_id": p.ID}, 401, nil)
	vali.do("PUT", "/api/me/pin", map[string]any{"post_id": p.ID}, 400, nil)
	ali.do("PUT", "/api/me/pin", map[string]any{"post_id": p.ID}, 204, nil)

	// reports
	if c := ali.status("POST", "/api/reports", map[string]any{"target_type": "diagram", "target_file_id": "no-such-file", "reason": "spam"}); c < 400 || c >= 500 {
		t.Errorf("report missing diagram = %d", c)
	}
	if c := ali.status("POST", "/api/reports", map[string]any{"target_type": "post", "target_id": 999999, "reason": "spam"}); c != 404 {
		t.Errorf("report missing post = %d", c)
	}
	anon.do("POST", "/api/reports", map[string]any{"target_type": "post", "target_id": p.ID, "reason": "spam"}, 401, nil)

	// feedback honeypot looks like success; stored nothing
	anon.do("POST", "/api/feedback", map[string]any{"kind": "bug", "message": "hi there", "website": "http://spam"}, 201, nil)
	anon.do("POST", "/api/feedback", "not-an-object", 400, nil)

	// feeds
	anon.do("GET", "/api/feed.xml", nil, 200, nil)
	anon.do("GET", "/api/sitemap.xml", nil, 200, nil)
	anon.do("GET", "/api/users/nobody-here/feed.xml", nil, 404, nil)

	// rate limits on auth and read endpoints
	if !limited(8, func() int {
		return anon.status("POST", "/api/auth/forgot-password", map[string]string{"email": "nobody@example.com"})
	}) {
		t.Error("forgot-password never rate limited")
	}
	if !limited(14, func() int {
		return anon.status("POST", "/api/auth/reset-password", map[string]string{"token": "x", "password": "secret123"})
	}) {
		t.Error("reset-password never rate limited")
	}
	if !limited(14, func() int { return anon.status("POST", "/api/auth/verify-email", map[string]string{"code": "x"}) }) {
		t.Error("verify-email never rate limited")
	}
	if !limited(14, func() int {
		return anon.status("POST", "/api/auth/login/2fa", map[string]string{"mfa_token": "x", "code": "000000"})
	}) {
		t.Error("login/2fa never rate limited")
	}
	if !limited(130, func() int { return anon.status("POST", "/api/posts/"+pid+"/view", nil) }) {
		t.Error("view never rate limited")
	}
	if !limited(130, func() int { return anon.status("POST", "/api/posts/"+pid+"/read", map[string]any{"seconds": 5}) }) {
		t.Error("read never rate limited")
	}
	if !limited(8, func() int {
		return anon.status("POST", "/api/feedback", map[string]any{"kind": "bug", "message": "hello there", "website": "x"})
	}) {
		t.Error("feedback never rate limited")
	}
}
