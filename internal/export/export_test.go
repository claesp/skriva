package export

import (
	"strings"
	"testing"
	"time"

	"github.com/clpe/skriva/internal/doc"
)

func TestLookup(t *testing.T) {
	if f, ok := Lookup("markdown"); !ok || f.Extension != ".md" {
		t.Fatalf("Lookup(markdown) = %v, %v", f, ok)
	}
	if _, ok := Lookup("pdf"); ok {
		t.Fatal("Lookup(pdf) found a format")
	}
	for _, f := range Formats {
		if f.Name == "" || strings.Trim(f.Name, "abcdefghijklmnopqrstuvwxyz") != "" || f.Write == nil {
			t.Errorf("format %q is not valid", f.Name)
		}
	}
}

func markdown(t *testing.T, d *Document) string {
	t.Helper()
	var b strings.Builder
	if err := markdownFormat.Write(&b, d); err != nil {
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
		"_This document is classified as RESTRICTED._\n\n" +
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
