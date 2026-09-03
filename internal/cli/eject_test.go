package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/drift"
	"github.com/zhivko-kocev/friday/internal/snapshot"
)

// ejectEnv points the store and every cache at throwaway dirs, seeds a
// manifest plus one store file, and returns the paths eject is expected to
// remove (and the ones it must leave alone).
func ejectEnv(t *testing.T) (storeDir string, bookkeeping []string) {
	t.Helper()
	setDriftEnv(t)
	storeDir = t.TempDir()
	t.Setenv("FRIDAY_HOME", storeDir)

	manifest := filepath.Join(storeDir, config.ManifestName)
	if err := os.WriteFile(manifest, []byte("version: 1\nadapters: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "core.md"), []byte("# core\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	driftPath, err := drift.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(driftPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(driftPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	snapDir, err := snapshot.Dir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(snapDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return storeDir, []string{manifest, driftPath, snapDir}
}

// `friday eject claude` reads as "stop managing claude", but eject removes
// whole-install bookkeeping — the manifest, the drift cache and every rollback
// snapshot. It used to accept and silently discard the adapter name, so with
// --yes the user lost all of it unprompted while believing they had ejected one
// agent. An adapter name must be refused, and nothing removed.
func TestEjectRefusesAnAdapterArgument(t *testing.T) {
	_, bookkeeping := ejectEnv(t)

	var code int
	// The refusal itself goes to stderr; the actionable hint is the stdout half.
	out := captureStdout(t, func() { code = Run([]string{"eject", "claude", "--yes"}, "test") })
	if code == 0 {
		t.Errorf("`eject claude --yes` exited 0, want non-zero")
	}
	if !strings.Contains(out, "delete its entry") {
		t.Errorf("refusal did not say how to stop managing one agent:\n%s", out)
	}
	for _, path := range bookkeeping {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s was removed by a refused eject: %v", path, err)
		}
	}
}

// The real eject removes exactly friday's bookkeeping and leaves the store's
// own content — the user's data — in place.
func TestEjectRemovesOnlyBookkeeping(t *testing.T) {
	storeDir, bookkeeping := ejectEnv(t)

	var code int
	captureStdout(t, func() { code = Run([]string{"eject", "--yes"}, "test") })
	if code != 0 {
		t.Fatalf("eject --yes exited %d, want 0", code)
	}
	for _, path := range bookkeeping {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s survived eject", path)
		}
	}
	if _, err := os.Stat(filepath.Join(storeDir, "core.md")); err != nil {
		t.Errorf("eject removed store content: %v", err)
	}
}
