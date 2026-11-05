package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"
)

// Limits of the search index.
const (
	// maxWordRunes is the maximum length of a word in the index. Longer
	// words are cut.
	maxWordRunes = 64
	// maxSearchTokens is the maximum number of different words of one
	// document in the index.
	maxSearchTokens = 50000
	// minPrefixRunes and maxPrefixRunes are the lengths of the prefixes
	// in the index. A search word with fewer letters than minPrefixRunes
	// finds only whole words. A longer search word uses its first
	// maxPrefixRunes letters.
	minPrefixRunes = 3
	maxPrefixRunes = 20
	// maxPrefixTokens is the maximum number of different prefixes of one
	// document in the index.
	maxPrefixTokens = 200000
	// searchIndexVersion is the version of the format of the index. A
	// change of the words or the tokens needs a new version. Then
	// ReindexBatch makes the index of the old rows again.
	searchIndexVersion = 2
)

// searchIndex is the search index of a node.
type searchIndex struct {
	// tokens are the hashes of the words of the title and the text.
	// titleTokens are the hashes of the words of the title.
	// prefixTokens are the hashes of the prefixes of the words of the
	// title and the text.
	tokens, titleTokens, prefixTokens []int64
}

// makeSearchIndex makes the search index of a title and a plain text.
func makeSearchIndex(key []byte, title, plainText string) searchIndex {
	words := searchWords(title+"\n"+plainText, maxSearchTokens)
	tokens := tokensOf(key, words)
	slices.Sort(tokens)
	return searchIndex{
		tokens:       tokens,
		titleTokens:  searchTokens(key, title),
		prefixTokens: prefixTokens(key, words),
	}
}

// wordPrefix returns the prefix of a word for the index, or "" if the
// word is too short.
func wordPrefix(word string, length int) string {
	r := []rune(word)
	if len(r) < length {
		return ""
	}
	return string(r[:length])
}

// prefixTokens returns the hashes of the prefixes of the words, sorted.
// Each word gives its prefixes of minPrefixRunes to maxPrefixRunes
// letters. A word of 3 to 20 letters is also a prefix of itself.
func prefixTokens(key []byte, words []string) []int64 {
	seen := map[string]bool{}
	// Not nil: the column is NOT NULL, and pgx sends nil as NULL.
	out := []int64{}
	for _, w := range words {
		for n := minPrefixRunes; n <= maxPrefixRunes; n++ {
			p := wordPrefix(w, n)
			if p == "" {
				break
			}
			if !seen[p] {
				seen[p] = true
				out = append(out, prefixToken(key, p))
			}
		}
		if len(out) >= maxPrefixTokens {
			break
		}
	}
	slices.Sort(out)
	return out
}

// queryTokens are the tokens of the words of a search.
type queryTokens struct {
	// exact are the hashes of all words. A result with all of them has
	// the words as whole words.
	exact []int64
	// short are the hashes of the words with fewer than minPrefixRunes
	// letters. A result must contain them as whole words.
	short []int64
	// prefixes are the hashes of the prefixes of the other words. A
	// result must contain words that start with them.
	prefixes []int64
	// exclude are the hashes of the words that a result must not
	// contain.
	exclude []int64
}

func makeQueryTokens(key []byte, include, exclude []string) queryTokens {
	// The arrays must not be nil: pgx sends nil as NULL, and "@> NULL"
	// finds nothing.
	q := queryTokens{exact: tokensOf(key, include), exclude: tokensOf(key, exclude),
		short: []int64{}, prefixes: []int64{}}
	for _, w := range include {
		if p := wordPrefix(w, min(len([]rune(w)), maxPrefixRunes)); len([]rune(w)) >= minPrefixRunes {
			q.prefixes = append(q.prefixes, prefixToken(key, p))
		} else {
			q.short = append(q.short, searchToken(key, w))
		}
	}
	return q
}

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
	// A word of the text matches if it is a search word, or if it starts
	// with a search word of minPrefixRunes letters or more.
	match := func(f string) bool {
		for _, w := range searchWords(f, 8) {
			for _, q := range words {
				if w == q || (len([]rune(q)) >= minPrefixRunes && strings.HasPrefix(w, wordPrefix(q, min(len([]rune(q)), maxPrefixRunes)))) {
					return true
				}
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

// reindexBatchSize is the number of rows of one ReindexBatch call.
const reindexBatchSize = 100

// ReindexBatch makes the search index again for up to reindexBatchSize
// nodes with an index of an older format (search_version). It returns
// the number of changed nodes. A node that a user saves during the
// batch keeps the index of the save.
func (s *Store) ReindexBatch(ctx context.Context) (int, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT sp.tenant_id, n.id, n.space_id, n.public_id, n.version, n.title, n.plain_text
		FROM nodes n JOIN spaces sp ON sp.id = n.space_id
		WHERE n.search_version < $1 LIMIT $2`, searchIndexVersion, reindexBatchSize)
	if err != nil {
		return 0, err
	}
	type item struct {
		tenantID, id, spaceID int64
		publicID              string
		version               int
		title, plainText      []byte
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (item, error) {
		var it item
		err := row.Scan(&it.tenantID, &it.id, &it.spaceID, &it.publicID, &it.version, &it.title, &it.plainText)
		return it, err
	})
	if err != nil {
		return 0, err
	}
	changed := 0
	var failed []error
	for _, it := range items {
		tk, err := s.tenantKeys(ctx, it.tenantID)
		if err != nil {
			return changed, err
		}
		title, err := s.open(ctx, it.tenantID, titlePlace(it.spaceID, it.publicID), it.title)
		if err == nil {
			var plain []byte
			if plain, err = s.open(ctx, it.tenantID, plainTextPlace(it.spaceID, it.publicID), it.plainText); err == nil {
				idx := makeSearchIndex(tk.search, string(title), string(plain))
				_, err = s.pool.Exec(ctx, `
					UPDATE nodes SET search_tokens = $3, title_tokens = $4, prefix_tokens = $5, search_version = $6
					WHERE id = $1 AND version = $2 AND search_version < $6`,
					it.id, it.version, idx.tokens, idx.titleTokens, idx.prefixTokens, searchIndexVersion)
			}
		}
		if errors.Is(err, ErrKey) {
			// Continue with the other rows. The row stays in the old
			// format and the next batch tries again.
			failed = append(failed, fmt.Errorf("node %s: %w", it.publicID, err))
			continue
		}
		if err != nil {
			return changed, err
		}
		changed++
	}
	return changed, errors.Join(failed...)
}
