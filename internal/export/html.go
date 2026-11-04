package export

import (
	"context"
	"encoding/base64"
	"errors"
	"html/template"
	"io"
	"time"

	"github.com/claesp/skriva/internal/doc"
)

var htmlFormat = Format{
	Name:  "html",
	Label: "HTML",
	Description: "One web page with the text and the images. The metadata contains " +
		"the title, the classification and the version. A browser can show the file " +
		"without Skriva, but the links refer to Skriva.",
	Extension:   ".html",
	ContentType: "text/html; charset=utf-8",
	Text:        true,
	Write:       writeHTML,
}

// MaxEmbeddedBytes is the largest total size of the images in one HTML
// file. The file refers to the images after this limit with their URLs.
const MaxEmbeddedBytes = 32 << 20

// embeddedTypes are the image types that the HTML file can contain. The
// sanitizer of package doc permits only these types in data URIs.
var embeddedTypes = map[string]bool{
	"image/gif": true, "image/jpeg": true, "image/png": true, "image/svg+xml": true, "image/webp": true,
}

// htmlPage is the template of the exported file. The security policy
// permits no scripts and no external styles. Images can be data URIs or
// come from other sites.
var htmlPage = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src data: https: http:; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer">
<title>{{.Title}}</title>
{{with .Classification}}<meta name="classification" content="{{.}}">
{{end}}<meta name="version" content="{{.Version}}">
<meta name="dcterms.modified" content="{{.Updated}}">
{{with .URL}}<link rel="canonical" href="{{.}}">
{{end}}<meta name="generator" content="Skriva">
<style>{{.Style}}</style>
</head>
<body>
<main class="prose">
<h1>{{.Title}}</h1>
{{.Body}}
</main>
</body>
</html>
`))

// writeHTML writes the document as one HTML page. The page has the
// images in data URIs, because the server does not permit other sites
// to load its files. The headings of the text are h2 to h6, as on the
// page in Skriva. The title is the h1 element.
func writeHTML(_ context.Context, w io.Writer, d *Document) error {
	body := d.Body
	if body == nil {
		body = doc.Empty()
	}
	if d.Image != nil {
		if err := embedImages(body, d.Image); err != nil {
			return err
		}
	}
	return htmlPage.Execute(w, struct {
		Title          string
		Classification string
		Version        int
		Updated        string
		URL            string
		Style          template.CSS
		Body           template.HTML
	}{
		d.Title, d.Classification, d.Version,
		d.UpdatedAt.UTC().Format(time.RFC3339), d.URL, template.CSS(htmlStyle),
		body.HTMLWith(d.Links, doc.HTMLOptions{DataImages: true, Diagram: embedDiagrams(d.Diagram)}),
	})
}

// errDiagramSize tells that the file has no room for a diagram.
var errDiagramSize = errors.New("the diagrams are too large for the file")

// embedDiagrams returns the Diagram option of doc.HTMLWith. The images
// are data URIs. All diagrams together can have MaxEmbeddedBytes.
func embedDiagrams(get func(string) ([]byte, error)) func(string) (string, error) {
	if get == nil {
		return nil
	}
	total := 0
	return func(source string) (string, error) {
		svg, err := get(source)
		if err != nil {
			return "", err
		}
		if total+len(svg) > MaxEmbeddedBytes {
			return "", errDiagramSize
		}
		total += len(svg)
		return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString(svg), nil
	}
}

// embedImages changes the "file:" source of each image to a data URI.
// It changes body. An image that does not exist, an image of a type that
// is not permitted and the images after MaxEmbeddedBytes keep their
// sources. Each use of an image counts for MaxEmbeddedBytes, because the
// file contains the data again for each use.
func embedImages(body *doc.Node, get func(id string) (*Image, error)) error {
	type embedded struct {
		uri  string // "" if the image is not embedded
		size int
	}
	cache := map[string]embedded{}
	total := 0
	var err error
	body.MapImages(func(src string) string {
		id, ok := doc.FileID(src)
		if !ok || err != nil {
			return src
		}
		e, ok := cache[id]
		if !ok {
			var img *Image
			if img, err = get(id); err == nil && img != nil && embeddedTypes[img.ContentType] {
				e = embedded{"data:" + img.ContentType + ";base64," + base64.StdEncoding.EncodeToString(img.Data), len(img.Data)}
			}
			cache[id] = e
		}
		if e.uri == "" || total+e.size > MaxEmbeddedBytes {
			return src
		}
		total += e.size
		return e.uri
	})
	return err
}

// htmlStyle is the style sheet of the exported page. It is a short form
// of the style of the document page in Skriva. It uses the fonts of the
// system, because the file does not contain fonts.
const htmlStyle = `
:root { color-scheme: light dark; --fg: #1b1b1b; --bg: #fffff8; --muted: #6b6b6b; --line: #d8d8d0;
  --panel: #f2f2ea; --accent: #1f5fbf; }
@media (prefers-color-scheme: dark) {
  :root { --fg: #e6e6e0; --bg: #161614; --muted: #a0a09a; --line: #3a3a36; --panel: #22221f; --accent: #7aa7ff; }
  img { background: #fff; }
}
body { margin: 0; padding: 2rem 1rem; background: var(--bg); color: var(--fg); }
.prose { max-width: 42rem; margin: 0 auto; font-family: Charter, "Sitka Text", Cambria, Palatino, Georgia, serif;
  font-size: 1.125rem; line-height: 1.6; overflow-wrap: break-word; }
h1 { font-weight: 400; font-size: 2.25rem; line-height: 1.15; margin: 0 0 1.5rem; }
.prose p, .prose ul, .prose ol, .prose blockquote, .prose pre { margin: 0 0 1.25em; }
.prose [class*="level-"] { margin: 1.4em 0 0.35em; font-size: 1em; font-weight: 400; line-height: 1.25; }
.prose .level-1 { font-size: 1.6em; line-height: 1.15; margin: 1.6em 0 0.5em; }
.prose .level-2 { font-size: 1.35em; font-style: italic; margin-top: 1.6em; }
.prose .level-3 { font-size: 1.15em; font-weight: 600; margin-top: 1.5em; }
.prose .level-4 { font-weight: 700; }
.prose .level-5 { font-weight: 700; font-style: italic; }
.prose .level-6 { font-size: 0.85em; font-weight: 700; letter-spacing: 0.08em; text-transform: uppercase; }
.prose .level-7 { font-size: 0.85em; letter-spacing: 0.12em; text-transform: uppercase; }
.prose .level-8 { font-style: italic; color: var(--muted); }
.prose a { color: inherit; text-underline-offset: 0.15em; text-decoration-color: var(--accent); }
.prose ul, .prose ol { padding-left: 1.4em; }
.prose li + li { margin-top: 0.35em; }
.prose blockquote { margin-left: 0; padding-left: 1.2em; border-left: 2px solid var(--line); font-style: italic; }
.prose hr { margin: 2.5em 0; border: 0; border-top: 1px solid var(--line); }
.prose img { max-width: 100%; height: auto; }
.prose figure { margin: 0 0 1.25em; }
.prose figure.diagram img { display: block; }
.prose figure.diagram-error pre { margin-bottom: 0.4em; }
.prose figcaption { color: var(--muted); font-size: 0.85em; font-variant-ligatures: none; }
.prose code { font-family: ui-monospace, Menlo, Consolas, monospace; font-size: 0.82em; font-variant-ligatures: none; }
.prose :not(pre) > code { padding: 0.1em 0.3em; background: var(--panel); border-radius: 3px; }
.prose pre { overflow-x: auto; padding: 0.9em 1em; background: var(--panel); border-radius: 4px; line-height: 1.5; }
.prose .align-center { text-align: center; }
.prose .align-right { text-align: right; }
.prose ul.tasks { padding-left: 0; list-style: none; }
.prose li.task { display: flex; gap: 0.5em; align-items: baseline; }
.prose li.task > p { margin: 0; }
.prose li.task.done > p { color: var(--muted); text-decoration: line-through; }
.prose div.table { max-width: 100%; margin: 0 0 1.25em; overflow-x: auto; }
.prose table { border-collapse: collapse; font-family: system-ui, sans-serif; font-size: 0.8em; line-height: 1.45; }
.prose td, .prose th { padding: 0.35em 0.6em; border: 1px solid var(--line); vertical-align: top; text-align: left; }
.prose th { background: var(--panel); font-weight: 600; }
.prose td > p, .prose th > p { margin: 0; }
.prose p:empty::before { content: "\00a0"; }
@media print { body { padding: 0; background: #fff; color: #000; } }
`
