package doc

import (
	"regexp"
	"strconv"
	"strings"
)

// Markdown converts the document to Markdown (GitHub Flavored Markdown).
// Markdown does not have all formats of a document. The converter ignores
// the alignment and the underline. A cell that covers more than one
// column or row becomes one cell and empty cells. Wiki links become
// [[Title]].
func (n *Node) Markdown(l Links) string {
	return n.MarkdownWith(l, MarkdownOptions{WikiLinks: true})
}

// MarkdownOptions change the result of MarkdownWith.
type MarkdownOptions struct {
	// HeadingOffset is added to the level of each heading. Markdown has
	// six heading levels, thus a higher level becomes level 6.
	HeadingOffset int
	// WikiLinks keeps the wiki links as [[Title]]. If it is false, a wiki
	// link becomes a normal link to the URL from Links.Href.
	WikiLinks bool
}

// MarkdownWith converts the document to Markdown with the options o. See
// Markdown.
func (n *Node) MarkdownWith(l Links, o MarkdownOptions) string {
	m := mdWriter{links: l, opts: o}
	m.blocks(n.Content, "")
	return strings.TrimRight(m.b.String(), "\n") + "\n"
}

type mdWriter struct {
	b     strings.Builder
	links Links
	opts  MarkdownOptions
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
		m.lines(first, prefix, escapeLineStarts(m.inline(n)))
	case "heading":
		// Markdown has six heading levels. Higher levels become level 6.
		level := min(max(n.level()+m.opts.HeadingOffset, 1), 6)
		m.lines(first, prefix, strings.Repeat("#", level)+" "+strings.ReplaceAll(m.inline(n), "\n", " "))
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
	// An image is on one line.
	oneLine := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	out := "![" + escapeMD(oneLine(attrString(n.Attrs, "alt"))) + "](" + escapeURL(m.links.Src(attrString(n.Attrs, "src")))
	if t := oneLine(attrString(n.Attrs, "title")); t != "" {
		out += ` "` + titleEscaper.Replace(t) + `"`
	}
	return out + ")"
}

var titleEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`)

// table writes a GFM table. The first row is the header row.
func (m *mdWriter) table(n *Node, first, prefix string) {
	var rows [][]string
	width := 0
	// covered[c] is the number of rows below that a cell with a rowspan
	// covers in the column c.
	var covered []int
	for _, row := range n.Content {
		var cells []string
		col := 0
		// skip adds empty cells for the columns that a cell above covers.
		skip := func() {
			for col < len(covered) && covered[col] > 0 {
				covered[col]--
				cells = append(cells, "")
				col++
			}
		}
		for _, cell := range row.Content {
			skip()
			cells = append(cells, strings.ReplaceAll(m.cellText(cell), "|", `\|`))
			for k := 1; k < cell.span("colspan"); k++ {
				cells = append(cells, "")
			}
			for k := range cell.span("colspan") {
				for len(covered) <= col+k {
					covered = append(covered, 0)
				}
				covered[col+k] = max(covered[col+k], cell.span("rowspan")-1)
			}
			col += cell.span("colspan")
		}
		skip()
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

// emphasis are the marks that Markdown writes with delimiters, from the
// outside to the inside.
var emphasis = []struct{ mark, delim string }{{"bold", "**"}, {"italic", "*"}, {"strike", "~~"}}

// inline returns the Markdown of the inline content of n. A mark that
// continues over more than one text node opens and closes only once.
// Thus "**a *b***" is correct, not "**a****_b_**".
func (m *mdWriter) inline(n *Node) string {
	var b []byte
	var open []string // the delimiters of the open marks, outside first
	// closeTo closes the marks after the first k.
	closeTo := func(k int) {
		if k >= len(open) {
			return
		}
		// A closing delimiter must follow a character that is not a
		// space. Thus the spaces go after it.
		end := len(b)
		for end > 0 && b[end-1] == ' ' {
			end--
		}
		spaces := string(b[end:])
		b = b[:end]
		for i := len(open) - 1; i >= k; i-- {
			b = append(b, open[i]...)
		}
		b = append(b, spaces...)
		open = open[:k]
	}
	for _, c := range n.Content {
		switch c.Type {
		case "text":
		case "hardBreak":
			closeTo(0)
			b = append(b, "\\\n"...)
			continue
		case "image":
			closeTo(0)
			b = append(b, m.image(c)...)
			continue
		default:
			closeTo(0)
			b = append(b, m.inline(c)...)
			continue
		}
		core := m.textCore(c)
		if strings.TrimSpace(core) == "" {
			// Only spaces: the open marks stay open.
			b = append(b, core...)
			continue
		}
		var want []string
		for _, e := range emphasis {
			if c.hasMark(e.mark) {
				want = append(want, e.delim)
			}
		}
		// Keep the open marks that the node also has, in the same order.
		k := 0
		for k < len(open) && k < len(want) && open[k] == want[k] {
			k++
		}
		closeTo(k)
		// An opening delimiter must come before a character that is not
		// a space. Thus the spaces go before it.
		lead := core[:len(core)-len(strings.TrimLeft(core, " "))]
		b = append(b, lead...)
		for _, d := range want[k:] {
			b = append(b, d...)
			open = append(open, d)
		}
		b = append(b, core[len(lead):]...)
	}
	closeTo(0)
	return string(b)
}

// hasMark reports whether the node has a mark of type t.
func (n *Node) hasMark(t string) bool {
	for _, mk := range n.Marks {
		if mk.Type == t {
			return true
		}
	}
	return false
}

// textCore returns the Markdown of a text node, with the code and the
// link marks but without the emphasis marks.
func (m *mdWriter) textCore(n *Node) string {
	var href string
	for _, mk := range n.Marks {
		if mk.Type == "link" {
			href = attrString(mk.Attrs, "href")
		}
	}
	var s string
	if n.hasMark("code") {
		s = codeSpan(n.Text)
	} else {
		s = escapeMD(n.Text)
	}
	if href == "" {
		return s
	}
	// The spaces stay outside the link.
	core := strings.Trim(s, " ")
	if core == "" {
		return s
	}
	lead := s[:strings.Index(s, core)]
	trail := s[len(lead)+len(core):]
	if title, ok := strings.CutPrefix(href, WikiPrefix); ok && m.opts.WikiLinks {
		if title == n.Text {
			core = "[[" + title + "]]"
		} else {
			core = "[[" + title + "|" + core + "]]"
		}
	} else {
		core = "[" + core + "](" + escapeURL(m.links.Href(href)) + ")"
	}
	return lead + core + trail
}

// codeSpan returns text as a code span. The fence is longer than each
// run of backticks in text. CommonMark removes one space at each end, if
// both ends have a space. Thus the function adds spaces if text starts or
// ends with a backtick or with a space.
func codeSpan(text string) string {
	fence := "`"
	for strings.Contains(text, fence) {
		fence += "`"
	}
	if strings.HasPrefix(text, "`") || strings.HasSuffix(text, "`") ||
		(strings.HasPrefix(text, " ") && strings.HasSuffix(text, " ") && strings.Trim(text, " ") != "") {
		text = " " + text + " "
	}
	return fence + text + fence
}

// lineStartRE finds text at the start of a line that Markdown reads as a
// block: a list item ("- ", "+ ", "1. ", "1) "), a line of a heading or
// a rule ("===", "---").
var lineStartRE = regexp.MustCompile(`(?m)^( {0,3})([-+=]|[0-9]{1,9}[.)])`)

// escapeLineStarts escapes the text at the start of each line that
// Markdown reads as a block.
func escapeLineStarts(s string) string {
	return lineStartRE.ReplaceAllStringFunc(s, func(m string) string {
		sp := strings.TrimLeft(m, " ")
		ind := m[:len(m)-len(sp)]
		if last := sp[len(sp)-1]; last == '.' || last == ')' {
			return ind + sp[:len(sp)-1] + `\` + string(last)
		}
		return ind + `\` + sp
	})
}

var mdEscaper = strings.NewReplacer(
	`\`, `\\`, "`", "\\`", `*`, `\*`, `_`, `\_`, `[`, `\[`, `]`, `\]`,
	`<`, `\<`, `>`, `\>`, `#`, `\#`, `~`, `\~`, `&`, `\&`,
)

// escapeMD escapes the characters that have a meaning in Markdown.
func escapeMD(s string) string { return mdEscaper.Replace(s) }

// EscapeMarkdown makes s safe for one line of Markdown text. It changes
// each sequence of white space to one space and escapes the characters
// that have a meaning in Markdown.
func EscapeMarkdown(s string) string { return escapeMD(strings.Join(strings.Fields(s), " ")) }

var urlEscaper = strings.NewReplacer(" ", "%20", "(", "%28", ")", "%29", "<", "%3C", ">", "%3E")

func escapeURL(s string) string { return urlEscaper.Replace(s) }
