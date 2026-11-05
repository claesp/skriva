package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/claesp/skriva/internal/store"
)

// The content language of the tenant selects the stemmer. A change makes
// the index again in the cleanup task.
func TestContentLanguage(t *testing.T) {
	st := newStore(t)
	tn := defaultTenant(t, st)
	sp := defaultSpace(t, st, tn)
	u := login(t, st, tn, "lin")
	if tn.ContentLanguage != "en" || tn.Language != "" {
		t.Fatalf("languages of the default tenant = %q, %q", tn.ContentLanguage, tn.Language)
	}
	id, err := st.CreateNode(ctx, sp.ID, store.NewNode{Kind: store.KindDocument, Title: "Lekar",
		Content: content("En flicka leker."), AuthorID: u.ID})
	if err != nil {
		t.Fatal(err)
	}
	// "flickorna" and "flicka" have the same Swedish stem, but not the
	// same English stem. "flickorna" is not the start of "flicka".
	found := func() bool {
		t.Helper()
		res, err := st.Search(ctx, tn.ID, []int64{sp.ID}, "flickorna", readAll)
		if err != nil {
			t.Fatal(err)
		}
		return len(res) == 1 && res[0].PublicID == id
	}
	if found() {
		t.Fatal("the English stemmer finds flickorna")
	}

	set := tn.TenantSettings
	set.Language, set.ContentLanguage = "sv", "sv"
	got, err := st.UpdateTenant(ctx, tn.ID, tn.Code, set, nil)
	if err != nil || got.Language != "sv" || got.ContentLanguage != "sv" {
		t.Fatalf("UpdateTenant = %+v, %v", got, err)
	}
	// The index still has the English stems.
	if found() {
		t.Fatal("found before ReindexBatch")
	}
	if n, err := st.ReindexBatch(ctx); err != nil || n == 0 {
		t.Fatalf("ReindexBatch = %d, %v", n, err)
	}
	if n, err := st.ReindexBatch(ctx); err != nil || n != 0 {
		t.Fatalf("second ReindexBatch = %d, %v", n, err)
	}
	if !found() {
		t.Fatal("the Swedish stemmer does not find flickorna")
	}
	// A new document gets the stems of the new language at once.
	if _, err := st.CreateNode(ctx, sp.ID, store.NewNode{Kind: store.KindDocument, Title: "Fler",
		Content: content("Flickan"), AuthorID: u.ID}); err != nil {
		t.Fatal(err)
	}
	if n, err := st.ReindexBatch(ctx); err != nil || n != 0 {
		t.Fatalf("ReindexBatch after a new document = %d, %v", n, err)
	}

	// No stemmer.
	set.ContentLanguage = ""
	if _, err := st.UpdateTenant(ctx, tn.ID, tn.Code, set, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReindexBatch(ctx); err != nil {
		t.Fatal(err)
	}
	if found() {
		t.Fatal("found without a stemmer")
	}

	for _, bad := range [][2]string{{"fr", "fr"}, {"", "xx"}, {"xx", "en"}} {
		set.Language, set.ContentLanguage = bad[0], bad[1]
		if _, err := st.UpdateTenant(ctx, tn.ID, tn.Code, set, nil); !errors.Is(err, store.ErrInvalid) {
			t.Errorf("UpdateTenant with %v = %v, want ErrInvalid", bad, err)
		}
		if _, err := st.CreateTenant(ctx, "lang", set, ""); !errors.Is(err, store.ErrInvalid) {
			t.Errorf("CreateTenant with %v = %v, want ErrInvalid", bad, err)
		}
	}
}

func TestProfile(t *testing.T) {
	st := newStore(t)
	tn := defaultTenant(t, st)
	u := login(t, st, tn, "pia")
	if u.Language != "" {
		t.Fatalf("language of a new user = %q", u.Language)
	}
	if err := st.SetProfile(ctx, u.ID, store.Profile{Language: "sv"}); err != nil {
		t.Fatal(err)
	}
	token, err := st.CreateSession(ctx, &tn.ID, u.ID, "", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := st.Session(ctx, token, &tn.ID)
	if err != nil || sess.User.Language != "sv" {
		t.Fatalf("session language = %v, %v", sess, err)
	}
	if err := st.SetProfile(ctx, u.ID, store.Profile{Language: "fr"}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("SetProfile(fr) = %v, want ErrInvalid", err)
	}
	if err := st.SetProfile(ctx, u.ID, store.Profile{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.User(ctx, tn.ID, u.ID); got.Language != "" {
		t.Fatalf("language after a reset = %q", got.Language)
	}
	if err := st.SetProfile(ctx, -1, store.Profile{}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("SetProfile of an unknown user = %v", err)
	}
}
