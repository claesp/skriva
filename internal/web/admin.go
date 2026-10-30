package web

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/clpe/skriva/internal/rbac"
	"github.com/clpe/skriva/internal/store"
)

var roleNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

func (s *Server) adminRoutes(mux *http.ServeMux) {
	mux.Handle("GET /admin/{$}", http.RedirectHandler("/admin/users", http.StatusSeeOther))
	mux.Handle("GET /admin/users", s.require(permAdmin, s.handleAdminUsers))
	mux.Handle("GET /admin/users/{id}", s.require(permAdmin, s.handleAdminUser))
	mux.Handle("POST /admin/users/{id}/disable", s.require(permAdmin, s.handleAdminUserDisable(true)))
	mux.Handle("POST /admin/users/{id}/enable", s.require(permAdmin, s.handleAdminUserDisable(false)))

	mux.Handle("GET /admin/groups", s.require(permAdmin, s.handleAdminGroups))
	mux.Handle("POST /admin/groups", s.require(permAdmin, s.handleAdminGroupCreate))
	mux.Handle("GET /admin/groups/{id}", s.require(permAdmin, s.handleAdminGroup))
	mux.Handle("POST /admin/groups/{id}/delete", s.require(permAdmin, s.handleAdminGroupDelete))
	mux.Handle("POST /admin/groups/{id}/members", s.require(permAdmin, s.handleAdminMemberAdd))
	mux.Handle("POST /admin/groups/{id}/members/{user}/remove", s.require(permAdmin, s.handleAdminMemberRemove))

	mux.Handle("GET /admin/roles", s.require(permAdmin, s.handleAdminRoles))
	mux.Handle("POST /admin/roles", s.require(permAdmin, s.handleAdminRoleCreate))
	mux.Handle("POST /admin/roles/{id}/delete", s.require(permAdmin, s.handleAdminRoleDelete))

	mux.Handle("GET /admin/bindings", s.require(permAdmin, s.handleAdminBindings))
	mux.Handle("POST /admin/bindings", s.require(permAdmin, s.handleAdminBindingCreate))
	mux.Handle("POST /admin/bindings/{id}/delete", s.require(permAdmin, s.handleAdminBindingDelete))
}

func pathID(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	return id, err == nil && id > 0
}

func formID(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PostFormValue(name), 10, 64)
	return id, err == nil && id > 0
}

// storeResult handles the error from a store call that changes data. It
// returns true if the handler can continue.
func (s *Server) storeResult(w http.ResponseWriter, r *http.Request, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, store.ErrNotFound):
		s.notFound(w, r)
	case errors.Is(err, store.ErrConflict):
		s.renderError(w, r, http.StatusConflict, "The name is in use already.")
	default:
		s.serverError(w, r, err)
	}
	return false
}

func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.Users(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "admin-users", "Users", users)
}

func (s *Server) handleAdminUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		s.notFound(w, r)
		return
	}
	ctx := r.Context()
	user, err := s.store.User(ctx, id)
	if err != nil {
		s.storeResult(w, r, err)
		return
	}
	groups, err := s.store.UserGroups(ctx, id)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	perms, err := s.store.UserPermissions(ctx, id, s.opts.DefaultRole)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	bindings, err := s.store.Bindings(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	var own []store.Binding
	for _, b := range bindings {
		if b.UserID == id {
			own = append(own, b)
		}
	}
	roles, err := s.store.Roles(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "admin-user", user.Name(), struct {
		User        *store.User
		Groups      []store.Group
		Permissions []rbac.Permission
		Bindings    []store.Binding
		Roles       []store.Role
		DefaultRole string
		IsSelf      bool
	}{user, groups, perms.List(), own, roles, s.opts.DefaultRole, id == sessionFrom(ctx).User.ID})
}

func (s *Server) handleAdminUserDisable(disabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(r, "id")
		if !ok {
			s.notFound(w, r)
			return
		}
		if disabled && id == sessionFrom(r.Context()).User.ID {
			s.badRequest(w, r, "You cannot disable your own account.")
			return
		}
		if !s.storeResult(w, r, s.store.SetUserDisabled(r.Context(), id, disabled)) {
			return
		}
		s.log.Info("user changed", "target_user_id", id, "disabled", disabled,
			"user_id", sessionFrom(r.Context()).User.ID)
		http.Redirect(w, r, "/admin/users/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
	}
}

func (s *Server) handleAdminGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.store.Groups(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "admin-groups", "Groups", groups)
}

func (s *Server) handleAdminGroupCreate(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PostFormValue("name"))
	desc := strings.TrimSpace(r.PostFormValue("description"))
	if name == "" || utf8.RuneCountInString(name) > 200 || utf8.RuneCountInString(desc) > 500 {
		s.badRequest(w, r, "The group name is necessary and must have 200 characters or less.")
		return
	}
	id, err := s.store.CreateLocalGroup(r.Context(), name, desc)
	if !s.storeResult(w, r, err) {
		return
	}
	http.Redirect(w, r, "/admin/groups/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (s *Server) handleAdminGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		s.notFound(w, r)
		return
	}
	ctx := r.Context()
	group, err := s.store.Group(ctx, id)
	if err != nil {
		s.storeResult(w, r, err)
		return
	}
	members, err := s.store.GroupMembers(ctx, id)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	var users []*store.User
	if group.Source == store.GroupLocal {
		if users, err = s.store.Users(ctx); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	bindings, err := s.store.Bindings(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	var own []store.Binding
	for _, b := range bindings {
		if b.GroupID == id {
			own = append(own, b)
		}
	}
	roles, err := s.store.Roles(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "admin-group", "Group "+group.Name, struct {
		Group    *store.Group
		Members  []*store.User
		Users    []*store.User
		Bindings []store.Binding
		Roles    []store.Role
	}{group, members, users, own, roles})
}

func (s *Server) handleAdminGroupDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		s.notFound(w, r)
		return
	}
	if !s.storeResult(w, r, s.store.DeleteGroup(r.Context(), id)) {
		return
	}
	http.Redirect(w, r, "/admin/groups", http.StatusSeeOther)
}

func (s *Server) handleAdminMemberAdd(w http.ResponseWriter, r *http.Request) {
	gid, ok1 := pathID(r, "id")
	uid, ok2 := formID(r, "user_id")
	if !ok1 || !ok2 {
		s.badRequest(w, r, "Select a user.")
		return
	}
	if !s.storeResult(w, r, s.store.AddLocalGroupMember(r.Context(), gid, uid)) {
		return
	}
	http.Redirect(w, r, "/admin/groups/"+strconv.FormatInt(gid, 10), http.StatusSeeOther)
}

func (s *Server) handleAdminMemberRemove(w http.ResponseWriter, r *http.Request) {
	gid, ok1 := pathID(r, "id")
	uid, ok2 := pathID(r, "user")
	if !ok1 || !ok2 {
		s.notFound(w, r)
		return
	}
	if !s.storeResult(w, r, s.store.RemoveLocalGroupMember(r.Context(), gid, uid)) {
		return
	}
	http.Redirect(w, r, "/admin/groups/"+strconv.FormatInt(gid, 10), http.StatusSeeOther)
}

func (s *Server) handleAdminRoles(w http.ResponseWriter, r *http.Request) {
	roles, err := s.store.Roles(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "admin-roles", "Roles", struct {
		Roles       []store.Role
		Permissions []rbac.Permission
		DefaultRole string
	}{roles, rbac.All, s.opts.DefaultRole})
}

func (s *Server) handleAdminRoleCreate(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PostFormValue("name"))
	desc := strings.TrimSpace(r.PostFormValue("description"))
	if !roleNameRE.MatchString(name) || utf8.RuneCountInString(desc) > 500 {
		s.badRequest(w, r, "The role name must start with a-z and contain only a-z, 0-9 and \"-\".")
		return
	}
	var perms []rbac.Permission
	for _, p := range r.PostForm["permission"] {
		perm := rbac.Permission(p)
		if !perm.Valid() {
			s.badRequest(w, r, "The form contains an unknown permission.")
			return
		}
		perms = append(perms, perm)
	}
	if !s.storeResult(w, r, s.store.CreateRole(r.Context(), name, desc, perms)) {
		return
	}
	s.log.Info("role created", "role", name, "user_id", sessionFrom(r.Context()).User.ID)
	http.Redirect(w, r, "/admin/roles", http.StatusSeeOther)
}

func (s *Server) handleAdminRoleDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		s.notFound(w, r)
		return
	}
	if !s.storeResult(w, r, s.store.DeleteRole(r.Context(), id)) {
		return
	}
	http.Redirect(w, r, "/admin/roles", http.StatusSeeOther)
}

func (s *Server) handleAdminBindings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	bindings, err := s.store.Bindings(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	roles, err := s.store.Roles(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	groups, err := s.store.Groups(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	users, err := s.store.Users(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "admin-bindings", "Role bindings", struct {
		Bindings []store.Binding
		Roles    []store.Role
		Groups   []store.Group
		Users    []*store.User
	}{bindings, roles, groups, users})
}

// handleAdminBindingCreate gives a role to a user or a group. The form
// field "subject" has the form "user:ID" or "group:ID".
func (s *Server) handleAdminBindingCreate(w http.ResponseWriter, r *http.Request) {
	role := r.PostFormValue("role")
	kind, idText, _ := strings.Cut(r.PostFormValue("subject"), ":")
	id, err := strconv.ParseInt(idText, 10, 64)
	if !roleNameRE.MatchString(role) || err != nil || id <= 0 {
		s.badRequest(w, r, "Select a role and a user or a group.")
		return
	}
	switch kind {
	case "user":
		err = s.store.BindRoleToUser(r.Context(), role, id)
	case "group":
		err = s.store.BindRoleToGroup(r.Context(), role, id)
	default:
		s.badRequest(w, r, "Select a role and a user or a group.")
		return
	}
	if !s.storeResult(w, r, err) {
		return
	}
	s.log.Info("role binding created", "role", role, "subject", kind, "subject_id", id,
		"user_id", sessionFrom(r.Context()).User.ID)
	http.Redirect(w, r, safeReturnTo(r.PostFormValue("return_to")), http.StatusSeeOther)
}

func (s *Server) handleAdminBindingDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		s.notFound(w, r)
		return
	}
	if !s.storeResult(w, r, s.store.DeleteBinding(r.Context(), id)) {
		return
	}
	s.log.Info("role binding deleted", "binding_id", id, "user_id", sessionFrom(r.Context()).User.ID)
	http.Redirect(w, r, safeReturnTo(r.PostFormValue("return_to")), http.StatusSeeOther)
}
