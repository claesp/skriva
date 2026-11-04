package web

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/claesp/skriva/internal/rbac"
	"github.com/claesp/skriva/internal/store"
)

var roleNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// adminRoutes adds the tenant administration routes. Each page has one
// function: a list, the data of one item, or one form. A GET request to a
// "new", "delete" or "default-role" path shows the form. The POST request
// does the action.
func (s *Server) adminRoutes(mux routeMux) {
	p := "/{tenant}/admin"
	m := rbac.TenantManage
	mux.Handle("GET "+p+"/{$}", s.require(m, s.handleAdminHome))
	mux.Handle("GET "+p+"/users", s.require(m, s.handleAdminUsers))
	mux.Handle("GET "+p+"/users/{id}", s.require(m, s.handleAdminUser))
	mux.Handle("GET "+p+"/users/{id}/bindings", s.require(m, s.handleAdminUserBindings))
	mux.Handle("GET "+p+"/users/{id}/permissions", s.require(m, s.handleAdminUserPermissions))
	mux.Handle("POST "+p+"/users/{id}/disable", s.require(m, s.handleAdminUserDisable(true)))
	mux.Handle("POST "+p+"/users/{id}/enable", s.require(m, s.handleAdminUserDisable(false)))

	mux.Handle("GET "+p+"/groups", s.require(m, s.handleAdminGroups))
	mux.Handle("GET "+p+"/groups/new", s.require(m, s.handleAdminGroupNew))
	mux.Handle("POST "+p+"/groups", s.require(m, s.handleAdminGroupCreate))
	mux.Handle("GET "+p+"/groups/{id}", s.require(m, s.handleAdminGroup))
	mux.Handle("GET "+p+"/groups/{id}/bindings", s.require(m, s.handleAdminGroupBindings))
	mux.Handle("GET "+p+"/groups/{id}/delete", s.require(m, s.handleAdminGroupDeleteForm))
	mux.Handle("POST "+p+"/groups/{id}/delete", s.require(m, s.handleAdminGroupDelete))
	mux.Handle("GET "+p+"/groups/{id}/members/new", s.require(m, s.handleAdminMemberNew))
	mux.Handle("POST "+p+"/groups/{id}/members", s.require(m, s.handleAdminMemberAdd))
	mux.Handle("POST "+p+"/groups/{id}/members/{user}/remove", s.require(m, s.handleAdminMemberRemove))

	mux.Handle("GET "+p+"/roles", s.require(m, s.handleAdminRoles))
	mux.Handle("GET "+p+"/roles/new", s.require(m, s.handleAdminRoleNew))
	mux.Handle("POST "+p+"/roles", s.require(m, s.handleAdminRoleCreate))
	mux.Handle("GET "+p+"/roles/{id}/delete", s.require(m, s.handleAdminRoleDeleteForm))
	mux.Handle("POST "+p+"/roles/{id}/delete", s.require(m, s.handleAdminRoleDelete))

	mux.Handle("GET "+p+"/classifications", s.require(m, s.handleAdminClassifications))
	mux.Handle("GET "+p+"/classifications/new", s.require(m, s.handleAdminClassificationNew))
	mux.Handle("POST "+p+"/classifications", s.require(m, s.handleAdminClassificationSave))
	mux.Handle("GET "+p+"/classifications/{id}/edit", s.require(m, s.handleAdminClassificationEdit))
	mux.Handle("POST "+p+"/classifications/{id}", s.require(m, s.handleAdminClassificationSave))
	mux.Handle("GET "+p+"/classifications/{id}/delete", s.require(m, s.handleAdminClassificationDeleteForm))
	mux.Handle("POST "+p+"/classifications/{id}/delete", s.require(m, s.handleAdminClassificationDelete))

	mux.Handle("GET "+p+"/recent", s.require(m, s.handleAdminRecent))
	mux.Handle("GET "+p+"/audit", s.require(m, s.handleAudit))
	mux.Handle("GET "+p+"/bindings", s.require(m, s.handleAdminBindings))
	mux.Handle("GET "+p+"/bindings/new", s.require(m, s.handleAdminBindingNew))
	mux.Handle("POST "+p+"/bindings", s.require(m, s.handleAdminBindingCreate))
	mux.Handle("POST "+p+"/bindings/{id}/delete", s.require(m, s.handleAdminBindingDelete))

	sm := rbac.SpaceManage
	mux.Handle("GET "+p+"/spaces", s.require(sm, s.handleAdminSpaces))
	mux.Handle("GET "+p+"/spaces/new", s.require(sm, s.handleAdminSpaceNew))
	mux.Handle("POST "+p+"/spaces", s.require(sm, s.handleAdminSpaceCreate))
	mux.Handle("GET "+p+"/spaces/{slug}", s.require(sm, s.handleAdminSpace))
	mux.Handle("GET "+p+"/spaces/{slug}/edit", s.require(sm, s.handleAdminSpaceEdit))
	mux.Handle("POST "+p+"/spaces/{slug}", s.require(sm, s.handleAdminSpaceUpdate))
	mux.Handle("GET "+p+"/spaces/{slug}/delete", s.require(sm, s.handleAdminSpaceDeleteForm))
	mux.Handle("POST "+p+"/spaces/{slug}/delete", s.require(sm, s.handleAdminSpaceDelete))
	// The access of a space is part of the access control of the tenant.
	// Thus it needs tenant.manage, not space.manage.
	mux.Handle("GET "+p+"/spaces/{slug}/access", s.require(m, s.handleAdminSpaceAccess))
	mux.Handle("GET "+p+"/spaces/{slug}/default-role", s.require(m, s.handleAdminSpaceDefaultRoleForm))
	mux.Handle("POST "+p+"/spaces/{slug}/default-role", s.require(m, s.handleAdminSpaceDefaultRole))
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

// csrfOf returns the CSRF token of the session of the request.
func csrfOf(r *http.Request) string { return reqInfoFrom(r.Context()).Session.CSRFToken }

// bindingTable is the argument of the "bindingtable" template.
type bindingTable struct {
	Bindings []store.Binding
	// Admin is the URL path of the tenant administration.
	Admin string
	CSRF  string
	// ReturnTo is the page that the browser shows after a removal.
	ReturnTo    string
	ShowSubject bool
	ShowScope   bool
}

// bindings returns the role bindings of the tenant for which keep returns
// true, in a table for the "bindingtable" template.
func (s *Server) bindings(r *http.Request, keep func(b store.Binding) bool, returnTo string, subject, scope bool) (bindingTable, error) {
	all, err := s.store.Bindings(r.Context(), tenantOf(r).ID)
	if err != nil {
		return bindingTable{}, err
	}
	t := bindingTable{Admin: adminPath(r), CSRF: csrfOf(r), ReturnTo: returnTo, ShowSubject: subject, ShowScope: scope}
	for _, b := range all {
		if keep(b) {
			t.Bindings = append(t.Bindings, b)
		}
	}
	return t, nil
}

// confirmDelete is the data of the page that asks the user to confirm a
// deletion. Code is the text that the user must type, or "".
type confirmDelete struct {
	// RootURL and RootLabel are the list page above the item.
	RootURL   string
	RootLabel string
	Heading   string
	// Parent is the name of the page of Cancel.
	Parent  string
	Message string
	Action  string
	Code    string
	Cancel  string
	CSRF    string
}

// tabCrumbs are the last parts of the path for the tabs of an item.
var tabCrumbs = map[string]string{
	"bindings":    "Role bindings",
	"permissions": "Permissions",
	"access":      "Access",
	"delete":      "Delete",
	"spaces":      "Spaces",
}

// defaultRoleText describes the default role of a space or a node. A nil
// role uses the default role of the parent.
func defaultRoleText(role *string, parent string) string {
	switch {
	case role == nil:
		return "Same as the " + parent
	case *role == "":
		return "None. Only users with a role binding have access."
	}
	return *role
}

func (s *Server) handleAdminHome(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, adminPath(r)+"/users", http.StatusSeeOther)
}

func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.Users(r.Context(), tenantOf(r).ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderAdmin(w, r, http.StatusOK, "users", "admin-users", "Users", users)
}

// userHead is the data of the head of the user pages.
type userHead struct {
	User   *store.User
	IsSelf bool
	Tab    string
	Crumb  string
}

// loadUser loads the user from the {id} path value.
func (s *Server) loadUser(w http.ResponseWriter, r *http.Request, tab string) (userHead, bool) {
	id, ok := pathID(r, "id")
	if !ok {
		s.notFound(w, r)
		return userHead{}, false
	}
	user, err := s.store.User(r.Context(), tenantOf(r).ID, id)
	if err != nil {
		s.storeResult(w, r, err)
		return userHead{}, false
	}
	return userHead{user, id == reqInfoFrom(r.Context()).Session.User.ID, tab, tabCrumbs[tab]}, true
}

func (s *Server) handleAdminUser(w http.ResponseWriter, r *http.Request) {
	h, ok := s.loadUser(w, r, "")
	if !ok {
		return
	}
	groups, err := s.store.UserGroups(r.Context(), tenantOf(r).ID, h.User.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderAdmin(w, r, http.StatusOK, "users", "admin-user", h.User.Name(), struct {
		userHead
		Groups []store.Group
	}{h, groups})
}

func (s *Server) handleAdminUserBindings(w http.ResponseWriter, r *http.Request) {
	h, ok := s.loadUser(w, r, "bindings")
	if !ok {
		return
	}
	table, err := s.bindings(r, func(b store.Binding) bool { return b.UserID == h.User.ID },
		r.URL.Path, false, true)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderAdmin(w, r, http.StatusOK, "users", "admin-user-bindings", h.User.Name(), struct {
		userHead
		Table bindingTable
	}{h, table})
}

// spacePermissions is one row of the per-space permission table.
type spacePermissions struct {
	Space       store.Space
	Permissions []rbac.Permission
}

func (s *Server) handleAdminUserPermissions(w http.ResponseWriter, r *http.Request) {
	h, ok := s.loadUser(w, r, "permissions")
	if !ok {
		return
	}
	ctx := r.Context()
	t := tenantOf(r)
	perms, err := s.store.UserPermissions(ctx, t.ID, h.User.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	spaces, err := s.store.Spaces(ctx, t.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	all, err := s.store.AllSpacePermissions(ctx, t.ID, h.User.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	spacePerms := make([]spacePermissions, len(spaces))
	for i, sp := range spaces {
		spacePerms[i] = spacePermissions{Space: sp, Permissions: all[sp.ID].List()}
	}
	s.renderAdmin(w, r, http.StatusOK, "users", "admin-user-permissions", h.User.Name(), struct {
		userHead
		Permissions      []rbac.Permission
		SpacePermissions []spacePermissions
	}{h, perms.List(), spacePerms})
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
	s.renderAdmin(w, r, http.StatusOK, "groups", "admin-groups", "Groups", groups)
}

func (s *Server) handleAdminGroupNew(w http.ResponseWriter, r *http.Request) {
	s.renderAdmin(w, r, http.StatusOK, "groups", "admin-group-new", "New local group", nil)
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

// groupHead is the data of the head of the group pages.
type groupHead struct {
	Group *store.Group
	Tab   string
	Crumb string
}

// loadGroup loads the group from the {id} path value.
func (s *Server) loadGroup(w http.ResponseWriter, r *http.Request, tab string) (groupHead, bool) {
	id, ok := pathID(r, "id")
	if !ok {
		s.notFound(w, r)
		return groupHead{}, false
	}
	group, err := s.store.Group(r.Context(), tenantOf(r).ID, id)
	if err != nil {
		s.storeResult(w, r, err)
		return groupHead{}, false
	}
	return groupHead{group, tab, tabCrumbs[tab]}, true
}

func (s *Server) handleAdminGroup(w http.ResponseWriter, r *http.Request) {
	h, ok := s.loadGroup(w, r, "")
	if !ok {
		return
	}
	members, err := s.store.GroupMembers(r.Context(), tenantOf(r).ID, h.Group.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderAdmin(w, r, http.StatusOK, "groups", "admin-group", "Group "+h.Group.Name, struct {
		groupHead
		Members []*store.User
	}{h, members})
}

func (s *Server) handleAdminGroupBindings(w http.ResponseWriter, r *http.Request) {
	h, ok := s.loadGroup(w, r, "bindings")
	if !ok {
		return
	}
	table, err := s.bindings(r, func(b store.Binding) bool { return b.GroupID == h.Group.ID },
		r.URL.Path, false, true)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderAdmin(w, r, http.StatusOK, "groups", "admin-group-bindings", "Group "+h.Group.Name, struct {
		groupHead
		Table bindingTable
	}{h, table})
}

func (s *Server) handleAdminGroupDeleteForm(w http.ResponseWriter, r *http.Request) {
	h, ok := s.loadGroup(w, r, "delete")
	if !ok {
		return
	}
	if h.Group.Source != store.GroupLocal {
		s.badRequest(w, r, "The identity provider controls this group. You cannot delete it.")
		return
	}
	g := adminPath(r) + "/groups/" + strconv.FormatInt(h.Group.ID, 10)
	s.renderAdmin(w, r, http.StatusOK, "groups", "confirm-delete", "Delete group", confirmDelete{
		Heading: "Delete group " + h.Group.Name,
		Message: "Do you want to delete the group and its role bindings? The members lose the roles of the group.",
		Action:  g + "/delete", Cancel: g, Parent: h.Group.Name, CSRF: csrfOf(r),
		RootURL: adminPath(r) + "/groups", RootLabel: "Groups",
	})
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

// handleAdminMemberNew shows the form to add a user to a local group. The
// list contains only users that are not members.
func (s *Server) handleAdminMemberNew(w http.ResponseWriter, r *http.Request) {
	h, ok := s.loadGroup(w, r, "")
	if !ok {
		return
	}
	if h.Group.Source != store.GroupLocal {
		s.badRequest(w, r, "The identity provider controls the members of this group.")
		return
	}
	ctx := r.Context()
	t := tenantOf(r)
	members, err := s.store.GroupMembers(ctx, t.ID, h.Group.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	users, err := s.store.Users(ctx, t.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	users = slices.DeleteFunc(users, func(u *store.User) bool {
		return slices.ContainsFunc(members, func(m *store.User) bool { return m.ID == u.ID })
	})
	s.renderAdmin(w, r, http.StatusOK, "groups", "admin-group-member-new", "New member", struct {
		Group *store.Group
		Users []*store.User
	}{h.Group, users})
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
	s.renderAdmin(w, r, http.StatusOK, "roles", "admin-roles", "Roles", roles)
}

func (s *Server) handleAdminRoleNew(w http.ResponseWriter, r *http.Request) {
	s.renderAdmin(w, r, http.StatusOK, "roles", "admin-role-new", "New role", rbac.All)
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

// handleAdminRoleDeleteForm asks the user to confirm the deletion of a
// custom role. A built-in role does not have this page.
func (s *Server) handleAdminRoleDeleteForm(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		s.notFound(w, r)
		return
	}
	roles, err := s.store.Roles(r.Context(), tenantOf(r).ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	i := slices.IndexFunc(roles, func(role store.Role) bool { return role.ID == id && !role.Builtin })
	if i < 0 {
		s.notFound(w, r)
		return
	}
	rp := adminPath(r) + "/roles"
	s.renderAdmin(w, r, http.StatusOK, "roles", "confirm-delete", "Delete role", confirmDelete{
		Heading: "Delete role " + roles[i].Name,
		Message: "Do you want to delete the role and its role bindings? The users and groups lose the permissions of the role.",
		Action:  rp + "/" + strconv.FormatInt(id, 10) + "/delete", Cancel: rp, Parent: "Roles", CSRF: csrfOf(r),
	})
}

func (s *Server) handleAdminRoleDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		s.notFound(w, r)
		return
	}
	err := s.store.DeleteRole(r.Context(), tenantOf(r).ID, id)
	if errors.Is(err, store.ErrRoleInUse) {
		s.renderError(w, r, http.StatusConflict,
			"The role is the default role of the tenant, a space or an item. Change the default role first.")
		return
	}
	if !s.storeResult(w, r, err) {
		return
	}
	s.log.Info("role deleted", "tenant", tenantOf(r).Code, "role_id", id,
		"user_id", reqInfoFrom(r.Context()).Session.User.ID)
	http.Redirect(w, r, adminPath(r)+"/roles", http.StatusSeeOther)
}

// handleAdminRecent shows the documents of the tenant that changed last.
// It shows only the documents that the user can read.
func (s *Server) handleAdminRecent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	t := tenantOf(r)
	userID := reqInfoFrom(ctx).Session.User.ID
	list, err := s.store.RecentChanges(ctx, t.ID, 200)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	access := map[int64]*store.Access{}
	var out []store.RecentChange
	for _, c := range list {
		acc, ok := access[c.SpaceID]
		if !ok {
			if acc, err = s.store.Access(ctx, t.ID, c.SpaceID, userID); err != nil {
				s.serverError(w, r, err)
				return
			}
			access[c.SpaceID] = acc
		}
		if acc.Can(c.AccessPathID, rbac.DocRead) {
			out = append(out, c)
			if len(out) == 50 {
				break
			}
		}
	}
	s.renderAdmin(w, r, http.StatusOK, "recent", "admin-recent", "Recent changes", out)
}

func (s *Server) handleAdminBindings(w http.ResponseWriter, r *http.Request) {
	table, err := s.bindings(r, func(store.Binding) bool { return true }, r.URL.Path, true, true)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderAdmin(w, r, http.StatusOK, "bindings", "admin-bindings", "Role bindings", table)
}

// bindingForm is the data of the form for a new role binding. The query
// of the URL can set the subject ("user:ID" or "group:ID"), the space and
// the node. The form then does not show these fields.
type bindingForm struct {
	Roles        []store.Role
	Groups       []store.Group
	Users        []*store.User
	Spaces       []store.Space
	Subject      string
	SubjectLabel string
	Space        *store.Space
	Node         *store.Node
	ReturnTo     string
}

func (s *Server) handleAdminBindingNew(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	t := tenantOf(r)
	q := r.URL.Query()
	f := bindingForm{ReturnTo: safeReturnTo(q.Get("return_to"), adminPath(r))}
	var err error
	if f.Roles, err = s.store.Roles(ctx, t.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	if subject := q.Get("subject"); subject != "" {
		if f.Subject, f.SubjectLabel, err = s.subjectLabel(ctx, t.ID, subject); err != nil {
			s.storeResult(w, r, err)
			return
		}
	} else {
		if f.Groups, err = s.store.Groups(ctx, t.ID); err != nil {
			s.serverError(w, r, err)
			return
		}
		if f.Users, err = s.store.Users(ctx, t.ID); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	if slug := q.Get("space"); slug != "" {
		if !store.ValidCode(slug) {
			s.notFound(w, r)
			return
		}
		if f.Space, err = s.store.Space(ctx, t.ID, slug); err != nil {
			s.storeResult(w, r, err)
			return
		}
		if pub := q.Get("node"); pub != "" {
			if !store.ValidPublicID(pub) {
				s.notFound(w, r)
				return
			}
			if f.Node, err = s.store.Node(ctx, f.Space.ID, pub); err != nil {
				s.storeResult(w, r, err)
				return
			}
		}
	} else if f.Spaces, err = s.store.Spaces(ctx, t.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderAdmin(w, r, http.StatusOK, "bindings", "admin-binding-new", "New role binding", f)
}

// subjectLabel checks a subject of the form "user:ID" or "group:ID". It
// returns the subject and a text for the form.
func (s *Server) subjectLabel(ctx context.Context, tenantID int64, subject string) (string, string, error) {
	kind, idText, _ := strings.Cut(subject, ":")
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id <= 0 {
		return "", "", store.ErrNotFound
	}
	switch kind {
	case "user":
		u, err := s.store.User(ctx, tenantID, id)
		if err != nil {
			return "", "", err
		}
		return subject, "User " + u.Username, nil
	case "group":
		g, err := s.store.Group(ctx, tenantID, id)
		if err != nil {
			return "", "", err
		}
		return subject, "Group " + g.Name, nil
	}
	return "", "", store.ErrNotFound
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
	// An empty "space" field gives a binding for all spaces. The "node"
	// field (the public ID of a folder or a document in the space) gives a
	// binding for one item.
	var scope store.Scope
	if slug := r.PostFormValue("space"); slug != "" {
		if !store.ValidCode(slug) {
			s.badRequest(w, r, "The space is not valid.")
			return
		}
		sp, err := s.store.Space(r.Context(), t.ID, slug)
		if err != nil {
			s.storeResult(w, r, err)
			return
		}
		scope.SpaceID = sp.ID
		if pub := r.PostFormValue("node"); pub != "" {
			if !store.ValidPublicID(pub) {
				s.badRequest(w, r, "The item is not valid.")
				return
			}
			n, err := s.store.Node(r.Context(), sp.ID, pub)
			if err != nil {
				s.storeResult(w, r, err)
				return
			}
			scope.NodeID = n.ID
		}
	}
	switch kind {
	case "user":
		err = s.store.BindRoleToUser(r.Context(), t.ID, role, id, scope)
	case "group":
		err = s.store.BindRoleToGroup(r.Context(), t.ID, role, id, scope)
	default:
		s.badRequest(w, r, "Select a role and a user or a group.")
		return
	}
	if !s.storeResult(w, r, err) {
		return
	}
	s.log.Info("role binding created", "tenant", t.Code, "role", role, "subject", kind, "subject_id", id,
		"space", r.PostFormValue("space"), "node", r.PostFormValue("node"),
		"user_id", reqInfoFrom(r.Context()).Session.User.ID)
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
func (s *Server) spaceForm(r *http.Request) (slug, name, desc, msg string) {
	slug = strings.TrimSpace(r.PostFormValue("slug"))
	name = strings.TrimSpace(r.PostFormValue("name"))
	desc = strings.TrimSpace(r.PostFormValue("description"))
	switch {
	case !s.validSpaceSlug(slug):
		msg = spaceCodeError
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
	s.renderAdmin(w, r, http.StatusOK, "spaces", "admin-spaces", "Spaces", spaces)
}

func (s *Server) handleAdminSpaceNew(w http.ResponseWriter, r *http.Request) {
	s.renderAdmin(w, r, http.StatusOK, "spaces", "admin-space-new", "New space", nil)
}

func (s *Server) handleAdminSpaceCreate(w http.ResponseWriter, r *http.Request) {
	slug, name, desc, msg := s.spaceForm(r)
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
	http.Redirect(w, r, adminPath(r)+"/spaces/"+slug, http.StatusSeeOther)
}

// spaceHead is the data of the head of the space pages.
type spaceHead struct {
	Space *store.Space
	Tab   string
	Crumb string
}

// loadSpace loads the space from the {slug} path value.
func (s *Server) loadSpace(w http.ResponseWriter, r *http.Request, tab string) (spaceHead, bool) {
	sp, err := s.store.Space(r.Context(), tenantOf(r).ID, r.PathValue("slug"))
	if err != nil {
		s.storeResult(w, r, err)
		return spaceHead{}, false
	}
	return spaceHead{sp, tab, tabCrumbs[tab]}, true
}

func (s *Server) handleAdminSpace(w http.ResponseWriter, r *http.Request) {
	h, ok := s.loadSpace(w, r, "")
	if !ok {
		return
	}
	s.renderAdmin(w, r, http.StatusOK, "spaces", "admin-space", "Space "+h.Space.Name, h)
}

func (s *Server) handleAdminSpaceEdit(w http.ResponseWriter, r *http.Request) {
	h, ok := s.loadSpace(w, r, "")
	if !ok {
		return
	}
	s.renderAdmin(w, r, http.StatusOK, "spaces", "admin-space-edit", "Settings of "+h.Space.Name, h)
}

func (s *Server) handleAdminSpaceUpdate(w http.ResponseWriter, r *http.Request) {
	newSlug, name, desc, msg := s.spaceForm(r)
	if msg != "" {
		s.badRequest(w, r, msg)
		return
	}
	slug := r.PathValue("slug")
	if !s.storeResult(w, r, s.store.UpdateSpace(r.Context(), tenantOf(r).ID, slug, newSlug, name, desc)) {
		return
	}
	if newSlug != slug {
		s.log.Info("space code changed", "tenant", tenantOf(r).Code, "from", slug, "to", newSlug,
			"user_id", reqInfoFrom(r.Context()).Session.User.ID)
	}
	http.Redirect(w, r, adminPath(r)+"/spaces/"+newSlug, http.StatusSeeOther)
}

func (s *Server) handleAdminSpaceDeleteForm(w http.ResponseWriter, r *http.Request) {
	h, ok := s.loadSpace(w, r, "delete")
	if !ok {
		return
	}
	sp := adminPath(r) + "/spaces/" + h.Space.Slug
	s.renderAdmin(w, r, http.StatusOK, "spaces", "confirm-delete", "Delete space", confirmDelete{
		Heading: "Delete space " + h.Space.Name,
		Message: "Do you want to delete the space? This deletes all folders and documents in it.",
		Action:  sp + "/delete", Code: h.Space.Slug, Cancel: sp, Parent: h.Space.Name, CSRF: csrfOf(r),
		RootURL: adminPath(r) + "/spaces", RootLabel: "Spaces",
	})
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

// handleAdminSpaceAccess shows the default role and the role bindings of a
// space.
func (s *Server) handleAdminSpaceAccess(w http.ResponseWriter, r *http.Request) {
	h, ok := s.loadSpace(w, r, "access")
	if !ok {
		return
	}
	table, err := s.bindings(r, func(b store.Binding) bool { return b.SpaceID == h.Space.ID && b.NodeID == 0 },
		r.URL.Path, true, false)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	parent := "tenant"
	if role := tenantOf(r).DefaultRole; role != "" {
		parent += " (" + role + ")"
	}
	s.renderAdmin(w, r, http.StatusOK, "spaces", "admin-space-access", "Access to "+h.Space.Name, struct {
		spaceHead
		DefaultRoleText string
		Table           bindingTable
	}{h, defaultRoleText(h.Space.DefaultRole, parent), table})
}

func (s *Server) handleAdminSpaceDefaultRoleForm(w http.ResponseWriter, r *http.Request) {
	h, ok := s.loadSpace(w, r, "")
	if !ok {
		return
	}
	roles, err := s.store.Roles(r.Context(), tenantOf(r).ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	// The select uses "-" for "same as the tenant".
	defaultRole := "-"
	if h.Space.DefaultRole != nil {
		defaultRole = *h.Space.DefaultRole
	}
	s.renderAdmin(w, r, http.StatusOK, "spaces", "admin-space-default-role", "Default role", struct {
		Space       *store.Space
		DefaultRole string
		Roles       []store.Role
	}{h.Space, defaultRole, roles})
}

// handleAdminSpaceDefaultRole sets the default role of a space. The value
// "-" means "same as the tenant", and "" means no default role.
func (s *Server) handleAdminSpaceDefaultRole(w http.ResponseWriter, r *http.Request) {
	t := tenantOf(r)
	slug := r.PathValue("slug")
	role, ok := defaultRoleValue(r)
	if !ok {
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

// defaultRoleValue reads the field default_role of a form. The value "-"
// gives nil: the space or the node uses the default role of its parent.
// The value "" means no default role.
func defaultRoleValue(r *http.Request) (*string, bool) {
	switch v := r.PostFormValue("default_role"); {
	case v == "-":
		return nil, true
	case v == "" || roleNameRE.MatchString(v):
		return &v, true
	}
	return nil, false
}
