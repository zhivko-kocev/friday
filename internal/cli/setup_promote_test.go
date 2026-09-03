package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhivko-kocev/friday/internal/config"
)

// projectEnv seeds a store with one applicable file and chdirs into an empty
// project dir, so `setup`/`promote` have something real to plan against.
func projectEnv(t *testing.T) (store, project string) {
	t.Helper()
	setDriftEnv(t)
	store = t.TempDir()
	t.Setenv("FRIDAY_HOME", store)
	if err := os.WriteFile(filepath.Join(store, "core.md"), []byte("# core\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, config.ManifestName), []byte(
		"version: 1\nadapters:\n  claude:\n    target: ~/.claude\n    project_target: .claude\n"+
			"    rules: []\n    project_rules:\n      - from: core.md\n        to: ../CLAUDE.md\n        strategy: concatenate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	project = t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	return store, project
}

// The urfave rewrite replaced cmdSetup's int return with exitErr/ucli.Exit and
// new positional handling, none of which had a test. A dry run must plan and
// write nothing.
func TestSetupDryRunPlansWithoutWriting(t *testing.T) {
	_, project := projectEnv(t)

	var code int
	out := captureStdout(t, func() {
		code = Run([]string{"setup", "--agent", "claude", "--dry-run", "--no-interactive"}, "test")
	})
	if code != 0 {
		t.Fatalf("setup --dry-run exited %d, want 0:\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(project, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Error("setup --dry-run wrote CLAUDE.md")
	}
}

// setup takes its selection through flags and prompts, never a positional, so
// a stray argument has to be refused rather than silently ignored.
func TestSetupRejectsPositionalArgument(t *testing.T) {
	projectEnv(t)

	var code int
	captureStdout(t, func() {
		code = Run([]string{"setup", "claude", "--dry-run", "--no-interactive"}, "test")
	})
	if code == 0 {
		t.Error("setup with a positional argument exited 0, want non-zero")
	}
}

// promote --propose has nothing to propose without a message, and the check
// moved into the urfave Action during the rewrite.
func TestPromoteProposeRequiresAMessage(t *testing.T) {
	projectEnv(t)

	var code int
	out := captureBothStreamsCode(t, func() {
		code = Run([]string{"promote", "--agent", "claude", "--propose", "--dry-run", "--no-interactive"}, "test")
	})
	if code == 0 {
		t.Error("promote --propose without -m exited 0, want non-zero")
	}
	if !strings.Contains(out, "-m") && !strings.Contains(out, "message") {
		t.Errorf("error does not name the missing message:\n%s", out)
	}
}

// A promote dry run reads the project and writes nothing into the store.
func TestPromoteDryRunWritesNothing(t *testing.T) {
	store, project := projectEnv(t)
	if err := os.WriteFile(filepath.Join(project, "CLAUDE.md"), []byte("# from the project\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(store, "core.md"))
	if err != nil {
		t.Fatal(err)
	}

	captureStdout(t, func() {
		Run([]string{"promote", "--agent", "claude", "--dry-run", "--no-interactive"}, "test")
	})
	after, err := os.ReadFile(filepath.Join(store, "core.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("promote --dry-run modified the store: %q -> %q", before, after)
	}
}

// captureBothStreamsCode is captureBothStreams' single-string form, for a
// check that does not care which stream carried the message.
func captureBothStreamsCode(t *testing.T, fn func()) string {
	t.Helper()
	out, err := captureBothStreams(t, fn)
	return out + err
}
