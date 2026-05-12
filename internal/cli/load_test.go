package cli

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/zhivko-kocev/friday/internal/config"
)

func TestInstalledAdapters(t *testing.T) {
	root := t.TempDir()
	// Two adapters: claude has its target dir on disk, cursor doesn't.
	mustMkdir(t, filepath.Join(root, ".claude"))

	cfg := &config.Config{
		Version:    1,
		Scope:      config.ScopeUser,
		StoreDir:   root,
		TargetRoot: root,
		Adapters: map[string]*config.Adapter{
			"claude": {Target: ".claude"},
			"cursor": {Target: ".cursor"},
		},
	}

	got := installedAdapters(cfg)
	sort.Strings(got)
	want := []string{"claude"}

	if len(got) != len(want) || got[0] != want[0] {
		t.Errorf("installedAdapters = %v, want %v", got, want)
	}
}

func TestInstalledAdaptersIgnoresFiles(t *testing.T) {
	root := t.TempDir()
	// Create a regular file at the target path — must not count as installed.
	if err := os.WriteFile(filepath.Join(root, ".claude"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Version:    1,
		StoreDir:   root,
		TargetRoot: root,
		Adapters:   map[string]*config.Adapter{"claude": {Target: ".claude"}},
	}
	if got := installedAdapters(cfg); len(got) != 0 {
		t.Errorf("installedAdapters = %v, want []", got)
	}
}

func TestInstalledAdaptersEmptyConfig(t *testing.T) {
	cfg := &config.Config{
		Version:    1,
		StoreDir:   t.TempDir(),
		TargetRoot: t.TempDir(),
		Adapters:   map[string]*config.Adapter{},
	}
	if got := installedAdapters(cfg); len(got) != 0 {
		t.Errorf("installedAdapters on empty config = %v, want []", got)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}
