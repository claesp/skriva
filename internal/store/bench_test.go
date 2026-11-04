package store_test

import (
	"testing"

	"github.com/claesp/skriva/internal/rbac"
	"github.com/claesp/skriva/internal/store"
	"github.com/claesp/skriva/internal/testdb"
)

// BenchmarkAccessLargeSpace measures the permission data for a space with
// 50000 nodes and 100 nodes with own settings. Run it with:
//
//	scripts/test-integration.sh -run '^$' -bench AccessLargeSpace ./internal/store/
func BenchmarkAccessLargeSpace(b *testing.B) {
	pool := testdb.New(b)
	st := store.New(pool, testdb.Box(b))
	tn, _ := st.DefaultTenant(ctx)
	sp, _ := st.Space(ctx, tn.ID, "default0")
	u, err := st.SyncLogin(ctx, &tn.ID, store.Identity{Issuer: "i", Subject: "s", Username: "u"})
	if err != nil {
		b.Fatal(err)
	}
	// 100 folders at the top level, each with 499 documents.
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_paths (space_id, path) VALUES ($1, '{}') ON CONFLICT DO NOTHING;`, sp.ID); err != nil {
		b.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		WITH root AS (SELECT id FROM access_paths WHERE space_id = $1 AND path = '{}'),
		folders AS (
			INSERT INTO nodes (public_id, space_id, kind, title, body, plain_text, key_version, access_path_id)
			SELECT 'f' || lpad(g::text, 13, '0'), $1, 'folder', '', '', '', 1, (SELECT id FROM root)
			FROM generate_series(1, 100) g RETURNING id, public_id)
		INSERT INTO nodes (public_id, space_id, parent_id, kind, title, body, plain_text, key_version,
		                   access_path_id, classification_id)
		SELECT 'd' || lpad((f.id * 1000 + g)::text, 13, '0'), $1, f.id, 'document', '', '', '', 1,
		       (SELECT id FROM root), (SELECT id FROM classifications WHERE tenant_id = $2 AND is_default)
		FROM folders f, generate_series(1, 499) g`, sp.ID, tn.ID); err != nil {
		b.Fatal(err)
	}
	// Each folder gets its own default role. Then the cache has 101 paths.
	if _, err := pool.Exec(ctx, `
		UPDATE nodes SET default_role = 'editor' WHERE space_id = $1 AND kind = 'folder'`, sp.ID); err != nil {
		b.Fatal(err)
	}
	if err := st.RebuildAccessPaths(ctx, sp.ID); err != nil {
		b.Fatal(err)
	}
	// The nodes have no encrypted content. The benchmark needs only the
	// access path of a document.
	var pathID int64
	if err := pool.QueryRow(ctx, `SELECT access_path_id FROM nodes
		WHERE space_id = $1 AND kind = 'document' LIMIT 1`, sp.ID).Scan(&pathID); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for b.Loop() {
		a, err := st.Access(ctx, tn.ID, sp.ID, u.ID)
		if err != nil {
			b.Fatal(err)
		}
		if !a.Can(pathID, rbac.DocEdit) {
			b.Fatal("the folder default role does not apply")
		}
	}
}
