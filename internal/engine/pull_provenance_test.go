package engine

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/rules"
)

// twoAgentSameStore sets up one store file mapped by a plain copy rule into two
// separate agent targets — the shape behind the reported "pull shows the change
// as removed on the second agent" bug.
func twoAgentSameStore(t *testing.T) (storeAbs, tA, tB string, cfg *config.Config) {
	t.Helper()
	storeAbs = t.TempDir()
	if err := os.WriteFile(filepath.Join(storeAbs, "a.md"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tA, tB = t.TempDir(), t.TempDir()
	rule := func() []*rules.Rule {
		return []*rules.Rule{{From: rules.FromSpec{"a.md"}, To: "a.md", Strategy: rules.StrategyCopy}}
	}
	cfg = &config.Config{
		Version:  1,
		StoreDir: storeAbs,
		Adapters: map[string]*config.Adapter{
			"agentA": {Target: tA, Rules: rule()},
			"agentB": {Target: tB, Rules: rule()},
		},
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if runtime.GOOS == "windows" {
		t.Setenv("LocalAppData", t.TempDir())
	}
	return storeAbs, tA, tB, cfg
}

// storeFile reads the single store file under test.
func storeFile(t *testing.T, storeAbs string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(storeAbs, "a.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestPullStaleSecondAgentIsNotAReversal reproduces the reported bug: agentA's
// edit is captured, then agentB — whose target still holds the old bytes and
// has no baseline (never pushed) — must NOT be planned as an update writing the
// old content back over the freshly-pulled store. It reports as in-sync ("push
// to fan out"), never a removal.
func TestPullStaleSecondAgentIsNotAReversal(t *testing.T) {
	storeAbs, tA, tB, cfg := twoAgentSameStore(t)

	// agentA is a managed agent: push seeds its baseline + the canonical store
	// baseline. agentB's copy exists (matching the old store) but friday never
	// wrote it, so it has no baseline — the condition that defeats the existing
	// clean-stale guard.
	if _, err := Push(cfg, Options{Adapters: []string{"agentA"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tB, "a.md"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The edit the user made in agentA.
	if err := os.WriteFile(filepath.Join(tA, "a.md"), []byte("v1\nadded line\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Pull(cfg, Options{
		Adapters:         []string{"agentA", "agentB"},
		PulledStorePaths: map[string]bool{},
	})
	if err != nil {
		t.Fatal(err)
	}

	byAgent := map[string]Change{}
	for _, ch := range got {
		byAgent[ch.Adapter] = ch
	}
	if a := byAgent["agentA"]; a.Action != ActionUpdate {
		t.Fatalf("agentA = %v (%s), want Update (the real capture)", a.Action, a.Reason)
	}
	if b := byAgent["agentB"]; b.Action != ActionInSync || !b.staleTarget {
		t.Fatalf("agentB = %v staleTarget=%v (%s), want InSync/staleTarget (behind, not a removal)",
			b.Action, b.staleTarget, b.Reason)
	}
	if s := storeFile(t, storeAbs); s != "v1\nadded line\n" {
		t.Errorf("store = %q, want agentA's captured edit intact", s)
	}
}

// TestPullForceDoesNotRevertCapturedEdit is the silent-data-loss guard: the
// same setup under --force. Before the fix, agentB's stale copy overwrites the
// store and reverts agentA's just-captured edit with no prompt.
func TestPullForceDoesNotRevertCapturedEdit(t *testing.T) {
	storeAbs, tA, tB, cfg := twoAgentSameStore(t)
	if _, err := Push(cfg, Options{Adapters: []string{"agentA"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tB, "a.md"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tA, "a.md"), []byte("v1\nadded line\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Pull(cfg, Options{
		Adapters:         []string{"agentA", "agentB"},
		Force:            true,
		PulledStorePaths: map[string]bool{},
	}); err != nil {
		t.Fatal(err)
	}
	if s := storeFile(t, storeAbs); s != "v1\nadded line\n" {
		t.Errorf("store = %q — --force pull reverted the captured edit", s)
	}
}

// TestPullDivergentSecondAgentConflicts covers the other branch: agentB WAS
// pushed (has a baseline) and then edited differently. That's a genuine
// divergence between two agents, so it must surface as a conflict — not be
// silently dropped, and not overwrite agentA's capture.
func TestPullDivergentSecondAgentConflicts(t *testing.T) {
	storeAbs, tA, tB, cfg := twoAgentSameStore(t)
	// Both agents managed: push both to seed baselines.
	if _, err := Push(cfg, Options{Adapters: []string{"agentA", "agentB"}}); err != nil {
		t.Fatal(err)
	}
	// Each agent edits its own copy differently.
	if err := os.WriteFile(filepath.Join(tA, "a.md"), []byte("v1\nfrom A\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tB, "a.md"), []byte("v1\nfrom B\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Pull(cfg, Options{
		Adapters:         []string{"agentA", "agentB"},
		PulledStorePaths: map[string]bool{},
	}) // no resolver
	if err != nil {
		t.Fatal(err)
	}
	byAgent := map[string]Change{}
	for _, ch := range got {
		byAgent[ch.Adapter] = ch
	}
	if a := byAgent["agentA"]; a.Action != ActionUpdate {
		t.Fatalf("agentA = %v, want Update", a.Action)
	}
	if b := byAgent["agentB"]; b.Action != ActionConflict {
		t.Fatalf("agentB = %v (%s), want Conflict (divergent edit surfaced)", b.Action, b.Reason)
	}
	if s := storeFile(t, storeAbs); s != "v1\nfrom A\n" {
		t.Errorf("store = %q, want agentA's capture (agentB's divergence must not overwrite it)", s)
	}
}
