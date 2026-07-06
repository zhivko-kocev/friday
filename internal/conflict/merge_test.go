package conflict

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
