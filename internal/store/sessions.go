package store

import (
	"context"
	"crypto/rand"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/clpe/skriva/internal/rbac"
)

// Session is a signed-in browser session. A session belongs to one tenant,
// or to the system administration if TenantID is nil.
type Session struct {
	User        *User
	TenantID    *int64
	CSRFToken   string
	ExpiresAt   time.Time
	Permissions rbac.Set
}

// System reports whether the session is a system administrator session.
func (s *Session) System() bool { return s.TenantID == nil }

// CreateSession makes a new session for the user. It returns the secret
// token for the cookie. The database keeps only a hash of the token.
// idToken is the ID token from the login. It can be empty.
func (s *Store) CreateSession(ctx context.Context, tenantID *int64, userID int64, idToken string, ttl time.Duration) (string, error) {
	token := rand.Text()
	csrf := rand.Text()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO sessions (token_hash, tenant_id, user_id, csrf_token, id_token, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		hashToken(token), tenantID, userID, csrf, idToken, s.now(), s.now().Add(ttl))
	if err != nil {
		return "", err
	}
	return token, nil
}

// Session finds the session for token in a tenant, or a system session if
// tenantID is nil. It returns ErrNotFound if the session does not exist,
// is expired, belongs to a different tenant, or belongs to a disabled
// user or tenant. The result includes the effective permissions of the
// user in the tenant.
func (s *Store) Session(ctx context.Context, token string, tenantID *int64) (*Session, error) {
	var sess Session
	var u User
	var perms []string
	err := s.pool.QueryRow(ctx, `
		SELECT `+prefixed("u.", userColumns)+`,
		       s.tenant_id, s.csrf_token, s.expires_at,
		       ARRAY(`+permissionsQuery+`)
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > $2 AND NOT u.disabled
		  AND s.tenant_id IS NOT DISTINCT FROM $3
		  AND u.tenant_id IS NOT DISTINCT FROM $3
		  AND ($3::bigint IS NULL OR EXISTS (SELECT 1 FROM tenants t WHERE t.id = $3 AND NOT t.disabled))`,
		hashToken(token), s.now(), tenantID,
	).Scan(&u.ID, &u.TenantID, &u.Issuer, &u.Subject, &u.Username, &u.Email, &u.DisplayName,
		&u.Disabled, &u.CreatedAt, &u.LastLoginAt,
		&sess.TenantID, &sess.CSRFToken, &sess.ExpiresAt, &perms)
	if err != nil {
		return nil, notFound(err)
	}
	sess.User = &u
	sess.Permissions = rbac.NewSet(perms...)
	return &sess, nil
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

// SaveLoginState keeps st for the given state value. tenantID is nil for
// a system administrator login.
func (s *Store) SaveLoginState(ctx context.Context, tenantID *int64, state string, st LoginState, ttl time.Duration) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO oidc_states (state_hash, tenant_id, nonce, code_verifier, return_to, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		hashToken(state), tenantID, st.Nonce, st.CodeVerifier, st.ReturnTo, s.now().Add(ttl))
	return err
}

// TakeLoginState returns and removes the login state for the given state
// value. Thus each state value is usable only one time. The state must
// belong to the same tenant.
func (s *Store) TakeLoginState(ctx context.Context, tenantID *int64, state string) (*LoginState, error) {
	var st LoginState
	var valid bool
	err := s.pool.QueryRow(ctx, `
		DELETE FROM oidc_states WHERE state_hash = $1
		RETURNING nonce, code_verifier, return_to,
		          expires_at > $2 AND tenant_id IS NOT DISTINCT FROM $3`,
		hashToken(state), s.now(), tenantID,
	).Scan(&st.Nonce, &st.CodeVerifier, &st.ReturnTo, &valid)
	if err != nil {
		return nil, notFound(err)
	}
	if !valid {
		return nil, ErrNotFound
	}
	return &st, nil
}
