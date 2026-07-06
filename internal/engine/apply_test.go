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

// driftHarness pushes once to seed baselines, then lets the test mutate both
// sides before pulling.
func driftHarness(t *testing.T) (storeAbs, targetAbs string, cfg *config.Config) {
	t.Helper()
	storeAbs, targetAbs = scaffold(t, map[string]string{"a.md": "v1"})
	cfg = &config.Config{
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
	return storeAbs, targetAbs, cfg
}

func TestPullTargetOnlyEditAppliesSilently(t *testing.T) {
	storeAbs, targetAbs, cfg := driftHarness(t)
	if err := os.WriteFile(filepath.Join(targetAbs, "a.md"), []byte("target-edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Pull(cfg, Options{}) // no resolver — must not be needed
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Action != ActionUpdate {
		t.Fatalf("got %+v, want silent Update (canonical unchanged since push)", got)
	}
	data, _ := os.ReadFile(filepath.Join(storeAbs, "a.md"))
	if string(data) != "target-edit" {
		t.Errorf("store = %q", data)
	}
}

func TestPullBothSidesDriftedConflicts(t *testing.T) {
	storeAbs, targetAbs, cfg := driftHarness(t)
	if err := os.WriteFile(filepath.Join(targetAbs, "a.md"), []byte("target-edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeAbs, "a.md"), []byte("canonical-edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Pull(cfg, Options{}) // no resolver → conflict, store untouched
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Action != ActionConflict {
		t.Fatalf("got %+v, want Conflict", got)
	}
	data, _ := os.ReadFile(filepath.Join(storeAbs, "a.md"))
	if string(data) != "canonical-edit" {
		t.Errorf("pull ate the canonical edit: %q", data)
	}

	// A resolver choosing the incoming target version resolves it.
	resolver := func(ConflictInfo) Resolution { return Resolution{Choice: ConflictKeepCanonical} }
	got, err = Pull(cfg, Options{OnConflict: resolver})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Action != ActionUpdate {
		t.Fatalf("resolved action = %v", got[0].Action)
	}
	data, _ = os.ReadFile(filepath.Join(storeAbs, "a.md"))
	if string(data) != "target-edit" {
		t.Errorf("store = %q, want target version", data)
	}

	// Baselines updated: a follow-up push is clean and in-sync.
	pushed, err := Push(cfg, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if pushed[0].Action != ActionInSync {
		t.Errorf("post-pull push = %v, want in-sync", pushed[0].Action)
	}
}

func TestPullForceBypassesCanonicalDrift(t *testing.T) {
	storeAbs, targetAbs, cfg := driftHarness(t)
	if err := os.WriteFile(filepath.Join(targetAbs, "a.md"), []byte("target-edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeAbs, "a.md"), []byte("canonical-edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Pull(cfg, Options{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Action != ActionUpdate {
		t.Errorf("got %v, want Update under --force", got[0].Action)
	}
}

func TestPushConflictResolvedByMerge(t *testing.T) {
	storeAbs, targetAbs, cfg := driftHarness(t)
	if err := os.WriteFile(filepath.Join(targetAbs, "a.md"), []byte("target-edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeAbs, "a.md"), []byte("canonical-edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	merged := []byte("merged-content")
	resolver := func(info ConflictInfo) Resolution {
		return Resolution{Choice: ConflictUseMerged, Content: merged}
	}
	got, err := Push(cfg, Options{OnConflict: resolver})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Action != ActionUpdate {
		t.Fatalf("action = %v, want Update carrying merged content", got[0].Action)
	}
	data, _ := os.ReadFile(filepath.Join(targetAbs, "a.md"))
	if string(data) != "merged-content" {
		t.Errorf("target = %q", data)
	}
	// The merged content is the new baseline: pushing again without edits
	// must not re-prompt.
	got, err = Push(cfg, Options{}) // nil resolver would surface any conflict
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Action == ActionConflict {
		t.Error("merged baseline not recorded — second push conflicts")
	}
}

func TestConflictInfoCarriesBaseContent(t *testing.T) {
	storeAbs, targetAbs, cfg := driftHarness(t)
	if err := os.WriteFile(filepath.Join(targetAbs, "a.md"), []byte("target-edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeAbs, "a.md"), []byte("canonical-edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The base lookup maps the baseline hash (of "v1", the first push's
	// content) back to its bytes, like the snapshot blob store does.
	lookup := func(h string) ([]byte, bool) {
		if h == drift.Hash([]byte("v1")) {
			return []byte("v1"), true
		}
		return nil, false
	}
	var gotBase []byte
	resolver := func(info ConflictInfo) Resolution {
		gotBase = info.BaseContent
		return Resolution{Choice: ConflictSkip}
	}
	if _, err := Push(cfg, Options{OnConflict: resolver, BaseLookup: lookup}); err != nil {
		t.Fatal(err)
	}
	if string(gotBase) != "v1" {
		t.Errorf("BaseContent = %q, want the last-synced v1", gotBase)
	}
}
