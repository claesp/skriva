package web

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/claesp/skriva/internal/diff"
	"github.com/claesp/skriva/internal/doc"
	"github.com/claesp/skriva/internal/store"
)

// diffContext is the number of equal lines that the comparison shows
// before and after each change.
const diffContext = 3

// diffPart is a part of a line in the comparison. Changed is true for the
// words that are not in the other version.
type diffPart struct {
	Text    string
	Changed bool
}

// diffLine is one row of the comparison. Kind is "equal", "delete",
// "insert" or "skip". A "skip" row stands for Skipped equal lines that
// the comparison does not show.
type diffLine struct {
	Kind    string
	Parts   []diffPart
	Skipped int
	// Old and New are the line numbers in the old and the new version,
	// or 0.
	Old, New int
}

// compareLines compares two texts line by line. In a changed block, the
// function compares each removed line with the added line at the same
// position word by word. Thus the result shows the changed words.
func compareLines(oldText, newText string) []diffLine {
	a, b := splitLines(oldText), splitLines(newText)
	ops := diff.Diff(a, b)
	var out []diffLine
	for i := 0; i < len(ops); {
		if ops[i].Kind == diff.Equal {
			op := ops[i]
			out = append(out, diffLine{Kind: "equal", Parts: []diffPart{{Text: a[op.I]}}, Old: op.I + 1, New: op.J + 1})
			i++
			continue
		}
		// A changed block: the deletions, then the insertions.
		var dels, ins []diff.Op
		for ; i < len(ops) && ops[i].Kind != diff.Equal; i++ {
			if ops[i].Kind == diff.Delete {
				dels = append(dels, ops[i])
			} else {
				ins = append(ins, ops[i])
			}
		}
		delLines := make([]diffLine, len(dels))
		insLines := make([]diffLine, len(ins))
		for k, op := range dels {
			delLines[k] = diffLine{Kind: "delete", Parts: []diffPart{{Text: a[op.I], Changed: true}}, Old: op.I + 1}
		}
		for k, op := range ins {
			insLines[k] = diffLine{Kind: "insert", Parts: []diffPart{{Text: b[op.J], Changed: true}}, New: op.J + 1}
		}
		for k := range min(len(dels), len(ins)) {
			delLines[k].Parts, insLines[k].Parts = compareWords(a[dels[k].I], b[ins[k].J])
		}
		out = append(out, delLines...)
		out = append(out, insLines...)
	}
	return collapse(out)
}

func splitLines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// wordRE divides a line into words, spaces and other characters.
var wordRE = regexp.MustCompile(`\s+|[\p{L}\p{N}_]+|.`)

// maxWordTokens is the maximum number of words in a line for the
// comparison word by word. A longer line shows as fully changed.
const maxWordTokens = 2000

// compareWords compares two lines word by word. It returns the parts of
// the old line and of the new line.
func compareWords(oldLine, newLine string) (oldParts, newParts []diffPart) {
	a, b := wordRE.FindAllString(oldLine, -1), wordRE.FindAllString(newLine, -1)
	if len(a) > maxWordTokens || len(b) > maxWordTokens {
		return []diffPart{{oldLine, true}}, []diffPart{{newLine, true}}
	}
	add := func(parts []diffPart, text string, changed bool) []diffPart {
		if n := len(parts); n > 0 && parts[n-1].Changed == changed {
			parts[n-1].Text += text
			return parts
		}
		return append(parts, diffPart{text, changed})
	}
	for _, op := range diff.Diff(a, b) {
		switch op.Kind {
		case diff.Equal:
			oldParts = add(oldParts, a[op.I], false)
			newParts = add(newParts, b[op.J], false)
		case diff.Delete:
			oldParts = add(oldParts, a[op.I], true)
		case diff.Insert:
			newParts = add(newParts, b[op.J], true)
		}
	}
	return oldParts, newParts
}

// collapse replaces the equal lines that are far from a change with
// "skip" rows.
func collapse(lines []diffLine) []diffLine {
	keep := make([]bool, len(lines))
	for i, l := range lines {
		if l.Kind == "equal" {
			continue
		}
		for k := max(0, i-diffContext); k <= min(len(lines)-1, i+diffContext); k++ {
			keep[k] = true
		}
	}
	var out []diffLine
	for i := 0; i < len(lines); {
		if keep[i] {
			out = append(out, lines[i])
			i++
			continue
		}
		j := i
		for j < len(lines) && !keep[j] {
			j++
		}
		out = append(out, diffLine{Kind: "skip", Skipped: j - i})
		i = j
	}
	return out
}

// compareForm is the data of the form that selects two versions.
type compareForm struct {
	Action   string
	Versions []store.Revision
	From, To int
}

// revisionText returns the title and the text of a revision in Markdown.
func revisionText(r *http.Request, rev *store.Revision) (string, error) {
	d, err := doc.Parse([]byte(rev.Body), nil)
	if err != nil {
		return "", err
	}
	return d.Markdown(links(r)), nil
}

// handleCompare shows the changes between two versions of a document.
// The query parameters "from" and "to" are the versions. The default is
// the change from the version before "to", and "to" is the current
// version by default.
func (s *Server) handleCompare(w http.ResponseWriter, r *http.Request, n *store.Node) {
	q := r.URL.Query()
	to := n.Version
	if v := q.Get("to"); v != "" {
		var ok bool
		if to, ok = parseVersion(v); !ok || to < 1 {
			s.badRequest(w, r, "The version is not valid.")
			return
		}
	}
	from := to - 1
	if v := q.Get("from"); v != "" {
		var ok bool
		if from, ok = parseVersion(v); !ok || from < 1 {
			s.badRequest(w, r, "The version is not valid.")
			return
		}
	}
	if from < 1 || from == to {
		s.badRequest(w, r, "Select two different versions.")
		return
	}
	revs := make([]*store.Revision, 2)
	texts := make([]string, 2)
	for i, v := range []int{from, to} {
		rev, err := s.store.Revision(r.Context(), n.ID, v)
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		if texts[i], err = revisionText(r, rev); err != nil {
			s.serverError(w, r, err)
			return
		}
		revs[i] = rev
	}
	all, err := s.store.Revisions(r.Context(), n.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	page, err := s.loadNodePage(r, n, "history", "Compare")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	page.CloseURL = base(r) + "/history/" + n.PublicID
	lines := compareLines(texts[0], texts[1])
	changed := false
	for _, l := range lines {
		changed = changed || l.Kind == "delete" || l.Kind == "insert"
	}
	var oldTitle, newTitle []diffPart
	if revs[0].Title != revs[1].Title {
		oldTitle, newTitle = compareWords(revs[0].Title, revs[1].Title)
	}
	s.render(w, r, http.StatusOK, "compare",
		"Compare versions "+strconv.Itoa(from)+" and "+strconv.Itoa(to)+" of "+n.Title, struct {
			nodePage
			From, To           *store.Revision
			Lines              []diffLine
			Changed            bool
			OldTitle, NewTitle []diffPart
			Compare            compareForm
		}{page, revs[0], revs[1], lines, changed, oldTitle, newTitle, compareForm{
			Action: base(r) + "/compare/" + n.PublicID, Versions: all, From: from, To: to,
		}})
}
