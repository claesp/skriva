package export

import (
	"bytes"
	"context"
	_ "embed" // For the template.
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/clpe/skriva/internal/doc"
)

// PDFOptions configure the PDF format. The format uses the Typst program
// (https://typst.app) to make the file.
type PDFOptions struct {
	// Typst is the path of the Typst program.
	Typst string
	// TempDir is the folder for the temporary files, or "" for the
	// default folder of the system.
	TempDir string
	// Timeout is the longest time for one file. Zero means
	// DefaultPDFTimeout.
	Timeout time.Duration
	// MaxJobs is the largest number of Typst processes at the same time.
	// Zero means DefaultPDFJobs.
	MaxJobs int
}

// Default values of PDFOptions. The timeout is shorter than the timeout
// of a request (30 seconds).
const (
	DefaultPDFTimeout = 25 * time.Second
	DefaultPDFJobs    = 2
)

// Limits of the PDF format.
const (
	// maxPDFBytes is the largest size of a PDF file.
	maxPDFBytes = 256 << 20
	// maxStderr is the largest part of the error output of Typst that
	// the error message contains.
	maxStderr = 4 << 10
)

// pdfTemplate is the Typst file that makes the page from doc.json.
//
//go:embed pdf.typ
var pdfTemplate []byte

// imageExtensions are the image types that Typst can show, with the file
// name extensions.
var imageExtensions = map[string]string{
	"image/gif": ".gif", "image/jpeg": ".jpg", "image/png": ".png", "image/svg+xml": ".svg", "image/webp": ".webp",
}

func pdfFormat(o *PDFOptions) Format {
	f := Format{
		Name:  "pdf",
		Label: "PDF",
		Description: "A document for print, with A4 pages and the images. Each page shows " +
			"the classification. The metadata contains the title and the classification.",
		Extension:   ".pdf",
		ContentType: "application/pdf",
		Unavailable: "The server does not have the Typst program, thus it cannot make PDF files.",
	}
	if o != nil {
		r := &pdfRunner{opts: *o}
		if r.opts.Timeout <= 0 {
			r.opts.Timeout = DefaultPDFTimeout
		}
		if r.opts.MaxJobs <= 0 {
			r.opts.MaxJobs = DefaultPDFJobs
		}
		r.jobs = make(chan struct{}, r.opts.MaxJobs)
		f.Write = r.write
	}
	return f
}

// pdfRunner runs Typst. The channel jobs limits the number of processes,
// because each process can use much memory and processor time.
type pdfRunner struct {
	opts PDFOptions
	jobs chan struct{}
}

// pdfData is the content of doc.json. See pdf.typ.
type pdfData struct {
	Title          string    `json:"title"`
	Classification string    `json:"classification"`
	Version        int       `json:"version"`
	Updated        string    `json:"updated"`
	URL            string    `json:"url"`
	Body           *doc.Node `json:"body"`
}

// write makes the PDF file. If Typst cannot read an image, it fails. Then
// write tries again without the images, because a document with
// placeholders is better than no document.
func (r *pdfRunner) write(ctx context.Context, w io.Writer, d *Document) error {
	select {
	case r.jobs <- struct{}{}:
		defer func() { <-r.jobs }()
	case <-ctx.Done():
		return ctx.Err()
	}
	ctx, cancel := context.WithTimeout(ctx, r.opts.Timeout)
	defer cancel()

	dir, err := os.MkdirTemp(r.opts.TempDir, "skriva-pdf-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	body := d.Body
	if body == nil {
		body = doc.Empty()
	}
	body.MapLinks(d.Links.Href)
	images, err := writeImages(dir, body, d.Image)
	if err != nil {
		return err
	}
	data := pdfData{
		Title: d.Title, Classification: d.Classification, Version: d.Version,
		Updated: d.UpdatedAt.UTC().Format(time.RFC3339), URL: d.URL, Body: body,
	}
	pdf, err := r.compile(ctx, dir, &data, d.UpdatedAt)
	if err != nil && images > 0 && ctx.Err() == nil {
		body.MapImages(func(string) string { return "" })
		pdf, err = r.compile(ctx, dir, &data, d.UpdatedAt)
	}
	if err != nil {
		return err
	}
	_, err = w.Write(pdf)
	return err
}

// writeImages writes the uploaded images of body into the folder dir, and
// changes their sources to the file names. Other images get the source
// "", and the page shows their descriptions. The function returns the
// number of images in the folder.
func writeImages(dir string, body *doc.Node, get func(id string) (*Image, error)) (int, error) {
	if err := os.Mkdir(filepath.Join(dir, "img"), 0o700); err != nil {
		return 0, err
	}
	names := map[string]string{}
	total := 0
	var err error
	body.MapImages(func(src string) string {
		id, ok := doc.FileID(src)
		if !ok || get == nil || err != nil {
			return ""
		}
		if name, ok := names[id]; ok {
			return name
		}
		names[id] = ""
		var img *Image
		if img, err = get(id); err != nil || img == nil {
			return ""
		}
		ext, ok := imageExtensions[img.ContentType]
		if !ok || total+len(img.Data) > MaxEmbeddedBytes {
			return ""
		}
		name := "img/" + strconv.Itoa(len(names)) + ext
		if err = os.WriteFile(filepath.Join(dir, name), img.Data, 0o600); err != nil {
			return ""
		}
		total += len(img.Data)
		names[id] = name
		return name
	})
	n := 0
	for _, name := range names {
		if name != "" {
			n++
		}
	}
	return n, err
}

// compile writes doc.json and main.typ, and runs Typst. Typst can read
// only the files in dir (--root). It uses only the fonts in the program
// (--ignore-system-fonts). The package folders are in dir and empty, thus
// Typst cannot load packages. The template imports no packages, and the
// data cannot contain code.
func (r *pdfRunner) compile(ctx context.Context, dir string, data *pdfData, created time.Time) ([]byte, error) {
	js, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "doc.json"), js, 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "main.typ"), pdfTemplate, 0o600); err != nil {
		return nil, err
	}
	out := filepath.Join(dir, "out.pdf")
	pkg := filepath.Join(dir, "packages")
	// #nosec G204 -- the program comes from the configuration, and the
	// arguments are paths in the temporary folder.
	cmd := exec.CommandContext(ctx, r.opts.Typst, "compile",
		"--root", dir,
		"--ignore-system-fonts",
		"--package-path", pkg,
		"--package-cache-path", pkg,
		"--jobs", "1",
		"--creation-timestamp", strconv.FormatInt(max(created.Unix(), 0), 10),
		filepath.Join(dir, "main.typ"), out)
	cmd.Dir = dir
	cmd.Env = []string{"HOME=" + dir}
	cmd.WaitDelay = time.Second
	var stderr limitedBuffer
	stderr.max = maxStderr
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("typst: %w", ctx.Err())
		}
		return nil, fmt.Errorf("typst: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	f, err := os.Open(out)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	pdf, err := io.ReadAll(io.LimitReader(f, maxPDFBytes+1))
	if err != nil {
		return nil, err
	}
	if len(pdf) > maxPDFBytes {
		return nil, errors.New("typst: the PDF file is too large")
	}
	return pdf, nil
}

// limitedBuffer keeps the first max bytes that are written to it.
type limitedBuffer struct {
	bytes.Buffer
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.Len(); room > 0 {
		b.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}
