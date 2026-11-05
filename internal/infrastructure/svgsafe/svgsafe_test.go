package svgsafe

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

const open = `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 24 24">`

func TestSanitizeKeepsIcon(t *testing.T) {
	in := open + `<defs><linearGradient id="g"><stop offset="0" stop-color="#fff"/></linearGradient></defs>` +
		`<g fill="url(#g)" style="stroke:#000;stroke-width:2"><path d="M0 0L5 5"/><circle cx="1" cy="1" r="1"/>` +
		`<use xlink:href="#p"/></g><text x="1">a &amp; b<tspan>c</tspan></text><title>T</title></svg>`
	out, err := Sanitize([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`<path d="M0 0L5 5"/>`, `fill="url(#g)"`, `style="stroke:#000;stroke-width:2"`,
		`xlink:href="#p"`, `a &amp; b`, `<tspan>c</tspan>`, `<title>T</title>`, `viewBox="0 0 24 24"`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
	if again, err := Sanitize(out); err != nil || string(again) != string(out) {
		t.Errorf("not idempotent: %v\n%s\n%s", err, out, again)
	}
}

var forbidden = regexp.MustCompile(`(?i)<script|foreignobject|onload|onclick|onerror|javascript|data:|http|<iframe|<image|<style|<animate|<set|expression|@import|<!`)

func TestSanitizeStripsPayloads(t *testing.T) {
	cases := map[string]string{
		"script element":     open + `<script>alert(1)</script><path d="M0 0"/></svg>`,
		"script in g":        open + `<g><script>alert(1)</script></g></svg>`,
		"foreignObject":      open + `<foreignObject><body xmlns="http://www.w3.org/1999/xhtml" onload="alert(1)"/></foreignObject></svg>`,
		"onload root":        `<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`,
		"onclick":            open + `<rect onclick="alert(1)" width="1"/></svg>`,
		"ONERROR case":       open + `<rect ONERROR="alert(1)" width="1"/></svg>`,
		"onmouseover":        open + `<circle onmouseover="alert(1)" r="1"/></svg>`,
		"href javascript":    open + `<use href="javascript:alert(1)"/></svg>`,
		"xlink javascript":   open + `<use xlink:href="javascript:alert(1)"/></svg>`,
		"href external":      open + `<use href="http://evil.test/x.svg#a"/></svg>`,
		"href data":          open + `<use href="data:image/svg+xml;base64,PHN2Zz4="/></svg>`,
		"a element":          open + `<a href="javascript:alert(1)"><text>x</text></a></svg>`,
		"image element":      open + `<image href="http://evil.test/a.png"/></svg>`,
		"style element":      open + `<style>@import url(http://evil.test/a.css);</style></svg>`,
		"animate":            open + `<a><animate attributeName="href" values="javascript:alert(1)"/></a></svg>`,
		"set":                open + `<set attributeName="onload" to="alert(1)"/></svg>`,
		"iframe xhtml":       open + `<iframe xmlns="http://www.w3.org/1999/xhtml" src="javascript:alert(1)"/></svg>`,
		"style url external": open + `<rect style="fill:url(http://evil.test/x)" width="1"/></svg>`,
		"style js":           open + `<rect style="fill:javascript:alert(1)" width="1"/></svg>`,
		"style expression":   open + `<rect style="width:expression(alert(1))"/></svg>`,
		"style import":       open + `<rect style="@import 'http://evil.test'"/></svg>`,
		"style escape":       open + `<rect style="fill:u\\72l(http://evil.test)"/></svg>`,
		"fill url external":  open + `<rect fill="url(https://evil.test/x#a)"/></svg>`,
		"fill url data":      open + `<rect fill="url(data:image/svg+xml,x)"/></svg>`,
		"clip-path external": open + `<rect clip-path="url(//evil.test/x#a)"/></svg>`,
		"fill javascript":    open + `<rect fill="javascript:alert(1)"/></svg>`,
		"unknown prefix":     open + `<x:script xmlns:x="http://www.w3.org/2000/svg">alert(1)</x:script></svg>`,
		"xhtml script":       open + `<script xmlns="http://www.w3.org/1999/xhtml">alert(1)</script></svg>`,
		"text cdata":         open + `<text><![CDATA[<script>alert(1)</script>]]></text></svg>`,
		"xlink on path":      open + `<path xlink:href="http://evil.test" d="M0 0"/></svg>`,
		"xml-stylesheet pi":  `<?xml-stylesheet href="http://evil.test/a.css"?>` + open + `</svg>`,
	}
	for name, in := range cases {
		out, err := Sanitize([]byte(in))
		if err != nil {
			if !errors.Is(err, ErrUnsafe) {
				t.Errorf("%s: err not ErrUnsafe: %v", name, err)
			}
			continue
		}
		s := strings.ReplaceAll(string(out), `xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink"`, "")
		if m := forbidden.FindString(s); m != "" {
			t.Errorf("%s: %q survived: %s", name, m, out)
		}
	}
}

func TestSanitizeRefuses(t *testing.T) {
	cases := map[string]string{
		"empty":            ``,
		"not xml":          `hello`,
		"html":             `<html><script>alert(1)</script></html>`,
		"no namespace":     `<svg><path d="M0 0"/></svg>`,
		"wrong namespace":  `<svg xmlns="http://www.w3.org/1999/xhtml"/>`,
		"doctype":          `<!DOCTYPE svg><svg xmlns="http://www.w3.org/2000/svg"/>`,
		"external dtd":     `<!DOCTYPE svg PUBLIC "-//W3C//DTD SVG 1.1//EN" "http://evil.test/x.dtd"><svg xmlns="http://www.w3.org/2000/svg"/>`,
		"xxe":              `<!DOCTYPE svg [<!ENTITY x SYSTEM "file:///etc/passwd">]><svg xmlns="http://www.w3.org/2000/svg"><text>&x;</text></svg>`,
		"billion laughs":   `<!DOCTYPE lolz [<!ENTITY lol "lol"><!ENTITY lol2 "&lol;&lol;&lol;&lol;">]><svg xmlns="http://www.w3.org/2000/svg"><text>&lol2;</text></svg>`,
		"undefined entity": open + `<text>&nbsp;</text></svg>`,
		"two roots":        `<svg xmlns="http://www.w3.org/2000/svg"/><svg xmlns="http://www.w3.org/2000/svg"/>`,
		"text after root":  `<svg xmlns="http://www.w3.org/2000/svg"/>junk`,
		"unclosed":         open + `<g>`,
		"mismatched":       open + `<g></path></svg>`,
		"non-utf8 charset": `<?xml version="1.0" encoding="UTF-16"?>` + open + `</svg>`,
		"nested use":       open + `<g id="a"><use href="#b"/></g><g id="b"><use href="#a"/></g><use href="#a"/></svg>`,
		"deep":             open + strings.Repeat("<g>", MaxDepth+1) + strings.Repeat("</g>", MaxDepth+1) + `</svg>`,
		"many elements":    open + strings.Repeat("<g/>", MaxElements+1) + `</svg>`,
		"many uses":        open + `<path id="p"/>` + strings.Repeat(`<use href="#p"/>`, MaxUses+1) + `</svg>`,
		"too big":          open + strings.Repeat(" ", MaxBytes) + `</svg>`,
	}
	for name, in := range cases {
		out, err := Sanitize([]byte(in))
		if !errors.Is(err, ErrUnsafe) {
			t.Errorf("%s: err = %v, out = %s", name, err, out)
		}
	}
}

func TestSanitizeDropsOnlyBadParts(t *testing.T) {
	out, err := Sanitize([]byte(open + `<g><script>x</script><path d="M1 1" onclick="x"/></g><rect fill="url(#a)" width="3"/></svg>`))
	if err != nil {
		t.Fatal(err)
	}
	want := `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 24 24"><g><path d="M1 1"/></g><rect fill="url(#a)" width="3"/></svg>`
	if string(out) != want {
		t.Errorf("got  %s\nwant %s", out, want)
	}
}

func TestAttributeEscapingCannotBreakOut(t *testing.T) {
	out, err := Sanitize([]byte(open + `<path id="a&quot;&gt;&lt;script&gt;alert(1)&lt;/script&gt;" d="M0 0"/></svg>`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "<script") {
		t.Errorf("broke out: %s", out)
	}
}

func TestLooks(t *testing.T) {
	if !Looks([]byte("<?xml version='1.0'?>\n<SVG/>")) || Looks([]byte("\x89PNG....")) || Looks([]byte("<html/>")) {
		t.Error("Looks")
	}
}

func FuzzSanitize(f *testing.F) {
	f.Add([]byte(open + `<path d="M0 0"/></svg>`))
	f.Add([]byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="x"><script/></svg>`))
	f.Fuzz(func(t *testing.T, in []byte) {
		out, err := Sanitize(in)
		if err != nil {
			return
		}
		if again, err := Sanitize(out); err != nil || string(again) != string(out) {
			t.Fatalf("not stable: %v\n%s\n%s", err, out, again)
		}
		l := strings.ToLower(string(out))
		for _, bad := range []string{"<script", "onload", "javascript:", "<foreignobject"} {
			if strings.Contains(l, bad) {
				t.Fatalf("%s in %s", bad, out)
			}
		}
	})
}
