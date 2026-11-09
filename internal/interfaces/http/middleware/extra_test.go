package middleware

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/redis/go-redis/v9"
)

// fakeRedis is a minimal RESP2 server: GET, SET, INCR.
type fakeRedis struct {
	mu   sync.Mutex
	data map[string]string
	fail bool // answer every command with an error
	ln   net.Listener
}

func newFakeRedis(t *testing.T) (*fakeRedis, *redis.Client) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeRedis{data: map[string]string{}, ln: ln}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	rdb := redis.NewClient(&redis.Options{Addr: ln.Addr().String(), Protocol: 2, DisableIdentity: true, MaxRetries: -1})
	t.Cleanup(func() { rdb.Close(); ln.Close() })
	return f, rdb
}

func (f *fakeRedis) setFail(v bool) { f.mu.Lock(); f.fail = v; f.mu.Unlock() }

func (f *fakeRedis) get(k string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.data[k]
	return v, ok
}

func (f *fakeRedis) serve(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		n, _ := strconv.Atoi(strings.TrimSpace(line[1:]))
		args := make([]string, 0, n)
		for i := 0; i < n; i++ {
			l, err := br.ReadString('\n')
			if err != nil {
				return
			}
			sz, _ := strconv.Atoi(strings.TrimSpace(l[1:]))
			buf := make([]byte, sz+2)
			if _, err := io.ReadFull(br, buf); err != nil {
				return
			}
			args = append(args, string(buf[:sz]))
		}
		f.mu.Lock()
		fail := f.fail
		var out string
		switch cmd := strings.ToUpper(args[0]); {
		case fail:
			out = "-ERR boom\r\n"
		case cmd == "HELLO":
			out = "-ERR unknown command 'hello'\r\n"
		case cmd == "GET":
			if v, ok := f.data[args[1]]; ok {
				out = fmt.Sprintf("$%d\r\n%s\r\n", len(v), v)
			} else {
				out = "$-1\r\n"
			}
		case cmd == "SET":
			f.data[args[1]] = args[2]
			out = "+OK\r\n"
		case cmd == "INCR":
			v, _ := strconv.Atoi(f.data[args[1]])
			v++
			f.data[args[1]] = strconv.Itoa(v)
			out = fmt.Sprintf(":%d\r\n", v)
		default:
			out = "+OK\r\n"
		}
		f.mu.Unlock()
		c.Write([]byte(out))
	}
}

func jsonHandler(calls *int, status int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(`{"ok":true}`))
	})
}

func do(h http.Handler, method, url string, mod func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, url, nil)
	if mod != nil {
		mod(r)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestCacheMissThenHit(t *testing.T) {
	f, rdb := newFakeRedis(t)
	calls := 0
	h := Cache(rdb, jsonHandler(&calls, 200))
	if w := do(h, "GET", "/api/posts?tag=go", nil); w.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("first: %q", w.Header().Get("X-Cache"))
	}
	w := do(h, "GET", "/api/posts?tag=go", nil)
	if w.Header().Get("X-Cache") != "HIT" || w.Body.String() != `{"ok":true}` || calls != 1 {
		t.Fatalf("second: %q calls=%d body=%q", w.Header().Get("X-Cache"), calls, w.Body.String())
	}
	if _, ok := f.get("cache:0:GET:/api/posts?tag=go"); !ok {
		t.Fatal("key not stored")
	}
}

func TestCacheBypasses(t *testing.T) {
	_, rdb := newFakeRedis(t)
	calls := 0
	h := Cache(rdb, jsonHandler(&calls, 200))
	for _, c := range []struct {
		method, url string
		mod         func(*http.Request)
	}{
		{"GET", "/api/me", nil},
		{"GET", "/api/posts/stream", nil},
		{"GET", "/api/posts?junk=1", nil},
		{"GET", "/api/posts", func(r *http.Request) { r.Header.Set("Authorization", "x") }},
		{"OPTIONS", "/api/posts", nil},
	} {
		if w := do(h, c.method, c.url, c.mod); w.Header().Get("X-Cache") != "" {
			t.Errorf("%s %s cached", c.method, c.url)
		}
	}
	if calls != 5 {
		t.Fatalf("calls %d", calls)
	}
}

func TestCacheNonOKNotStored(t *testing.T) {
	f, rdb := newFakeRedis(t)
	calls := 0
	h := Cache(rdb, jsonHandler(&calls, 404))
	do(h, "GET", "/api/posts", nil)
	if _, ok := f.get("cache:0:GET:/api/posts"); ok {
		t.Fatal("404 stored")
	}
}

func TestCacheSetErrorIgnored(t *testing.T) {
	f, rdb := newFakeRedis(t)
	calls := 0
	h := Cache(rdb, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.setFail(true) // GET already done; SET will fail
		jsonHandler(&calls, 200).ServeHTTP(w, r)
	}))
	if w := do(h, "GET", "/api/posts", nil); w.Code != 200 {
		t.Fatalf("code %d", w.Code)
	}
}

func TestCacheWriteInvalidates(t *testing.T) {
	f, rdb := newFakeRedis(t)
	calls := 0
	h := Cache(rdb, jsonHandler(&calls, 200))
	do(h, "POST", "/api/posts", nil)
	if v, _ := f.get(cacheVerKey); v != "1" {
		t.Fatalf("ver %q", v)
	}
	do(h, "PUT", "/api/auth/login", nil) // session only: no bump
	do(h, "DELETE", "/api/diagrams/x", nil)
	if v, _ := f.get(cacheVerKey); v != "1" {
		t.Fatalf("ver %q after session-only writes", v)
	}
	skip := Cache(rdb, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SkipInvalidate(r.Context())
	}))
	do(skip, "POST", "/api/posts/1/view", nil)
	fail := Cache(rdb, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	do(fail, "PATCH", "/api/posts/1", nil)
	if v, _ := f.get(cacheVerKey); v != "1" {
		t.Fatalf("ver %q after skip/fail", v)
	}
	f.setFail(true)
	do(h, "POST", "/api/posts", nil) // incr error is only logged
}

func TestResetCache(t *testing.T) {
	f, rdb := newFakeRedis(t)
	if err := ResetCache(context.Background(), rdb); err != nil {
		t.Fatal(err)
	}
	if v, _ := f.get(cacheVerKey); v != "1" {
		t.Fatalf("ver %q", v)
	}
}

func TestSessionOnlyAndKeyURI(t *testing.T) {
	if sessionOnly("/api/diagrams/files/3/publish") {
		t.Error("publish must invalidate")
	}
	if !sessionOnly("/api/diagrams/files/3") || !sessionOnly("/api/guard/x") {
		t.Error("sessionOnly")
	}
	if got := cacheKeyURI(httptest.NewRequest("GET", "/p", nil)); got != "/p" {
		t.Error(got)
	}
}

func TestRecorderUnwrapAndWrite(t *testing.T) {
	w := httptest.NewRecorder()
	rec := &recorder{ResponseWriter: w}
	if rec.Unwrap() != w {
		t.Fatal("unwrap")
	}
	rec.Write([]byte("x")) // unbuffered
	if rec.buf.Len() != 0 || !rec.wrote {
		t.Fatal("unbuffered write")
	}
	g := &guardWriter{ResponseWriter: w}
	if g.Unwrap() != w {
		t.Fatal("guard unwrap")
	}
}

func TestRequestID(t *testing.T) {
	var seen string
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = RequestIDFrom(r.Context()) }))
	w := do(h, "GET", "/", func(r *http.Request) { r.Header.Set(RequestIDHeader, "abc-1") })
	if seen != "abc-1" || w.Header().Get(RequestIDHeader) != "abc-1" {
		t.Fatalf("kept: %q", seen)
	}
	w = do(h, "GET", "/", func(r *http.Request) { r.Header.Set(RequestIDHeader, "bad id!") })
	if len(seen) != 32 || w.Header().Get(RequestIDHeader) != seen {
		t.Fatalf("generated: %q", seen)
	}
	if RequestIDFrom(context.Background()) != "" {
		t.Fatal("empty ctx")
	}
}

func TestSpanRoute(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /a", func(w http.ResponseWriter, r *http.Request) {})
	h := SpanRoute(mux)
	do(h, "GET", "/a", nil)
	do(h, "GET", "/nope", nil) // no pattern: early return
}

func TestClientIPEdges(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "noport"
	if got := ClientIP(false, nil)(r); got != "noport" {
		t.Error(got)
	}
	if got := ClientIP(true, trusted)(r); got != "noport" {
		t.Error(got)
	}
}

func TestCORSNonPreflight(t *testing.T) {
	called := false
	h := CORS(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	do(h, "GET", "/", nil)
	if !called {
		t.Fatal("next not called")
	}
}
