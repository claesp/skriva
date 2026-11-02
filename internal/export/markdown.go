package export

import (
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/clpe/skriva/internal/doc"
)

var markdownFormat = Format{
	Name:  "markdown",
	Label: "Markdown",
	Description: "Text with GitHub Flavored Markdown. A YAML front matter block " +
		"gives the title, the classification and the version. Markdown does not have " +
		"all formats of a document, for example the alignment and the underline.",
	Extension:   ".md",
	ContentType: "text/markdown; charset=utf-8",
	Text:        true,
	Write:       writeMarkdown,
}

// writeMarkdown writes the document in Markdown. The file starts with a
// YAML front matter block. The title is the first heading, thus the
// headings of the text go down one level. Wiki links become normal
// links, because other programs do not know the [[Title]] syntax.
func writeMarkdown(w io.Writer, d *Document) error {
	var b strings.Builder
	b.WriteString("---\n")
	field(&b, "title", d.Title)
	if d.Classification != "" {
		field(&b, "classification", d.Classification)
	}
	b.WriteString("version: " + strconv.Itoa(d.Version) + "\n")
	field(&b, "updated", d.UpdatedAt.UTC().Format(time.RFC3339))
	if d.URL != "" {
		field(&b, "source", d.URL)
	}
	b.WriteString("---\n\n")
	b.WriteString("# " + doc.EscapeMarkdown(d.Title) + "\n\n")
	if d.Classification != "" {
		b.WriteString("_This document is classified as " + doc.EscapeMarkdown(strings.ToUpper(d.Classification)) + "._\n\n")
	}
	if d.Body != nil && len(d.Body.Content) > 0 {
		b.WriteString(d.Body.MarkdownWith(d.Links, doc.MarkdownOptions{HeadingOffset: 1}))
	}
	_, err := io.WriteString(w, strings.TrimRight(b.String(), "\n")+"\n")
	return err
}

// field writes a YAML field with a quoted value. A JSON string is also a
// valid YAML string in double quotes, thus no character can change the
// structure of the block.
func field(b *strings.Builder, key, value string) {
	q, _ := json.Marshal(value) // A string always gives valid JSON.
	b.WriteString(key + ": " + string(q) + "\n")
}
