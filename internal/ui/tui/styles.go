package tui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/ui/theme"
)

// styles derives the composite lipgloss styles the bubbles components need from
// theme's six color-only styles, so the control room never introduces a second
// color source. theme stays authoritative (see internal/ui/theme) and the plain
// output path (internal/output) is untouched.
type styles struct {
	title    lipgloss.Style // screen title
	footer   lipgloss.Style // key-hint line
	errText  lipgloss.Style // store-absent / op-error message
	warn     lipgloss.Style // non-fatal advisory (a success still happened)
	ok       lipgloss.Style // positive confirmation (e.g. "applied")
	changeHd lipgloss.Style // "changes:" header inside the viewport
	selected lipgloss.Style // highlighted row (checklist cursor)
}

func newStyles() styles {
	return styles{
		title:    theme.Bold.Padding(0, 1),
		footer:   theme.Gray,
		errText:  theme.Red,
		warn:     theme.Yellow,
		ok:       theme.Green,
		changeHd: theme.Bold,
		selected: theme.Cyan.Bold(true),
	}
}

// action colors the label of a planned change by its disk effect, reusing the
// same theme palette the plain report uses (create/update read as go-ahead,
// conflict as attention, in-sync/skip as context).
func (s styles) action(a engine.Action) lipgloss.Style {
	switch a {
	case engine.ActionCreate:
		return theme.Green
	case engine.ActionUpdate:
		return theme.Yellow
	case engine.ActionConflict:
		return theme.Red
	default: // in-sync, missing-source, unsupported
		return theme.Gray
	}
}
