package store_test

import (
	"bytes"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/clpe/skriva/internal/secret"
	"github.com/clpe/skriva/internal/store"
	"github.com/clpe/skriva/internal/testdb"
)

func newBox(t *testing.T) *secret.Box {
	t.Helper()
	key := make([]byte, secret.KeySize)
	_, _ = rand.Read(key)
	b, err := secret.New(key)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestEncryptionAtRest(t *testing.T) {
	pool := testdb.New(t)
	box := testdb.Box(t)
	st := store.New(pool, box)
	tn := defaultTenant(t, st)
	sp := defaultSpace(t, st, tn)

	const secretText = "The launch code is swordfish."
	body := `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"` + secretText + `"}]}]}`
	id, err := st.CreateNode(ctx, sp.ID, store.NewNode{Kind: store.KindDocument, Title: "Plan",
		Content: store.Content{Body: body, PlainText: secretText}})
	if err != nil {
		t.Fatal(err)
	}
	fileID, err := st.CreateFile(ctx, sp.ID, "image/png", []byte("PNG image data swordfish"), 0)
	if err != nil {
		t.Fatal(err)
	}

	// The database contains only ciphertext.
	var nodeBody, plainText, revBody, fileData []byte
	var tokens []int64
	if err := pool.QueryRow(ctx, `
		SELECT n.body, n.plain_text, n.search_tokens, r.body FROM nodes n JOIN revisions r ON r.node_id = n.id
		WHERE n.public_id = $1`, id).Scan(&nodeBody, &plainText, &tokens, &revBody); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT data FROM files WHERE public_id = $1", fileID).Scan(&fileData); err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string][]byte{"body": nodeBody, "plain text": plainText, "revision": revBody, "file": fileData} {
		if bytes.Contains(v, []byte("swordfish")) || len(v) == 0 || v[0] != 1 {
			t.Errorf("%s is not encrypted: %q", name, v)
		}
	}
	if len(tokens) == 0 {
		t.Fatal("no search index")
	}
	var wrapped []byte
	if err := pool.QueryRow(ctx, `SELECT wrapped FROM tenant_keys WHERE tenant_id = $1 AND purpose = 'data'`, tn.ID).Scan(&wrapped); err != nil {
		t.Fatal(err)
	}

	// The store decrypts the values. A new store (a new process) reads the
	// keys from the database.
	st2 := store.New(pool, box)
	n, err := st2.Node(ctx, sp.ID, id)
	if err != nil || n.Body != body {
		t.Fatalf("Node = %+v, %v", n, err)
	}
	if f, err := st2.File(ctx, sp.ID, fileID); err != nil || string(f.Data) != "PNG image data swordfish" {
		t.Fatalf("File = %v", err)
	}

	// Search finds whole words. It does not find parts of words.
	results, err := st2.Search(ctx, tn.ID, []int64{sp.ID}, "SWORDFISH launch", 10)
	if err != nil || len(results) != 1 || results[0].Snippet != "The «launch» code is «swordfish.»" {
		t.Fatalf("search = %+v, %v", results, err)
	}
	if results, _ := st2.Search(ctx, tn.ID, []int64{sp.ID}, "sword", 10); len(results) != 0 {
		t.Fatalf("part of a word: %+v", results)
	}
	if results, _ := st2.Search(ctx, tn.ID, []int64{sp.ID}, "plan -swordfish", 10); len(results) != 0 {
		t.Fatalf("excluded word: %+v", results)
	}
	if results, _ := st2.Search(ctx, tn.ID, []int64{sp.ID}, "plan", 10); len(results) != 1 {
		t.Fatalf("title word: %+v", results)
	}

	// A value moved to a different row cannot be decrypted: the
	// associated data binds it to its row.
	other, _ := st.CreateNode(ctx, sp.ID, store.NewNode{Kind: store.KindDocument, Title: "Other"})
	if _, err := pool.Exec(ctx, "UPDATE nodes SET body = $1 WHERE public_id = $2", nodeBody, other); err != nil {
		t.Fatal(err)
	}
	if _, err := st2.Node(ctx, sp.ID, other); !errors.Is(err, store.ErrKey) {
		t.Fatalf("moved value: %v", err)
	}

	// With a different KEK, the store cannot read the keys.
	if _, err := store.New(pool, newBox(t)).Node(ctx, sp.ID, id); !errors.Is(err, store.ErrKey) {
		t.Fatalf("wrong KEK: %v", err)
	}
}

func TestKeyRotation(t *testing.T) {
	pool := testdb.New(t)
	box := testdb.Box(t)
	st := store.New(pool, box)
	tn := defaultTenant(t, st)
	sp := defaultSpace(t, st, tn)
	id, err := st.CreateNode(ctx, sp.ID, store.NewNode{Kind: store.KindDocument, Title: "Plan",
		Content: store.Content{Body: `{"type":"doc"}`, PlainText: "one"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateNode(ctx, sp.ID, id, store.NodeUpdate{BaseVersion: 1, Title: "Plan",
		Content: store.Content{Body: `{"type":"doc","content":[]}`, PlainText: "two"}}); err != nil {
		t.Fatal(err)
	}
	fileID, _ := st.CreateFile(ctx, sp.ID, "image/png", []byte("png"), 0)

	v, err := st.RotateDataKey(ctx, tn.ID)
	if err != nil || v != 2 {
		t.Fatalf("rotate = %d, %v", v, err)
	}
	info, err := st.TenantKeyInfo(ctx, tn.ID)
	if err != nil || info.Version != 2 || info.Retired != 1 || info.OldItems != 4 {
		t.Fatalf("key info = %+v, %v", info, err)
	}
	// Old values are still readable. New values use the new key.
	if n, err := st.Node(ctx, sp.ID, id); err != nil || n.Version != 2 {
		t.Fatalf("Node after rotation: %v", err)
	}
	// The re-encryption changes all old values. The retired key stays
	// for retiredKeyAge.
	if changed, err := st.ReencryptBatch(ctx); err != nil || changed != 4 {
		t.Fatalf("re-encryption changed %d, %v", changed, err)
	}
	if info, _ := st.TenantKeyInfo(ctx, tn.ID); info.OldItems != 0 || info.Retired != 1 {
		t.Fatalf("key info after re-encryption = %+v", info)
	}
	st.SetNow(func() time.Time { return time.Now().Add(2 * time.Hour) })
	if changed, err := st.ReencryptBatch(ctx); err != nil || changed != 0 {
		t.Fatalf("second re-encryption changed %d, %v", changed, err)
	}
	if info, _ := st.TenantKeyInfo(ctx, tn.ID); info.Retired != 0 {
		t.Fatalf("retired key not deleted: %+v", info)
	}
	// A new store has only the new key and reads all values.
	st2 := store.New(pool, box)
	if rev, err := st2.Revision(ctx, mustNode(t, st2, sp.ID, id).ID, 1); err != nil || rev.Body != `{"type":"doc"}` {
		t.Fatalf("old revision: %v", err)
	}
	if _, err := st2.File(ctx, sp.ID, fileID); err != nil {
		t.Fatalf("file: %v", err)
	}
	events, _ := st.AuditEvents(ctx, nil, 0, 1)
	if len(events) != 1 || events[0].Action != store.AuditTenantKeyRotate {
		t.Fatalf("audit = %+v", events)
	}
}

func mustNode(t *testing.T, st *store.Store, spaceID int64, id string) *store.Node {
	t.Helper()
	n, err := st.Node(ctx, spaceID, id)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRewrapKeys(t *testing.T) {
	pool := testdb.New(t)
	oldBox, newBoxValue := newBox(t), newBox(t)
	st := store.New(pool, oldBox)
	tn := defaultTenant(t, st)
	sp := defaultSpace(t, st, tn)
	id, err := st.CreateNode(ctx, sp.ID, store.NewNode{Kind: store.KindDocument, Title: "Plan"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateTenant(ctx, "acme", settings("Acme"), "s3cret")
	if err != nil {
		t.Fatal(err)
	}

	// The new KEK cannot read the keys before the rewrap.
	st2 := store.New(pool, newBoxValue)
	if _, err := st2.Node(ctx, sp.ID, id); !errors.Is(err, store.ErrKey) {
		t.Fatalf("before rewrap: %v", err)
	}
	n, err := st2.RewrapKeys(ctx, oldBox)
	if err != nil || n != 2 {
		t.Fatalf("rewrap = %d, %v", n, err)
	}
	st3 := store.New(pool, newBoxValue)
	if _, err := st3.Node(ctx, sp.ID, id); err != nil {
		t.Fatalf("after rewrap: %v", err)
	}
	if s, err := st3.ClientSecret(ctx, other); err != nil || s != "s3cret" {
		t.Fatalf("client secret = %q, %v", s, err)
	}
	// A second rewrap does nothing.
	if n, err := st3.RewrapKeys(ctx, oldBox); err != nil || n != 0 {
		t.Fatalf("second rewrap = %d, %v", n, err)
	}
}
