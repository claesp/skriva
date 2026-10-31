package doc

import "strings"

// PlainText returns the plain text of the document for the search index and
// for the parts of the text in the search results. Each block starts on
// a new line. The text contains the alternative text of images, but not
// the destinations of links.
func (n *Node) PlainText() string {
	var b strings.Builder
	n.plain(&b)
	lines := strings.Split(b.String(), "\n")
	out := lines[:0]
	for _, l := range lines {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

func (n *Node) plain(b *strings.Builder) {
	switch n.Type {
	case "text":
		b.WriteString(n.Text)
		return
	case "hardBreak":
		b.WriteByte('\n')
		return
	case "image":
		b.WriteString("\n" + attrString(n.Attrs, "alt") + "\n")
		return
	}
	for _, c := range n.Content {
		c.plain(b)
	}
	switch n.Type {
	case "paragraph", "heading", "codeBlock", "tableRow", "horizontalRule":
		b.WriteByte('\n')
	}
}
