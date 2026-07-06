package conflict

import (
	"bytes"
	"strings"
	"testing"
)

func TestPromptKeep(t *testing.T) {
	in := strings.NewReader("k\n")
	out := &bytes.Buffer{}
	if got, _ := promptIO(in, out, "src", "dest", []byte("a"), []byte("b"), nil); got != ChoiceKeep {
		t.Errorf("got %v, want ChoiceKeep", got)
	}
}

func TestPromptTake(t *testing.T) {
	in := strings.NewReader("t\n")
	out := &bytes.Buffer{}
	if got, _ := promptIO(in, out, "src", "dest", []byte("a"), []byte("b"), nil); got != ChoiceTake {
		t.Errorf("got %v, want ChoiceTake", got)
	}
}

func TestPromptSkipOnBlankLine(t *testing.T) {
	in := strings.NewReader("\n")
	out := &bytes.Buffer{}
	if got, _ := promptIO(in, out, "src", "dest", []byte("a"), []byte("b"), nil); got != ChoiceSkip {
		t.Errorf("got %v, want ChoiceSkip", got)
	}
}

func TestPromptDiffThenChoose(t *testing.T) {
	// First "d" prints a diff, then "k" picks keep.
	in := strings.NewReader("d\nk\n")
	out := &bytes.Buffer{}
	got, _ := promptIO(in, out, "canonical", "target", []byte("one\n"), []byte("two\n"), nil)
	if got != ChoiceKeep {
		t.Errorf("got %v, want ChoiceKeep", got)
	}
	s := out.String()
	if !strings.Contains(s, "--- canonical") || !strings.Contains(s, "+++ target") {
		t.Errorf("diff headers missing; output:\n%s", s)
	}
	if !strings.Contains(s, "- one") || !strings.Contains(s, "+ two") {
		t.Errorf("diff lines missing; output:\n%s", s)
	}
}

func TestPromptUnknownThenSkip(t *testing.T) {
	in := strings.NewReader("zzz\nq\n\n")
	out := &bytes.Buffer{}
	got, _ := promptIO(in, out, "src", "dest", nil, nil, nil)
	if got != ChoiceSkip {
		t.Errorf("got %v, want ChoiceSkip after junk", got)
	}
	if !strings.Contains(out.String(), "unrecognised") {
		t.Errorf("expected hint; output: %s", out.String())
	}
}

func TestPromptEOFSkips(t *testing.T) {
	in := strings.NewReader("")
	out := &bytes.Buffer{}
	if got, _ := promptIO(in, out, "s", "d", nil, nil, nil); got != ChoiceSkip {
		t.Errorf("got %v, want ChoiceSkip on EOF", got)
	}
}

func TestPromptMergeHiddenWithoutBase(t *testing.T) {
	in := strings.NewReader("m\ns\n")
	out := &bytes.Buffer{}
	got, merged := promptIO(in, out, "src", "dest", []byte("a"), []byte("b"), nil)
	if got != ChoiceSkip || merged != nil {
		t.Errorf("got %v/%q, want skip and no merge without a base", got, merged)
	}
	if strings.Contains(out.String(), "[m] merge") {
		t.Errorf("merge option offered without a base:\n%s", out.String())
	}
}

func TestPromptCleanMerge(t *testing.T) {
	base := []byte("one\ntwo\nthree\n")
	ours := []byte("ONE\ntwo\nthree\n")   // edited line 1
	theirs := []byte("one\ntwo\nTHREE\n") // edited line 3
	in := strings.NewReader("m\n")
	out := &bytes.Buffer{}
	got, merged := promptIO(in, out, "canonical", "target", ours, theirs, base)
	if got != ChoiceMerge {
		t.Fatalf("got %v, want ChoiceMerge", got)
	}
	if string(merged) != "ONE\ntwo\nTHREE\n" {
		t.Errorf("merged = %q", merged)
	}
}

func TestPromptDirtyMergeDeclined(t *testing.T) {
	base := []byte("one\n")
	ours := []byte("OURS\n")
	theirs := []byte("THEIRS\n")
	// m → overlap prompt, answer n → back to the menu → s skips.
	in := strings.NewReader("m\nn\ns\n")
	out := &bytes.Buffer{}
	got, _ := promptIO(in, out, "canonical", "target", ours, theirs, base)
	if got != ChoiceSkip {
		t.Errorf("got %v, want ChoiceSkip after declining markers", got)
	}
	if !strings.Contains(out.String(), "overlap") {
		t.Errorf("expected overlap warning:\n%s", out.String())
	}
}

func TestPromptDirtyMergeWithMarkers(t *testing.T) {
	base := []byte("one\n")
	ours := []byte("OURS\n")
	theirs := []byte("THEIRS\n")
	in := strings.NewReader("m\ny\n")
	out := &bytes.Buffer{}
	got, merged := promptIO(in, out, "canonical", "target", ours, theirs, base)
	if got != ChoiceMerge {
		t.Fatalf("got %v, want ChoiceMerge", got)
	}
	s := string(merged)
	for _, want := range []string{"<<<<<<< canonical", "OURS", "=======", "THEIRS", ">>>>>>> target"} {
		if !strings.Contains(s, want) {
			t.Errorf("merged missing %q:\n%s", want, s)
		}
	}
}
