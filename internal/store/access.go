package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/clpe/skriva/internal/db"
	"github.com/clpe/skriva/internal/rbac"
)

// Group sources.
const (
	GroupOIDC  = "oidc"
	GroupLocal = "local"
)

// Group is a set of users.
type Group struct {
	ID          int64
	Name        string
	Source      string
	Description string
	Members     int
}

// Role is a named set of permissions.
type Role struct {
	ID          int64
	Name        string
	Description string
	Builtin     bool
	Permissions []rbac.Permission
}

// Binding gives a role to a user or to a group.
type Binding struct {
	ID       int64
	RoleName string
	// Exactly one of UserID and GroupID is not zero.
	UserID      int64
	UserName    string
	GroupID     int64
	GroupName   string
	GroupSource string
}

// Groups returns all groups, sorted by source and name.
func (s *Store) Groups(ctx context.Context) ([]Group, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT g.id, g.name, g.source, g.description,
		       (SELECT count(*) FROM group_members gm WHERE gm.group_id = g.id)
		FROM groups g ORDER BY g.source, lower(g.name)`)
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

// Group returns the group with the given ID.
func (s *Store) Group(ctx context.Context, id int64) (*Group, error) {
	var g Group
	err := s.pool.QueryRow(ctx, `
		SELECT g.id, g.name, g.source, g.description,
		       (SELECT count(*) FROM group_members gm WHERE gm.group_id = g.id)
		FROM groups g WHERE g.id = $1`, id,
	).Scan(&g.ID, &g.Name, &g.Source, &g.Description, &g.Members)
	if err != nil {
		return nil, notFound(err)
	}
	return &g, nil
}

// CreateLocalGroup makes a local group.
func (s *Store) CreateLocalGroup(ctx context.Context, name, description string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO groups (name, source, description) VALUES ($1, 'local', $2)
		RETURNING id`, name, description).Scan(&id)
	if db.IsUniqueViolation(err) {
		return 0, ErrConflict
	}
	return id, err
}

// EnsureOIDCGroup makes the OIDC group name if it does not exist.
func (s *Store) EnsureOIDCGroup(ctx context.Context, name string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		WITH ins AS (
			INSERT INTO groups (name, source) VALUES ($1, 'oidc')
			ON CONFLICT (source, name) DO NOTHING RETURNING id)
		SELECT id FROM ins
		UNION ALL
		SELECT id FROM groups WHERE source = 'oidc' AND name = $1
		LIMIT 1`, name).Scan(&id)
	return id, err
}

// DeleteGroup removes a local group. OIDC groups come from the identity
// provider, thus the function does not remove them.
func (s *Store) DeleteGroup(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM groups WHERE id = $1 AND source = 'local'", id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GroupMembers returns the members of a group.
func (s *Store) GroupMembers(ctx context.Context, groupID int64) ([]*User, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+prefixed("u.", userColumns)+`
		FROM users u JOIN group_members gm ON gm.user_id = u.id
		WHERE gm.group_id = $1 ORDER BY u.username`, groupID)
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
func (s *Store) UserGroups(ctx context.Context, userID int64) ([]Group, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT g.id, g.name, g.source, g.description
		FROM groups g JOIN group_members gm ON gm.group_id = g.id
		WHERE gm.user_id = $1 ORDER BY g.source, lower(g.name)`, userID)
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

// AddLocalGroupMember adds a user to a local group.
func (s *Store) AddLocalGroupMember(ctx context.Context, groupID, userID int64) error {
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO group_members (group_id, user_id)
		SELECT g.id, u.id FROM groups g, users u
		WHERE g.id = $1 AND g.source = 'local' AND u.id = $2
		ON CONFLICT DO NOTHING`, groupID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		// Either the user is a member already, or the group or the user
		// does not exist.
		var ok bool
		err := s.pool.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM group_members WHERE group_id = $1 AND user_id = $2)`, groupID, userID).Scan(&ok)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
	}
	return nil
}

// RemoveLocalGroupMember removes a user from a local group.
func (s *Store) RemoveLocalGroupMember(ctx context.Context, groupID, userID int64) error {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM group_members gm USING groups g
		WHERE gm.group_id = g.id AND g.source = 'local'
		  AND gm.group_id = $1 AND gm.user_id = $2`, groupID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Roles returns all roles with their permissions.
func (s *Store) Roles(ctx context.Context) ([]Role, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.name, r.description, r.builtin,
		       ARRAY(SELECT permission FROM role_permissions rp WHERE rp.role_id = r.id ORDER BY permission)
		FROM roles r ORDER BY r.builtin DESC, r.id`)
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

// RoleExists reports whether a role with the given name exists.
func (s *Store) RoleExists(ctx context.Context, name string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM roles WHERE name = $1)", name).Scan(&ok)
	return ok, err
}

// CreateRole makes a custom role.
func (s *Store) CreateRole(ctx context.Context, name, description string, perms []rbac.Permission) error {
	for _, p := range perms {
		if !p.Valid() {
			return fmt.Errorf("unknown permission %q", p)
		}
	}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var id int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO roles (name, description) VALUES ($1, $2) RETURNING id`,
			name, description).Scan(&id); err != nil {
			return err
		}
		names := make([]string, len(perms))
		for i, p := range perms {
			names[i] = string(p)
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO role_permissions (role_id, permission)
			SELECT DISTINCT $1::bigint, unnest($2::text[])`, id, names)
		return err
	})
	if db.IsUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

// DeleteRole removes a custom role and its bindings. Built-in roles are
// permanent.
func (s *Store) DeleteRole(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM roles WHERE id = $1 AND NOT builtin", id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Bindings returns all role bindings.
func (s *Store) Bindings(ctx context.Context) ([]Binding, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT b.id, r.name,
		       COALESCE(u.id, 0), COALESCE(u.username, ''),
		       COALESCE(g.id, 0), COALESCE(g.name, ''), COALESCE(g.source, '')
		FROM role_bindings b
		JOIN roles r ON r.id = b.role_id
		LEFT JOIN users u ON u.id = b.user_id
		LEFT JOIN groups g ON g.id = b.group_id
		ORDER BY r.name, g.name NULLS LAST, u.username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Binding
	for rows.Next() {
		var b Binding
		if err := rows.Scan(&b.ID, &b.RoleName, &b.UserID, &b.UserName,
			&b.GroupID, &b.GroupName, &b.GroupSource); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// BindRoleToUser gives a role to a user.
func (s *Store) BindRoleToUser(ctx context.Context, roleName string, userID int64) error {
	return s.bind(ctx, roleName, &userID, nil)
}

// BindRoleToGroup gives a role to a group.
func (s *Store) BindRoleToGroup(ctx context.Context, roleName string, groupID int64) error {
	return s.bind(ctx, roleName, nil, &groupID)
}

func (s *Store) bind(ctx context.Context, roleName string, userID, groupID *int64) error {
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO role_bindings (role_id, user_id, group_id)
		SELECT id, $2, $3 FROM roles WHERE name = $1
		ON CONFLICT DO NOTHING`, roleName, userID, groupID)
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) && pgErr.SQLState() == "23503" {
		// The user or the group does not exist.
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		exists, err := s.RoleExists(ctx, roleName)
		if err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
	}
	return nil
}

// DeleteBinding removes a role binding.
func (s *Store) DeleteBinding(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM role_bindings WHERE id = $1", id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UserPermissions returns the effective permissions of a user.
func (s *Store) UserPermissions(ctx context.Context, userID int64, defaultRole string) (rbac.Set, error) {
	var perms []string
	err := s.pool.QueryRow(ctx,
		`SELECT ARRAY(`+permissionsQuery("$2")+`) FROM users u WHERE u.id = $1`,
		userID, defaultRole).Scan(&perms)
	if err != nil {
		return nil, notFound(err)
	}
	return rbac.NewSet(perms...), nil
}

func prefixed(prefix, columns string) string {
	cols := strings.Split(columns, ",")
	for i, c := range cols {
		cols[i] = prefix + strings.TrimSpace(c)
	}
	return strings.Join(cols, ", ")
}
