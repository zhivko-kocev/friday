package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	ucli "github.com/urfave/cli/v3"

	"github.com/zhivko-kocev/friday/internal/output"
)

// findCommand locates a top-level command by name in a fresh root graph —
// shared by tests that assert on flag/category wiring instead of stdout.
func findCommand(t *testing.T, name string) *ucli.Command {
	t.Helper()
	root := newRootCommand("test")
	for _, c := range root.Commands {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("command %q not found in root command graph", name)
	return nil
}

// TestHelpAllListsExpectedCommands is TestHelpCommandUnchanged's replacement:
// Phase 10 deliberately changed help *formatting*, but the two-tier
// porcelain/advanced split itself is a real contract this pins instead.
func TestHelpAllListsExpectedCommands(t *testing.T) {
	output.SetColor(false)
	t.Cleanup(func() { output.SetColor(false) })

	short := captureStdout(t, func() { Run([]string{"help"}, "test") })
	for _, want := range []string{"init", "setup", "sync", "status", "share"} {
		if !strings.Contains(short, want) {
			t.Errorf("friday help missing porcelain command %q:\n%s", want, short)
		}
	}
	for _, absent := range []string{"push", "pull", "rollback", "doctor", "completion"} {
		if strings.Contains(short, absent) {
			t.Errorf("friday help should not list advanced command %q without --all:\n%s", absent, short)
		}
	}

	full := captureStdout(t, func() { Run([]string{"help", "--all"}, "test") })
	for _, want := range []string{"push", "pull", "promote", "undo", "eject", "remote", "doctor", "completion", "version"} {
		if !strings.Contains(full, want) {
			t.Errorf("friday help --all missing advanced command %q:\n%s", want, full)
		}
	}
}

// TestBareHelpFlagMatchesHelpCommand pins the ShowRootCommandHelp /
// ShowSubcommandHelp override: `friday -h`/`friday --help` on the root must
// render the same short banner as `friday help`, not urfave's generic
// per-command template (which is what a naive override would leave in
// place for the flag-triggered path — see cli.go's init for why both
// package vars need overriding).
func TestBareHelpFlagMatchesHelpCommand(t *testing.T) {
	output.SetColor(false)
	t.Cleanup(func() { output.SetColor(false) })

	viaCommand := captureStdout(t, func() { Run([]string{"help"}, "test") })
	viaLongFlag := captureStdout(t, func() { Run([]string{"--help"}, "test") })
	viaShortFlag := captureStdout(t, func() { Run([]string{"-h"}, "test") })
	if viaCommand != viaLongFlag {
		t.Errorf("friday --help diverged from friday help:\n--help: %q\nhelp:   %q", viaLongFlag, viaCommand)
	}
	if viaCommand != viaShortFlag {
		t.Errorf("friday -h diverged from friday help:\n-h:   %q\nhelp: %q", viaShortFlag, viaCommand)
	}
}

// TestPerCommandHelpListsItsOwnFlags guards the other half of the help
// split: a subcommand's own -h must come from urfave's generated template
// (accurate to its real Flags), not the root banner.
func TestPerCommandHelpListsItsOwnFlags(t *testing.T) {
	output.SetColor(false)
	t.Cleanup(func() { output.SetColor(false) })

	out := captureStdout(t, func() { Run([]string{"push", "--help"}, "test") })
	for _, want := range []string{"--dry-run", "--force", "--only"} {
		if !strings.Contains(out, want) {
			t.Errorf("friday push --help missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Common commands:") {
		t.Errorf("friday push --help rendered the root banner instead of its own help:\n%s", out)
	}
}

// TestCompletionCommandEmitsScripts guards the porting from the old
// friday-authored bash/zsh/fish heredocs (which delegated to a hidden
// `friday __complete` callback) to urfave's own EnableShellCompletion
// mechanism. The exact script bytes are allowed to change (Phase 10 grants
// latitude on "completion scripts"); what must hold is that the command
// still dispatches per shell and the script actually names the binary.
func TestCompletionCommandEmitsScripts(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		var rc int
		out := captureStdout(t, func() { rc = Run([]string{"completion", shell}, "test") })
		if rc != 0 {
			t.Errorf("friday completion %s exited %d, want 0", shell, rc)
		}
		if !strings.Contains(out, "friday") {
			t.Errorf("friday completion %s produced no usable script:\n%s", shell, out)
		}
	}
}

// TestPushShellCompletesAdapterNames is the dynamic-value replacement for
// the old completesAdapters bool: push's ShellComplete must surface the
// store's adapter names to `--generate-shell-completion`.
func TestPushShellCompletesAdapterNames(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	if err := os.MkdirAll(filepath.Join(dir, ".friday"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "version: 1\nadapters:\n  zzz-test-adapter:\n    target: " + filepath.ToSlash(dir) + "\n    rules: []\n"
	if err := os.WriteFile(filepath.Join(dir, ".friday", "friday.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() { Run([]string{"push", "--generate-shell-completion"}, "test") })
	if !strings.Contains(out, "zzz-test-adapter") {
		t.Errorf("push --generate-shell-completion missing adapter name:\n%s", out)
	}
}

// TestEveryCommandLandsInAHelpTier is the drift guard the two-tier split
// needs: printTier filters on the literal strings "porcelain"/"advanced",
// so a command with an unset or misspelled Category still dispatches and
// still tab-completes but is mentioned nowhere in help. Asserting over the
// live graph covers every future command for free; the hand-maintained name
// list in TestHelpAllListsExpectedCommands cannot.
func TestEveryCommandLandsInAHelpTier(t *testing.T) {
	output.SetColor(false)
	t.Cleanup(func() { output.SetColor(false) })

	full := captureStdout(t, func() { Run([]string{"help", "--all"}, "test") })
	for _, c := range newRootCommand("test").Commands {
		// help is deliberately uncategorised: the footer advertises it.
		if c.Hidden || c.Name == "help" {
			continue
		}
		if c.Category != "porcelain" && c.Category != "advanced" {
			t.Errorf("command %q has Category %q; want porcelain or advanced (otherwise it never appears in help)", c.Name, c.Category)
			continue
		}
		if c.Usage == "" {
			t.Errorf("command %q has an empty Usage, so its help line is blank", c.Name)
		}
		if !strings.Contains(full, c.Name) {
			t.Errorf("command %q missing from `friday help --all`:\n%s", c.Name, full)
		}
	}
}

// TestHelpAllListsPerCommandFlags guards the `--all` flag section. Three
// separate strings promise "all flags"/"flag details"; the section that
// delivered them was dropped in the urfave migration and the promises
// survived. It is derived from the command graph now, so a new flag needs
// no help edit.
func TestHelpAllListsPerCommandFlags(t *testing.T) {
	output.SetColor(false)
	t.Cleanup(func() { output.SetColor(false) })

	full := captureStdout(t, func() { Run([]string{"help", "--all"}, "test") })
	for _, want := range []string{"friday push", "--only", "friday status", "--origin", "friday remote propose", "--branch"} {
		if !strings.Contains(full, want) {
			t.Errorf("friday help --all missing flag detail %q:\n%s", want, full)
		}
	}
	short := captureStdout(t, func() { Run([]string{"help"}, "test") })
	if strings.Contains(short, "Command flags:") {
		t.Errorf("short help leaked the --all flag section:\n%s", short)
	}
}

// TestRootHelpFlagHonoursAll pins `friday --help --all`, which selected the
// full manual before --help was remapped onto urfave's own help path.
func TestRootHelpFlagHonoursAll(t *testing.T) {
	output.SetColor(false)
	t.Cleanup(func() { output.SetColor(false) })

	viaCommand := captureStdout(t, func() { Run([]string{"help", "--all"}, "test") })
	viaFlag := captureStdout(t, func() { Run([]string{"--help", "--all"}, "test") })
	if viaCommand != viaFlag {
		t.Errorf("friday --help --all diverged from friday help --all:\n--help --all: %q\nhelp --all:   %q", viaFlag, viaCommand)
	}
}

// TestRootAllWithoutHelpIsAnError keeps the root's --all from silently
// swallowing a mistyped `friday --all pull` (which would then run pull
// *without* --all).
func TestRootAllWithoutHelpIsAnError(t *testing.T) {
	output.SetColor(false)
	t.Cleanup(func() { output.SetColor(false) })

	if rc := Run([]string{"--all", "pull"}, "test"); rc != 1 {
		t.Errorf("friday --all pull exited %d, want 1", rc)
	}
}

// TestCompletionWithoutShellFails guards the exit-0-on-failure path: the
// documented install is `friday completion bash > /etc/bash_completion.d/friday`,
// so a forgotten or misspelled shell must not write a help page into a file
// the shell sources at every login, and must not exit 0.
func TestCompletionWithoutShellFails(t *testing.T) {
	output.SetColor(false)
	t.Cleanup(func() { output.SetColor(false) })

	for _, args := range [][]string{{"completion"}, {"completion", "powershell"}, {"completion", "bogus"}} {
		var rc int
		out := captureStdout(t, func() { rc = Run(args, "test") })
		if rc != 1 {
			t.Errorf("friday %v exited %d, want 1", args, rc)
		}
		if out != "" {
			t.Errorf("friday %v wrote %q to stdout; a redirect would capture it as a completion script", args, out)
		}
	}
}

// TestLegacyCompleteCallbackStillAnswers guards the shell scripts users
// installed to persistent locations before v1.0: they call `friday
// __complete`, and without that command an upgraded binary answered with
// its usage banner, which the scripts then offered as candidates.
func TestLegacyCompleteCallbackStillAnswers(t *testing.T) {
	output.SetColor(false)
	t.Cleanup(func() { output.SetColor(false) })

	var rc int
	out := captureStdout(t, func() { rc = Run([]string{"__complete"}, "test") })
	if rc != 0 {
		t.Fatalf("friday __complete exited %d, want 0", rc)
	}
	if strings.Contains(out, "Usage:") {
		t.Fatalf("friday __complete printed the usage banner instead of candidates:\n%s", out)
	}
	words := strings.Fields(out)
	// undo is rollback's alias and completion is Hidden for urfave's own
	// bookkeeping — both were completable before the migration.
	for _, want := range []string{"push", "rollback", "undo", "completion"} {
		if !slices.Contains(words, want) {
			t.Errorf("friday __complete missing %q:\n%s", want, out)
		}
	}
	if slices.Contains(words, "__complete") {
		t.Errorf("friday __complete offered itself as a candidate:\n%s", out)
	}
}

// TestLegacyCompleteCallbackKnowsCommandFlags is the second half of the old
// protocol: `friday __complete <cmd>` lists that command's flags and, for
// adapter-positional commands, the live adapter names.
func TestLegacyCompleteCallbackKnowsCommandFlags(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	if err := os.MkdirAll(filepath.Join(dir, ".friday"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "version: 1\nadapters:\n  zzz-test-adapter:\n    target: " + filepath.ToSlash(dir) + "\n    rules: []\n"
	if err := os.WriteFile(filepath.Join(dir, ".friday", "friday.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() { Run([]string{"__complete", "push"}, "test") })
	words := strings.Fields(out)
	for _, want := range []string{"--dry-run", "--only", "zzz-test-adapter"} {
		if !slices.Contains(words, want) {
			t.Errorf("friday __complete push missing %q:\n%s", want, out)
		}
	}

	// Aliases resolve too — the old callback took whatever the shell had
	// on the line, which may be the alias.
	viaAlias := captureStdout(t, func() { Run([]string{"__complete", "undo"}, "test") })
	if !strings.Contains(viaAlias, "--list") {
		t.Errorf("friday __complete undo missing rollback's --list:\n%s", viaAlias)
	}
}

// TestRootShellCompleteIncludesAliasesAndCompletion covers the modern
// protocol's half of the same gap: urfave's printCommandSuggestions emits
// Name only and skips Hidden commands.
func TestRootShellCompleteIncludesAliasesAndCompletion(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)

	out := captureStdout(t, func() { Run([]string{"--generate-shell-completion"}, "test") })
	for _, want := range []string{"undo:", "completion:", "push:"} {
		if !strings.Contains(out, want) {
			t.Errorf("root --generate-shell-completion missing %q:\n%s", want, out)
		}
	}
}
