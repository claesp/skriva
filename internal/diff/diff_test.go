package diff

import (
	"math/rand/v2"
	"strings"
	"testing"
)

// check makes sure that ops change a into b, that the operations are in
// order, and that the number of changes is minimal.
func check(t *testing.T, a, b []byte, ops []Op) {
	t.Helper()
	var got []byte
	i, j, edits := 0, 0, 0
	for _, op := range ops {
		switch op.Kind {
		case Equal:
			if op.I != i || op.J != j || a[op.I] != b[op.J] {
				t.Fatalf("%q -> %q: bad equal %+v at %d,%d", a, b, op, i, j)
			}
			got = append(got, a[i])
			i++
			j++
		case Delete:
			if op.I != i {
				t.Fatalf("%q -> %q: bad delete %+v at %d", a, b, op, i)
			}
			i++
			edits++
		case Insert:
			if op.J != j {
				t.Fatalf("%q -> %q: bad insert %+v at %d", a, b, op, j)
			}
			got = append(got, b[j])
			j++
			edits++
		}
	}
	if i != len(a) || string(got) != string(b) {
		t.Fatalf("%q -> %q: result %q, %d of %d", a, b, got, i, len(a))
	}
	if want := len(a) + len(b) - 2*lcs(a, b); edits != want {
		t.Fatalf("%q -> %q: %d changes, minimum %d", a, b, edits, want)
	}
}

// lcs returns the length of the longest common subsequence.
func lcs(a, b []byte) int {
	prev := make([]int, len(b)+1)
	for i := range a {
		cur := make([]int, len(b)+1)
		for j := range b {
			if a[i] == b[j] {
				cur[j+1] = prev[j] + 1
			} else {
				cur[j+1] = max(cur[j], prev[j+1])
			}
		}
		prev = cur
	}
	return prev[len(b)]
}

func TestDiff(t *testing.T) {
	for _, c := range [][2]string{
		{"", ""}, {"a", ""}, {"", "a"}, {"abc", "abc"}, {"abcabba", "cbabac"},
		{"abc", "xyz"}, {"abcdef", "abxdef"}, {"aaaa", "aa"}, {"ab", "ba"},
	} {
		a, b := []byte(c[0]), []byte(c[1])
		check(t, a, b, Diff(a, b))
	}
	r := rand.New(rand.NewPCG(1, 2))
	for range 2000 {
		a := make([]byte, r.IntN(20))
		b := make([]byte, r.IntN(20))
		for i := range a {
			a[i] = "abc"[r.IntN(3)]
		}
		for i := range b {
			b[i] = "abc"[r.IntN(3)]
		}
		check(t, a, b, Diff(a, b))
	}
}

func TestDiffLimit(t *testing.T) {
	// More changes than MaxEdits: the result replaces the middle part,
	// but it is correct.
	a := []byte(strings.Repeat("ab", MaxEdits))
	b := []byte(strings.Repeat("ba", MaxEdits) + "x")
	ops := Diff(a, b)
	var got []byte
	for _, op := range ops {
		switch op.Kind {
		case Equal:
			got = append(got, a[op.I])
		case Insert:
			got = append(got, b[op.J])
		}
	}
	if string(got) != string(b) {
		t.Fatal("wrong result above the limit")
	}
}

func BenchmarkDiffSmallChange(b *testing.B) {
	lines := make([]string, 5000)
	for i := range lines {
		lines[i] = strings.Repeat("x", i%70)
	}
	changed := append([]string{}, lines...)
	changed[2500] = "changed"
	for b.Loop() {
		Diff(lines, changed)
	}
}
