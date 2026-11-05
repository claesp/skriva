package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/claesp/skriva/internal/store"
)

// Limits of the fields of a classification level.
const (
	maxClassificationName = 60
	maxClassificationDesc = 500
)

// classificationForm is the data of the form for a classification level.
type classificationForm struct {
	store.Classification
	IsNew  bool
	Action string
	Error  string
}

func (s *Server) handleAdminClassifications(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.Classifications(r.Context(), tenantOf(r).ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderAdmin(w, r, http.StatusOK, "classifications", "admin-classifications", s.tr(r).T("Classifications"), list)
}

func (s *Server) handleAdminClassificationNew(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.Classifications(r.Context(), tenantOf(r).ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	// A new level comes after the highest level.
	pos := 1
	if len(list) > 0 {
		pos = list[len(list)-1].Position + 1
	}
	s.renderAdmin(w, r, http.StatusOK, "classifications", "admin-classification-form", s.tr(r).T("New classification"),
		classificationForm{IsNew: true, Action: adminPath(r) + "/classifications",
			Classification: store.Classification{Position: pos}})
}

func (s *Server) handleAdminClassificationEdit(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		s.notFound(w, r)
		return
	}
	c, err := s.store.Classification(r.Context(), tenantOf(r).ID, id)
	if !s.storeResult(w, r, err) {
		return
	}
	s.renderAdmin(w, r, http.StatusOK, "classifications", "admin-classification-form", s.tr(r).T("Change %s", c.Name),
		classificationForm{Action: adminPath(r) + "/classifications/" + strconv.FormatInt(id, 10), Classification: *c})
}

// handleAdminClassificationSave makes a level (POST to the list) or
// changes a level (POST to the level).
func (s *Server) handleAdminClassificationSave(w http.ResponseWriter, r *http.Request) {
	f := classificationForm{Action: r.URL.Path}
	if v := r.PathValue("id"); v != "" {
		id, ok := pathID(r, "id")
		if !ok {
			s.notFound(w, r)
			return
		}
		f.ID = id
	} else {
		f.IsNew = true
	}
	f.Name = strings.TrimSpace(r.PostFormValue("name"))
	f.Description = strings.TrimSpace(r.PostFormValue("description"))
	f.IsDefault = r.PostFormValue("default") == "on"
	pos, err := strconv.Atoi(r.PostFormValue("position"))
	f.Position = pos
	p := s.tr(r)
	switch {
	case f.Name == "" || utf8.RuneCountInString(f.Name) > maxClassificationName || !utf8.ValidString(f.Name):
		f.Error = p.T("The name is necessary. The maximum is 60 characters.")
	case utf8.RuneCountInString(f.Description) > maxClassificationDesc || !utf8.ValidString(f.Description):
		f.Error = p.T("The description is too long.")
	case err != nil || pos < 0 || pos > 1000:
		f.Error = p.T("The order must be a number from 0 to 1000.")
	}
	title := p.T("New classification")
	if !f.IsNew {
		title = p.T("Change %s", f.Name)
	}
	if f.Error == "" {
		_, err = s.store.SaveClassification(r.Context(), tenantOf(r).ID, f.Classification)
		if errors.Is(err, store.ErrConflict) {
			f.Error = p.T("A classification with this name exists already.")
		} else if !s.storeResult(w, r, err) {
			return
		}
	}
	if f.Error != "" {
		s.renderAdmin(w, r, http.StatusUnprocessableEntity, "classifications", "admin-classification-form", title, f)
		return
	}
	http.Redirect(w, r, adminPath(r)+"/classifications", http.StatusSeeOther)
}

func (s *Server) handleAdminClassificationDeleteForm(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		s.notFound(w, r)
		return
	}
	c, err := s.store.Classification(r.Context(), tenantOf(r).ID, id)
	if !s.storeResult(w, r, err) {
		return
	}
	if c.IsDefault {
		s.badRequest(w, r, s.tr(r).T("You cannot delete the default classification. Make a different classification the default first."))
		return
	}
	cp := adminPath(r) + "/classifications"
	p := s.tr(r)
	s.renderAdmin(w, r, http.StatusOK, "classifications", "confirm-delete", p.T("Delete classification"), confirmDelete{
		Heading: p.T("Delete classification %s", c.Name),
		Message: p.T("Do you want to delete the classification? You can delete only a classification that no document uses."),
		Action:  cp + "/" + strconv.FormatInt(id, 10) + "/delete", Cancel: cp, Parent: p.T("Classifications"), CSRF: csrfOf(r),
	})
}

func (s *Server) handleAdminClassificationDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		s.notFound(w, r)
		return
	}
	err := s.store.DeleteClassification(r.Context(), tenantOf(r).ID, id)
	switch {
	case errors.Is(err, store.ErrConflict):
		s.renderError(w, r, http.StatusConflict, s.tr(r).T(
			"Documents, spaces or folders use this classification. Give the documents a different "+
				"classification first. This includes the documents in the trash: restore them, or delete them "+
				"permanently. Remove the classification from the limits of the spaces and folders."))
		return
	case errors.Is(err, store.ErrInvalid):
		s.badRequest(w, r, s.tr(r).T("You cannot delete the default classification."))
		return
	}
	if !s.storeResult(w, r, err) {
		return
	}
	http.Redirect(w, r, adminPath(r)+"/classifications", http.StatusSeeOther)
}
