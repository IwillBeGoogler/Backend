// Package svgsafe sanitizes SVG icons and diagram assets so they can be
// served from our origin. It is an allowlist: the document is parsed with
// encoding/xml, every element, attribute and value that is not known safe is
// dropped, and what remains is serialized again. Anything that could make the
// parser expand or fetch things (DOCTYPE, entity declarations, processing
// instructions) is rejected outright.
package svgsafe

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// Limits. Icons are small; anything bigger is refused rather than trimmed.
const (
	MaxBytes    = 512 << 10
	MaxDepth    = 32
	MaxElements = 5000
	MaxAttrs    = 64
	MaxAttrLen  = 64 << 10
	MaxText     = 8 << 10
	MaxUses     = 64
)

const (
	nsSVG   = "http://www.w3.org/2000/svg"
	nsXLink = "http://www.w3.org/1999/xlink"
	nsXML   = "http://www.w3.org/XML/1998/namespace"
)

// ErrUnsafe wraps every reason a document is refused.
var ErrUnsafe = errors.New("svg refused")

func refuse(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrUnsafe, fmt.Sprintf(format, a...))
}

var elements = set("svg", "g", "path", "circle", "ellipse", "rect", "line", "polyline", "polygon",
	"defs", "linearGradient", "radialGradient", "stop", "clipPath", "mask", "use", "title", "desc",
	"symbol", "text", "tspan")

// textElements may hold character data.
var textElements = set("text", "tspan", "title", "desc")

// hrefElements may carry a (local) href.
var hrefElements = set("use", "linearGradient", "radialGradient")

// styleProps are presentation properties: allowed as attributes and inside
// a style attribute.
var styleProps = set("fill", "fill-opacity", "fill-rule", "stroke", "stroke-width", "stroke-linecap",
	"stroke-linejoin", "stroke-miterlimit", "stroke-dasharray", "stroke-dashoffset", "stroke-opacity",
	"opacity", "clip-path", "clip-rule", "mask", "stop-color", "stop-opacity", "font-family", "font-size",
	"font-weight", "font-style", "text-anchor", "dominant-baseline", "letter-spacing", "display",
	"visibility", "color", "vector-effect", "mix-blend-mode", "transform")

var attrs = merge(styleProps, set("id", "class", "x", "y", "cx", "cy", "r", "rx", "ry", "x1", "y1", "x2", "y2",
	"width", "height", "d", "points", "viewBox", "preserveAspectRatio", "offset", "gradientUnits",
	"gradientTransform", "spreadMethod", "fx", "fy", "fr", "clipPathUnits", "maskUnits",
	"maskContentUnits", "dx", "dy", "style", "version", "aria-hidden", "focusable", "role",
	"refX", "refY", "textLength", "lengthAdjust"))

func set(v ...string) map[string]bool {
	m := make(map[string]bool, len(v))
	for _, s := range v {
		m[s] = true
	}
	return m
}

func merge(a, b map[string]bool) map[string]bool {
	for k := range a {
		b[k] = true
	}
	return b
}

var (
	localRef = regexp.MustCompile(`^#[A-Za-z_][A-Za-z0-9_.:-]*$`)
	urlCall  = regexp.MustCompile(`(?i)url\(`)
	urlLocal = regexp.MustCompile(`(?i)url\(\s*(?:#[A-Za-z_][A-Za-z0-9_.:-]*|'#[A-Za-z_][A-Za-z0-9_.:-]*'|"#[A-Za-z_][A-Za-z0-9_.:-]*")\s*\)`)
)

// Looks reports whether b appears to be an SVG document (so a refusal can
// say so instead of "unknown type").
func Looks(b []byte) bool {
	head := b
	if len(head) > 2048 {
		head = head[:2048]
	}
	return bytes.Contains(bytes.ToLower(head), []byte("<svg"))
}

type attr struct{ name, val string }

type node struct {
	name     string
	attrs    []attr
	children []*node
	text     string // character data; name == ""
}

// Sanitize returns a cleaned copy of an SVG document or an error wrapping
// ErrUnsafe when the document cannot be cleaned.
func Sanitize(in []byte) ([]byte, error) {
	if len(in) == 0 || len(in) > MaxBytes {
		return nil, refuse("size")
	}
	in = bytes.TrimPrefix(in, []byte("\xef\xbb\xbf"))
	dec := xml.NewDecoder(bytes.NewReader(in))
	dec.Strict = true

	var (
		root  *node
		stack []*node
		skip  int // depth inside a dropped element
		depth int
		count int
		uses  int
	)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, refuse("xml: %v", err)
		}
		switch t := tok.(type) {
		case xml.Directive:
			return nil, refuse("DOCTYPE or declaration")
		case xml.ProcInst:
			if t.Target != "xml" {
				return nil, refuse("processing instruction")
			}
		case xml.Comment:
		case xml.StartElement:
			depth++
			count++
			if depth > MaxDepth || count > MaxElements {
				return nil, refuse("too deep or too many elements")
			}
			if skip > 0 {
				skip++
				continue
			}
			if root != nil && len(stack) == 0 {
				return nil, refuse("more than one root")
			}
			n, ok := cleanElement(t, root == nil)
			if root == nil && !ok {
				return nil, refuse("root is not <svg>")
			}
			if !ok {
				skip = 1
				continue
			}
			if n.name == "use" {
				if uses++; uses > MaxUses {
					return nil, refuse("too many <use>")
				}
			}
			if root == nil {
				root = n
			} else {
				p := stack[len(stack)-1]
				p.children = append(p.children, n)
			}
			stack = append(stack, n)
		case xml.EndElement:
			depth--
			if skip > 0 {
				skip--
				continue
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if skip > 0 || len(stack) == 0 {
				if len(stack) == 0 && len(bytes.TrimSpace(t)) > 0 {
					return nil, refuse("text outside root")
				}
				continue
			}
			p := stack[len(stack)-1]
			if textElements[p.name] && len(t) <= MaxText {
				p.children = append(p.children, &node{text: string(t)})
			}
		}
	}
	if root == nil || len(stack) != 0 {
		return nil, refuse("no <svg> root")
	}
	if err := checkUses(root); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	write(&out, root, true)
	return out.Bytes(), nil
}

// cleanElement filters one start tag; ok is false when the element must go
// (with its subtree).
func cleanElement(t xml.StartElement, isRoot bool) (*node, bool) {
	// Token resolves prefixes: Space is the namespace URL.
	if t.Name.Space != nsSVG || !elements[t.Name.Local] {
		return nil, false
	}
	if isRoot && t.Name.Local != "svg" {
		return nil, false
	}
	n := &node{name: t.Name.Local}
	for _, a := range t.Attr {
		if len(n.attrs) >= MaxAttrs {
			break
		}
		if len(a.Value) > MaxAttrLen {
			continue
		}
		name, val, ok := cleanAttr(n.name, a)
		if ok {
			n.attrs = append(n.attrs, attr{name, val})
		}
	}
	return n, true
}

func cleanAttr(el string, a xml.Attr) (name, val string, ok bool) {
	space, local := a.Name.Space, a.Name.Local
	switch {
	case space == "" && local == "xmlns", space == "xmlns":
		return
	case space == nsXML && local == "space":
		if a.Value == "preserve" || a.Value == "default" {
			return "xml:space", a.Value, true
		}
		return
	case space == nsXLink && local == "href", space == "" && local == "href":
		if hrefElements[el] && localRef.MatchString(a.Value) {
			if space == nsXLink {
				return "xlink:href", a.Value, true
			}
			return "href", a.Value, true
		}
		return
	case space != "":
		return
	case strings.HasPrefix(strings.ToLower(local), "on"):
		return
	case !attrs[local]:
		return
	}
	if local == "style" {
		v, ok := cleanStyle(a.Value)
		return local, v, ok && v != ""
	}
	if !safeValue(a.Value) {
		return
	}
	return local, a.Value, true
}

func cleanStyle(s string) (string, bool) {
	if strings.Contains(s, "\\") || strings.Contains(s, "/*") {
		return "", false
	}
	var out []string
	for _, decl := range strings.Split(s, ";") {
		prop, v, found := strings.Cut(decl, ":")
		prop = strings.ToLower(strings.TrimSpace(prop))
		if !found || !styleProps[prop] || !safeValue(v) {
			continue
		}
		out = append(out, prop+":"+strings.TrimSpace(v))
	}
	return strings.Join(out, ";"), true
}

// safeValue accepts a property value with no script, data or external url.
func safeValue(v string) bool {
	l := strings.ToLower(v)
	for _, bad := range []string{"javascript:", "vbscript:", "data:", "expression(", "@import", "\\", "<", "behavior", "binding", "image(", "image-set("} {
		if strings.Contains(l, bad) {
			return false
		}
	}
	if urlCall.MatchString(v) {
		return len(urlCall.FindAllString(v, -1)) == len(urlLocal.FindAllString(v, -1))
	}
	return true
}

// checkUses refuses a <use> whose target contains another <use>, so
// references cannot nest into an exponential expansion.
func checkUses(root *node) error {
	byID := map[string]*node{}
	var walk func(n *node)
	walk = func(n *node) {
		for _, a := range n.attrs {
			if a.name == "id" {
				if _, dup := byID[a.val]; !dup {
					byID[a.val] = n
				}
			}
		}
		for _, c := range n.children {
			walk(c)
		}
	}
	walk(root)
	var hasUse func(n *node) bool
	hasUse = func(n *node) bool {
		if n.name == "use" {
			return true
		}
		for _, c := range n.children {
			if hasUse(c) {
				return true
			}
		}
		return false
	}
	var err error
	var check func(n *node)
	check = func(n *node) {
		if n.name == "use" && err == nil {
			for _, a := range n.attrs {
				if a.name == "href" || a.name == "xlink:href" {
					if t := byID[strings.TrimPrefix(a.val, "#")]; t != nil && hasUse(t) {
						err = refuse("nested <use>")
					}
				}
			}
		}
		for _, c := range n.children {
			check(c)
		}
	}
	check(root)
	return err
}

func write(b *bytes.Buffer, n *node, root bool) {
	if n.name == "" {
		xml.EscapeText(b, []byte(n.text))
		return
	}
	b.WriteByte('<')
	b.WriteString(n.name)
	if root {
		b.WriteString(` xmlns="` + nsSVG + `" xmlns:xlink="` + nsXLink + `"`)
	}
	for _, a := range n.attrs {
		b.WriteByte(' ')
		b.WriteString(a.name)
		b.WriteString(`="`)
		xml.EscapeText(b, []byte(a.val))
		b.WriteByte('"')
	}
	if len(n.children) == 0 {
		b.WriteString("/>")
		return
	}
	b.WriteByte('>')
	for _, c := range n.children {
		write(b, c, false)
	}
	b.WriteString("</" + n.name + ">")
}
