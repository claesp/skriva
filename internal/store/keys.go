package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/clpe/skriva/internal/secret"
)

// KEK is a key encryption key. It encrypts (wraps) the keys of the
// tenants. The KEK is outside the database, thus a copy of the database
// does not contain the keys in plain text. An implementation can use a
// key management system, for example HashiCorp Vault Transit.
type KEK interface {
	// ID identifies the KEK. It must not tell the key.
	ID() string
	Wrap(ctx context.Context, key []byte, aad string) ([]byte, error)
	Unwrap(ctx context.Context, wrapped []byte, aad string) ([]byte, error)
}

// localKEK is a KEK from the configuration (SKRIVA_ENCRYPTION_KEY).
type localKEK struct{ box *secret.Box }

func (k localKEK) ID() string { return "local:" + k.box.ID() }

func (k localKEK) Wrap(_ context.Context, key []byte, aad string) ([]byte, error) {
	return k.box.Seal(key, aad), nil
}

func (k localKEK) Unwrap(_ context.Context, wrapped []byte, aad string) ([]byte, error) {
	return k.box.Open(wrapped, aad)
}

// Key purposes.
const (
	purposeData   = "data"
	purposeSearch = "search"
)

// keyCacheTTL is the time after which the store reads the current key
// version of a tenant again. A different server can rotate the key.
const keyCacheTTL = 5 * time.Minute

// retiredKeyAge is the minimum age of a retired key before the store
// deletes it. It is longer than keyCacheTTL. Thus no server still
// encrypts with the key.
const retiredKeyAge = time.Hour

// ErrKey tells that a key is missing or that data cannot be decrypted.
var ErrKey = errors.New("encryption key error")

// tenantKeys are the keys of one tenant in memory.
type tenantKeys struct {
	data    map[int]cipher.AEAD
	current int
	search  []byte
	loaded  time.Time
}

// keyring keeps the keys of the tenants in memory, and the tenant of
// each space. It is safe for concurrent use.
type keyring struct {
	mu          sync.Mutex
	tenants     map[int64]*tenantKeys
	spaceTenant map[int64]int64
}

func newKeyring() *keyring {
	return &keyring{tenants: map[int64]*tenantKeys{}, spaceTenant: map[int64]int64{}}
}

func keyAAD(tenantID int64, purpose string, version int) string {
	return "tenant-key:" + strconv.FormatInt(tenantID, 10) + ":" + purpose + ":" + strconv.Itoa(version)
}

// tenantOfSpace returns the tenant of a space. The tenant of a space does
// not change, thus the result stays in memory.
func (s *Store) tenantOfSpace(ctx context.Context, spaceID int64) (int64, error) {
	s.keys.mu.Lock()
	t, ok := s.keys.spaceTenant[spaceID]
	s.keys.mu.Unlock()
	if ok {
		return t, nil
	}
	if err := s.pool.QueryRow(ctx, "SELECT tenant_id FROM spaces WHERE id = $1", spaceID).Scan(&t); err != nil {
		return 0, notFound(err)
	}
	s.keys.mu.Lock()
	s.keys.spaceTenant[spaceID] = t
	s.keys.mu.Unlock()
	return t, nil
}

// tenantKeys returns the keys of a tenant. It makes the keys if the
// tenant has no keys. It uses its own transaction, never the transaction
// of the caller: a key that encrypts data must be saved, also if the
// transaction of the caller fails.
func (s *Store) tenantKeys(ctx context.Context, tenantID int64) (*tenantKeys, error) {
	if p, ok := ctx.Value(newKeysKey{}).(*newKeys); ok && p.tenantID == tenantID {
		return p.keys, nil
	}
	s.keys.mu.Lock()
	tk, ok := s.keys.tenants[tenantID]
	s.keys.mu.Unlock()
	if ok && s.now().Sub(tk.loaded) < keyCacheTTL {
		return tk, nil
	}
	if err := s.ensureKeys(ctx, tenantID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT purpose, version, wrapped, retired_at IS NULL FROM tenant_keys WHERE tenant_id = $1`, tenantID)
	if err != nil {
		return nil, err
	}
	type row struct {
		purpose string
		version int
		wrapped []byte
		current bool
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		err := r.Scan(&x.purpose, &x.version, &x.wrapped, &x.current)
		return x, err
	})
	if err != nil {
		return nil, err
	}
	tk = &tenantKeys{data: map[int]cipher.AEAD{}, loaded: s.now()}
	for _, x := range list {
		key, err := s.kek.Unwrap(ctx, x.wrapped, keyAAD(tenantID, x.purpose, x.version))
		if err != nil {
			return nil, fmt.Errorf("%w: unwrap %s key %d of tenant %d: %v", ErrKey, x.purpose, x.version, tenantID, err)
		}
		switch x.purpose {
		case purposeData:
			aead, err := newAEAD(key)
			if err != nil {
				return nil, err
			}
			tk.data[x.version] = aead
			if x.current {
				tk.current = x.version
			}
		case purposeSearch:
			if x.current {
				tk.search = key
			}
		}
	}
	if tk.current == 0 || tk.search == nil {
		return nil, fmt.Errorf("%w: tenant %d has no current key", ErrKey, tenantID)
	}
	s.keys.mu.Lock()
	s.keys.tenants[tenantID] = tk
	s.keys.mu.Unlock()
	return tk, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// ensureKeys makes the first data key and the search key of a tenant if
// they do not exist.
func (s *Store) ensureKeys(ctx context.Context, tenantID int64) error {
	for _, purpose := range []string{purposeData, purposeSearch} {
		var exists bool
		if err := s.pool.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM tenant_keys WHERE tenant_id = $1 AND purpose = $2 AND retired_at IS NULL)`,
			tenantID, purpose).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		if err := s.insertKey(ctx, s.pool, tenantID, purpose, 1); err != nil {
			return err
		}
	}
	return nil
}

// insertKey makes a random key and saves it wrapped. If the key exists
// already (a different request made it), the function does nothing.
func (s *Store) insertKey(ctx context.Context, q querier, tenantID int64, purpose string, version int) error {
	_, err := s.insertNewKey(ctx, q, tenantID, purpose, version)
	return err
}

// insertNewKey is insertKey. It also returns the new key.
func (s *Store) insertNewKey(ctx context.Context, q querier, tenantID int64, purpose string, version int) ([]byte, error) {
	key := make([]byte, secret.KeySize)
	_, _ = rand.Read(key) // crypto/rand.Read does not return errors.
	wrapped, err := s.kek.Wrap(ctx, key, keyAAD(tenantID, purpose, version))
	if err != nil {
		return nil, err
	}
	_, err = q.Exec(ctx, `
		INSERT INTO tenant_keys (tenant_id, purpose, version, wrapped, kek_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT DO NOTHING`, tenantID, purpose, version, wrapped, s.kek.ID(), s.now())
	if sqlState(err) == codeForeignKeyViolation {
		return nil, ErrNotFound
	}
	return key, err
}

// newKeys are the keys of a tenant that the transaction of the context
// makes. Other connections cannot see the tenant before the commit.
type newKeys struct {
	tenantID int64
	keys     *tenantKeys
}

type newKeysKey struct{}

// withNewKeys makes the keys of a new tenant in the transaction tx. The
// store uses the keys of the returned context for the tenant in the
// transaction. The keys do not go into the keyring: if the transaction
// fails, the keys do not exist.
func (s *Store) withNewKeys(ctx context.Context, tx pgx.Tx, tenantID int64) (context.Context, error) {
	dk, err := s.insertNewKey(ctx, tx, tenantID, purposeData, 1)
	if err != nil {
		return nil, err
	}
	sk, err := s.insertNewKey(ctx, tx, tenantID, purposeSearch, 1)
	if err != nil {
		return nil, err
	}
	aead, err := newAEAD(dk)
	if err != nil {
		return nil, err
	}
	tk := &tenantKeys{data: map[int]cipher.AEAD{1: aead}, current: 1, search: sk, loaded: s.now()}
	return context.WithValue(ctx, newKeysKey{}, &newKeys{tenantID: tenantID, keys: tk}), nil
}

// The format of an encrypted value: one byte for the format, the key
// version (4 bytes, big-endian), the nonce and the sealed data with the
// tag of GCM.
const cipherFormat = 1

// valueAAD binds an encrypted value to its tenant and its place, for
// example "nodes.body:<space>:<public ID>". Thus a person who can write
// to the database cannot move a value to a different row or tenant.
func valueAAD(tenantID int64, place string) []byte {
	return []byte("skriva:" + strconv.FormatInt(tenantID, 10) + ":" + place)
}

// seal encrypts plain with the current data key of the tenant. It
// returns the ciphertext and the key version.
func (s *Store) seal(ctx context.Context, tenantID int64, place string, plain []byte) ([]byte, int, error) {
	tk, err := s.tenantKeys(ctx, tenantID)
	if err != nil {
		return nil, 0, err
	}
	return sealWith(tk.data[tk.current], tk.current, tenantID, place, plain), tk.current, nil
}

func sealWith(aead cipher.AEAD, version int, tenantID int64, place string, plain []byte) []byte {
	head := 1 + 4 + aead.NonceSize()
	out := make([]byte, head, head+len(plain)+aead.Overhead())
	out[0] = cipherFormat
	binary.BigEndian.PutUint32(out[1:5], uint32(version)) // #nosec G115 -- versions are small and positive.
	_, _ = rand.Read(out[5:head])
	return aead.Seal(out, out[5:head], plain, valueAAD(tenantID, place))
}

// open decrypts a value from seal.
func (s *Store) open(ctx context.Context, tenantID int64, place string, data []byte) ([]byte, error) {
	if len(data) < 5 || data[0] != cipherFormat {
		return nil, fmt.Errorf("%w: unknown format", ErrKey)
	}
	version := int(binary.BigEndian.Uint32(data[1:5]))
	tk, err := s.tenantKeys(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	aead, ok := tk.data[version]
	if !ok {
		// A different server can have made a new key version.
		s.forgetKeys(tenantID)
		if tk, err = s.tenantKeys(ctx, tenantID); err != nil {
			return nil, err
		}
		if aead, ok = tk.data[version]; !ok {
			return nil, fmt.Errorf("%w: tenant %d has no data key %d", ErrKey, tenantID, version)
		}
	}
	head := 5 + aead.NonceSize()
	if len(data) < head+aead.Overhead() {
		return nil, fmt.Errorf("%w: value too short", ErrKey)
	}
	plain, err := aead.Open(nil, data[5:head], data[head:], valueAAD(tenantID, place))
	if err != nil {
		return nil, fmt.Errorf("%w: cannot decrypt %s", ErrKey, place)
	}
	return plain, nil
}

func (s *Store) forgetKeys(tenantID int64) {
	s.keys.mu.Lock()
	delete(s.keys.tenants, tenantID)
	s.keys.mu.Unlock()
}

// searchToken returns the keyed hash of a word for the search index. The
// first 8 bytes of HMAC-SHA-256 are enough to make collisions rare.
func searchToken(key []byte, word string) int64 {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("word:" + word))
	return int64(binary.BigEndian.Uint64(m.Sum(nil)[:8])) // #nosec G115 -- a hash value, any bit pattern is good.
}

// slugToken returns the keyed hash of the slug of a title, for wiki
// links, or nil for an empty slug. The prefix "slug:" makes the hash
// different from the hash of a word.
func slugToken(key []byte, slug string) *int64 {
	if slug == "" {
		return nil
	}
	m := hmac.New(sha256.New, key)
	m.Write([]byte("slug:" + slug))
	t := int64(binary.BigEndian.Uint64(m.Sum(nil)[:8])) // #nosec G115 -- a hash value, any bit pattern is good.
	return &t
}

// EnsureAllKeys makes the keys of all tenants that have no keys. The
// application calls it at start. Thus a problem with the KEK stops the
// server at start, and not at the first save.
func (s *Store) EnsureAllKeys(ctx context.Context) error {
	tenants, err := s.Tenants(ctx)
	if err != nil {
		return err
	}
	for _, t := range tenants {
		if _, err := s.tenantKeys(ctx, t.ID); err != nil {
			return err
		}
	}
	return nil
}

// KeyInfo describes the keys of a tenant for the administration.
type KeyInfo struct {
	// Version and CreatedAt are the current data key.
	Version   int
	CreatedAt time.Time
	KEKID     string
	// Retired is the number of retired data keys that the store did not
	// delete yet, because data uses them.
	Retired int
	// OldItems is the number of encrypted values that use a retired key.
	OldItems int
}

// TenantKeyInfo returns the state of the data keys of a tenant.
func (s *Store) TenantKeyInfo(ctx context.Context, tenantID int64) (*KeyInfo, error) {
	if _, err := s.tenantKeys(ctx, tenantID); err != nil {
		return nil, err
	}
	var k KeyInfo
	err := s.pool.QueryRow(ctx, `
		SELECT k.version, k.created_at, k.kek_id,
		       (SELECT count(*) FROM tenant_keys r WHERE r.tenant_id = $1 AND r.purpose = 'data' AND r.retired_at IS NOT NULL),
		       (SELECT count(*) FROM nodes n JOIN spaces sp ON sp.id = n.space_id
		        WHERE sp.tenant_id = $1 AND n.key_version <> k.version)
		     + (SELECT count(*) FROM revisions r JOIN nodes n ON n.id = r.node_id JOIN spaces sp ON sp.id = n.space_id
		        WHERE sp.tenant_id = $1 AND r.key_version <> k.version)
		     + (SELECT count(*) FROM files f JOIN spaces sp ON sp.id = f.space_id
		        WHERE sp.tenant_id = $1 AND f.key_version <> k.version)
		     + (SELECT count(*) FROM audit_events a WHERE a.tenant_id = $1 AND a.key_version <> k.version)
		FROM tenant_keys k
		WHERE k.tenant_id = $1 AND k.purpose = 'data' AND k.retired_at IS NULL`, tenantID,
	).Scan(&k.Version, &k.CreatedAt, &k.KEKID, &k.Retired, &k.OldItems)
	if err != nil {
		return nil, notFound(err)
	}
	return &k, nil
}

// RotateDataKey makes a new data key for a tenant and retires the current
// key. New values use the new key at once. The cleanup task encrypts the
// old values again with the new key (ReencryptBatch), and then deletes
// the retired key.
func (s *Store) RotateDataKey(ctx context.Context, tenantID int64) (int, error) {
	if err := s.ensureKeys(ctx, tenantID); err != nil {
		return 0, err
	}
	var version int
	err := s.tx(ctx, func(tx pgx.Tx) error {
		var current int
		var code string
		if err := tx.QueryRow(ctx, `
			SELECT k.version, t.code FROM tenant_keys k JOIN tenants t ON t.id = k.tenant_id
			WHERE k.tenant_id = $1 AND k.purpose = 'data' AND k.retired_at IS NULL FOR UPDATE`,
			tenantID).Scan(&current, &code); err != nil {
			return notFound(err)
		}
		var maxVersion int
		if err := tx.QueryRow(ctx, `
			SELECT max(version) FROM tenant_keys WHERE tenant_id = $1 AND purpose = 'data'`,
			tenantID).Scan(&maxVersion); err != nil {
			return err
		}
		version = maxVersion + 1
		if _, err := tx.Exec(ctx, `
			UPDATE tenant_keys SET retired_at = $3 WHERE tenant_id = $1 AND purpose = 'data' AND version = $2`,
			tenantID, current, s.now()); err != nil {
			return err
		}
		if err := s.insertKey(ctx, tx, tenantID, purposeData, version); err != nil {
			return err
		}
		return s.audit(ctx, tx, auditEntry{Action: AuditTenantKeyRotate, TargetID: code,
			Details: fmt.Sprintf("Data key version %d replaces version %d.", version, current)})
	})
	if err != nil {
		return 0, err
	}
	s.forgetKeys(tenantID)
	return version, nil
}

// reencryptBatchSize is the number of values that one step of the
// re-encryption changes.
const reencryptBatchSize = 100

// ReencryptBatch encrypts up to reencryptBatchSize values that use a
// retired data key again with the current key. It returns the number of
// changed values. Then it deletes the retired keys that no value uses.
func (s *Store) ReencryptBatch(ctx context.Context) (int, error) {
	// Each query finds the rows that do not use the current key of their
	// tenant. It returns the tenant, the key of the row (id1, id2), the
	// place for the AAD (with %s for the column) and the values.
	queries := []struct {
		sql, update string
		cols        []string
	}{
		{`SELECT sp.tenant_id, n.id, 0, 'nodes.%s:' || n.space_id || ':' || n.public_id, n.body, n.plain_text, n.title
		  FROM nodes n JOIN spaces sp ON sp.id = n.space_id
		  JOIN tenant_keys k ON k.tenant_id = sp.tenant_id AND k.purpose = 'data' AND k.retired_at IS NULL
		  WHERE n.key_version <> k.version LIMIT $1`,
			"UPDATE nodes SET body = $1, plain_text = $2, title = $3, key_version = $4 WHERE id = $5 AND $6::int = 0 AND body = $7",
			[]string{"body", "plain_text", "title"}},
		{`SELECT sp.tenant_id, r.node_id, r.version, 'revisions.%s:' || n.space_id || ':' || n.public_id || ':' || r.version, r.body, r.title
		  FROM revisions r JOIN nodes n ON n.id = r.node_id JOIN spaces sp ON sp.id = n.space_id
		  JOIN tenant_keys k ON k.tenant_id = sp.tenant_id AND k.purpose = 'data' AND k.retired_at IS NULL
		  WHERE r.key_version <> k.version LIMIT $1`,
			"UPDATE revisions SET body = $1, title = $2, key_version = $3 WHERE node_id = $4 AND version = $5 AND body = $6",
			[]string{"body", "title"}},
		{`SELECT sp.tenant_id, f.id, 0, 'files.%s:' || f.space_id || ':' || f.public_id, f.data
		  FROM files f JOIN spaces sp ON sp.id = f.space_id
		  JOIN tenant_keys k ON k.tenant_id = sp.tenant_id AND k.purpose = 'data' AND k.retired_at IS NULL
		  WHERE f.key_version <> k.version LIMIT $1`,
			"UPDATE files SET data = $1, key_version = $2 WHERE id = $3 AND $4::int = 0 AND data = $5",
			[]string{"data"}},
		{`SELECT a.tenant_id, a.id, 0, 'audit_events.%s:' || a.id, a.sealed
		  FROM audit_events a
		  JOIN tenant_keys k ON k.tenant_id = a.tenant_id AND k.purpose = 'data' AND k.retired_at IS NULL
		  WHERE a.key_version <> k.version LIMIT $1`,
			"UPDATE audit_events SET sealed = $1, key_version = $2 WHERE id = $3 AND $4::int = 0 AND sealed = $5",
			[]string{"sealed"}},
	}
	changed := 0
	for _, q := range queries {
		if changed >= reencryptBatchSize {
			break
		}
		type item struct {
			tenant, id1 int64
			id2         int
			place       string
			values      [][]byte
		}
		rows, err := s.pool.Query(ctx, q.sql, reencryptBatchSize-changed)
		if err != nil {
			return changed, err
		}
		var items []item
		for rows.Next() {
			it := item{values: make([][]byte, len(q.cols))}
			dest := []any{&it.tenant, &it.id1, &it.id2, &it.place}
			for i := range it.values {
				dest = append(dest, &it.values[i])
			}
			if err := rows.Scan(dest...); err != nil {
				rows.Close()
				return changed, err
			}
			items = append(items, it)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return changed, err
		}
		for _, it := range items {
			args := make([]any, 0, len(q.cols)+3)
			var version int
			for i, col := range q.cols {
				place := fmt.Sprintf(it.place, col)
				plain, err := s.open(ctx, it.tenant, place, it.values[i])
				if err != nil {
					return changed, err
				}
				ct, v, err := s.seal(ctx, it.tenant, place, plain)
				if err != nil {
					return changed, err
				}
				args = append(args, ct)
				version = v
			}
			// The update applies only if the value did not change after
			// the query: a user can save the document at the same time.
			args = append(args, version, it.id1, it.id2, it.values[0])
			if _, err := s.pool.Exec(ctx, q.update, args...); err != nil {
				return changed, err
			}
			changed++
		}
	}
	return changed, s.deleteUnusedKeys(ctx)
}

// deleteUnusedKeys deletes the retired data keys that no value uses. A
// key must be retired for retiredKeyAge. Thus no server still uses it as
// the current key.
func (s *Store) deleteUnusedKeys(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM tenant_keys k
		WHERE k.purpose = 'data' AND k.retired_at < $1
		  AND NOT EXISTS (SELECT 1 FROM nodes n JOIN spaces sp ON sp.id = n.space_id
		                  WHERE sp.tenant_id = k.tenant_id AND n.key_version = k.version)
		  AND NOT EXISTS (SELECT 1 FROM revisions r JOIN nodes n ON n.id = r.node_id JOIN spaces sp ON sp.id = n.space_id
		                  WHERE sp.tenant_id = k.tenant_id AND r.key_version = k.version)
		  AND NOT EXISTS (SELECT 1 FROM files f JOIN spaces sp ON sp.id = f.space_id
		                  WHERE sp.tenant_id = k.tenant_id AND f.key_version = k.version)
		  AND NOT EXISTS (SELECT 1 FROM audit_events a
		                  WHERE a.tenant_id = k.tenant_id AND a.key_version = k.version)`,
		s.now().Add(-retiredKeyAge))
	return err
}

// RewrapKeys wraps all keys that use the KEK previous again with the
// current KEK. It also encrypts the client secrets of the tenants again.
// The application calls it at start when the configuration has a
// previous key. Then the previous key can be destroyed.
func (s *Store) RewrapKeys(ctx context.Context, previous *secret.Box) (int, error) {
	old := localKEK{previous}
	n := 0
	err := s.tx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT tenant_id, purpose, version, wrapped FROM tenant_keys WHERE kek_id = $1 FOR UPDATE`, old.ID())
		if err != nil {
			return err
		}
		type row struct {
			tenant  int64
			purpose string
			version int
			wrapped []byte
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
			var x row
			err := r.Scan(&x.tenant, &x.purpose, &x.version, &x.wrapped)
			return x, err
		})
		if err != nil {
			return err
		}
		for _, x := range list {
			aad := keyAAD(x.tenant, x.purpose, x.version)
			key, err := old.Unwrap(ctx, x.wrapped, aad)
			if err != nil {
				return fmt.Errorf("%w: unwrap with the previous key: %v", ErrKey, err)
			}
			wrapped, err := s.kek.Wrap(ctx, key, aad)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				UPDATE tenant_keys SET wrapped = $4, kek_id = $5
				WHERE tenant_id = $1 AND purpose = $2 AND version = $3`,
				x.tenant, x.purpose, x.version, wrapped, s.kek.ID()); err != nil {
				return err
			}
			n++
		}
		return s.rewrapClientSecrets(ctx, tx, previous)
	})
	return n, err
}
