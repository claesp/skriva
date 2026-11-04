// Package export converts a document to a file that the user can
// download. Each format is a Format value. NewFormats returns the list of
// all formats.
//
// An exported file contains the title and the information
// classification of the document. Thus the classification stays with the
// text outside of Skriva.
package export

import (
	"context"
	"io"
	"time"

	"github.com/claesp/skriva/internal/doc"
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
	// Body is the text. It must be the result of doc.Parse. A format can
	// change it, thus use a Document for one export only.
	Body *doc.Node
	// Links makes the URLs of links and images. The URLs must be
	// absolute, because the file is not on the server.
	Links doc.Links
	// Image returns an uploaded image with the file ID, for the formats
	// that contain the images. It returns nil if the image does not
	// exist or the user cannot read it. If Image is nil, the images keep
	// their URLs.
	Image func(id string) (*Image, error)
	// Diagram returns the SVG image of a Mermaid diagram. The error
	// message is for the user. If Diagram is nil, a diagram is a code
	// block. The Markdown format always keeps the text of a diagram.
	Diagram func(source string) ([]byte, error)
}

// Image is an uploaded image.
type Image struct {
	ContentType string
	Data        []byte
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
	// Write writes the file of d to w. It is nil if the server cannot
	// make the format.
	Write func(ctx context.Context, w io.Writer, d *Document) error
	// Unavailable tells the user why the server cannot make the format.
	Unavailable string
}

// Available reports whether the server can make the format.
func (f Format) Available() bool { return f.Write != nil }

// Formats is a list of formats.
type Formats []Format

// NewFormats returns all export formats, in the sequence of the export
// page. The PDF format needs the Typst program. If pdf is nil, the PDF
// format is in the list, but it is not available.
func NewFormats(pdf *PDFOptions) Formats {
	return Formats{markdownFormat, htmlFormat, pdfFormat(pdf)}
}

// Lookup returns the format with the name.
func (fs Formats) Lookup(name string) (Format, bool) {
	for _, f := range fs {
		if f.Name == name {
			return f, true
		}
	}
	return Format{}, false
}
