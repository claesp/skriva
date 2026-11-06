package web

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/claesp/skriva/internal/store"
)

// symbolRE finds the icons and their content in static/icons.svg.
var symbolRE = regexp.MustCompile(`<symbol id="i_([a-z0-9-]+)" viewBox="0 0 24 24">(.*?)</symbol>`)

// Each icon has one meaning. Two icons do not look the same, and each
// icon has a use.
func TestIcons(t *testing.T) {
	data, err := staticFS.ReadFile("static/icons.svg")
	if err != nil {
		t.Fatal(err)
	}
	var tmpl strings.Builder
	files, _ := fs.Glob(templateFS, "templates/*.html")
	for _, f := range files {
		b, err := templateFS.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		tmpl.Write(b)
	}
	// The tree shows the kind of a node as an icon.
	kinds := map[string]bool{store.KindFolder: true, store.KindDocument: true}

	shapes := map[string]string{}
	matches := symbolRE.FindAllSubmatch(data, -1)
	names, _ := iconNames()
	if len(matches) == 0 || len(matches) != len(names) {
		t.Fatalf("found %d icons with a 24x24 view box, %d icons in all", len(matches), len(names))
	}
	for _, m := range matches {
		name, shape := string(m[1]), string(m[2])
		if other, ok := shapes[shape]; ok {
			t.Errorf("the icons %q and %q look the same", other, name)
		}
		shapes[shape] = name
		if !kinds[name] && !strings.Contains(tmpl.String(), `"`+name+`"`) {
			t.Errorf("no template uses the icon %q", name)
		}
	}
}
