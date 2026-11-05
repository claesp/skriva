package store

import (
	"fmt"
	"slices"
	"testing"
)

func TestPrefixTokens(t *testing.T) {
	key := []byte("key")
	words := searchWords("Describes an internationalization plan", maxSearchTokens)
	got := prefixTokens(key, words)
	// "describes" gives 7 prefixes (3 to 9 letters), "an" none, the long
	// word 18 (3 to 20 letters) and "plan" 2.
	if len(got) != 7+18+2 || !slices.IsSorted(got) {
		t.Fatalf("got %d tokens, sorted %v", len(got), slices.IsSorted(got))
	}
	for _, p := range []string{"des", "describe", "describes", "internationalizatio", "pla", "plan"} {
		if !slices.Contains(got, prefixToken(key, p)) {
			t.Errorf("no token for the prefix %q", p)
		}
	}
	// A prefix is not the same as a word.
	if slices.Contains(got, searchToken(key, "plan")) {
		t.Error("a prefix token is the same as a word token")
	}
}

func TestQueryTokens(t *testing.T) {
	key := []byte("key")
	q := makeQueryTokens(key, []string{"go", "describe", "internationalizations"}, nil)
	if len(q.exact) != 3 || len(q.exclude) != 0 || q.exclude == nil {
		t.Fatalf("exact %d, exclude %v", len(q.exact), q.exclude)
	}
	if !slices.Equal(q.short, []int64{searchToken(key, "go")}) {
		t.Errorf("short = %v", q.short)
	}
	// A long word uses its first 20 letters.
	want := []longWord{
		{prefixToken(key, "describe"), stemToken(key, "describ")},
		{prefixToken(key, "internationalizatio"+"n"), stemToken(key, "internation")},
	}
	if !slices.Equal(q.long, want) {
		t.Errorf("long = %v, want %v", q.long, want)
	}
	if len(q.stems) != 3 || q.stems[0] != stemToken(key, "go") {
		t.Errorf("stems = %v", q.stems)
	}
	if q := makeQueryTokens(key, []string{"plan"}, nil); q.short == nil {
		t.Error("short is nil")
	}
}

func TestStems(t *testing.T) {
	for _, w := range []string{"describe", "describes", "described", "describing"} {
		if got := wordStem(w); got != "describ" {
			t.Errorf("wordStem(%q) = %q", w, got)
		}
	}
	key := []byte("key")
	got := stemTokens(key, searchWords("Running runs ran", maxSearchTokens))
	// "running" and "runs" have the stem "run". "ran" is a different stem.
	if len(got) != 2 || !slices.Contains(got, stemToken(key, "run")) {
		t.Fatalf("stem tokens = %v", got)
	}
	if slices.Contains(got, prefixToken(key, "run")) || slices.Contains(got, searchToken(key, "run")) {
		t.Error("a stem token is the same as a prefix or a word token")
	}
	if got := stemTokens(key, nil); got == nil {
		t.Error("stem tokens are nil")
	}
}

func TestSnippetPartial(t *testing.T) {
	got := fmt.Sprint(snippet("It describes the design. De facto.", []string{"describe", "de"}))
	if got != "[{It  false} {describes true} { the design.  false} {De true} { facto. false}]" {
		t.Errorf("snippet = %s", got)
	}
	// A word with the same stem also has a mark.
	got = fmt.Sprint(snippet("We described it.", []string{"describing"}))
	if got != "[{We  false} {described true} { it. false}]" {
		t.Errorf("snippet = %s", got)
	}
}
