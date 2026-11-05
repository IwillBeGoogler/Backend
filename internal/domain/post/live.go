package post

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
)

// Live HTML blocks: a top-level fenced block whose language is html-live,
//
//	```html-live height=400
//	<div>...</div>
//	```
//
// is author-written HTML/CSS/JS that clients run in a sandboxed frame. The
// body stays plain text everywhere else.
const (
	LiveLang          = "html-live"
	MaxLiveBytes      = 64 << 10
	MaxLiveBlocks     = 10
	DefaultLiveHeight = 400
	MinLiveHeight     = 100
	MaxLiveHeight     = 1000

	// LiveClassPrefix starts the class of a rendered block's <code>; the height follows.
	LiveClassPrefix = "language-html-live-"
	// LiveNote is what output that cannot run the block shows instead.
	LiveNote = "Interactive content: open on site"
)

// LiveBlock is one html-live block of a body. Lines [Start, End) of the body
// (split on "\n") hold it, fences included.
type LiveBlock struct {
	Start, End int
	Code       string
	Height     int
}

var (
	fenceOpen   = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})(.*)$")
	fenceClose  = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})[ \t]*$")
	liveHeight  = regexp.MustCompile(`^height=(\d{1,5})$`)
	liveHTMLRe  = regexp.MustCompile(`(?s)<pre><code class="` + LiveClassPrefix + `\d{3,4}">.*?</code></pre>`)
	errLiveInfo = domain.Invalid("html-live: the only option is height=N (" + strconv.Itoa(MinLiveHeight) + "-" + strconv.Itoa(MaxLiveHeight) + ")")
)

// scanLive finds the top-level html-live blocks. A fence with an option other
// than height=N is not a block; the first such problem is returned.
func scanLive(body string) (blocks []LiveBlock, problem error) {
	lines := strings.Split(body, "\n")
	var (
		in     bool
		ch     byte
		n      int
		live   bool
		cur    LiveBlock
		code   []string
		finish = func(end int) {
			if live {
				cur.End, cur.Code = end, strings.Join(code, "\n")
				blocks = append(blocks, cur)
			}
			in, live, code = false, false, nil
		}
	)
	for i, raw := range lines {
		line := strings.TrimSuffix(raw, "\r")
		if !in {
			m := fenceOpen.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			info := strings.TrimSpace(m[2])
			if m[1][0] == '`' && strings.Contains(info, "`") {
				continue
			}
			in, ch, n = true, m[1][0], len(m[1])
			f := strings.Fields(info)
			if len(f) == 0 || f[0] != LiveLang {
				continue
			}
			h := DefaultLiveHeight
			switch {
			case len(f) == 1:
			case len(f) == 2 && liveHeight.MatchString(f[1]):
				v, _ := strconv.Atoi(liveHeight.FindStringSubmatch(f[1])[1])
				h = min(max(v, MinLiveHeight), MaxLiveHeight)
			default:
				if problem == nil {
					problem = errLiveInfo
				}
				continue
			}
			live, cur = true, LiveBlock{Start: i, Height: h}
			continue
		}
		if m := fenceClose.FindStringSubmatch(line); m != nil && m[1][0] == ch && len(m[1]) >= n {
			finish(i + 1)
		} else if live {
			code = append(code, line)
		}
	}
	if in {
		finish(len(lines))
	}
	return blocks, problem
}

// ValidateLive checks the html-live blocks of a body: well-formed options, at
// most MaxLiveBlocks, each at most MaxLiveBytes.
func ValidateLive(body string) error {
	if !strings.Contains(body, LiveLang) {
		return nil
	}
	blocks, problem := scanLive(body)
	if problem != nil {
		return problem
	}
	if len(blocks) > MaxLiveBlocks {
		return domain.Invalid(fmt.Sprintf("html-live: at most %d blocks per story", MaxLiveBlocks))
	}
	for i, b := range blocks {
		if len(b.Code) > MaxLiveBytes {
			return domain.Invalid(fmt.Sprintf("html-live: block %d is over %d KB", i+1, MaxLiveBytes>>10))
		}
	}
	return nil
}

// LiveBlocks returns the blocks a renderer runs: valid ones within the
// limits. Anything else stays an ordinary code block.
func LiveBlocks(body string) []LiveBlock {
	if !strings.Contains(body, LiveLang) {
		return nil
	}
	blocks, _ := scanLive(body)
	out := blocks[:0]
	for _, b := range blocks {
		if len(b.Code) <= MaxLiveBytes && len(out) < MaxLiveBlocks {
			out = append(out, b)
		}
	}
	return out
}

// StripLive removes the html-live blocks from a body, for outputs that only
// read prose (excerpts, reading time, mentions).
func StripLive(body string) string {
	blocks := LiveBlocks(body)
	if len(blocks) == 0 {
		return body
	}
	lines := strings.Split(body, "\n")
	var out []string
	at := 0
	for _, b := range blocks {
		out = append(out, lines[at:b.Start]...)
		out = append(out, "")
		at = b.End
	}
	return strings.Join(append(out, lines[at:]...), "\n")
}

// LiveHTML is the rendered form of a block: inert, escaped text that clients
// turn into a sandboxed frame.
func LiveHTML(b LiveBlock) string {
	return `<pre><code class="` + LiveClassPrefix + strconv.Itoa(b.Height) + `">` + html.EscapeString(b.Code) + `</code></pre>`
}

// LiveToLink replaces each rendered block in HTML with a plain link to href.
func LiveToLink(htmlText, href string) string {
	return liveHTMLRe.ReplaceAllString(htmlText, `<p><a href="`+html.EscapeString(href)+`">`+LiveNote+`</a></p>`)
}
