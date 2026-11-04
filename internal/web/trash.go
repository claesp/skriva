package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/claesp/skriva/internal/rbac"
	"github.com/claesp/skriva/internal/store"
)

// trashHandler is a handler for one item in the trash.
type trashHandler func(w http.ResponseWriter, r *http.Request, t *store.TrashItem)

// canSeeTrashItem reports whether the user can see an item in the trash.
// The user must be able to read and delete the item at its old place.
func canSeeTrashItem(acc *store.Access, t *store.TrashItem) bool {
	return acc.Can(t.AccessPathID, rbac.DocRead) && acc.Can(t.AccessPathID, rbac.DocDelete)
}

// trashed loads the item in the trash from the {id} path value and checks
// perm for the old place of the item. An item that the user cannot see is
// "not found".
func (s *Server) trashed(perm rbac.Permission, h trashHandler) http.Handler {
	return s.require("", func(w http.ResponseWriter, r *http.Request) {
		ri := reqInfoFrom(r.Context())
		id := strings.ToLower(r.PathValue("id"))
		if !store.ValidPublicID(id) {
			s.notFound(w, r)
			return
		}
		t, err := s.store.TrashedNode(r.Context(), ri.Space.ID, id)
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		if !canSeeTrashItem(ri.Access, t) {
			s.notFound(w, r)
			return
		}
		if !ri.Access.Can(t.AccessPathID, perm) {
			s.forbidden(w, r)
			return
		}
		h(w, r, t)
	})
}

// trashRow is one row of the trash page.
type trashRow struct {
	store.TrashItem
	// CanPurge is true if the user can delete the item permanently.
	CanPurge bool
}

// trashPage is the data of the trash page. Confirm is the item for which
// the page asks the user to confirm the permanent deletion, or nil.
type trashPage struct {
	spacePage
	Items   []trashRow
	Days    int
	Confirm *store.TrashItem
}

func (s *Server) loadTrashPage(r *http.Request) (trashPage, error) {
	tree, err := s.tree(r, 0, nil)
	if err != nil {
		return trashPage{}, err
	}
	return trashPage{spacePage: spacePage{Tree: tree, Trash: true}, Days: store.TrashDays}, nil
}

func (s *Server) handleTrash(w http.ResponseWriter, r *http.Request) {
	ri := reqInfoFrom(r.Context())
	if !ri.Access.Any(rbac.DocDelete) {
		s.forbidden(w, r)
		return
	}
	items, err := s.store.Trash(r.Context(), ri.Space.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	page, err := s.loadTrashPage(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	for _, t := range items {
		if !canSeeTrashItem(ri.Access, &t) {
			continue
		}
		if t.ParentAccessPathID != 0 && !ri.Access.Can(t.ParentAccessPathID, rbac.DocRead) {
			t.ParentTitle = "A folder that you cannot read"
		}
		page.Items = append(page.Items, trashRow{t, ri.Access.Can(t.AccessPathID, rbac.SpaceManage)})
	}
	s.render(w, r, http.StatusOK, "trash", "Trash", page)
}

func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request, t *store.TrashItem) {
	ri := reqInfoFrom(r.Context())
	err := s.store.RestoreNode(r.Context(), ri.Space.ID, t.PublicID)
	if errors.Is(err, store.ErrInvalid) {
		s.badRequest(w, r, "The old parent of the item is in the trash. Restore the parent first.")
		return
	}
	if !s.storeResult(w, r, err) {
		return
	}
	s.log.Info("node restored from the trash", "tenant", ri.Tenant.Code, "space", ri.Space.Slug,
		"node", t.PublicID, "user_id", ri.Session.User.ID)
	http.Redirect(w, r, base(r)+"/d/"+t.PublicID, http.StatusSeeOther)
}

func (s *Server) handlePurgeForm(w http.ResponseWriter, r *http.Request, t *store.TrashItem) {
	page, err := s.loadTrashPage(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	page.Confirm = t
	s.render(w, r, http.StatusOK, "trash", "Delete "+t.Title, page)
}

func (s *Server) handlePurge(w http.ResponseWriter, r *http.Request, t *store.TrashItem) {
	ri := reqInfoFrom(r.Context())
	if !s.storeResult(w, r, s.store.PurgeNode(r.Context(), ri.Space.ID, t.PublicID)) {
		return
	}
	s.log.Info("node deleted permanently", "tenant", ri.Tenant.Code, "space", ri.Space.Slug,
		"node", t.PublicID, "user_id", ri.Session.User.ID)
	http.Redirect(w, r, base(r)+"/trash", http.StatusSeeOther)
}
