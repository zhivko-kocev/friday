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

// TestBareInvocationPipedKeepsPlainPath guards the v0.5.0 identity change: bare
// `friday` launches the control room only on a real terminal. Under `go test`
// stdout is a pipe, so ui.Interactive() is false and the TTY branch must NOT
// fire — bare friday keeps the exact plain usage + exit-1 path, byte-for-byte
// what printUsage prints. This is the regression guard on the invariant.
func TestBareInvocationPipedKeepsPlainPath(t *testing.T) {
	var rc int
	got := captureStdout(t, func() { rc = Run([]string{}, "test") })
	if rc != 1 {
		t.Fatalf("bare friday (piped) exit = %d, want 1", rc)
	}
	want := captureStdout(t, printUsage)
	if got != want {
		t.Errorf("bare friday output diverged from usage:\n got: %q\nwant: %q", got, want)
	}
}

// TestHelpCommandUnchanged pins `friday help` to the usage text, so a future
// slice can't accidentally reroute the plain help path.
func TestHelpCommandUnchanged(t *testing.T) {
	got := captureStdout(t, func() { Run([]string{"help"}, "test") })
	want := captureStdout(t, printUsage)
	if got != want {
		t.Errorf("`friday help` output diverged from usage:\n got: %q\nwant: %q", got, want)
	}
}

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
