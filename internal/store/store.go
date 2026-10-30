// Package store contains all database queries of the application.
package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Errors that the store returns.
var (
	ErrNotFound = errors.New("not found")
	// ErrConflict tells that the data changed after the caller read it,
	// or that a unique value exists already.
	ErrConflict = errors.New("conflict")
)

// Store gives access to the database.
type Store struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// New makes a Store that uses pool.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, now: time.Now}
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
