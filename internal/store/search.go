package store

import (
	"slices"
	"strings"
	"unicode"
)

// Limits of the search index.
const (
	// maxWordRunes is the maximum length of a word in the index. Longer
	// words are cut.
	maxWordRunes = 64
	// maxSearchTokens is the maximum number of different words of one
	// document in the index.
	maxSearchTokens = 50000
)

// searchWords returns the different words of text in lower case. A word
// is a sequence of letters and digits. The order is the order of the
// first occurrence.
func searchWords(text string, max int) []string {
	var out []string
	seen := map[string]bool{}
	for _, w := range strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		w = strings.ToLower(w)
		if r := []rune(w); len(r) > maxWordRunes {
			w = string(r[:maxWordRunes])
		}
		if !seen[w] {
			seen[w] = true
			out = append(out, w)
			if len(out) == max {
				break
			}
		}
	}
	return out
}

// searchTokens returns the search index of a text: the keyed hashes of
// its words, sorted.
func searchTokens(key []byte, text string) []int64 {
	out := tokensOf(key, searchWords(text, maxSearchTokens))
	slices.Sort(out)
	return out
}

func tokensOf(key []byte, words []string) []int64 {
	out := make([]int64, len(words))
	for i, w := range words {
		out[i] = searchToken(key, w)
	}
	return out
}

// maxQueryWords is the maximum number of words in a search query.
const maxQueryWords = 32

// parseSearchQuery returns the words that a result must contain and the
// words that it must not contain ("-word").
func parseSearchQuery(q string) (include, exclude []string) {
	for _, f := range strings.Fields(q) {
		words := searchWords(f, maxQueryWords)
		if strings.HasPrefix(f, "-") {
			exclude = append(exclude, words...)
		} else {
			include = append(include, words...)
		}
	}
	if len(include) > maxQueryWords {
		include = include[:maxQueryWords]
	}
	if len(exclude) > maxQueryWords {
		exclude = exclude[:maxQueryWords]
	}
	return include, exclude
}

// snippetWords is the number of words in a snippet.
const snippetWords = 24

// SnippetPart is a part of a snippet. Mark is true for a word that
// matches the search.
type SnippetPart struct {
	Text string
	Mark bool
}

// snippet returns a part of text around the first word of words. Each
// word of text that contains one of words is a part with Mark. "…" shows
// that the snippet starts or ends in the text.
func snippet(text string, words []string) []SnippetPart {
	fields := strings.Fields(text)
	match := func(f string) bool {
		for _, w := range searchWords(f, 8) {
			if slices.Contains(words, w) {
				return true
			}
		}
		return false
	}
	first := 0
	for i, f := range fields {
		if match(f) {
			first = i
			break
		}
	}
	start := max(0, first-snippetWords/3)
	end := min(len(fields), start+snippetWords)
	var out []SnippetPart
	var b strings.Builder
	flush := func(mark bool) {
		if b.Len() > 0 {
			out = append(out, SnippetPart{Text: b.String(), Mark: mark})
			b.Reset()
		}
	}
	if start > 0 {
		b.WriteString("… ")
	}
	for i := start; i < end; i++ {
		if i > start {
			b.WriteByte(' ')
		}
		if match(fields[i]) {
			flush(false)
			b.WriteString(fields[i])
			flush(true)
		} else {
			b.WriteString(fields[i])
		}
	}
	if end < len(fields) {
		b.WriteString(" …")
	}
	flush(false)
	return out
}
