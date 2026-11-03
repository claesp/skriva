// Package doc contains the format of the documents. A document is a tree
// of nodes in the JSON format of the Tiptap editor (the ProseMirror
// format). The package checks a document against a list of permitted
// nodes, marks and attributes. It also converts a document to HTML, to
// Markdown and to plain text.
//
// The server does not trust the editor. Parse removes all items that are
// not on the list. The other functions use only the result of Parse.
package doc

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/clpe/skriva/internal/slug"
)

// Node is one node of a document.
type Node struct {
	Type    string         `json:"type"`
	Attrs   map[string]any `json:"attrs,omitempty"`
	Content []*Node        `json:"content,omitempty"`
	Marks   []*Mark        `json:"marks,omitempty"`
	Text    string         `json:"text,omitempty"`
}

// Mark is a format of a text, for example bold or a link.
type Mark struct {
	Type  string         `json:"type"`
	Attrs map[string]any `json:"attrs,omitempty"`
}

// Limits of a document.
const (
	// MaxDepth is the largest permitted depth of the tree. Deeper nodes
	// make Parse fail.
	MaxDepth = 64
	// MaxNodes is the largest permitted number of nodes.
	MaxNodes = 200000
	// MaxHeadingLevel is the largest heading level. The page shows a
	// heading of level n as an HTML heading of level n+1, because the
	// title of the page is the h1 element. HTML has no h7 to h9
	// elements, thus levels 6 to 8 are paragraphs with the role
	// "heading". See Links.HTML.
	MaxHeadingLevel = 8
	maxAttrText     = 500
	maxCells        = 100
)

// Errors of Parse.
var (
	ErrSyntax   = errors.New("the document is not valid JSON")
	ErrTooLarge = errors.New("the document has too many nodes or is too deep")
)

// Empty returns an empty document.
func Empty() *Node { return &Node{Type: "doc"} }

// EmptyJSON is the JSON form of an empty document.
const EmptyJSON = `{"type":"doc"}`

// Parse reads a document in JSON format and removes all nodes, marks and
// attributes that are not permitted. Empty data gives an empty document.
// If imageSrc is not nil, Parse calls it for each image source before the
// check. Thus the caller can change the URL of an uploaded file back to a
// "file:" reference.
func Parse(data []byte, imageSrc func(string) string) (*Node, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return Empty(), nil
	}
	if !utf8.Valid(data) {
		return nil, ErrSyntax
	}
	var root Node
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, ErrSyntax
	}
	if root.Type != "doc" {
		return nil, ErrSyntax
	}
	c := cleaner{imageSrc: imageSrc}
	out := c.node(&root, 0)
	if c.err != nil {
		return nil, c.err
	}
	// The editor adds an empty paragraph at the end, for example after
	// a table. The document does not keep it.
	for len(out.Content) > 0 {
		last := out.Content[len(out.Content)-1]
		if last.Type != "paragraph" || len(last.Content) > 0 {
			break
		}
		out.Content = out.Content[:len(out.Content)-1]
	}
	return out, nil
}

// JSON returns the document in JSON format.
func (n *Node) JSON() []byte {
	b, err := json.Marshal(n)
	if err != nil {
		// A tree of strings, numbers, booleans and maps always encodes.
		panic(err)
	}
	return b
}

// Files returns the IDs of the uploaded files that the document uses.
// Each ID occurs one time.
func (n *Node) Files() []string {
	var out []string
	seen := map[string]bool{}
	n.walk(func(x *Node) {
		if x.Type != "image" {
			return
		}
		if id, ok := FileID(attrString(x.Attrs, "src")); ok && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	})
	return out
}

// MapImages changes the source of each image to fn(source).
func (n *Node) MapImages(fn func(string) string) {
	n.walk(func(x *Node) {
		if x.Type == "image" {
			x.Attrs["src"] = fn(attrString(x.Attrs, "src"))
		}
	})
}

// MapLinks changes the destination of each link to fn(destination).
func (n *Node) MapLinks(fn func(string) string) {
	n.walk(func(x *Node) {
		for _, m := range x.Marks {
			if m.Type == "link" {
				m.Attrs["href"] = fn(attrString(m.Attrs, "href"))
			}
		}
	})
}

func (n *Node) walk(fn func(*Node)) {
	fn(n)
	for _, c := range n.Content {
		c.walk(fn)
	}
}

// FilePrefix is the prefix of an image source that refers to an uploaded
// file, for example "file:0a1b2c3d4e5f6g".
const FilePrefix = "file:"

var fileIDRE = regexp.MustCompile(`^[0-9a-z]{14}$`)

// FileID returns the file ID of a "file:" source.
func FileID(src string) (string, bool) {
	id, ok := strings.CutPrefix(src, FilePrefix)
	return id, ok && fileIDRE.MatchString(id)
}

// WikiPrefix is the prefix of a link to a document with a title, for
// example "wiki:Getting started#Install".
const WikiPrefix = "wiki:"

// DocPrefix is the prefix of a link to a document with its public ID,
// for example "doc:k3x9p2qa7tm4zb#Install". The link stays correct when
// the title of the document changes or the document moves.
const DocPrefix = "doc:"

// DocLink returns the public ID and the heading of a "doc:" link.
func DocLink(href string) (id, heading string, ok bool) {
	rest, ok := strings.CutPrefix(href, DocPrefix)
	if !ok {
		return "", "", false
	}
	id, heading, _ = strings.Cut(rest, "#")
	return id, heading, fileIDRE.MatchString(id)
}

// Links returns the targets of the links in the document: the public IDs
// of the "doc:" links and the slugs of the titles of the wiki links. Each
// value occurs one time.
func (n *Node) Links() (docs, wiki []string) {
	seenDoc, seenWiki := map[string]bool{}, map[string]bool{}
	n.walk(func(x *Node) {
		for _, m := range x.Marks {
			if m.Type != "link" {
				continue
			}
			href := attrString(m.Attrs, "href")
			if id, _, ok := DocLink(href); ok && !seenDoc[id] {
				seenDoc[id] = true
				docs = append(docs, id)
			}
			if title, ok := strings.CutPrefix(href, WikiPrefix); ok {
				title, _, _ = strings.Cut(title, "#")
				if s := slug.Make(title); s != "" && !seenWiki[s] {
					seenWiki[s] = true
					wiki = append(wiki, s)
				}
			}
		}
	})
	return docs, wiki
}

// nodeSpec tells which attributes a node type has and whether it can
// have children.
type nodeSpec struct {
	leaf  bool
	attrs func(c *cleaner, in, out map[string]any) bool
}

var nodeSpecs = map[string]nodeSpec{
	"doc":            {},
	"paragraph":      {attrs: alignAttr},
	"heading":        {attrs: headingAttrs},
	"text":           {leaf: true},
	"hardBreak":      {leaf: true},
	"horizontalRule": {leaf: true},
	"blockquote":     {},
	"codeBlock":      {attrs: codeAttrs},
	"bulletList":     {},
	"orderedList":    {attrs: orderedAttrs},
	"listItem":       {},
	"taskList":       {},
	"taskItem":       {attrs: taskAttrs},
	"image":          {leaf: true, attrs: imageAttrs},
	"table":          {},
	"tableRow":       {},
	"tableHeader":    {attrs: cellAttrs},
	"tableCell":      {attrs: cellAttrs},
}

var markTypes = map[string]bool{
	"bold": true, "italic": true, "underline": true, "strike": true, "code": true, "link": true,
}

// Alignments are the permitted values of the textAlign attribute. Left
// is the default value, thus it is not in the document.
var alignments = map[string]bool{"center": true, "right": true}

type cleaner struct {
	imageSrc func(string) string
	count    int
	err      error
}

// node returns a clean copy of n, or nil if n is not permitted.
func (c *cleaner) node(n *Node, depth int) *Node {
	if n == nil || c.err != nil {
		return nil
	}
	c.count++
	if depth > MaxDepth || c.count > MaxNodes {
		c.err = ErrTooLarge
		return nil
	}
	spec, ok := nodeSpecs[n.Type]
	if !ok {
		return nil
	}
	out := &Node{Type: n.Type}
	if spec.attrs != nil {
		out.Attrs = map[string]any{}
		if !spec.attrs(c, n.Attrs, out.Attrs) {
			return nil
		}
		if len(out.Attrs) == 0 {
			out.Attrs = nil
		}
	}
	if n.Type == "text" {
		if n.Text == "" {
			return nil
		}
		out.Text = cleanText(n.Text)
		out.Marks = cleanMarks(n.Marks)
		return out
	}
	if spec.leaf {
		return out
	}
	for _, ch := range n.Content {
		if x := c.node(ch, depth+1); x != nil {
			out.Content = append(out.Content, x)
		}
	}
	return out
}

// cleanText removes control characters other than tab and new line.
func cleanText(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || !unicode.IsControl(r) {
			return r
		}
		return -1
	}, s)
}

func cleanMarks(in []*Mark) []*Mark {
	var out []*Mark
	seen := map[string]bool{}
	for _, m := range in {
		if m == nil || !markTypes[m.Type] || seen[m.Type] {
			continue
		}
		cm := &Mark{Type: m.Type}
		if m.Type == "link" {
			href, ok := cleanHref(attrString(m.Attrs, "href"))
			if !ok {
				continue
			}
			cm.Attrs = map[string]any{"href": href}
		}
		seen[m.Type] = true
		out = append(out, cm)
	}
	return out
}

// cleanHref checks the destination of a link. It permits http, https and
// mailto URLs, wiki links, document links, paths on the same site and
// fragments.
func cleanHref(href string) (string, bool) {
	href = strings.TrimSpace(href)
	if href == "" || len(href) > 2000 || strings.ContainsFunc(href, unicode.IsControl) || strings.Contains(href, `\`) {
		return "", false
	}
	if title, ok := strings.CutPrefix(href, WikiPrefix); ok {
		return href, strings.TrimSpace(strings.SplitN(title, "#", 2)[0]) != ""
	}
	if strings.HasPrefix(href, DocPrefix) {
		_, _, ok := DocLink(href)
		return href, ok
	}
	if strings.HasPrefix(href, "#") || (strings.HasPrefix(href, "/") && !strings.HasPrefix(href, "//")) {
		return href, true
	}
	u, err := url.Parse(href)
	if err != nil {
		return "", false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return href, u.Host != ""
	case "mailto":
		return href, true
	}
	return "", false
}

// cleanImageSrc permits "file:" references and https URLs.
func cleanImageSrc(src string) (string, bool) {
	src = strings.TrimSpace(src)
	if _, ok := FileID(src); ok {
		return src, true
	}
	if len(src) > 2000 || strings.ContainsFunc(src, unicode.IsControl) || strings.Contains(src, `\`) {
		return "", false
	}
	u, err := url.Parse(src)
	if err != nil || strings.ToLower(u.Scheme) != "https" || u.Host == "" {
		return "", false
	}
	return src, true
}

func attrString(attrs map[string]any, key string) string {
	s, _ := attrs[key].(string)
	return s
}

// attrInt returns an integer attribute. JSON numbers are float64 values.
func attrInt(attrs map[string]any, key string) (int, bool) {
	f, ok := attrs[key].(float64)
	if !ok || f != math.Trunc(f) || f < math.MinInt32 || f > math.MaxInt32 {
		return 0, false
	}
	return int(f), true
}

func limitText(s string) string {
	s = cleanText(s)
	if utf8.RuneCountInString(s) <= maxAttrText {
		return s
	}
	return string([]rune(s)[:maxAttrText])
}

func alignAttr(_ *cleaner, in, out map[string]any) bool {
	if a := attrString(in, "textAlign"); alignments[a] {
		out["textAlign"] = a
	}
	return true
}

func headingAttrs(c *cleaner, in, out map[string]any) bool {
	level, ok := attrInt(in, "level")
	if !ok || level < 1 {
		level = 1
	}
	out["level"] = min(level, MaxHeadingLevel)
	return alignAttr(c, in, out)
}

var languageRE = regexp.MustCompile(`^[a-zA-Z0-9+#_-]{1,32}$`)

func codeAttrs(_ *cleaner, in, out map[string]any) bool {
	if l := attrString(in, "language"); languageRE.MatchString(l) {
		out["language"] = l
	}
	return true
}

func orderedAttrs(_ *cleaner, in, out map[string]any) bool {
	if s, ok := attrInt(in, "start"); ok && s != 1 && s >= 0 && s <= 1_000_000 {
		out["start"] = s
	}
	return true
}

func taskAttrs(_ *cleaner, in, out map[string]any) bool {
	if b, _ := in["checked"].(bool); b {
		out["checked"] = true
	}
	return true
}

func imageAttrs(c *cleaner, in, out map[string]any) bool {
	src := attrString(in, "src")
	if c.imageSrc != nil {
		src = c.imageSrc(src)
	}
	src, ok := cleanImageSrc(src)
	if !ok {
		return false
	}
	out["src"] = src
	if a := limitText(attrString(in, "alt")); a != "" {
		out["alt"] = a
	}
	if t := limitText(attrString(in, "title")); t != "" {
		out["title"] = t
	}
	return true
}

func cellAttrs(_ *cleaner, in, out map[string]any) bool {
	for _, k := range []string{"colspan", "rowspan"} {
		if v, ok := attrInt(in, k); ok && v > 1 {
			out[k] = min(v, maxCells)
		}
	}
	return true
}

// align returns the alignment of a paragraph or a heading, or "".
func (n *Node) align() string { return attrString(n.Attrs, "textAlign") }

// span returns the colspan or rowspan of a cell. The default is 1.
func (n *Node) span(key string) int {
	switch v := n.Attrs[key].(type) {
	case int:
		return v
	case float64:
		return int(v)
	}
	return 1
}

// level returns the level of a heading.
func (n *Node) level() int {
	switch v := n.Attrs["level"].(type) {
	case int:
		return v
	case float64:
		return int(v)
	}
	return 1
}
