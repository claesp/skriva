package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// User is a person who signed in at least one time. A user belongs to one
// tenant. A user with no tenant (TenantID == nil) is a system
// administrator.
type User struct {
	ID          int64
	TenantID    *int64
	Issuer      string
	Subject     string
	Username    string
	Email       string
	DisplayName string
	Disabled    bool
	CreatedAt   time.Time
	LastLoginAt *time.Time
}

// Name returns the best name to show for the user.
func (u *User) Name() string {
	if u.DisplayName != "" {
		return u.DisplayName
	}
	return u.Username
}

const userColumns = "id, tenant_id, issuer, subject, username, email, display_name, disabled, created_at, last_login_at"

func scanUser(row pgx.Row) (*User, error) {
	var u User
	err := row.Scan(&u.ID, &u.TenantID, &u.Issuer, &u.Subject, &u.Username, &u.Email,
		&u.DisplayName, &u.Disabled, &u.CreatedAt, &u.LastLoginAt)
	if err != nil {
		return nil, notFound(err)
	}
	return &u, nil
}

// Identity is the data about a user that the identity provider sends.
type Identity struct {
	Issuer      string
	Subject     string
	Username    string
	Email       string
	DisplayName string
	// Groups are the names of the OIDC groups of the user.
	Groups []string
	// IDToken is the raw ID token from the login.
	IDToken string
}

// SyncLogin records a login to a tenant, or a login of a system
// administrator if tenantID is nil. It makes or updates the user. For a
// tenant, it also replaces the OIDC group memberships of the user with
// id.Groups. Memberships in local groups do not change.
func (s *Store) SyncLogin(ctx context.Context, tenantID *int64, id Identity) (*User, error) {
	var u *User
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		u, err = scanUser(tx.QueryRow(ctx, `
			INSERT INTO users (tenant_id, issuer, subject, username, email, display_name, last_login_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT ON CONSTRAINT users_identity_key DO UPDATE
			SET username = EXCLUDED.username,
			    email = EXCLUDED.email,
			    display_name = EXCLUDED.display_name,
			    last_login_at = EXCLUDED.last_login_at
			RETURNING `+userColumns,
			tenantID, id.Issuer, id.Subject, id.Username, id.Email, id.DisplayName, s.now()))
		if err != nil {
			return fmt.Errorf("upsert user: %w", err)
		}
		if tenantID == nil {
			return nil
		}

		groups := id.Groups
		if groups == nil {
			groups = []string{}
		}
		// The sorted order of the names prevents a deadlock between two
		// first logins with the same new groups.
		if _, err := tx.Exec(ctx, `
			INSERT INTO groups (tenant_id, name, source)
			SELECT DISTINCT $1::bigint, g, 'oidc' FROM unnest($2::text[]) g ORDER BY g
			ON CONFLICT (tenant_id, source, name) DO NOTHING`, *tenantID, groups); err != nil {
			return fmt.Errorf("insert groups: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			DELETE FROM group_members gm
			USING groups g
			WHERE gm.group_id = g.id AND gm.tenant_id = $1 AND gm.user_id = $2
			  AND g.source = 'oidc' AND NOT (g.name = ANY($3::text[]))`, *tenantID, u.ID, groups); err != nil {
			return fmt.Errorf("remove old memberships: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO group_members (tenant_id, group_id, user_id)
			SELECT $1, id, $2 FROM groups
			WHERE tenant_id = $1 AND source = 'oidc' AND name = ANY($3::text[])
			ON CONFLICT DO NOTHING`, *tenantID, u.ID, groups); err != nil {
			return fmt.Errorf("add memberships: %w", err)
		}
		return nil
	})
	return u, err
}

// User returns a user of a tenant.
func (s *Store) User(ctx context.Context, tenantID, id int64) (*User, error) {
	return scanUser(s.pool.QueryRow(ctx,
		"SELECT "+userColumns+" FROM users WHERE tenant_id = $1 AND id = $2", tenantID, id))
}

// Users returns the users of a tenant, sorted by user name.
func (s *Store) Users(ctx context.Context, tenantID int64) ([]*User, error) {
	rows, err := s.pool.Query(ctx,
		"SELECT "+userColumns+" FROM users WHERE tenant_id = $1 ORDER BY username, id", tenantID)
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

// SetUserDisabled enables or disables a user of a tenant. When the
// function disables a user, it also removes all sessions of that user.
func (s *Store) SetUserDisabled(ctx context.Context, tenantID, id int64, disabled bool) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var username string
		err := tx.QueryRow(ctx, "UPDATE users SET disabled = $3 WHERE tenant_id = $1 AND id = $2 RETURNING username",
			tenantID, id, disabled).Scan(&username)
		if err != nil {
			return notFound(err)
		}
		action := AuditUserEnable
		if disabled {
			action = AuditUserDisable
			if _, err := tx.Exec(ctx, "DELETE FROM sessions WHERE user_id = $1", id); err != nil {
				return err
			}
		}
		return s.audit(ctx, tx, auditEntry{TenantID: &tenantID, Action: action, Target: username})
	})
}
