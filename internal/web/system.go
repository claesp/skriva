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

	"github.com/clpe/skriva/internal/store"
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
	mux.Handle("GET /admin/tenants/new", s.requireSystem(s.handleTenantNewForm))
	mux.Handle("POST /admin/tenants", s.requireSystem(s.handleTenantCreate))
	mux.Handle("GET /admin/tenants/{code}", s.requireSystem(s.handleTenantForm))
	mux.Handle("POST /admin/tenants/{code}", s.requireSystem(s.handleTenantUpdate))
	mux.Handle("GET /admin/tenants/{code}/delete", s.requireSystem(s.handleTenantDeleteForm))
	mux.Handle("POST /admin/tenants/{code}/delete", s.requireSystem(s.handleTenantDelete))
	mux.Handle("GET /admin/tenants/{code}/spaces", s.requireSystem(s.handleTenantSpaces))
	mux.Handle("GET /admin/tenants/{code}/spaces/new", s.requireSystem(s.handleTenantSpaceNew))
	mux.Handle("POST /admin/tenants/{code}/spaces", s.requireSystem(s.handleTenantSpaceCreate))
}

func (s *Server) handleTenants(w http.ResponseWriter, r *http.Request) {
	tenants, err := s.store.Tenants(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderAdmin(w, r, http.StatusOK, "tenants", "system-tenants", "Tenants", tenants)
}

// tenantForm is the data for the tenant settings form.
type tenantForm struct {
	IsNew     bool
	IsDefault bool
	Code      string
	store.TenantSettings
	Scopes      string
	SessionTTL  string
	MaxDocKiB   int
	HasSecret   bool
	RedirectURI string
	LogoutURI   string
	Roles       []store.Role
	Error       string
	// Tab is the current tab of the tenant pages.
	Tab string
}

func (s *Server) newTenantForm(r *http.Request, t *store.Tenant) (*tenantForm, error) {
	f := &tenantForm{IsNew: t == nil, Tab: "settings"}
	var tenantID int64
	if t == nil {
		f.TenantSettings = store.TenantSettings{
			OIDCScopes: []string{"openid", "profile", "email"}, OIDCGroupsClaim: "groups",
			DefaultRole: "viewer", SessionTTL: 12 * time.Hour, MaxDocBytes: 1 << 20,
		}
	} else {
		tenantID = t.ID
		f.IsDefault = t.IsDefault
		f.Code = t.Code
		f.TenantSettings = t.TenantSettings
		f.HasSecret = t.HasClientSecret
	}
	f.Scopes = strings.Join(f.OIDCScopes, " ")
	f.SessionTTL = f.TenantSettings.SessionTTL.String()
	f.MaxDocKiB = f.MaxDocBytes / 1024
	roles, err := s.store.Roles(r.Context(), tenantID)
	if err != nil {
		return nil, err
	}
	f.Roles = roles
	f.setURIs(s)
	return f, nil
}

func (f *tenantForm) setURIs(s *Server) {
	if f.Code != "" {
		f.RedirectURI = s.absURL("/" + f.Code + "/auth/callback")
		f.LogoutURI = s.absURL("/" + f.Code + "/signed-out")
	}
}

// readTenantForm reads and checks the settings in the form. It returns an
// error text, or "".
func readTenantForm(r *http.Request, f *tenantForm) string {
	get := func(k string) string { return strings.TrimSpace(r.PostFormValue(k)) }
	f.Name = get("name")
	f.Disabled = r.PostFormValue("disabled") == "on"
	f.OIDCIssuer = get("oidc_issuer")
	f.OIDCClientID = get("oidc_client_id")
	f.Scopes = get("oidc_scopes")
	f.OIDCScopes = strings.Fields(f.Scopes)
	f.OIDCGroupsClaim = get("oidc_groups_claim")
	f.AdminGroup = get("admin_group")
	f.DefaultRole = get("default_role")
	f.SessionTTL = get("session_ttl")

	switch {
	case f.Name == "" || utf8.RuneCountInString(f.Name) > 200:
		return "The name is necessary and must have 200 characters or less."
	case !validIssuer(f.OIDCIssuer):
		return "The issuer must be an https URL. An http URL is permitted only for localhost."
	case len(f.OIDCClientID) > 200 || len(f.OIDCGroupsClaim) > 100 || utf8.RuneCountInString(f.AdminGroup) > 200:
		return "A value is too long."
	case len(r.PostFormValue("oidc_client_secret")) > 500:
		return "The client secret is too long."
	case len(f.OIDCScopes) > 20 || len(f.Scopes) > 500:
		return "There are too many scopes."
	case f.DefaultRole != "" && !roleNameRE.MatchString(f.DefaultRole):
		return "The default role is not valid."
	}
	for _, sc := range f.OIDCScopes {
		for _, c := range sc {
			if c < 0x21 || c > 0x7e || c == '"' || c == '\\' {
				return "A scope contains a character that is not permitted."
			}
		}
	}
	ttl, err := time.ParseDuration(f.SessionTTL)
	if err != nil || ttl < minSessionTTL || ttl > maxSessionTTL {
		return "The session life must be a duration from 1m to 720h, for example 12h."
	}
	f.TenantSettings.SessionTTL = ttl
	kib, err := strconv.Atoi(get("max_doc_kib"))
	if err != nil || kib < 1 || kib > maxDocKiB {
		return "The maximum document size must be from 1 to 1024 KiB."
	}
	f.MaxDocKiB = kib
	f.MaxDocBytes = kib * 1024
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
	title := "New tenant"
	if !f.IsNew {
		title = "Tenant " + f.Code
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
	f.Code = strings.TrimSpace(r.PostFormValue("code"))
	f.Error = readTenantForm(r, f)
	if f.Error == "" && !tenantCodeRE.MatchString(f.Code) {
		f.Error = "The tenant code must have exactly 4 characters from a-z and 0-9."
	}
	if f.Error != "" {
		s.renderTenantForm(w, r, http.StatusUnprocessableEntity, f)
		return
	}
	t, err := s.store.CreateTenant(r.Context(), f.Code, f.TenantSettings, r.PostFormValue("oidc_client_secret"))
	switch {
	case errors.Is(err, store.ErrConflict):
		f.Error = "A tenant with this code exists already."
	case errors.Is(err, store.ErrInvalid):
		f.Error = "The default role does not exist."
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
	if !tenantCodeRE.MatchString(code) {
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

func (s *Server) handleTenantForm(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadTenant(w, r)
	if !ok {
		return
	}
	f, err := s.newTenantForm(r, t)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderTenantForm(w, r, http.StatusOK, f)
}

func (s *Server) handleTenantUpdate(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadTenant(w, r)
	if !ok {
		return
	}
	f, err := s.newTenantForm(r, t)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if f.Error = readTenantForm(r, f); f.Error != "" {
		s.renderTenantForm(w, r, http.StatusUnprocessableEntity, f)
		return
	}
	// An empty secret field keeps the secret. The check box removes it.
	var newSecret *string
	if v := r.PostFormValue("oidc_client_secret"); v != "" {
		newSecret = &v
	} else if r.PostFormValue("remove_secret") == "on" {
		empty := ""
		newSecret = &empty
	}
	_, err = s.store.UpdateTenant(r.Context(), t.ID, f.TenantSettings, newSecret)
	if errors.Is(err, store.ErrInvalid) {
		f.Error = "The default role does not exist."
		s.renderTenantForm(w, r, http.StatusUnprocessableEntity, f)
		return
	}
	if !s.storeResult(w, r, err) {
		return
	}
	s.log.Info("tenant changed", "tenant", t.Code, "secret_changed", newSecret != nil,
		"user_id", reqInfoFrom(r.Context()).Session.User.ID)
	http.Redirect(w, r, "/admin/tenants/"+t.Code, http.StatusSeeOther)
}

func (s *Server) handleTenantDeleteForm(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadTenant(w, r)
	if !ok {
		return
	}
	if t.IsDefault {
		s.badRequest(w, r, "You cannot delete the default tenant.")
		return
	}
	p := "/admin/tenants/" + t.Code
	s.renderAdmin(w, r, http.StatusOK, "tenants", "confirm-delete", "Delete tenant", confirmDelete{
		Heading: "Delete tenant " + t.Name,
		Message: "Do you want to delete the tenant? This deletes all users, groups, roles, spaces and documents of the tenant.",
		Action:  p + "/delete", Code: t.Code, Cancel: p, Parent: t.Code, CSRF: csrfOf(r),
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
		s.badRequest(w, r, "Type the tenant code to confirm the deletion.")
		return
	}
	err := s.store.DeleteTenant(r.Context(), t.ID)
	if errors.Is(err, store.ErrInvalid) {
		s.badRequest(w, r, "You cannot delete the default tenant.")
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
	s.renderAdmin(w, r, http.StatusOK, "tenants", "system-tenant-spaces", "Spaces of "+t.Code, struct {
		Code      string
		IsDefault bool
		Tab       string
		Spaces    []store.Space
	}{t.Code, t.IsDefault, "spaces", spaces})
}

func (s *Server) handleTenantSpaceNew(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadTenant(w, r)
	if !ok {
		return
	}
	s.renderAdmin(w, r, http.StatusOK, "tenants", "system-space-new", "New space", t.Code)
}

func (s *Server) handleTenantSpaceCreate(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadTenant(w, r)
	if !ok {
		return
	}
	slug, name, desc, msg := spaceForm(r, true)
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
