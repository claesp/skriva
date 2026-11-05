// Package store contains all database queries of the application.
//
// The functions for users, groups, roles, spaces and settings take the
// tenant ID as a parameter and use it in each query. The functions for
// the data in a space take the space ID. A space ID is unique in all
// tenants, and the caller gets it from Space with the tenant ID. Thus a
// request for one tenant cannot read or change the data of a different
// tenant.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claesp/skriva/internal/secret"
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
	// keyPool is a small pool only for the keys. The store reads keys
	// also in a transaction or in a loop over rows, which hold a
	// connection of pool. With one pool, these requests can wait for
	// each other until all connections are in use.
	keyPool *pgxpool.Pool
	box     *secret.Box
	now     func() time.Time
	// kek wraps the keys of the tenants. keys keeps the unwrapped keys
	// in memory.
	kek  KEK
	keys *keyring
}

// New makes a Store that uses pool. box encrypts the client secrets of
// the tenants, and it is the key encryption key (KEK) of the keys of the
// tenants. Call Close when the Store is not necessary any more.
func New(pool *pgxpool.Pool, box *secret.Box) *Store {
	cfg := pool.Config()
	cfg.MaxConns, cfg.MinConns = keyPoolConns, 0
	// NewWithConfig does not connect, thus it fails only for a
	// configuration that is not valid. The configuration of pool is valid.
	keyPool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		panic("store: key pool: " + err.Error())
	}
	return &Store{pool: pool, keyPool: keyPool, box: box, now: time.Now, kek: localKEK{box}, keys: newKeyring()}
}

// keyPoolConns is the largest number of connections for the keys.
const keyPoolConns = 2

// Close closes the connections of the store for the keys. It does not
// close the pool of New.
func (s *Store) Close() { s.keyPool.Close() }

// Ping makes sure that the database responds.
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// DeleteExpired removes expired sessions and login states, the items
// that are in the trash for more than TrashDays days, the audit events
// that are older than AuditDays days, and the uploaded files that no node
// uses.
func (s *Store) DeleteExpired(ctx context.Context) error {
	now := s.now()
	// Each step runs also if a step before it fails. Thus one value that
	// cannot be decrypted does not stop all the cleanup.
	exec := func(sql string, args ...any) error {
		_, err := s.pool.Exec(ctx, sql, args...)
		return err
	}
	return errors.Join(
		exec("DELETE FROM sessions WHERE expires_at < $1", now),
		exec("DELETE FROM oidc_states WHERE expires_at < $1", now),
		s.purgeTrash(ctx),
		exec("DELETE FROM audit_events WHERE created_at < $1", now.AddDate(0, 0, -AuditDays)),
		// A file that no node uses after one day is an upload that the
		// user did not save in a document.
		exec(`
			DELETE FROM files f WHERE f.created_at < $1
			AND NOT EXISTS (SELECT 1 FROM file_refs r WHERE r.file_id = f.id)`, now.Add(-unusedFileAge)),
	)
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
