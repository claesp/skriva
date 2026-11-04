package store

import (
	"context"

	"github.com/claesp/skriva/internal/rbac"
)

// The permission queries use these rules:
//
//   - A role binding without a space applies in all spaces of the tenant.
//     It gives all permissions of the role.
//   - A role binding for a space applies only in that space. A role
//     binding for a node (a folder or a document) applies to the node and
//     its subtree. These
//     bindings give only the space permissions of the role (the
//     permissions that start with "doc.").
//   - The default role of the tenant applies everywhere, except where a
//     space or a node has its own default role. The nearest setting
//     applies: first the node, then its parents, then the space, then the
//     tenant. The default role of a space or a folder gives only
//     space permissions.
//   - Bindings add permissions. A default role "none" does not remove the
//     permissions from bindings at a higher level.
//
// Thus a binding for a space or a folder can never give tenant.manage or
// space.manage. The SQL uses 'doc.%' for the space permissions.

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

const (
	tenantDefaultRole = `(SELECT t.default_role FROM tenants t WHERE t.id = u.tenant_id)`
	tenantScope       = "rb.space_id IS NULL"
	spaceScope        = "rb.space_id = sp.id AND rb.node_id IS NULL"
	rolesOfTenant     = "(r.tenant_id IS NULL OR r.tenant_id = u.tenant_id)"
)

// permissionsQuery selects the tenant-level permissions of user u: the
// permissions of the default role of the tenant and of the bindings
// without a space. A system administrator (u.tenant_id IS NULL) gets no
// permissions.
var permissionsQuery = `
	SELECT DISTINCT rp.permission
	FROM role_permissions rp
	JOIN roles r ON r.id = rp.role_id
	WHERE ` + rolesOfTenant + `
	  AND (r.name = ` + tenantDefaultRole + `
	       OR r.id IN (` + boundRoles(tenantScope) + `))`

// spaceDefaultQuery selects the space permissions of the default role in
// space sp.
var spaceDefaultQuery = `
	SELECT DISTINCT rp.permission
	FROM role_permissions rp
	JOIN roles r ON r.id = rp.role_id
	WHERE ` + rolesOfTenant + ` AND rp.permission LIKE 'doc.%'
	  AND r.name = COALESCE(sp.default_role, ` + tenantDefaultRole + `)`

// spaceBoundQuery selects the permissions of user u in space sp that do
// not come from a default role of the space or of a folder.
//
// The query is a UNION and not one WHERE clause with OR. With OR,
// PostgreSQL can use one hashed subquery at two places in the plan. In a
// query for more than one space, one of the two places then keeps the
// result for the first space. PostgreSQL 17 gave incorrect results for
// AllSpacePermissions in this way.
var spaceBoundQuery = `
	SELECT rp.permission
	FROM role_permissions rp
	JOIN roles r ON r.id = rp.role_id
	WHERE ` + rolesOfTenant + ` AND r.id IN (` + boundRoles(tenantScope) + `)
	UNION
	SELECT rp.permission
	FROM role_permissions rp
	JOIN roles r ON r.id = rp.role_id
	WHERE ` + rolesOfTenant + ` AND rp.permission LIKE 'doc.%'
	  AND r.id IN (` + boundRoles(spaceScope) + `)
	UNION
	SELECT rp.permission
	FROM role_permissions rp
	JOIN roles r ON r.id = rp.role_id
	WHERE ` + rolesOfTenant + ` AND rp.permission NOT LIKE 'doc.%'
	  AND r.name = ` + tenantDefaultRole

// UserPermissions returns the tenant-level permissions of a user. See
// Access for the permissions in a space.
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

// AllSpacePermissions returns the permissions of a user at the top level
// of each space of the tenant. The key is the space ID.
func (s *Store) AllSpacePermissions(ctx context.Context, tenantID, userID int64) (map[int64]rbac.Set, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT sp.id, ARRAY(`+spaceDefaultQuery+`), ARRAY(`+spaceBoundQuery+`)
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
		var def, bound []string
		if err := rows.Scan(&id, &def, &bound); err != nil {
			return nil, err
		}
		out[id] = rbac.NewSet(append(def, bound...)...)
	}
	return out, rows.Err()
}

// ReadableSpaces returns the IDs of the spaces in which the user can read
// something: the top level of the space, or at least one folder.
func (s *Store) ReadableSpaces(ctx context.Context, tenantID, userID int64) ([]int64, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT sp.id FROM users u, spaces sp
		WHERE u.tenant_id = $1 AND u.id = $2 AND sp.tenant_id = $1
		  AND ('doc.read' = ANY(ARRAY(`+spaceDefaultQuery+`))
		    OR 'doc.read' = ANY(ARRAY(`+spaceBoundQuery+`))
		    -- A folder with a default role that can read.
		    OR EXISTS (
		        SELECT 1 FROM nodes n
		        JOIN roles r ON r.name = n.default_role AND `+rolesOfTenant+`
		        JOIN role_permissions rp ON rp.role_id = r.id AND rp.permission = 'doc.read'
		        WHERE n.space_id = sp.id AND n.deleted_at IS NULL)
		    -- A folder binding that can read.
		    OR EXISTS (
		        SELECT 1 FROM role_permissions rp
		        WHERE rp.permission = 'doc.read'
		          AND rp.role_id IN (`+boundRoles("rb.space_id = sp.id AND rb.node_id IS NOT NULL")+`)))`,
		tenantID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Access contains the data to calculate the permissions of one user for
// the nodes of one space. It does not read the tree: each node has a
// cached access path (see the table access_paths), and the permissions depend only
// on that path. Thus the size of the data depends on the number of nodes
// with own access settings, and not on the size of the space.
//
// Access is not safe for concurrent use.
type Access struct {
	// spaceDefault contains the space permissions of the default role of
	// the space.
	spaceDefault rbac.Set
	// bound contains the permissions from the bindings for the tenant and
	// for the space, and the tenant permissions of the default role of
	// the tenant.
	bound rbac.Set
	// paths maps each access path ID to the nodes in the path, from the
	// top level down.
	paths map[int64][]int64
	// overrides contains the default role permissions of nodes with their
	// own default role.
	overrides map[int64]rbac.Set
	// nodeBound contains the permissions from the node bindings of the
	// user.
	nodeBound map[int64]rbac.Set

	memo map[int64]rbac.Set
}

// Access loads the permission data of a user in a space.
func (s *Store) Access(ctx context.Context, tenantID, spaceID, userID int64) (*Access, error) {
	a := &Access{
		paths:     map[int64][]int64{},
		overrides: map[int64]rbac.Set{},
		nodeBound: map[int64]rbac.Set{},
		memo:      map[int64]rbac.Set{},
	}
	var def, bound []string
	err := s.pool.QueryRow(ctx, `
		SELECT ARRAY(`+spaceDefaultQuery+`), ARRAY(`+spaceBoundQuery+`)
		FROM users u, spaces sp
		WHERE u.tenant_id = $1 AND u.id = $2 AND sp.tenant_id = $1 AND sp.id = $3`,
		tenantID, userID, spaceID).Scan(&def, &bound)
	if err != nil {
		return nil, notFound(err)
	}
	a.spaceDefault = rbac.NewSet(def...)
	a.bound = rbac.NewSet(bound...)

	rows, err := s.pool.Query(ctx, "SELECT id, path FROM access_paths WHERE space_id = $1", spaceID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var path []int64
		if err := rows.Scan(&id, &path); err != nil {
			rows.Close()
			return nil, err
		}
		a.paths[id] = path
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := s.permissionMap(ctx, a.overrides, `
		SELECT n.id, ARRAY(
			SELECT DISTINCT rp.permission FROM role_permissions rp
			JOIN roles r ON r.id = rp.role_id
			WHERE r.name = n.default_role AND (r.tenant_id IS NULL OR r.tenant_id = $1)
			  AND rp.permission LIKE 'doc.%')
		FROM nodes n WHERE n.space_id = $2 AND n.default_role IS NOT NULL`,
		tenantID, spaceID); err != nil {
		return nil, err
	}
	if err := s.permissionMap(ctx, a.nodeBound, `
		SELECT rb.node_id, array_agg(DISTINCT rp.permission)
		FROM role_bindings rb
		JOIN role_permissions rp ON rp.role_id = rb.role_id
		WHERE rb.tenant_id = $1 AND rb.space_id = $2 AND rb.node_id IS NOT NULL
		  AND rp.permission LIKE 'doc.%'
		  AND (rb.user_id = $3 OR rb.group_id IN (
		      SELECT gm.group_id FROM group_members gm WHERE gm.tenant_id = $1 AND gm.user_id = $3))
		GROUP BY rb.node_id`,
		tenantID, spaceID, userID); err != nil {
		return nil, err
	}
	return a, nil
}

func (s *Store) permissionMap(ctx context.Context, out map[int64]rbac.Set, sql string, args ...any) error {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var perms []string
		if err := rows.Scan(&id, &perms); err != nil {
			return err
		}
		out[id] = rbac.NewSet(perms...)
	}
	return rows.Err()
}

// Root returns the permissions at the top level of the space. They apply
// to new nodes at the top level.
func (a *Access) Root() rbac.Set {
	return union(a.spaceDefault, a.bound)
}

// For returns the permissions of the user for a node with the given access
// path ID. An unknown path gives no permissions: the path can be new after
// the data was loaded.
func (a *Access) For(pathID int64) rbac.Set {
	if p, ok := a.memo[pathID]; ok {
		return p
	}
	path, ok := a.paths[pathID]
	if !ok {
		return rbac.Set{}
	}
	// Go from the top level down. def is the nearest default role. bound
	// adds the node bindings.
	def, bound := a.spaceDefault, a.bound
	for _, n := range path {
		if o, ok := a.overrides[n]; ok {
			def = o
		}
		if b, ok := a.nodeBound[n]; ok {
			bound = union(bound, b)
		}
	}
	p := union(def, bound)
	a.memo[pathID] = p
	return p
}

// Can reports whether the user has permission p for a node with the given
// access path ID.
func (a *Access) Can(pathID int64, p rbac.Permission) bool { return a.For(pathID).Has(p) }

func union(a, b rbac.Set) rbac.Set {
	out := make(rbac.Set, len(a)+len(b))
	for p := range a {
		out[p] = struct{}{}
	}
	for p := range b {
		out[p] = struct{}{}
	}
	return out
}

// Any reports whether the user has permission p at the top level of the
// space or for at least one access path of the space.
func (a *Access) Any(p rbac.Permission) bool {
	if a.Root().Has(p) {
		return true
	}
	for id := range a.paths {
		if a.Can(id, p) {
			return true
		}
	}
	return false
}
