package markdown

import (
	"strings"
	"testing"
)

func TestSlugify(t *testing.T) {
	tests := map[string]string{
		"Home":                   "home",
		"  Hello,   World! ":     "hello-world",
		"Översikt för 2026":      "oversikt-for-2026",
		"a--b__c":                "a-b-c",
		"!!!":                    "",
		"日本語":                    "",
		strings.Repeat("a", 300): strings.Repeat("a", MaxSlugLength),
	}
	for in, want := range tests {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
		if want != "" && !ValidSlug(want) {
			t.Errorf("ValidSlug(%q) = false", want)
		}
	}
}

func TestSlugifyNoTrailingDash(t *testing.T) {
	in := strings.Repeat("a", MaxSlugLength-1) + " b"
	got := Slugify(in)
	if !ValidSlug(got) {
		t.Fatalf("Slugify(%q) = %q is not valid", in, got)
	}
}

func TestValidSlug(t *testing.T) {
	for _, s := range []string{"", "-a", "a-", "A", "a b", "a/b", "../x", "a--b"} {
		if ValidSlug(s) {
			t.Errorf("ValidSlug(%q) = true", s)
		}
	}
}

func TestRenderBasic(t *testing.T) {
	r := New()
	out, err := r.Render("# Title\n\nSome *text* and [[Other Page]] and [[Page#Part Two]].\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n- [x] done\n", "/wiki/")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		`<h1 id="title">Title</h1>`,
		`<em>text</em>`,
		`href="/wiki/other-page"`,
		`href="/wiki/page#part-two"`,
		`<table>`,
		`type="checkbox"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output does not contain %q:\n%s", want, s)
		}
	}
}

func TestRenderRemovesUnsafeContent(t *testing.T) {
	r := New()
	inputs := []string{
		"<script>alert(1)</script>",
		"<img src=x onerror=alert(1)>",
		"[click](javascript:alert(1))",
		"[click](data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==)",
		"![x](javascript:alert(1))",
		"<a href=\"javascript:alert(1)\">x</a>",
		"<iframe src=\"https://example.com\"></iframe>",
		"<div style=\"background:url(javascript:alert(1))\">x</div>",
		"[[\"><script>alert(1)</script>]]",
	}
	for _, in := range inputs {
		out, err := r.Render(in, "/wiki/")
		if err != nil {
			t.Fatal(err)
		}
		s := strings.ToLower(string(out))
		for _, bad := range []string{"<script", "javascript:", "onerror", "<iframe", "style=", "data:text"} {
			if strings.Contains(s, bad) {
				t.Errorf("Render(%q) contains %q: %s", in, bad, out)
			}
		}
	}
}

func TestRenderLinksHaveNoReferrer(t *testing.T) {
	out, err := New().Render("[x](https://example.com)", "/wiki/")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "noreferrer") {
		t.Fatalf("external link has no noreferrer: %s", out)
	}
}
