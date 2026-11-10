package markdown

import (
	"regexp"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// A run of fenced code blocks that carry the same `group=name` in their info
// string (```go group=sort) renders as one <div class="code-tabs">; the
// client turns it into language tabs. A group of one block, or fences that are
// not next to each other, stay plain code blocks.

var codeGroupAttr = regexp.MustCompile(`(?:^|\s)group=([A-Za-z0-9_-]{1,32})(?:\s|$)`)

var kindCodeTabs = ast.NewNodeKind("CodeTabs")

type codeTabs struct{ ast.BaseBlock }

func (*codeTabs) Kind() ast.NodeKind           { return kindCodeTabs }
func (n *codeTabs) Dump(src []byte, level int) { ast.DumpHelper(n, src, level, nil, nil) }

func groupOf(n ast.Node, src []byte) string {
	fc, ok := n.(*ast.FencedCodeBlock)
	if !ok || fc.Info == nil {
		return ""
	}
	if m := codeGroupAttr.FindSubmatch(fc.Info.Segment.Value(src)); m != nil {
		return string(m[1])
	}
	return ""
}

type codeTabsTransformer struct{}

func (codeTabsTransformer) Transform(doc *ast.Document, reader text.Reader, _ parser.Context) {
	src := reader.Source()
	var parents []ast.Node
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering && n.HasChildren() {
			parents = append(parents, n)
		}
		return ast.WalkContinue, nil
	})
	for _, p := range parents {
		for c := p.FirstChild(); c != nil; {
			g := groupOf(c, src)
			if g == "" {
				c = c.NextSibling()
				continue
			}
			run := []ast.Node{c}
			for s := c.NextSibling(); s != nil && groupOf(s, src) == g; s = s.NextSibling() {
				run = append(run, s)
			}
			next := run[len(run)-1].NextSibling()
			if len(run) > 1 {
				box := &codeTabs{}
				p.InsertBefore(p, c, box)
				for _, r := range run {
					p.RemoveChild(p, r)
					box.AppendChild(box, r)
				}
			}
			c = next
		}
	}
}

type codeTabsRenderer struct{}

func (codeTabsRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindCodeTabs, func(w util.BufWriter, _ []byte, _ ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			_, _ = w.WriteString("<div class=\"code-tabs\">\n")
		} else {
			_, _ = w.WriteString("</div>\n")
		}
		return ast.WalkContinue, nil
	})
}
