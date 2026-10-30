package store

import (
	"context"
	"crypto/rand"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/clpe/skriva/internal/rbac"
)

// Session is a signed-in browser session.
type Session struct {
	User        *User
	CSRFToken   string
	ExpiresAt   time.Time
	Permissions rbac.Set
}

// CreateSession makes a new session for the user. It returns the secret
// token for the cookie. The database keeps only a hash of the token.
// idToken is the ID token from the login. It can be empty.
func (s *Store) CreateSession(ctx context.Context, userID int64, idToken string, ttl time.Duration) (string, error) {
	token := rand.Text()
	csrf := rand.Text()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO sessions (token_hash, user_id, csrf_token, id_token, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		hashToken(token), userID, csrf, idToken, s.now(), s.now().Add(ttl))
	if err != nil {
		return "", err
	}
	return token, nil
}

// Session finds the session for token. It returns ErrNotFound if the
// session does not exist, is expired, or belongs to a disabled user.
// The result includes the effective permissions of the user.
func (s *Store) Session(ctx context.Context, token, defaultRole string) (*Session, error) {
	var sess Session
	var u User
	var perms []string
	err := s.pool.QueryRow(ctx, `
		SELECT u.id, u.issuer, u.subject, u.username, u.email, u.display_name,
		       u.disabled, u.created_at, u.last_login_at,
		       s.csrf_token, s.expires_at,
		       ARRAY(`+permissionsQuery("$3")+`)
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > $2 AND NOT u.disabled`,
		hashToken(token), s.now(), defaultRole,
	).Scan(&u.ID, &u.Issuer, &u.Subject, &u.Username, &u.Email, &u.DisplayName,
		&u.Disabled, &u.CreatedAt, &u.LastLoginAt,
		&sess.CSRFToken, &sess.ExpiresAt, &perms)
	if err != nil {
		return nil, notFound(err)
	}
	sess.User = &u
	sess.Permissions = rbac.NewSet(perms...)
	return &sess, nil
}

// permissionsQuery returns SQL that selects the effective permissions of
// user u.id. roleParam is the placeholder for the default role name.
func permissionsQuery(roleParam string) string {
	return `
	SELECT DISTINCT rp.permission
	FROM role_permissions rp
	JOIN roles r ON r.id = rp.role_id
	WHERE r.name = ` + roleParam + `
	   OR r.id IN (
		SELECT rb.role_id FROM role_bindings rb WHERE rb.user_id = u.id
		UNION
		SELECT rb.role_id FROM role_bindings rb
		JOIN group_members gm ON gm.group_id = rb.group_id
		WHERE gm.user_id = u.id)`
}

// DeleteSession removes the session for token. It returns the ID token
// of the session, or "" if the session does not exist.
func (s *Store) DeleteSession(ctx context.Context, token string) (string, error) {
	var idToken string
	err := s.pool.QueryRow(ctx,
		"DELETE FROM sessions WHERE token_hash = $1 RETURNING id_token", hashToken(token)).Scan(&idToken)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	return idToken, nil
}

// LoginState is the data that the application keeps between the start
// of a login and the callback from the identity provider.
type LoginState struct {
	Nonce        string
	CodeVerifier string
	ReturnTo     string
}

// SaveLoginState keeps st for the given state value.
func (s *Store) SaveLoginState(ctx context.Context, state string, st LoginState, ttl time.Duration) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO oidc_states (state_hash, nonce, code_verifier, return_to, expires_at)
		VALUES ($1, $2, $3, $4, $5)`,
		hashToken(state), st.Nonce, st.CodeVerifier, st.ReturnTo, s.now().Add(ttl))
	return err
}

// TakeLoginState returns and removes the login state for the given state
// value. Thus each state value is usable only one time.
func (s *Store) TakeLoginState(ctx context.Context, state string) (*LoginState, error) {
	var st LoginState
	var valid bool
	err := s.pool.QueryRow(ctx, `
		DELETE FROM oidc_states WHERE state_hash = $1
		RETURNING nonce, code_verifier, return_to, expires_at > $2`,
		hashToken(state), s.now(),
	).Scan(&st.Nonce, &st.CodeVerifier, &st.ReturnTo, &valid)
	if err != nil {
		return nil, notFound(err)
	}
	if !valid {
		return nil, ErrNotFound
	}
	return &st, nil
}
