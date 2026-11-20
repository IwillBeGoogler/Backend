package markdown

import (
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// ==highlighted text== renders as <mark>.

var kindMark = ast.NewNodeKind("Mark")

type markNode struct{ ast.BaseInline }

func (*markNode) Kind() ast.NodeKind           { return kindMark }
func (n *markNode) Dump(src []byte, level int) { ast.DumpHelper(n, src, level, nil, nil) }

type markDelimiters struct{}

func (markDelimiters) IsDelimiter(b byte) bool { return b == '=' }
func (markDelimiters) CanOpenCloser(opener, closer *parser.Delimiter) bool {
	return opener.Char == closer.Char
}
func (markDelimiters) OnMatch(int) ast.Node { return &markNode{} }

type markParser struct{}

func (markParser) Trigger() []byte { return []byte{'='} }

func (markParser) Parse(_ ast.Node, block text.Reader, pc parser.Context) ast.Node {
	before := block.PrecendingCharacter()
	line, segment := block.PeekLine()
	node := parser.ScanDelimiter(line, before, 2, markDelimiters{})
	if node == nil || node.OriginalLength != 2 || before == '=' {
		return nil
	}
	node.Segment = segment.WithStop(segment.Start + node.OriginalLength)
	block.Advance(node.OriginalLength)
	pc.PushDelimiter(node)
	return node
}

func (markParser) CloseBlock(ast.Node, parser.Context) {}

type markRenderer struct{}

func (markRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindMark, func(w util.BufWriter, _ []byte, _ ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			_, _ = w.WriteString("<mark>")
		} else {
			_, _ = w.WriteString("</mark>")
		}
		return ast.WalkContinue, nil
	})
}

type highlight struct{}

func (highlight) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(parser.WithInlineParsers(util.Prioritized(markParser{}, 450)))
	m.Renderer().AddOptions(renderer.WithNodeRenderers(util.Prioritized(markRenderer{}, 450)))
}

// GitHub-style alerts: a blockquote whose first line is [!NOTE], [!TIP],
// [!IMPORTANT], [!WARNING] or [!CAUTION] becomes
// <blockquote class="alert alert-note"> without the marker line; the client
// styles and titles it.

var alertMarker = regexp.MustCompile(`^\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]$`)

type alertTransformer struct{}

func (alertTransformer) Transform(doc *ast.Document, reader text.Reader, _ parser.Context) {
	src := reader.Source()
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		bq, ok := n.(*ast.Blockquote)
		if !entering || !ok {
			return ast.WalkContinue, nil
		}
		para, ok := bq.FirstChild().(*ast.Paragraph)
		if !ok {
			return ast.WalkContinue, nil
		}
		var line []ast.Node
		var raw strings.Builder
		for c := para.FirstChild(); c != nil; c = c.NextSibling() {
			t, ok := c.(*ast.Text)
			if !ok {
				return ast.WalkContinue, nil
			}
			raw.Write(t.Segment.Value(src))
			line = append(line, c)
			if t.SoftLineBreak() || t.HardLineBreak() {
				break
			}
		}
		m := alertMarker.FindStringSubmatch(strings.TrimSpace(raw.String()))
		if m == nil {
			return ast.WalkContinue, nil
		}
		for _, c := range line {
			para.RemoveChild(para, c)
		}
		if para.FirstChild() == nil {
			bq.RemoveChild(bq, para)
		}
		bq.SetAttributeString("class", []byte("alert alert-"+strings.ToLower(m[1])))
		return ast.WalkContinue, nil
	})
}

// ^superscript^ and ~subscript~ (Pandoc style): O(n^2^), H~2~O. One delimiter
// character each side, no spaces inside; ~~text~~ stays strikethrough.

var (
	kindSup = ast.NewNodeKind("Sup")
	kindSub = ast.NewNodeKind("Sub")
)

type scriptNode struct {
	ast.BaseInline
	kind ast.NodeKind
}

func (n *scriptNode) Kind() ast.NodeKind         { return n.kind }
func (n *scriptNode) Dump(src []byte, level int) { ast.DumpHelper(n, src, level, nil, nil) }

type scriptDelimiters struct{ char byte }

func (d scriptDelimiters) IsDelimiter(b byte) bool { return b == d.char }
func (scriptDelimiters) CanOpenCloser(opener, closer *parser.Delimiter) bool {
	return opener.Char == closer.Char
}
func (d scriptDelimiters) OnMatch(int) ast.Node {
	if d.char == '^' {
		return &scriptNode{kind: kindSup}
	}
	return &scriptNode{kind: kindSub}
}

type scriptParser struct{ char byte }

func (p scriptParser) Trigger() []byte { return []byte{p.char} }

func (p scriptParser) Parse(_ ast.Node, block text.Reader, pc parser.Context) ast.Node {
	before := block.PrecendingCharacter()
	line, segment := block.PeekLine()
	if len(line) == 0 || (len(line) > 1 && line[1] == p.char) || before == rune(p.char) {
		return nil // ~~strike~~ (or ^^) is left to the other parsers
	}
	node := parser.ScanDelimiter(line, before, 1, scriptDelimiters{char: p.char})
	if node == nil || node.OriginalLength != 1 {
		return nil
	}
	// no spaces inside: `a ^ b` and `x ~ y` stay text
	if node.CanOpen && len(line) > 1 && (line[1] == ' ' || line[1] == '\t') {
		return nil
	}
	if node.CanClose && !node.CanOpen && (before == ' ' || before == '\t') {
		return nil
	}
	node.Segment = segment.WithStop(segment.Start + 1)
	block.Advance(1)
	pc.PushDelimiter(node)
	return node
}

func (scriptParser) CloseBlock(ast.Node, parser.Context) {}

type scriptRenderer struct{}

func (scriptRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	for kind, tag := range map[ast.NodeKind]string{kindSup: "sup", kindSub: "sub"} {
		reg.Register(kind, func(w util.BufWriter, _ []byte, _ ast.Node, entering bool) (ast.WalkStatus, error) {
			if entering {
				_, _ = w.WriteString("<" + tag + ">")
			} else {
				_, _ = w.WriteString("</" + tag + ">")
			}
			return ast.WalkContinue, nil
		})
	}
}

type scripts struct{}

// Priority 400: before GFM strikethrough (500), which would take a single ~.
func (scripts) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(parser.WithInlineParsers(
		util.Prioritized(scriptParser{char: '^'}, 400),
		util.Prioritized(scriptParser{char: '~'}, 400),
	))
	m.Renderer().AddOptions(renderer.WithNodeRenderers(util.Prioritized(scriptRenderer{}, 400)))
}
