// Package output centralises the CLI's user-facing print helpers so that
// every command renders status, errors, and diffs the same way.
package output

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/zhivko-kocev/friday/internal/ui/theme"
)

// useColor is set on init from environment, then optionally overridden by
// the CLI via SetColor (e.g. for a --no-color flag).
var useColor = initColor()

// initColor answers the color predicate and applies the one process-global
// side effect the answer implies. Keeping the mutation here rather than
// inside colorEnabled leaves the predicate pure, so a test (or any future
// re-query) can ask the question without permanently repinning the global
// lipgloss renderer for the rest of the process.
func initColor() bool {
	on := colorEnabled()
	if on && os.Getenv("FORCE_COLOR") != "" {
		pinColorProfile()
	}
	return on
}

func colorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("FRIDAY_NO_COLOR") != "" {
		return false
	}
	if os.Getenv("FORCE_COLOR") != "" {
		return true
	}
	return isTTY()
}

// pinColorProfile forces lipgloss to emit escapes. Its own termenv-based
// renderer auto-detects terminal color support independently of the useColor
// gate and silently downgrades to no color when stdout isn't a TTY — exactly
// the case FORCE_COLOR exists for (piping through `less -r`, capturing
// colored output to a log).
func pinColorProfile() { lipgloss.SetColorProfile(termenv.ANSI) }

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

// quiet suppresses every helper below except Err (the --quiet root flag) and
// anything printed inside WithPromptContext. Exit codes are computed
// independently of what got printed, so a script checking $?/$LASTEXITCODE
// sees no behavior change — only the progress chatter goes away, letting CI
// drop its own ">/dev/null" redirect. Machine output is likewise unaffected:
// the --json documents are written with plain fmt (internal/cli/jsonout.go),
// deliberately outside these helpers, so `--json --quiet` still emits its
// document — that combination is exactly how CI wants to call friday.
var quiet bool

// SetQuiet lets the CLI apply the --quiet root flag.
func SetQuiet(on bool) { quiet = on }

// Quiet reports whether --quiet is in effect, for the two places that print
// with plain fmt rather than through these helpers (the bare-invocation usage
// banner) and so cannot be gated by muted().
func Quiet() bool { return quiet }

// promptDepth is non-zero while a prompt's own evidence is being printed.
// The CLI prompts on one goroutine, so a plain counter is enough.
var promptDepth int

// WithPromptContext prints fn's output even under --quiet.
//
// A prompt cannot be suppressed — it blocks on stdin regardless — so the
// evidence needed to answer it must not be either: the adapter and shell
// commands behind "install this hook?", the file name behind "keep canonical
// / use target", the plan behind "apply / skip / quit". Silencing the
// evidence while leaving the question turns informed consent into a blind
// guess, and two of those three questions can destroy the user's edits.
// --quiet means "drop the progress chatter", never "stop telling me what I
// am agreeing to".
func WithPromptContext(fn func()) {
	promptDepth++
	defer func() { promptDepth-- }()
	fn()
}

// muted reports whether a helper should swallow its output.
func muted() bool { return quiet && promptDepth == 0 }

func OK(format string, args ...any) {
	if muted() {
		return
	}
	fmt.Printf("  "+col(theme.Green, "✓")+" "+format+"\n", args...)
}
func Skip(format string, args ...any) {
	if muted() {
		return
	}
	fmt.Printf("  "+col(theme.Gray, "–")+" "+format+"\n", args...)
}
func Warn(format string, args ...any) {
	if muted() {
		return
	}
	fmt.Printf("  "+col(theme.Yellow, "!")+" "+format+"\n", args...)
}
func Err(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "  "+col(theme.Red, "✗")+" "+format+"\n", args...)
}
func Info(format string, args ...any) {
	if muted() {
		return
	}
	fmt.Printf("  "+col(theme.Cyan, "→")+" "+format+"\n", args...)
}

// Header and Dim render an already-formatted string. The two-step
// "Sprintf then Print" pattern avoids a second pass where literal %
// in paths would be re-interpreted as format verbs.
func Header(s string) {
	if muted() {
		return
	}
	fmt.Print("\n" + col(theme.Bold, s) + "\n")
}
func Dim(format string, args ...any) {
	if muted() {
		return
	}
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
	if muted() {
		return
	}
	fmt.Println("  " + col(level.style(), fmt.Sprintf(format, args...)))
}

func DiffLine(line string) {
	if muted() {
		return
	}
	switch {
	case strings.HasPrefix(line, "+"):
		fmt.Println("  " + col(theme.Green, line))
	case strings.HasPrefix(line, "-"):
		fmt.Println("  " + col(theme.Red, line))
	default:
		fmt.Println("  " + col(theme.Gray, line))
	}
}
