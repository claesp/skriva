package store_test

import (
	"errors"
	"testing"

	"github.com/claesp/skriva/internal/store"
)

// classMin returns the name of the lowest level in a *ClassificationError.
func classMin(err error) string {
	var ce *store.ClassificationError
	if errors.As(err, &ce) {
		return ce.Min
	}
	return ""
}

func TestClassificationLimits(t *testing.T) {
	st := newStore(t)
	tn := defaultTenant(t, st)
	sp := defaultSpace(t, st, tn)
	u := login(t, st, tn, "cleo")
	levels, err := st.Classifications(ctx, tn.ID)
	if err != nil {
		t.Fatal(err)
	}
	id := map[string]int64{}
	for _, c := range levels {
		id[c.Name] = c.ID
	}
	setSpace := func(def, min string) error {
		return st.UpdateSpace(ctx, tn.ID, sp.Slug, store.SpaceSettings{
			Slug: sp.Slug, Name: sp.Name, UploadQuotaMB: 1024, SearchResults: 50,
			DefaultClassificationID: id[def], MinClassificationID: id[min],
		})
	}
	newDoc := func(parent, class string) (string, error) {
		return st.CreateNode(ctx, sp.ID, store.NewNode{ParentPublicID: parent, Kind: store.KindDocument,
			Title: "Doc", Content: content("x"), AuthorID: u.ID, ClassificationID: id[class]})
	}
	classOf := func(publicID string) string {
		t.Helper()
		n, err := st.Node(ctx, sp.ID, publicID)
		if err != nil {
			t.Fatal(err)
		}
		return n.Classification
	}

	// A document with a lower level prevents the limit of the space.
	low, err := newDoc("", "")
	if err != nil {
		t.Fatal(err)
	}
	var ce *store.ClassificationError
	if err := setSpace("Secret", "Restricted"); !errors.As(err, &ce) || ce.Documents != 1 || ce.Min != "Restricted" {
		t.Fatalf("space limit with a low document = %v", err)
	}
	if err := st.UpdateNode(ctx, sp.ID, low, store.NodeUpdate{BaseVersion: 1, Title: "Doc", Content: content("x"),
		AuthorID: u.ID, ClassificationID: id["Restricted"]}); err != nil {
		t.Fatal(err)
	}
	// The default must not be lower than the lowest level.
	if err := setSpace("Public", "Restricted"); classMin(err) != "Restricted" {
		t.Fatalf("space default below the limit = %v", err)
	}
	if err := setSpace("Secret", "Restricted"); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Space(ctx, tn.ID, sp.Slug)
	if got.DefaultClassification != "Secret" || got.MinClassification != "Restricted" {
		t.Fatalf("space limits = %q, %q", got.DefaultClassification, got.MinClassification)
	}

	// New documents at the top level.
	top, err := newDoc("", "")
	if err != nil || classOf(top) != "Secret" {
		t.Fatalf("new document = %v, %q, want the default of the space", err, classOf(top))
	}
	if _, err := newDoc("", "Public"); classMin(err) != "Restricted" {
		t.Fatalf("document below the limit = %v", err)
	}

	// A folder can only make the limits stricter.
	folder, err := st.CreateNode(ctx, sp.ID, store.NewNode{Kind: store.KindFolder, Title: "Vault", AuthorID: u.ID})
	if err != nil {
		t.Fatal(err)
	}
	setFolder := func(def, min string) error {
		return st.SetFolderClassification(ctx, sp.ID, folder, store.FolderClassification{DefaultID: id[def], MinID: id[min]})
	}
	if err := setFolder("", "Public"); classMin(err) != "Restricted" {
		t.Fatalf("folder limit below the space limit = %v", err)
	}
	if err := setFolder("Restricted", "Top Secret"); classMin(err) != "Top Secret" {
		t.Fatalf("folder default below the folder limit = %v", err)
	}
	if err := setFolder("", "Top Secret"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetFolderClassification(ctx, sp.ID, top, store.FolderClassification{MinID: id["Secret"]}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("limits on a document = %v, want ErrInvalid", err)
	}
	f, _ := st.Node(ctx, sp.ID, folder)
	l, err := st.ClassificationLimits(ctx, sp.ID, &f.ID)
	if err != nil || l.Min.Name != "Top Secret" || l.Default.Name != "Top Secret" {
		t.Fatalf("folder limits = %+v, %v", l, err)
	}
	// The default of the space is lower than the limit of the folder.
	// Thus a new document gets the lowest level of the folder.
	inside, err := newDoc(folder, "")
	if err != nil || classOf(inside) != "Top Secret" {
		t.Fatalf("new document in the folder = %v, %q", err, classOf(inside))
	}
	if err := st.UpdateNode(ctx, sp.ID, inside, store.NodeUpdate{BaseVersion: 1, Title: "Doc", Content: content("y"),
		AuthorID: u.ID, ClassificationID: id["Secret"]}); classMin(err) != "Top Secret" {
		t.Fatalf("update below the folder limit = %v", err)
	}
	// A save without a level keeps the level.
	if err := st.UpdateNode(ctx, sp.ID, inside, store.NodeUpdate{BaseVersion: 1, Title: "Doc", Content: content("y"),
		AuthorID: u.ID}); err != nil {
		t.Fatal(err)
	}

	// A move must keep the documents permitted.
	if err := st.MoveNode(ctx, sp.ID, top, folder); classMin(err) != "Top Secret" {
		t.Fatalf("move of a Secret document into the folder = %v", err)
	}
	if err := st.MoveNode(ctx, sp.ID, inside, ""); err != nil {
		t.Fatalf("move out of the folder = %v", err)
	}
	// A folder limit is not possible if documents in it have lower levels.
	if err := st.MoveNode(ctx, sp.ID, inside, folder); err != nil {
		t.Fatal(err)
	}
	if err := setFolder("", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.MoveNode(ctx, sp.ID, top, folder); err != nil {
		t.Fatal(err)
	}
	if err := setFolder("", "Top Secret"); !errors.As(err, &ce) || ce.Documents != 1 {
		t.Fatalf("folder limit with a low document = %v", err)
	}

	// A restore checks the limits of the old place now.
	if err := st.TrashNode(ctx, sp.ID, low, u.ID); err != nil {
		t.Fatal(err)
	}
	if err := setSpace("", "Secret"); err != nil {
		t.Fatal(err)
	}
	if err := st.RestoreNode(ctx, sp.ID, low); classMin(err) != "Secret" {
		t.Fatalf("restore below the limit = %v", err)
	}

	// A level that a limit uses cannot be deleted.
	levels, _ = st.Classifications(ctx, tn.ID)
	for _, c := range levels {
		if c.Name == "Secret" && (c.Settings != 1 || !c.InUse()) {
			t.Fatalf("Secret: %d settings", c.Settings)
		}
	}
	if err := st.SetFolderClassification(ctx, sp.ID, folder, store.FolderClassification{}); err != nil {
		t.Fatal(err)
	}

	// A level of a different tenant is not valid.
	other := newTenant(t, st, "othr")
	otherLevels, _ := st.Classifications(ctx, other.ID)
	if err := st.SetFolderClassification(ctx, sp.ID, folder, store.FolderClassification{MinID: otherLevels[4].ID}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("level of a different tenant = %v, want ErrInvalid", err)
	}
	if err := st.UpdateSpace(ctx, tn.ID, sp.Slug, store.SpaceSettings{Slug: sp.Slug, Name: sp.Name,
		UploadQuotaMB: 1024, SearchResults: 50, MinClassificationID: otherLevels[4].ID}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("space level of a different tenant = %v, want ErrInvalid", err)
	}
}
