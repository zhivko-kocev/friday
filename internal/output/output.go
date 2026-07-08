// Package output centralises the CLI's user-facing print helpers so that
// every command renders status, errors, and diffs the same way.
package output

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/zhivko-kocev/friday/internal/ui/theme"
)

// useColor is set on init from environment, then optionally overridden by
// the CLI via SetColor (e.g. for a --no-color flag).
var useColor = colorEnabled()

func colorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("FRIDAY_NO_COLOR") != "" {
		return false
	}
	return isTTY()
}

// SetColor lets the CLI override the auto-detected color preference.
func SetColor(on bool) { useColor = on }

func isTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// col renders s in the given theme style, but only when color is enabled;
// otherwise it returns s untouched so non-TTY / NO_COLOR / --no-color output
// stays plain (and byte-for-byte identical to what scripts and tests expect).
func col(style lipgloss.Style, s string) string {
	if !useColor {
		return s
	}
	return style.Render(s)
}

func OK(format string, args ...any) {
	fmt.Printf("  "+col(theme.Green, "✓")+" "+format+"\n", args...)
}
func Skip(format string, args ...any) {
	fmt.Printf("  "+col(theme.Gray, "–")+" "+format+"\n", args...)
}
func Warn(format string, args ...any) {
	fmt.Printf("  "+col(theme.Yellow, "!")+" "+format+"\n", args...)
}
func Err(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "  "+col(theme.Red, "✗")+" "+format+"\n", args...)
}
func Info(format string, args ...any) {
	fmt.Printf("  "+col(theme.Cyan, "→")+" "+format+"\n", args...)
}

// Header and Dim render an already-formatted string. The two-step
// "Sprintf then Print" pattern avoids a second pass where literal %
// in paths would be re-interpreted as format verbs.
func Header(s string) { fmt.Print("\n" + col(theme.Bold, s) + "\n") }
func Dim(format string, args ...any) {
	fmt.Println("  " + col(theme.Gray, fmt.Sprintf(format, args...)))
}

// Level is a semantic color for glyph-less tabular rows — output whose
// leading columns already encode state (the `status` grid), so an injected
// status glyph would clash with them.
type Level int

const (
	LevelInfo Level = iota // pending change
	LevelWarn              // drift / conflict — needs attention
	LevelSkip              // no-op / unsupported
)

func (l Level) style() lipgloss.Style {
	switch l {
	case LevelWarn:
		return theme.Yellow
	case LevelSkip:
		return theme.Gray
	default:
		return theme.Cyan
	}
}

// Line prints a two-space-indented row colored by level, with no status
// glyph — the caller supplies its own leading marker. In plain mode it emits
// exactly its text, byte-for-byte, like every other helper.
func Line(level Level, format string, args ...any) {
	fmt.Println("  " + col(level.style(), fmt.Sprintf(format, args...)))
}

func DiffLine(line string) {
	switch {
	case strings.HasPrefix(line, "+"):
		fmt.Println("  " + col(theme.Green, line))
	case strings.HasPrefix(line, "-"):
		fmt.Println("  " + col(theme.Red, line))
	default:
		fmt.Println("  " + col(theme.Gray, line))
	}
}
