package svgsafe

import (
	"strings"
	"testing"
)

func TestCovLooksTruncates(t *testing.T) {
	b := []byte(strings.Repeat(" ", 3000) + "<svg")
	if Looks(b) {
		t.Fatal("svg beyond 2048 bytes must not match")
	}
	if !Looks([]byte("<SVG")) {
		t.Fatal("want match")
	}
}

func TestCovSanitizeNoRoot(t *testing.T) {
	if _, err := Sanitize([]byte(`<!-- c -->`)); err == nil {
		t.Fatal("want refusal")
	}
}

func TestCovSanitizeNonSVGRootNS(t *testing.T) {
	if _, err := Sanitize([]byte(`<svg xmlns="http://example.com"/>`)); err == nil {
		t.Fatal("want refusal")
	}
	// svg-namespace element that is allowed but not svg, as root
	if _, err := Sanitize([]byte(`<g xmlns="http://www.w3.org/2000/svg"/>`)); err == nil {
		t.Fatal("want refusal for <g> root")
	}
}

func TestCovAttrBranches(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(open)
	sb.WriteString(`<rect xml:space="preserve" ` + `big="` + `" width="` + strings.Repeat("1", MaxAttrLen+1) + `" height="2"/>`)
	sb.WriteString(`<rect xml:space="bogus" xml:lang="en" foo:bar="1" xmlns:foo="urn:x" onclick="x" onload="y" unknown="1" style="/* c */fill:red"/>`)
	sb.WriteString(`<rect style="fill:red;bogus:1;nocolon"/>`)
	sb.WriteString(`<rect style="bogus:1"/>`)
	sb.WriteString(`</svg>`)
	out, err := Sanitize([]byte(sb.String()))
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, bad := range []string{"onclick", "xml:lang", "foo:bar", "unknown", "bogus", "/*"} {
		if strings.Contains(s, bad) {
			t.Errorf("%s survived: %s", bad, s)
		}
	}
	if !strings.Contains(s, `xml:space="preserve"`) || !strings.Contains(s, `height="2"`) {
		t.Errorf("missing kept attrs: %s", s)
	}
	if strings.Contains(s, "width=") {
		t.Errorf("oversize attr kept")
	}
}

func TestCovMaxAttrs(t *testing.T) {
	in := open[:len(open)-1] + strings.Repeat(` x="1"`, 3) + `>` + `<rect ` + strings.Repeat(`x="1" `, MaxAttrs+5) + `/></svg>`
	out, err := Sanitize([]byte(in))
	if err != nil {
		t.Skipf("parser rejects duplicate attrs: %v", err)
	}
	if strings.Count(string(out), `x="1"`) > MaxAttrs+3 {
		t.Fatal("attrs not capped")
	}
}

func TestCovHrefNotAllowedElementOrValue(t *testing.T) {
	in := open + `<rect href="#a"/><use href="#a"/><use href="http://x/y"/><use xlink:href="#b"/></svg>`
	out, err := Sanitize([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "http://x") || strings.Contains(s, `<rect href`) {
		t.Fatal(s)
	}
	if !strings.Contains(s, `<use href="#a"/>`) || !strings.Contains(s, `xlink:href="#b"`) {
		t.Fatal(s)
	}
}

func TestCovUseNonNestingTarget(t *testing.T) {
	in := open + `<g id="a"><rect/></g><use href="#a"/></svg>`
	if _, err := Sanitize([]byte(in)); err != nil {
		t.Fatal(err)
	}
	in = open + `<g id="a"><use href="#b"/></g><use href="#a"/></svg>`
	if _, err := Sanitize([]byte(in)); err == nil {
		t.Fatal("want nested refusal")
	}
}
