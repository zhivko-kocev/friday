package conflict

import (
	"slices"
	"strings"
)

// Merge performs a line-based 3-way merge of ours and theirs against their
// common base. Non-overlapping edits combine cleanly; overlapping hunks get
// git-style conflict markers and clean=false. No external diff tool, no
// dependencies — built on the same LCS machinery as LineDiff.
func Merge(base, ours, theirs []byte, labelOurs, labelTheirs string) (merged []byte, clean bool) {
	b := splitLines(string(base))
	o := splitLines(string(ours))
	th := splitLines(string(theirs))

	anchors := commonAnchors(b, o, th)
	// Sentinel anchor at the end so the final region is processed uniformly.
	anchors = append(anchors, [3]int{len(b), len(o), len(th)})

	var out []string
	clean = true
	prev := [3]int{-1, -1, -1}
	for _, a := range anchors {
		bo := b[prev[0]+1 : a[0]]
		oo := o[prev[1]+1 : a[1]]
		to := th[prev[2]+1 : a[2]]
		switch {
		case slices.Equal(oo, bo):
			out = append(out, to...) // only theirs changed
		case slices.Equal(to, bo):
			out = append(out, oo...) // only ours changed
		case slices.Equal(oo, to):
			out = append(out, oo...) // both made the same change
		default:
			clean = false
			out = append(out, "<<<<<<< "+labelOurs)
			out = append(out, oo...)
			out = append(out, "=======")
			out = append(out, to...)
			out = append(out, ">>>>>>> "+labelTheirs)
		}
		if a[0] < len(b) {
			out = append(out, b[a[0]]) // the anchor line itself
		}
		prev = a
	}
	if len(out) == 0 {
		return []byte{}, clean
	}
	return []byte(strings.Join(out, "\n") + "\n"), clean
}

// commonAnchors returns index triples (base, ours, theirs) of lines all
// three versions share, in order. Each pairwise map comes from an LCS, so
// indices are strictly increasing on both sides; their intersection over
// base order is therefore monotonic in all three.
func commonAnchors(b, o, t []string) [][3]int {
	toOurs := lcsPairs(b, o)
	toTheirs := lcsPairs(b, t)
	var anchors [][3]int
	for bi := range b {
		oi, okO := toOurs[bi]
		ti, okT := toTheirs[bi]
		if okO && okT {
			anchors = append(anchors, [3]int{bi, oi, ti})
		}
	}
	return anchors
}

// LCSPairs maps a-indices to b-indices along one longest common subsequence
// of two line slices. The engine uses it to tell edited lines from unchanged
// ones when inverting a pull.
func LCSPairs(a, b []string) map[int]int { return lcsPairs(a, b) }

// lcsPairs maps a-indices to b-indices along one longest common subsequence.
func lcsPairs(a, b []string) map[int]int {
	dp := lcsTable(a, b)
	pairs := map[int]int{}
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			pairs[i] = j
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			i++
		default:
			j++
		}
	}
	return pairs
}
