package store

import (
	"context"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/claesp/skriva/internal/rbac"
	"github.com/claesp/skriva/internal/secret"
)

// Session is a signed-in browser session. A session belongs to one tenant,
// or to the system administration if TenantID is nil.
type Session struct {
	User        *User
	TenantID    *int64
	CSRFToken   string
	Permissions rbac.Set
}

// System reports whether the session is a system administrator session.
func (s *Session) System() bool { return s.TenantID == nil }

// CreateSession makes a new session for the user. It returns the secret
// token for the cookie. The database keeps only a hash of the token.
// idToken is the ID token from the login. It can be empty. The database
// keeps it encrypted with a key from the token (see sessionBox).
func (s *Store) CreateSession(ctx context.Context, tenantID *int64, userID int64, idToken string, ttl time.Duration) (string, error) {
	token := rand.Text()
	csrf := rand.Text()
	var sealed []byte
	if idToken != "" {
		box, err := sessionBox(token)
		if err != nil {
			return "", err
		}
		sealed = box.Seal([]byte(idToken), idTokenContext)
	}
	err := s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO sessions (token_hash, tenant_id, user_id, csrf_token, id_token, created_at, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			hashToken(token), tenantID, userID, csrf, sealed, s.now(), s.now().Add(ttl)); err != nil {
			return err
		}
		// A login is an event of the tenant of the user.
		return s.audit(ctx, tx, auditEntry{TenantID: tenantID, ActorID: userID, Action: AuditLogin})
	})
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
		       s.tenant_id, s.csrf_token,
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
		&sess.TenantID, &sess.CSRFToken, &perms)
	if err != nil {
		return nil, notFound(err)
	}
	sess.User = &u
	sess.Permissions = rbac.NewSet(perms...)
	return &sess, nil
}

// DeleteSession removes the session for token. It returns the ID token
// of the session, or "" if the session does not exist or has no ID
// token. If the ID token cannot be decrypted, the session is removed and
// the result is "".
func (s *Store) DeleteSession(ctx context.Context, token string) (string, error) {
	var sealed []byte
	err := s.pool.QueryRow(ctx,
		"DELETE FROM sessions WHERE token_hash = $1 RETURNING id_token", hashToken(token)).Scan(&sealed)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	if sealed == nil {
		return "", nil
	}
	box, err := sessionBox(token)
	if err != nil {
		return "", err
	}
	idToken, err := box.Open(sealed, idTokenContext)
	if err != nil {
		// The logout continues without the ID token. Then the identity
		// provider asks the user to confirm the logout.
		return "", nil
	}
	return string(idToken), nil
}

// idTokenContext is the associated data of an encrypted ID token.
const idTokenContext = "session id_token"

// sessionBox returns the key that encrypts the ID token of a session. The
// key comes from the session token (HKDF-SHA256). The database keeps only
// a hash of the token. Thus only a request with the cookie can decrypt
// the ID token, and a copy of the database does not contain it.
func sessionBox(token string) (*secret.Box, error) {
	key, err := hkdf.Key(sha256.New, []byte(token), nil, "skriva session id_token", secret.KeySize)
	if err != nil {
		return nil, err
	}
	return secret.New(key)
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
