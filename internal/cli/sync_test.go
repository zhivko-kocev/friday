package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhivko-kocev/friday/internal/output"
)

// TestSyncStopsFanOutAfterFailedCapture is the data-loss guard: sync used to
// run the push half unconditionally, so a pull that bailed part-way left the
// adapters it never reached holding uncaptured edits — and then pushed the
// store over them. Under --force that overwrite happens with no prompt.
func TestSyncStopsFanOutAfterFailedCapture(t *testing.T) {
	output.SetColor(false)
	t.Cleanup(func() { output.SetColor(false) })
	// Closed stdin with pending pull work is the reproduction: the apply
	// prompt reads EOF, pullPerAdapter reports exit 2 and applies nothing.
	answerStdin(t, "")

	setDriftEnv(t)
	home, _ := os.UserHomeDir()
	store := filepath.Join(home, ".friday")
	target := filepath.Join(home, "tgt")
	for _, dir := range []string{store, target} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(store, "core.md"), []byte("store version\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The agent's copy differs, so the pull half has something to capture.
	if err := os.WriteFile(filepath.Join(target, "OUT.md"), []byte("agent edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := "version: 1\nadapters:\n  test:\n    target: " + filepath.ToSlash(target) +
		"\n    rules:\n      - from: [core.md]\n        to: OUT.md\n"
	if err := os.WriteFile(filepath.Join(store, "friday.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	var code int
	got := captureStdout(t, func() { code = Run([]string{"sync", "test"}, "test") })
	if code == 0 {
		t.Errorf("sync exited 0 after a failed capture:\n%s", got)
	}
	if !strings.Contains(got, "sync: pull") {
		t.Fatalf("pull half never ran:\n%s", got)
	}
	if strings.Contains(got, "sync: push") {
		t.Errorf("sync fanned out after a failed capture:\n%s", got)
	}
	// The store must still hold its own version — nothing was captured.
	body, err := os.ReadFile(filepath.Join(store, "core.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "store version\n" {
		t.Errorf("store core.md = %q, want it untouched", body)
	}
	// And the agent's uncaptured edit must survive.
	body, err = os.ReadFile(filepath.Join(target, "OUT.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "agent edit\n" {
		t.Errorf("target OUT.md = %q, want the uncaptured edit intact", body)
	}
}
