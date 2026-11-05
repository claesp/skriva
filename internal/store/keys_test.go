package store_test

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/claesp/skriva/internal/secret"
	"github.com/claesp/skriva/internal/store"
	"github.com/claesp/skriva/internal/testdb"
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

	// Search finds whole words, and words that start with a search word
	// of 3 letters or more.
	results, err := st2.Search(ctx, tn.ID, []int64{sp.ID}, "SWORDFISH launch", readAll)
	if err != nil || len(results) != 1 || fmt.Sprint(results[0].Snippet) != "[{The  false} {launch true} { code is  false} {swordfish. true}]" {
		t.Fatalf("search = %+v, %v", results, err)
	}
	if results, _ := st2.Search(ctx, tn.ID, []int64{sp.ID}, "sword", readAll); len(results) != 1 {
		t.Fatalf("start of a word: %+v", results)
	}
	if results, _ := st2.Search(ctx, tn.ID, []int64{sp.ID}, "fish", readAll); len(results) != 0 {
		t.Fatalf("end of a word: %+v", results)
	}
	if results, _ := st2.Search(ctx, tn.ID, []int64{sp.ID}, "plan -swordfish", readAll); len(results) != 0 {
		t.Fatalf("excluded word: %+v", results)
	}
	if results, _ := st2.Search(ctx, tn.ID, []int64{sp.ID}, "plan", readAll); len(results) != 1 {
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
	// Two tenants with a data key and a search key each. A new tenant
	// gets its keys when it is made.
	n, err := st2.RewrapKeys(ctx, oldBox)
	if err != nil || n != 4 {
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

func TestTitleEncryption(t *testing.T) {
	pool := testdb.New(t)
	st := store.New(pool, testdb.Box(t))
	tn := defaultTenant(t, st)
	sp := defaultSpace(t, st, tn)
	mk := func(parent, kind, title string, c store.Content) string {
		t.Helper()
		id, err := st.CreateNode(ctx, sp.ID, store.NewNode{ParentPublicID: parent, Kind: kind, Title: title, Content: c})
		if err != nil {
			t.Fatalf("create %s: %v", title, err)
		}
		return id
	}
	folder := mk("", store.KindFolder, "Zeta", store.Content{})
	secretID := mk(folder, store.KindDocument, "Project Swordfish", store.Content{})
	mk("", store.KindDocument, "banana", store.Content{})
	mk("", store.KindDocument, "Ärende", store.Content{})
	linker := mk("", store.KindDocument, "apple", store.Content{
		Body: `{"type":"doc"}`, PlainText: "See swordfish.", WikiLinks: []string{"project-swordfish"}})
	if err := st.MoveNode(ctx, sp.ID, secretID, ""); err != nil {
		t.Fatal(err)
	}

	// The database has no title in plain text.
	var leaks int
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM nodes WHERE position('Swordfish'::bytea in title) > 0)
		     + (SELECT count(*) FROM revisions WHERE position('Swordfish'::bytea in title) > 0)
		     + (SELECT count(*) FROM audit_events WHERE target LIKE '%Swordfish%' OR details LIKE '%Zeta%'
		          OR position('Swordfish'::bytea in sealed) > 0)`).Scan(&leaks); err != nil {
		t.Fatal(err)
	}
	if leaks != 0 {
		t.Fatalf("%d values with a plain title", leaks)
	}

	// The tree sorts the folders first, then the titles. The case and
	// the diacritics come after the base letters.
	nodes, err := st.SpaceNodes(ctx, sp.ID)
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, n := range nodes {
		titles = append(titles, n.Title)
	}
	if want := []string{"Zeta", "apple", "Ärende", "banana", "Project Swordfish"}; !slices.Equal(titles, want) {
		t.Fatalf("tree = %q, want %q", titles, want)
	}

	// Wiki links, backlinks and the link picker use the decrypted titles.
	bySlug, err := st.NodesBySlug(ctx, sp.ID, "project-swordfish")
	if err != nil || len(bySlug) != 1 || bySlug[0].PublicID != secretID || bySlug[0].Slug != "project-swordfish" {
		t.Fatalf("NodesBySlug = %+v, %v", bySlug, err)
	}
	n, err := st.Node(ctx, sp.ID, secretID)
	if err != nil || n.Title != "Project Swordfish" {
		t.Fatalf("Node = %+v, %v", n, err)
	}
	back, err := st.Backlinks(ctx, n)
	if err != nil || len(back) != 1 || back[0].PublicID != linker {
		t.Fatalf("Backlinks = %+v, %v", back, err)
	}
	targets, err := st.FindLinkTargets(ctx, sp.ID, "SWORD", 10)
	if err != nil || len(targets) != 1 || targets[0].PublicID != secretID {
		t.Fatalf("FindLinkTargets = %+v, %v", targets, err)
	}
	targets, _ = st.FindLinkTargets(ctx, sp.ID, "a", 10)
	if len(targets) != 3 || targets[0].Title != "apple" || targets[1].Title != "banana" {
		t.Fatalf("FindLinkTargets(a) = %+v", targets)
	}

	// The search shows the result with the word in the title first.
	results, err := st.Search(ctx, tn.ID, []int64{sp.ID}, "swordfish", readAll)
	if err != nil || len(results) != 2 || results[0].PublicID != secretID {
		t.Fatalf("Search = %+v, %v", results, err)
	}

	// The audit log shows the decrypted titles.
	events, err := st.AuditEvents(ctx, &tn.ID, 0, 10)
	if err != nil || len(events) == 0 || events[0].Target != "Project Swordfish" ||
		events[0].Details != `To the top level.` {
		t.Fatalf("AuditEvents = %+v, %v", events, err)
	}

	// A new data key encrypts the titles and the audit log again.
	if _, err := st.RotateDataKey(ctx, tn.ID); err != nil {
		t.Fatal(err)
	}
	for {
		n, err := st.ReencryptBatch(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
	}
	info, err := st.TenantKeyInfo(ctx, tn.ID)
	if err != nil || info.OldItems != 0 {
		t.Fatalf("TenantKeyInfo = %+v, %v", info, err)
	}
	if events, err := st.AuditEvents(ctx, &tn.ID, 0, 10); err != nil || events[0].Target != "Project Swordfish" {
		t.Fatalf("AuditEvents after rotation = %+v, %v", events, err)
	}
	if revs, err := st.Revisions(ctx, n.ID); err != nil || revs[0].Title != "Project Swordfish" {
		t.Fatalf("Revisions = %+v, %v", revs, err)
	}

	// A title must have 1 to 200 characters.
	if _, err := st.CreateNode(ctx, sp.ID, store.NewNode{Kind: store.KindDocument, Title: ""}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("empty title: %v", err)
	}
}
