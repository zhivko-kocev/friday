package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/rules"
)

// scaffold creates a store + target dir tree and returns their abs paths.
func scaffold(t *testing.T, files map[string]string) (storeAbs, targetAbs string) {
	t.Helper()
	root := t.TempDir()
	storeAbs = filepath.Join(root, "store")
	targetAbs = filepath.Join(root, "target")
	for path, content := range files {
		full := filepath.Join(storeAbs, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(targetAbs, 0o755); err != nil {
		t.Fatal(err)
	}
	return
}

func TestPlanPushCopy(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{
		"identity.md":      "id",
		"rules/a.md":       "A",
		"rules/b.md":       "B",
		"rules/.hidden.md": "skip me",
	})
	ad := &config.Adapter{
		Target: targetAbs,
		Rules: []*rules.Rule{
			{From: rules.FromSpec{"identity.md"}, To: "AGENTS.md", Strategy: rules.StrategyCopy},
			{From: rules.FromSpec{"rules/*.md"}, To: "rules/{filename}", Strategy: rules.StrategyCopy},
		},
	}
	changes, err := planPush("test", ad, storeAbs, targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	// 1 identity + 2 visible rules (.hidden excluded) = 3 changes
	if len(changes) != 3 {
		t.Fatalf("got %d changes, want 3 — %+v", len(changes), changes)
	}
	for _, ch := range changes {
		if ch.Action != ActionCreate {
			t.Errorf("change %s action = %s, want create", ch.DestRel, ch.Action)
		}
	}
}

func TestPlanPushConcatenate(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{
		"identity.md": "ID",
		"rules/a.md":  "A",
		"rules/b.md":  "B",
	})
	ad := &config.Adapter{
		Target: targetAbs,
		Rules: []*rules.Rule{
			{
				From:     rules.FromSpec{"identity.md", "rules/*.md"},
				To:       "CLAUDE.md",
				Strategy: rules.StrategyConcatenate,
			},
		},
	}
	changes, err := planPush("test", ad, storeAbs, targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1", len(changes))
	}
	want := "ID" + rules.DefaultSeparator + "A" + rules.DefaultSeparator + "B"
	if string(changes[0].NewContent) != want {
		t.Errorf("content = %q, want %q", changes[0].NewContent, want)
	}
}

func TestPlanPushInSync(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{"a.md": "hello"})
	// Pre-create the target identical to the source.
	if err := os.WriteFile(filepath.Join(targetAbs, "a.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	ad := &config.Adapter{
		Target: targetAbs,
		Rules: []*rules.Rule{
			{From: rules.FromSpec{"a.md"}, To: "a.md", Strategy: rules.StrategyCopy},
		},
	}
	changes, _ := planPush("test", ad, storeAbs, targetAbs)
	if len(changes) != 1 || changes[0].Action != ActionInSync {
		t.Errorf("got %+v, want one in-sync change", changes)
	}
}

func TestPlanPullSkipsConcatenate(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{
		"identity.md": "x",
		"rules/a.md":  "y",
	})
	ad := &config.Adapter{
		Target: targetAbs,
		Rules: []*rules.Rule{
			{
				From:     rules.FromSpec{"identity.md", "rules/*.md"},
				To:       "CLAUDE.md",
				Strategy: rules.StrategyConcatenate,
			},
		},
	}
	changes, err := planPull("test", ad, storeAbs, targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Action != ActionUnsupported {
		t.Errorf("got %+v, want unsupported", changes)
	}
}

func TestPlanPullCopy(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{"a.md": "old"})
	if err := os.WriteFile(filepath.Join(targetAbs, "a.md"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	ad := &config.Adapter{
		Target: targetAbs,
		Rules: []*rules.Rule{
			{From: rules.FromSpec{"a.md"}, To: "a.md", Strategy: rules.StrategyCopy},
		},
	}
	changes, err := planPull("test", ad, storeAbs, targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Action != ActionUpdate {
		t.Fatalf("got %+v, want update", changes)
	}
	if string(changes[0].NewContent) != "new" {
		t.Errorf("NewContent = %q, want target value", changes[0].NewContent)
	}
}

func TestPlanPullMissingTargetSkipped(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{"a.md": "x"})
	ad := &config.Adapter{
		Target: targetAbs,
		Rules: []*rules.Rule{
			{From: rules.FromSpec{"a.md"}, To: "a.md", Strategy: rules.StrategyCopy},
		},
	}
	changes, err := planPull("test", ad, storeAbs, targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	// Target file doesn't exist — nothing to pull, no entries.
	if len(changes) != 0 {
		t.Errorf("got %+v, want zero changes", changes)
	}
}
