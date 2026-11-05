// Package markdown renders post bodies to safe HTML: GitHub-flavoured
// Markdown, sanitized, with YouTube and Vimeo links on their own line turned
// into embeds.
package markdown

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"html"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	mdhtml "github.com/yuin/goldmark/renderer/html"

	"github.com/iBlog/iblog-monolith-go/internal/domain/post"
)

type Renderer struct {
	md       goldmark.Markdown
	policy   *bluemonday.Policy
	diagrams atomic.Pointer[DiagramResolver]
}

// Diagram is a published diagram a post may embed.
type Diagram struct {
	FileID string
	Slug   string
	Title  string
	// ThumbnailHash is the preview's asset hash ("" for none).
	ThumbnailHash string
}

// DiagramResolver looks up embed references (diagram file ids or slugs) and
// returns, keyed by reference, the diagrams that are unlisted or public. A
// reference it leaves out renders nothing.
type DiagramResolver func(ctx context.Context, refs []string) map[string]Diagram

// SetDiagrams enables the ::diagram embed; without a resolver every embed
// renders nothing. It is safe to call while rendering.
func (r *Renderer) SetDiagrams(f DiagramResolver) { r.diagrams.Store(&f) }

// Where the client shows a diagram, and where its previews are served.
const (
	DiagramPagePath  = "/d/v/"
	DiagramAssetPath = "/api/diagrams/assets/"
)

// HasDiagramEmbed reports whether a body may contain a diagram embed. Its
// HTML depends on the diagram's visibility, so it must not be cached.
func HasDiagramEmbed(body string) bool { return strings.Contains(body, "::diagram{") }

func New() *Renderer {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		// Raw HTML passes through to the sanitizer, which has the last word.
		goldmark.WithRendererOptions(mdhtml.WithUnsafe()),
	)
	p := bluemonday.UGCPolicy()
	p.AllowAttrs("id").Matching(regexp.MustCompile(`^[a-z0-9-]+$`)).OnElements("h1", "h2", "h3", "h4", "h5", "h6")
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^language-[\w+-]+$`)).OnElements("code")
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^embed$`)).OnElements("div")
	// ::diagram embeds: <figure class="diagram-embed" data-file data-page>.
	p.AllowElements("figure", "figcaption")
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^diagram-embed$`)).OnElements("figure")
	// alt text is any plain text (the default pattern drops titles with
	// quotes or ampersands); the attribute value is escaped on output.
	p.AllowAttrs("alt").Matching(regexp.MustCompile(`^[^\x00-\x1f]{0,300}$`)).OnElements("img")
	p.AllowAttrs("loading").Matching(regexp.MustCompile(`^(lazy|eager)$`)).OnElements("img")
	p.AllowAttrs("data-file").Matching(diagramRef).OnElements("figure")
	p.AllowAttrs("data-page").Matching(regexp.MustCompile(`^[1-9]\d{0,2}$`)).OnElements("figure")
	p.AllowElements("iframe")
	p.AllowAttrs("src").Matching(embedSrc).OnElements("iframe")
	p.AllowAttrs("allowfullscreen", "loading", "title").OnElements("iframe")
	p.AddTargetBlankToFullyQualifiedLinks(true)
	return &Renderer{md: md, policy: p}
}

var (
	diagramRef = regexp.MustCompile(`^[A-Za-z0-9_-]{6,128}$`)
	// ::diagram{file=fil_x page=2} or ::diagram{slug=my-diagram-1a2b3c4d}
	diagramLine = regexp.MustCompile(`^::diagram\{([^{}]*)\}$`)
	diagramAttr = regexp.MustCompile(`(file|slug|page)=("[^"]*"|\S+)`)
	embedSrc    = regexp.MustCompile(`^https://(www\.youtube-nocookie\.com/embed/[\w-]{11}|player\.vimeo\.com/video/\d+)$`)
	youtube     = regexp.MustCompile(`^https?://(?:www\.)?(?:youtube\.com/watch\?v=|youtu\.be/)([\w-]{11})\S*$`)
	vimeo       = regexp.MustCompile(`^https?://(?:www\.)?vimeo\.com/(\d+)\S*$`)
)

// liveNonce makes the stand-ins for html-live blocks unguessable, so a body
// cannot forge one.
var (
	liveNonce = func() string {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		return "IBLIVE" + hex.EncodeToString(b) + "X"
	}()
	liveToken = regexp.MustCompile(liveNonce + `\d+`)
)

// Render returns sanitized HTML for a Markdown body.
//
// An html-live block (see post.LiveBlocks) never goes through Markdown or the
// sanitizer: a token stands in for it, and after sanitizing the token becomes
// a <pre><code class="language-html-live-N"> holding the escaped source. Any
// such class written by the author is renamed first, so this is the only way
// to get one.
func (r *Renderer) Render(body string) string {
	body, live := extractLive(body)
	var out bytes.Buffer
	if err := r.md.Convert([]byte(r.diagramEmbeds(embeds(body))), &out); err != nil {
		return ""
	}
	s := r.policy.Sanitize(out.String())
	s = strings.ReplaceAll(s, `class="language-html-live`, `class="language-html_live`)
	for i, b := range live {
		s = strings.Replace(s, "<p>"+liveNonce+strconv.Itoa(i)+"</p>", post.LiveHTML(b), 1)
	}
	if len(live) > 0 {
		s = liveToken.ReplaceAllString(s, "")
	}
	return s
}

// extractLive swaps each runnable html-live block for a token paragraph.
func extractLive(body string) (string, []post.LiveBlock) {
	blocks := post.LiveBlocks(body)
	if len(blocks) == 0 {
		return body, nil
	}
	lines := strings.Split(body, "\n")
	var out []string
	at := 0
	for i, b := range blocks {
		out = append(out, lines[at:b.Start]...)
		out = append(out, "", liveNonce+strconv.Itoa(i), "")
		at = b.End
	}
	return strings.Join(append(out, lines[at:]...), "\n"), blocks
}

// embeds replaces a line that is only a YouTube or Vimeo URL (outside code
// fences) with an iframe block.
func embeds(body string) string {
	lines := strings.Split(body, "\n")
	fenced := false
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		src := ""
		if m := youtube.FindStringSubmatch(t); m != nil {
			src = "https://www.youtube-nocookie.com/embed/" + m[1]
		} else if m := vimeo.FindStringSubmatch(t); m != nil {
			src = "https://player.vimeo.com/video/" + m[1]
		}
		if src != "" {
			lines[i] = "\n<div class=\"embed\"><iframe src=\"" + src + "\" loading=\"lazy\" allowfullscreen></iframe></div>\n"
		}
	}
	return strings.Join(lines, "\n")
}

// diagramEmbed is one ::diagram line of a body.
type diagramEmbed struct {
	line int
	ref  string
	page string
}

// diagramEmbeds replaces each line that is only a ::diagram{...} directive
// (outside code fences) with a figure for a published diagram, and with
// nothing for any other: a missing, private or unresolvable one shows no
// trace of itself.
func (r *Renderer) diagramEmbeds(body string) string {
	if !HasDiagramEmbed(body) {
		return body
	}
	lines := strings.Split(body, "\n")
	var found []diagramEmbed
	var refs []string
	fenced := false
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fenced = !fenced
			continue
		}
		m := diagramLine.FindStringSubmatch(t)
		if fenced || m == nil {
			continue
		}
		e := diagramEmbed{line: i, page: "1"}
		for _, a := range diagramAttr.FindAllStringSubmatch(m[1], -1) {
			v := strings.Trim(a[2], `"`)
			switch a[1] {
			case "file", "slug":
				if diagramRef.MatchString(v) {
					e.ref = v
				}
			case "page":
				if len(v) <= 3 && v != "0" && onlyDigits(v) {
					e.page = strings.TrimLeft(v, "0")
				}
			}
		}
		if e.ref != "" {
			refs = append(refs, e.ref)
		}
		found = append(found, e)
	}
	var resolved map[string]Diagram
	if f := r.diagrams.Load(); f != nil && len(refs) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		resolved = (*f)(ctx, refs)
	}
	for _, e := range found {
		lines[e.line] = ""
		if d, ok := resolved[e.ref]; ok && e.ref != "" {
			lines[e.line] = "\n" + figure(d, e.page) + "\n"
		}
	}
	return strings.Join(lines, "\n")
}

func onlyDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}

// figure is the HTML of an embed; the sanitizer has the last word.
func figure(d Diagram, page string) string {
	title := html.EscapeString(d.Title)
	var b strings.Builder
	b.WriteString(`<figure class="diagram-embed" data-file="` + html.EscapeString(d.FileID) + `" data-page="` + page + `">`)
	b.WriteString(`<a href="` + DiagramPagePath + html.EscapeString(d.Slug) + `">`)
	if d.ThumbnailHash != "" {
		b.WriteString(`<img src="` + DiagramAssetPath + html.EscapeString(d.ThumbnailHash) + `" alt="` + title + `" loading="lazy">`)
	}
	b.WriteString(`<figcaption>` + title + `</figcaption></a></figure>`)
	return b.String()
}
