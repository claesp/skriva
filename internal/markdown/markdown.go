// Package markdown converts page text in Markdown to safe HTML.
package markdown

import (
	"bytes"
	"html/template"
	"regexp"
	"strings"
	"unicode"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"go.abhg.dev/goldmark/wikilink"
	"golang.org/x/text/unicode/norm"
)

// Renderer converts Markdown to HTML. It is safe for concurrent use.
type Renderer struct {
	md     goldmark.Markdown
	policy *bluemonday.Policy
}

// New makes a Renderer. Wiki links ([[Page title]]) point to
// pathPrefix + Slugify(title).
func New(pathPrefix string) *Renderer {
	md := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			extension.Footnote,
			&wikilink.Extender{Resolver: resolver{prefix: pathPrefix}},
		),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		// The renderer does not have html.WithUnsafe. Thus goldmark
		// removes raw HTML and dangerous link destinations.
	)

	// The sanitizer is a second layer of protection.
	p := bluemonday.UGCPolicy()
	p.AllowAttrs("id").Matching(regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)).OnElements("h1", "h2", "h3", "h4", "h5", "h6", "li")
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^language-[a-zA-Z0-9+#_-]+$`)).OnElements("code")
	p.AllowAttrs("type").Matching(regexp.MustCompile(`^checkbox$`)).OnElements("input")
	p.AllowAttrs("checked", "disabled").OnElements("input")
	p.AllowURLSchemes("http", "https", "mailto")
	p.RequireNoReferrerOnLinks(true)
	return &Renderer{md: md, policy: p}
}

// Render converts src to sanitized HTML.
func (r *Renderer) Render(src string) (template.HTML, error) {
	var buf bytes.Buffer
	if err := r.md.Convert([]byte(src), &buf); err != nil {
		return "", err
	}
	// The policy removes all unsafe content. Thus the conversion to
	// template.HTML is safe.
	return template.HTML(r.policy.SanitizeBytes(buf.Bytes())), nil // #nosec G203 -- sanitized.
}

type resolver struct{ prefix string }

func (r resolver) ResolveWikilink(n *wikilink.Node) ([]byte, error) {
	var dest []byte
	if len(n.Target) > 0 {
		slug := Slugify(string(n.Target))
		if slug == "" {
			return nil, nil
		}
		dest = append(dest, r.prefix...)
		dest = append(dest, slug...)
	}
	if len(n.Fragment) > 0 {
		dest = append(dest, '#')
		dest = append(dest, Slugify(string(n.Fragment))...)
	}
	return dest, nil
}

// MaxSlugLength is the largest length of a slug in bytes.
const MaxSlugLength = 100

// Slugify converts a page title to a slug. A slug contains only the
// characters a-z, 0-9 and "-". The function removes diacritics, for
// example "Översikt" becomes "oversikt". The result is empty if the title
// has no letters or digits.
func Slugify(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range norm.NFKD.String(title) {
		switch {
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(unicode.ToLower(r))
		case unicode.Is(unicode.Mn, r):
			// Ignore combining marks (the diacritics after NFKD).
		default:
			dash = true
		}
		if b.Len() >= MaxSlugLength {
			break
		}
	}
	return strings.TrimRight(b.String()[:min(b.Len(), MaxSlugLength)], "-")
}

var slugRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ValidSlug reports whether s is a valid slug.
func ValidSlug(s string) bool {
	return len(s) <= MaxSlugLength && slugRE.MatchString(s)
}
