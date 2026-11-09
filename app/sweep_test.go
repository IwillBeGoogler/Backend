package app_test

// Every blog route is called as nobody, as a plain user and as the admin
// with no body, a broken body and an empty object. The point is the error
// branches of the handlers: whatever arrives, the server must answer.

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func blogRoutes(t *testing.T) []string {
	t.Helper()
	files, _ := filepath.Glob("../internal/interfaces/http/*.go")
	re := regexp.MustCompile(`HandleFunc\("([A-Z]+) (/[^"]*)"`)
	seen := map[string]bool{}
	for _, f := range files {
		base := filepath.Base(f)
		if strings.HasSuffix(base, "_test.go") || strings.HasPrefix(base, "diagram") || base == "admin_diagrams.go" {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(src), -1) {
			seen[m[1]+" "+m[2]] = true
		}
	}
	var out []string
	for r := range seen {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

func TestBlogRouteSweep(t *testing.T) {
	e := start(t)
	anon := e.anon(t)
	user := anon.register("sweeper")
	admin := anon.login("admin", "admin-pass-123")

	// real rows so the id-bearing routes get past the lookup
	var p post
	user.do("POST", "/api/posts", map[string]any{"title": "Sweep", "body": "hello", "status": "published"}, 201, &p)
	repl := strings.NewReplacer("{id}", fmt.Sprint(p.ID), "{slug}", p.Slug, "{version}", "1", "{username}", "sweeper", "{name}", "x")

	skip := func(path string) bool {
		for _, s := range []string{"stream", "/uploads", "/oauth", "/social", "/admin/export", "/me/delete"} {
			if strings.Contains(path, s) {
				return true
			}
		}
		return false
	}
	bodies := []string{"", "{broken", "{}"}
	calls := 0
	// admin goes last: some routes end its session or account
	for _, who := range []*client{anon, user, admin} {
		for _, route := range blogRoutes(t) {
			method, path, _ := strings.Cut(route, " ")
			path = repl.Replace(path)
			if skip(path) || (who == admin && (method == "DELETE" && (strings.HasPrefix(path, "/api/me") || strings.HasPrefix(path, "/api/auth")))) {
				continue
			}
			for _, body := range bodies {
				if method == "GET" && body != "" {
					continue
				}
				req, _ := http.NewRequest(method, who.base+path, bytes.NewReader([]byte(body)))
				req.Header.Set("Content-Type", "application/json")
				if who.token != "" {
					req.Header.Set("Authorization", "Bearer "+who.token)
				}
				res, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Errorf("%s %s: %v", method, path, err)
					continue
				}
				res.Body.Close()
				calls++
			}
		}
	}
	if calls < 300 {
		t.Fatalf("only %d calls: route table not read?", calls)
	}
}
