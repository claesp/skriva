package web

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"

	"github.com/claesp/skriva/internal/diagram"
	"github.com/claesp/skriva/internal/doc"
	"github.com/claesp/skriva/internal/store"
)

// diagramKeyRE matches the result of diagram.Key.
var diagramKeyRE = regexp.MustCompile(`^[0-9a-f]{32}$`)

// renderDiagram returns the SVG image of a Mermaid diagram. The error is a
// *diagram.SourceError or diagram.ErrUnavailable. Its message is for the
// user.
func (s *Server) renderDiagram(r *http.Request, source string) ([]byte, error) {
	if s.opts.Diagrams == nil {
		return nil, diagram.ErrUnavailable
	}
	svg, err := s.opts.Diagrams.Render(r.Context(), source)
	var se *diagram.SourceError
	switch {
	case errors.As(err, &se):
		return nil, se
	case err != nil:
		s.log.WarnContext(r.Context(), "cannot render a diagram", "error", err)
		return nil, diagram.ErrUnavailable
	}
	return svg, nil
}

// diagramURLs returns the Diagram option of doc.HTMLWith for version
// version of node n. Zero is the current version. The page refers to
// the images, because the page CSP does not permit images in data URIs.
// The function renders each diagram, thus the page can show the errors,
// and the image request usually finds the image in the cache.
func (s *Server) diagramURLs(r *http.Request, n *store.Node, version int) func(string) (string, error) {
	if s.opts.Diagrams == nil {
		return nil
	}
	return func(source string) (string, error) {
		if _, err := s.renderDiagram(r, source); err != nil {
			return "", err
		}
		u := base(r) + "/diagram/" + n.PublicID + "/" + diagram.Key(source)
		if version > 0 {
			u += "?v=" + strconv.Itoa(version)
		}
		return u, nil
	}
}

// handleDiagram sends the image of a diagram in a document. The URL
// contains the key of the text of the diagram. The query parameter "v"
// selects an old version. The image is not stored. The function finds
// the text in the document and makes the image again if the cache does
// not have it. Thus the URL works on each server of a cluster.
func (s *Server) handleDiagram(w http.ResponseWriter, r *http.Request, n *store.Node) {
	key := r.PathValue("key")
	if !diagramKeyRE.MatchString(key) || n.IsFolder() {
		s.notFound(w, r)
		return
	}
	body := n.Body
	if v := r.URL.Query().Get("v"); v != "" {
		version, ok := parseVersion(v)
		if !ok || version < 1 {
			s.notFound(w, r)
			return
		}
		rev, err := s.store.Revision(r.Context(), n.ID, version)
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		body = rev.Body
	}
	d, err := doc.Parse([]byte(body), nil)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	for _, c := range d.Diagrams() {
		source, _ := c.DiagramSource()
		if diagram.Key(source) != key {
			continue
		}
		svg, err := s.renderDiagram(r, source)
		var se *diagram.SourceError
		switch {
		case errors.As(err, &se):
			s.notFound(w, r)
			return
		case err != nil:
			http.Error(w, "The server cannot show the diagram now.", http.StatusServiceUnavailable)
			return
		}
		h := w.Header()
		h.Set("Content-Type", svgType)
		// The SVG of mmdr can contain links. The sandbox blocks them, also
		// when a user opens the image directly.
		h.Set("Content-Security-Policy", fileSVGSecurityPolicy)
		h.Set("Content-Disposition", "inline")
		h.Set("Cache-Control", "private, max-age=3600")
		_, _ = w.Write(svg)
		return
	}
	s.notFound(w, r)
}
