package web

import (
	"bytes"
	"errors"
	"mime"
	"net/http"
	"strings"

	"github.com/clpe/skriva/internal/doc"
	"github.com/clpe/skriva/internal/export"
	"github.com/clpe/skriva/internal/store"
)

// handleExportPage shows the export formats of a document. A folder has
// no text, thus it has no export.
func (s *Server) handleExportPage(w http.ResponseWriter, r *http.Request, n *store.Node) {
	if n.IsFolder() {
		s.notFound(w, r)
		return
	}
	page, err := s.loadNodePage(r, n, "export", "Export")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "export", "Export "+n.Title, struct {
		nodePage
		Formats []export.Format
	}{page, export.Formats})
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
	f, found := export.Lookup(r.PathValue("format"))
	if !found || n.IsFolder() {
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
		Version:        n.Version,
		UpdatedAt:      n.UpdatedAt,
		URL:            site + docPath(base(r), n.PublicID, n.Slug),
		Body:           body,
		Links:          doc.Links{Wiki: b + "/w/", Files: b + "/files/", Doc: b + "/d/"},
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
	var buf bytes.Buffer
	if err := f.Write(&buf, d); err != nil {
		s.serverError(w, r, err)
		return f, nil, false
	}
	return f, buf.Bytes(), true
}
