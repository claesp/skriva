package doc

import (
	"strconv"
	"strings"
)

// Markdown converts the document to Markdown (GitHub Flavored Markdown).
// Markdown does not have all formats of a document. The converter ignores
// the alignment and the underline. A cell that covers more than one
// column becomes one cell and empty cells. Wiki links become [[Title]].
func (n *Node) Markdown(l Links) string {
	m := mdWriter{links: l}
	m.blocks(n.Content, "")
	return strings.TrimRight(m.b.String(), "\n") + "\n"
}

type mdWriter struct {
	b     strings.Builder
	links Links
}

// blocks writes block nodes. prefix is the start of each line, for
// example "> " in a quote or spaces in a list item.
func (m *mdWriter) blocks(nodes []*Node, prefix string) {
	for i, n := range nodes {
		if i > 0 {
			m.b.WriteString(strings.TrimRight(prefix, " ") + "\n")
		}
		m.block(n, prefix, prefix)
	}
}

// block writes one block node. first is the prefix of the first line.
func (m *mdWriter) block(n *Node, first, prefix string) {
	switch n.Type {
	case "paragraph":
		m.lines(first, prefix, m.inline(n))
	case "heading":
		// Markdown has six heading levels. Levels 7 and 8 become level 6.
		m.lines(first, prefix, strings.Repeat("#", min(n.level(), 6))+" "+strings.ReplaceAll(m.inline(n), "\n", " "))
	case "horizontalRule":
		m.lines(first, prefix, "---")
	case "blockquote":
		m.b.WriteString(first)
		m.blocks(n.Content, prefix+"> ")
		if len(n.Content) == 0 {
			m.b.WriteString(">\n")
		}
	case "codeBlock":
		code := plainInline(n)
		fence := "```"
		for strings.Contains(code, fence) {
			fence += "`"
		}
		text := fence + attrString(n.Attrs, "language") + "\n" + code + "\n" + fence
		m.lines(first, prefix, text)
	case "bulletList", "orderedList", "taskList":
		m.list(n, first, prefix)
	case "image":
		m.lines(first, prefix, m.image(n))
	case "table":
		m.table(n, first, prefix)
	default:
		if len(n.Content) > 0 {
			m.blocks(n.Content, prefix)
		}
	}
}

// lines writes text. Each line after the first gets prefix.
func (m *mdWriter) lines(first, prefix, text string) {
	for i, line := range strings.Split(text, "\n") {
		if i == 0 {
			m.b.WriteString(first)
		} else {
			m.b.WriteString(prefix)
		}
		m.b.WriteString(line)
		m.b.WriteByte('\n')
	}
}

func (m *mdWriter) list(n *Node, first, prefix string) {
	num := 1
	if s, ok := n.Attrs["start"]; ok {
		num = toInt(s)
	}
	for i, item := range n.Content {
		marker := "- "
		switch n.Type {
		case "orderedList":
			marker = strconv.Itoa(num+i) + ". "
		case "taskList":
			marker = "- [ ] "
			if b, _ := item.Attrs["checked"].(bool); b {
				marker = "- [x] "
			}
		}
		lead := prefix
		if i == 0 {
			lead = first
		}
		inner := prefix + strings.Repeat(" ", len(marker))
		if len(item.Content) == 0 {
			m.b.WriteString(lead + strings.TrimRight(marker, " ") + "\n")
			continue
		}
		for j, c := range item.Content {
			if j == 0 {
				m.block(c, lead+marker, inner)
				continue
			}
			// A list in an item follows the text of the item directly.
			if c.Type != "bulletList" && c.Type != "orderedList" && c.Type != "taskList" {
				m.b.WriteString(strings.TrimRight(inner, " ") + "\n")
			}
			m.block(c, inner, inner)
		}
	}
}

func (m *mdWriter) image(n *Node) string {
	out := "![" + escapeMD(attrString(n.Attrs, "alt")) + "](" + escapeURL(m.links.Src(attrString(n.Attrs, "src")))
	if t := attrString(n.Attrs, "title"); t != "" {
		out += ` "` + strings.ReplaceAll(t, `"`, `\"`) + `"`
	}
	return out + ")"
}

// table writes a GFM table. The first row is the header row.
func (m *mdWriter) table(n *Node, first, prefix string) {
	var rows [][]string
	width := 0
	for _, row := range n.Content {
		var cells []string
		for _, cell := range row.Content {
			text := strings.ReplaceAll(m.cellText(cell), "|", `\|`)
			cells = append(cells, text)
			for k := 1; k < cell.span("colspan"); k++ {
				cells = append(cells, "")
			}
		}
		width = max(width, len(cells))
		rows = append(rows, cells)
	}
	if len(rows) == 0 || width == 0 {
		return
	}
	line := func(cells []string) string {
		for len(cells) < width {
			cells = append(cells, "")
		}
		return "| " + strings.Join(cells, " | ") + " |"
	}
	var text []string
	text = append(text, line(rows[0]))
	sep := make([]string, width)
	for i := range sep {
		sep[i] = "---"
	}
	text = append(text, line(sep))
	for _, r := range rows[1:] {
		text = append(text, line(r))
	}
	m.lines(first, prefix, strings.Join(text, "\n"))
}

// cellText returns the inline Markdown of all blocks in a cell, on one
// line.
func (m *mdWriter) cellText(cell *Node) string {
	var parts []string
	for _, c := range cell.Content {
		if t := strings.TrimSpace(m.inline(c)); t != "" {
			parts = append(parts, strings.ReplaceAll(t, "\n", " "))
		}
	}
	return strings.Join(parts, " ")
}

// inline returns the Markdown of the inline content of n.
func (m *mdWriter) inline(n *Node) string {
	var b strings.Builder
	for _, c := range n.Content {
		switch c.Type {
		case "text":
			b.WriteString(m.text(c))
		case "hardBreak":
			b.WriteString("\\\n")
		case "image":
			b.WriteString(m.image(c))
		default:
			b.WriteString(m.inline(c))
		}
	}
	return b.String()
}

func (m *mdWriter) text(n *Node) string {
	has := map[string]bool{}
	var href string
	for _, mk := range n.Marks {
		has[mk.Type] = true
		if mk.Type == "link" {
			href = attrString(mk.Attrs, "href")
		}
	}
	var s string
	if has["code"] {
		fence := "`"
		for strings.Contains(n.Text, fence) {
			fence += "`"
		}
		s = fence + n.Text + fence
	} else {
		s = escapeMD(n.Text)
	}
	// Markdown does not permit spaces inside the emphasis marks. Thus
	// the spaces stay outside.
	core := strings.TrimSpace(s)
	if core == "" {
		return s
	}
	lead := s[:strings.Index(s, core)]
	trail := s[len(lead)+len(core):]
	if has["strike"] {
		core = "~~" + core + "~~"
	}
	if has["italic"] {
		core = "_" + core + "_"
	}
	if has["bold"] {
		core = "**" + core + "**"
	}
	if href != "" {
		if title, ok := strings.CutPrefix(href, WikiPrefix); ok {
			if title == n.Text {
				core = "[[" + title + "]]"
			} else {
				core = "[[" + title + "|" + core + "]]"
			}
		} else {
			core = "[" + core + "](" + escapeURL(m.links.Href(href)) + ")"
		}
	}
	return lead + core + trail
}

var mdEscaper = strings.NewReplacer(
	`\`, `\\`, "`", "\\`", `*`, `\*`, `_`, `\_`, `[`, `\[`, `]`, `\]`,
	`<`, `\<`, `>`, `\>`, `#`, `\#`, `~`, `\~`,
)

// escapeMD escapes the characters that have a meaning in Markdown.
func escapeMD(s string) string { return mdEscaper.Replace(s) }

var urlEscaper = strings.NewReplacer(" ", "%20", "(", "%28", ")", "%29", "<", "%3C", ">", "%3E")

func escapeURL(s string) string { return urlEscaper.Replace(s) }
