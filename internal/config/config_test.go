package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadProject(t *testing.T) {
	dir := t.TempDir()
	manifest := []byte(`version: 1
adapters:
  claude:
    target: .claude
    rules:
      - from: identity.md
        to: CLAUDE.md
`)
	if err := os.WriteFile(filepath.Join(dir, "friday.yaml"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	cfg, err := LoadProject(dir, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Scope != ScopeProject {
		t.Errorf("scope = %v", cfg.Scope)
	}
	if cfg.TargetRoot != cwd {
		t.Errorf("TargetRoot = %s, want %s", cfg.TargetRoot, cwd)
	}
	if cfg.Adapters["claude"].Target != ".claude" {
		t.Errorf("claude.target = %q", cfg.Adapters["claude"].Target)
	}
}

func TestLoadProjectMissingManifest(t *testing.T) {
	if _, err := LoadProject(t.TempDir(), t.TempDir()); err != ErrNoManifest {
		t.Errorf("err = %v, want ErrNoManifest", err)
	}
}

func TestLoadProjectVersionMismatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "friday.yaml"), []byte("version: 99\nadapters: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProject(dir, t.TempDir()); err == nil {
		t.Errorf("expected unsupported-version error")
	}
}

func TestResolvePathAbsolute(t *testing.T) {
	cfg := &Config{TargetRoot: filepath.Join(t.TempDir(), "root")}
	abs := mustAbs(t)
	if got := cfg.resolvePath(abs); got != abs {
		t.Errorf("resolvePath(abs) = %q, want %q", got, abs)
	}
}

func TestResolvePathRelative(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	cfg := &Config{TargetRoot: root}
	got := cfg.resolvePath(".claude")
	want := filepath.Join(root, ".claude")
	if got != want {
		t.Errorf("resolvePath(rel) = %q, want %q", got, want)
	}
}

func TestResolvePathTilde(t *testing.T) {
	cfg := &Config{TargetRoot: "/should-not-be-used"}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir on this platform")
	}
	got := cfg.resolvePath("~/foo")
	want := filepath.Join(home, "foo")
	if got != want {
		t.Errorf("resolvePath(~/) = %q, want %q", got, want)
	}
}

func TestSelectAdaptersValidates(t *testing.T) {
	cfg := &Config{Adapters: map[string]*Adapter{"claude": {Target: ".claude"}}}
	if _, err := cfg.SelectAdapters([]string{"missing"}); err == nil {
		t.Errorf("expected error for unknown adapter")
	}
	got, err := cfg.SelectAdapters(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "claude" {
		t.Errorf("got %v", got)
	}
}

func TestSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		Version:      1,
		ManifestPath: filepath.Join(dir, "friday.yaml"),
		Adapters: map[string]*Adapter{
			"claude": {Target: ".claude"},
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	out, err := LoadProject(dir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if out.Adapters["claude"].Target != ".claude" {
		t.Errorf("round-trip lost target")
	}
}

func mustAbs(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return `C:\never\used`
	}
	return "/never/used"
}
