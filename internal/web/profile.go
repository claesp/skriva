package web

import (
	"net/http"

	"github.com/claesp/skriva/internal/i18n"
	"github.com/claesp/skriva/internal/store"
)

// profilePage is the data of the profile page.
type profilePage struct {
	User *store.User
	// Action is the URL of the form.
	Action string
	// Languages are the choices of the language field. Default is the
	// name of the language that the user gets without a choice.
	Languages []i18n.Language
	Default   string
	Saved     bool
	Error     string
}

// profileAction returns the URL of the profile page of the request.
func profileAction(r *http.Request) string {
	if t := reqInfoFrom(r.Context()).Tenant; t != nil {
		return tenantPath(t) + "/profile"
	}
	return "/admin/profile"
}

// defaultLanguage returns the language of a user without a language
// setting: the language of the tenant, else of the system.
func (s *Server) defaultLanguage(r *http.Request) string {
	if t := reqInfoFrom(r.Context()).Tenant; t != nil && t.Language != "" {
		return t.Language
	}
	return s.opts.Language
}

func (s *Server) newProfilePage(r *http.Request) *profilePage {
	return &profilePage{
		User:      reqInfoFrom(r.Context()).Session.User,
		Action:    profileAction(r),
		Languages: i18n.UILanguages(),
		Default:   i18n.Name(s.defaultLanguage(r)),
		Saved:     r.URL.Query().Get("saved") == "1",
	}
}

func (s *Server) handleProfile(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "profile", s.tr(r).T("Profile"), s.newProfilePage(r))
}

func (s *Server) handleProfileUpdate(w http.ResponseWriter, r *http.Request) {
	sess := reqInfoFrom(r.Context()).Session
	lang := r.PostFormValue("language")
	if lang != "" && !i18n.IsUI(lang) {
		p := s.newProfilePage(r)
		p.Error = s.tr(r).T("The language is not valid.")
		s.render(w, r, http.StatusBadRequest, "profile", s.tr(r).T("Profile"), p)
		return
	}
	if !s.storeResult(w, r, s.store.SetProfile(r.Context(), sess.User.ID, store.Profile{Language: lang})) {
		return
	}
	http.Redirect(w, r, profileAction(r)+"?saved=1", http.StatusSeeOther)
}
