// Package diff compares two sequences with the algorithm of Eugene W.
// Myers ("An O(ND) Difference Algorithm and Its Variations", 1986).
//
// The time is O((N+M)·D) and the memory is O(D²), where D is the number
// of changes. The common start and the common end are removed first.
// Thus a small change in a long text is fast. If D is more than MaxEdits,
// the result replaces all of the remaining part. Thus a large change
// cannot use too much time or memory.
package diff

// Kind is the type of an operation.
type Kind int

// The kinds of operations.
const (
	Equal Kind = iota
	Delete
	Insert
)

// Op is one operation. Delete removes A[I] from the old sequence. Insert
// adds B[J] from the new sequence. Equal keeps A[I], which is equal to
// B[J].
type Op struct {
	Kind Kind
	I, J int
}

// MaxEdits is the maximum number of changes that Diff calculates exactly.
const MaxEdits = 1000

// Diff returns the operations that change a into b. The operations are
// in the order of the sequences.
func Diff[T comparable](a, b []T) []Op {
	// Remove the common start and the common end.
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	ops := make([]Op, 0, len(a)+len(b))
	for i := range pre {
		ops = append(ops, Op{Equal, i, i})
	}
	ops = append(ops, middle(a[pre:len(a)-suf], b[pre:len(b)-suf], pre, pre)...)
	for k := suf; k > 0; k-- {
		ops = append(ops, Op{Equal, len(a) - k, len(b) - k})
	}
	return ops
}

// middle compares a and b. offA and offB are the positions of a and b in
// the full sequences.
func middle[T comparable](a, b []T, offA, offB int) []Op {
	n, m := len(a), len(b)
	if n == 0 || m == 0 {
		return replace(n, m, offA, offB)
	}
	maxD := min(n+m, MaxEdits)
	// v[k+offset] is the end of the furthest path on diagonal k. trace
	// keeps a copy of v for each D, to find the path afterwards.
	offset := maxD + 1
	v := make([]int, 2*maxD+3)
	var trace [][]int
	found := false
	for d := 0; d <= maxD && !found; d++ {
		// Only the diagonals -d to d can change in this step.
		snap := make([]int, 2*d+3)
		copy(snap, v[offset-d-1:offset+d+2])
		trace = append(trace, snap)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
				x = v[offset+k+1] // A step down: an insertion.
			} else {
				x = v[offset+k-1] + 1 // A step right: a deletion.
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[offset+k] = x
			if x >= n && y >= m {
				found = true
				break
			}
		}
	}
	if !found {
		return replace(n, m, offA, offB)
	}
	return backtrack(trace, n, m, offA, offB)
}

// backtrack finds the path from the end to the start with the copies of
// v. snap of step d contains the diagonals -d-1 to d+1.
func backtrack(trace [][]int, n, m, offA, offB int) []Op {
	var rev []Op
	x, y := n, m
	for d := len(trace) - 1; d >= 0; d-- {
		snap := trace[d]
		at := func(k int) int { return snap[k+d+1] }
		k := x - y
		var prevK int
		if k == -d || (k != d && at(k-1) < at(k+1)) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := 0
		if d > 0 {
			prevX = at(prevK)
		}
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			x--
			y--
			rev = append(rev, Op{Equal, offA + x, offB + y})
		}
		if d > 0 {
			if x == prevX {
				y--
				rev = append(rev, Op{Insert, offA + x, offB + y})
			} else {
				x--
				rev = append(rev, Op{Delete, offA + x, offB + y})
			}
		}
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

// replace deletes all of a and inserts all of b.
func replace(n, m, offA, offB int) []Op {
	ops := make([]Op, 0, n+m)
	for i := range n {
		ops = append(ops, Op{Delete, offA + i, offB})
	}
	for j := range m {
		ops = append(ops, Op{Insert, offA + n, offB + j})
	}
	return ops
}
