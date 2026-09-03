package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/rules"
)

// mergeTOMLAdapter mirrors mergeJSONAdapter for the plain (non-render)
// merge-toml strategy Phase 5 adds: settings/<adapter>.toml -> the agent's
// own config.toml, merged.
func mergeTOMLAdapter(targetAbs string) *config.Adapter {
	return &config.Adapter{
		Target: targetAbs,
		Rules: []*rules.Rule{{
			From:     rules.FromSpec{"settings/codex.toml"},
			To:       "config.toml",
			Strategy: rules.StrategyMergeTOML,
		}},
	}
}

const storeTOML = "[tui]\ntheme = \"dark\"\n"

func TestPlanMergeTOMLCreate(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{"settings/codex.toml": storeTOML})
	changes, err := planPush(nil, "codex", mergeTOMLAdapter(targetAbs), singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1", len(changes))
	}
	ch := changes[0]
	if ch.Action != ActionCreate {
		t.Errorf("action = %s, want create", ch.Action)
	}
	if !ch.driftExempt {
		t.Error("merge-toml change must be drift-exempt")
	}
	if !strings.Contains(string(ch.NewContent), `theme = 'dark'`) && !strings.Contains(string(ch.NewContent), `theme = "dark"`) {
		t.Errorf("tui table not present in output: %s", ch.NewContent)
	}
}

func TestPlanMergeTOMLInSyncDespiteReformattedNeighbor(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{"settings/codex.toml": storeTOML})
	// Target already wired, but with a differently formatted, unmanaged table.
	existing := "[other]\n  key    =   \"val\"\n\n[tui]\ntheme = \"dark\"\n"
	writeTarget(t, targetAbs, "config.toml", existing)

	changes, err := planPush(nil, "codex", mergeTOMLAdapter(targetAbs), singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if changes[0].Action != ActionInSync {
		t.Errorf("action = %s, want in-sync (owned table already matches)", changes[0].Action)
	}
}

func TestPlanMergeTOMLUpdatePreservesNeighbor(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{"settings/codex.toml": storeTOML})
	// Target has an unmanaged table (other) and a stale value in the owned one.
	writeTarget(t, targetAbs, "config.toml", "[other]\nkey = \"val\"\n\n[tui]\ntheme = \"light\"\n")

	changes, err := planPush(nil, "codex", mergeTOMLAdapter(targetAbs), singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	ch := changes[0]
	if ch.Action != ActionUpdate {
		t.Fatalf("action = %s, want update", ch.Action)
	}
	s := string(ch.NewContent)
	if !strings.Contains(s, `key = "val"`) {
		t.Errorf("unmanaged table lost: %s", s)
	}
	if !strings.Contains(s, `theme = "dark"`) && !strings.Contains(s, `theme = 'dark'`) {
		t.Errorf("owned table not updated to friday's value: %s", s)
	}
	if strings.Contains(s, "light") {
		t.Errorf("stale owned value survived the merge: %s", s)
	}
}

func TestPlanMergeTOMLMalformedTargetSkips(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{"settings/codex.toml": storeTOML})
	writeTarget(t, targetAbs, "config.toml", "[ this is not valid toml")

	// A malformed target is skipped (unsupported), not an error that aborts the
	// whole run — and never overwritten.
	changes, err := planPush(nil, "codex", mergeTOMLAdapter(targetAbs), singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatalf("malformed target must not abort the run: %v", err)
	}
	if changes[0].Action != ActionUnsupported {
		t.Errorf("action = %s, want unsupported", changes[0].Action)
	}
	got, _ := os.ReadFile(filepath.Join(targetAbs, "config.toml"))
	if string(got) != "[ this is not valid toml" {
		t.Errorf("malformed target was modified during planning: %s", got)
	}
}

func TestPlanMergeTOMLMissingSource(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{})
	changes, err := planPush(nil, "codex", mergeTOMLAdapter(targetAbs), singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if changes[0].Action != ActionMissingSource {
		t.Errorf("action = %s, want missing-source", changes[0].Action)
	}
}

func TestPlanMergeTOMLPullUnsupported(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{"settings/codex.toml": storeTOML})
	changes, err := planPull(nil, "codex", mergeTOMLAdapter(targetAbs), singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Action != ActionUnsupported {
		t.Fatalf("want one unsupported change, got %+v", changes)
	}
}

func TestPlanMergeTOMLImportUnsupported(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{"settings/codex.toml": storeTOML})
	writeTarget(t, targetAbs, "config.toml", "model = \"gpt-5\"\n")
	changes, err := planImport(nil, "codex", mergeTOMLAdapter(targetAbs), singleRoot(storeAbs), targetAbs, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Action != ActionUnsupported {
		t.Fatalf("want one unsupported change (eject safety), got %+v", changes)
	}
	// The store's settings/codex.toml must be untouched.
	got, _ := os.ReadFile(filepath.Join(storeAbs, "settings", "codex.toml"))
	if string(got) != storeTOML {
		t.Errorf("store settings/codex.toml corrupted by import: %s", got)
	}
}
