package web

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/claesp/skriva/internal/i18n"
	"github.com/claesp/skriva/internal/rbac"
)

// templateTextRE finds the texts of the template functions t, tn and
// thtml. A text is a Go string literal ("…" or `…`).
var templateTextRE = regexp.MustCompile("(?:^|[\\s({])(t|thtml|tn\\s+[^\"`\\s]+)\\s+(\"(?:[^\"\\\\]|\\\\.)*\"|`[^`]*`)(?:\\s+(\"(?:[^\"\\\\]|\\\\.)*\"|`[^`]*`))?")

// templateTexts returns the texts of the translations in the templates.
func templateTexts(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := fs.WalkDir(templateFS, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := templateFS.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range templateTextRE.FindAllStringSubmatch(string(data), -1) {
			lits := []string{m[2]}
			// tn has two texts: the singular and the plural.
			if strings.HasPrefix(m[1], "tn") && m[3] != "" {
				lits = append(lits, m[3])
			}
			for _, lit := range lits {
				s, err := strconv.Unquote(lit)
				if err != nil {
					t.Errorf("%s: %s: %v", path, lit, err)
					continue
				}
				out[s] = path
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// goTexts returns the texts of the calls of the Printer methods T, N,
// HTML and Catalog in the Go files of the package. A text must be a
// string literal or a constant of the package.
func goTexts(t *testing.T) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var parsed []*ast.File
	consts := map[string]string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		parsed = append(parsed, af)
		ast.Inspect(af, func(n ast.Node) bool {
			if vs, ok := n.(*ast.ValueSpec); ok {
				for i, name := range vs.Names {
					if i < len(vs.Values) {
						if s, ok := stringValue(vs.Values[i], nil); ok {
							consts[name.Name] = s
						}
					}
				}
			}
			return true
		})
	}
	out := map[string]string{}
	for _, af := range parsed {
		ast.Inspect(af, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "template" {
				// template.HTML is a type conversion.
				return true
			}
			var args []ast.Expr
			switch sel.Sel.Name {
			case "T", "HTML":
				if len(call.Args) > 0 {
					args = call.Args[:1]
				}
			case "N":
				if len(call.Args) > 2 {
					args = call.Args[1:3]
				}
			case "Catalog":
				args = call.Args
			}
			for _, a := range args {
				s, ok := stringValue(a, consts)
				if !ok {
					t.Errorf("%s: the text of %s is not a string literal or a constant", fset.Position(a.Pos()), sel.Sel.Name)
					continue
				}
				out[s] = fset.Position(a.Pos()).String()
			}
			return true
		})
	}
	return out
}

// stringValue returns the value of a string literal, of a constant in
// consts, or of a concatenation of these.
func stringValue(e ast.Expr, consts map[string]string) (string, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind == token.STRING {
			s, err := strconv.Unquote(v.Value)
			return s, err == nil
		}
	case *ast.Ident:
		s, ok := consts[v.Name]
		return s, ok
	case *ast.BinaryExpr:
		if v.Op == token.ADD {
			a, ok1 := stringValue(v.X, consts)
			b, ok2 := stringValue(v.Y, consts)
			return a + b, ok1 && ok2
		}
	}
	return "", false
}

// Each text has a translation in each catalog, and each translation has
// a text in the code.
func TestTranslationsComplete(t *testing.T) {
	used := templateTexts(t)
	for k, v := range goTexts(t) {
		used[k] = v
	}
	if len(used) < 10 {
		t.Fatalf("found only %d texts", len(used))
	}
	for _, l := range i18n.UILanguages() {
		if l.Code == i18n.Default {
			continue
		}
		cat := i18n.Texts(l.Code)
		var missing []string
		for k, where := range used {
			if _, ok := cat[k]; !ok {
				missing = append(missing, where+": "+strconv.Quote(k))
			}
		}
		slices.Sort(missing)
		for _, m := range missing {
			t.Errorf("%s: no translation: %s", l.Code, m)
		}
		for k := range cat {
			if _, ok := used[k]; !ok {
				t.Errorf("%s: the catalog has a text that the code does not use: %q", l.Code, k)
			}
		}
	}
}

// Patterns for TestTemplatesTranslated.
var (
	// actionRE finds the template actions and comments.
	actionRE = regexp.MustCompile(`(?s)\{\{.*?\}\}`)
	// textRE finds the text between two tags.
	textRE = regexp.MustCompile(`>([^<>]*)<`)
	// attrRE finds the attributes with a text for the user.
	attrRE = regexp.MustCompile(`\b(title|aria-label|placeholder|alt|value)="([^"]*)"`)
	// stringLitRE finds a string literal in an action.
	stringLitRE = regexp.MustCompile("\"(?:[^\"\\\\]|\\\\.)*\"|`[^`]*`")
	// upperRE matches a string that starts with an upper-case letter.
	upperRE = regexp.MustCompile(`^\p{Lu}`)
	// letterRE finds a letter.
	letterRE = regexp.MustCompile(`\pL`)
	// styleRE finds the style and script elements and the SVG symbols.
	styleRE = regexp.MustCompile(`(?s)<(style|script|svg)\b.*?</(style|script|svg)>`)
)

// untranslatedOK are texts in the templates that are the same in all
// languages: names and technical words.
var untranslatedOK = map[string]bool{
	"Skriva": true, "OpenID Connect": true, "OIDC": true, "MiB": true, "KiB": true,
	"·": true, "· Skriva": true, "›": true, "/": true, "×": true, "…": true, "on": true,
}

// No template has a text for the user outside the template functions.
func TestTemplatesTranslated(t *testing.T) {
	err := fs.WalkDir(templateFS, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := templateFS.ReadFile(path)
		if err != nil {
			return err
		}
		src := styleRE.ReplaceAllString(string(data), "")
		// A string in an action that is not a text of t, tn or thtml
		// must not look like a text for the user: it must not start with
		// an upper-case letter or contain a space.
		for _, action := range actionRE.FindAllString(src, -1) {
			if strings.HasPrefix(action, "{{/*") {
				continue
			}
			rest := templateTextRE.ReplaceAllString(action, "")
			for _, lit := range stringLitRE.FindAllString(rest, -1) {
				v, err := strconv.Unquote(lit)
				if err != nil {
					continue
				}
				if untranslatedOK[strings.TrimSpace(v)] {
					continue
				}
				if letterRE.MatchString(v) && (strings.ContainsAny(v, " ") || upperRE.MatchString(v)) {
					t.Errorf("%s: untranslated string in an action: %q", path, v)
				}
			}
		}
		src = actionRE.ReplaceAllString(src, "")
		check := func(kind, s string) {
			s = strings.TrimSpace(s)
			if s == "" || untranslatedOK[s] || !letterRE.MatchString(s) {
				return
			}
			t.Errorf("%s: untranslated %s: %q", path, kind, s)
		}
		for _, m := range textRE.FindAllStringSubmatch(src, -1) {
			check("text", m[1])
		}
		for _, m := range attrRE.FindAllStringSubmatch(src, -1) {
			if m[1] == "value" {
				// A value is often a code, not a text.
				continue
			}
			check(m[1], m[2])
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// The catalog files are formatted, so that a diff shows one change in
// one line.
func TestCatalogFiles(t *testing.T) {
	files, err := filepath.Glob("../i18n/locales/*/*.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("catalog files: %v, %v", files, err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(string(data), "{\n") {
			t.Errorf("%s: write one text in each line", f)
		}
	}
}

// trRE finds the texts of the calls of tr in the editor script.
var trRE = regexp.MustCompile(`\btr\('((?:[^'\\]|\\.)*)'`)

// editorTexts gives each text that the editor script translates.
func TestEditorTexts(t *testing.T) {
	src, err := os.ReadFile("../../web/editor/src/editor.js")
	if err != nil {
		t.Fatal(err)
	}
	var texts map[string]string
	if err := json.Unmarshal([]byte(editorTexts(i18n.For("en"))), &texts); err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, m := range trRE.FindAllStringSubmatch(string(src), -1) {
		found[m[1]] = true
		if _, ok := texts[m[1]]; !ok {
			t.Errorf("editorTexts does not have %q", m[1])
		}
	}
	for k := range texts {
		if !found[k] {
			t.Errorf("the script does not use %q", k)
		}
	}
}

// permissionText has a text for each permission, the same as the
// description in English.
func TestPermissionTexts(t *testing.T) {
	for _, p := range rbac.All {
		if got := permissionText(i18n.For("en"), p); got != p.Description() {
			t.Errorf("permissionText(%s) = %q, want %q", p, got, p.Description())
		}
	}
}
