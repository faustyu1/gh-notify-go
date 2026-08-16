package render

import (
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// Markdown converts GitHub-flavoured Markdown into the Telegram HTML subset
// this package emits: b, i, code, pre, a and blockquote. Anything else
// degrades to plain text, per the spec.
//
// limit caps the rendered length in characters; 0 means uncapped. When the
// render overflows, the source is cut and re-rendered rather than slicing the
// HTML, which would split a tag.
func Markdown(src string, limit int) string {
	rendered := renderMarkdown(src)
	if limit <= 0 || len([]rune(rendered)) <= limit {
		return rendered
	}
	// Cut generously (markup does not count towards the visible length),
	// then trim the result; if tags still overflow, plain truncation of the
	// stripped text is the last resort.
	cut := renderMarkdown(Truncate(src, limit))
	if len([]rune(cut)) <= limit {
		return cut
	}
	return Truncate(Strip(cut), limit)
}

func renderMarkdown(src string) string {
	reader := text.NewReader([]byte(src))
	doc := goldmark.New().Parser().Parse(reader)

	w := &mdWriter{source: []byte(src)}
	w.node(doc)
	return strings.TrimSpace(w.b.String())
}

// mdWriter walks the AST depth-first, tracking list depth for indentation.
// It is deliberately small: exotic nodes degrade to their text content.
type mdWriter struct {
	b       strings.Builder
	source  []byte
	listDep int
}

func (w *mdWriter) node(n ast.Node) {
	switch n := n.(type) {
	case *ast.Document, *ast.TextBlock, *ast.ListItem:
		w.children(n)
	case *ast.Heading:
		w.b.WriteString("<b>")
		w.children(n)
		w.b.WriteString("</b>\n")
	case *ast.Paragraph:
		w.children(n)
		w.b.WriteString("\n")
	case *ast.Text:
		w.b.WriteString(Escape(string(n.Segment.Value(w.source))))
	case *ast.String:
		w.b.WriteString(Escape(string(n.Value)))
	case *ast.CodeSpan:
		w.b.WriteString("<code>")
		w.children(n)
		w.b.WriteString("</code>")
	case *ast.Emphasis:
		tag := "i"
		if n.Level == 2 {
			tag = "b"
		}
		w.b.WriteString("<" + tag + ">")
		w.children(n)
		w.b.WriteString("</" + tag + ">")
	case *ast.Link:
		w.b.WriteString(`<a href="` + Escape(string(n.Destination)) + `">`)
		w.children(n)
		w.b.WriteString("</a>")
	case *ast.AutoLink:
		url := string(n.URL(w.source))
		w.b.WriteString(Link(url, url))
	case *ast.FencedCodeBlock, *ast.CodeBlock:
		w.b.WriteString("<pre>")
		for i := 0; i < n.Lines().Len(); i++ {
			segment := n.Lines().At(i)
			w.b.WriteString(Escape(string(segment.Value(w.source))))
		}
		w.b.WriteString("</pre>\n")
	case *ast.Blockquote:
		w.b.WriteString("<blockquote>")
		w.children(n)
		w.b.WriteString("</blockquote>\n")
	case *ast.List:
		w.listDep++
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			w.b.WriteString(strings.Repeat("  ", w.listDep-1) + "• ")
			w.node(c)
			w.b.WriteString("\n")
		}
		w.listDep--
	case *ast.ThematicBreak, *ast.Image, *ast.HTMLBlock:
		// Degrade: images and raw HTML blocks contribute nothing usable to a
		// chat. Inline HTML, however, is common in plain prose ("a < b"), so
		// it is escaped rather than dropped below.
	case *ast.RawHTML:
		for i := 0; i < n.Segments.Len(); i++ {
			segment := n.Segments.At(i)
			w.b.WriteString(Escape(string(segment.Value(w.source))))
		}
	default:
		w.children(n)
	}
}

func (w *mdWriter) children(n ast.Node) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		w.node(c)
	}
}
