package web

import (
	"bytes"
	"errors"
	"mime"
	"net/http"
	"strings"

	"github.com/claesp/skriva/internal/doc"
	"github.com/claesp/skriva/internal/export"
	"github.com/claesp/skriva/internal/i18n"
	"github.com/claesp/skriva/internal/store"
)

// handleExportPage shows the export formats of a document. A folder has
// no text, thus it has no export.
func (s *Server) handleExportPage(w http.ResponseWriter, r *http.Request, n *store.Node) {
	if n.IsFolder() {
		s.notFound(w, r)
		return
	}
	p := s.tr(r)
	page, err := s.loadNodePage(r, n, "export", p.T("Export"))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	formats := make([]export.Format, len(s.opts.Formats))
	for i, f := range s.opts.Formats {
		f.Description = formatText(p, f.Description)
		f.Unavailable = formatText(p, f.Unavailable)
		formats[i] = f
	}
	s.render(w, r, http.StatusOK, "export", p.T("Export %s", n.Title), struct {
		nodePage
		Formats []export.Format
	}{page, formats})
}

// formatText translates a text of an export format. The texts are in the
// package export. A text without a case here stays in English.
func formatText(p *i18n.Printer, s string) string {
	switch s {
	case "One web page with the text and the images. The metadata contains " +
		"the title, the classification and the version. A browser can show the file " +
		"without Skriva, but the links refer to Skriva.":
		return p.T("One web page with the text and the images. The metadata contains " +
			"the title, the classification and the version. A browser can show the file " +
			"without Skriva, but the links refer to Skriva.")
	case "GitHub Flavored Markdown. The front matter contains the title, " +
		"the classification and the version. The formats of Markdown and of the editor " +
		"are not the same, thus the file can look different from the page.":
		return p.T("GitHub Flavored Markdown. The front matter contains the title, " +
			"the classification and the version. The formats of Markdown and of the editor " +
			"are not the same, thus the file can look different from the page.")
	case "A document for print, with A4 pages and the images. Each page shows " +
		"the classification. The metadata contains the title and the classification.":
		return p.T("A document for print, with A4 pages and the images. Each page shows " +
			"the classification. The metadata contains the title and the classification.")
	case "The server does not have the Typst program, thus it cannot make PDF files.":
		return p.T("The server does not have the Typst program, thus it cannot make PDF files.")
	}
	return s
}

// handleExport sends the export of a document as a file to download.
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request, n *store.Node) {
	f, data, ok := s.exportFile(w, r, n)
	if !ok {
		return
	}
	name := n.Slug
	if name == "" {
		name = n.PublicID
	}
	h := w.Header()
	h.Set("Content-Type", f.ContentType)
	// The slug and the public ID contain only a-z, 0-9 and "-".
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name + f.Extension}))
	_, _ = w.Write(data)
}

// handleExportText shows the export of a document as plain text in the
// browser. Only text formats permit this.
func (s *Server) handleExportText(w http.ResponseWriter, r *http.Request, n *store.Node) {
	f, data, ok := s.exportFile(w, r, n)
	if !ok {
		return
	}
	if !f.Text {
		s.notFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(data)
}

// exportFile makes the export of node n in the format of the request. If
// ok is false, the function sent an error page.
func (s *Server) exportFile(w http.ResponseWriter, r *http.Request, n *store.Node) (f export.Format, data []byte, ok bool) {
	f, found := s.opts.Formats.Lookup(r.PathValue("format"))
	if !found || !f.Available() || n.IsFolder() {
		s.notFound(w, r)
		return f, nil, false
	}
	body, err := doc.Parse([]byte(n.Body), nil)
	if err != nil {
		s.serverError(w, r, err)
		return f, nil, false
	}
	site := strings.TrimSuffix(s.opts.BaseURL.String(), "/")
	b := site + base(r)
	d := &export.Document{
		Title:          n.Title,
		Classification: n.Classification,
		// The label is in the language of the user. Lang is the language
		// of the content.
		ClassificationLabel: s.tr(r).T("Classification"),
		Lang:                reqInfoFrom(r.Context()).Tenant.ContentLanguage,
		Version:             n.Version,
		UpdatedAt:           n.UpdatedAt,
		URL:                 site + docPath(base(r), n.PublicID, n.Slug),
		Body:                body,
		Links:               doc.Links{Wiki: b + "/w/", Files: b + "/files/", Doc: b + "/d/", Site: site},
		Image: func(id string) (*export.Image, error) {
			f, err := s.readableFile(r, id)
			if errors.Is(err, store.ErrNotFound) {
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			return &export.Image{ContentType: f.ContentType, Data: f.Data}, nil
		},
	}
	if s.opts.Diagrams != nil {
		d.Diagram = func(source string) ([]byte, error) { return s.renderDiagram(r, source) }
	}
	var buf bytes.Buffer
	if err := f.Write(r.Context(), &buf, d); err != nil {
		s.serverError(w, r, err)
		return f, nil, false
	}
	return f, buf.Bytes(), true
}
