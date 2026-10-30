package web

import (
	"html/template"
	"net/http"
	"slices"
	"strings"

	"github.com/clpe/skriva/internal/store"
)

const (
	maxSearchLen   = 200
	searchPageSize = 50
)

func (s *Server) tenantRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{tenant}/login", s.handleLogin)
	mux.HandleFunc("GET /{tenant}/auth/callback", s.handleCallback)
	mux.HandleFunc("GET /{tenant}/signed-out", s.handleSignedOut)
	mux.Handle("POST /{tenant}/logout", s.require("", s.handleLogout))
	// The space permissions apply to these pages. Thus they require only a
	// session, and show only the spaces that the user can read.
	mux.Handle("GET /{tenant}/{$}", s.require("", s.handleTenantHome))
	mux.Handle("GET /{tenant}/search", s.require("", s.handleSearch))
}

func (s *Server) handleTenantHome(w http.ResponseWriter, r *http.Request) {
	ri := reqInfoFrom(r.Context())
	spaces, err := s.store.Spaces(r.Context(), ri.Tenant.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	readable, err := s.readable(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	var visible []store.Space
	for _, sp := range spaces {
		if slices.Contains(readable, sp.ID) {
			visible = append(visible, sp)
		}
	}
	s.render(w, r, http.StatusOK, "tenant", ri.Tenant.Name, visible)
}

// readable returns the IDs of the spaces that the user of the request can
// read.
func (s *Server) readable(r *http.Request) ([]int64, error) {
	ri := reqInfoFrom(r.Context())
	return s.store.ReadableSpaces(r.Context(), ri.Tenant.ID, ri.Session.User.ID)
}

// handleSearch searches in the space of the request, or in all spaces of
// the tenant if the request has no space.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	ri := reqInfoFrom(r.Context())
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) > maxSearchLen {
		s.badRequest(w, r, "The search text is too long.")
		return
	}
	var results []store.SearchResult
	if q != "" {
		// In a space, the route checked doc.read for the space. Outside a
		// space, search only the spaces that the user can read.
		spaceIDs := []int64{}
		if ri.Space != nil {
			spaceIDs = append(spaceIDs, ri.Space.ID)
		} else {
			var err error
			if spaceIDs, err = s.readable(r); err != nil {
				s.serverError(w, r, err)
				return
			}
		}
		var err error
		results, err = s.store.Search(r.Context(), ri.Tenant.ID, spaceIDs, q, searchPageSize)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	s.render(w, r, http.StatusOK, "search", "Search", struct {
		Query   string
		Results []searchResult
	}{q, toSearchResults(results)})
}

type searchResult struct {
	store.SearchResult
	SnippetHTML template.HTML
}

// toSearchResults converts the « and » marks from the database to <mark>
// elements. The function escapes all other text.
func toSearchResults(in []store.SearchResult) []searchResult {
	out := make([]searchResult, len(in))
	for i, p := range in {
		esc := template.HTMLEscapeString(p.Snippet)
		esc = strings.ReplaceAll(esc, "«", "<mark>")
		esc = strings.ReplaceAll(esc, "»", "</mark>")
		out[i] = searchResult{SearchResult: p, SnippetHTML: template.HTML(esc)} // #nosec G203 -- esc is escaped above.
	}
	return out
}
