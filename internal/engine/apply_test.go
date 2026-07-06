package engine

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestPullSkipsWhenTargetMatchesBaseline(t *testing.T) {
	// Store edited, target untouched since the last push: pull has nothing
	// to capture and must not overwrite the newer store — even under force.
	storeAbs, _, cfg := driftHarness(t)
	if err := os.WriteFile(filepath.Join(storeAbs, "a.md"), []byte("store-edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, force := range []bool{false, true} {
		got, err := Pull(cfg, Options{Force: force})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Action != ActionInSync {
			t.Fatalf("force=%v: got %+v, want InSync (store is newer)", force, got)
		}
		data, _ := os.ReadFile(filepath.Join(storeAbs, "a.md"))
		if string(data) != "store-edit" {
			t.Fatalf("force=%v: pull overwrote the newer store: %q", force, data)
		}
	}
}

func TestPushMergeWritesBackToStore(t *testing.T) {
	storeAbs, targetAbs, cfg := driftHarness(t)
	if err := os.WriteFile(filepath.Join(targetAbs, "a.md"), []byte("target-edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeAbs, "a.md"), []byte("canonical-edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolver := func(ConflictInfo) Resolution {
		return Resolution{Choice: ConflictUseMerged, Content: []byte("merged-content")}
	}
	if _, err := Push(cfg, Options{OnConflict: resolver}); err != nil {
		t.Fatal(err)
	}
	// The merge must reach the store too; otherwise the next push plans from
	// the old store content and silently reverts it.
	data, _ := os.ReadFile(filepath.Join(storeAbs, "a.md"))
	if string(data) != "merged-content" {
		t.Fatalf("store = %q, want the merged content", data)
	}
	got, err := Push(cfg, Options{}) // nil resolver — must be a clean no-op
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Action != ActionInSync {
		t.Errorf("post-merge push = %v, want InSync (both sides converged)", got[0].Action)
	}
	tdata, _ := os.ReadFile(filepath.Join(targetAbs, "a.md"))
	if string(tdata) != "merged-content" {
		t.Errorf("second push reverted the merge: target = %q", tdata)
	}
}

func TestPushMergeOnConcatenateKeepsPromptAndWarns(t *testing.T) {
	// Concatenate rules can't route a merge back into their (multi-file)
	// sources. The merge lands in the target, but the old baseline is kept
	// so the next push re-prompts instead of silently reverting it.
	storeAbs, targetAbs := scaffold(t, map[string]string{"a.md": "v1"})
	cfg := &config.Config{
		Version:    1,
		StoreDir:   storeAbs,
		TargetRoot: targetAbs,
		Adapters: map[string]*config.Adapter{
			"test": {Target: targetAbs, Rules: []*rules.Rule{
				{From: rules.FromSpec{"a.md"}, To: "out.md", Strategy: rules.StrategyConcatenate},
			}},
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
	if err := os.WriteFile(filepath.Join(targetAbs, "out.md"), []byte("target-edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeAbs, "a.md"), []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolver := func(ConflictInfo) Resolution {
		return Resolution{Choice: ConflictUseMerged, Content: []byte("merged-content")}
	}
	got, err := Push(cfg, Options{OnConflict: resolver})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Warning == "" {
		t.Error("no warning that the store still holds the old content")
	}
	data, _ := os.ReadFile(filepath.Join(targetAbs, "out.md"))
	if string(data) != "merged-content" {
		t.Fatalf("target = %q", data)
	}
	// Next push must NOT silently revert the merge.
	got, err = Push(cfg, Options{}) // nil resolver → drift surfaces as conflict
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Action != ActionConflict {
		t.Errorf("post-merge push = %v, want Conflict (merge must not be silently reverted)", got[0].Action)
	}
	data, _ = os.ReadFile(filepath.Join(targetAbs, "out.md"))
	if string(data) != "merged-content" {
		t.Errorf("second push reverted the merge: %q", data)
	}
}

func TestInSyncRunHealsMissingBaselines(t *testing.T) {
	// A store upgraded from a pre-baseline friday is fully in sync but has
	// no recorded baselines. The first in-sync run must record them —
	// otherwise every later target edit reads as an unresolvable conflict
	// in non-interactive pulls.
	storeAbs, targetAbs := scaffold(t, map[string]string{"a.md": "v1"})
	if err := os.WriteFile(filepath.Join(targetAbs, "a.md"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
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
	got, err := Push(cfg, Options{}) // in-sync: writes nothing, heals baselines
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Action != ActionInSync {
		t.Fatalf("got %v, want InSync", got[0].Action)
	}
	// Target edit + non-interactive pull: with the healed canonical baseline
	// this is a plain capture, not a conflict.
	if err := os.WriteFile(filepath.Join(targetAbs, "a.md"), []byte("target-edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = Pull(cfg, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Action != ActionUpdate {
		t.Fatalf("pull after heal = %v (%s), want Update", got[0].Action, got[0].Reason)
	}
	data, _ := os.ReadFile(filepath.Join(storeAbs, "a.md"))
	if string(data) != "target-edit" {
		t.Errorf("store = %q", data)
	}
}

func TestMaxBytesWarningSurvivesConflict(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{"a.md": strings.Repeat("x", 100)})
	cfg := &config.Config{
		Version:    1,
		StoreDir:   storeAbs,
		TargetRoot: targetAbs,
		Adapters: map[string]*config.Adapter{
			"test": {Target: targetAbs, Rules: []*rules.Rule{
				{From: rules.FromSpec{"a.md"}, To: "out.md", Strategy: rules.StrategyConcatenate, MaxBytes: 50},
			}},
		},
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if runtime.GOOS == "windows" {
		t.Setenv("LocalAppData", t.TempDir())
	}
	if _, err := Push(cfg, Options{Force: true}); err != nil {
		t.Fatal(err)
	}
	// Drift the target and change the store so the next push conflicts.
	if err := os.WriteFile(filepath.Join(targetAbs, "out.md"), []byte("target-edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeAbs, "a.md"), []byte(strings.Repeat("y", 100)), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Push(cfg, Options{}) // nil resolver → conflict
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Action != ActionConflict {
		t.Fatalf("got %v, want Conflict", got[0].Action)
	}
	if !strings.Contains(got[0].Warning, "exceeds") {
		t.Errorf("Warning = %q — the max_bytes advisory was lost in conflict resolution", got[0].Warning)
	}
}
