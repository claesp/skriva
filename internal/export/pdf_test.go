package export

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/claesp/skriva/internal/doc"
)

// pdfOptions returns the options for a test with the real Typst program.
// SKRIVA_TEST_TYPST is the path of the program, and
// SKRIVA_TEST_TYPST_TMP is an optional folder for the temporary files.
// Without SKRIVA_TEST_TYPST, the test is skipped.
func pdfOptions(t *testing.T) *PDFOptions {
	t.Helper()
	path := os.Getenv("SKRIVA_TEST_TYPST")
	if path == "" {
		t.Skip("SKRIVA_TEST_TYPST is not set")
	}
	tmp := os.Getenv("SKRIVA_TEST_TYPST_TMP")
	if tmp != "" {
		var err error
		if tmp, err = os.MkdirTemp(tmp, "test-"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(tmp) })
	}
	return &PDFOptions{Typst: path, TempDir: tmp}
}

// TestWriteImages checks the image files without Typst.
func TestWriteImages(t *testing.T) {
	const a, b, c = "aaaaaaaaaaaaaa", "bbbbbbbbbbbbbb", "cccccccccccccc"
	body, err := doc.Parse([]byte(`{"type":"doc","content":[`+
		`{"type":"image","attrs":{"src":"file:`+a+`"}},{"type":"image","attrs":{"src":"file:`+a+`"}},`+
		`{"type":"image","attrs":{"src":"file:`+b+`"}},{"type":"image","attrs":{"src":"file:`+c+`"}},`+
		`{"type":"image","attrs":{"src":"https://img.example/x.png"}}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	images := map[string]*Image{
		a: {ContentType: "image/svg+xml", Data: []byte("<svg/>")},
		b: {ContentType: "text/plain", Data: []byte("x")},
	}
	n, err := writeImages(dir, body, func(id string) (*Image, error) { return images[id], nil })
	if err != nil || n != 1 {
		t.Fatalf("writeImages = %d, %v", n, err)
	}
	var srcs []string
	for _, c := range body.Content {
		srcs = append(srcs, c.Attrs["src"].(string))
	}
	if got := strings.Join(srcs, ","); got != "img/1.svg,img/1.svg,,," {
		t.Fatalf("sources = %q", got)
	}
	if data, err := os.ReadFile(dir + "/img/1.svg"); err != nil || string(data) != "<svg/>" {
		t.Fatalf("image file = %q, %v", data, err)
	}
}

// fakeDiagram returns a small SVG image, or an error for the text "bad".
func fakeDiagram(source string) ([]byte, error) {
	if source == "bad" {
		return nil, errors.New("the diagram has an error: bad text")
	}
	return []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="80" height="40"><rect width="80" height="40" fill="#ececff"/></svg>`), nil
}

// TestWriteDiagrams checks the diagram files without Typst.
func TestWriteDiagrams(t *testing.T) {
	body, err := doc.Parse([]byte(`{"type":"doc","content":[`+
		`{"type":"codeBlock","attrs":{"language":"mermaid"},"content":[{"type":"text","text":"accTitle: Flow\ngraph TD"}]},`+
		`{"type":"codeBlock","attrs":{"language":"mermaid"},"content":[{"type":"text","text":"bad"}]},`+
		`{"type":"codeBlock","attrs":{"language":"go"},"content":[{"type":"text","text":"x"}]}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Mkdir(dir+"/img", 0o700); err != nil {
		t.Fatal(err)
	}
	n, err := writeDiagrams(dir, body, fakeDiagram)
	if err != nil || n != 1 {
		t.Fatalf("writeDiagrams = %d, %v", n, err)
	}
	a, b, c := body.Content[0].Attrs, body.Content[1].Attrs, body.Content[2].Attrs
	if a["src"] != "img/diagram-1.svg" || a["alt"] != "Flow" || b["error"] != "the diagram has an error: bad text" ||
		b["src"] != nil || c["src"] != nil {
		t.Fatalf("attributes = %v, %v, %v", a, b, c)
	}
	if _, err := os.Stat(dir + "/img/diagram-1.svg"); err != nil {
		t.Fatal(err)
	}
}

func TestPDF(t *testing.T) {
	o := pdfOptions(t)
	f := pdfFormat(o)
	svg, err := os.ReadFile("../web/testdata/flow.drawio.svg")
	if err != nil {
		t.Fatal(err)
	}
	const good, bad = "aaaaaaaaaaaaaa", "bbbbbbbbbbbbbb"
	newDoc := func(images map[string]*Image) *Document {
		body, err := doc.Parse([]byte(`{"type":"doc","content":[`+
			`{"type":"heading","attrs":{"level":1},"content":[{"type":"text","text":"Install #set page(paper: \"a5\")"}]},`+
			`{"type":"paragraph","content":[{"type":"text","text":"Go","marks":[{"type":"link","attrs":{"href":"wiki:Start"}}]}]},`+
			`{"type":"image","attrs":{"src":"file:`+good+`","alt":"Flow"}},`+
			`{"type":"image","attrs":{"src":"file:`+bad+`","alt":"Bad"}},`+
			`{"type":"codeBlock","attrs":{"language":"mermaid"},"content":[{"type":"text","text":"graph TD"}]},`+
			`{"type":"codeBlock","attrs":{"language":"mermaid"},"content":[{"type":"text","text":"bad"}]},`+
			`{"type":"table","content":[{"type":"tableRow","content":[`+
			`{"type":"tableHeader","attrs":{"colspan":2},"content":[{"type":"paragraph","content":[{"type":"text","text":"A"}]}]}]},`+
			`{"type":"tableRow","content":[{"type":"tableCell","content":[]},{"type":"tableCell","content":[]}]}]}]}`), nil)
		if err != nil {
			t.Fatal(err)
		}
		return &Document{
			Title: "Plan $x$ #1", Classification: "Restricted", Version: 2,
			UpdatedAt: time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC),
			Body:      body,
			Links:     doc.Links{Wiki: "https://wiki.example/t/s/w/"},
			Image:     func(id string) (*Image, error) { return images[id], nil },
			Diagram:   fakeDiagram,
		}
	}
	check := func(name string, d *Document) {
		t.Helper()
		var b bytes.Buffer
		if err := f.Write(context.Background(), &b, d); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !bytes.HasPrefix(b.Bytes(), []byte("%PDF-")) || !bytes.Contains(b.Bytes(), []byte("%%EOF")) {
			t.Fatalf("%s: the result is not a PDF file: %.100q", name, b.String())
		}
	}
	check("valid images", newDoc(map[string]*Image{good: {ContentType: "image/svg+xml", Data: svg}}))
	// Typst cannot read the second image. The export then tries again
	// without the images.
	check("bad image", newDoc(map[string]*Image{
		good: {ContentType: "image/svg+xml", Data: svg},
		bad:  {ContentType: "image/png", Data: []byte("not a PNG file")},
	}))
	check("empty document", &Document{Title: "Empty", Version: 1})

	// A row can be wider than the first row, and a rowspan takes columns
	// in the next row. An empty first row does not hide the table.
	cell := func(attrs string) string {
		return `{"type":"tableCell","attrs":{` + attrs + `},"content":[{"type":"paragraph","content":[{"type":"text","text":"x"}]}]}`
	}
	table, err := doc.Parse([]byte(`{"type":"doc","content":[{"type":"table","content":[`+
		`{"type":"tableRow","content":[]},`+
		`{"type":"tableRow","content":[`+cell("")+`]},`+
		`{"type":"tableRow","content":[`+cell(`"rowspan":2`)+`,`+cell(`"colspan":3`)+`]},`+
		`{"type":"tableRow","content":[`+cell("")+`,`+cell("")+`,`+cell("")+`]}]}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	check("table", &Document{Title: "Table", Version: 1, Body: table})

	// A canceled request stops the export.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := f.Write(ctx, &bytes.Buffer{}, newDoc(nil)); err == nil {
		t.Fatal("Write with a canceled context did not fail")
	}
	// The temporary files are removed.
	if o.TempDir != "" {
		if entries, _ := os.ReadDir(o.TempDir); len(entries) != 0 {
			t.Fatalf("temporary folder contains %d items", len(entries))
		}
	}
}
