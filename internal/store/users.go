package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// User is a person who signed in at least one time.
type User struct {
	ID          int64
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

const userColumns = "id, issuer, subject, username, email, display_name, disabled, created_at, last_login_at"

func scanUser(row pgx.Row) (*User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Issuer, &u.Subject, &u.Username, &u.Email,
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
}

// SyncLogin records a login. It makes or updates the user and replaces
// the OIDC group memberships of the user with id.Groups. Memberships in
// local groups do not change.
func (s *Store) SyncLogin(ctx context.Context, id Identity) (*User, error) {
	var u *User
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		u, err = scanUser(tx.QueryRow(ctx, `
			INSERT INTO users (issuer, subject, username, email, display_name, last_login_at)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (issuer, subject) DO UPDATE
			SET username = EXCLUDED.username,
			    email = EXCLUDED.email,
			    display_name = EXCLUDED.display_name,
			    last_login_at = EXCLUDED.last_login_at
			RETURNING `+userColumns,
			id.Issuer, id.Subject, id.Username, id.Email, id.DisplayName, s.now()))
		if err != nil {
			return fmt.Errorf("upsert user: %w", err)
		}

		groups := id.Groups
		if groups == nil {
			groups = []string{}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO groups (name, source)
			SELECT DISTINCT unnest($1::text[]), 'oidc'
			ON CONFLICT (source, name) DO NOTHING`, groups); err != nil {
			return fmt.Errorf("insert groups: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			DELETE FROM group_members gm
			USING groups g
			WHERE gm.group_id = g.id AND gm.user_id = $1
			  AND g.source = 'oidc' AND NOT (g.name = ANY($2::text[]))`, u.ID, groups); err != nil {
			return fmt.Errorf("remove old memberships: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO group_members (group_id, user_id)
			SELECT id, $1 FROM groups WHERE source = 'oidc' AND name = ANY($2::text[])
			ON CONFLICT DO NOTHING`, u.ID, groups); err != nil {
			return fmt.Errorf("add memberships: %w", err)
		}
		return nil
	})
	return u, err
}

// User returns the user with the given ID.
func (s *Store) User(ctx context.Context, id int64) (*User, error) {
	return scanUser(s.pool.QueryRow(ctx, "SELECT "+userColumns+" FROM users WHERE id = $1", id))
}

// Users returns all users, sorted by user name.
func (s *Store) Users(ctx context.Context) ([]*User, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+userColumns+" FROM users ORDER BY username, id")
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

// SetUserDisabled enables or disables a user. When the function disables
// a user, it also removes all sessions of that user.
func (s *Store) SetUserDisabled(ctx context.Context, id int64, disabled bool) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, "UPDATE users SET disabled = $2 WHERE id = $1", id, disabled)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		if disabled {
			_, err = tx.Exec(ctx, "DELETE FROM sessions WHERE user_id = $1", id)
		}
		return err
	})
}
