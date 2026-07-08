// Package ui holds friday's interactive terminal surfaces — selection prompts
// and a spinner — built on the Charm stack (huh / bubbletea / bubbles).
//
// Every entry point is meant to be called only after Interactive() returns
// true. Callers keep their existing plain, line-based path for the
// non-interactive case (pipes, CI, --no-interactive, tests), so the
// deterministic output the rest of the tool and its tests depend on is never
// replaced — the TUI is strictly additive.
package ui

import (
	"os"

	"github.com/charmbracelet/huh"
	"github.com/mattn/go-isatty"
)

// Interactive reports whether a rich terminal UI can run: both stdin and
// stdout must be terminals. Anything piped or redirected (CI, tests, a shell
// pipeline) returns false and the caller falls back to plain prompts.
func Interactive() bool {
	return isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stdout.Fd())
}

// Choice is one selectable option. Value is what the caller gets back; Label is
// what the user sees; On pre-checks it (multi-select only).
type Choice struct {
	Value string
	Label string
	On    bool
}

// SelectOne shows a single-choice list and returns the chosen Value.
func SelectOne(title string, choices []Choice) (string, error) {
	opts := make([]huh.Option[string], len(choices))
	for i, c := range choices {
		opts[i] = huh.NewOption(c.Label, c.Value)
	}
	var out string
	if err := huh.NewSelect[string]().Title(title).Options(opts...).Value(&out).Run(); err != nil {
		return "", err
	}
	return out, nil
}

// MultiSelect shows a checkbox list (space toggles, enter confirms) and returns
// the chosen Values. Choices with On set start checked.
func MultiSelect(title string, choices []Choice) ([]string, error) {
	opts := make([]huh.Option[string], len(choices))
	for i, c := range choices {
		o := huh.NewOption(c.Label, c.Value)
		if c.On {
			o = o.Selected(true)
		}
		opts[i] = o
	}
	var out []string
	if err := huh.NewMultiSelect[string]().Title(title).Options(opts...).Value(&out).Run(); err != nil {
		return nil, err
	}
	return out, nil
}

// Confirm asks a yes/no question, defaulting to no. A cancelled prompt
// (ctrl-c / esc) returns false.
func Confirm(title string) bool {
	var ok bool
	if err := huh.NewConfirm().Title(title).Value(&ok).Run(); err != nil {
		return false
	}
	return ok
}

// Input reads a single line of free text. placeholder is shown greyed until
// the user types. A cancelled prompt returns "".
func Input(title, placeholder string) (string, error) {
	var out string
	err := huh.NewInput().Title(title).Placeholder(placeholder).Value(&out).Run()
	return out, err
}
