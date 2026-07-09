package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/rules"
)

func mergeJSONAdapter(targetAbs string) *config.Adapter {
	return &config.Adapter{
		Target: targetAbs,
		Rules: []*rules.Rule{{
			From:     rules.FromSpec{"hooks/hooks.json"},
			To:       "settings.json",
			Strategy: rules.StrategyMergeJSON,
		}},
	}
}

const storeHooks = `{"hooks":{"PreToolUse":[{"matcher":"Bash"}]}}`

func TestPlanMergeJSONCreate(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{"hooks/hooks.json": storeHooks})
	changes, err := planPush(nil, "claude", mergeJSONAdapter(targetAbs), storeAbs, targetAbs)
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
		t.Error("merge-json change must be drift-exempt")
	}
	if !strings.Contains(string(ch.NewContent), `"PreToolUse"`) {
		t.Errorf("hooks not present in output: %s", ch.NewContent)
	}
}

func TestPlanMergeJSONInSyncDespiteReformattedNeighbor(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{"hooks/hooks.json": storeHooks})
	// Target already wired, but with a differently formatted, unmanaged key.
	existing := "{\n    \"model\": \"opus\",\n    \"hooks\": { \"PreToolUse\": [ {\"matcher\":\"Bash\"} ] }\n}\n"
	writeTarget(t, targetAbs, "settings.json", existing)

	changes, err := planPush(nil, "claude", mergeJSONAdapter(targetAbs), storeAbs, targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if changes[0].Action != ActionInSync {
		t.Errorf("action = %s, want in-sync (owned key already matches)", changes[0].Action)
	}
}

func TestPlanMergeJSONUpdatePreservesNeighbor(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{"hooks/hooks.json": storeHooks})
	// Target has an unmanaged key (model) and the user's own hook (Read).
	writeTarget(t, targetAbs, "settings.json", `{"model":"opus","hooks":{"PreToolUse":[{"matcher":"Read"}]}}`)

	changes, err := planPush(nil, "claude", mergeJSONAdapter(targetAbs), storeAbs, targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	ch := changes[0]
	if ch.Action != ActionUpdate {
		t.Fatalf("action = %s, want update", ch.Action)
	}
	s := string(ch.NewContent)
	if !strings.Contains(s, `"model": "opus"`) {
		t.Errorf("unmanaged key lost: %s", s)
	}
	// Entry-level merge: friday's Bash entry is added while the user's Read entry
	// is preserved (not wiped).
	if !strings.Contains(s, `"Bash"`) || !strings.Contains(s, `"Read"`) {
		t.Errorf("expected both the user's Read hook and friday's Bash hook: %s", s)
	}
}

func TestPlanMergeJSONMalformedTargetSkips(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{"hooks/hooks.json": storeHooks})
	writeTarget(t, targetAbs, "settings.json", `{ this is not json `)

	// A malformed target is skipped (unsupported), not an error that aborts the
	// whole run — and never overwritten.
	changes, err := planPush(nil, "claude", mergeJSONAdapter(targetAbs), storeAbs, targetAbs)
	if err != nil {
		t.Fatalf("malformed target must not abort the run: %v", err)
	}
	if changes[0].Action != ActionUnsupported {
		t.Errorf("action = %s, want unsupported", changes[0].Action)
	}
	got, _ := os.ReadFile(filepath.Join(targetAbs, "settings.json"))
	if string(got) != `{ this is not json ` {
		t.Errorf("malformed target was modified during planning: %s", got)
	}
}

func TestPlanMergeJSONMissingSource(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{})
	changes, err := planPush(nil, "claude", mergeJSONAdapter(targetAbs), storeAbs, targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if changes[0].Action != ActionMissingSource {
		t.Errorf("action = %s, want missing-source", changes[0].Action)
	}
}

func TestPlanMergeJSONPullUnsupported(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{"hooks/hooks.json": storeHooks})
	changes, err := planPull(nil, "claude", mergeJSONAdapter(targetAbs), storeAbs, targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Action != ActionUnsupported {
		t.Fatalf("want one unsupported change, got %+v", changes)
	}
}

func TestPlanMergeJSONImportUnsupported(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{"hooks/hooks.json": storeHooks})
	writeTarget(t, targetAbs, "settings.json", `{"model":"opus"}`)
	changes, err := planImport(nil, "claude", mergeJSONAdapter(targetAbs), storeAbs, targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Action != ActionUnsupported {
		t.Fatalf("want one unsupported change (eject safety), got %+v", changes)
	}
	// The store's hooks.json must be untouched.
	got, _ := os.ReadFile(filepath.Join(storeAbs, "hooks/hooks.json"))
	if string(got) != storeHooks {
		t.Errorf("store hooks.json corrupted by import: %s", got)
	}
}

func writeTarget(t *testing.T, targetAbs, rel, content string) {
	t.Helper()
	full := filepath.Join(targetAbs, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
