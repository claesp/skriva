package store_test

import (
	"testing"

	"github.com/clpe/skriva/internal/rbac"
	"github.com/clpe/skriva/internal/store"
	"github.com/clpe/skriva/internal/testdb"
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
			INSERT INTO nodes (public_id, space_id, kind, title, slug, body, access_path_id)
			SELECT 'F' || lpad(g::text, 11, '0'), $1, 'folder', 'Folder ' || g, 'folder-' || g, '', (SELECT id FROM root)
			FROM generate_series(1, 100) g RETURNING id, public_id)
		INSERT INTO nodes (public_id, space_id, parent_id, kind, title, slug, body, access_path_id)
		SELECT 'D' || lpad((f.id * 1000 + g)::text, 11, '0'), $1, f.id, 'document', 'Doc ' || g, 'doc-' || g, 'text',
		       (SELECT id FROM root)
		FROM folders f, generate_series(1, 499) g`, sp.ID); err != nil {
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
	var docID string
	if err := pool.QueryRow(ctx, `SELECT public_id FROM nodes
		WHERE space_id = $1 AND kind = 'document' LIMIT 1`, sp.ID).Scan(&docID); err != nil {
		b.Fatal(err)
	}
	n, err := st.Node(ctx, sp.ID, docID)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for b.Loop() {
		a, err := st.Access(ctx, tn.ID, sp.ID, u.ID)
		if err != nil {
			b.Fatal(err)
		}
		if !a.Can(n.AccessPathID, rbac.DocEdit) {
			b.Fatal("the folder default role does not apply")
		}
	}
}
