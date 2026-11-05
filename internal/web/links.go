package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/claesp/skriva/internal/store"
)

// linkTarget is one item of the result of handleLinkTargets.
type linkTarget struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Kind  string `json:"kind"`
	// Parent is the title of the parent, or "" for the top level or for
	// a parent that the user cannot read.
	Parent string `json:"parent"`
}

// Limits of handleLinkTargets.
const (
	maxLinkQuery   = 100
	maxLinkTargets = 10
	// linkCandidates is the number of rows that the query reads before
	// the permission filter.
	linkCandidates = 50
)

// handleLinkTargets finds the folders and documents for a link in the
// editor. The query parameter q is a part of the title. The parameter id
// is the public ID of one node. The result is a JSON list. It contains
// only the nodes that the user can read.
func (s *Server) handleLinkTargets(w http.ResponseWriter, r *http.Request) {
	ri := reqInfoFrom(r.Context())
	q := r.URL.Query()
	var found []store.LinkTarget
	switch id := strings.ToLower(q.Get("id")); {
	case id != "":
		if !store.ValidPublicID(id) {
			s.badRequest(w, r, s.tr(r).T("The ID is not valid."))
			return
		}
		n, err := s.store.Node(r.Context(), ri.Space.ID, id)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			s.serverError(w, r, err)
			return
		}
		if err == nil {
			found = []store.LinkTarget{{NodeSummary: store.NodeSummary{
				ID: n.ID, PublicID: n.PublicID, Kind: n.Kind, Title: n.Title, AccessPathID: n.AccessPathID,
			}}}
		}
	default:
		text := strings.TrimSpace(q.Get("q"))
		if text == "" || !utf8.ValidString(text) || utf8.RuneCountInString(text) > maxLinkQuery {
			break
		}
		var err error
		if found, err = s.store.FindLinkTargets(r.Context(), ri.Space.ID, text, linkCandidates); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	out := []linkTarget{}
	for _, t := range found {
		if !canRead(r, t.AccessPathID) {
			continue
		}
		lt := linkTarget{ID: t.PublicID, Title: t.Title, Kind: t.Kind}
		if t.ParentPathID != 0 && canRead(r, t.ParentPathID) {
			lt.Parent = t.ParentTitle
		}
		out = append(out, lt)
		if len(out) == maxLinkTargets {
			break
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(out)
}
