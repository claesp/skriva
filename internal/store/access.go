package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/clpe/skriva/internal/rbac"
)

// querier is the part of pgxpool.Pool and pgx.Tx that the helpers use.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Group sources.
const (
	GroupOIDC  = "oidc"
	GroupLocal = "local"
)

// Group is a set of users in a tenant.
type Group struct {
	ID          int64
	Name        string
	Source      string
	Description string
	Members     int
}

// Role is a named set of permissions. Built-in roles apply to all
// tenants. A custom role belongs to one tenant.
type Role struct {
	ID          int64
	Name        string
	Description string
	Builtin     bool
	Permissions []rbac.Permission
}

// Binding gives a role to a user or to a group, in all spaces or in one
// space.
type Binding struct {
	ID       int64
	RoleName string
	// SpaceID is zero for a binding that applies in all spaces.
	SpaceID   int64
	SpaceSlug string
	SpaceName string
	// NodeID is not zero for a binding that applies to one folder.
	NodeID       int64
	NodePublicID string
	NodeTitle    string
	// Exactly one of UserID and GroupID is not zero.
	UserID      int64
	UserName    string
	GroupID     int64
	GroupName   string
	GroupSource string
}

// Groups returns the groups of a tenant, sorted by source and name.
func (s *Store) Groups(ctx context.Context, tenantID int64) ([]Group, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT g.id, g.name, g.source, g.description,
		       (SELECT count(*) FROM group_members gm WHERE gm.group_id = g.id)
		FROM groups g WHERE g.tenant_id = $1
		ORDER BY g.source, lower(g.name)`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.Name, &g.Source, &g.Description, &g.Members); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// Group returns a group of a tenant.
func (s *Store) Group(ctx context.Context, tenantID, id int64) (*Group, error) {
	var g Group
	err := s.pool.QueryRow(ctx, `
		SELECT g.id, g.name, g.source, g.description,
		       (SELECT count(*) FROM group_members gm WHERE gm.group_id = g.id)
		FROM groups g WHERE g.tenant_id = $1 AND g.id = $2`, tenantID, id,
	).Scan(&g.ID, &g.Name, &g.Source, &g.Description, &g.Members)
	if err != nil {
		return nil, notFound(err)
	}
	return &g, nil
}

// CreateLocalGroup makes a local group in a tenant.
func (s *Store) CreateLocalGroup(ctx context.Context, tenantID int64, name, description string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO groups (tenant_id, name, source, description) VALUES ($1, $2, 'local', $3)
		RETURNING id`, tenantID, name, description).Scan(&id)
	if sqlState(err) == codeUniqueViolation {
		return 0, ErrConflict
	}
	return id, err
}

// EnsureOIDCGroup makes the OIDC group name in a tenant if it does not
// exist. It returns the ID of the group.
func (s *Store) EnsureOIDCGroup(ctx context.Context, tenantID int64, name string) (int64, error) {
	return ensureOIDCGroup(ctx, s.pool, tenantID, name)
}

func ensureOIDCGroup(ctx context.Context, q querier, tenantID int64, name string) (int64, error) {
	var id int64
	err := q.QueryRow(ctx, `
		WITH ins AS (
			INSERT INTO groups (tenant_id, name, source) VALUES ($1, $2, 'oidc')
			ON CONFLICT (tenant_id, source, name) DO NOTHING RETURNING id)
		SELECT id FROM ins
		UNION ALL
		SELECT id FROM groups WHERE tenant_id = $1 AND source = 'oidc' AND name = $2
		LIMIT 1`, tenantID, name).Scan(&id)
	return id, err
}

// DeleteGroup removes a local group. OIDC groups come from the identity
// provider, thus the function does not remove them.
func (s *Store) DeleteGroup(ctx context.Context, tenantID, id int64) error {
	tag, err := s.pool.Exec(ctx,
		"DELETE FROM groups WHERE tenant_id = $1 AND id = $2 AND source = 'local'", tenantID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GroupMembers returns the members of a group.
func (s *Store) GroupMembers(ctx context.Context, tenantID, groupID int64) ([]*User, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+prefixed("u.", userColumns)+`
		FROM users u JOIN group_members gm ON gm.user_id = u.id
		WHERE gm.tenant_id = $1 AND gm.group_id = $2 ORDER BY u.username`, tenantID, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UserGroups returns the groups of a user.
func (s *Store) UserGroups(ctx context.Context, tenantID, userID int64) ([]Group, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT g.id, g.name, g.source, g.description
		FROM groups g JOIN group_members gm ON gm.group_id = g.id
		WHERE gm.tenant_id = $1 AND gm.user_id = $2
		ORDER BY g.source, lower(g.name)`, tenantID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.Name, &g.Source, &g.Description); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// AddLocalGroupMember adds a user to a local group. The group and the user
// must be in the tenant.
func (s *Store) AddLocalGroupMember(ctx context.Context, tenantID, groupID, userID int64) error {
	var exists bool
	err := s.pool.QueryRow(ctx, `
		WITH ins AS (
			INSERT INTO group_members (tenant_id, group_id, user_id)
			SELECT $1, g.id, u.id FROM groups g, users u
			WHERE g.tenant_id = $1 AND g.id = $2 AND g.source = 'local'
			  AND u.tenant_id = $1 AND u.id = $3
			ON CONFLICT DO NOTHING RETURNING 1)
		SELECT EXISTS (SELECT 1 FROM ins)
		    OR EXISTS (SELECT 1 FROM group_members gm JOIN groups g ON g.id = gm.group_id
		               WHERE gm.tenant_id = $1 AND gm.group_id = $2 AND gm.user_id = $3
		                 AND g.source = 'local')`,
		tenantID, groupID, userID).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

// RemoveLocalGroupMember removes a user from a local group.
func (s *Store) RemoveLocalGroupMember(ctx context.Context, tenantID, groupID, userID int64) error {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM group_members gm USING groups g
		WHERE gm.group_id = g.id AND g.source = 'local'
		  AND gm.tenant_id = $1 AND gm.group_id = $2 AND gm.user_id = $3`, tenantID, groupID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Roles returns the built-in roles and the custom roles of a tenant.
func (s *Store) Roles(ctx context.Context, tenantID int64) ([]Role, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.name, r.description, r.builtin,
		       ARRAY(SELECT permission FROM role_permissions rp WHERE rp.role_id = r.id ORDER BY permission)
		FROM roles r WHERE r.tenant_id IS NULL OR r.tenant_id = $1
		ORDER BY r.builtin DESC, r.id`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Role
	for rows.Next() {
		var r Role
		var perms []string
		if err := rows.Scan(&r.ID, &r.Name, &r.Description, &r.Builtin, &perms); err != nil {
			return nil, err
		}
		for _, p := range perms {
			r.Permissions = append(r.Permissions, rbac.Permission(p))
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RoleExists reports whether the tenant can use a role with the given name.
func (s *Store) RoleExists(ctx context.Context, tenantID int64, name string) (bool, error) {
	err := checkRole(ctx, s.pool, tenantID, name)
	if err == ErrInvalid {
		return false, nil
	}
	return err == nil, err
}

// CreateRole makes a custom role in a tenant. It returns ErrConflict if
// the tenant or a built-in role has the name already.
func (s *Store) CreateRole(ctx context.Context, tenantID int64, name, description string, perms []rbac.Permission) error {
	names := make([]string, len(perms))
	for i, p := range perms {
		if !p.Valid() {
			return fmt.Errorf("unknown permission %q", p)
		}
		names[i] = string(p)
	}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var builtin bool
		if err := tx.QueryRow(ctx,
			"SELECT EXISTS (SELECT 1 FROM roles WHERE builtin AND name = $1)", name).Scan(&builtin); err != nil {
			return err
		}
		if builtin {
			return ErrConflict
		}
		var id int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO roles (tenant_id, name, description) VALUES ($1, $2, $3) RETURNING id`,
			tenantID, name, description).Scan(&id); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO role_permissions (role_id, permission)
			SELECT DISTINCT $1::bigint, unnest($2::text[])`, id, names)
		return err
	})
	if sqlState(err) == codeUniqueViolation {
		return ErrConflict
	}
	return err
}

// DeleteRole removes a custom role of a tenant and its bindings. Built-in
// roles are permanent.
func (s *Store) DeleteRole(ctx context.Context, tenantID, id int64) error {
	tag, err := s.pool.Exec(ctx,
		"DELETE FROM roles WHERE tenant_id = $1 AND id = $2 AND NOT builtin", tenantID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Bindings returns the role bindings of a tenant.
func (s *Store) Bindings(ctx context.Context, tenantID int64) ([]Binding, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT b.id, r.name,
		       COALESCE(sp.id, 0), COALESCE(sp.slug, ''), COALESCE(sp.name, ''),
		       COALESCE(n.id, 0), COALESCE(n.public_id, ''), COALESCE(n.title, ''),
		       COALESCE(u.id, 0), COALESCE(u.username, ''),
		       COALESCE(g.id, 0), COALESCE(g.name, ''), COALESCE(g.source, '')
		FROM role_bindings b
		JOIN roles r ON r.id = b.role_id
		LEFT JOIN spaces sp ON sp.id = b.space_id
		LEFT JOIN nodes n ON n.id = b.node_id
		LEFT JOIN users u ON u.id = b.user_id
		LEFT JOIN groups g ON g.id = b.group_id
		WHERE b.tenant_id = $1
		ORDER BY sp.name NULLS FIRST, n.title NULLS FIRST, r.name, g.name NULLS LAST, u.username`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Binding
	for rows.Next() {
		var b Binding
		if err := rows.Scan(&b.ID, &b.RoleName, &b.SpaceID, &b.SpaceSlug, &b.SpaceName,
			&b.NodeID, &b.NodePublicID, &b.NodeTitle, &b.UserID, &b.UserName,
			&b.GroupID, &b.GroupName, &b.GroupSource); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Scope is the scope of a role binding. The zero value is the full
// tenant. With SpaceID, the binding applies in one space. With SpaceID and
// NodeID, the binding applies to one folder or document and its subtree.
type Scope struct {
	SpaceID int64
	NodeID  int64
}

func (sc Scope) ids() (spaceID, nodeID *int64) {
	if sc.SpaceID != 0 {
		spaceID = &sc.SpaceID
	}
	if sc.NodeID != 0 {
		nodeID = &sc.NodeID
	}
	return spaceID, nodeID
}

// BindRoleToUser gives a role to a user of the tenant, in the given scope.
func (s *Store) BindRoleToUser(ctx context.Context, tenantID int64, roleName string, userID int64, sc Scope) error {
	return s.bindScoped(ctx, tenantID, roleName, &userID, nil, sc)
}

// BindRoleToGroup gives a role to a group of the tenant, in the given
// scope.
func (s *Store) BindRoleToGroup(ctx context.Context, tenantID int64, roleName string, groupID int64, sc Scope) error {
	return s.bindScoped(ctx, tenantID, roleName, nil, &groupID, sc)
}

// bindScoped makes a role binding. A binding for a node changes the access
// paths of the subtree of the node. Thus the function refreshes them in
// the same transaction.
func (s *Store) bindScoped(ctx context.Context, tenantID int64, roleName string, userID, groupID *int64, sc Scope) error {
	if sc.NodeID == 0 {
		return bind(ctx, s.pool, tenantID, roleName, userID, groupID, sc)
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockSpace(ctx, tx, sc.SpaceID); err != nil {
			return err
		}
		if err := bind(ctx, tx, tenantID, roleName, userID, groupID, sc); err != nil {
			return err
		}
		return refreshAccess(ctx, tx, sc.SpaceID, sc.NodeID)
	})
}

// bind makes a role binding. The role must be a built-in role or a custom
// role of the tenant. The foreign keys make sure that the user, the group,
// the space and the node are in the tenant.
func bind(ctx context.Context, q querier, tenantID int64, roleName string, userID, groupID *int64, sc Scope) error {
	if err := checkRole(ctx, q, tenantID, roleName); err != nil {
		if err == ErrInvalid {
			return ErrNotFound
		}
		return err
	}
	spaceID, nodeID := sc.ids()
	_, err := q.Exec(ctx, `
		INSERT INTO role_bindings (tenant_id, role_id, user_id, group_id, space_id, node_id)
		SELECT $1, id, $3, $4, $5, $6 FROM roles
		WHERE name = $2 AND (tenant_id IS NULL OR tenant_id = $1)
		ON CONFLICT DO NOTHING`, tenantID, roleName, userID, groupID, spaceID, nodeID)
	if sqlState(err) == codeForeignKeyViolation {
		// The user, the group or the space does not exist in the tenant.
		return ErrNotFound
	}
	return err
}

// DeleteBinding removes a role binding of a tenant. For a binding of a
// node, it also refreshes the access paths of the subtree of the node.
func (s *Store) DeleteBinding(ctx context.Context, tenantID, id int64) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var spaceID, nodeID *int64
		err := tx.QueryRow(ctx, "SELECT space_id, node_id FROM role_bindings WHERE tenant_id = $1 AND id = $2",
			tenantID, id).Scan(&spaceID, &nodeID)
		if err != nil {
			return notFound(err)
		}
		if nodeID != nil {
			if err := lockSpace(ctx, tx, *spaceID); err != nil {
				return err
			}
		}
		tag, err := tx.Exec(ctx, "DELETE FROM role_bindings WHERE tenant_id = $1 AND id = $2", tenantID, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		if nodeID != nil {
			return refreshAccess(ctx, tx, *spaceID, *nodeID)
		}
		return nil
	})
}

func prefixed(prefix, columns string) string {
	cols := strings.Split(columns, ",")
	for i, c := range cols {
		cols[i] = prefix + strings.TrimSpace(c)
	}
	return strings.Join(cols, ", ")
}
