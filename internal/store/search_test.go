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
	want := []int64{prefixToken(key, "describe"), prefixToken(key, "internationalizatio"+"n")}
	if !slices.Equal(q.prefixes, want) {
		t.Errorf("prefixes = %v, want %v", q.prefixes, want)
	}
	if q := makeQueryTokens(key, []string{"plan"}, nil); q.short == nil {
		t.Error("short is nil")
	}
}

func TestSnippetPartial(t *testing.T) {
	got := fmt.Sprint(snippet("It describes the design. De facto.", []string{"describe", "de"}))
	if got != "[{It  false} {describes true} { the design.  false} {De true} { facto. false}]" {
		t.Errorf("snippet = %s", got)
	}
}
