package doc

import (
	"html"
	"html/template"
	"regexp"
	"strconv"
	"strings"

	"github.com/clpe/skriva/internal/slug"
	"github.com/microcosm-cc/bluemonday"
)

// Links tells how the converters make the URLs of wiki links and of
// uploaded files.
type Links struct {
	// Wiki is the prefix of wiki links. The link "wiki:Title#Part"
	// becomes Wiki + slug.Make("Title") + "#" + slug.Make("Part").
	Wiki string
	// Files is the prefix of uploaded files. The source "file:ID"
	// becomes Files + ID.
	Files string
}

// Href returns the URL of a link destination.
func (l Links) Href(href string) string {
	title, ok := strings.CutPrefix(href, WikiPrefix)
	if !ok {
		return href
	}
	title, frag, _ := strings.Cut(title, "#")
	out := l.Wiki + slug.Make(title)
	if f := slug.Make(frag); f != "" {
		out += "#" + f
	}
	return out
}

// Src returns the URL of an image source.
func (l Links) Src(src string) string {
	if id, ok := FileID(src); ok {
		return l.Files + id
	}
	return src
}

// policy is a second layer of protection after the HTML converter.
var policy = func() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.AllowAttrs("id").Matching(regexp.MustCompile(`^[a-z0-9-]+$`)).OnElements("h2", "h3", "h4")
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^align-(center|right)$`)).OnElements("p", "h2", "h3", "h4")
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^language-[a-zA-Z0-9+#_-]+$`)).OnElements("code")
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^tasks$`)).OnElements("ul")
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^task( done)?$`)).OnElements("li")
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^table$`)).OnElements("div")
	p.AllowAttrs("type").Matching(regexp.MustCompile(`^checkbox$`)).OnElements("input")
	p.AllowAttrs("checked", "disabled").OnElements("input")
	p.AllowAttrs("colspan", "rowspan").Matching(bluemonday.Integer).OnElements("td", "th")
	p.AllowAttrs("start").Matching(bluemonday.Integer).OnElements("ol")
	p.AllowAttrs("loading").Matching(regexp.MustCompile(`^lazy$`)).OnElements("img")
	p.AllowElements("u", "s")
	p.AllowURLSchemes("http", "https", "mailto")
	p.RequireNoReferrerOnLinks(true)
	return p
}()

// HTML converts the document to safe HTML.
func (n *Node) HTML(l Links) template.HTML {
	h := htmlWriter{links: l, ids: map[string]bool{}}
	h.children(n)
	// The policy removes all unsafe content. Thus the conversion to
	// template.HTML is safe.
	return template.HTML(policy.Sanitize(h.b.String())) // #nosec G203 -- sanitized.
}

type htmlWriter struct {
	b     strings.Builder
	links Links
	ids   map[string]bool
}

func (h *htmlWriter) children(n *Node) {
	for _, c := range n.Content {
		h.node(c)
	}
}

func (h *htmlWriter) open(tag, attrs string) { h.b.WriteString("<" + tag + attrs + ">") }
func (h *htmlWriter) close(tag string)       { h.b.WriteString("</" + tag + ">") }

func (h *htmlWriter) wrap(tag, attrs string, n *Node) {
	h.open(tag, attrs)
	h.children(n)
	h.close(tag)
}

func alignClass(n *Node) string {
	if a := n.align(); a != "" {
		return ` class="align-` + a + `"`
	}
	return ""
}

func (h *htmlWriter) node(n *Node) {
	switch n.Type {
	case "paragraph":
		h.wrap("p", alignClass(n), n)
	case "heading":
		tag := "h" + strconv.Itoa(n.level()+1)
		h.wrap(tag, ` id="`+h.headingID(plainInline(n))+`"`+alignClass(n), n)
	case "text":
		h.text(n)
	case "hardBreak":
		h.b.WriteString("<br>")
	case "horizontalRule":
		h.b.WriteString("<hr>")
	case "blockquote":
		h.wrap("blockquote", "", n)
	case "codeBlock":
		attrs := ""
		if l := attrString(n.Attrs, "language"); l != "" {
			attrs = ` class="language-` + l + `"`
		}
		h.b.WriteString("<pre><code" + attrs + ">")
		h.b.WriteString(html.EscapeString(plainInline(n)))
		h.b.WriteString("</code></pre>")
	case "bulletList":
		h.wrap("ul", "", n)
	case "orderedList":
		attrs := ""
		if s, ok := n.Attrs["start"]; ok {
			attrs = ` start="` + strconv.Itoa(toInt(s)) + `"`
		}
		h.wrap("ol", attrs, n)
	case "listItem":
		h.wrap("li", "", n)
	case "taskList":
		h.wrap("ul", ` class="tasks"`, n)
	case "taskItem":
		checked, class := "", "task"
		if b, _ := n.Attrs["checked"].(bool); b {
			checked, class = " checked", "task done"
		}
		h.open("li", ` class="`+class+`"`)
		h.b.WriteString(`<input type="checkbox" disabled` + checked + `>`)
		h.children(n)
		h.close("li")
	case "image":
		h.b.WriteString(`<img src="` + html.EscapeString(h.links.Src(attrString(n.Attrs, "src"))) +
			`" alt="` + html.EscapeString(attrString(n.Attrs, "alt")) + `"`)
		if t := attrString(n.Attrs, "title"); t != "" {
			h.b.WriteString(` title="` + html.EscapeString(t) + `"`)
		}
		h.b.WriteString(` loading="lazy">`)
	case "table":
		h.b.WriteString(`<div class="table"><table><tbody>`)
		h.children(n)
		h.b.WriteString(`</tbody></table></div>`)
	case "tableRow":
		h.wrap("tr", "", n)
	case "tableHeader", "tableCell":
		tag := "td"
		if n.Type == "tableHeader" {
			tag = "th"
		}
		attrs := ""
		for _, k := range []string{"colspan", "rowspan"} {
			if v := n.span(k); v > 1 {
				attrs += ` ` + k + `="` + strconv.Itoa(v) + `"`
			}
		}
		h.wrap(tag, attrs, n)
	default:
		h.children(n)
	}
}

// markTags gives the HTML element of each mark, from the outside to the
// inside. A link is the outermost element.
var markTags = []struct{ mark, tag string }{
	{"bold", "strong"}, {"italic", "em"}, {"underline", "u"}, {"strike", "s"}, {"code", "code"},
}

func (h *htmlWriter) text(n *Node) {
	var href string
	has := map[string]bool{}
	for _, m := range n.Marks {
		has[m.Type] = true
		if m.Type == "link" {
			href = attrString(m.Attrs, "href")
		}
	}
	if href != "" {
		h.b.WriteString(`<a href="` + html.EscapeString(h.links.Href(href)) + `">`)
	}
	for _, mt := range markTags {
		if has[mt.mark] {
			h.open(mt.tag, "")
		}
	}
	h.b.WriteString(html.EscapeString(n.Text))
	for i := len(markTags) - 1; i >= 0; i-- {
		if has[markTags[i].mark] {
			h.close(markTags[i].tag)
		}
	}
	if href != "" {
		h.b.WriteString("</a>")
	}
}

// headingID makes a unique ID for a heading with slug.Make. Thus the
// fragment of a wiki link "wiki:Title#Heading" is equal to the ID.
func (h *htmlWriter) headingID(text string) string {
	id := slug.Make(text)
	if id == "" {
		id = "heading"
	}
	unique := id
	for i := 1; h.ids[unique]; i++ {
		unique = id + "-" + strconv.Itoa(i)
	}
	h.ids[unique] = true
	return unique
}

// plainInline returns the text of the inline content of n. A hard break
// becomes a new line.
func plainInline(n *Node) string {
	var b strings.Builder
	var walk func(*Node)
	walk = func(x *Node) {
		switch x.Type {
		case "text":
			b.WriteString(x.Text)
		case "hardBreak":
			b.WriteByte('\n')
		}
		for _, c := range x.Content {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

func toInt(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case float64:
		return int(x)
	}
	return 0
}
