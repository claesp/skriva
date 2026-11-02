// Package export converts a document to a file that the user can
// download. Each format is a Format value. Formats is the list of all
// formats.
//
// An exported file contains the title and the information
// classification of the document. Thus the classification stays with the
// text outside of Skriva.
package export

import (
	"io"
	"time"

	"github.com/clpe/skriva/internal/doc"
)

// Document is the data of a document for an export.
type Document struct {
	Title string
	// Classification is the name of the information classification, or
	// "" if the document has none.
	Classification string
	Version        int
	UpdatedAt      time.Time
	// URL is the absolute URL of the document in Skriva.
	URL string
	// Body is the text. It must be the result of doc.Parse.
	Body *doc.Node
	// Links makes the URLs of links and images. The URLs must be
	// absolute, because the file is not on the server.
	Links doc.Links
}

// Format is a file format for an export.
type Format struct {
	// Name identifies the format in URLs. It contains only a-z.
	Name string
	// Label is the name of the format for the user.
	Label string
	// Description tells the user what the file contains.
	Description string
	// Extension is the file name extension, with the dot.
	Extension string
	// ContentType is the media type of the file.
	ContentType string
	// Text is true if a browser can show the file as plain text.
	Text bool
	// Write writes the file of d to w.
	Write func(w io.Writer, d *Document) error
}

// Formats are all export formats, in the sequence of the export page.
var Formats = []Format{markdownFormat}

// Lookup returns the format with the name.
func Lookup(name string) (Format, bool) {
	for _, f := range Formats {
		if f.Name == name {
			return f, true
		}
	}
	return Format{}, false
}
