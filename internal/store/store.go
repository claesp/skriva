// Package store contains all database queries of the application.
//
// All functions that read or change tenant data take the tenant ID as a
// parameter and use it in each query. Thus a request for one tenant
// cannot read or change the data of a different tenant.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/clpe/skriva/internal/secret"
)

// Errors that the store returns.
var (
	ErrNotFound = errors.New("not found")
	// ErrConflict tells that the data changed after the caller read it,
	// or that a unique value exists already.
	ErrConflict = errors.New("conflict")
	// ErrInvalid tells that the request is not permitted for the data,
	// for example a move of a folder into its own subtree.
	ErrInvalid = errors.New("invalid request")
)

// Store gives access to the database.
type Store struct {
	pool *pgxpool.Pool
	box  *secret.Box
	now  func() time.Time
}

// New makes a Store that uses pool. box encrypts the secrets of the
// tenants.
func New(pool *pgxpool.Pool, box *secret.Box) *Store {
	return &Store{pool: pool, box: box, now: time.Now}
}

// Ping makes sure that the database responds.
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// DeleteExpired removes expired sessions and login states, the items
// that are in the trash for more than TrashDays days, the audit events
// that are older than AuditDays days, and the uploaded files that no node
// uses.
func (s *Store) DeleteExpired(ctx context.Context) error {
	now := s.now()
	if _, err := s.pool.Exec(ctx, "DELETE FROM sessions WHERE expires_at < $1", now); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, "DELETE FROM oidc_states WHERE expires_at < $1", now); err != nil {
		return err
	}
	if err := s.purgeTrash(ctx); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, "DELETE FROM audit_events WHERE created_at < $1",
		now.AddDate(0, 0, -AuditDays)); err != nil {
		return err
	}
	// A file that no node uses after one day is an upload that the user
	// did not save in a document.
	_, err := s.pool.Exec(ctx, `
		DELETE FROM files f WHERE f.created_at < $1
		AND NOT EXISTS (SELECT 1 FROM file_refs r WHERE r.file_id = f.id)`, now.Add(-unusedFileAge))
	return err
}

// unusedFileAge is the time after which DeleteExpired removes a file that
// no node uses.
const unusedFileAge = 24 * time.Hour

func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// sqlState returns the PostgreSQL error code of err, or "".
func sqlState(err error) string {
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState()
	}
	return ""
}

// PostgreSQL error codes.
const (
	codeUniqueViolation     = "23505"
	codeForeignKeyViolation = "23503"
	codeCheckViolation      = "23514"
)

// PublicIDLength is the length of a public node ID.
const PublicIDLength = 14

// The public ID uses only lower-case letters and digits. Thus the URLs
// are always in lower case.
const base36 = "0123456789abcdefghijklmnopqrstuvwxyz"

// NewPublicID returns a random ID with PublicIDLength characters from
// [0-9a-z]. The ID has approximately 72 bits of entropy.
func NewPublicID() string {
	out := make([]byte, 0, PublicIDLength)
	buf := make([]byte, PublicIDLength*2)
	for len(out) < PublicIDLength {
		_, _ = rand.Read(buf) // crypto/rand.Read does not return errors.
		for _, b := range buf {
			// Reject values >= 252 (7*36). Thus all characters have the
			// same probability.
			if b < 252 && len(out) < PublicIDLength {
				out = append(out, base36[b%36])
			}
		}
	}
	return string(out)
}

// ValidPublicID reports whether id has the format of a public node ID.
func ValidPublicID(id string) bool {
	if len(id) != PublicIDLength {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'z') {
			return false
		}
	}
	return true
}
