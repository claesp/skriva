// Package diagram makes SVG images from the source text of Mermaid
// diagrams. It runs the mmdr program (mermaid-rs-renderer), thus the
// browser needs no JavaScript.
//
// The SVG of mmdr is not safe in a page. For example, a "click" line of
// Mermaid makes a javascript: link. Thus show the SVG only as an image
// (an img element, or a file with a Content-Security-Policy that has
// "sandbox"). A browser does not run scripts or follow links in an
// image.
package diagram

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Options configure a Renderer.
type Options struct {
	// Program is the path of the mmdr program.
	Program string
	// Timeout is the longest time for one diagram. Zero means
	// DefaultTimeout.
	Timeout time.Duration
	// MaxJobs is the largest number of mmdr processes at the same time.
	// Zero means DefaultJobs.
	MaxJobs int
	// CacheBytes is the largest size of the cache of images. Zero means
	// DefaultCacheBytes.
	CacheBytes int
	// Config is the configuration of mmdr in JSON format, for example
	// the theme variables of Mermaid. Nil means DefaultConfig. An init
	// directive in a diagram (%%{init: ...}%%) changes these values.
	Config []byte
}

// DefaultConfig gives the images the style of the pages of Skriva:
//   - The font is Inter, the font of the interface. The image does not
//     contain the font. See the package web.
//   - The background color is the color of the page. mmdr also uses it in
//     some shapes, for example the inside of the end state of a state
//     diagram. The labels on lines have the same color, thus the lines
//     do not go through the text. Render removes the rectangle of the
//     background, thus the image is transparent.
var DefaultConfig = []byte(`{"themeVariables": {` +
	`"fontFamily": "Inter Variable, sans-serif", ` +
	`"background": "#fffff8", ` +
	`"edgeLabelBackground": "#fffff8"}}`)

// backgroundRE matches the svg element and a first rectangle. The
// rectangle is the background if it has the size of the view box.
var backgroundRE = regexp.MustCompile(`^<svg[^>]* viewBox="([-0-9.]+) ([-0-9.]+) ([-0-9.]+) ([-0-9.]+)"[^>]*>` +
	`(<rect x="([-0-9.]+)" y="([-0-9.]+)" width="([-0-9.]+)" height="([-0-9.]+)" fill="[^"]*"/>)`)

// removeBackground removes the rectangle of the background from the image
// of mmdr.
func removeBackground(svg []byte) []byte {
	m := backgroundRE.FindSubmatchIndex(svg)
	if m == nil {
		return svg
	}
	group := func(i int) string { return string(svg[m[2*i]:m[2*i+1]]) }
	for i := 1; i <= 4; i++ {
		if group(i) != group(i+5) {
			return svg
		}
	}
	return append(svg[:m[10]:m[10]], svg[m[11]:]...)
}

// Default values of Options.
const (
	DefaultTimeout    = 5 * time.Second
	DefaultJobs       = 2
	DefaultCacheBytes = 32 << 20
)

// Limits of a diagram.
const (
	// MaxSource is the largest size of the source text.
	MaxSource = 32 << 10
	// MaxSVG is the largest size of an image.
	MaxSVG = 4 << 20
	// maxMessage is the largest number of characters in the message of
	// a SourceError.
	maxMessage = 300
)

// ErrUnavailable tells that the server cannot make diagrams now. The
// message is for the user.
var ErrUnavailable = errors.New("the server cannot show the diagram now")

// SourceError is an error in the source text of a diagram. The message
// comes from mmdr and is for the user.
type SourceError struct{ Msg string }

func (e *SourceError) Error() string { return "the diagram has an error: " + e.Msg }

// Key returns a short identifier of the source text. Two diagrams have
// the same key only if they have the same text.
func Key(source string) string {
	sum := sha256.Sum256([]byte(source))
	return hex.EncodeToString(sum[:16])
}

// Renderer makes the images. It keeps the recent images in a cache. A
// Renderer is safe for use by more than one goroutine.
type Renderer struct {
	opts Options
	jobs chan struct{}

	mu    sync.Mutex
	lru   *list.List // of *entry, the most recent first
	byKey map[string]*list.Element
	size  int
}

type entry struct {
	key string
	svg []byte
	err *SourceError
}

func (e *entry) size() int {
	n := len(e.key) + len(e.svg)
	if e.err != nil {
		n += len(e.err.Msg)
	}
	return n
}

// New returns a Renderer.
func New(o Options) *Renderer {
	if o.Timeout <= 0 {
		o.Timeout = DefaultTimeout
	}
	if o.MaxJobs <= 0 {
		o.MaxJobs = DefaultJobs
	}
	if o.CacheBytes <= 0 {
		o.CacheBytes = DefaultCacheBytes
	}
	if o.Config == nil {
		o.Config = DefaultConfig
	}
	return &Renderer{
		opts: o, jobs: make(chan struct{}, o.MaxJobs),
		lru: list.New(), byKey: map[string]*list.Element{},
	}
}

// Render returns the SVG image of the source text. The error is a
// *SourceError if the text is not correct. Other errors are
// ErrUnavailable. The caller must not change the result.
func (r *Renderer) Render(ctx context.Context, source string) ([]byte, error) {
	key := Key(source)
	if e := r.get(key); e != nil {
		if e.err != nil {
			return nil, e.err
		}
		return e.svg, nil
	}
	if len(source) > MaxSource {
		return nil, r.put(&entry{key: key, err: &SourceError{fmt.Sprintf("the text is larger than %d KB", MaxSource>>10)}})
	}
	select {
	case r.jobs <- struct{}{}:
		defer func() { <-r.jobs }()
	case <-ctx.Done():
		return nil, ErrUnavailable
	}
	svg, err := r.run(ctx, source)
	var se *SourceError
	switch {
	case errors.As(err, &se):
		return nil, r.put(&entry{key: key, err: se})
	case err != nil:
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	r.put(&entry{key: key, svg: svg})
	return svg, nil
}

// run runs mmdr. The program gets the source on the standard input, and
// the configuration on file descriptor 3. Thus the server writes no
// files. The program writes the SVG to the standard output. It gets an
// empty environment. The program reads no other files, except system
// fonts if they exist.
func (r *Renderer) run(ctx context.Context, source string) ([]byte, error) {
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, r.opts.Timeout)
	defer cancel()
	config, err := r.configPipe()
	if err != nil {
		return nil, err
	}
	defer config.Close()
	// #nosec G204 -- the program comes from the configuration, and the
	// arguments are constants.
	cmd := exec.CommandContext(ctx, r.opts.Program, "--configFile", "/dev/fd/3", "--input", "-")
	cmd.ExtraFiles = []*os.File{config}
	cmd.Env = []string{}
	cmd.Dir = "/"
	cmd.WaitDelay = time.Second
	cmd.Stdin = strings.NewReader(source)
	stdout := &limitedBuffer{max: MaxSVG}
	stderr := &limitedBuffer{max: 4 << 10}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err = cmd.Run()
	switch {
	case parent.Err() != nil:
		return nil, fmt.Errorf("mmdr: %w", parent.Err())
	case ctx.Err() != nil:
		// The same text takes the same time. Thus Render keeps this
		// error in the cache, and the server does not try again on
		// each view.
		return nil, &SourceError{fmt.Sprintf("the diagram takes more than %s to make", r.opts.Timeout)}
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		switch exit.ExitCode() {
		case 1:
			return nil, &SourceError{message(stderr.String())}
		case 101:
			// A panic of the Rust program. The output can contain
			// paths of the source code, thus do not show it.
			return nil, &SourceError{"mmdr cannot read the text"}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("mmdr: %w", err)
	}
	if stdout.full {
		return nil, &SourceError{fmt.Sprintf("the image is larger than %d MB", MaxSVG>>20)}
	}
	svg := bytes.TrimSpace(stdout.Bytes())
	if !bytes.HasPrefix(svg, []byte("<svg")) && !bytes.HasPrefix(svg, []byte("<?xml")) {
		return nil, errors.New("mmdr: the output is not SVG")
	}
	return removeBackground(svg), nil
}

// configPipe returns the read end of a pipe that contains the
// configuration. The configuration is small, thus it fits in the buffer
// of the pipe.
func (r *Renderer) configPipe() (*os.File, error) {
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	_, err = pw.Write(r.opts.Config)
	if cerr := pw.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		pr.Close()
		return nil, err
	}
	return pr, nil
}

// message returns the first line of the error output of mmdr, without
// control characters.
func message(stderr string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(stderr), "\n")
	line = strings.TrimSpace(strings.TrimPrefix(line, "error:"))
	line = strings.Map(func(c rune) rune {
		if unicode.IsControl(c) {
			return -1
		}
		return c
	}, line)
	if r := []rune(line); len(r) > maxMessage {
		line = string(r[:maxMessage]) + "…"
	}
	if line == "" {
		return "mmdr cannot read the text"
	}
	return line
}

func (r *Renderer) get(key string) *entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	el, ok := r.byKey[key]
	if !ok {
		return nil
	}
	r.lru.MoveToFront(el)
	return el.Value.(*entry)
}

// put adds e to the cache and returns the error of e.
func (r *Renderer) put(e *entry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byKey[e.key]; !ok && e.size() <= r.opts.CacheBytes {
		r.byKey[e.key] = r.lru.PushFront(e)
		r.size += e.size()
		for r.size > r.opts.CacheBytes {
			old := r.lru.Remove(r.lru.Back()).(*entry)
			delete(r.byKey, old.key)
			r.size -= old.size()
		}
	}
	if e.err != nil {
		return e.err
	}
	return nil
}

// limitedBuffer keeps the first max bytes that are written to it. The
// buffer is a field, not an embedded type. Else io.Copy uses the ReadFrom
// method of bytes.Buffer, which has no limit.
type limitedBuffer struct {
	buf  bytes.Buffer
	max  int
	full bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	room := b.max - b.buf.Len()
	if len(p) > room {
		b.full = true
	}
	if room > 0 {
		b.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (b *limitedBuffer) Bytes() []byte  { return b.buf.Bytes() }
func (b *limitedBuffer) String() string { return b.buf.String() }
