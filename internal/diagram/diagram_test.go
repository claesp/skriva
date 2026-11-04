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
	prog, runs := fake(t, `[ "$1 $2" = "--input -" ] || exit 9; printf '<svg>'; cat; printf '</svg>'`)
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
		"timeout": "sleep 5",
		"not svg": "echo hello",
		"signal":  "kill -9 $$",
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
	_, err = r.Render(context.Background(), "flowchart LR\n  A -->\n")
	var se *SourceError
	if !errors.As(err, &se) {
		t.Fatalf("Render of a bad diagram = %v", err)
	}
}
