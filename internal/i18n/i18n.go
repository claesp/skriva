// Package i18n contains the languages of the user interface and of the
// content.
//
// The texts of the user interface are in English in the code and in the
// templates. The English text is the key of a translation. A catalog
// (locales/<code>/*.json) gives the translations for one language. A
// text without a translation stays in English.
//
// The content language of a tenant selects the stemmer of the search.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/kljensen/snowball/english"
	"github.com/kljensen/snowball/french"
	"github.com/kljensen/snowball/hungarian"
	"github.com/kljensen/snowball/norwegian"
	"github.com/kljensen/snowball/russian"
	"github.com/kljensen/snowball/spanish"
	"github.com/kljensen/snowball/swedish"
)

// Default is the language of the texts in the code.
const Default = "en"

// Language is a language of the user interface or of the content. Code
// is a BCP 47 language code. Name is the name of the language in the
// language itself.
type Language struct {
	Code string
	Name string
}

// ui are the languages of the user interface. Each language except
// English has a catalog.
var ui = []Language{
	{"en", "English"},
	{"sv", "Svenska"},
}

// content are the languages of the content, with a stemmer for the
// search.
var content = []Language{
	{"en", "English"},
	{"es", "Español"},
	{"fr", "Français"},
	{"hu", "Magyar"},
	{"nb", "Norsk bokmål"},
	{"ru", "Русский"},
	{"sv", "Svenska"},
}

// stemmers are the Snowball stemmers of the content languages.
var stemmers = map[string]func(string, bool) string{
	"en": english.Stem,
	"es": spanish.Stem,
	"fr": french.Stem,
	"hu": hungarian.Stem,
	"nb": norwegian.Stem,
	"ru": russian.Stem,
	"sv": swedish.Stem,
}

// NoStemming is the content language code for no stemmer.
const NoStemming = ""

// UILanguages returns the languages of the user interface.
func UILanguages() []Language { return ui }

// ContentLanguages returns the content languages with a stemmer.
func ContentLanguages() []Language { return content }

// IsUI reports whether code is a language of the user interface.
func IsUI(code string) bool { return find(ui, code) != nil }

// IsContent reports whether code is a content language, or NoStemming.
func IsContent(code string) bool { return code == NoStemming || find(content, code) != nil }

// Name returns the name of a language, or the code if the language is
// not known.
func Name(code string) string {
	if l := find(ui, code); l != nil {
		return l.Name
	}
	if l := find(content, code); l != nil {
		return l.Name
	}
	return code
}

func find(list []Language, code string) *Language {
	for i := range list {
		if list[i].Code == code {
			return &list[i]
		}
	}
	return nil
}

// Stem returns the stem of a word in the content language code. Words
// with fewer than 3 letters and stop words do not change. For
// NoStemming or an unknown code, the word does not change.
func Stem(code, word string) string {
	if f := stemmers[code]; f != nil {
		return f(word, false)
	}
	return word
}

//go:embed locales
var localeFS embed.FS

// catalogs are the translations by language code.
var catalogs = mustLoad(localeFS)

// mustLoad reads all catalogs. A language can have more than one file.
// The same text in two files of a language must have the same
// translation.
func mustLoad(fsys fs.FS) map[string]map[string]string {
	out, err := load(fsys)
	if err != nil {
		panic(err)
	}
	return out
}

func load(fsys fs.FS) (map[string]map[string]string, error) {
	files, err := fs.Glob(fsys, "locales/*/*.json")
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	out := map[string]map[string]string{}
	for _, f := range files {
		code := path.Base(path.Dir(f))
		if !IsUI(code) || code == Default {
			return nil, fmt.Errorf("%s: %q is not a language with a catalog", f, code)
		}
		data, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, err
		}
		var m map[string]string
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		cat := out[code]
		if cat == nil {
			cat = map[string]string{}
			out[code] = cat
		}
		for k, v := range m {
			if old, ok := cat[k]; ok && old != v {
				return nil, fmt.Errorf("%s: %q has two different translations", f, k)
			}
			cat[k] = v
		}
	}
	return out, nil
}

// Printer gives the texts of one language.
type Printer struct {
	lang string
	cat  map[string]string
}

// For returns the Printer of a language of the user interface. For an
// unknown code, it returns the Printer of the default language.
func For(code string) *Printer {
	if !IsUI(code) {
		code = Default
	}
	return &Printer{lang: code, cat: catalogs[code]}
}

// Lang returns the language code of the Printer.
func (p *Printer) Lang() string { return p.lang }

// lookup returns the translation of msg, or msg.
func (p *Printer) lookup(msg string) string {
	if s, ok := p.cat[msg]; ok && s != "" {
		return s
	}
	return msg
}

// T returns the translation of msg. If there are arguments, msg is a
// format for fmt.Sprintf.
func (p *Printer) T(msg string, args ...any) string {
	s := p.lookup(msg)
	if len(args) == 0 {
		return s
	}
	return fmt.Sprintf(s, args...)
}

// N returns the translation of one (if n is 1) or other, as a format
// with the arguments. Without arguments, the argument is n. The rule is
// correct for English and Swedish.
func (p *Printer) N(n int, one, other string, args ...any) string {
	msg := other
	if n == 1 {
		msg = one
	}
	if len(args) == 0 {
		args = []any{n}
	}
	return p.T(msg, args...)
}

// HTML returns the translation of msg as HTML. The translation is
// trusted (it comes from the catalog). The arguments are escaped, except
// values of the type template.HTML and numbers.
func (p *Printer) HTML(msg string, args ...any) template.HTML {
	s := p.lookup(msg)
	if len(args) == 0 {
		return template.HTML(s) // #nosec G203 -- the text comes from the code or the embedded catalog.
	}
	esc := make([]any, len(args))
	for i, a := range args {
		switch v := a.(type) {
		case template.HTML:
			esc[i] = string(v)
		case int, int64, int32, uint, uint64, float64, bool:
			// A number cannot contain HTML. It keeps its type for %d.
			esc[i] = v
		default:
			esc[i] = template.HTMLEscapeString(fmt.Sprint(v))
		}
	}
	return template.HTML(fmt.Sprintf(s, esc...)) // #nosec G203 -- trusted format, escaped arguments.
}

// Catalog returns the translations of a language for the texts msgs, for
// example for a script. A text without a translation is in English.
func (p *Printer) Catalog(msgs ...string) map[string]string {
	out := make(map[string]string, len(msgs))
	for _, m := range msgs {
		out[m] = p.lookup(m)
	}
	return out
}

// Texts returns the translations of a language, for tests.
func Texts(code string) map[string]string { return catalogs[code] }

// Verbs returns the format verbs of a text, sorted, for tests: "%s",
// "%d", "%[2]s" and so on. "%%" is not a verb.
func Verbs(s string) []string {
	var out []string
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			continue
		}
		j := i + 1
		for j < len(s) && strings.IndexByte("+-# 0123456789.[]", s[j]) >= 0 {
			j++
		}
		if j < len(s) && s[j] != '%' {
			out = append(out, s[i:j+1])
		}
		i = j
	}
	sort.Strings(out)
	return out
}
