// Package theme is friday's single source of color truth. Both the plain-line
// output helpers (internal/output) and the interactive surfaces (internal/ui)
// render from these lipgloss styles, so terminal colors never drift between the
// two layers.
//
// The styles only describe color — they do not decide whether color is applied.
// Callers gate on their own TTY / NO_COLOR / --no-color preference (see
// internal/output) and render through these styles only when color is wanted.
// The ANSI palette indices below mirror the raw escapes friday used before
// lipgloss (green 32, red 31, yellow 33, cyan 36, gray 90, bold 1), so a
// colored line renders byte-for-byte as it always has.
package theme

import "github.com/charmbracelet/lipgloss"

var (
	Green  = lipgloss.NewStyle().Foreground(lipgloss.Color("2")) // OK glyph
	Red    = lipgloss.NewStyle().Foreground(lipgloss.Color("1")) // error glyph
	Yellow = lipgloss.NewStyle().Foreground(lipgloss.Color("3")) // warning glyph
	Cyan   = lipgloss.NewStyle().Foreground(lipgloss.Color("6")) // info glyph
	Gray   = lipgloss.NewStyle().Foreground(lipgloss.Color("8")) // dim / skip / context
	Bold   = lipgloss.NewStyle().Bold(true)                      // headers
)
