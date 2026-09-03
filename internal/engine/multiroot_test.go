package engine

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/rules"
)

// scaffold2 creates two source roots — "team" (lower precedence) and
// "local" (the primary) — plus a target dir, populating each root from its
// own file map. Returns roots in source.Load's own order (lowest precedence
// first, primary last), exactly what runWith threads into a planner.
func scaffold2(t *testing.T, teamFiles, localFiles map[string]string) (roots []rules.Root, targetAbs string) {
	t.Helper()
	base := t.TempDir()
	teamDir := filepath.Join(base, "team")
	localDir := filepath.Join(base, "local")
	targetAbs = filepath.Join(base, "target")
	for rel, content := range teamFiles {
		writeTarget(t, teamDir, rel, content)
	}
	for rel, content := range localFiles {
		writeTarget(t, localDir, rel, content)
	}
	if err := os.MkdirAll(targetAbs, 0o755); err != nil {
		t.Fatal(err)
	}
	return []rules.Root{{Name: "team", Dir: teamDir}, {Name: "local", Dir: localDir}}, targetAbs
}

// TestPlanConcatenateAcrossSourcesEntryAndGlobDispatch is Phase 2's D11 entry-
// file exception in one rule: the literal "core.md" pattern takes every
// source's own copy (team's identity, then local's, in precedence order),
// while the glob "rules/*.md" pattern unions distinct files and keeps only
// the highest-precedence copy of a colliding relative path.
func TestPlanConcatenateAcrossSourcesEntryAndGlobDispatch(t *testing.T) {
	roots, targetAbs := scaffold2(t,
		map[string]string{"core.md": "TEAM CORE", "rules/a.md": "TEAM A"},
		map[string]string{"core.md": "LOCAL CORE", "rules/a.md": "LOCAL A", "rules/b.md": "LOCAL B"},
	)
	ad := &config.Adapter{
		Target: targetAbs,
		Rules: []*rules.Rule{
			{From: rules.FromSpec{"core.md", "rules/*.md"}, To: "CLAUDE.md", Strategy: rules.StrategyConcatenate},
		},
	}
	changes, err := planPush(nil, "test", ad, roots, targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1", len(changes))
	}
	ch := changes[0]
	want := "TEAM CORE" + rules.DefaultSeparator + "LOCAL CORE" + rules.DefaultSeparator + "LOCAL A" + rules.DefaultSeparator + "LOCAL B"
	if string(ch.NewContent) != want {
		t.Errorf("content = %q, want %q", ch.NewContent, want)
	}
	wantSources := []string{"core.md", "core.md", "rules/a.md", "rules/b.md"}
	if len(ch.Sources) != len(wantSources) {
		t.Fatalf("Sources = %v, want %v", ch.Sources, wantSources)
	}
	for i, s := range wantSources {
		if ch.Sources[i] != s {
			t.Errorf("Sources[%d] = %q, want %q", i, ch.Sources[i], s)
		}
	}
	if len(ch.SourceAbs) != len(ch.Sources) {
		t.Fatalf("SourceAbs = %v, want one entry per Sources entry %v", ch.SourceAbs, ch.Sources)
	}
	if ch.SourceAbs[0] != filepath.Join(roots[0].Dir, "core.md") {
		t.Errorf("SourceAbs[0] = %q, want team's core.md", ch.SourceAbs[0])
	}
	if ch.SourceAbs[1] != filepath.Join(roots[1].Dir, "core.md") {
		t.Errorf("SourceAbs[1] = %q, want local's core.md", ch.SourceAbs[1])
	}
	if ch.SourceAbs[2] != filepath.Join(roots[1].Dir, filepath.FromSlash("rules/a.md")) {
		t.Errorf("SourceAbs[2] = %q, want local's (winning) rules/a.md", ch.SourceAbs[2])
	}
}

// TestPlanTokenizedCollisionKeepsHighestPrecedence covers D11's general
// rule for copy-style strategies: a distinct filename from every source
// applies, but a colliding relative path only renders from the
// highest-precedence source — never both, never the lower one.
func TestPlanTokenizedCollisionKeepsHighestPrecedence(t *testing.T) {
	roots, targetAbs := scaffold2(t,
		map[string]string{"agents/x.md": "TEAM X"},
		map[string]string{"agents/x.md": "LOCAL X", "agents/y.md": "LOCAL Y"},
	)
	ad := &config.Adapter{
		Target: targetAbs,
		Rules: []*rules.Rule{
			{From: rules.FromSpec{"agents/*.md"}, To: "agents/{filename}", Strategy: rules.StrategyCopy},
		},
	}
	changes, err := planPush(nil, "test", ad, roots, targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 {
		t.Fatalf("got %d changes, want 2 (one per distinct filename)", len(changes))
	}
	byDestRel := map[string]Change{}
	for _, ch := range changes {
		byDestRel[ch.DestRel] = ch
	}
	x, ok := byDestRel["agents/x.md"]
	if !ok {
		t.Fatal("no change for agents/x.md")
	}
	if string(x.NewContent) != "LOCAL X" {
		t.Errorf("agents/x.md content = %q, want %q (highest-precedence source, never TEAM X)", x.NewContent, "LOCAL X")
	}
	if x.SrcAbs != filepath.Join(roots[1].Dir, filepath.FromSlash("agents/x.md")) {
		t.Errorf("agents/x.md SrcAbs = %q, want local's copy", x.SrcAbs)
	}
	y, ok := byDestRel["agents/y.md"]
	if !ok {
		t.Fatal("no change for agents/y.md")
	}
	if string(y.NewContent) != "LOCAL Y" {
		t.Errorf("agents/y.md content = %q, want %q", y.NewContent, "LOCAL Y")
	}
}

// TestPlanMergeJSONResolvesAcrossSources exercises merge-json's single-
// winner semantics: it resolves the highest-precedence source that has the
// literal source file, moving from team to local as soon as local gains one.
func TestPlanMergeJSONResolvesAcrossSources(t *testing.T) {
	roots, targetAbs := scaffold2(t,
		map[string]string{"hooks/hooks.json": `{"a":1}`},
		nil,
	)
	ad := &config.Adapter{
		Target: targetAbs,
		Rules: []*rules.Rule{
			{From: rules.FromSpec{"hooks/hooks.json"}, To: "settings.json", Strategy: rules.StrategyMergeJSON},
		},
	}
	changes, err := planPush(nil, "claude", ad, roots, targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Action != ActionCreate {
		t.Fatalf("got %+v, want one create (resolved from team, the only source with it)", changes)
	}
	if changes[0].SrcAbs != filepath.Join(roots[0].Dir, filepath.FromSlash("hooks/hooks.json")) {
		t.Errorf("SrcAbs = %q, want team's hooks.json", changes[0].SrcAbs)
	}

	// Once local also has one, the higher-precedence local copy wins — and the
	// loser must be reported, not silently dropped. That "reported as a
	// Warning" half of the contract is the whole reason ResolveSingle returns
	// its shadowed list, so assert it here rather than only the winner.
	writeTarget(t, roots[1].Dir, "hooks/hooks.json", `{"b":2}`)
	changes, err = planPush(nil, "claude", ad, roots, targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if changes[0].SrcAbs != filepath.Join(roots[1].Dir, filepath.FromSlash("hooks/hooks.json")) {
		t.Errorf("SrcAbs = %q, want local's hooks.json now that it exists", changes[0].SrcAbs)
	}
	if changes[0].Warning == "" {
		t.Error("no Warning for the shadowed team copy — the contract says reported, never silently dropped")
	}
	if !strings.Contains(changes[0].Warning, roots[0].Name) {
		t.Errorf("Warning %q does not name the shadowed source %q", changes[0].Warning, roots[0].Name)
	}
	wantShadowed := filepath.Join(roots[0].Dir, filepath.FromSlash("hooks/hooks.json"))
	if !slices.Contains(changes[0].Shadowed, wantShadowed) {
		t.Errorf("Shadowed = %v, want it to list %q", changes[0].Shadowed, wantShadowed)
	}
	if changes[0].Root != roots[1].Name {
		t.Errorf("Root = %q, want the winning root %q", changes[0].Root, roots[1].Name)
	}
}

// TestPlanPullRefusesSourceOwnedFile pins binding decision 4: a configured
// source is read-only from friday's perspective, so a captured edit whose
// canonical file is owned by one must be refused rather than written into
// .sources/<name>, where the next `friday source update` would silently
// discard it. The refusal names both escape routes so the edit is never
// stranded.
func TestPlanPullRefusesSourceOwnedFile(t *testing.T) {
	roots, targetAbs := scaffold2(t,
		map[string]string{"agents/x.md": "TEAM X"},
		nil,
	)
	writeTarget(t, targetAbs, "agents/x.md", "EDITED X")
	ad := &config.Adapter{
		Target: targetAbs,
		Rules: []*rules.Rule{
			{From: rules.FromSpec{"agents/*.md"}, To: "agents/{filename}", Strategy: rules.StrategyCopy},
		},
	}
	changes, err := planPull(nil, "test", ad, roots, targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1", len(changes))
	}
	if changes[0].Action != ActionUnsupported {
		t.Errorf("Action = %v, want ActionUnsupported", changes[0].Action)
	}
	if changes[0].DestPath != "" {
		t.Errorf("DestPath = %q, want empty — nothing may be written into a source checkout", changes[0].DestPath)
	}
	for _, want := range []string{`source "team"`, "own repository", "primary store"} {
		if !strings.Contains(changes[0].Reason, want) {
			t.Errorf("Reason = %q, want it to mention %q", changes[0].Reason, want)
		}
	}
}

// TestPlanImportDestRootRouting covers all three branches of import's
// write-back routing (plan step 6): an existing owner always wins over
// ToSource; ToSource picks the destination for a genuinely new capture;
// and the primary is the final fallback when neither applies.
func TestPlanImportDestRootRouting(t *testing.T) {
	rule := &rules.Rule{From: rules.FromSpec{"agents/*.md"}, To: "agents/{filename}", Strategy: rules.StrategyCopy}

	t.Run("a capture owned by a configured source is refused", func(t *testing.T) {
		roots, targetAbs := scaffold2(t, map[string]string{"agents/x.md": "OLD X"}, nil)
		writeTarget(t, targetAbs, "agents/x.md", "NEW X")
		ad := &config.Adapter{Target: targetAbs, Rules: []*rules.Rule{rule}}
		// team owns the file, and a configured source is read-only: writing
		// there would be discarded by the next `friday source update`.
		changes, err := planImport(nil, "test", ad, roots, targetAbs, "local")
		if err != nil {
			t.Fatal(err)
		}
		if len(changes) != 1 {
			t.Fatalf("got %d changes, want 1", len(changes))
		}
		if changes[0].Action != ActionUnsupported {
			t.Errorf("Action = %v, want ActionUnsupported (source-owned capture must be refused)", changes[0].Action)
		}
		if changes[0].DestPath != "" {
			t.Errorf("DestPath = %q, want empty — a refused capture writes nowhere", changes[0].DestPath)
		}
		// The refusal must name both escape routes, or the edit is stranded.
		for _, want := range []string{`source "team"`, "own repository", "primary store"} {
			if !strings.Contains(changes[0].Reason, want) {
				t.Errorf("Reason = %q, want it to mention %q", changes[0].Reason, want)
			}
		}
	})

	t.Run("ToSource picks destination for a new capture", func(t *testing.T) {
		roots, targetAbs := scaffold2(t, nil, nil)
		writeTarget(t, targetAbs, "agents/y.md", "BRAND NEW Y")
		ad := &config.Adapter{Target: targetAbs, Rules: []*rules.Rule{rule}}
		changes, err := planImport(nil, "test", ad, roots, targetAbs, "team")
		if err != nil {
			t.Fatal(err)
		}
		if len(changes) != 1 {
			t.Fatalf("got %d changes, want 1", len(changes))
		}
		want := filepath.Join(roots[0].Dir, filepath.FromSlash("agents/y.md"))
		if changes[0].DestPath != want {
			t.Errorf("DestPath = %q, want %q (ToSource=team)", changes[0].DestPath, want)
		}
		if changes[0].Action != ActionCreate {
			t.Errorf("Action = %v, want ActionCreate", changes[0].Action)
		}
	})

	t.Run("primary is the fallback with no owner and no ToSource", func(t *testing.T) {
		roots, targetAbs := scaffold2(t, nil, nil)
		writeTarget(t, targetAbs, "agents/z.md", "BRAND NEW Z")
		ad := &config.Adapter{Target: targetAbs, Rules: []*rules.Rule{rule}}
		changes, err := planImport(nil, "test", ad, roots, targetAbs, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(changes) != 1 {
			t.Fatalf("got %d changes, want 1", len(changes))
		}
		want := filepath.Join(roots[len(roots)-1].Dir, filepath.FromSlash("agents/z.md"))
		if changes[0].DestPath != want {
			t.Errorf("DestPath = %q, want %q (primary, the final fallback)", changes[0].DestPath, want)
		}
		if changes[0].Action != ActionCreate {
			t.Errorf("Action = %v, want ActionCreate", changes[0].Action)
		}
	})
}
