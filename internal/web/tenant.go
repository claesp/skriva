package web

import (
	"net/http"
	"slices"
	"strings"

	"github.com/claesp/skriva/internal/rbac"
	"github.com/claesp/skriva/internal/store"
)

const (
	maxSearchLen   = 200
	searchPageSize = 50
)

func (s *Server) tenantRoutes(mux routeMux) {
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
	visible, err := s.visibleSpaces(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "tenant", reqInfoFrom(r.Context()).Tenant.Name, visible)
}

// visibleSpaces returns the spaces of the tenant that the user of the
// request can read.
func (s *Server) visibleSpaces(r *http.Request) ([]store.Space, error) {
	spaces, err := s.store.Spaces(r.Context(), reqInfoFrom(r.Context()).Tenant.ID)
	if err != nil {
		return nil, err
	}
	readable, err := s.readable(r)
	if err != nil {
		return nil, err
	}
	var visible []store.Space
	for _, sp := range spaces {
		if slices.Contains(readable, sp.ID) {
			visible = append(visible, sp)
		}
	}
	return visible, nil
}

// readable returns the IDs of the spaces that the user of the request can
// read.
func (s *Server) readable(r *http.Request) ([]int64, error) {
	ri := reqInfoFrom(r.Context())
	return s.store.ReadableSpaces(r.Context(), ri.Tenant.ID, ri.Session.User.ID)
}

// readableResults removes the results that the user cannot read. Folders
// can have different permissions. Thus the function checks each node.
func (s *Server) readableResults(r *http.Request, in []store.SearchResult) ([]store.SearchResult, error) {
	ri := reqInfoFrom(r.Context())
	access := map[int64]*store.Access{}
	if ri.Space != nil {
		access[ri.Space.ID] = ri.Access
	}
	out := in[:0:0]
	for _, res := range in {
		acc, ok := access[res.SpaceID]
		if !ok {
			var err error
			if acc, err = s.store.Access(r.Context(), ri.Tenant.ID, res.SpaceID, ri.Session.User.ID); err != nil {
				return nil, err
			}
			access[res.SpaceID] = acc
		}
		if acc.Can(res.AccessPathID, rbac.DocRead) {
			out = append(out, res)
		}
	}
	return out, nil
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
		// In a space, search only that space. Outside a space, search
		// only the spaces that the user can read. readableResults then
		// removes the items that the user cannot read.
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
		if results, err = s.readableResults(r, results); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	// The column at the left side shows the tree of the space, or the
	// spaces of the tenant.
	var tree []*store.TreeNode
	var spaces []store.Space
	var err error
	if ri.Space != nil {
		tree, err = s.tree(r, 0, nil)
	} else {
		spaces, err = s.visibleSpaces(r)
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "search", "Search", struct {
		Query   string
		Results []store.SearchResult
		Tree    []*store.TreeNode
		Spaces  []store.Space
	}{q, results, tree, spaces})
}
