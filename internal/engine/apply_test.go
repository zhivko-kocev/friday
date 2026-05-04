package engine

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/drift"
	"github.com/zhivko-kocev/friday/internal/rules"
)

// applyHarness runs Push end-to-end against a temp store + target and
// returns the resulting changes plus the on-disk drift store.
func applyHarness(t *testing.T, files map[string]string, ruleSet []*rules.Rule, opts Options) (storeAbs, targetAbs string, changes []Change, st *drift.Store) {
	t.Helper()
	storeAbs, targetAbs = scaffold(t, files)
	cfg := &config.Config{
		Version:    1,
		StoreDir:   storeAbs,
		TargetRoot: targetAbs,
		Adapters: map[string]*config.Adapter{
			"test": {Target: targetAbs, Rules: ruleSet},
		},
	}
	// Force the drift store into the temp dir so tests don't share state.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if runtime.GOOS == "windows" {
		t.Setenv("LocalAppData", t.TempDir())
	}

	got, err := Push(cfg, opts)
	if err != nil {
		t.Fatal(err)
	}
	driftPath, _ := drift.DefaultPath()
	st, _ = drift.Load(driftPath)
	return storeAbs, targetAbs, got, st
}

func TestApplyWritesFileAndRecordsHash(t *testing.T) {
	_, targetAbs, changes, st := applyHarness(t,
		map[string]string{"a.md": "hello"},
		[]*rules.Rule{{From: rules.FromSpec{"a.md"}, To: "a.md", Strategy: rules.StrategyCopy}},
		Options{},
	)
	if len(changes) != 1 || changes[0].Action != ActionCreate {
		t.Fatalf("got %+v", changes)
	}
	out := filepath.Join(targetAbs, "a.md")
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Errorf("file content = %q", data)
	}
	drifted, exists := st.Check("test", out)
	if !exists || drifted {
		t.Errorf("drift state: drifted=%v exists=%v want false,true", drifted, exists)
	}
}

func TestApplyDryRunWritesNothing(t *testing.T) {
	_, targetAbs, changes, _ := applyHarness(t,
		map[string]string{"a.md": "hello"},
		[]*rules.Rule{{From: rules.FromSpec{"a.md"}, To: "a.md", Strategy: rules.StrategyCopy}},
		Options{DryRun: true},
	)
	if len(changes) != 1 || changes[0].Action != ActionCreate {
		t.Fatalf("got %+v", changes)
	}
	if _, err := os.Stat(filepath.Join(targetAbs, "a.md")); !os.IsNotExist(err) {
		t.Errorf("dry-run wrote file (err=%v)", err)
	}
}

func TestDriftFlaggedAsConflictWhenNonInteractive(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{"a.md": "v1"})
	// First push: populate baseline.
	cfg := &config.Config{
		Version:    1,
		StoreDir:   storeAbs,
		TargetRoot: targetAbs,
		Adapters: map[string]*config.Adapter{
			"test": {Target: targetAbs, Rules: []*rules.Rule{{From: rules.FromSpec{"a.md"}, To: "a.md", Strategy: rules.StrategyCopy}}},
		},
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if runtime.GOOS == "windows" {
		t.Setenv("LocalAppData", t.TempDir())
	}
	if _, err := Push(cfg, Options{}); err != nil {
		t.Fatal(err)
	}

	// User edits the target out of band.
	if err := os.WriteFile(filepath.Join(targetAbs, "a.md"), []byte("user-edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	// And the store moves on.
	if err := os.WriteFile(filepath.Join(storeAbs, "a.md"), []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Push(cfg, Options{}) // OnConflict nil → conflict
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Action != ActionConflict {
		t.Errorf("got %+v, want one Conflict", got)
	}
}

func TestForceOverridesDrift(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{"a.md": "v1"})
	cfg := &config.Config{
		Version:    1,
		StoreDir:   storeAbs,
		TargetRoot: targetAbs,
		Adapters: map[string]*config.Adapter{
			"test": {Target: targetAbs, Rules: []*rules.Rule{{From: rules.FromSpec{"a.md"}, To: "a.md", Strategy: rules.StrategyCopy}}},
		},
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if runtime.GOOS == "windows" {
		t.Setenv("LocalAppData", t.TempDir())
	}
	if _, err := Push(cfg, Options{}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetAbs, "a.md"), []byte("user-edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeAbs, "a.md"), []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Push(cfg, Options{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Action != ActionUpdate {
		t.Errorf("got %v, want Update under --force", got[0].Action)
	}
	data, _ := os.ReadFile(filepath.Join(targetAbs, "a.md"))
	if string(data) != "v2" {
		t.Errorf("force did not overwrite; got %q", data)
	}
}

func TestCRLFDoesNotTripDrift(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{"a.md": "line1\nline2\n"})
	cfg := &config.Config{
		Version:    1,
		StoreDir:   storeAbs,
		TargetRoot: targetAbs,
		Adapters: map[string]*config.Adapter{
			"test": {Target: targetAbs, Rules: []*rules.Rule{{From: rules.FromSpec{"a.md"}, To: "a.md", Strategy: rules.StrategyCopy}}},
		},
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if runtime.GOOS == "windows" {
		t.Setenv("LocalAppData", t.TempDir())
	}
	if _, err := Push(cfg, Options{}); err != nil {
		t.Fatal(err)
	}
	// Rewrite target with CRLF line endings (simulating a Windows editor).
	if err := os.WriteFile(filepath.Join(targetAbs, "a.md"), []byte("line1\r\nline2\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Push(cfg, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Action != ActionInSync {
		t.Errorf("got %v, want InSync (CRLF should not count as drift)", got[0].Action)
	}
}
