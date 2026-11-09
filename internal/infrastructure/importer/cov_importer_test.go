package importer

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type covErrReader struct{}

func (covErrReader) Read([]byte) (int, error) { return 0, errors.New("read boom") }

func covBase() *url.URL { u, _ := url.Parse("https://example.com/a/b"); return u }

func TestCovParseReaderError(t *testing.T) {
	if _, err := Parse(covErrReader{}, covBase()); err == nil {
		t.Fatal("want error")
	}
}

func TestCovParseFull(t *testing.T) {
	page := `<html><head><title> Fallback </title>
<meta property="og:description" content="sub"><meta name="description" content="ignored">
<meta property="og:image" content="/cover.png"><meta property="og:image" content="http://x/y.png">
<link rel="canonical" href="/canon"><link rel="canonical" href="/second">
<meta content="nokey"></head><body>
<article>
<h1>Fallback</h1><h2><em></em></h2>
<p>Hello <strong>bold</strong> <b></b><em>it</em> <code>a` + "`" + `b</code> <code> </code> text_with*special [x] &lt;tag&gt;<br>next</p>
<p><a href="/rel">link</a> <a href="javascript:x">bad</a> <a href="/e"></a></p>
<hr>
<img src="/i.png" alt="al*t"><img data-src="https://x/y.png"><img src="javascript:x">
<pre><code class="language-go">fmt.Println()<br>x</code></pre>
<pre><code class="language-html-live-0">x</code></pre>
<pre><code class="language-a b">y</code></pre>
<pre>plain</pre>
<blockquote><p>quoted</p><blockquote>deeper<br>line</blockquote></blockquote>
<ul><li>one<ul><li>nested</li></ul></li><li></li>text<li>two</li></ul>
<ol><li>a</li><li>b</li></ol><ul></ul>
<!-- comment --><script>x()</script><nav>n</nav><div>div <span>span</span></div>
</article></body></html>`
	got, err := Parse(strings.NewReader(page), covBase())
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Fallback" || got.Subtitle != "sub" {
		t.Errorf("%+v", got)
	}
	if got.CanonicalURL != "https://example.com/canon" {
		t.Errorf("canon %q", got.CanonicalURL)
	}
	if got.CoverURL != "https://example.com/cover.png" {
		t.Errorf("cover %q", got.CoverURL)
	}
	for _, want := range []string{"**bold**", "*it*", "`ab`", "[link](https://example.com/rel)", "---",
		"![al\\*t](https://example.com/i.png)", "```go\nfmt.Println()\nx\n```", "> quoted", "> > deeper", "- one", "1. a", "2. b", "div span", "- two"} {
		if !strings.Contains(got.Body, want) {
			t.Errorf("missing %q in\n%s", want, got.Body)
		}
	}
	if strings.Contains(got.Body, "x()") || strings.Contains(got.Body, "html-live") || strings.Contains(got.Body, "language-a b") {
		t.Errorf("leak:\n%s", got.Body)
	}
}

func TestCovParseTitleFromH1AndStripHeading(t *testing.T) {
	got, err := Parse(strings.NewReader(`<body><main><h1>Only Heading</h1><p>text</p></main></body>`), covBase())
	if err != nil || got.Title != "Only Heading" {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = Parse(strings.NewReader(`<head><title>T</title></head><body><h1>T</h1><p>text</p></body>`), covBase())
	if err != nil || strings.Contains(got.Body, "# T") {
		t.Fatalf("%q %v", got.Body, err)
	}
}

func TestCovParseNoContent(t *testing.T) {
	for _, p := range []string{`<body><script>x</script></body>`, `<body><article> </article></body>`} {
		if _, err := Parse(strings.NewReader(p), covBase()); err == nil {
			t.Errorf("%s: want no-content error", p)
		}
	}
}

func TestCovFirstResolveText(t *testing.T) {
	if first(" ", "") != "" || first("", " x ") != "x" {
		t.Error("first")
	}
	if text(nil) != "" {
		t.Error("text nil")
	}
	b := covBase()
	if resolve(b, "") != "" || resolve(b, "mailto:a@b") != "" || resolve(b, "http://[::bad") != "" || resolve(b, "/x") != "https://example.com/x" {
		t.Error("resolve")
	}
}

func TestCovFetch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><title>T</title></head><body><p>hi there</p></body></html>`))
	})
	mux.HandleFunc("/404", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	mux.HandleFunc("/json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	mux.HandleFunc("/loop", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/loop", http.StatusFound) })
	mux.HandleFunc("/ftp", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "ftp://example.com/x")
		w.WriteHeader(http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	im := New(true)
	ctx := context.Background()

	got, err := im.Fetch(ctx, srv.URL+"/ok")
	if err != nil || got.Title != "T" || !strings.Contains(got.Body, "hi there") {
		t.Fatalf("%+v %v", got, err)
	}
	for _, p := range []string{"/404", "/json", "/loop", "/ftp"} {
		if _, err := im.Fetch(ctx, srv.URL+p); err == nil {
			t.Errorf("%s: want error", p)
		}
	}
	for _, raw := range []string{"ftp://x/y", "http://", "http://u:p@x/", "://bad"} {
		if _, err := im.Fetch(ctx, raw); err == nil {
			t.Errorf("%q: want invalid", raw)
		}
	}
	url := srv.URL
	srv.Close()
	if _, err := im.Fetch(ctx, url+"/ok"); err == nil {
		t.Error("closed server: want error")
	}
}

func TestCovFetchPublicAddressPassesGuard(t *testing.T) {
	// TEST-NET-1 is not in the blocked list, so the dial Control hook accepts
	// it; the connect itself then fails or times out.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := New(false).Fetch(ctx, "http://192.0.2.1/")
	if err == nil || errors.Is(err, ErrForbiddenAddress) {
		t.Fatalf("got %v", err)
	}
}
