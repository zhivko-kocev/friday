package tui

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// checklistItem is one toggleable row. value is what the caller reads back;
// label is what the user sees.
type checklistItem struct {
	label   string
	value   string
	checked bool
}

// checklist is a minimal multiselect: bubbles/list has no checkbox, and huh's
// MultiSelect runs its own tea.Program (which would deadlock nested inside the
// control room), so the control room owns this one. space toggles, `a`
// toggles all, up/down move the cursor; the parent model owns enter/esc.
type checklist struct {
	title  string
	items  []checklistItem
	cursor int
}

func newChecklist(title string, items []checklistItem) checklist {
	return checklist{title: title, items: items}
}

// update handles the navigation/toggle keys the checklist owns. enter and esc
// are left to the parent screen.
func (c checklist) update(msg tea.KeyMsg) checklist {
	switch msg.String() {
	case "up", "k":
		if c.cursor > 0 {
			c.cursor--
		}
	case "down", "j":
		if c.cursor < len(c.items)-1 {
			c.cursor++
		}
	case " ":
		if c.cursor < len(c.items) {
			c.items[c.cursor].checked = !c.items[c.cursor].checked
		}
	case "a":
		all := !c.allChecked()
		for i := range c.items {
			c.items[i].checked = all
		}
	}
	return c
}

func (c checklist) allChecked() bool {
	return len(c.items) > 0 && !slices.ContainsFunc(c.items, func(it checklistItem) bool { return !it.checked })
}

// checked returns the values of every ticked row, in display order.
func (c checklist) checked() []string {
	var out []string
	for _, it := range c.items {
		if it.checked {
			out = append(out, it.value)
		}
	}
	return out
}

// anyChecked reports whether at least one row is ticked (the View calls this
// every frame to pick the footer hint).
func (c checklist) anyChecked() bool {
	return slices.ContainsFunc(c.items, func(it checklistItem) bool { return it.checked })
}

// view renders the list windowed to at most maxRows item rows, scrolling to keep
// the cursor visible and marking hidden rows above/below — so a list longer than
// the terminal stays navigable. maxRows <= 0 shows everything. Labels are
// truncated to width so a long path can't overflow a narrow terminal (width <= 0
// disables truncation).
func (c checklist) view(st styles, maxRows, width int) string {
	var b strings.Builder
	b.WriteString(st.title.Render(c.title))
	b.WriteString("\n\n")

	top, bottom := 0, len(c.items)
	if maxRows > 0 && len(c.items) > maxRows {
		top = c.cursor - maxRows/2 // center the cursor
		if top < 0 {
			top = 0
		}
		if max := len(c.items) - maxRows; top > max {
			top = max
		}
		bottom = top + maxRows
	}

	if top > 0 {
		b.WriteString(st.footer.Render(fmt.Sprintf("  ↑ %d more", top)) + "\n")
	}
	for i := top; i < bottom; i++ {
		it := c.items[i]
		cursor := "  "
		if i == c.cursor {
			cursor = "> "
		}
		box := "[ ]"
		if it.checked {
			box = "[x]"
		}
		label := it.label
		// Truncate on grapheme/display-width boundaries (ansi.Truncate + lipgloss
		// .Width), not byte offsets — a byte slice would split a multi-byte rune in
		// a non-ASCII path and emit invalid UTF-8. cursor/box are ASCII, so len() is
		// their display width. ansi.Truncate's length budget includes the "…" tail.
		if prefix := len(cursor) + len(box) + 1; width > prefix+1 && lipgloss.Width(label) > width-prefix {
			label = ansi.Truncate(label, width-prefix, "…")
		}
		line := cursor + box + " " + label
		if i == c.cursor {
			line = st.selected.Render(line)
		}
		b.WriteString(line + "\n")
	}
	if bottom < len(c.items) {
		b.WriteString(st.footer.Render(fmt.Sprintf("  ↓ %d more", len(c.items)-bottom)))
	}
	return b.String()
}
