package export

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/claesp/skriva/internal/doc"
)

func TestLookup(t *testing.T) {
	fs := NewFormats(nil)
	if f, ok := fs.Lookup("markdown"); !ok || f.Extension != ".md" || !f.Available() {
		t.Fatalf("Lookup(markdown) = %v, %v", f, ok)
	}
	if f, ok := fs.Lookup("pdf"); !ok || f.Available() || f.Unavailable == "" {
		t.Fatalf("Lookup(pdf) = %v, %v", f, ok)
	}
	if _, ok := fs.Lookup("docx"); ok {
		t.Fatal("Lookup(docx) found a format")
	}
	if f, _ := NewFormats(&PDFOptions{Typst: "typst"}).Lookup("pdf"); !f.Available() {
		t.Fatal("PDF is not available with a Typst program")
	}
	for _, f := range fs {
		if f.Name == "" || strings.Trim(f.Name, "abcdefghijklmnopqrstuvwxyz") != "" {
			t.Errorf("format %q is not valid", f.Name)
		}
	}
}

func markdown(t *testing.T, d *Document) string {
	t.Helper()
	var b strings.Builder
	if err := markdownFormat.Write(context.Background(), &b, d); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestMarkdown(t *testing.T) {
	body, err := doc.Parse([]byte(`{"type":"doc","content":[`+
		`{"type":"heading","attrs":{"level":5},"content":[{"type":"text","text":"Five"}]},`+
		`{"type":"heading","attrs":{"level":8},"content":[{"type":"text","text":"Eight"}]},`+
		`{"type":"paragraph","content":[{"type":"text","text":"Go","marks":[{"type":"link","attrs":{"href":"wiki:Start#Run"}}]}]},`+
		`{"type":"image","attrs":{"src":"https://img.example/a.png","alt":"A"}}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := markdown(t, &Document{
		Title: "Plan", Classification: "Restricted", Version: 3,
		UpdatedAt: time.Date(2026, 11, 2, 10, 0, 0, 0, time.FixedZone("CET", 3600)),
		URL:       "https://wiki.example/t/s/d/x/plan",
		Body:      body,
		Links:     doc.Links{Wiki: "https://wiki.example/t/s/w/", Files: "https://wiki.example/t/s/files/"},
	})
	want := "---\n" +
		"title: \"Plan\"\n" +
		"classification: \"Restricted\"\n" +
		"version: 3\n" +
		"updated: \"2026-11-02T09:00:00Z\"\n" +
		"source: \"https://wiki.example/t/s/d/x/plan\"\n" +
		"---\n\n" +
		"# Plan\n\n" +
		"###### Five\n\n" +
		"###### Eight\n\n" +
		"[Go](https://wiki.example/t/s/w/start#run)\n\n" +
		"![A](https://img.example/a.png)\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// TestMarkdownFrontMatter checks that a title cannot change the
// structure of the front matter or of the first heading.
func TestMarkdownFrontMatter(t *testing.T) {
	got := markdown(t, &Document{Title: "a\"\n---\nx: *y\n# z", Version: 1, Body: doc.Empty()})
	want := "---\n" +
		"title: \"a\\\"\\n---\\nx: *y\\n# z\"\n" +
		"version: 1\n" +
		"updated: \"0001-01-01T00:00:00Z\"\n" +
		"---\n\n" +
		"# a\" --- x: \\*y \\# z\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestHTML(t *testing.T) {
	const id, other = "abcdefghijklmn", "zzzzzzzzzzzzzz"
	body, err := doc.Parse([]byte(`{"type":"doc","content":[`+
		`{"type":"heading","attrs":{"level":1},"content":[{"type":"text","text":"Install"}]},`+
		`{"type":"paragraph","content":[{"type":"text","text":"Go","marks":[{"type":"link","attrs":{"href":"wiki:Start"}}]}]},`+
		`{"type":"image","attrs":{"src":"file:`+id+`","alt":"A"}},`+
		`{"type":"image","attrs":{"src":"file:`+id+`","alt":"B"}},`+
		`{"type":"image","attrs":{"src":"file:`+other+`","alt":"C"}}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	var b strings.Builder
	err = htmlFormat.Write(context.Background(), &b, &Document{
		Title: `Plan <script>alert(1)</script>`, Classification: "Restricted", Version: 3,
		UpdatedAt: time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC),
		URL:       "https://wiki.example/t/s/d/x/plan",
		Body:      body,
		Links:     doc.Links{Wiki: "https://wiki.example/t/s/w/", Files: "https://wiki.example/t/s/files/"},
		Image: func(fid string) (*Image, error) {
			calls++
			if fid != id {
				return nil, nil
			}
			return &Image{ContentType: "image/png", Data: []byte("PNG")}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := b.String()
	for _, want := range []string{
		`<meta http-equiv="Content-Security-Policy" content="default-src 'none';`,
		`<title>Plan &lt;script&gt;alert(1)&lt;/script&gt;</title>`,
		`<meta name="classification" content="Restricted">`,
		`<meta name="version" content="3">`,
		`<meta name="dcterms.modified" content="2026-11-02T10:00:00Z">`,
		`<link rel="canonical" href="https://wiki.example/t/s/d/x/plan">`,
		`<h1>Plan &lt;script&gt;alert(1)&lt;/script&gt;</h1>`,
		`<h2 id="install" class="level-1">Install</h2>`,
		`<a href="https://wiki.example/t/s/w/start" rel="nofollow noreferrer">Go</a>`,
		`<img src="data:image/png;base64,UE5H" alt="A"`,
		`<img src="data:image/png;base64,UE5H" alt="B"`,
		`<img src="https://wiki.example/t/s/files/` + other + `" alt="C"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("HTML does not contain %q", want)
		}
	}
	if strings.Contains(got, "<script") {
		t.Error("HTML contains a script element")
	}
	if calls != 2 {
		t.Errorf("Image was called %d times, want 2", calls)
	}
	if t.Failed() {
		t.Log(got)
	}
}

// TestEmbedImages checks the types and the size limit of the images.
func TestEmbedImages(t *testing.T) {
	ids := []string{"aaaaaaaaaaaaaa", "bbbbbbbbbbbbbb", "cccccccccccccc"}
	var content []string
	for _, id := range ids {
		content = append(content, `{"type":"image","attrs":{"src":"file:`+id+`"}}`)
	}
	body, err := doc.Parse([]byte(`{"type":"doc","content":[`+strings.Join(content, ",")+`]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	images := map[string]*Image{
		ids[0]: {ContentType: "text/html", Data: []byte("<script>")},
		ids[1]: {ContentType: "image/gif", Data: make([]byte, MaxEmbeddedBytes-10)},
		ids[2]: {ContentType: "image/gif", Data: make([]byte, 11)},
	}
	if err := embedImages(body, func(id string) (*Image, error) { return images[id], nil }); err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"file:" + ids[0], "data:image/gif;base64,", "file:" + ids[2]} {
		if src, _ := body.Content[i].Attrs["src"].(string); !strings.HasPrefix(src, want) {
			t.Errorf("image %d has the source %.40q, want %q", i, src, want)
		}
	}
}
