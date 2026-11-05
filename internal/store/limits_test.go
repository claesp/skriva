package store_test

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/claesp/skriva/internal/store"
)

// readAll is a permission check for Search that permits all nodes.
func readAll(int64, int64) (bool, error) { return true, nil }

func TestUploadQuota(t *testing.T) {
	st := newStore(t)
	tn := defaultTenant(t, st)
	sp := defaultSpace(t, st, tn)
	u := login(t, st, tn, "quinn")
	if sp.UploadQuotaMB != store.DefaultUploadQuotaMB || sp.SearchResults != store.DefaultSearchResults {
		t.Fatalf("default limits = %d MiB, %d results", sp.UploadQuotaMB, sp.SearchResults)
	}
	set := store.SpaceSettings{Slug: sp.Slug, Name: sp.Name, UploadQuotaMB: 1, SearchResults: 10}
	if err := st.UpdateSpace(ctx, tn.ID, sp.Slug, set); err != nil {
		t.Fatal(err)
	}
	part := make([]byte, 600<<10)
	if _, err := st.CreateFile(ctx, sp.ID, "image/png", part, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateFile(ctx, sp.ID, "image/png", part, u.ID); !errors.Is(err, store.ErrQuota) {
		t.Fatalf("upload over the quota = %v, want ErrQuota", err)
	}
	// A file that fits in the rest of the quota is permitted.
	if _, err := st.CreateFile(ctx, sp.ID, "image/png", make([]byte, 1<<20-600<<10), u.ID); err != nil {
		t.Fatalf("upload to the limit = %v", err)
	}
	if n, err := st.SpaceFileBytes(ctx, sp.ID); err != nil || n != 1<<20 {
		t.Fatalf("SpaceFileBytes = %d, %v", n, err)
	}

	for _, bad := range []store.SpaceSettings{
		{Slug: sp.Slug, Name: sp.Name, UploadQuotaMB: 0, SearchResults: 10},
		{Slug: sp.Slug, Name: sp.Name, UploadQuotaMB: store.MaxUploadQuotaMB + 1, SearchResults: 10},
		{Slug: sp.Slug, Name: sp.Name, UploadQuotaMB: 1, SearchResults: 9},
		{Slug: sp.Slug, Name: sp.Name, UploadQuotaMB: 1, SearchResults: store.MaxSearchResults + 1},
	} {
		if err := st.UpdateSpace(ctx, tn.ID, sp.Slug, bad); !errors.Is(err, store.ErrInvalid) {
			t.Errorf("UpdateSpace(%+v) = %v, want ErrInvalid", bad, err)
		}
	}
	got, _ := st.Space(ctx, tn.ID, sp.Slug)
	if got.UploadQuotaMB != 1 || got.SearchResults != 10 {
		t.Fatalf("limits = %d MiB, %d results", got.UploadQuotaMB, got.SearchResults)
	}
}

// The permission check comes before the limit of the space. Thus many
// matches that the user cannot read do not hide the matches that the
// user can read.
func TestSearchPermissionFirst(t *testing.T) {
	st := newStore(t)
	tn := defaultTenant(t, st)
	sp := defaultSpace(t, st, tn)
	u := login(t, st, tn, "sam")
	newNode := func(parent, kind, title string) string {
		t.Helper()
		id, err := st.CreateNode(ctx, sp.ID, store.NewNode{ParentPublicID: parent, Kind: kind, Title: title,
			Content: content("walrus"), AuthorID: u.ID})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	private := newNode("", store.KindFolder, "Private")
	none := ""
	if err := st.SetNodeDefaultRole(ctx, tn.ID, sp.ID, private, &none); err != nil {
		t.Fatal(err)
	}
	// The private documents are newer, thus they come first in the order.
	for i := range 3 {
		newNode("", store.KindDocument, fmt.Sprintf("Open %d", i))
	}
	for i := range 30 {
		newNode(private, store.KindDocument, fmt.Sprintf("Hidden %d", i))
	}
	folder, _ := st.Node(ctx, sp.ID, private)
	can := func(_, accessPathID int64) (bool, error) { return accessPathID != folder.AccessPathID, nil }

	res, err := st.Search(ctx, tn.ID, []int64{sp.ID}, "walrus", can)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 3 {
		t.Fatalf("got %d results, want the 3 open documents", len(res))
	}
	for _, r := range res {
		if r.AccessPathID == folder.AccessPathID || len(r.Snippet) == 0 {
			t.Fatalf("result %q: access path %d, snippet %v", r.Title, r.AccessPathID, r.Snippet)
		}
	}

	// The limit of the space applies to the readable results.
	set := store.SpaceSettings{Slug: sp.Slug, Name: sp.Name, UploadQuotaMB: 1, SearchResults: 10}
	if err := st.UpdateSpace(ctx, tn.ID, sp.Slug, set); err != nil {
		t.Fatal(err)
	}
	if res, _ := st.Search(ctx, tn.ID, []int64{sp.ID}, "walrus", readAll); len(res) != 10 {
		t.Fatalf("got %d results, want the limit 10", len(res))
	}
	if res, _ := st.Search(ctx, tn.ID, []int64{sp.ID}, "walrus", can); len(res) != 3 {
		t.Fatalf("got %d results with the limit, want 3", len(res))
	}
	if _, err := st.Search(ctx, tn.ID, []int64{sp.ID}, "walrus", func(int64, int64) (bool, error) {
		return false, errors.New("broken")
	}); err == nil {
		t.Fatal("Search ignores the error of the permission check")
	}
}

// A search word also finds the words that start with it. These results
// come after the results with whole words.
func TestSearchPartialWords(t *testing.T) {
	st := newStore(t)
	tn := defaultTenant(t, st)
	sp := defaultSpace(t, st, tn)
	u := login(t, st, tn, "pat")
	mk := func(title, text string) string {
		t.Helper()
		id, err := st.CreateNode(ctx, sp.ID, store.NewNode{Kind: store.KindDocument, Title: title,
			Content: content(text), AuthorID: u.ID})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	// The newest document comes first in each group. Thus the order of
	// the results shows the groups.
	inTitle := mk("Describe the plan", "x")
	inText := mk("Plan", "We describe it.")
	partial := mk("Notes", "It describes the design.")
	mk("Other", "A description of nothing.")
	titles := func(q string) []string {
		t.Helper()
		res, err := st.Search(ctx, tn.ID, []int64{sp.ID}, q, readAll)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, r := range res {
			out = append(out, r.PublicID)
		}
		return out
	}
	if got := titles("describe"); !slices.Equal(got, []string{inTitle, inText, partial}) {
		t.Fatalf("describe = %v, want %v", got, []string{inTitle, inText, partial})
	}
	if got := titles("describ plan"); len(got) != 2 {
		t.Fatalf("describ plan = %v", got)
	}
	// A short word finds only the whole word.
	if got := titles("it"); !slices.Equal(got, []string{partial, inText}) {
		t.Fatalf("it = %v", got)
	}
	// An excluded word is a whole word.
	if got := titles("describe -describes"); !slices.Equal(got, []string{inTitle, inText}) {
		t.Fatalf("describe -describes = %v", got)
	}

	// ReindexBatch makes an index of an old format again.
	if err := st.ResetSearchIndex(ctx, sp.ID); err != nil {
		t.Fatal(err)
	}
	if got := titles("describ"); len(got) != 0 {
		t.Fatalf("describ with the old index = %v", got)
	}
	n, err := st.ReindexBatch(ctx)
	if err != nil || n != 4 {
		t.Fatalf("ReindexBatch = %d, %v", n, err)
	}
	if n, err := st.ReindexBatch(ctx); err != nil || n != 0 {
		t.Fatalf("second ReindexBatch = %d, %v", n, err)
	}
	if got := titles("describ"); len(got) != 3 {
		t.Fatalf("describ after ReindexBatch = %v", got)
	}
}
