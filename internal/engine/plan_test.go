package engine

import (
	"os"
	"path/filepath"
	"strings"
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

// singleRoot wraps a bare store dir as the one-element []rules.Root every
// planPush/planPull/planImport test call passes — the single-source shape
// Phase 2's multi-root signature must keep behaving exactly like before.
func singleRoot(dir string) []rules.Root {
	return []rules.Root{{Name: "local", Dir: dir}}
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
	changes, err := planPush(nil, "test", ad, singleRoot(storeAbs), targetAbs)
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

// TestPlanImportRefusesRender locks the import-side gate added alongside the
// render strategy: without it, importing a literal-target render rule (e.g.
// codex's agents/*.md -> agents/{stem}.toml) would read the generated .toml
// straight back over the store markdown — silent store corruption on a
// supported config.
func TestPlanImportRefusesRender(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{
		"agents/architect.md": "---\nname: architect\n---\noriginal store body",
	})
	if err := os.MkdirAll(filepath.Join(targetAbs, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetAbs, "agents", "architect.toml"), []byte("name = \"architect\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ad := &config.Adapter{
		Target: targetAbs,
		Rules: []*rules.Rule{
			{
				From:     rules.FromSpec{"agents/architect.md"},
				To:       "agents/architect.toml",
				Strategy: rules.StrategyRender,
				Surface:  rules.SurfaceCodexAgent,
				Merge:    rules.StrategyCopy,
			},
		},
	}
	changes, err := planImport(nil, "test", ad, singleRoot(storeAbs), targetAbs, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Action != ActionUnsupported {
		t.Fatalf("render import must be reported unsupported, got %+v", changes)
	}
	// The store markdown must be untouched by planning (it never writes, but be
	// explicit that the corruption path is closed).
	if got, _ := os.ReadFile(filepath.Join(storeAbs, "agents", "architect.md")); !strings.Contains(string(got), "original store body") {
		t.Errorf("store markdown was altered: %s", got)
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
	changes, err := planPush(nil, "test", ad, singleRoot(storeAbs), targetAbs)
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
	changes, _ := planPush(nil, "test", ad, singleRoot(storeAbs), targetAbs)
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
	changes, err := planPull(nil, "test", ad, singleRoot(storeAbs), targetAbs)
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
	changes, err := planPull(nil, "test", ad, singleRoot(storeAbs), targetAbs)
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
	changes, err := planPush(nil, "test", ad, singleRoot(storeAbs), targetAbs)
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
	changes, err := planPull(nil, "test", ad, singleRoot(storeAbs), targetAbs)
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
	changes, err = planPull(nil, "test", ad, singleRoot(storeAbs), targetAbs)
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
	changes, err := planPull(nil, "test", ad, singleRoot(storeAbs), targetAbs)
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
	changes, err := planPull(nil, "test", ad, singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	// Target file doesn't exist — nothing to pull, no entries.
	if len(changes) != 0 {
		t.Errorf("got %+v, want zero changes", changes)
	}
}

func TestPlanConcatenateMaxBytesWarning(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{"core.md": strings.Repeat("x", 100)})
	ad := &config.Adapter{
		Target: targetAbs,
		Rules: []*rules.Rule{
			{From: rules.FromSpec{"core.md"}, To: "out.md", Strategy: rules.StrategyConcatenate, MaxBytes: 50},
		},
	}
	changes, err := planPush(nil, "test", ad, singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Action != ActionCreate {
		t.Fatalf("got %+v", changes)
	}
	if !strings.Contains(changes[0].Warning, "exceeds") {
		t.Errorf("Warning = %q, want the over-limit warning", changes[0].Warning)
	}
	// Under the limit: no warning.
	ad.Rules[0].MaxBytes = 200
	changes, _ = planPush(nil, "test", ad, singleRoot(storeAbs), targetAbs)
	if changes[0].Warning != "" {
		t.Errorf("Warning = %q, want none under the limit", changes[0].Warning)
	}
}

func TestPlanCopyLiteralToFirstVariantWins(t *testing.T) {
	// A store mid-migration carries both core.md and the legacy identity.md.
	// The from-list is most-preferred first, so core.md must win — planning
	// both would write the same dest twice with the LAST variant on top.
	storeAbs, targetAbs := scaffold(t, map[string]string{
		"core.md":     "new core",
		"identity.md": "legacy",
	})
	ad := &config.Adapter{
		Target: targetAbs,
		Rules: []*rules.Rule{
			{From: rules.FromSpec{"core.md", "core/core.md", "identity.md"}, To: "AGENTS.md", Strategy: rules.StrategyCopy},
		},
	}
	changes, err := planPush(nil, "test", ad, singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1 (one dest, first variant wins): %+v", len(changes), changes)
	}
	if changes[0].Sources[0] != "core.md" || string(changes[0].NewContent) != "new core" {
		t.Errorf("winner = %v %q, want core.md / \"new core\"", changes[0].Sources, changes[0].NewContent)
	}
}

func TestPlanPullLiteralToCapturesPreferredVariantOnly(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{
		"core.md":     "new core",
		"identity.md": "legacy",
	})
	if err := os.WriteFile(filepath.Join(targetAbs, "AGENTS.md"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	ad := &config.Adapter{
		Target: targetAbs,
		Rules: []*rules.Rule{
			{From: rules.FromSpec{"core.md", "core/core.md", "identity.md"}, To: "AGENTS.md", Strategy: rules.StrategyCopy},
		},
	}
	changes, err := planPull(nil, "test", ad, singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].DestRel != "core.md" {
		t.Fatalf("got %+v, want a single pull into core.md — fanning one target over every variant corrupts the others", changes)
	}
}

func TestPlanCopyTokenizedToReportsMissingPerPattern(t *testing.T) {
	// With a tokenized template the from-patterns are independent globs; a
	// typo'd second pattern must be reported, not silently absorbed because
	// the first one matched.
	storeAbs, targetAbs := scaffold(t, map[string]string{"rules/a.md": "A"})
	ad := &config.Adapter{
		Target: targetAbs,
		Rules: []*rules.Rule{
			{From: rules.FromSpec{"rules/*.md", "standrds/*.md"}, To: "x/{filename}", Strategy: rules.StrategyCopy},
		},
	}
	changes, err := planPush(nil, "test", ad, singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	var missing []string
	for _, ch := range changes {
		if ch.Action == ActionMissingSource {
			missing = append(missing, ch.Sources...)
		}
	}
	if len(missing) != 1 || missing[0] != "standrds/*.md" {
		t.Errorf("missing-source = %v, want exactly the typo'd pattern", missing)
	}
}

func TestPullContentPreservesUnchangedStoreLines(t *testing.T) {
	r := &rules.Rule{
		From:    rules.FromSpec{"a.md"},
		To:      "a.md",
		Replace: map[string]string{"${ROOT}": "~/.friday"},
	}
	if err := r.Normalize(); err != nil {
		t.Fatal(err)
	}
	// Line 1 mentions ~/.friday NATURALLY (no marker in the store). Line 3
	// carries the marker. The target edit touches only line 2.
	store := []byte("clone into ~/.friday\nline two\nsee ${ROOT}/core.md\n")
	target := r.ApplyReplace(store) // what push wrote
	target = []byte(strings.Replace(string(target), "line two", "line two EDITED ~/.friday", 1))

	got := string(pullContent(r, store, target))
	want := "clone into ~/.friday\nline two EDITED ${ROOT}\nsee ${ROOT}/core.md\n"
	if got != want {
		t.Errorf("pullContent:\n got %q\nwant %q", got, want)
	}
}

// mcp and co-owned permissions DO have a pull inverse, and planPull already
// uses it. Reporting every render rule unsupported meant `friday claim` could
// never capture a user's existing native MCP servers or permission policy off
// an already-configured agent, while `friday pull` recovered the identical
// content from the identical rule shape.
func TestPlanImportCapturesInvertibleRenderSurfaces(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{
		"mcp.yaml": "version: 1\nservers: {}\n",
	})
	if err := os.WriteFile(filepath.Join(targetAbs, "mcp.json"), []byte(
		`{"mcpServers":{"fs":{"command":"npx","args":["-y","srv"]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ad := &config.Adapter{
		Target: targetAbs,
		Rules: []*rules.Rule{{
			From:     rules.FromSpec{"mcp.yaml"},
			To:       "mcp.json",
			Strategy: rules.StrategyRender,
			Surface:  rules.SurfaceMCP,
			Merge:    rules.StrategyMergeJSON,
		}},
	}
	changes, err := planImport(nil, "claude", ad, singleRoot(storeAbs), targetAbs, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) == 0 {
		t.Fatal("mcp render rule produced no import change at all")
	}
	for _, ch := range changes {
		if ch.Action == ActionUnsupported {
			t.Fatalf("mcp render rule reported unsupported (%s) even though pull inverts it", ch.Reason)
		}
	}
	// The capture lands in the canonical schema, not as a raw copy of the
	// native file.
	if !strings.HasSuffix(filepath.ToSlash(changes[0].DestPath), "mcp.yaml") {
		t.Errorf("capture destination = %q, want the canonical mcp.yaml", changes[0].DestPath)
	}
	if !strings.Contains(string(changes[0].NewContent), "fs") {
		t.Errorf("captured mcp.yaml lost the server:\n%s", changes[0].NewContent)
	}
}

// The genuinely one-way surfaces stay refused, each with a reason that names
// its own obstacle rather than one blanket claim for all five.
func TestPlanImportRefusesOneWayRenderSurfaces(t *testing.T) {
	cases := map[string]struct {
		surface string
		merge   string
		want    string
	}{
		"defaults":         {rules.SurfaceDefaults, rules.StrategyMergeJSON, "defaults"},
		"gemini-command":   {rules.SurfaceGeminiCommand, rules.StrategyCopy, "frontmatter"},
		"permissions copy": {rules.SurfacePermissions, rules.StrategyCopy, "friday-owned outright"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			storeAbs, targetAbs := scaffold(t, map[string]string{"defaults.yaml": "version: 1\n"})
			if err := os.WriteFile(filepath.Join(targetAbs, "settings.json"), []byte(`{}`), 0o644); err != nil {
				t.Fatal(err)
			}
			ad := &config.Adapter{Target: targetAbs, Rules: []*rules.Rule{{
				From:     rules.FromSpec{"defaults.yaml"},
				To:       "settings.json",
				Strategy: rules.StrategyRender,
				Surface:  c.surface,
				Merge:    c.merge,
			}}}
			changes, err := planImport(nil, "claude", ad, singleRoot(storeAbs), targetAbs, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(changes) != 1 || changes[0].Action != ActionUnsupported {
				t.Fatalf("want one unsupported change, got %+v", changes)
			}
			if !strings.Contains(changes[0].Reason, c.want) {
				t.Errorf("reason %q does not explain this surface's own obstacle (%q)", changes[0].Reason, c.want)
			}
		})
	}
}
