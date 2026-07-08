package cli

import (
	"fmt"
	"strings"
	"testing"

	"github.com/zhivko-kocev/friday/internal/output"
)

// ctx builds n context-prefixed diff lines, distinctly numbered.
func ctxLines(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("  %s%d", prefix, i)
	}
	return out
}

func TestWindowDiffSmallUnchanged(t *testing.T) {
	in := []string{"  a", "- b", "+ B", "  c"}
	out, added, removed, hunks, overflow := windowDiff(in)
	if strings.Join(out, "\n") != strings.Join(in, "\n") {
		t.Errorf("small diff should pass through unchanged:\n%v", out)
	}
	if added != 1 || removed != 1 || hunks != 1 || overflow != 0 {
		t.Errorf("counts = +%d −%d %d hunks overflow %d; want +1 −1 1 0", added, removed, hunks, overflow)
	}
}

func TestWindowDiffElidesDistantContext(t *testing.T) {
	var in []string
	in = append(in, ctxLines("top", 10)...)
	in = append(in, "+ NEW")
	in = append(in, ctxLines("bot", 10)...)
	out, added, _, hunks, overflow := windowDiff(in)
	if out[0] != "…" || out[len(out)-1] != "…" {
		t.Errorf("distant context should collapse to leading/trailing …:\n%v", out)
	}
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "+ NEW") {
		t.Errorf("the change line must survive:\n%v", out)
	}
	if strings.Contains(joined, "top0") || strings.Contains(joined, "bot9") {
		t.Errorf("far context should be elided:\n%v", out)
	}
	// 3 lines of context each side + the change + two … markers.
	if len(out) != 9 || added != 1 || hunks != 1 || overflow != 0 {
		t.Errorf("out len %d, +%d, %d hunks, overflow %d; want 9/1/1/0", len(out), added, hunks, overflow)
	}
}

func TestWindowDiffCountsHunks(t *testing.T) {
	var in []string
	in = append(in, ctxLines("a", 10)...)
	in = append(in, "- gone")
	in = append(in, ctxLines("b", 10)...)
	in = append(in, "+ added")
	in = append(in, ctxLines("c", 10)...)
	_, added, removed, hunks, _ := windowDiff(in)
	if hunks != 2 || added != 1 || removed != 1 {
		t.Errorf("counts = +%d −%d %d hunks; want +1 −1 2", added, removed, hunks)
	}
}

func TestWindowDiffCapsOverflow(t *testing.T) {
	in := make([]string, 100)
	for i := range in {
		in[i] = fmt.Sprintf("+ line%d", i)
	}
	out, added, _, _, overflow := windowDiff(in)
	if len(out) != maxDiffLines {
		t.Errorf("out len = %d, want cap %d", len(out), maxDiffLines)
	}
	if overflow != 100-maxDiffLines {
		t.Errorf("overflow = %d, want %d", overflow, 100-maxDiffLines)
	}
	if added != 100 {
		t.Errorf("added = %d, want 100 (counted before the cap)", added)
	}
}

// TestPrintDiffFooter checks the rendered footer and cap message end-to-end.
func TestPrintDiffFooter(t *testing.T) {
	output.SetColor(false)
	t.Cleanup(func() { output.SetColor(false) })
	got := captureStdout(t, func() { printDiff([]byte("a\nb\nc\n"), []byte("a\nB\nc\n")) })
	if !strings.Contains(got, "(+1 −1, 1 hunk)") {
		t.Errorf("footer missing/singular-wrong in:\n%s", got)
	}
}
