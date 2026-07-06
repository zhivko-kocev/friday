package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/rules"
)

// populateTarget writes files under targetAbs.
func populateTarget(t *testing.T, targetAbs string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		full := filepath.Join(targetAbs, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func claudeishAdapter(targetAbs string) *config.Adapter {
	return &config.Adapter{
		Target: targetAbs,
		Rules: []*rules.Rule{
			{From: rules.FromSpec{"core.md", "rules/*.md"}, To: "CLAUDE.md", Strategy: rules.StrategyConcatenate},
			{From: rules.FromSpec{"agents/*.md"}, To: "agents/{filename}", Strategy: rules.StrategyCopy,
				Replace: map[string]string{"${ROOT}": "~/.friday"}},
			{From: rules.FromSpec{"skills/**/*"}, To: "skills/{relpath}", Strategy: rules.StrategyCopy},
		},
	}
}

func TestPlanImportPopulatesEmptyStore(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, nil)
	populateTarget(t, targetAbs, map[string]string{
		"CLAUDE.md":               "concatenated stuff",
		"agents/architect.md":     "plans; see ~/.friday/core/core.md",
		"skills/onboard/SKILL.md": "onboarding",
		"skills/onboard/tpl/t1":   "template",
	})
	changes, err := planImport("test", claudeishAdapter(targetAbs), storeAbs, targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, ch := range changes {
		got[ch.DestRel] = ch.Action.String()
	}
	want := map[string]string{
		"CLAUDE.md":               "unsupported", // concat is irreversible
		"agents/architect.md":     "create",
		"skills/onboard/SKILL.md": "create",
		"skills/onboard/tpl/t1":   "create",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for rel, action := range want {
		if got[rel] != action {
			t.Errorf("%s = %s, want %s", rel, got[rel], action)
		}
	}
	// Replace must invert on the way into the store.
	for _, ch := range changes {
		if ch.DestRel == "agents/architect.md" && string(ch.NewContent) != "plans; see ${ROOT}/core/core.md" {
			t.Errorf("replace not inverted: %q", ch.NewContent)
		}
	}
}

func TestImportEndToEndAndRerunInSync(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, nil)
	populateTarget(t, targetAbs, map[string]string{
		"agents/a.md":   "A",
		"skills/x/S.md": "S",
	})
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("LocalAppData", t.TempDir())

	cfg := config.NewDefault(config.ScopeUser, storeAbs, filepath.Dir(targetAbs),
		map[string]*config.Adapter{"test": claudeishAdapter(targetAbs)})

	changes, err := Import(cfg, Options{})
	if err != nil {
		t.Fatal(err)
	}
	created := 0
	for _, ch := range changes {
		if ch.Action == ActionCreate {
			created++
		}
	}
	if created != 2 {
		t.Fatalf("created %d, want 2 — %+v", created, changes)
	}
	for _, rel := range []string{"agents/a.md", "skills/x/S.md"} {
		if _, err := os.Stat(filepath.Join(storeAbs, filepath.FromSlash(rel))); err != nil {
			t.Errorf("store missing %s: %v", rel, err)
		}
	}

	// Second import: everything in-sync.
	changes, err = Import(cfg, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range changes {
		if ch.Action != ActionInSync && ch.Action != ActionUnsupported {
			t.Errorf("re-import %s = %s, want in-sync", ch.DestRel, ch.Action)
		}
	}
}

func TestImportLiteralTemplateMapsToFromPattern(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, nil)
	populateTarget(t, targetAbs, map[string]string{"AGENTS.md": "the core"})
	ad := &config.Adapter{
		Target: targetAbs,
		Rules: []*rules.Rule{
			{From: rules.FromSpec{"core.md", "core/core.md", "identity.md"}, To: "AGENTS.md", Strategy: rules.StrategyCopy},
		},
	}
	changes, err := planImport("test", ad, storeAbs, targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].DestRel != "core.md" || changes[0].Action != ActionCreate {
		t.Fatalf("got %+v, want create of core.md (first from-variant)", changes)
	}
}

func TestImportLiteralTemplatePrefersExistingVariant(t *testing.T) {
	// Store already keeps its entry file nested (developer-os shape); the
	// import must not grow a duplicate root core.md.
	storeAbs, targetAbs := scaffold(t, map[string]string{"core/core.md": "old"})
	populateTarget(t, targetAbs, map[string]string{"AGENTS.md": "new core"})
	ad := &config.Adapter{
		Target: targetAbs,
		Rules: []*rules.Rule{
			{From: rules.FromSpec{"core.md", "core/core.md", "identity.md"}, To: "AGENTS.md", Strategy: rules.StrategyCopy},
		},
	}
	changes, err := planImport("test", ad, storeAbs, targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].DestRel != "core/core.md" || changes[0].Action != ActionUpdate {
		t.Fatalf("got %+v, want update of the existing core/core.md", changes)
	}
}
