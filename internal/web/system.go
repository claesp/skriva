package web

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/claesp/skriva/internal/i18n"
	"github.com/claesp/skriva/internal/store"
)

// Limits for the tenant settings.
const (
	minSessionTTL = time.Minute
	maxSessionTTL = 30 * 24 * time.Hour
	maxDocKiB     = 1024
)

func (s *Server) systemRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/login", s.handleLogin)
	mux.HandleFunc("GET /admin/auth/callback", s.handleCallback)
	mux.HandleFunc("GET /admin/signed-out", s.handleSignedOut)
	mux.Handle("POST /admin/logout", s.requireSystem(s.handleLogout))
	mux.Handle("GET /admin/{$}", s.requireSystem(s.handleTenants))
	mux.Handle("GET /admin/audit", s.requireSystem(s.handleAudit))
	mux.Handle("GET /admin/profile", s.requireSystem(s.handleProfile))
	mux.Handle("POST /admin/profile", s.requireSystem(s.handleProfileUpdate))
	mux.Handle("GET /admin/tenants/new", s.requireSystem(s.handleTenantNewForm))
	mux.Handle("POST /admin/tenants", s.requireSystem(s.handleTenantCreate))
	mux.Handle("GET /admin/tenants/{code}", s.requireSystem(s.handleTenant))
	mux.Handle("GET /admin/tenants/{code}/settings/{section}", s.requireSystem(s.handleTenantForm))
	mux.Handle("POST /admin/tenants/{code}/settings/{section}", s.requireSystem(s.handleTenantUpdate))
	mux.Handle("GET /admin/tenants/{code}/delete", s.requireSystem(s.handleTenantDeleteForm))
	mux.Handle("POST /admin/tenants/{code}/delete", s.requireSystem(s.handleTenantDelete))
	mux.Handle("GET /admin/tenants/{code}/spaces", s.requireSystem(s.handleTenantSpaces))
	mux.Handle("POST /admin/tenants/{code}/rotate-key", s.requireSystem(s.handleTenantRotateKey))
	mux.Handle("GET /admin/tenants/{code}/spaces/new", s.requireSystem(s.handleTenantSpaceNew))
	mux.Handle("POST /admin/tenants/{code}/spaces", s.requireSystem(s.handleTenantSpaceCreate))
}

func (s *Server) handleTenants(w http.ResponseWriter, r *http.Request) {
	tenants, err := s.store.Tenants(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderAdmin(w, r, http.StatusOK, "tenants", "system-tenants", s.tr(r).T("Tenants"), tenants)
}

// tenantForm is the data for the tenant settings form.
type tenantForm struct {
	IsNew bool
	// Section is the part of the settings that the form changes. A new
	// tenant has no section.
	Section   string
	IsDefault bool
	// Code is the value of the code field. Path is the current code of
	// the tenant for the URLs of the form, or "" for a new tenant.
	Code string
	Path string
	store.TenantSettings
	Scopes      string
	SessionTTL  string
	MaxDocKiB   int
	HasSecret   bool
	RedirectURI string
	LogoutURI   string
	Roles       []store.Role
	// UILanguages and ContentLanguages are the choices of the language
	// fields. SystemLanguage is the name of the default language of the
	// system.
	UILanguages      []i18n.Language
	ContentLanguages []i18n.Language
	SystemLanguage   string
	Error            string
	// Keys is the state of the data keys of the tenant. Only the page
	// of the tenant shows it.
	Keys *store.KeyInfo
	// Tab and Crumb are the current tab of the tenant pages and its name.
	Tab   string
	Crumb string
}

func (s *Server) newTenantForm(r *http.Request, t *store.Tenant) (*tenantForm, error) {
	f := &tenantForm{IsNew: t == nil}
	var tenantID int64
	if t == nil {
		f.TenantSettings = store.TenantSettings{
			OIDCScopes: []string{"openid", "profile", "email"}, OIDCGroupsClaim: "groups",
			DefaultRole: "viewer", SessionTTL: 12 * time.Hour, MaxDocBytes: 1 << 20,
		}
		// The content language of a new tenant is the default language
		// of the system, if it has a stemmer.
		if i18n.IsContent(s.opts.Language) {
			f.ContentLanguage = s.opts.Language
		}
	} else {
		tenantID = t.ID
		f.IsDefault = t.IsDefault
		f.Code, f.Path = t.Code, t.Code
		f.TenantSettings = t.TenantSettings
		f.HasSecret = t.HasClientSecret
	}
	f.Scopes = strings.Join(f.OIDCScopes, " ")
	f.SessionTTL = shortDuration(f.TenantSettings.SessionTTL)
	f.MaxDocKiB = f.MaxDocBytes / 1024
	f.UILanguages, f.ContentLanguages = i18n.UILanguages(), i18n.ContentLanguages()
	f.SystemLanguage = i18n.Name(s.opts.Language)
	roles, err := s.store.Roles(r.Context(), tenantID)
	if err != nil {
		return nil, err
	}
	f.Roles = roles
	f.setURIs(s)
	return f, nil
}

// shortDuration returns d without zero minutes and seconds, for example
// "12h" for 12h0m0s.
func shortDuration(d time.Duration) string {
	s := strings.TrimSuffix(d.String(), "m0s")
	if s != d.String() {
		s += "m"
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

func (f *tenantForm) setURIs(s *Server) {
	if f.Code != "" {
		f.RedirectURI = s.absURL("/" + f.Code + "/auth/callback")
		f.LogoutURI = s.absURL("/" + f.Code + "/signed-out")
	}
}

// tenantSection returns the name of a part of the tenant settings in the
// language of p, or "" if the part does not exist.
func tenantSection(p *i18n.Printer, section string) string {
	switch section {
	case "general":
		return p.T("General")
	case "idp":
		return p.T("Identity provider")
	case "access":
		return p.T("Access")
	case "languages":
		return p.T("Languages")
	case "limits":
		return p.T("Limits")
	}
	return ""
}

// readTenantForm reads and checks the fields of f.Section in the form.
// For a new tenant, it reads the code and the name. It returns an error
// text in the language of p, or "".
func (s *Server) readTenantForm(r *http.Request, p *i18n.Printer, f *tenantForm) string {
	get := func(k string) string { return strings.TrimSpace(r.PostFormValue(k)) }
	switch {
	case f.IsNew || f.Section == "general":
		f.Code = get("code")
		f.Name = get("name")
		if !f.IsNew {
			f.Disabled = r.PostFormValue("disabled") == "on"
		}
		switch {
		case f.Name == "" || utf8.RuneCountInString(f.Name) > 200:
			return p.T("The name is necessary and must have 200 characters or less.")
		case !s.validTenantCode(f.Code):
			return p.T(tenantCodeError)
		}
	case f.Section == "idp":
		f.OIDCIssuer = get("oidc_issuer")
		f.OIDCClientID = get("oidc_client_id")
		f.Scopes = get("oidc_scopes")
		f.OIDCScopes = strings.Fields(f.Scopes)
		f.OIDCGroupsClaim = get("oidc_groups_claim")
		switch {
		case !validIssuer(f.OIDCIssuer):
			return p.T("The issuer must be an https URL. An http URL is permitted only for localhost.")
		case len(f.OIDCClientID) > 200 || len(f.OIDCGroupsClaim) > 100:
			return p.T("A value is too long.")
		case len(r.PostFormValue("oidc_client_secret")) > 500:
			return p.T("The client secret is too long.")
		case len(f.OIDCScopes) > 20 || len(f.Scopes) > 500:
			return p.T("There are too many scopes.")
		}
		for _, sc := range f.OIDCScopes {
			for _, c := range sc {
				if c < 0x21 || c > 0x7e || c == '"' || c == '\\' {
					return p.T("A scope contains a character that is not permitted.")
				}
			}
		}
	case f.Section == "access":
		f.AdminGroup = get("admin_group")
		f.DefaultRole = get("default_role")
		switch {
		case utf8.RuneCountInString(f.AdminGroup) > 200:
			return p.T("A value is too long.")
		case f.DefaultRole != "" && !roleNameRE.MatchString(f.DefaultRole):
			return p.T("The default role is not valid.")
		}
	case f.Section == "languages":
		f.Language = get("language")
		f.ContentLanguage = get("content_language")
		switch {
		case f.Language != "" && !i18n.IsUI(f.Language):
			return p.T("The language is not valid.")
		case !i18n.IsContent(f.ContentLanguage):
			return p.T("The content language is not valid.")
		}
	case f.Section == "limits":
		f.SessionTTL = get("session_ttl")
		ttl, err := time.ParseDuration(f.SessionTTL)
		if err != nil || ttl < minSessionTTL || ttl > maxSessionTTL {
			return p.T("The session life must be a duration from 1m to 720h, for example 12h.")
		}
		f.TenantSettings.SessionTTL = ttl
		kib, err := strconv.Atoi(get("max_doc_kib"))
		if err != nil || kib < 1 || kib > maxDocKiB {
			return p.T("The maximum document size must be from 1 to 1024 KiB.")
		}
		f.MaxDocKiB = kib
		f.MaxDocBytes = kib * 1024
	}
	return ""
}

// validIssuer reports whether u is empty or a valid issuer URL.
func validIssuer(u string) bool {
	if u == "" {
		return true
	}
	p, err := url.Parse(u)
	if err != nil || p.Host == "" || p.User != nil || p.RawQuery != "" || p.Fragment != "" {
		return false
	}
	switch p.Scheme {
	case "https":
		return true
	case "http":
		host := p.Hostname()
		ip := net.ParseIP(host)
		return host == "localhost" || (ip != nil && ip.IsLoopback())
	}
	return false
}

func (s *Server) renderTenantForm(w http.ResponseWriter, r *http.Request, status int, f *tenantForm) {
	p := s.tr(r)
	title := p.T("New tenant")
	if !f.IsNew {
		title = tenantSection(p, f.Section)
	}
	s.renderAdmin(w, r, status, "tenants", "system-tenant", title, f)
}

func (s *Server) handleTenantNewForm(w http.ResponseWriter, r *http.Request) {
	f, err := s.newTenantForm(r, nil)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderTenantForm(w, r, http.StatusOK, f)
}

func (s *Server) handleTenantCreate(w http.ResponseWriter, r *http.Request) {
	f, err := s.newTenantForm(r, nil)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	p := s.tr(r)
	f.Error = s.readTenantForm(r, p, f)
	if f.Error != "" {
		s.renderTenantForm(w, r, http.StatusUnprocessableEntity, f)
		return
	}
	// The other settings have their default values. The administrator
	// changes them on the page of the tenant.
	t, err := s.store.CreateTenant(r.Context(), f.Code, f.TenantSettings, "")
	switch {
	case errors.Is(err, store.ErrConflict):
		f.Error = p.T("A tenant with this code exists already.")
	case errors.Is(err, store.ErrInvalid):
		f.Error = p.T("The default role does not exist.")
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	if f.Error != "" {
		s.renderTenantForm(w, r, http.StatusUnprocessableEntity, f)
		return
	}
	s.log.Info("tenant created", "tenant", t.Code, "user_id", reqInfoFrom(r.Context()).Session.User.ID)
	http.Redirect(w, r, "/admin/tenants/"+t.Code, http.StatusSeeOther)
}

// loadTenant loads the tenant from the {code} path value.
func (s *Server) loadTenant(w http.ResponseWriter, r *http.Request) (*store.Tenant, bool) {
	code := r.PathValue("code")
	if !store.ValidCode(code) {
		s.notFound(w, r)
		return nil, false
	}
	t, err := s.store.TenantByCode(r.Context(), code)
	if err != nil {
		s.storeResult(w, r, err)
		return nil, false
	}
	return t, true
}

// handleTenant shows the settings of a tenant.
func (s *Server) handleTenant(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadTenant(w, r)
	if !ok {
		return
	}
	f, err := s.newTenantForm(r, t)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if f.Keys, err = s.store.TenantKeyInfo(r.Context(), t.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderAdmin(w, r, http.StatusOK, "tenants", "system-tenant-view", s.tr(r).T("Tenant %s", t.Code), f)
}

// handleTenantRotateKey makes a new data key for the tenant. The cleanup
// task encrypts the old values again with the new key.
func (s *Server) handleTenantRotateKey(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadTenant(w, r)
	if !ok {
		return
	}
	v, err := s.store.RotateDataKey(r.Context(), t.ID)
	if !s.storeResult(w, r, err) {
		return
	}
	s.log.Info("data key rotated", "tenant", t.Code, "version", v,
		"user_id", reqInfoFrom(r.Context()).Session.User.ID)
	http.Redirect(w, r, "/admin/tenants/"+t.Code+"#encryption", http.StatusSeeOther)
}

// loadTenantSection loads the tenant and the form for the {section} path
// value.
func (s *Server) loadTenantSection(w http.ResponseWriter, r *http.Request) (*store.Tenant, *tenantForm, bool) {
	t, ok := s.loadTenant(w, r)
	if !ok {
		return nil, nil, false
	}
	section := r.PathValue("section")
	if tenantSection(s.tr(r), section) == "" {
		s.notFound(w, r)
		return nil, nil, false
	}
	f, err := s.newTenantForm(r, t)
	if err != nil {
		s.serverError(w, r, err)
		return nil, nil, false
	}
	f.Section = section
	return t, f, true
}

func (s *Server) handleTenantForm(w http.ResponseWriter, r *http.Request) {
	if _, f, ok := s.loadTenantSection(w, r); ok {
		s.renderTenantForm(w, r, http.StatusOK, f)
	}
}

// handleTenantUpdate changes one part of the tenant settings. The other
// settings do not change.
func (s *Server) handleTenantUpdate(w http.ResponseWriter, r *http.Request) {
	t, f, ok := s.loadTenantSection(w, r)
	if !ok {
		return
	}
	p := s.tr(r)
	f.Error = s.readTenantForm(r, p, f)
	if f.Error != "" {
		s.renderTenantForm(w, r, http.StatusUnprocessableEntity, f)
		return
	}
	// An empty secret field keeps the secret. The check box removes it.
	var newSecret *string
	if f.Section == "idp" {
		if v := r.PostFormValue("oidc_client_secret"); v != "" {
			newSecret = &v
		} else if r.PostFormValue("remove_secret") == "on" {
			empty := ""
			newSecret = &empty
		}
	}
	nt, err := s.store.UpdateTenant(r.Context(), t.ID, f.Code, f.TenantSettings, newSecret)
	switch {
	case errors.Is(err, store.ErrInvalid):
		f.Error = p.T("The default role does not exist.")
	case errors.Is(err, store.ErrConflict):
		f.Error = p.T("A tenant with this code exists already.")
	}
	if f.Error != "" {
		s.renderTenantForm(w, r, http.StatusUnprocessableEntity, f)
		return
	}
	if !s.storeResult(w, r, err) {
		return
	}
	s.log.Info("tenant changed", "tenant", nt.Code, "old_code", t.Code, "section", f.Section,
		"secret_changed", newSecret != nil, "user_id", reqInfoFrom(r.Context()).Session.User.ID)
	http.Redirect(w, r, "/admin/tenants/"+nt.Code+"#"+f.Section, http.StatusSeeOther)
}

func (s *Server) handleTenantDeleteForm(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadTenant(w, r)
	if !ok {
		return
	}
	if t.IsDefault {
		s.badRequest(w, r, s.tr(r).T("You cannot delete the default tenant."))
		return
	}
	tr := s.tr(r)
	p := "/admin/tenants/" + t.Code
	s.renderAdmin(w, r, http.StatusOK, "tenants", "confirm-delete", tr.T("Delete tenant"), confirmDelete{
		Heading: tr.T("Delete tenant %s", t.Name),
		Message: tr.T("Do you want to delete the tenant? This deletes all users, groups, roles, spaces and documents of the tenant."),
		Action:  p + "/delete", Code: t.Code, Cancel: p, Parent: t.Code, CSRF: csrfOf(r),
		RootURL: "/admin/", RootLabel: tr.T("Tenants"),
	})
}

// handleTenantDelete removes a tenant. The user must type the tenant code
// to confirm, because the action removes all data of the tenant.
func (s *Server) handleTenantDelete(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadTenant(w, r)
	if !ok {
		return
	}
	if r.PostFormValue("confirm") != t.Code {
		s.badRequest(w, r, s.tr(r).T("Type the tenant code to confirm the deletion."))
		return
	}
	err := s.store.DeleteTenant(r.Context(), t.ID)
	if errors.Is(err, store.ErrInvalid) {
		s.badRequest(w, r, s.tr(r).T("You cannot delete the default tenant."))
		return
	}
	if !s.storeResult(w, r, err) {
		return
	}
	s.log.Info("tenant deleted", "tenant", t.Code, "user_id", reqInfoFrom(r.Context()).Session.User.ID)
	http.Redirect(w, r, "/admin/", http.StatusSeeOther)
}

func (s *Server) handleTenantSpaces(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadTenant(w, r)
	if !ok {
		return
	}
	spaces, err := s.store.Spaces(r.Context(), t.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	p := s.tr(r)
	s.renderAdmin(w, r, http.StatusOK, "tenants", "system-tenant-spaces", p.T("Spaces of %s", t.Code), struct {
		Code      string
		IsDefault bool
		Tab       string
		Crumb     string
		Spaces    []store.Space
	}{t.Code, t.IsDefault, "spaces", p.T("Spaces"), spaces})
}

func (s *Server) handleTenantSpaceNew(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadTenant(w, r)
	if !ok {
		return
	}
	s.renderAdmin(w, r, http.StatusOK, "tenants", "system-space-new", s.tr(r).T("New space"), t.Code)
}

func (s *Server) handleTenantSpaceCreate(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadTenant(w, r)
	if !ok {
		return
	}
	slug, name, desc, msg := s.spaceForm(r)
	if msg != "" {
		s.badRequest(w, r, msg)
		return
	}
	if !s.storeResult(w, r, s.store.CreateSpace(r.Context(), t.ID, slug, name, desc)) {
		return
	}
	s.log.Info("space created", "tenant", t.Code, "space", slug,
		"user_id", reqInfoFrom(r.Context()).Session.User.ID)
	http.Redirect(w, r, "/admin/tenants/"+t.Code+"/spaces", http.StatusSeeOther)
}
