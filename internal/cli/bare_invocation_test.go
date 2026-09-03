package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhivko-kocev/friday/internal/output"
)

// captureStdout (defined in status_test.go) swaps os.Stdout for a pipe while fn
// runs and returns what was written.

// TestBareInvocationAlwaysPrintsUsage guards the post-control-room invariant:
// bare `friday` has no TTY branch left to special-case. Whether stdout is a
// pipe (as it always is under `go test`) or a real terminal, the outcome is
// identical — the plain usage + exit-1 path, byte-for-byte what renderUsage
// prints, plus exactly one trailing state-aware hint line (D29). HOME is
// pinned to an empty temp dir so that hint — driven by whether a store
// exists — is deterministic regardless of the machine running this test.
// Usage stays reachable via `friday help` / `-h`.
func TestBareInvocationAlwaysPrintsUsage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)

	var rc int
	got := captureStdout(t, func() { rc = Run([]string{}, "test") })
	if rc != 1 {
		t.Fatalf("bare friday exit = %d, want 1", rc)
	}
	want := captureStdout(t, func() { renderUsage(newRootCommand("test"), false) })
	want += "No store yet: run `friday init` to create one, or `friday claim` to adopt what's already installed.\n"
	if got != want {
		t.Errorf("bare friday output diverged from usage:\n got: %q\nwant: %q", got, want)
	}
}

// --quiet promises "all non-error output" is suppressed with the exit code
// unchanged. A bare or misspelled invocation is the most reachable place that
// matters: it is exactly the shape a script takes, and the usage banner is
// printed with plain fmt, so it does not go through output's mute at all.
func TestQuietSuppressesUsageBanner(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)

	for _, args := range [][]string{{"--quiet"}, {"--quiet", "nonsuch"}} {
		var rc int
		got := captureStdout(t, func() { rc = Run(args, "test") })
		if rc != 1 {
			t.Errorf("friday %v exit = %d, want 1 (--quiet must not change exit codes)", args, rc)
		}
		if got != "" {
			t.Errorf("friday %v wrote to stdout under --quiet:\n%q", args, got)
		}
	}
}

// urfave applies a flag only when it appears in argv, so nothing resets the
// globals --verbose and --quiet write to. Run has to leave them as it found
// them, or one invocation silently reconfigures every later one in the same
// process. Written without the defensive per-test cleanup that would mask it.
func TestRunDoesNotLeakOutputFlags(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)

	captureStdout(t, func() { Run([]string{"--verbose", "--quiet", "version"}, "test") })
	if verbose {
		t.Error("--verbose leaked past Run")
	}
	if output.Quiet() {
		t.Error("--quiet leaked past Run")
	}
}

// TestHelpCommandUnchanged is retired: Phase 10's urfave/cli/v3 migration
// deliberately changed help formatting (two-tier porcelain/advanced split
// via `help --all`), so pinning `friday help` byte-for-byte to the bare
// usage text no longer applies. TestHelpAllListsExpectedCommands (help_test.go)
// is its replacement.

// TestStatusOriginShowsRuleMappings verifies the folded-in `list` view: after
// removing `friday list`, `status --origin` must enumerate each adapter's rule
// mappings (strategy: from → to), the one thing list uniquely showed.
func TestStatusOriginShowsRuleMappings(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	if err := os.MkdirAll(filepath.Join(dir, ".friday"), 0o755); err != nil {
		t.Fatal(err)
	}
	output.SetColor(false)
	t.Cleanup(func() { output.SetColor(false) })

	out := captureStdout(t, func() { Run([]string{"status", "--origin"}, "test") })
	if !strings.Contains(out, "origin:") {
		t.Fatalf("no origin section in status --origin:\n%s", out)
	}
	if !strings.Contains(out, "→") {
		t.Errorf("status --origin missing rule mappings (from → to), folded from list:\n%s", out)
	}
}

// TestStatusPlainPathStable is the whole-command regression guard: with color
// off, `friday status` must be deterministic across runs and emit no ANSI —
// the plain path the CLI, CI, and tests depend on. status embeds $HOME-relative
// absolute paths, so rather than a non-portable golden this asserts the two
// structural properties that a perturbed plain path would break. A temp HOME +
// empty store isolates it from the developer's real ~/.friday.
func TestStatusPlainPathStable(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)        // os.UserHomeDir on Unix
	t.Setenv("USERPROFILE", dir) // os.UserHomeDir on Windows
	if err := os.MkdirAll(filepath.Join(dir, ".friday"), 0o755); err != nil {
		t.Fatal(err)
	}
	output.SetColor(false)
	t.Cleanup(func() { output.SetColor(false) })

	run := func() (string, int) {
		var rc int
		out := captureStdout(t, func() { rc = Run([]string{"status"}, "test") })
		return out, rc
	}
	out1, rc1 := run()
	out2, rc2 := run()

	if rc1 == 1 {
		t.Fatalf("status errored (exit 1) on a valid empty store:\n%s", out1)
	}
	if out1 != out2 || rc1 != rc2 {
		t.Errorf("status not deterministic:\n run1 (rc=%d): %q\n run2 (rc=%d): %q", rc1, out1, rc2, out2)
	}
	if strings.Contains(out1, "\x1b[") {
		t.Errorf("plain path leaked ANSI escapes: %q", out1)
	}
}
