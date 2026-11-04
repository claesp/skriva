package doc

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

var links = Links{Wiki: "/t/s/w/", Files: "/t/s/files/", Doc: "/t/s/d/"}

func mustParse(t *testing.T, src string) *Node {
	t.Helper()
	n, err := Parse([]byte(src), nil)
	if err != nil {
		t.Fatalf("Parse(%s): %v", src, err)
	}
	return n
}

func TestParseRemovesItemsThatAreNotPermitted(t *testing.T) {
	src := `{"type":"doc","content":[
		{"type":"paragraph","attrs":{"textAlign":"center","style":"color:red","onclick":"x()"},"content":[
			{"type":"text","text":"a","marks":[{"type":"bold"},{"type":"textStyle","attrs":{"color":"red"}}]},
			{"type":"text","text":"b","marks":[{"type":"link","attrs":{"href":"javascript:alert(1)"}}]},
			{"type":"text","text":"c","marks":[{"type":"link","attrs":{"href":"https://example.com","target":"_blank"}}]}
		]},
		{"type":"script","content":[{"type":"text","text":"bad"}]},
		{"type":"image","attrs":{"src":"data:image/png;base64,AAAA"}},
		{"type":"image","attrs":{"src":"http://example.com/a.png"}},
		{"type":"image","attrs":{"src":"file:0123456789abcd","alt":"x","width":5}},
		{"type":"heading","attrs":{"level":9,"textAlign":"justify"},"content":[{"type":"text","text":"H"}]},
		{"type":"table","attrs":{"style":"width:1px"},"content":[{"type":"tableRow","content":[
			{"type":"tableCell","attrs":{"colspan":2,"rowspan":1,"colwidth":[100],"align":"left"}}]}]}
	]}`
	got := string(mustParse(t, src).JSON())
	want := `{"type":"doc","content":[` +
		`{"type":"paragraph","attrs":{"textAlign":"center"},"content":[` +
		`{"type":"text","marks":[{"type":"bold"}],"text":"a"},` +
		`{"type":"text","text":"b"},` +
		`{"type":"text","marks":[{"type":"link","attrs":{"href":"https://example.com"}}],"text":"c"}]},` +
		`{"type":"image","attrs":{"alt":"x","src":"file:0123456789abcd"}},` +
		`{"type":"heading","attrs":{"level":8}},` +
		`{"type":"table","content":[{"type":"tableRow","content":[{"type":"tableCell","attrs":{"colspan":2}}]}]}]}`
	// The heading keeps its text.
	want = strings.Replace(want, `{"type":"heading","attrs":{"level":8}}`,
		`{"type":"heading","attrs":{"level":8},"content":[{"type":"text","text":"H"}]}`, 1)
	if got != want {
		t.Fatalf("Parse:\n got %s\nwant %s", got, want)
	}
}

func TestParseRemovesEmptyParagraphsAtTheEnd(t *testing.T) {
	got := string(mustParse(t, `{"type":"doc","content":[{"type":"paragraph"},{"type":"horizontalRule"},{"type":"paragraph"},{"type":"paragraph"}]}`).JSON())
	if want := `{"type":"doc","content":[{"type":"paragraph"},{"type":"horizontalRule"}]}`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestParseErrors(t *testing.T) {
	for _, src := range []string{`{`, `[]`, `{"type":"paragraph"}`, "{\"type\":\"doc\",\"x\":\"\xff\"}"} {
		if _, err := Parse([]byte(src), nil); !errors.Is(err, ErrSyntax) {
			t.Errorf("Parse(%q) = %v, want ErrSyntax", src, err)
		}
	}
	deep := strings.Repeat(`{"type":"blockquote","content":[`, MaxDepth+1) + strings.Repeat(`]}`, MaxDepth+1)
	if _, err := Parse([]byte(`{"type":"doc","content":[`+deep+`]}`), nil); !errors.Is(err, ErrTooLarge) {
		t.Errorf("deep document: %v, want ErrTooLarge", err)
	}
	if n, err := Parse(nil, nil); err != nil || string(n.JSON()) != EmptyJSON {
		t.Errorf("empty data: %v, %v", n, err)
	}
}

func TestParseImageSrc(t *testing.T) {
	src := `{"type":"doc","content":[{"type":"image","attrs":{"src":"/t/s/files/0123456789abcd"}}]}`
	n, err := Parse([]byte(src), func(s string) string {
		if id, ok := strings.CutPrefix(s, "/t/s/files/"); ok {
			return FilePrefix + id
		}
		return s
	})
	if err != nil {
		t.Fatal(err)
	}
	if f := n.Files(); len(f) != 1 || f[0] != "0123456789abcd" {
		t.Fatalf("Files() = %v", f)
	}
	n.MapImages(links.Src)
	if !strings.Contains(string(n.JSON()), `"src":"/t/s/files/0123456789abcd"`) {
		t.Fatalf("MapImages: %s", n.JSON())
	}
}

func TestLinks(t *testing.T) {
	for _, href := range []string{"https://a.example/x", "http://a.example", "mailto:a@example.com", "/t/s/d/x", "#part", "wiki:Start", "doc:0123456789abcd", "doc:0123456789abcd#Part"} {
		if _, ok := cleanHref(href); !ok {
			t.Errorf("cleanHref(%q) refused", href)
		}
	}
	for _, href := range []string{"javascript:x", "JavaScript:x", "data:text/html,x", "//evil.example", "/\\evil", "vbscript:x", "wiki:", "wiki:#x", "https://", "a\x00b", "doc:", "doc:short", "doc:0123456789ABCD", "doc:0123456789abcd/x"} {
		if _, ok := cleanHref(href); ok {
			t.Errorf("cleanHref(%q) permitted", href)
		}
	}
	if got := links.Href("wiki:Getting Started#Über uns"); got != "/t/s/w/getting-started#uber-uns" {
		t.Errorf("Href = %q", got)
	}
	if got := links.Href("doc:0123456789abcd#Über uns"); got != "/t/s/d/0123456789abcd#uber-uns" {
		t.Errorf("Href = %q", got)
	}
	n := mustParse(t, `{"type":"doc","content":[{"type":"paragraph","content":[
		{"type":"text","text":"a","marks":[{"type":"link","attrs":{"href":"doc:0123456789abcd"}}]},
		{"type":"text","text":"b","marks":[{"type":"link","attrs":{"href":"doc:0123456789abcd#x"}}]},
		{"type":"text","text":"c","marks":[{"type":"link","attrs":{"href":"wiki:Getting Started#x"}}]},
		{"type":"text","text":"d","marks":[{"type":"link","attrs":{"href":"wiki:getting started"}}]},
		{"type":"text","text":"e","marks":[{"type":"link","attrs":{"href":"https://a.example"}}]}]}]}`)
	docs, wiki := n.Links()
	if len(docs) != 1 || docs[0] != "0123456789abcd" || len(wiki) != 1 || wiki[0] != "getting-started" {
		t.Errorf("Links() = %v, %v", docs, wiki)
	}
	if md := n.Markdown(links); !strings.Contains(md, "[a](/t/s/d/0123456789abcd)") {
		t.Errorf("Markdown = %q", md)
	}
}

const sample = `{"type":"doc","content":[
	{"type":"heading","attrs":{"level":1},"content":[{"type":"text","text":"Intro"}]},
	{"type":"paragraph","attrs":{"textAlign":"right"},"content":[
		{"type":"text","text":"Plain "},
		{"type":"text","text":"bold","marks":[{"type":"bold"}]},
		{"type":"text","text":" and "},
		{"type":"text","text":"Start","marks":[{"type":"link","attrs":{"href":"wiki:Start"}}]},
		{"type":"text","text":" <x> "},
		{"type":"text","text":"u","marks":[{"type":"underline"}]}
	]},
	{"type":"bulletList","content":[
		{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"one"}]},
			{"type":"orderedList","attrs":{"start":3},"content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"two"}]}]}]}]}
	]},
	{"type":"taskList","content":[{"type":"taskItem","attrs":{"checked":true},"content":[{"type":"paragraph","content":[{"type":"text","text":"done"}]}]}]},
	{"type":"codeBlock","attrs":{"language":"go"},"content":[{"type":"text","text":"a < b"}]},
	{"type":"image","attrs":{"src":"file:0123456789abcd","alt":"Logo"}},
	{"type":"table","content":[
		{"type":"tableRow","content":[{"type":"tableHeader","attrs":{"colspan":2},"content":[{"type":"paragraph","content":[{"type":"text","text":"H|1"}]}]}]},
		{"type":"tableRow","content":[{"type":"tableCell","content":[{"type":"paragraph","content":[{"type":"text","text":"a"}]}]},{"type":"tableCell","content":[{"type":"paragraph","content":[{"type":"text","text":"b"}]}]}]}
	]},
	{"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"Intro"}]}
]}`

func TestHTML(t *testing.T) {
	got := string(mustParse(t, sample).HTML(links))
	for _, want := range []string{
		`<h2 id="intro" class="level-1">Intro</h2>`,
		`<p class="align-right">Plain <strong>bold</strong> and <a href="/t/s/w/start" rel="nofollow noreferrer">Start</a> &lt;x&gt; <u>u</u></p>`,
		`<ul><li><p>one</p><ol start="3"><li><p>two</p></li></ol></li></ul>`,
		`<ul class="tasks"><li class="task done"><input type="checkbox" disabled="" checked=""><p>done</p></li></ul>`,
		`<pre><code class="language-go">a &lt; b</code></pre>`,
		`<img src="/t/s/files/0123456789abcd" alt="Logo" loading="lazy">`,
		`<div class="table"><table><tbody><tr><th colspan="2"><p>H|1</p></th></tr>`,
		`<h3 id="intro-1" class="level-2">Intro</h3>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("HTML does not contain\n%s\nin\n%s", want, got)
		}
	}
}

// TestHeadingLevels checks the HTML and the Markdown of all heading
// levels. Levels 6 to 8 have no HTML element.
func TestHeadingLevels(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"type":"doc","content":[`)
	for level := 1; level <= 8; level++ {
		if level > 1 {
			b.WriteString(",")
		}
		align := ""
		if level == 7 {
			align = `,"textAlign":"center"`
		}
		fmt.Fprintf(&b, `{"type":"heading","attrs":{"level":%d%s},"content":[{"type":"text","text":"L%d"}]}`, level, align, level)
	}
	b.WriteString("]}")
	d := mustParse(t, b.String())
	got := string(d.HTML(links))
	for _, want := range []string{
		`<h2 id="l1" class="level-1">L1</h2>`,
		`<h6 id="l5" class="level-5">L5</h6>`,
		`<p id="l6" class="level-6" role="heading" aria-level="7">L6</p>`,
		`<p id="l7" class="level-7 align-center" role="heading" aria-level="8">L7</p>`,
		`<p id="l8" class="level-8" role="heading" aria-level="9">L8</p>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("HTML does not contain\n%s\nin\n%s", want, got)
		}
	}
	md := d.Markdown(links)
	for _, want := range []string{"# L1\n", "\n###### L6\n", "\n###### L8\n"} {
		if !strings.Contains(md, want) {
			t.Errorf("Markdown does not contain %q in\n%s", want, md)
		}
	}
}

func TestMarkdown(t *testing.T) {
	got := mustParse(t, sample).Markdown(links)
	want := "# Intro\n" +
		"\n" +
		"Plain **bold** and [[Start]] \\<x\\> u\n" +
		"\n" +
		"- one\n" +
		"  3. two\n" +
		"\n" +
		"- [x] done\n" +
		"\n" +
		"```go\na < b\n```\n" +
		"\n" +
		"![Logo](/t/s/files/0123456789abcd)\n" +
		"\n" +
		"| H\\|1 |  |\n" +
		"| --- | --- |\n" +
		"| a | b |\n" +
		"\n" +
		"## Intro\n"
	if got != want {
		t.Fatalf("Markdown:\n%s\nwant:\n%s", got, want)
	}
}

func TestPlainText(t *testing.T) {
	got := mustParse(t, sample).PlainText()
	want := "Intro\nPlain bold and Start <x> u\none\ntwo\ndone\na < b\nLogo\nH|1\na\nb\nIntro"
	if got != want {
		t.Fatalf("PlainText:\n%q\nwant\n%q", got, want)
	}
}

// unsafeAttr finds event handlers and script URLs in HTML attributes.
var unsafeAttr = regexp.MustCompile(`<[^>]*(\son[a-z]+=|(href|src)="\s*(javascript|data|vbscript):)`)

func FuzzParse(f *testing.F) {
	f.Add([]byte(sample))
	f.Add([]byte(`{"type":"doc","content":[{"type":"text","text":"<script>"}]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		n, err := Parse(data, nil)
		if err != nil {
			return
		}
		h := strings.ToLower(string(n.HTML(links)))
		if strings.Contains(h, "<script") || unsafeAttr.MatchString(h) {
			t.Fatalf("unsafe HTML: %s", h)
		}
		_ = n.Markdown(links)
		_ = n.PlainText()
		// The result of Parse is stable.
		again, err := Parse(n.JSON(), nil)
		if err != nil || string(again.JSON()) != string(n.JSON()) {
			t.Fatalf("Parse is not stable: %s / %v", n.JSON(), err)
		}
	})
}

func TestDiagrams(t *testing.T) {
	d := mustParse(t, `{"type":"doc","content":[
	{"type":"codeBlock","attrs":{"language":"mermaid"},"content":[{"type":"text","text":"graph TD\n  accTitle: Flow\n  A-->B"}]},
	{"type":"codeBlock","attrs":{"language":"go"},"content":[{"type":"text","text":"x"}]},
	{"type":"codeBlock","attrs":{"language":"mermaid"},"content":[{"type":"text","text":"graph TD; A-->"}]}]}`)
	if n := len(d.Diagrams()); n != 2 {
		t.Fatalf("Diagrams = %d, want 2", n)
	}
	// Without the option, a diagram is a code block.
	if got := string(d.HTML(links)); !strings.Contains(got, `<code class="language-mermaid">graph TD`) {
		t.Fatalf("HTML without diagrams = %s", got)
	}
	got := string(d.HTMLWith(links, HTMLOptions{Diagram: func(src string) (string, error) {
		if strings.HasSuffix(src, "-->") {
			return "", errors.New("the diagram has an error: <b>bad</b>")
		}
		return "/t/s/diagram/abc/123", nil
	}}))
	for _, want := range []string{
		`<figure class="diagram"><img src="/t/s/diagram/abc/123" alt="Flow"></figure>`,
		`<pre><code class="language-go">x</code></pre>`,
		`<figure class="diagram-error"><pre><code class="language-mermaid">graph TD; A--&gt;</code></pre>` +
			`<figcaption>The diagram has an error: &lt;b&gt;bad&lt;/b&gt;.</figcaption></figure>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("HTML does not contain %s:\n%s", want, got)
		}
	}
	// The policy removes a data URI from a page, but not from an export.
	data := func(string) (string, error) { return "data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=", nil }
	if got := string(d.HTMLWith(links, HTMLOptions{Diagram: data})); strings.Contains(got, "data:") {
		t.Errorf("page HTML contains a data URI: %s", got)
	}
	if got := string(d.HTMLWith(links, HTMLOptions{Diagram: data, DataImages: true})); !strings.Contains(got, `src="data:image/svg+xml;base64,`) {
		t.Errorf("export HTML has no data URI: %s", got)
	}
}

func TestDiagramAlt(t *testing.T) {
	for src, want := range map[string]string{
		"graph TD\nA-->B": "Diagram",
		"graph TD\n  accTitle: Steps of a review \n  accDescr: The author asks a reviewer.\nA-->B": "Steps of a review. The author asks a reviewer",
	} {
		if got := DiagramAlt(src); got != want {
			t.Errorf("DiagramAlt(%q) = %q, want %q", src, got, want)
		}
	}
}
