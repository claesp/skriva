package i18n

import (
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

func TestPrinter(t *testing.T) {
	sv := For("sv")
	if sv.Lang() != "sv" || For("xx").Lang() != Default {
		t.Fatalf("languages: %q, %q", sv.Lang(), For("xx").Lang())
	}
	if got := For("en").T("%d of %d", 1, 2); got != "1 of 2" {
		t.Errorf("T = %q", got)
	}
	if got := sv.T("no translation %s", "x"); got != "no translation x" {
		t.Errorf("T without a translation = %q", got)
	}
	if got := For("en").N(2, "%d day", "%d days"); got != "2 days" {
		t.Errorf("N = %q", got)
	}
	if got := For("en").N(1, "%d day", "%d days"); got != "1 day" {
		t.Errorf("N = %q", got)
	}
	if got := For("en").HTML("Type <code>%s</code>.", "<b>"); got != "Type <code>&lt;b&gt;</code>." {
		t.Errorf("HTML = %q", got)
	}
	if got := For("en").HTML("<b>%d</b> of %s", 2, "x&y"); got != "<b>2</b> of x&amp;y" {
		t.Errorf("HTML with a number = %q", got)
	}
}

func TestLoad(t *testing.T) {
	good := fstest.MapFS{
		"locales/sv/a.json": {Data: []byte(`{"Save": "Spara", "Edit": "Redigera"}`)},
		"locales/sv/b.json": {Data: []byte(`{"Save": "Spara"}`)},
	}
	cat, err := load(good)
	if err != nil || cat["sv"]["Edit"] != "Redigera" {
		t.Fatalf("load = %v, %v", cat, err)
	}
	for name, fsys := range map[string]fstest.MapFS{
		"conflict": {
			"locales/sv/a.json": {Data: []byte(`{"Save": "Spara"}`)},
			"locales/sv/b.json": {Data: []byte(`{"Save": "Lagra"}`)},
		},
		"unknown language": {"locales/xx/a.json": {Data: []byte(`{}`)}},
		"english":          {"locales/en/a.json": {Data: []byte(`{}`)}},
		"bad json":         {"locales/sv/a.json": {Data: []byte(`{`)}},
	} {
		if _, err := load(fsys); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// Each translation has the same format verbs as the English text.
func TestCatalogVerbs(t *testing.T) {
	for _, l := range UILanguages() {
		for k, v := range Texts(l.Code) {
			if !slices.Equal(Verbs(k), Verbs(v)) {
				t.Errorf("%s: %q has the verbs %v, the translation %q has %v", l.Code, k, Verbs(k), v, Verbs(v))
			}
			if strings.TrimSpace(v) == "" {
				t.Errorf("%s: %q has an empty translation", l.Code, k)
			}
		}
	}
	if got := Verbs("%d of %[2]s, 100%%"); !slices.Equal(got, []string{"%[2]s", "%d"}) {
		t.Errorf("Verbs = %v", got)
	}
}

func TestStem(t *testing.T) {
	for _, c := range []struct{ lang, word, want string }{
		{"en", "describing", "describ"},
		{"sv", "dokumenten", "dokument"},
		{"fr", "continuellement", "continuel"},
		{NoStemming, "describing", "describing"},
		{"xx", "describing", "describing"},
	} {
		if got := Stem(c.lang, c.word); got != c.want {
			t.Errorf("Stem(%q, %q) = %q, want %q", c.lang, c.word, got, c.want)
		}
	}
	for _, l := range ContentLanguages() {
		if stemmers[l.Code] == nil {
			t.Errorf("%s has no stemmer", l.Code)
		}
	}
	if !IsContent(NoStemming) || IsContent("xx") || !IsUI("sv") || IsUI("fr") {
		t.Error("IsContent or IsUI is wrong")
	}
}
