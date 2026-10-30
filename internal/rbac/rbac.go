// Package rbac defines the permissions of the application and the
// permission set of one user.
//
// A user gets roles through role bindings. A binding connects a role to a
// user or to a group. A role is a named set of permissions. The effective
// permissions of a user are the union of the permissions of all roles that
// apply to the user directly or through a group.
package rbac

import "sort"

// Permission is the name of one action that the application controls.
type Permission string

// Permissions that the application knows.
const (
	PageRead    Permission = "page.read"
	PageCreate  Permission = "page.create"
	PageEdit    Permission = "page.edit"
	PageDelete  Permission = "page.delete"
	AdminManage Permission = "admin.manage"
)

// All is the list of all permissions, in display order.
var All = []Permission{PageRead, PageCreate, PageEdit, PageDelete, AdminManage}

var descriptions = map[Permission]string{
	PageRead:    "Read pages, history and search results",
	PageCreate:  "Create new pages",
	PageEdit:    "Edit pages",
	PageDelete:  "Delete pages",
	AdminManage: "Manage users, groups, roles and role bindings",
}

// Description returns a short text that tells what the permission allows.
func (p Permission) Description() string { return descriptions[p] }

// Valid reports whether p is a known permission.
func (p Permission) Valid() bool {
	_, ok := descriptions[p]
	return ok
}

// Set is the set of permissions that one user has.
// The zero value has no permissions.
type Set map[Permission]struct{}

// NewSet makes a set from a list of permission names.
// The function ignores unknown names.
func NewSet(names ...string) Set {
	s := make(Set, len(names))
	for _, n := range names {
		p := Permission(n)
		if p.Valid() {
			s[p] = struct{}{}
		}
	}
	return s
}

// Has reports whether the set contains p.
func (s Set) Has(p Permission) bool {
	_, ok := s[p]
	return ok
}

// List returns the permissions in the set in sorted order.
func (s Set) List() []Permission {
	out := make([]Permission, 0, len(s))
	for p := range s {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
