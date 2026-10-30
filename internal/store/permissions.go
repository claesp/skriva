package store

import (
	"context"

	"github.com/clpe/skriva/internal/rbac"
)

// The permission queries use these rules:
//
//   - A role binding without a space applies in all spaces of the tenant.
//     It gives all permissions of the role.
//   - A role binding with a space applies only in that space. It gives
//     only the space permissions of the role (rbac.Permission.SpaceScoped).
//   - The default role of the tenant applies everywhere, except in a
//     space with its own default role. The default role of a space gives
//     only space permissions.
//
// Thus a binding for one space can never give tenant.manage or
// space.manage. The SQL uses 'doc.%' for the space permissions. Keep it
// the same as rbac.Permission.SpaceScoped.

// boundRoles returns SQL that selects the IDs of the roles of user u
// through bindings to the user or to a group of the user. scope is an SQL
// condition on the binding rb.
func boundRoles(scope string) string {
	return `
		SELECT rb.role_id FROM role_bindings rb
		WHERE rb.tenant_id = u.tenant_id AND rb.user_id = u.id AND ` + scope + `
		UNION
		SELECT rb.role_id FROM role_bindings rb
		JOIN group_members gm ON gm.tenant_id = rb.tenant_id AND gm.group_id = rb.group_id
		WHERE rb.tenant_id = u.tenant_id AND gm.user_id = u.id AND ` + scope
}

const tenantDefaultRole = `(SELECT t.default_role FROM tenants t WHERE t.id = u.tenant_id)`

// permissionsQuery selects the tenant-level permissions of user u: the
// permissions of the default role of the tenant and of the bindings
// without a space. A system administrator (u.tenant_id IS NULL) gets no
// permissions.
var permissionsQuery = `
	SELECT DISTINCT rp.permission
	FROM role_permissions rp
	JOIN roles r ON r.id = rp.role_id
	WHERE (r.tenant_id IS NULL OR r.tenant_id = u.tenant_id)
	  AND (r.name = ` + tenantDefaultRole + `
	       OR r.id IN (` + boundRoles("rb.space_id IS NULL") + `))`

// spacePermissionsQuery selects the permissions of user u in space sp.
var spacePermissionsQuery = `
	SELECT DISTINCT rp.permission
	FROM role_permissions rp
	JOIN roles r ON r.id = rp.role_id
	WHERE (r.tenant_id IS NULL OR r.tenant_id = u.tenant_id)
	  AND (
	    (rp.permission LIKE 'doc.%' AND (
	        r.name = COALESCE(sp.default_role, ` + tenantDefaultRole + `)
	        OR r.id IN (` + boundRoles("rb.space_id IS NULL") + `)
	        OR r.id IN (` + boundRoles("rb.space_id = sp.id") + `)))
	    OR
	    (rp.permission NOT LIKE 'doc.%' AND (
	        r.name = ` + tenantDefaultRole + `
	        OR r.id IN (` + boundRoles("rb.space_id IS NULL") + `))))`

// UserPermissions returns the tenant-level permissions of a user. See
// SpacePermissions for the permissions in one space.
func (s *Store) UserPermissions(ctx context.Context, tenantID, userID int64) (rbac.Set, error) {
	var perms []string
	err := s.pool.QueryRow(ctx,
		`SELECT ARRAY(`+permissionsQuery+`) FROM users u WHERE u.tenant_id = $1 AND u.id = $2`,
		tenantID, userID).Scan(&perms)
	if err != nil {
		return nil, notFound(err)
	}
	return rbac.NewSet(perms...), nil
}

// SpacePermissions returns the permissions of a user in a space.
func (s *Store) SpacePermissions(ctx context.Context, tenantID, spaceID, userID int64) (rbac.Set, error) {
	var perms []string
	err := s.pool.QueryRow(ctx, `
		SELECT ARRAY(`+spacePermissionsQuery+`)
		FROM users u, spaces sp
		WHERE u.tenant_id = $1 AND u.id = $2 AND sp.tenant_id = $1 AND sp.id = $3`,
		tenantID, userID, spaceID).Scan(&perms)
	if err != nil {
		return nil, notFound(err)
	}
	return rbac.NewSet(perms...), nil
}

// AllSpacePermissions returns the permissions of a user in each space of
// the tenant. The key is the space ID.
func (s *Store) AllSpacePermissions(ctx context.Context, tenantID, userID int64) (map[int64]rbac.Set, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT sp.id, ARRAY(`+spacePermissionsQuery+`)
		FROM users u, spaces sp
		WHERE u.tenant_id = $1 AND u.id = $2 AND sp.tenant_id = $1`,
		tenantID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]rbac.Set{}
	for rows.Next() {
		var id int64
		var perms []string
		if err := rows.Scan(&id, &perms); err != nil {
			return nil, err
		}
		out[id] = rbac.NewSet(perms...)
	}
	return out, rows.Err()
}

// ReadableSpaces returns the IDs of the spaces in which the user has the
// permission doc.read.
func (s *Store) ReadableSpaces(ctx context.Context, tenantID, userID int64) ([]int64, error) {
	all, err := s.AllSpacePermissions(ctx, tenantID, userID)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for id, perms := range all {
		if perms.Has(rbac.DocRead) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}
