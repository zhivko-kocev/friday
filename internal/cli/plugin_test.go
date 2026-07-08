package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/zhivko-kocev/friday/internal/presets"
)

// gitInitRepo makes a throwaway git repo containing files, committed once, and
// returns its path (usable as a local clone source).
func gitInitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
		{"add", "-A"},
		{"commit", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestPluginAddUpgradeRemove(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	storeDir := filepath.Join(home, ".friday")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}

	repo := gitInitRepo(t, map[string]string{
		"friday-plugin.yaml": "target: .aider\nrules:\n  - from: [core.md]\n    to: CONVENTIONS.md\n    strategy: concatenate\n",
	})

	if code := pluginAdd(storeDir, []string{"aider", repo}); code != 0 {
		t.Fatalf("pluginAdd = %d, want 0", code)
	}
	installed := filepath.Join(storeDir, presets.PluginsDirName, "aider.yaml")
	if _, err := os.Stat(installed); err != nil {
		t.Fatalf("plugin not installed: %v", err)
	}
	if plugins, _ := presets.LoadPlugins(storeDir); plugins["aider"].Target != ".aider" {
		t.Errorf("LoadPlugins did not pick up the added plugin: %+v", plugins)
	}
	pin := loadPluginLock(storeDir).Plugins["aider"]
	if pin.URL != repo || pin.SHA == "" || pin.File != "aider.yaml" {
		t.Errorf("lock pin = %+v", pin)
	}

	// Re-adding the same name must refuse rather than clobber.
	if code := pluginAdd(storeDir, []string{"aider", repo}); code == 0 {
		t.Errorf("re-add of existing plugin should fail")
	}

	// No new commit upstream → upgrade is a no-op but still succeeds.
	if code := pluginUpgrade(storeDir, []string{"--all"}); code != 0 {
		t.Errorf("upgrade --all = %d, want 0", code)
	}

	if code := pluginRemove(storeDir, []string{"aider"}); code != 0 {
		t.Errorf("remove = %d, want 0", code)
	}
	if _, err := os.Stat(installed); !os.IsNotExist(err) {
		t.Errorf("plugin file still present after remove")
	}
	if _, ok := loadPluginLock(storeDir).Plugins["aider"]; ok {
		t.Errorf("lock entry not cleared after remove")
	}
}

func TestPluginAddRejectsInvalidPreset(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	storeDir := filepath.Join(home, ".friday")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A repo whose preset has no target — must be rejected, nothing installed.
	repo := gitInitRepo(t, map[string]string{"friday-plugin.yaml": "rules: []\n"})
	if code := pluginAdd(storeDir, []string{"bad", repo}); code == 0 {
		t.Errorf("add of invalid preset should fail")
	}
	if _, err := os.Stat(filepath.Join(storeDir, presets.PluginsDirName, "bad.yaml")); !os.IsNotExist(err) {
		t.Errorf("invalid preset should not have been installed")
	}
}

func TestFindPresetYAML(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.yaml")
	write("b.yaml")
	if _, err := findPresetYAML(dir); err == nil {
		t.Errorf("multiple .yaml with no friday-plugin.yaml should error")
	}
	write("friday-plugin.yaml")
	got, err := findPresetYAML(dir)
	if err != nil || filepath.Base(got) != "friday-plugin.yaml" {
		t.Errorf("friday-plugin.yaml should win: got %q, %v", got, err)
	}
}

func TestValidatePluginName(t *testing.T) {
	for _, bad := range []string{"", "~x", "a/b", "..", `a\b`, "a:b"} {
		if err := validatePluginName(bad); err == nil {
			t.Errorf("name %q should be rejected", bad)
		}
	}
	if err := validatePluginName("aider"); err != nil {
		t.Errorf("valid name rejected: %v", err)
	}
}
