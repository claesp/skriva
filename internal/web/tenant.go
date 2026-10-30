package web

import (
	"html/template"
	"net/http"
	"strings"

	"github.com/clpe/skriva/internal/rbac"
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
	mux.Handle("GET /{tenant}/{$}", s.require(rbac.DocRead, s.handleTenantHome))
	mux.Handle("GET /{tenant}/search", s.require(rbac.DocRead, s.handleSearch))
}

func (s *Server) handleTenantHome(w http.ResponseWriter, r *http.Request) {
	t := reqInfoFrom(r.Context()).Tenant
	spaces, err := s.store.Spaces(r.Context(), t.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "tenant", t.Name, spaces)
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
	var spaceID int64
	if ri.Space != nil {
		spaceID = ri.Space.ID
	}
	var results []store.SearchResult
	if q != "" {
		var err error
		results, err = s.store.Search(r.Context(), ri.Tenant.ID, spaceID, q, searchPageSize)
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
