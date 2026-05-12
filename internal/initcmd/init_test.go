package initcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/presets"
)

// withTempHome redirects $HOME (and $USERPROFILE on Windows) so config.UserStoreDir
// resolves into a t.TempDir(). Returns the resolved store path for assertions.
func withTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir prefers this on Windows
	return filepath.Join(home, ".friday")
}

func TestRunRejectsExistingStore(t *testing.T) {
	storeDir := withTempHome(t)
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "marker"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Run(strings.NewReader("\n"))
	if err == nil {
		t.Fatal("expected error when ~/.friday already exists")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error message missing 'already exists': %v", err)
	}
}

func TestRunScaffoldsOnBlankInput(t *testing.T) {
	storeDir := withTempHome(t)
	if err := Run(strings.NewReader("\n")); err != nil {
		t.Fatalf("Run with blank input failed: %v", err)
	}

	// Skeleton files
	for _, rel := range []string{"identity.md", "rules/general.md", ".gitignore", "friday.yaml"} {
		if _, err := os.Stat(filepath.Join(storeDir, rel)); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}
	// Empty subdirs with .gitkeep
	for _, sub := range []string{"rules", "agents", "commands", "skills"} {
		if _, err := os.Stat(filepath.Join(storeDir, sub, ".gitkeep")); err != nil {
			t.Errorf("missing %s/.gitkeep: %v", sub, err)
		}
	}

	// friday.yaml seeded with every built-in preset
	cfg, err := config.LoadUser()
	if err != nil {
		t.Fatalf("LoadUser after scaffold: %v", err)
	}
	if len(cfg.Adapters) != len(presets.Names()) {
		t.Errorf("manifest has %d adapters, want %d (all presets)", len(cfg.Adapters), len(presets.Names()))
	}
}

func TestRunCloneRejectsFlagLikeURL(t *testing.T) {
	withTempHome(t)
	err := Run(strings.NewReader("--upload-pack=evil\n"))
	if err == nil {
		t.Fatal("expected ValidateURL to reject flag-like input")
	}
}

func TestRunTrimsURLWhitespace(t *testing.T) {
	withTempHome(t)
	// Whitespace-only input should behave as blank → scaffold.
	if err := Run(strings.NewReader("   \n")); err != nil {
		t.Fatalf("whitespace-only input should scaffold, got: %v", err)
	}
}
