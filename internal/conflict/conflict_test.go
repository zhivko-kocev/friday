package conflict

import (
	"bytes"
	"strings"
	"testing"
)

func TestPromptKeep(t *testing.T) {
	in := strings.NewReader("k\n")
	out := &bytes.Buffer{}
	if got := promptIO(in, out, "src", "dest", []byte("a"), []byte("b")); got != ChoiceKeep {
		t.Errorf("got %v, want ChoiceKeep", got)
	}
}

func TestPromptTake(t *testing.T) {
	in := strings.NewReader("t\n")
	out := &bytes.Buffer{}
	if got := promptIO(in, out, "src", "dest", []byte("a"), []byte("b")); got != ChoiceTake {
		t.Errorf("got %v, want ChoiceTake", got)
	}
}

func TestPromptSkipOnBlankLine(t *testing.T) {
	in := strings.NewReader("\n")
	out := &bytes.Buffer{}
	if got := promptIO(in, out, "src", "dest", []byte("a"), []byte("b")); got != ChoiceSkip {
		t.Errorf("got %v, want ChoiceSkip", got)
	}
}

func TestPromptDiffThenChoose(t *testing.T) {
	// First "d" prints a diff, then "k" picks keep.
	in := strings.NewReader("d\nk\n")
	out := &bytes.Buffer{}
	got := promptIO(in, out, "canonical", "target", []byte("one\n"), []byte("two\n"))
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
	got := promptIO(in, out, "src", "dest", nil, nil)
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
	if got := promptIO(in, out, "s", "d", nil, nil); got != ChoiceSkip {
		t.Errorf("got %v, want ChoiceSkip on EOF", got)
	}
}
