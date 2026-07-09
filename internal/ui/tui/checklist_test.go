package tui

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func runeKey(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

func TestChecklistWindowing(t *testing.T) {
	items := make([]checklistItem, 10)
	for i := range items {
		items[i] = checklistItem{label: fmt.Sprintf("item-%d", i), value: strconv.Itoa(i)}
	}
	c := newChecklist("pick", items)
	// Move the cursor near the end so windowing must scroll.
	for i := 0; i < 8; i++ {
		c = c.update(tea.KeyMsg{Type: tea.KeyDown})
	}
	out := c.view(newStyles(), 4, 80) // window of 4 rows over 10 items

	if !strings.Contains(out, "item-8") {
		t.Errorf("cursor row (item-8) not visible in windowed view:\n%s", out)
	}
	if !strings.Contains(out, "more") {
		t.Errorf("expected a scroll indicator (\"N more\") in windowed view:\n%s", out)
	}
	if strings.Contains(out, "item-0") {
		t.Errorf("far-off row (item-0) should be scrolled out of a 4-row window:\n%s", out)
	}
}

func TestChecklistToggleAndCursor(t *testing.T) {
	c := newChecklist("pick", []checklistItem{
		{label: "claude", value: "claude"},
		{label: "codex", value: "codex"},
	})
	if got := c.checked(); len(got) != 0 {
		t.Fatalf("fresh checklist: want none checked, got %v", got)
	}

	// space toggles the cursor row (row 0).
	c = c.update(runeKey(' '))
	if got := c.checked(); len(got) != 1 || got[0] != "claude" {
		t.Fatalf("after space on row 0: got %v", got)
	}

	// down then space ticks row 1 too.
	c = c.update(tea.KeyMsg{Type: tea.KeyDown})
	c = c.update(runeKey(' '))
	if got := c.checked(); len(got) != 2 {
		t.Fatalf("after ticking both: got %v", got)
	}

	// `a` toggles all — all are checked, so it clears them.
	c = c.update(runeKey('a'))
	if got := c.checked(); len(got) != 0 {
		t.Fatalf("toggle-all off: got %v", got)
	}
	// `a` again ticks everything.
	c = c.update(runeKey('a'))
	if got := c.checked(); len(got) != 2 {
		t.Fatalf("toggle-all on: got %v", got)
	}

	// cursor never runs past the ends.
	c = c.update(tea.KeyMsg{Type: tea.KeyDown})
	c = c.update(tea.KeyMsg{Type: tea.KeyDown})
	if c.cursor != 1 {
		t.Fatalf("cursor ran past end: %d", c.cursor)
	}
	c = c.update(tea.KeyMsg{Type: tea.KeyUp})
	c = c.update(tea.KeyMsg{Type: tea.KeyUp})
	if c.cursor != 0 {
		t.Fatalf("cursor ran past start: %d", c.cursor)
	}
}
