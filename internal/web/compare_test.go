package web

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func TestCompareLines(t *testing.T) {
	var old, cur []string
	for i := range 20 {
		old = append(old, fmt.Sprint("line ", i))
	}
	cur = append(cur, old...)
	cur[10] = "line ten changed"
	cur = append(cur, "new end")
	lines := compareLines(strings.Join(old, "\n"), strings.Join(cur, "\n"))
	var kinds []string
	for _, l := range lines {
		kinds = append(kinds, l.Kind)
	}
	want := "skip equal equal equal delete insert equal equal equal skip equal equal equal insert"
	if got := strings.Join(kinds, " "); got != want {
		t.Fatalf("kinds = %s", got)
	}
	if lines[0].Skipped != 7 || lines[4].Old != 11 || lines[5].New != 11 {
		t.Fatalf("lines = %+v", lines[:6])
	}
	// The changed line shows the changed words only.
	del, ins := lines[4].Parts, lines[5].Parts
	if len(del) != 2 || del[0].Text != "line " || del[0].Changed || del[1].Text != "10" || !del[1].Changed {
		t.Fatalf("removed parts = %+v", del)
	}
	if len(ins) != 2 || ins[1].Text != "ten changed" || !ins[1].Changed {
		t.Fatalf("added parts = %+v", ins)
	}
	if l := compareLines("", "a\nb"); len(l) != 2 || l[0].Kind != "insert" {
		t.Fatalf("from empty = %+v", l)
	}
	if l := compareLines("a\n", "a"); len(l) != 1 || l[0].Kind != "skip" {
		t.Fatalf("same text = %+v", l)
	}
}

// Two very different versions do not use too much time: after the budget,
// the lines show as fully changed.
func TestCompareBudget(t *testing.T) {
	var old, cur []string
	for i := range 300 {
		old = append(old, strings.Repeat("w ", 900)+"old"+strconv.Itoa(i))
		cur = append(cur, strings.Repeat("w ", 900)+"new"+strconv.Itoa(i))
	}
	lines := compareLines(strings.Join(old, "\n"), strings.Join(cur, "\n"))
	last := lines[len(lines)-1]
	if last.Kind != "insert" || len(last.Parts) != 1 || !last.Parts[0].Changed {
		t.Fatalf("the last line is %+v", last.Kind)
	}
	if first := lines[0]; len(first.Parts) < 2 {
		t.Fatalf("the first line has no word changes: %d parts", len(first.Parts))
	}
}
