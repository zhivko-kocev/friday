package initcmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zhivko-kocev/friday/internal/config"
)

func TestAddRemoveAdapter(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Version:      1,
		Adapters:     map[string]*config.Adapter{},
		ManifestPath: filepath.Join(dir, "friday.yaml"),
		StoreDir:     dir,
	}
	if err := AddAdapter(cfg, "claude", "", false); err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Adapters["claude"]; !ok {
		t.Fatal("AddAdapter didn't register claude")
	}
	if err := AddAdapter(cfg, "claude", "", false); err == nil {
		t.Errorf("re-adding without --force should error")
	}
	if err := AddAdapter(cfg, "claude", "/custom", true); err != nil {
		t.Fatal(err)
	}
	if cfg.Adapters["claude"].Target != "/custom" {
		t.Errorf("force-add did not override target")
	}
	if err := RemoveAdapter(cfg, "claude"); err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Adapters["claude"]; ok {
		t.Errorf("RemoveAdapter left entry behind")
	}
	if err := RemoveAdapter(cfg, "claude"); err == nil {
		t.Errorf("removing missing adapter should error")
	}
}

func TestSafeWipeRefusesGitDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := safeWipe(dir, false); err == nil {
		t.Errorf("safeWipe with .git/ and reallyForce=false should error")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("dir was wiped despite refusal: %v", err)
	}
}

func TestSafeWipeWithReallyForce(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := safeWipe(dir, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("dir survived really-force wipe: %v", err)
	}
}

func TestSafeWipeNoDirIsNoOp(t *testing.T) {
	if err := safeWipe(filepath.Join(t.TempDir(), "missing"), false); err != nil {
		t.Errorf("safeWipe on missing dir errored: %v", err)
	}
}

func TestRunRejectsReallyForceWithoutForce(t *testing.T) {
	err := Run(Options{ReallyForce: true})
	if err == nil {
		t.Fatal("expected error when --really-force passed without --force")
	}
}
