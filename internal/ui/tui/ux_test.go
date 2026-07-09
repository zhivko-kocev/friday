package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/engine"
)

// TestControlRoomHelpOverlay: `?` opens the key reference from home and any key
// closes it back to where it opened.
func TestControlRoomHelpOverlay(t *testing.T) {
	m := newModel("test", []MenuEntry{{Name: "sync", Summary: "sync"}}, &config.Config{}, nil, nil)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))
	waitForText(t, tm, "sync")
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	waitForText(t, tm, "keys")
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}) // any key closes help
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}) // back on home → quit
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

// TestChangesDiffToggle: `d` on the changes screen expands per-file diffs.
func TestChangesDiffToggle(t *testing.T) {
	m := newModel("test", nil, &config.Config{}, nil, nil)
	m.result = []engine.Change{{
		Adapter: "claude", Direction: engine.DirPush, Action: engine.ActionCreate,
		DestRel: "CLAUDE.md", NewContent: []byte("hello\nworld\n"),
	}}
	m.screen = screenChanges

	if strings.Contains(m.body(), "hello") {
		t.Errorf("diff content shown before the toggle:\n%s", m.body())
	}
	next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	nm := next.(model)
	if !nm.showDiff {
		t.Fatal("`d` did not toggle showDiff")
	}
	if !strings.Contains(nm.body(), "hello") {
		t.Errorf("diff content missing after toggle:\n%s", nm.body())
	}
}

// TestConflictModalShowsDiff: the modal renders the old→new diff, not byte counts.
func TestConflictModalShowsDiff(t *testing.T) {
	info := engine.ConflictInfo{
		Direction: engine.DirPush, DestRel: "CLAUDE.md",
		OldContent: []byte("old line\n"), NewContent: []byte("new line\n"),
	}
	out := renderConflict(info, nil, newStyles())
	if !strings.Contains(out, "old line") || !strings.Contains(out, "new line") {
		t.Errorf("conflict modal missing diff content:\n%s", out)
	}
}

// TestRenderDiffWindowsDeepEdit: an edit deep in a large file must be shown with
// context (windowed), not buried behind its unchanged prefix and cut off.
func TestRenderDiffWindowsDeepEdit(t *testing.T) {
	var oldLines, newLines []string
	for i := range 100 {
		oldLines = append(oldLines, fmt.Sprintf("line%d", i))
		newLines = append(newLines, fmt.Sprintf("line%d", i))
	}
	newLines[80] = "CHANGED-DEEP"
	old := []byte(strings.Join(oldLines, "\n") + "\n")
	newC := []byte(strings.Join(newLines, "\n") + "\n")

	out := renderDiff(old, newC, newStyles())
	if !strings.Contains(out, "CHANGED-DEEP") {
		t.Errorf("windowed diff dropped the actual edit at line 80:\n%s", out)
	}
	if strings.Contains(out, "line40") {
		t.Errorf("distant context (line40) should be elided, not shown:\n%s", out)
	}
	if !strings.Contains(out, "…") {
		t.Errorf("elided-context marker missing:\n%s", out)
	}
}

// TestSurfacesWarning: a change carrying a max_bytes-style advisory must be
// shown in both the changes view and the conflict modal — the CLI is regression-
// tested not to swallow it (report_test.go), and the control room must not either.
func TestSurfacesWarning(t *testing.T) {
	st := newStyles()
	changes := []engine.Change{{
		Adapter: "windsurf", Direction: engine.DirPush, Action: engine.ActionUpdate,
		DestRel: "global_rules.md", Warning: "over max_bytes",
	}}
	if out := renderChanges(changes, st, false); !strings.Contains(out, "over max_bytes") {
		t.Errorf("renderChanges swallowed the warning:\n%s", out)
	}
	info := engine.ConflictInfo{Direction: engine.DirPush, DestRel: "global_rules.md", Warning: "over max_bytes"}
	if out := renderConflict(info, nil, st); !strings.Contains(out, "over max_bytes") {
		t.Errorf("renderConflict swallowed the warning:\n%s", out)
	}
}

// TestHelpSuppressedDuringOp: `?` must be ignored while an apply is running or a
// conflict modal is up (only ctrl+c acts there) — opening help would strand the
// running spinner's tick loop.
func TestHelpSuppressedDuringOp(t *testing.T) {
	for _, sc := range []screen{screenRunning, screenConflict} {
		m := newModel("test", nil, &config.Config{}, nil, nil)
		m.screen = sc
		next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
		if nm := next.(model); nm.screen != sc {
			t.Errorf("`?` on screen %v opened help (now %v); it must be ignored mid-op", sc, nm.screen)
		}
	}
}

// TestResizeAndTruncation: WindowSizeMsg updates the model, and the checklist
// truncates labels wider than the terminal.
func TestResizeAndTruncation(t *testing.T) {
	m := newModel("test", nil, &config.Config{}, nil, nil)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if nm := next.(model); nm.width != 120 || nm.height != 40 {
		t.Errorf("resize not applied: %dx%d", nm.width, nm.height)
	}

	c := newChecklist("x", []checklistItem{{label: strings.Repeat("z", 200), value: "0"}})
	out := c.view(newStyles(), 10, 40)
	if strings.Contains(out, strings.Repeat("z", 200)) {
		t.Error("long label not truncated to width")
	}
	if !strings.Contains(out, "…") {
		t.Errorf("truncation ellipsis missing:\n%s", out)
	}
}
