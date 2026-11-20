package markdown

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	r := New()
	cases := []struct{ in, want, notWant string }{
		{"# Hello", `<h1 id="hello">Hello</h1>`, ""},
		{"**b** ~~s~~", "<strong>b</strong> <del>s</del>", ""},
		{"<script>alert(1)</script>hi", "hi", "<script"},
		{`<a href="javascript:alert(1)">x</a>`, "x", "javascript:"},
		{`<img src=x onerror=alert(1)>`, "<img", "onerror"},
		{"```go\nfmt.Println()\n```", `<code class="language-go">`, ""},
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", `src="https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"`, ""},
		{"https://vimeo.com/76979871", `src="https://player.vimeo.com/video/76979871"`, ""},
		{"see https://youtu.be/dQw4w9WgXcQ here", "", "<iframe"},  // not alone on its line
		{"```\nhttps://youtu.be/dQw4w9WgXcQ\n```", "", "<iframe"}, // inside code
		{`<iframe src="https://evil.example/x"></iframe>`, "", "evil"},
		{"[x](https://example.com)", `rel="nofollow`, ""},
	}
	for _, c := range cases {
		got := r.Render(c.in)
		if c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("Render(%q) = %q, want %q", c.in, got, c.want)
		}
		if c.notWant != "" && strings.Contains(got, c.notWant) {
			t.Errorf("Render(%q) = %q, must not contain %q", c.in, got, c.notWant)
		}
	}
}

func diagramRenderer() *Renderer {
	r := New()
	known := map[string]Diagram{
		"fil_public0001":    {FileID: "fil_public0001", Slug: "checkout-1a2b3c4d", Title: `Check <out> & "pay"`, ThumbnailHash: strings.Repeat("a", 64)},
		"checkout-1a2b3c4d": {FileID: "fil_public0001", Slug: "checkout-1a2b3c4d", Title: `Check <out> & "pay"`, ThumbnailHash: strings.Repeat("a", 64)},
		"fil_nothumb001":    {FileID: "fil_nothumb001", Slug: "plain-9f8e7d6c", Title: "Plain"},
	}
	r.SetDiagrams(func(_ context.Context, refs []string) map[string]Diagram {
		out := map[string]Diagram{}
		for _, ref := range refs {
			if d, ok := known[ref]; ok {
				out[ref] = d
			}
		}
		return out
	})
	return r
}

func TestDiagramEmbed(t *testing.T) {
	r := diagramRenderer()
	thumb := strings.Repeat("a", 64)

	got := r.Render("before\n\n::diagram{file=fil_public0001 page=2}\n\nafter")
	for _, want := range []string{
		`<figure class="diagram-embed" data-file="fil_public0001" data-page="2">`,
		`<a href="/d/v/checkout-1a2b3c4d"`,
		`<img src="/api/diagrams/assets/` + thumb + `" alt="Check &lt;out&gt; &amp; &#34;pay&#34;" loading="lazy">`,
		"<figcaption>Check &lt;out&gt; &amp; &#34;pay&#34;</figcaption>", "<p>before</p>", "<p>after</p>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("render = %s\nmissing %s", got, want)
		}
	}

	cases := []struct{ in, want, notWant string }{
		// by slug, default page, with quotes
		{`::diagram{slug="checkout-1a2b3c4d"}`, `data-file="fil_public0001" data-page="1"`, ""},
		// no preview: just the caption
		{"::diagram{file=fil_nothumb001}", "<figcaption>Plain</figcaption>", "<img"},
		// bad page falls back to 1
		{"::diagram{file=fil_public0001 page=0}", `data-page="1"`, ""},
		{"::diagram{file=fil_public0001 page=abc}", `data-page="1"`, ""},
		{"::diagram{file=fil_public0001 page=99999}", `data-page="1"`, ""},
		// not public, unknown or malformed: nothing at all
		{"x\n\n::diagram{file=fil_private0001}\n\ny", "<p>y</p>", "diagram"},
		{"::diagram{}", "", "figure"},
		{"::diagram{file=../etc/passwd}", "", "figure"},
		{`::diagram{file="><script>alert(1)</script>}`, "", "script"},
		// only on a line of its own, and never inside code
		{"see ::diagram{file=fil_public0001} here", "", "<figure"},
		{"```\n::diagram{file=fil_public0001}\n```", "::diagram{file=fil_public0001}", "<figure"},
	}
	for _, c := range cases {
		got := r.Render(c.in)
		if c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("Render(%q) = %q, want %q", c.in, got, c.want)
		}
		if c.notWant != "" && strings.Contains(got, c.notWant) {
			t.Errorf("Render(%q) = %q, must not contain %q", c.in, got, c.notWant)
		}
	}

	// without a resolver nothing leaks either
	if got := New().Render("::diagram{file=fil_public0001}"); strings.Contains(got, "figure") || strings.Contains(got, "fil_public0001") {
		t.Errorf("no resolver: %q", got)
	}
	if !HasDiagramEmbed("a\n::diagram{file=x}") || HasDiagramEmbed("plain") {
		t.Error("HasDiagramEmbed")
	}
}

func TestDiagramEmbedPolicy(t *testing.T) {
	// the figure's attributes are allowed narrowly: raw HTML cannot widen them
	r := New()
	for in, notWant := range map[string]string{
		`<figure class="evil" onclick="x()" data-file="a b" data-page="x"><figcaption>c</figcaption></figure>`: "evil",
		`<figure class="diagram-embed" data-file="fil_abc123" style="position:fixed">z</figure>`:               "style",
		`<figure class="diagram-embed" data-file="javascript:alert(1)">z</figure>`:                             "javascript",
		`<figure class="diagram-embed" data-page="0">z</figure>`:                                               "data-page",
	} {
		if got := r.Render(in); strings.Contains(got, notWant) || strings.Contains(got, "onclick") {
			t.Errorf("Render(%q) = %q, must not contain %q", in, got, notWant)
		}
	}
	got := r.Render(`<figure class="diagram-embed" data-file="fil_abc123" data-page="3"><figcaption>c</figcaption></figure>`)
	if !strings.Contains(got, `class="diagram-embed" data-file="fil_abc123" data-page="3"`) {
		t.Errorf("hand-written figure with valid attributes = %q", got)
	}
}

func TestRenderLiveBlock(t *testing.T) {
	r := New()
	code := `<style>@keyframes s{to{opacity:0}}</style><div id="a">hi</div><script>document.getElementById("a").textContent=1&&2</script>`
	got := r.Render("before\n\n```html-live height=250\n" + code + "\n```\n\nafter")
	if !strings.Contains(got, `<pre><code class="language-html-live-250">`) {
		t.Fatalf("no live block: %q", got)
	}
	for _, bad := range []string{"<script", "<style", "<div id", "IBLIVE"} {
		if strings.Contains(got, bad) {
			t.Errorf("output holds %q: %s", bad, got)
		}
	}
	if !strings.Contains(got, "&lt;script&gt;") || !strings.Contains(got, "before") || !strings.Contains(got, "after") {
		t.Errorf("escaped source or prose missing: %s", got)
	}
	if !strings.Contains(r.Render("```html-live\nx\n```"), `language-html-live-400"`) {
		t.Error("default height")
	}
}

func TestRenderLiveInjection(t *testing.T) {
	r := New()
	cases := map[string]string{
		"close-code payload":   "```html-live\n</code></pre><script>alert(1)</script><pre><code>\n```",
		"height option":        "```html-live height=1 onload=alert(1)\n<b>x</b>\n```",
		"fence break":          "```html-live\nx\n``` <img src=x onerror=alert(1)>\n```",
		"forged class":         `<pre><code class="language-html-live-400">&lt;script&gt;alert(1)&lt;/script&gt;</code></pre>`,
		"forged class raw":     `<pre><code class="language-html-live-400"><script>alert(1)</script></code></pre>`,
		"forged via fence":     "```html-live-400\n<b>x</b>\n```",
		"forged token":         "IBLIVE00000000000000000000000000000000X0",
		"over limit":           strings.Repeat("```html-live\nx\n```\n\n", 12),
		"nested in blockquote": "> ```html-live\n> <script>alert(1)</script>\n> ```",
	}
	for name, in := range cases {
		got := r.Render(in)
		// text is fine (it is escaped); live elements and event-handler attributes are not
		if m := regexp.MustCompile(`<(script|img|style)|<[^>]* on\w+=`).FindString(got); m != "" {
			t.Errorf("%s: output holds %q: %s", name, m, got)
		}
	}
	// only generated blocks carry the live class: the forged and rejected ones do not
	for _, name := range []string{"forged class", "forged class raw", "forged via fence", "height option", "nested in blockquote"} {
		if strings.Contains(r.Render(cases[name]), `class="language-html-live-`) {
			t.Errorf("%s: forged a live block", name)
		}
	}
	if n := strings.Count(r.Render(cases["over limit"]), `class="language-html-live-`); n != 10 {
		t.Errorf("over limit: %d live blocks, want 10", n)
	}
	// a payload that ends the code element stays escaped text inside one live block
	got := r.Render(cases["close-code payload"])
	if strings.Count(got, "<pre>") != 1 || !strings.Contains(got, "&lt;/code&gt;&lt;/pre&gt;&lt;script&gt;") {
		t.Errorf("close-code payload: %s", got)
	}
}

func TestCodeTabs(t *testing.T) {
	r := New()
	two := "```go group=sort\na()\n```\n\n```python group=sort\nb()\n```\n"
	got := r.Render(two)
	for _, w := range []string{`<div class="code-tabs">`, `language-go`, `language-python`, "</div>"} {
		if !strings.Contains(got, w) {
			t.Errorf("group render = %q, want %q", got, w)
		}
	}
	if strings.Count(got, "<pre>") != 2 {
		t.Errorf("want two pre blocks: %q", got)
	}
	for name, in := range map[string]string{
		"single":      "```go group=sort\na()\n```\n",
		"split":       "```go group=sort\na()\n```\n\ntext\n\n```py group=sort\nb()\n```\n",
		"other group": "```go group=a\na()\n```\n\n```py group=b\nb()\n```\n",
		"no group":    "```go\na()\n```\n\n```py\nb()\n```\n",
	} {
		if g := r.Render(in); strings.Contains(g, "code-tabs") {
			t.Errorf("%s: must not group: %q", name, g)
		}
	}
	// a group name with markup is not a group, and nothing leaks into HTML
	bad := r.Render("```go group=\"><script>\na\n```\n\n```py group=\"><script>\nb\n```\n")
	if strings.Contains(bad, "<script") || strings.Contains(bad, "code-tabs") {
		t.Errorf("bad group: %q", bad)
	}

	var ct codeTabs
	if ct.Kind() != kindCodeTabs {
		t.Fatal("wrong kind")
	}
	ct.Dump([]byte("src"), 0)
}

func TestRenderMath(t *testing.T) {
	r := New()
	cases := []struct{ name, in, want string }{
		{"inline", "a $x^2$ b", `<span class="math-inline" data-tex="x^2">$x^2$</span>`},
		{"block", "$$\n\\int_0^1 x\\,dx\n$$", `<div class="math-block" data-tex="\int_0^1 x\,dx">`},
		{"one line block", "$$a<b$$", `data-tex="a&lt;b"`},
		{"money untouched", "costs $5 and $6 today", "costs $5 and $6 today"},
		{"code untouched", "`$x$`", "<code>$x$</code>"},
		{"fence untouched", "```\n$$\nx\n$$\n```", "$$\nx\n$$"},
		{"escaped dollar", `\$x\$`, "$x$"},
	}
	for _, c := range cases {
		if got := r.Render(c.in); !strings.Contains(got, c.want) {
			t.Errorf("%s: Render(%q) = %q, want it to contain %q", c.name, c.in, got, c.want)
		}
	}
	got := r.Render("$$<script>alert(1)</script>$$")
	if strings.Contains(got, "<script>") {
		t.Errorf("script leaked: %q", got)
	}
	if got := r.Render("`$x$` and $y$"); strings.Contains(got, `data-tex="x"`) {
		t.Errorf("math inside code: %q", got)
	}
}

func TestRenderExtras(t *testing.T) {
	r := New()
	cases := []struct{ name, in, want string }{
		{"footnote ref", "Text[^1]\n\n[^1]: Note.", `href="#fn:1"`},
		{"footnote id", "Text[^1]\n\n[^1]: Note.", `id="fn:1"`},
		{"footnote list", "Text[^1]\n\n[^1]: Note.", `class="footnotes"`},
		{"definition list", "Term\n: Meaning", "<dl>"},
		{"task list", "- [x] done", `type="checkbox"`},
		{"strikethrough", "~~x~~", "<del>x</del>"},
		{"table", "|a|b|\n|-|-|\n|1|2|", "<table>"},
	}
	for _, c := range cases {
		if got := r.Render(c.in); !strings.Contains(got, c.want) {
			t.Errorf("%s: Render(%q) = %q, want it to contain %q", c.name, c.in, got, c.want)
		}
	}
}

func TestRenderHighlightAndAlerts(t *testing.T) {
	r := New()
	cases := []struct{ name, in, want string }{
		{"mark", "a ==b== c", "<mark>b</mark>"},
		{"spaced equals", "a == b == c", "a == b == c"},
		{"mark in code", "`==x==`", "<code>==x==</code>"},
		{"alert", "> [!NOTE]\n> Careful here.", `<blockquote class="alert alert-note">`},
		{"alert body", "> [!WARNING]\n> Careful here.", "Careful here."},
		{"plain quote", "> hello", "<blockquote>"},
	}
	for _, c := range cases {
		if got := r.Render(c.in); !strings.Contains(got, c.want) {
			t.Errorf("%s: Render(%q) = %q, want it to contain %q", c.name, c.in, got, c.want)
		}
	}
	if got := r.Render("> [!NOTE]\n> x"); strings.Contains(got, "[!NOTE]") {
		t.Errorf("marker left in: %q", got)
	}
}

func TestRenderSupSub(t *testing.T) {
	r := New()
	cases := []struct{ name, in, want string }{
		{"sup", "O(n^2^)", "O(n<sup>2</sup>)"},
		{"sup words", "n^log n^", "n<sup>log n</sup>"},
		{"sub", "H~2~O", "H<sub>2</sub>O"},
		{"strike kept", "a ~~b~~ c", "<del>b</del>"},
		{"spaced caret", "a ^ b ^ c", "a ^ b ^ c"},
		{"spaced tilde", "a ~ b ~ c", "a ~ b ~ c"},
		{"lone caret", "2^10 bytes", "2^10 bytes"},
		{"in code", "`n^2^`", "<code>n^2^</code>"},
		{"both", "x~i~ + y^2^", "x<sub>i</sub> + y<sup>2</sup>"},
	}
	for _, c := range cases {
		if got := r.Render(c.in); !strings.Contains(got, c.want) {
			t.Errorf("%s: Render(%q) = %q, want it to contain %q", c.name, c.in, got, c.want)
		}
	}
}
