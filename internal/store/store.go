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

// DeleteExpired removes expired sessions and login states.
func (s *Store) DeleteExpired(ctx context.Context) error {
	now := s.now()
	if _, err := s.pool.Exec(ctx, "DELETE FROM sessions WHERE expires_at < $1", now); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, "DELETE FROM oidc_states WHERE expires_at < $1", now)
	return err
}

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
const PublicIDLength = 12

const base62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// NewPublicID returns a random ID with PublicIDLength characters from
// [0-9A-Za-z]. The ID has approximately 71 bits of entropy.
func NewPublicID() string {
	out := make([]byte, 0, PublicIDLength)
	buf := make([]byte, PublicIDLength*2)
	for len(out) < PublicIDLength {
		_, _ = rand.Read(buf) // crypto/rand.Read does not return errors.
		for _, b := range buf {
			// Reject values >= 248 (4*62). Thus all characters have the
			// same probability.
			if b < 248 && len(out) < PublicIDLength {
				out = append(out, base62[b%62])
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
		if !('0' <= c && c <= '9' || 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z') {
			return false
		}
	}
	return true
}
