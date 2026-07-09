package cli

import (
	"slices"
	"strings"
	"testing"
)

func TestCompletionsForCommands(t *testing.T) {
	names := completionsFor("")
	for _, want := range []string{"push", "pull", "sync", "status", "init", "setup", "share", "remote", "doctor", "undo", "completion", "version", "help"} {
		if !slices.Contains(names, want) {
			t.Errorf("top-level completions missing %q (got %v)", want, names)
		}
	}
}

func TestCompletionsForFlags(t *testing.T) {
	words := completionsFor("push")
	for _, want := range []string{"--dry-run", "--force", "--no-interactive", "--diff", "--only"} {
		if !slices.Contains(words, want) {
			t.Errorf("push completions missing %q (got %v)", want, words)
		}
	}
	words = completionsFor("remote")
	for _, want := range []string{"init", "pull", "push", "status"} {
		if !slices.Contains(words, want) {
			t.Errorf("remote completions missing %q (got %v)", want, words)
		}
	}
	if got := completionsFor("no-such-command"); got != nil {
		t.Errorf("unknown command completions = %v, want nil", got)
	}
}

// The scripts must delegate to the __complete callback rather than embed
// word lists — that's what keeps them drift-proof.
func TestCompletionScriptsDelegate(t *testing.T) {
	for shell, script := range map[string]string{
		"bash": bashCompletion,
		"zsh":  zshCompletion,
		"fish": fishCompletion,
	} {
		if !strings.Contains(script, "friday __complete") {
			t.Errorf("%s script does not call `friday __complete`", shell)
		}
	}
}

// Every table entry must dispatch — a mistyped name in the table would
// otherwise surface only at runtime.
func TestCommandTableDispatches(t *testing.T) {
	for _, c := range commandTable() {
		if c.run == nil {
			t.Errorf("command %s has no run func", c.name)
		}
		if c.name == "" {
			t.Errorf("command with empty name: %+v", c)
		}
	}
}
