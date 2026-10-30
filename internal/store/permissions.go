package store

import (
	"context"

	"github.com/clpe/skriva/internal/rbac"
)

// The permission queries use these rules:
//
//   - A role binding without a space applies in all spaces of the tenant.
//     It gives all permissions of the role.
//   - A role binding for a space applies only in that space. A role
//     binding for a folder applies to the folder and its subtree. These
//     bindings give only the space permissions of the role
//     (rbac.Permission.SpaceScoped).
//   - The default role of the tenant applies everywhere, except where a
//     space or a folder has its own default role. The nearest setting
//     applies: first the folder, then its parent folders, then the space,
//     then the tenant. The default role of a space or a folder gives only
//     space permissions.
//   - Bindings add permissions. A default role "none" does not remove the
//     permissions from bindings at a higher level.
//
// Thus a binding for a space or a folder can never give tenant.manage or
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
var spaceBoundQuery = `
	SELECT DISTINCT rp.permission
	FROM role_permissions rp
	JOIN roles r ON r.id = rp.role_id
	WHERE ` + rolesOfTenant + `
	  AND (r.id IN (` + boundRoles(tenantScope) + `)
	       OR (rp.permission LIKE 'doc.%' AND r.id IN (` + boundRoles(spaceScope) + `))
	       OR (rp.permission NOT LIKE 'doc.%' AND r.name = ` + tenantDefaultRole + `))`

// UserPermissions returns the tenant-level permissions of a user. See
// SpacePermissions and Access for the permissions in a space.
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

// SpacePermissions returns the permissions of a user at the top level of a
// space. Folders can have different permissions. See Access.
func (s *Store) SpacePermissions(ctx context.Context, tenantID, spaceID, userID int64) (rbac.Set, error) {
	var def, bound []string
	err := s.pool.QueryRow(ctx, `
		SELECT ARRAY(`+spaceDefaultQuery+`), ARRAY(`+spaceBoundQuery+`)
		FROM users u, spaces sp
		WHERE u.tenant_id = $1 AND u.id = $2 AND sp.tenant_id = $1 AND sp.id = $3`,
		tenantID, userID, spaceID).Scan(&def, &bound)
	if err != nil {
		return nil, notFound(err)
	}
	return rbac.NewSet(append(def, bound...)...), nil
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
		        WHERE n.space_id = sp.id)
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
// each node of one space. It is not safe for concurrent use.
type Access struct {
	// spaceDefault contains the space permissions of the default role of
	// the space.
	spaceDefault rbac.Set
	// bound contains the permissions from the bindings for the tenant and
	// for the space, and the tenant permissions of the default role of
	// the tenant.
	bound rbac.Set
	// parent maps each node to its parent. Nil is the top level.
	parent map[int64]*int64
	// overrides contains the default role permissions of folders with
	// their own default role.
	overrides map[int64]rbac.Set
	// folderBound contains the permissions from the folder bindings.
	folderBound map[int64]rbac.Set

	memo map[int64]rbac.Set
}

// Access loads the permission data of a user in a space.
func (s *Store) Access(ctx context.Context, tenantID, spaceID, userID int64) (*Access, error) {
	a := &Access{
		parent:      map[int64]*int64{},
		overrides:   map[int64]rbac.Set{},
		folderBound: map[int64]rbac.Set{},
		memo:        map[int64]rbac.Set{},
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

	rows, err := s.pool.Query(ctx, "SELECT id, parent_id FROM nodes WHERE space_id = $1", spaceID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var parent *int64
		if err := rows.Scan(&id, &parent); err != nil {
			rows.Close()
			return nil, err
		}
		a.parent[id] = parent
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
	if err := s.permissionMap(ctx, a.folderBound, `
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

// For returns the permissions of the user for a node. A node that is not
// in the space gets the permissions of the top level.
func (a *Access) For(id int64) rbac.Set {
	if p, ok := a.memo[id]; ok {
		return p
	}
	// Find the path to the top level. The visited set stops the loop if
	// the data contains a cycle.
	var path []int64
	visited := map[int64]bool{}
	for cur := &id; cur != nil && !visited[*cur]; {
		if _, ok := a.parent[*cur]; !ok {
			break
		}
		visited[*cur] = true
		path = append(path, *cur)
		cur = a.parent[*cur]
	}
	// Go from the top level down to the node. def is the nearest default
	// role. bound adds the folder bindings.
	def, bound := a.spaceDefault, a.bound
	for i := len(path) - 1; i >= 0; i-- {
		n := path[i]
		if o, ok := a.overrides[n]; ok {
			def = o
		}
		if b, ok := a.folderBound[n]; ok {
			bound = union(bound, b)
		}
	}
	p := union(def, bound)
	a.memo[id] = p
	return p
}

// Can reports whether the user has permission p for the node.
func (a *Access) Can(id int64, p rbac.Permission) bool { return a.For(id).Has(p) }

// Subtree returns the IDs of the descendants of a node.
func (a *Access) Subtree(id int64) []int64 {
	children := map[int64][]int64{}
	for n, p := range a.parent {
		if p != nil {
			children[*p] = append(children[*p], n)
		}
	}
	var out []int64
	visited := map[int64]bool{id: true}
	queue := children[id]
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if visited[n] {
			continue
		}
		visited[n] = true
		out = append(out, n)
		queue = append(queue, children[n]...)
	}
	return out
}

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
