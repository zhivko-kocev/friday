package setupcmd

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zhivko-kocev/friday/internal/presets"
)

// withStore redirects $HOME so config.UserStoreDir resolves into a temp dir,
// then writes the given files into ~/.friday. Returns the store path.
func withStore(t *testing.T, files map[string]string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	store := filepath.Join(home, ".friday")
	for rel, content := range files {
		full := filepath.Join(store, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

var storeFixture = map[string]string{
	"core/core.md":                "# Core doctrine",
	"rules/general.md":            "be concise",
	"agents/architect.md":         "plans things",
	"skills/onboard/SKILL.md":     "onboarding",
	"skills/onboard/templates/t1": "template",
	"skills/start-day/SKILL.md":   "morning",
}

func TestCatalog(t *testing.T) {
	store := withStore(t, storeFixture)
	items, err := Catalog(store)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, it := range items {
		got[it.Category+"/"+it.Name] = it.Patterns[0]
	}
	want := map[string]string{
		"core/core":        "core/core.md",
		"rules/general":    "rules/general.md",
		"agents/architect": "agents/architect.md",
		"skills/onboard":   "skills/onboard/**/*",
		"skills/start-day": "skills/start-day/**/*",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Catalog = %v, want %v", got, want)
	}
	// Entry file must come first so concat from-lists keep core leading.
	if items[0].Category != "core" {
		t.Errorf("first item = %+v, want the core entry", items[0])
	}
}

func TestCatalogPrefersCoreOverLegacy(t *testing.T) {
	store := withStore(t, map[string]string{"core.md": "x", "identity.md": "y"})
	items, err := Catalog(store)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Patterns[0] != "core.md" {
		t.Errorf("got %+v, want single core.md item", items)
	}
}

func TestParseSelection(t *testing.T) {
	cases := []struct {
		in      string
		n       int
		want    []int
		wantErr bool
	}{
		{"", 3, []int{0, 1, 2}, false},
		{"all", 3, []int{0, 1, 2}, false},
		{"a", 2, []int{0, 1}, false},
		{"1,3", 3, []int{0, 2}, false},
		{"2-4", 5, []int{1, 2, 3}, false},
		{"1,2-3,2", 3, []int{0, 1, 2}, false},
		{"4-2", 5, []int{1, 2, 3}, false}, // reversed range tolerated
		{"0", 3, nil, true},
		{"9", 3, nil, true},
		{"x", 3, nil, true},
	}
	for _, c := range cases {
		got, err := parseSelection(c.in, c.n)
		if (err != nil) != c.wantErr {
			t.Errorf("%q: err=%v wantErr=%v", c.in, err, c.wantErr)
			continue
		}
		if !c.wantErr && !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %v, want %v", c.in, got, c.want)
		}
	}
}

func TestFilterAdapterNarrowsToSelection(t *testing.T) {
	store := withStore(t, storeFixture)
	items, err := Catalog(store)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := presets.Get("claude")
	// Select core + the onboard skill only.
	var selected []Item
	for _, it := range items {
		if it.Name == "core" || it.Name == "onboard" {
			selected = append(selected, it)
		}
	}
	ad, only, skipped := FilterAdapter(p, selected)
	if len(skipped) != 0 {
		t.Errorf("skipped = %+v, want none", skipped)
	}
	if ad == nil || len(ad.Rules) != 2 {
		t.Fatalf("rules = %+v, want concat + skills", ad)
	}
	if ad.Rules[0].To != "CLAUDE.md" || len(ad.Rules[0].From) != 1 || ad.Rules[0].From[0] != "core/core.md" {
		t.Errorf("concat rule = %+v, want from [core/core.md]", ad.Rules[0])
	}
	// Copy rules keep the preset's pattern — the anchor drives {relpath} —
	// and the selection is enforced via the only-globs instead.
	if ad.Rules[1].To != ".claude/skills/{relpath}" || ad.Rules[1].From[0] != "skills/**/*" {
		t.Errorf("skills rule = %+v, want original pattern", ad.Rules[1])
	}
	wantOnly := []string{"core/core.md", "skills/onboard/**/*"}
	if !reflect.DeepEqual(only, wantOnly) {
		t.Errorf("only = %v, want %v", only, wantOnly)
	}
}

func TestFilterAdapterReportsUnmappedItems(t *testing.T) {
	store := withStore(t, storeFixture)
	items, err := Catalog(store)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := presets.Get("codex") // codex has no skills/agents mapping
	ad, _, skipped := FilterAdapter(p, items)
	if ad == nil {
		t.Fatal("core+rules should map to codex's AGENTS.md")
	}
	var names []string
	for _, it := range skipped {
		names = append(names, it.Category+"/"+it.Name)
	}
	want := []string{"agents/architect", "skills/onboard", "skills/start-day"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("skipped = %v, want %v", names, want)
	}
}

func TestRunEndToEnd(t *testing.T) {
	withStore(t, storeFixture)
	project := t.TempDir()

	// Script: agent prompt answered by --agent; item prompt selects all.
	changes, err := Run(strings.NewReader("all\n"), project, Options{Agent: "claude"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) == 0 {
		t.Fatal("no changes")
	}
	for _, rel := range []string{
		"CLAUDE.md",
		".claude/agents/architect.md",
		".claude/skills/onboard/SKILL.md",
		".claude/skills/onboard/templates/t1",
		".claude/skills/start-day/SKILL.md",
	} {
		if _, err := os.Stat(filepath.Join(project, filepath.FromSlash(rel))); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}
	claudeMd, err := os.ReadFile(filepath.Join(project, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(claudeMd), "# Core doctrine") || !strings.Contains(string(claudeMd), "be concise") {
		t.Errorf("CLAUDE.md = %q, want core + rules concatenated", claudeMd)
	}

	// Re-run with the same selection: everything in-sync, nothing rewritten.
	changes, err = Run(strings.NewReader("all\n"), project, Options{Agent: "claude"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range changes {
		if ch.Action.String() != "in-sync" {
			t.Errorf("re-run: %s action = %s, want in-sync", ch.DestRel, ch.Action)
		}
	}
}

func TestRunSubsetSelection(t *testing.T) {
	withStore(t, storeFixture)
	project := t.TempDir()

	// Items order: core, agents/architect, rules/general, skills/onboard, skills/start-day.
	// Select only the core entry (index 1).
	if _, err := Run(strings.NewReader("1\n"), project, Options{Agent: "claude"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(project, "CLAUDE.md")); err != nil {
		t.Errorf("missing CLAUDE.md: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, ".claude")); !os.IsNotExist(err) {
		t.Errorf(".claude should not exist when only core is selected (err=%v)", err)
	}
}

func TestRunUnknownAgent(t *testing.T) {
	withStore(t, storeFixture)
	if _, err := Run(strings.NewReader("\n"), t.TempDir(), Options{Agent: "nope"}, nil); err == nil {
		t.Fatal("expected unknown-agent error")
	}
}

func TestRunNoStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := Run(strings.NewReader("\n"), t.TempDir(), Options{Agent: "claude"}, nil); err == nil {
		t.Fatal("expected missing-store error")
	}
}

func TestPromoteCapturesProjectAdditions(t *testing.T) {
	store := withStore(t, storeFixture)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("LocalAppData", t.TempDir())
	project := t.TempDir()

	// A teammate dropped a skill + an agent into the project by hand.
	for rel, content := range map[string]string{
		".claude/skills/team-skill/SKILL.md": "team skill; base at ~/.friday/core/core.md",
		".claude/agents/tester.md":           "tests things",
		"CLAUDE.md":                          "project instructions (concat — not promotable)",
	} {
		full := filepath.Join(project, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	changes, err := Promote(strings.NewReader(""), project, PromoteOptions{Agent: "claude"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, ch := range changes {
		got[ch.DestRel] = ch.Action.String()
	}
	if got["skills/team-skill/SKILL.md"] != "create" || got["agents/tester.md"] != "create" {
		t.Fatalf("changes = %v, want both project additions created", got)
	}
	if got["CLAUDE.md"] != "unsupported" {
		t.Errorf("concat file should be unsupported, got %v", got)
	}
	// Replace inverted on the way up.
	blob, err := os.ReadFile(filepath.Join(store, "skills", "team-skill", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(blob), "${CLAUDE_PLUGIN_ROOT}/core/core.md") {
		t.Errorf("store content = %q, want marker restored", blob)
	}
}

func TestPromoteFilterSelectsOneItem(t *testing.T) {
	store := withStore(t, storeFixture)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("LocalAppData", t.TempDir())
	project := t.TempDir()
	for rel, content := range map[string]string{
		".claude/skills/wanted/SKILL.md":   "want this",
		".claude/skills/unwanted/SKILL.md": "not this",
	} {
		full := filepath.Join(project, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	changes, err := Promote(strings.NewReader(""), project, PromoteOptions{
		Agent:   "claude",
		Filters: []string{".claude/skills/wanted"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].DestRel != "skills/wanted/SKILL.md" {
		t.Fatalf("changes = %+v, want only the wanted skill", changes)
	}
	if _, err := os.Stat(filepath.Join(store, "skills", "unwanted")); !os.IsNotExist(err) {
		t.Errorf("unwanted skill leaked into the store (err=%v)", err)
	}
}
