package diagram

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fake writes a shell script that acts as mmdr. It counts the runs in the
// file "runs".
func fake(t *testing.T, body string) (prog, runs string) {
	t.Helper()
	dir := t.TempDir()
	prog = filepath.Join(dir, "mmdr")
	runs = filepath.Join(dir, "runs")
	script := "#!/bin/sh\necho x >> '" + runs + "'\n" + body + "\n"
	if err := os.WriteFile(prog, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return prog, runs
}

func count(t *testing.T, runs string) int {
	t.Helper()
	b, err := os.ReadFile(runs)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(b), "x")
}

func TestRender(t *testing.T) {
	// The fake program writes the input inside an svg element.
	prog, runs := fake(t, `[ "$*" = "--configFile /dev/fd/3 --input -" ] || exit 9; printf '<svg>'; cat; printf '</svg>'`)
	r := New(Options{Program: prog})
	ctx := context.Background()
	for range 2 {
		svg, err := r.Render(ctx, "graph TD; A-->B")
		if err != nil || string(svg) != "<svg>graph TD; A-->B</svg>" {
			t.Fatalf("Render = %q, %v", svg, err)
		}
	}
	if n := count(t, runs); n != 1 {
		t.Fatalf("the program ran %d times, want 1 (cache)", n)
	}
}

// TestConfig checks that the program gets the configuration on file
// descriptor 3.
func TestConfig(t *testing.T) {
	prog, _ := fake(t, `printf '<svg>'; cat /dev/fd/3; printf '</svg>'`)
	svg, err := New(Options{Program: prog}).Render(context.Background(), "graph TD")
	if err != nil || string(svg) != "<svg>"+string(DefaultConfig)+"</svg>" {
		t.Fatalf("Render = %q, %v", svg, err)
	}
	svg, err = New(Options{Program: prog, Config: []byte(`{"a":1}`)}).Render(context.Background(), "graph TD")
	if err != nil || string(svg) != `<svg>{"a":1}</svg>` {
		t.Fatalf("Render with a config = %q, %v", svg, err)
	}
}

func TestRemoveBackground(t *testing.T) {
	const red = `<rect x="0" y="0" width="1" height="1" fill="red"/></svg>`
	for in, want := range map[string]string{
		`<svg a="1" viewBox="0 0 10.5 20"><rect x="0" y="0" width="10.5" height="20" fill="#fffff8"/>` + red:      `<svg a="1" viewBox="0 0 10.5 20">` + red,
		`<svg viewBox="-50 -10 450 219" b="2"><rect x="-50" y="-10" width="450" height="219" fill="#fff"/>` + red: `<svg viewBox="-50 -10 450 219" b="2">` + red,
		// A rectangle with another size is not the background.
		`<svg viewBox="0 0 10 20">` + red: `<svg viewBox="0 0 10 20">` + red,
		`<svg>` + red:                     `<svg>` + red,
	} {
		if got := string(removeBackground([]byte(in))); got != want {
			t.Errorf("removeBackground(%q) = %q", in, got)
		}
	}
}

func TestRenderSourceError(t *testing.T) {
	prog, runs := fake(t, `printf 'error: invalid flowchart edge syntax: A-->\nmore\n' >&2; exit 1`)
	r := New(Options{Program: prog})
	for range 2 {
		_, err := r.Render(context.Background(), "graph TD; A-->")
		var se *SourceError
		if !errors.As(err, &se) || se.Msg != "invalid flowchart edge syntax: A-->" {
			t.Fatalf("Render error = %v", err)
		}
	}
	if n := count(t, runs); n != 1 {
		t.Fatalf("the program ran %d times, want 1 (cache)", n)
	}
	_, err := r.Render(context.Background(), strings.Repeat("a", MaxSource+1))
	var se *SourceError
	if !errors.As(err, &se) || !strings.Contains(se.Msg, "larger") {
		t.Fatalf("Render of a large text = %v", err)
	}
}

func TestRenderUnavailable(t *testing.T) {
	for name, body := range map[string]string{
		"not svg":   "echo hello",
		"signal":    "kill -9 $$",
		"exit code": "exit 2",
	} {
		t.Run(name, func(t *testing.T) {
			prog, runs := fake(t, body)
			r := New(Options{Program: prog, Timeout: 200 * time.Millisecond})
			for range 2 {
				if _, err := r.Render(context.Background(), "graph TD"); !errors.Is(err, ErrUnavailable) {
					t.Fatalf("Render error = %v", err)
				}
			}
			// The cache does not keep these errors.
			if n := count(t, runs); n != 2 {
				t.Fatalf("the program ran %d times, want 2", n)
			}
		})
	}
	r := New(Options{Program: filepath.Join(t.TempDir(), "missing")})
	if _, err := r.Render(context.Background(), "graph TD"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Render without a program = %v", err)
	}
}

// A text that takes too long, that makes mmdr panic or that needs too
// much memory gives the same result each time. Thus the cache keeps the
// error.
func TestRenderCachedFailure(t *testing.T) {
	for name, tc := range map[string]struct{ body, msg string }{
		"timeout": {"sleep 5", "takes more than 200ms"},
		"panic":   {"echo 'panicked at /src/x.rs:1' >&2; exit 101", "mmdr cannot read the text"},
		"memory":  {"kill -ABRT $$", "the diagram needs too much memory"},
	} {
		t.Run(name, func(t *testing.T) {
			prog, runs := fake(t, tc.body)
			r := New(Options{Program: prog, Timeout: 200 * time.Millisecond})
			for range 2 {
				_, err := r.Render(context.Background(), "graph TD")
				var se *SourceError
				if !errors.As(err, &se) || se.Msg != tc.msg && !strings.Contains(se.Msg, tc.msg) {
					t.Fatalf("Render error = %v, want %q", err, tc.msg)
				}
			}
			if n := count(t, runs); n != 1 {
				t.Fatalf("the program ran %d times, want 1", n)
			}
		})
	}
}

// If the request stops, the error is not a property of the text.
func TestRenderCanceled(t *testing.T) {
	prog, _ := fake(t, "sleep 5")
	r := New(Options{Program: prog})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := r.Render(ctx, "graph TD"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Render error = %v", err)
	}
}

func TestRenderLarge(t *testing.T) {
	prog, _ := fake(t, `printf '<svg>'; head -c 5000000 /dev/zero`)
	_, err := New(Options{Program: prog}).Render(context.Background(), "graph TD")
	var se *SourceError
	if !errors.As(err, &se) || !strings.Contains(se.Msg, "larger") {
		t.Fatalf("Render error = %v", err)
	}
}

func TestCacheLimit(t *testing.T) {
	prog, runs := fake(t, `printf '<svg>'; cat; printf '</svg>'`)
	// The cache has room for about one entry.
	r := New(Options{Program: prog, CacheBytes: 60})
	ctx := context.Background()
	for _, s := range []string{"a", "b", "a"} {
		if _, err := r.Render(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	if n := count(t, runs); n != 3 {
		t.Fatalf("the program ran %d times, want 3", n)
	}
	if r.size > 60 || r.lru.Len() != 1 {
		t.Fatalf("cache size %d with %d entries", r.size, r.lru.Len())
	}
}

func TestMessage(t *testing.T) {
	if m := message("error: bad\x1b[31m text\nline 2"); m != "bad[31m text" {
		t.Fatalf("message = %q", m)
	}
	if m := message(""); m == "" {
		t.Fatal("message is empty")
	}
	if m := message(strings.Repeat("é", 400)); len([]rune(m)) != maxMessage+1 {
		t.Fatalf("message has %d characters", len([]rune(m)))
	}
}

// TestMmdr runs the real program if SKRIVA_TEST_MMDR is its path.
func TestMmdr(t *testing.T) {
	prog := os.Getenv("SKRIVA_TEST_MMDR")
	if prog == "" {
		t.Skip("SKRIVA_TEST_MMDR is not set")
	}
	r := New(Options{Program: prog, Timeout: 30 * time.Second})
	svg, err := r.Render(context.Background(), "flowchart LR\n  A[Översikt] --> B{OK?}\n  B -->|Yes| C[Save]\n")
	if err != nil || !strings.Contains(string(svg), "Översikt") {
		t.Fatalf("Render = %.200q, %v", svg, err)
	}
	// The image has the style of DefaultConfig.
	if !strings.Contains(string(svg), `font-family="Inter Variable,sans-serif"`) {
		t.Error("image does not have the font of DefaultConfig")
	}
	// The images have no background. A sequence diagram has a view box
	// that does not start at 0 0.
	seq, err := r.Render(context.Background(), "sequenceDiagram\n  A->>B: Hello\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, img := range [][]byte{svg, seq} {
		if strings.Contains(string(img[:min(len(img), 400)]), `fill="#fffff8"/><defs>`) ||
			strings.Contains(string(img[:min(len(img), 400)]), `<rect x="0" y="0"`) {
			t.Errorf("image has a background: %.400s", img)
		}
	}
	_, err = r.Render(context.Background(), "flowchart LR\n  A -->\n")
	var se *SourceError
	if !errors.As(err, &se) {
		t.Fatalf("Render of a bad diagram = %v", err)
	}
}
