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

func TestPlanPushReplace(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{
		"core.md":       "Read ${ROOT}/standards/go.md",
		"skills/s.md":   "Follow ${ROOT}/core/core.md",
		"skills/raw.md": "no marker here",
	})
	replace := map[string]string{"${ROOT}": "~/.claude"}
	ad := &config.Adapter{
		Target: targetAbs,
		Rules: []*rules.Rule{
			{From: rules.FromSpec{"core.md"}, To: "CLAUDE.md", Strategy: rules.StrategyConcatenate, Replace: replace},
			{From: rules.FromSpec{"skills/*.md"}, To: "skills/{filename}", Strategy: rules.StrategyCopy, Replace: replace},
		},
	}
	changes, err := planPush("test", ad, storeAbs, targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"CLAUDE.md":     "Read ~/.claude/standards/go.md",
		"skills/raw.md": "no marker here",
		"skills/s.md":   "Follow ~/.claude/core/core.md",
	}
	if len(changes) != len(want) {
		t.Fatalf("got %d changes, want %d — %+v", len(changes), len(want), changes)
	}
	for _, ch := range changes {
		if got := string(ch.NewContent); got != want[filepath.ToSlash(ch.DestRel)] {
			t.Errorf("%s content = %q, want %q", ch.DestRel, got, want[filepath.ToSlash(ch.DestRel)])
		}
	}
}

func TestPlanPullReplaceRoundTrip(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{
		"skills/s.md": "Follow ${ROOT}/core/core.md",
	})
	replace := map[string]string{"${ROOT}": "~/.claude"}
	ad := &config.Adapter{
		Target: targetAbs,
		Rules: []*rules.Rule{
			{From: rules.FromSpec{"skills/*.md"}, To: "skills/{filename}", Strategy: rules.StrategyCopy, Replace: replace},
		},
	}
	// Simulate a prior push: target holds the rewritten form.
	if err := os.MkdirAll(filepath.Join(targetAbs, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetAbs, "skills", "s.md"), []byte("Follow ~/.claude/core/core.md"), 0o644); err != nil {
		t.Fatal(err)
	}
	changes, err := planPull("test", ad, storeAbs, targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	// Inverse-rewritten target equals the store — nothing to pull.
	if len(changes) != 1 || changes[0].Action != ActionInSync {
		t.Fatalf("got %+v, want one in-sync change", changes)
	}

	// A target edit comes back with the marker restored.
	if err := os.WriteFile(filepath.Join(targetAbs, "skills", "s.md"), []byte("edited: see ~/.claude/standards/go.md"), 0o644); err != nil {
		t.Fatal(err)
	}
	changes, err = planPull("test", ad, storeAbs, targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Action != ActionUpdate {
		t.Fatalf("got %+v, want one update", changes)
	}
	if got := string(changes[0].NewContent); got != "edited: see ${ROOT}/standards/go.md" {
		t.Errorf("NewContent = %q, marker not restored", got)
	}
}

// Regression: store content that naturally contains a replace VALUE (e.g. a
// skill mentioning ~/.claude/...) passes through push untouched; pull must
// report it in-sync, not "invert" it into the marker and corrupt the store.
func TestPlanPullReplaceNaturalValueUntouched(t *testing.T) {
	storeContent := "state lives in ~/.claude/developer-os/prefs.md; read ${ROOT}/core.md"
	storeAbs, targetAbs := scaffold(t, map[string]string{"skills/s.md": storeContent})
	replace := map[string]string{"${ROOT}": "~/.claude"}
	ad := &config.Adapter{
		Target: targetAbs,
		Rules: []*rules.Rule{
			{From: rules.FromSpec{"skills/*.md"}, To: "skills/{filename}", Strategy: rules.StrategyCopy, Replace: replace},
		},
	}
	// What push writes: marker resolved, natural path untouched.
	pushed := "state lives in ~/.claude/developer-os/prefs.md; read ~/.claude/core.md"
	if err := os.MkdirAll(filepath.Join(targetAbs, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetAbs, "skills", "s.md"), []byte(pushed), 0o644); err != nil {
		t.Fatal(err)
	}
	changes, err := planPull("test", ad, storeAbs, targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Action != ActionInSync {
		t.Fatalf("got %+v, want in-sync (no phantom update)", changes)
	}
	if string(changes[0].NewContent) != storeContent {
		t.Errorf("NewContent = %q, store content must stay untouched", changes[0].NewContent)
	}
}

func TestFilterOnly(t *testing.T) {
	changes := []Change{
		{DestRel: "CLAUDE.md", Sources: []string{"core.md", "rules/a.md"}},
		{DestRel: "skills/x/S.md", Sources: []string{"skills/x/S.md"}},
		{DestRel: "skills/y/S.md", Sources: []string{"skills/y/S.md"}},
	}
	got := filterOnly(changes, []string{"skills/x/**/*"})
	if len(got) != 1 || got[0].DestRel != "skills/x/S.md" {
		t.Errorf("got %+v, want only skills/x", got)
	}
	// A concat change survives when any member matches.
	got = filterOnly(changes, []string{"rules/*.md"})
	if len(got) != 1 || got[0].DestRel != "CLAUDE.md" {
		t.Errorf("got %+v, want the concat change", got)
	}
	// Empty filter keeps everything.
	if got := filterOnly(changes, nil); len(got) != 3 {
		t.Errorf("nil filter dropped changes: %+v", got)
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
