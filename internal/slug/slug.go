// Package slug makes the readable parts of URLs from titles.
package slug

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// MaxLength is the largest length of a slug in bytes.
const MaxLength = 100

// Make converts a page title to a slug. A slug contains only the
// characters a-z, 0-9 and "-". The function removes diacritics, for
// example "Översikt" becomes "oversikt". The result is empty if the title
// has no letters or digits.
func Make(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range norm.NFKD.String(title) {
		switch {
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(unicode.ToLower(r))
		case unicode.Is(unicode.Mn, r):
			// Ignore combining marks (the diacritics after NFKD).
		default:
			dash = true
		}
		if b.Len() >= MaxLength {
			break
		}
	}
	return strings.TrimRight(b.String()[:min(b.Len(), MaxLength)], "-")
}

var slugRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Normalize converts s from a URL to a slug. It ignores the case of
// the letters, thus "Getting-Started" becomes "getting-started". The
// result is empty if s cannot be a slug.
func Normalize(s string) string {
	// Change only A-Z. strings.ToLower changes also some non-ASCII
	// letters to ASCII letters, for example the Kelvin sign to "k".
	s = strings.Map(func(r rune) rune {
		if 'A' <= r && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, s)
	if !Valid(s) {
		return ""
	}
	return s
}

// Valid reports whether s is a valid slug.
func Valid(s string) bool {
	return len(s) <= MaxLength && slugRE.MatchString(s)
}
