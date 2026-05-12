package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadUserVersionMismatch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	storeDir := filepath.Join(home, ".friday")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "friday.yaml"), []byte("version: 99\nadapters: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadUser(); err == nil {
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
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	storeDir := filepath.Join(home, ".friday")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{
		Version:      1,
		ManifestPath: filepath.Join(storeDir, "friday.yaml"),
		Adapters:     map[string]*Adapter{"claude": {Target: ".claude"}},
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	out, err := LoadUser()
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
