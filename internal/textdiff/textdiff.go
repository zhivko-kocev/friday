// Package textdiff is the pure, dependency-free line-diff and 3-way-merge
// engine shared by the interactive conflict prompt (internal/conflict), the
// CLI --diff view (internal/cli), and the engine's pull-side unchanged-line
// detection. It has no UI dependency, so a headless consumer like
// internal/engine can use it without pulling in internal/ui.
package textdiff

import (
	"slices"
	"strings"
)

// LineDiff returns a unified-style line diff of a and b. Each line is
// prefixed with "  " (context), "- " (only in a), or "+ " (only in b).
// Uses LCS so single insertions don't cascade into reporting every later
// line as changed.
func LineDiff(a, b []byte) []string {
	return lcsDiff(splitLines(string(a)), splitLines(string(b)))
}

// Window trims a full LineDiff — lines prefixed "  " (context), "- ", or "+ " —
// to its changed regions plus contextLines lines of context, replacing each
// elided run (including leading/trailing file body) with a single "…" marker.
// It returns the windowed lines, the added/removed line counts, the number of
// change runs (hunks), and how many kept lines were dropped past the maxLines
// cap (0 if none). Windowing rather than head-truncating keeps the tail of a
// long edit visible, which is usually where the interesting change is.
func Window(lines []string, contextLines, maxLines int) (out []string, added, removed, hunks, overflow int) {
	isChange := func(s string) bool {
		return strings.HasPrefix(s, "+") || strings.HasPrefix(s, "-")
	}
	n := len(lines)
	keep := make([]bool, n)
	inRun := false
	for i, l := range lines {
		if isChange(l) {
			keep[i] = true
			if strings.HasPrefix(l, "+") {
				added++
			} else {
				removed++
			}
			if !inRun {
				hunks++
				inRun = true
			}
			for d := 1; d <= contextLines; d++ {
				if i-d >= 0 {
					keep[i-d] = true
				}
				if i+d < n {
					keep[i+d] = true
				}
			}
		} else {
			inRun = false
		}
	}

	pendingEllipsis := false
	for i := range n {
		if !keep[i] {
			pendingEllipsis = true
			continue
		}
		if len(out) >= maxLines {
			for j := i; j < n; j++ {
				if keep[j] {
					overflow++
				}
			}
			return out, added, removed, hunks, overflow
		}
		if pendingEllipsis {
			out = append(out, "…")
			pendingEllipsis = false
		}
		out = append(out, lines[i])
	}
	if pendingEllipsis && len(out) > 0 {
		out = append(out, "…")
	}
	return out, added, removed, hunks, overflow
}

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

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	// Trailing newline produces an empty final element — drop it for a cleaner diff.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// lcsTable builds the suffix LCS-length table shared by lcsDiff and lcsPairs.
func lcsTable(a, b []string) [][]int {
	la, lb := len(a), len(b)
	dp := make([][]int, la+1)
	for i := range dp {
		dp[i] = make([]int, lb+1)
	}
	for i := la - 1; i >= 0; i-- {
		for j := lb - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	return dp
}

// lcsDiff produces a simple line-by-line diff using LCS. Output lines are
// prefixed with " ", "-", or "+".
func lcsDiff(a, b []string) []string {
	la, lb := len(a), len(b)
	dp := lcsTable(a, b)
	var out []string
	i, j := 0, 0
	for i < la && j < lb {
		switch {
		case a[i] == b[j]:
			out = append(out, "  "+a[i])
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			out = append(out, "- "+a[i])
			i++
		default:
			out = append(out, "+ "+b[j])
			j++
		}
	}
	for ; i < la; i++ {
		out = append(out, "- "+a[i])
	}
	for ; j < lb; j++ {
		out = append(out, "+ "+b[j])
	}
	return out
}
