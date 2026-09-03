package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// seedStore creates a minimal user store (one core.md) plus the target dir
// of every named adapter, so installedAdapters(cfg) resolves to exactly
// those names. Mirrors what a real machine looks like: a store with content
// and one or two agents actually installed.
func seedStore(t *testing.T, installed ...string) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	storeDir := filepath.Join(home, ".friday")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "core.md"), []byte("be helpful"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, dir := range installed {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

// TestFirstRunHintNoStore guards the "nothing here yet" state: with no
// $HOME/.friday at all, the hint must point at init/claim, the only two
// commands that can create one.
func TestFirstRunHintNoStore(t *testing.T) {
	setDriftEnv(t)

	got := firstRunHint()
	want := "No store yet: run `friday init` to create one, or `friday claim` to adopt what's already installed."
	if got != want {
		t.Errorf("firstRunHint() = %q, want %q", got, want)
	}
}

// TestFirstRunHintPendingChanges guards the "store exists, nothing pushed
// yet" state: an installed agent whose target dir holds nothing friday wrote
// has a dry-run push full of creates, so the hint must point at `friday sync`.
func TestFirstRunHintPendingChanges(t *testing.T) {
	setDriftEnv(t)
	seedStore(t, ".claude")

	got := firstRunHint()
	want := "Pending changes: run `friday sync` to apply them."
	if got != want {
		t.Errorf("firstRunHint() = %q, want %q", got, want)
	}
}

// TestFirstRunHintClearsAfterSync is the regression guard for the nag loop:
// bare `friday` used to plan every *configured* adapter (all seven presets)
// while `friday sync` pushes only the *installed* ones, so the six agents
// this machine doesn't have stayed pending forever and no amount of syncing
// could clear the hint. Driving the sync through the CLI is the point — the
// old version of this test called engine.Push with no adapter filter, which
// reached a state no user can, and that is how the bug shipped.
func TestFirstRunHintClearsAfterSync(t *testing.T) {
	setDriftEnv(t)
	seedStore(t, ".claude")

	if rc := Run([]string{"sync", "--no-interactive"}, "test"); rc != 0 {
		t.Fatalf("friday sync exited %d, want 0", rc)
	}
	if got := firstRunHint(); got != "" {
		t.Errorf("firstRunHint() = %q after a successful sync, want \"\" — bare `friday` is nagging in a loop", got)
	}
	// Syncing again must stay a no-op: the loop the reviewer reproduced was
	// sync/hint/sync/hint with the hint never going away.
	if rc := Run([]string{"sync", "--no-interactive"}, "test"); rc != 0 {
		t.Fatalf("second friday sync exited %d, want 0", rc)
	}
	if got := firstRunHint(); got != "" {
		t.Errorf("firstRunHint() = %q after a second sync, want \"\"", got)
	}
}

// TestFirstRunHintNoInstalledAgents pins the other half of the filter: with
// a store but no agent installed, `friday sync` prints "no installed agents
// detected" and exits 0, so there is no pending work to nudge toward.
func TestFirstRunHintNoInstalledAgents(t *testing.T) {
	setDriftEnv(t)
	seedStore(t)

	if got := firstRunHint(); got != "" {
		t.Errorf("firstRunHint() = %q with no installed agent, want \"\"", got)
	}
}

// TestRewriteDeprecatedFlagsAfterGlobalFlag guards both deprecation shims
// against a leading global flag. --no-color/--quiet/--verbose are documented
// as working with any command, so anchoring the rewrite on args[0] made
// `friday --no-color pull --force` die with "flag provided but not defined"
// — an invocation that worked before the urfave migration.
func TestRewriteDeprecatedFlagsAfterGlobalFlag(t *testing.T) {
	cases := []struct {
		name      string
		in        []string
		want      []string
		wantNotes int
	}{
		{"pull bare", []string{"pull", "--force"}, []string{"pull", "--all"}, 1},
		{"pull after global", []string{"--no-color", "pull", "--force"}, []string{"--no-color", "pull", "--all"}, 1},
		{"pull after two globals", []string{"--quiet", "--verbose", "pull", "-force"}, []string{"--quiet", "--verbose", "pull", "--all"}, 1},
		{"init after global", []string{"--no-color", "init", "--remote", "u"}, []string{"--no-color", "init", "--from-git", "u"}, 1},
		{"canonical spelling, no note", []string{"pull", "--all"}, []string{"pull", "--all"}, 0},
		{"other command untouched", []string{"--no-color", "push", "--force"}, []string{"--no-color", "push", "--force"}, 0},
		{"terminator stops the scan", []string{"--", "pull", "--force"}, []string{"--", "pull", "--force"}, 0},
		{"flags only", []string{"--no-color"}, []string{"--no-color"}, 0},
		{"empty", []string{}, []string{}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := slices.Clone(c.in)
			got, notes := rewriteDeprecatedFlags(in)
			if !slices.Equal(got, c.want) {
				t.Errorf("rewriteDeprecatedFlags(%v) = %v, want %v", c.in, got, c.want)
			}
			if len(notes) != c.wantNotes {
				t.Errorf("rewriteDeprecatedFlags(%v) notes = %v, want %d", c.in, notes, c.wantNotes)
			}
			if !slices.Equal(in, c.in) {
				t.Errorf("rewriteDeprecatedFlags mutated its argument: %v, was %v", in, c.in)
			}
		})
	}
}

// TestGlobalFlagBeforeDeprecatedFlagDispatches is the end-to-end half:
// the rewritten tokens must actually reach the command, not just look right
// in a unit test. --dry-run keeps it read-only.
func TestGlobalFlagBeforeDeprecatedFlagDispatches(t *testing.T) {
	setDriftEnv(t)
	seedStore(t, ".claude")

	if rc := Run([]string{"--no-color", "pull", "--force", "--dry-run"}, "test"); rc != 0 {
		t.Errorf("friday --no-color pull --force --dry-run exited %d, want 0", rc)
	}
}

// TestManifestWarningsPrintedOnce pins the once-per-run contract for
// cfg.Warnings: a manifest written by an older friday must degrade loudly,
// but the several config loads a single command performs must not repeat the
// same line. Warnings go to stderr so `--json` output stays parseable.
func TestManifestWarningsPrintedOnce(t *testing.T) {
	setDriftEnv(t)
	home := seedStore(t, ".claude")
	manifest := "version: 1\nadapters:\n  antigravity:\n    target: " +
		filepath.ToSlash(filepath.Join(home, ".antigravity")) + "\n    rules: []\n"
	if err := os.WriteFile(filepath.Join(home, ".friday", "friday.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	manifestWarningsShown = false
	out := captureStdouterr(t, func() {
		if _, err := loadUserOrDefault(); err != nil {
			t.Fatalf("loadUserOrDefault: %v", err)
		}
		if _, err := loadUserOrDefault(); err != nil {
			t.Fatalf("loadUserOrDefault: %v", err)
		}
	})
	if n := strings.Count(out, "friday migrate"); n != 1 {
		t.Errorf("rename warning printed %d time(s) across two loads, want exactly 1:\n%s", n, out)
	}
	if !strings.Contains(out, "antigravity") {
		t.Errorf("rename warning does not name the stale adapter:\n%s", out)
	}
}
