package markdown

import (
	"crypto/rand"
	"encoding/hex"
	"html"
	"regexp"
	"strconv"
	"strings"
)

// Math is written as $…$ (inline) or $$…$$ (block, on lines of its own) and
// leaves the renderer as an empty element holding its TeX, which the client
// typesets:
//
//	<span class="math-inline" data-tex="…">$…$</span>
//	<div class="math-block" data-tex="…">$$…$$</div>
//
// The TeX never goes through Markdown or the sanitizer. A nonce token stands
// in for each formula and is swapped for escaped markup afterwards, so a body
// cannot inject markup through it. Code spans and fences are left alone.
const (
	maxMathLen   = 10000
	maxMathCount = 500
)

var (
	mathNonce = func() string {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		return "IBMATH" + hex.EncodeToString(b) + "X"
	}()
	mathToken  = regexp.MustCompile(mathNonce + `([BI])(\d+)Y`)
	mathInline = regexp.MustCompile(`\$([^\s$]|[^\s$][^$\n]*[^\s$])\$`)
	codeSpan   = regexp.MustCompile("`[^`\n]*`")
)

type formula struct {
	tex   string
	block bool
}

func isFence(t string) bool { return strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") }

// extractMath swaps each formula in body for a token.
func extractMath(body string) (string, []formula) {
	if !strings.Contains(body, "$") {
		return body, nil
	}
	var found []formula
	add := func(tex string, block bool) (string, bool) {
		tex = strings.TrimSpace(tex)
		if tex == "" || len(tex) > maxMathLen || len(found) >= maxMathCount {
			return "", false
		}
		found = append(found, formula{tex, block})
		kind := "I"
		if block {
			kind = "B"
		}
		return mathNonce + kind + strconv.Itoa(len(found)-1) + "Y", true
	}
	lines := strings.Split(body, "\n")
	var out []string
	fenced := false
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		t := strings.TrimSpace(line)
		if isFence(t) {
			fenced = !fenced
			out = append(out, line)
			continue
		}
		if fenced {
			out = append(out, line)
			continue
		}
		if strings.HasPrefix(t, "$$") {
			if len(t) > 4 && strings.HasSuffix(t, "$$") {
				if tok, ok := add(t[2:len(t)-2], true); ok {
					out = append(out, "", tok, "")
					continue
				}
			} else if t == "$$" {
				end := -1
				for j := i + 1; j < len(lines) && j <= i+400; j++ {
					if strings.TrimSpace(lines[j]) == "$$" {
						end = j
						break
					}
					if isFence(strings.TrimSpace(lines[j])) {
						break
					}
				}
				if end > 0 {
					if tok, ok := add(strings.Join(lines[i+1:end], "\n"), true); ok {
						out = append(out, "", tok, "")
						i = end
						continue
					}
				}
			}
		}
		out = append(out, inlineMath(line, add))
	}
	return strings.Join(out, "\n"), found
}

func inlineMath(line string, add func(string, bool) (string, bool)) string {
	if !strings.Contains(line, "$") {
		return line
	}
	var b strings.Builder
	at := 0
	segment := func(s string) {
		last := 0
		for _, m := range mathInline.FindAllStringSubmatchIndex(s, -1) {
			if m[0] > 0 && s[m[0]-1] == '\\' {
				continue
			}
			if m[1] < len(s) && s[m[1]] >= '0' && s[m[1]] <= '9' {
				continue
			}
			if m[0] < last {
				continue
			}
			tok, ok := add(s[m[2]:m[3]], false)
			if !ok {
				continue
			}
			b.WriteString(s[last:m[0]])
			b.WriteString(tok)
			last = m[1]
		}
		b.WriteString(s[last:])
	}
	for _, c := range codeSpan.FindAllStringIndex(line, -1) {
		segment(line[at:c[0]])
		b.WriteString(line[c[0]:c[1]])
		at = c[1]
	}
	segment(line[at:])
	return b.String()
}

// restoreMath turns tokens in sanitized HTML back into math elements. A block
// formula that stands alone in its paragraph becomes a div; anywhere else
// (a list item, say) it becomes a span with the same class. Tokens Markdown
// altered are dropped.
func restoreMath(s string, found []formula) string {
	if len(found) == 0 {
		return s
	}
	el := func(tok, tag string) string {
		m := mathToken.FindStringSubmatch(tok)
		i, err := strconv.Atoi(m[2])
		if err != nil || i >= len(found) {
			return ""
		}
		f := found[i]
		tex := html.EscapeString(f.tex)
		if f.block {
			return "<" + tag + ` class="math-block" data-tex="` + tex + `">$$` + tex + `$$</` + tag + ">"
		}
		return `<span class="math-inline" data-tex="` + tex + `">$` + tex + `$</span>`
	}
	s = alone.ReplaceAllStringFunc(s, func(m string) string {
		return el(strings.TrimSuffix(strings.TrimPrefix(m, "<p>"), "</p>"), "div")
	})
	return mathToken.ReplaceAllStringFunc(s, func(tok string) string { return el(tok, "span") })
}

var alone = regexp.MustCompile(`<p>` + mathNonce + `B\d+Y</p>`)
