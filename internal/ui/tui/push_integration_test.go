package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/presets"
)

// isolatedHome points every user directory friday resolves (home + cache) at a
// throwaway temp dir, so a real apply writes nowhere near the developer's own
// ~/.friday, drift cache, or snapshot store. Returns the home dir.
func isolatedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	cache := filepath.Join(t.TempDir(), "cache")
	t.Setenv("HOME", home)            // os.UserHomeDir (Unix)
	t.Setenv("USERPROFILE", home)     // os.UserHomeDir (Windows)
	t.Setenv("XDG_CACHE_HOME", cache) // os.UserCacheDir (Linux)
	t.Setenv("LocalAppData", cache)   // os.UserCacheDir (Windows)
	return home
}

// TestPushCmdPreviewThenApply is the end-to-end proof of the push flow the TUI
// wires: a dry-run preview reports changes and writes nothing, then an apply
// writes them and records a snapshot — all against a real preset-backed store,
// fully isolated from the developer's environment.
func TestPushCmdPreviewThenApply(t *testing.T) {
	home := isolatedHome(t)
	storeDir := filepath.Join(home, ".friday")
	mustWrite(t, filepath.Join(storeDir, "core.md"), "# Core\n\nHello from the store.\n")
	mustWrite(t, filepath.Join(storeDir, "rules", "general.md"), "Be precise.\n")

	p, ok := presets.Get("claude")
	if !ok {
		t.Fatal("claude preset missing")
	}
	cfg := config.NewDefault(config.ScopeUser, storeDir, home,
		map[string]*config.Adapter{"claude": p.Adapter()})

	// Preview: dry-run must produce a create/update and write nothing.
	preview := pushCmd(cfg, []string{"claude"}, false, nil)().(engineDoneMsg)
	if preview.err != nil {
		t.Fatalf("preview push: %v", preview.err)
	}
	dest := firstWrittenDest(preview.changes)
	if dest == "" {
		t.Fatalf("preview produced no create/update change; got %d changes", len(preview.changes))
	}
	if _, err := os.Stat(dest); err == nil {
		t.Fatalf("dry-run preview wrote %s — it must not touch disk", dest)
	}

	// Apply: the same push must now write the file and report no advisories.
	applied := pushCmd(cfg, []string{"claude"}, true, nil)().(engineDoneMsg)
	if applied.err != nil {
		t.Fatalf("apply push: %v", applied.err)
	}
	if len(applied.warnings) != 0 {
		t.Errorf("apply reported advisories: %v", applied.warnings)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Errorf("apply did not write %s: %v", dest, err)
	}
}

func firstWrittenDest(changes []engine.Change) string {
	for _, ch := range changes {
		if ch.Action == engine.ActionCreate || ch.Action == engine.ActionUpdate {
			return ch.DestPath
		}
	}
	return ""
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
