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
