package textdiff

import (
	"strings"
	"testing"
)

func TestMergeDisjointEdits(t *testing.T) {
	base := []byte("a\nb\nc\nd\ne\n")
	ours := []byte("A\nb\nc\nd\ne\n")   // line 1
	theirs := []byte("a\nb\nc\nd\nE\n") // line 5
	merged, clean := Merge(base, ours, theirs, "ours", "theirs")
	if !clean {
		t.Fatalf("disjoint edits flagged dirty:\n%s", merged)
	}
	if string(merged) != "A\nb\nc\nd\nE\n" {
		t.Errorf("merged = %q", merged)
	}
}

func TestMergeOneSideOnly(t *testing.T) {
	base := []byte("a\nb\n")
	ours := []byte("a\nb\n")
	theirs := []byte("a\nX\nb\n")
	merged, clean := Merge(base, ours, theirs, "o", "t")
	if !clean || string(merged) != "a\nX\nb\n" {
		t.Errorf("merged = %q clean=%v", merged, clean)
	}
}

func TestMergeBothSidesSameChange(t *testing.T) {
	base := []byte("a\n")
	ours := []byte("same\n")
	theirs := []byte("same\n")
	merged, clean := Merge(base, ours, theirs, "o", "t")
	if !clean || string(merged) != "same\n" {
		t.Errorf("merged = %q clean=%v", merged, clean)
	}
}

func TestMergeOverlapConflicts(t *testing.T) {
	base := []byte("shared\nmiddle\nshared2\n")
	ours := []byte("shared\nOURS\nshared2\n")
	theirs := []byte("shared\nTHEIRS\nshared2\n")
	merged, clean := Merge(base, ours, theirs, "mine", "yours")
	if clean {
		t.Fatal("overlapping edits reported clean")
	}
	s := string(merged)
	if !strings.Contains(s, "<<<<<<< mine\nOURS\n=======\nTHEIRS\n>>>>>>> yours") {
		t.Errorf("markers malformed:\n%s", s)
	}
	// Context survives around the conflict.
	if !strings.HasPrefix(s, "shared\n") || !strings.Contains(s, "shared2") {
		t.Errorf("context lost:\n%s", s)
	}
}

func TestMergeInsertionsAtSamePoint(t *testing.T) {
	base := []byte("a\nz\n")
	ours := []byte("a\nours\nz\n")
	theirs := []byte("a\ntheirs\nz\n")
	merged, clean := Merge(base, ours, theirs, "o", "t")
	if clean {
		t.Fatalf("same-point insertions must conflict:\n%s", merged)
	}
}

func TestMergeDeletions(t *testing.T) {
	base := []byte("a\nb\nc\n")
	ours := []byte("a\nc\n")         // deleted b
	theirs := []byte("a\nb\nc\nd\n") // appended d
	merged, clean := Merge(base, ours, theirs, "o", "t")
	if !clean || string(merged) != "a\nc\nd\n" {
		t.Errorf("merged = %q clean=%v", merged, clean)
	}
}

func TestMergeEmptyInputs(t *testing.T) {
	merged, clean := Merge(nil, nil, nil, "o", "t")
	if !clean || len(merged) != 0 {
		t.Errorf("empty merge = %q clean=%v", merged, clean)
	}
}

// TestWindow covers the windowing every --diff view goes through: which lines
// survive, where the "…" elision markers land, and the counts reported back.
func TestWindow(t *testing.T) {
	body := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = "  ctx"
		}
		return out
	}
	withChange := func(before, after int, change ...string) []string {
		return append(append(body(before), change...), body(after)...)
	}

	cases := []struct {
		name                    string
		lines                   []string
		contextLines, maxLines  int
		wantOut                 []string
		wantAdded, wantRemoved  int
		wantHunks, wantOverflow int
	}{
		{
			// Nothing kept means nothing to elide: a trailing marker alone
			// would claim a diff where there is none.
			name:  "no change renders nothing",
			lines: body(3), contextLines: 1, maxLines: 10,
			wantOut: nil,
		},
		{
			name:  "one hunk keeps its context and elides the rest",
			lines: withChange(3, 3, "+new"), contextLines: 1, maxLines: 10,
			wantOut:   []string{"…", "  ctx", "+new", "  ctx", "…"},
			wantAdded: 1, wantHunks: 1,
		},
		{
			name:  "adjacent changes are one hunk",
			lines: withChange(0, 0, "-old", "+new"), contextLines: 0, maxLines: 10,
			wantOut:   []string{"-old", "+new"},
			wantAdded: 1, wantRemoved: 1, wantHunks: 1,
		},
		{
			name:  "separated changes are two hunks",
			lines: append(withChange(0, 3, "+a"), "+b"), contextLines: 0, maxLines: 10,
			wantOut:   []string{"+a", "…", "+b"},
			wantAdded: 2, wantHunks: 2,
		},
		{
			name:  "overflow counts the kept lines it dropped",
			lines: []string{"+1", "+2", "+3", "+4"}, contextLines: 0, maxLines: 2,
			wantOut:   []string{"+1", "+2"},
			wantAdded: 4, wantHunks: 1, wantOverflow: 2,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, added, removed, hunks, overflow := Window(c.lines, c.contextLines, c.maxLines)
			if strings.Join(out, "|") != strings.Join(c.wantOut, "|") {
				t.Errorf("out = %v, want %v", out, c.wantOut)
			}
			if added != c.wantAdded || removed != c.wantRemoved {
				t.Errorf("added/removed = %d/%d, want %d/%d", added, removed, c.wantAdded, c.wantRemoved)
			}
			if hunks != c.wantHunks {
				t.Errorf("hunks = %d, want %d", hunks, c.wantHunks)
			}
			if overflow != c.wantOverflow {
				t.Errorf("overflow = %d, want %d", overflow, c.wantOverflow)
			}
		})
	}
}
