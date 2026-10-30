package web

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/clpe/skriva/internal/rbac"
	"github.com/clpe/skriva/internal/store"
)

var roleNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// adminRoutes adds the tenant administration routes.
func (s *Server) adminRoutes(mux *http.ServeMux) {
	p := "/{tenant}/admin"
	m := rbac.TenantManage
	mux.Handle("GET "+p+"/{$}", s.require(m, s.handleAdminHome))
	mux.Handle("GET "+p+"/users", s.require(m, s.handleAdminUsers))
	mux.Handle("GET "+p+"/users/{id}", s.require(m, s.handleAdminUser))
	mux.Handle("POST "+p+"/users/{id}/disable", s.require(m, s.handleAdminUserDisable(true)))
	mux.Handle("POST "+p+"/users/{id}/enable", s.require(m, s.handleAdminUserDisable(false)))

	mux.Handle("GET "+p+"/groups", s.require(m, s.handleAdminGroups))
	mux.Handle("POST "+p+"/groups", s.require(m, s.handleAdminGroupCreate))
	mux.Handle("GET "+p+"/groups/{id}", s.require(m, s.handleAdminGroup))
	mux.Handle("POST "+p+"/groups/{id}/delete", s.require(m, s.handleAdminGroupDelete))
	mux.Handle("POST "+p+"/groups/{id}/members", s.require(m, s.handleAdminMemberAdd))
	mux.Handle("POST "+p+"/groups/{id}/members/{user}/remove", s.require(m, s.handleAdminMemberRemove))

	mux.Handle("GET "+p+"/roles", s.require(m, s.handleAdminRoles))
	mux.Handle("POST "+p+"/roles", s.require(m, s.handleAdminRoleCreate))
	mux.Handle("POST "+p+"/roles/{id}/delete", s.require(m, s.handleAdminRoleDelete))

	mux.Handle("GET "+p+"/bindings", s.require(m, s.handleAdminBindings))
	mux.Handle("POST "+p+"/bindings", s.require(m, s.handleAdminBindingCreate))
	mux.Handle("POST "+p+"/bindings/{id}/delete", s.require(m, s.handleAdminBindingDelete))

	sm := rbac.SpaceManage
	mux.Handle("GET "+p+"/spaces", s.require(sm, s.handleAdminSpaces))
	mux.Handle("POST "+p+"/spaces", s.require(sm, s.handleAdminSpaceCreate))
	mux.Handle("GET "+p+"/spaces/{slug}", s.require(sm, s.handleAdminSpace))
	mux.Handle("POST "+p+"/spaces/{slug}", s.require(sm, s.handleAdminSpaceUpdate))
	mux.Handle("POST "+p+"/spaces/{slug}/delete", s.require(sm, s.handleAdminSpaceDelete))
	// The access of a space is part of the access control of the tenant.
	// Thus it needs tenant.manage, not space.manage.
	mux.Handle("GET "+p+"/spaces/{slug}/access", s.require(m, s.handleAdminSpaceAccess))
	mux.Handle("POST "+p+"/spaces/{slug}/access", s.require(m, s.handleAdminSpaceDefaultRole))
}

func pathID(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	return id, err == nil && id > 0
}

func formID(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PostFormValue(name), 10, 64)
	return id, err == nil && id > 0
}

// tenantOf returns the tenant of the request.
func tenantOf(r *http.Request) *store.Tenant { return reqInfoFrom(r.Context()).Tenant }

// adminPath returns the URL path of the tenant administration.
func adminPath(r *http.Request) string { return tenantPath(tenantOf(r)) + "/admin" }

func (s *Server) handleAdminHome(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, adminPath(r)+"/users", http.StatusSeeOther)
}

func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.Users(r.Context(), tenantOf(r).ID)
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
	t := tenantOf(r)
	user, err := s.store.User(ctx, t.ID, id)
	if err != nil {
		s.storeResult(w, r, err)
		return
	}
	groups, err := s.store.UserGroups(ctx, t.ID, id)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	perms, err := s.store.UserPermissions(ctx, t.ID, id)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	bindings, err := s.store.Bindings(ctx, t.ID)
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
	roles, err := s.store.Roles(ctx, t.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	spaces, err := s.store.Spaces(ctx, t.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	all, err := s.store.AllSpacePermissions(ctx, t.ID, id)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	spacePerms := make([]spacePermissions, len(spaces))
	for i, sp := range spaces {
		spacePerms[i] = spacePermissions{Space: sp, Permissions: all[sp.ID].List()}
	}
	s.render(w, r, http.StatusOK, "admin-user", user.Name(), struct {
		User             *store.User
		Groups           []store.Group
		Permissions      []rbac.Permission
		SpacePermissions []spacePermissions
		Bindings         []store.Binding
		Roles            []store.Role
		Spaces           []store.Space
		IsSelf           bool
	}{user, groups, perms.List(), spacePerms, own, roles, spaces, id == reqInfoFrom(ctx).Session.User.ID})
}

func (s *Server) handleAdminUserDisable(disabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(r, "id")
		if !ok {
			s.notFound(w, r)
			return
		}
		sess := reqInfoFrom(r.Context()).Session
		if disabled && id == sess.User.ID {
			s.badRequest(w, r, "You cannot disable your own account.")
			return
		}
		if !s.storeResult(w, r, s.store.SetUserDisabled(r.Context(), tenantOf(r).ID, id, disabled)) {
			return
		}
		s.log.Info("user changed", "tenant", tenantOf(r).Code, "target_user_id", id,
			"disabled", disabled, "user_id", sess.User.ID)
		http.Redirect(w, r, adminPath(r)+"/users/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
	}
}

func (s *Server) handleAdminGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.store.Groups(r.Context(), tenantOf(r).ID)
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
	id, err := s.store.CreateLocalGroup(r.Context(), tenantOf(r).ID, name, desc)
	if !s.storeResult(w, r, err) {
		return
	}
	http.Redirect(w, r, adminPath(r)+"/groups/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (s *Server) handleAdminGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		s.notFound(w, r)
		return
	}
	ctx := r.Context()
	t := tenantOf(r)
	group, err := s.store.Group(ctx, t.ID, id)
	if err != nil {
		s.storeResult(w, r, err)
		return
	}
	members, err := s.store.GroupMembers(ctx, t.ID, id)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	var users []*store.User
	if group.Source == store.GroupLocal {
		if users, err = s.store.Users(ctx, t.ID); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	bindings, err := s.store.Bindings(ctx, t.ID)
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
	roles, err := s.store.Roles(ctx, t.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	spaces, err := s.store.Spaces(ctx, t.ID)
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
		Spaces   []store.Space
	}{group, members, users, own, roles, spaces})
}

func (s *Server) handleAdminGroupDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		s.notFound(w, r)
		return
	}
	if !s.storeResult(w, r, s.store.DeleteGroup(r.Context(), tenantOf(r).ID, id)) {
		return
	}
	http.Redirect(w, r, adminPath(r)+"/groups", http.StatusSeeOther)
}

func (s *Server) handleAdminMemberAdd(w http.ResponseWriter, r *http.Request) {
	gid, ok1 := pathID(r, "id")
	uid, ok2 := formID(r, "user_id")
	if !ok1 || !ok2 {
		s.badRequest(w, r, "Select a user.")
		return
	}
	if !s.storeResult(w, r, s.store.AddLocalGroupMember(r.Context(), tenantOf(r).ID, gid, uid)) {
		return
	}
	http.Redirect(w, r, adminPath(r)+"/groups/"+strconv.FormatInt(gid, 10), http.StatusSeeOther)
}

func (s *Server) handleAdminMemberRemove(w http.ResponseWriter, r *http.Request) {
	gid, ok1 := pathID(r, "id")
	uid, ok2 := pathID(r, "user")
	if !ok1 || !ok2 {
		s.notFound(w, r)
		return
	}
	if !s.storeResult(w, r, s.store.RemoveLocalGroupMember(r.Context(), tenantOf(r).ID, gid, uid)) {
		return
	}
	http.Redirect(w, r, adminPath(r)+"/groups/"+strconv.FormatInt(gid, 10), http.StatusSeeOther)
}

func (s *Server) handleAdminRoles(w http.ResponseWriter, r *http.Request) {
	roles, err := s.store.Roles(r.Context(), tenantOf(r).ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "admin-roles", "Roles", struct {
		Roles       []store.Role
		Permissions []rbac.Permission
	}{roles, rbac.All})
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
	if !s.storeResult(w, r, s.store.CreateRole(r.Context(), tenantOf(r).ID, name, desc, perms)) {
		return
	}
	s.log.Info("role created", "tenant", tenantOf(r).Code, "role", name,
		"user_id", reqInfoFrom(r.Context()).Session.User.ID)
	http.Redirect(w, r, adminPath(r)+"/roles", http.StatusSeeOther)
}

func (s *Server) handleAdminRoleDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		s.notFound(w, r)
		return
	}
	if !s.storeResult(w, r, s.store.DeleteRole(r.Context(), tenantOf(r).ID, id)) {
		return
	}
	http.Redirect(w, r, adminPath(r)+"/roles", http.StatusSeeOther)
}

func (s *Server) handleAdminBindings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	t := tenantOf(r)
	bindings, err := s.store.Bindings(ctx, t.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	roles, err := s.store.Roles(ctx, t.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	groups, err := s.store.Groups(ctx, t.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	users, err := s.store.Users(ctx, t.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	spaces, err := s.store.Spaces(ctx, t.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "admin-bindings", "Role bindings", struct {
		Bindings []store.Binding
		Roles    []store.Role
		Groups   []store.Group
		Users    []*store.User
		Spaces   []store.Space
	}{bindings, roles, groups, users, spaces})
}

// handleAdminBindingCreate gives a role to a user or a group. The form
// field "subject" has the form "user:ID" or "group:ID".
func (s *Server) handleAdminBindingCreate(w http.ResponseWriter, r *http.Request) {
	t := tenantOf(r)
	role := r.PostFormValue("role")
	kind, idText, _ := strings.Cut(r.PostFormValue("subject"), ":")
	id, err := strconv.ParseInt(idText, 10, 64)
	if !roleNameRE.MatchString(role) || err != nil || id <= 0 {
		s.badRequest(w, r, "Select a role and a user or a group.")
		return
	}
	// An empty "space" field gives a binding for all spaces.
	var spaceID *int64
	if slug := r.PostFormValue("space"); slug != "" {
		if !spaceSlugRE.MatchString(slug) {
			s.badRequest(w, r, "The space is not valid.")
			return
		}
		sp, err := s.store.Space(r.Context(), t.ID, slug)
		if err != nil {
			s.storeResult(w, r, err)
			return
		}
		spaceID = &sp.ID
	}
	switch kind {
	case "user":
		err = s.store.BindRoleToUser(r.Context(), t.ID, role, id, spaceID)
	case "group":
		err = s.store.BindRoleToGroup(r.Context(), t.ID, role, id, spaceID)
	default:
		s.badRequest(w, r, "Select a role and a user or a group.")
		return
	}
	if !s.storeResult(w, r, err) {
		return
	}
	s.log.Info("role binding created", "tenant", t.Code, "role", role, "subject", kind, "subject_id", id,
		"space", r.PostFormValue("space"), "user_id", reqInfoFrom(r.Context()).Session.User.ID)
	http.Redirect(w, r, safeReturnTo(r.PostFormValue("return_to"), adminPath(r)), http.StatusSeeOther)
}

func (s *Server) handleAdminBindingDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		s.notFound(w, r)
		return
	}
	if !s.storeResult(w, r, s.store.DeleteBinding(r.Context(), tenantOf(r).ID, id)) {
		return
	}
	s.log.Info("role binding deleted", "tenant", tenantOf(r).Code, "binding_id", id,
		"user_id", reqInfoFrom(r.Context()).Session.User.ID)
	http.Redirect(w, r, safeReturnTo(r.PostFormValue("return_to"), adminPath(r)), http.StatusSeeOther)
}

// spaceForm reads and checks the fields of a space form. It returns an
// error text, or "".
func spaceForm(r *http.Request, withSlug bool) (slug, name, desc, msg string) {
	slug = strings.TrimSpace(r.PostFormValue("slug"))
	name = strings.TrimSpace(r.PostFormValue("name"))
	desc = strings.TrimSpace(r.PostFormValue("description"))
	switch {
	case withSlug && !spaceSlugRE.MatchString(slug):
		msg = "The space code must have exactly 8 characters from a-z and 0-9."
	case name == "" || utf8.RuneCountInString(name) > 200:
		msg = "The name is necessary and must have 200 characters or less."
	case utf8.RuneCountInString(desc) > 500:
		msg = "The description must have 500 characters or less."
	}
	return slug, name, desc, msg
}

func (s *Server) handleAdminSpaces(w http.ResponseWriter, r *http.Request) {
	spaces, err := s.store.Spaces(r.Context(), tenantOf(r).ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "admin-spaces", "Spaces", spaces)
}

func (s *Server) handleAdminSpaceCreate(w http.ResponseWriter, r *http.Request) {
	slug, name, desc, msg := spaceForm(r, true)
	if msg != "" {
		s.badRequest(w, r, msg)
		return
	}
	t := tenantOf(r)
	if !s.storeResult(w, r, s.store.CreateSpace(r.Context(), t.ID, slug, name, desc)) {
		return
	}
	s.log.Info("space created", "tenant", t.Code, "space", slug,
		"user_id", reqInfoFrom(r.Context()).Session.User.ID)
	http.Redirect(w, r, tenantPath(t)+"/"+slug+"/", http.StatusSeeOther)
}

func (s *Server) handleAdminSpace(w http.ResponseWriter, r *http.Request) {
	sp, err := s.store.Space(r.Context(), tenantOf(r).ID, r.PathValue("slug"))
	if err != nil {
		s.storeResult(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "admin-space", "Space "+sp.Name, sp)
}

func (s *Server) handleAdminSpaceUpdate(w http.ResponseWriter, r *http.Request) {
	_, name, desc, msg := spaceForm(r, false)
	if msg != "" {
		s.badRequest(w, r, msg)
		return
	}
	slug := r.PathValue("slug")
	if !s.storeResult(w, r, s.store.UpdateSpace(r.Context(), tenantOf(r).ID, slug, name, desc)) {
		return
	}
	http.Redirect(w, r, adminPath(r)+"/spaces", http.StatusSeeOther)
}

// handleAdminSpaceDelete removes a space. The user must type the space
// code to confirm, because the action removes all documents.
func (s *Server) handleAdminSpaceDelete(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if r.PostFormValue("confirm") != slug {
		s.badRequest(w, r, "Type the space code to confirm the deletion.")
		return
	}
	t := tenantOf(r)
	if !s.storeResult(w, r, s.store.DeleteSpace(r.Context(), t.ID, slug)) {
		return
	}
	s.log.Info("space deleted", "tenant", t.Code, "space", slug,
		"user_id", reqInfoFrom(r.Context()).Session.User.ID)
	http.Redirect(w, r, adminPath(r)+"/spaces", http.StatusSeeOther)
}

// spacePermissions is one row of the per-space permission table.
type spacePermissions struct {
	Space       store.Space
	Permissions []rbac.Permission
}

// handleAdminSpaceAccess shows the default role and the role bindings of a
// space.
func (s *Server) handleAdminSpaceAccess(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	t := tenantOf(r)
	sp, err := s.store.Space(ctx, t.ID, r.PathValue("slug"))
	if err != nil {
		s.storeResult(w, r, err)
		return
	}
	bindings, err := s.store.Bindings(ctx, t.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	var own []store.Binding
	for _, b := range bindings {
		if b.SpaceID == sp.ID {
			own = append(own, b)
		}
	}
	roles, err := s.store.Roles(ctx, t.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	groups, err := s.store.Groups(ctx, t.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	users, err := s.store.Users(ctx, t.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	// The default role select uses "-" for "same as the tenant".
	defaultRole := "-"
	if sp.DefaultRole != nil {
		defaultRole = *sp.DefaultRole
	}
	s.render(w, r, http.StatusOK, "admin-space-access", "Access to "+sp.Name, struct {
		Space       *store.Space
		DefaultRole string
		Bindings    []store.Binding
		Roles       []store.Role
		Groups      []store.Group
		Users       []*store.User
	}{sp, defaultRole, own, roles, groups, users})
}

// handleAdminSpaceDefaultRole sets the default role of a space. The value
// "-" means "same as the tenant", and "" means no default role.
func (s *Server) handleAdminSpaceDefaultRole(w http.ResponseWriter, r *http.Request) {
	t := tenantOf(r)
	slug := r.PathValue("slug")
	var role *string
	switch v := r.PostFormValue("default_role"); {
	case v == "-":
	case v == "" || roleNameRE.MatchString(v):
		role = &v
	default:
		s.badRequest(w, r, "The role is not valid.")
		return
	}
	if !s.storeResult(w, r, s.store.SetSpaceDefaultRole(r.Context(), t.ID, slug, role)) {
		return
	}
	s.log.Info("space default role changed", "tenant", t.Code, "space", slug,
		"user_id", reqInfoFrom(r.Context()).Session.User.ID)
	http.Redirect(w, r, adminPath(r)+"/spaces/"+slug+"/access", http.StatusSeeOther)
}
