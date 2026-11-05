package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// RebuildAccessPaths calculates all access paths of a space again. The
// tests use it to fill the cache after a direct change in the database.
func (s *Store) RebuildAccessPaths(ctx context.Context, spaceID int64) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockSpace(ctx, tx, spaceID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			SELECT refresh_access_paths(id) FROM nodes WHERE space_id = $1 AND parent_id IS NULL`, spaceID); err != nil {
			return err
		}
		return deleteUnusedPaths(ctx, tx, spaceID)
	})
}

// AccessCacheReport returns the cached access path of each node in a
// space, and the path that the function calculates from the tree. The two
// maps must be equal. The tests use this function to check the cache.
func (s *Store) AccessCacheReport(ctx context.Context, spaceID int64) (cached, calculated map[int64][]int64, err error) {
	type row struct {
		parent   *int64
		settings bool
		path     []int64
	}
	rows, err := s.pool.Query(ctx, `
		SELECT n.id, n.parent_id,
		       n.default_role IS NOT NULL OR EXISTS (SELECT 1 FROM role_bindings rb WHERE rb.node_id = n.id),
		       ap.path
		FROM nodes n JOIN access_paths ap ON ap.id = n.access_path_id
		WHERE n.space_id = $1`, spaceID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	all := map[int64]*row{}
	for rows.Next() {
		var id int64
		r := &row{}
		if err := rows.Scan(&id, &r.parent, &r.settings, &r.path); err != nil {
			return nil, nil, err
		}
		all[id] = r
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	cached = map[int64][]int64{}
	calculated = map[int64][]int64{}
	var calc func(id int64, depth int) []int64
	calc = func(id int64, depth int) []int64 {
		if p, ok := calculated[id]; ok {
			return p
		}
		r := all[id]
		var path []int64
		if r.parent != nil && depth < len(all) {
			path = append(path, calc(*r.parent, depth+1)...)
		}
		if r.settings {
			path = append(path, id)
		}
		if path == nil {
			path = []int64{}
		}
		calculated[id] = path
		return path
	}
	for id, r := range all {
		cached[id] = r.path
		calc(id, 0)
	}
	return cached, calculated, nil
}

// SetNow replaces the clock of the store.
func (s *Store) SetNow(now func() time.Time) { s.now = now }

// RawIDToken returns the id_token column of the session for token, as the
// database keeps it.
func (s *Store) RawIDToken(ctx context.Context, token string) ([]byte, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx, "SELECT id_token FROM sessions WHERE token_hash = $1", hashToken(token)).Scan(&raw)
	return raw, err
}

// ResetSearchIndex gives the nodes of a space an index of the old format,
// without prefixes. The tests use it for ReindexBatch.
func (s *Store) ResetSearchIndex(ctx context.Context, spaceID int64) error {
	_, err := s.pool.Exec(ctx, "UPDATE nodes SET prefix_tokens = '{}', stem_tokens = '{}', search_version = 1 WHERE space_id = $1", spaceID)
	return err
}
