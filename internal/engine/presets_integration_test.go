package engine

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/presets"
)

// TestPresetsRenderMCPAcrossSixAdapters is Phase 3's own verification: one
// store file, pushed through the *real* built-in preset registry (not
// schema_conformance_test.go's hand-built test adapters), lands correctly on
// every adapter that has a documented MCP surface. This is the difference
// between "the render package projects mcp.yaml correctly in the abstract"
// (already proven) and "the preset wiring — Target, Rule.To, Merge — that a
// real `friday push` uses is actually correct" (proven here).
func TestPresetsRenderMCPAcrossSixAdapters(t *testing.T) {
	root := t.TempDir()
	storeAbs := filepath.Join(root, "store")
	if err := os.MkdirAll(storeAbs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeAbs, "mcp.yaml"), []byte(conformanceMCPYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	targetRoot := filepath.Join(root, "target")
	adapters := map[string]*config.Adapter{}
	for _, name := range []string{"claude", "codex", "copilot", "opencode", "gemini", "oh-my-pi"} {
		p, ok := presets.Get(name)
		if !ok {
			t.Fatalf("preset %s missing from registry", name)
		}
		adapters[name] = p.Adapter()
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if runtime.GOOS == "windows" {
		t.Setenv("LocalAppData", t.TempDir())
	}
	cfg := config.NewDefault(config.ScopeUser, storeAbs, targetRoot, adapters)

	changes, err := Push(cfg, Options{Force: true})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	byAdapterSrc := map[string]Change{}
	for _, ch := range changes {
		if len(ch.Sources) > 0 {
			byAdapterSrc[ch.Adapter+":"+ch.Sources[0]] = ch
		}
	}
	get := func(adapter string) Change {
		t.Helper()
		ch, ok := byAdapterSrc[adapter+":mcp.yaml"]
		if !ok {
			t.Fatalf("no mcp.yaml change for %s (have %v)", adapter, changesKeys(byAdapterSrc))
		}
		return ch
	}

	t.Run("claude lands at ~/.claude.json sibling, not settings.json", func(t *testing.T) {
		ch := get("claude")
		requireAction(t, ch, ActionCreate)
		wantPath := filepath.Join(targetRoot, ".claude.json")
		if ch.DestPath != wantPath {
			t.Fatalf("claude mcp DestPath = %s, want %s", ch.DestPath, wantPath)
		}
		doc := decodeJSONMap(t, ch.NewContent)
		assertMCPEntryHasType(t, mustObj(t, doc["mcpServers"]), "github", "stdio")
	})
	t.Run("codex lands at .codex/config.toml", func(t *testing.T) {
		ch := get("codex")
		requireAction(t, ch, ActionCreate)
		wantPath := filepath.Join(targetRoot, ".codex", "config.toml")
		if ch.DestPath != wantPath {
			t.Fatalf("codex mcp DestPath = %s, want %s", ch.DestPath, wantPath)
		}
		var doc struct {
			MCPServers map[string]struct {
				Command string `toml:"command"`
			} `toml:"mcp_servers"`
		}
		if err := toml.Unmarshal(ch.NewContent, &doc); err != nil {
			t.Fatalf("invalid TOML: %v\n%s", err, ch.NewContent)
		}
		if doc.MCPServers["github"].Command != "npx" {
			t.Errorf("codex github.command = %q, want npx", doc.MCPServers["github"].Command)
		}
	})
	t.Run("copilot lands at .copilot/mcp-config.json with mandatory tools", func(t *testing.T) {
		ch := get("copilot")
		requireAction(t, ch, ActionCreate)
		wantPath := filepath.Join(targetRoot, ".copilot", "mcp-config.json")
		if ch.DestPath != wantPath {
			t.Fatalf("copilot mcp DestPath = %s, want %s", ch.DestPath, wantPath)
		}
		doc := decodeJSONMap(t, ch.NewContent)
		github := mustObj(t, mustObj(t, doc["mcpServers"])["github"])
		if _, ok := github["tools"]; !ok {
			t.Errorf("copilot github entry missing mandatory tools field: %v", github)
		}
	})
	t.Run("opencode lands at .config/opencode/opencode.json with array command", func(t *testing.T) {
		ch := get("opencode")
		requireAction(t, ch, ActionCreate)
		wantPath := filepath.Join(targetRoot, ".config", "opencode", "opencode.json")
		if ch.DestPath != wantPath {
			t.Fatalf("opencode mcp DestPath = %s, want %s", ch.DestPath, wantPath)
		}
		doc := decodeJSONMap(t, ch.NewContent)
		github := mustObj(t, mustObj(t, doc["mcp"])["github"])
		if cmd, ok := github["command"].([]any); !ok || len(cmd) < 2 {
			t.Errorf("opencode github.command must be a combined [exe, args...] array: %v", github["command"])
		}
	})
	t.Run("gemini lands at .gemini/settings.json", func(t *testing.T) {
		ch := get("gemini")
		requireAction(t, ch, ActionCreate)
		wantPath := filepath.Join(targetRoot, ".gemini", "settings.json")
		if ch.DestPath != wantPath {
			t.Fatalf("gemini mcp DestPath = %s, want %s", ch.DestPath, wantPath)
		}
		doc := decodeJSONMap(t, ch.NewContent)
		github := mustObj(t, mustObj(t, doc["mcpServers"])["github"])
		if _, ok := github["command"]; !ok {
			t.Errorf("gemini stdio server missing command: %v", github)
		}
	})
	t.Run("oh-my-pi lands at .omp/agent/mcp.json", func(t *testing.T) {
		ch := get("oh-my-pi")
		requireAction(t, ch, ActionCreate)
		wantPath := filepath.Join(targetRoot, ".omp", "agent", "mcp.json")
		if ch.DestPath != wantPath {
			t.Fatalf("oh-my-pi mcp DestPath = %s, want %s", ch.DestPath, wantPath)
		}
		doc := decodeJSONMap(t, ch.NewContent)
		assertMCPEntryHasType(t, mustObj(t, doc["mcpServers"]), "github", "stdio")
	})
}

// TestPresetsPassthroughMissingFragmentAndMergeTOMLScoping is Phase 5's own
// verification, through the real "codex" preset rather than a hand-built
// adapter: a native/codex/**/* fragment nobody wrote is reported
// missing-source and never blocks the rest of the push, and a
// settings/codex.toml fragment containing only [scratch] touches only
// [scratch] in an existing config.toml, leaving an unrelated table (and an
// unrelated key inside the very table the mcp/permissions render rules
// also own) completely untouched. [tui] is deliberately not used here: the
// defaults.yaml render rule (Phase 11) owns it outright.
func TestPresetsPassthroughMissingFragmentAndMergeTOMLScoping(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{
		"core.md":             "You are the assistant.",
		"settings/codex.toml": "[scratch]\nvalue = \"new\"\n",
		// Deliberately no native/codex/**/* file at all.
	})
	// Pre-existing config.toml: an unrelated table, plus a stale [scratch]
	// value and friday's own [permissions.friday] table from an earlier
	// push — the passthrough rule must touch neither.
	writeTarget(t, targetAbs, "config.toml",
		"[other]\nkey = \"val\"\n\n[scratch]\nvalue = \"old\"\n\n[permissions.friday]\ndescription = \"pre-existing\"\n")

	p, ok := presets.Get("codex")
	if !ok {
		t.Fatal("codex preset missing from registry")
	}
	ad := p.Adapter()
	ad.Target = targetAbs
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if runtime.GOOS == "windows" {
		t.Setenv("LocalAppData", t.TempDir())
	}
	cfg := config.NewDefault(config.ScopeUser, storeAbs, filepath.Dir(targetAbs), map[string]*config.Adapter{"codex": ad})

	changes, err := Push(cfg, Options{Force: true})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}

	var nativeCh, settingsCh Change
	for _, ch := range changes {
		switch {
		case len(ch.Sources) == 1 && ch.Sources[0] == "native/codex/**/*":
			nativeCh = ch
		case len(ch.Sources) == 1 && ch.Sources[0] == "settings/codex.toml":
			settingsCh = ch
		}
	}

	if nativeCh.Action != ActionMissingSource {
		t.Fatalf("native/codex/**/* action = %s, want missing-source (no such file in the store): %+v", nativeCh.Action, nativeCh)
	}
	if _, statErr := os.Stat(filepath.Join(targetAbs, "themes")); statErr == nil {
		t.Error("a missing native fragment must never create anything under the target")
	}

	if settingsCh.Action != ActionUpdate {
		t.Fatalf("settings/codex.toml action = %s, want update: %+v", settingsCh.Action, settingsCh)
	}
	out := string(settingsCh.NewContent)
	if !strings.Contains(out, `key = "val"`) {
		t.Errorf("unrelated [other] table lost:\n%s", out)
	}
	if !strings.Contains(out, `description = "pre-existing"`) {
		t.Errorf("[permissions.friday] (owned by a different rule) must survive untouched:\n%s", out)
	}
	if strings.Contains(out, "old") {
		t.Errorf("stale [scratch] value must have been replaced, not merely appended:\n%s", out)
	}
	if !strings.Contains(out, `value = "new"`) && !strings.Contains(out, `value = 'new'`) {
		t.Errorf("[scratch] must be updated to the fragment's own value:\n%s", out)
	}

	// Confirm the file on disk matches the planned content — the guard
	// above only inspected the Change, not the actual write.
	written, err := os.ReadFile(filepath.Join(targetAbs, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != out {
		t.Errorf("written config.toml does not match the planned NewContent:\n%s", written)
	}
}

// TestPresetsRenderDefaultsAcrossFiveAdapters is Phase 11's own
// verification, mirroring TestPresetsRenderMCPAcrossSixAdapters: one
// defaults.yaml, pushed through the real built-in preset registry, lands
// correctly on every adapter with a defaults surface — including
// OpenCode's split across two destination files and Codex's bare top-level
// TOML scalars. oh-my-pi has no defaults.yaml rule at all (config.yml is
// YAML; friday's merge-json/merge-toml co-owned-merge strategies have no
// YAML analog yet — see ROADMAP.md), so it is not one of these five.
// Deliberately exercises effort "off" so every adapter's own
// clamp/unsupported behavior is visible in one push: claude/copilot clamp
// it to low (reported), codex passes it through unclamped, opencode/gemini
// report it unsupported and write nothing for it.
func TestPresetsRenderDefaultsAcrossFiveAdapters(t *testing.T) {
	root := t.TempDir()
	storeAbs := filepath.Join(root, "store")
	if err := os.MkdirAll(storeAbs, 0o755); err != nil {
		t.Fatal(err)
	}
	const defaultsYAML = `
version: 1
model:
  provider: anthropic
  name: claude-sonnet-4-5
  effort: off
theme: github
statusline:
  command: statusline.sh
  padding: 2
  refresh_interval: 5
telemetry: false
overrides:
  codex: { model_verbosity: low }
`
	if err := os.WriteFile(filepath.Join(storeAbs, "defaults.yaml"), []byte(defaultsYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	targetRoot := filepath.Join(root, "target")
	adapters := map[string]*config.Adapter{}
	for _, name := range []string{"claude", "codex", "copilot", "opencode", "gemini"} {
		p, ok := presets.Get(name)
		if !ok {
			t.Fatalf("preset %s missing from registry", name)
		}
		adapters[name] = p.Adapter()
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if runtime.GOOS == "windows" {
		t.Setenv("LocalAppData", t.TempDir())
	}
	cfg := config.NewDefault(config.ScopeUser, storeAbs, targetRoot, adapters)

	changes, err := Push(cfg, Options{Force: true})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	// Keyed by DestRel rather than Sources[0]: OpenCode's two defaults.yaml
	// rules (opencode.json and tui.json) share the same source, so Sources
	// alone cannot disambiguate them the way the MCP test's map does.
	byAdapterDest := map[string]Change{}
	for _, ch := range changes {
		if len(ch.Sources) == 1 && ch.Sources[0] == "defaults.yaml" {
			byAdapterDest[ch.Adapter+":"+ch.DestRel] = ch
		}
	}
	get := func(adapter, destRel string) Change {
		t.Helper()
		ch, ok := byAdapterDest[adapter+":"+destRel]
		if !ok {
			t.Fatalf("no defaults.yaml change for %s:%s (have %v)", adapter, destRel, changesKeys(byAdapterDest))
		}
		return ch
	}

	t.Run("claude clamps off to low and renders every field", func(t *testing.T) {
		ch := get("claude", "settings.json")
		requireAction(t, ch, ActionCreate)
		if !strings.Contains(ch.Warning, `clamped to "low"`) {
			t.Errorf("warning = %q, want an effort-clamp note", ch.Warning)
		}
		doc := decodeJSONMap(t, ch.NewContent)
		if doc["model"] != "claude-sonnet-4-5" || doc["effortLevel"] != "low" || doc["theme"] != "github" {
			t.Errorf("model/effortLevel/theme = %v/%v/%v", doc["model"], doc["effortLevel"], doc["theme"])
		}
		sl := mustObj(t, doc["statusLine"])
		if sl["command"] != "statusline.sh" {
			t.Errorf("statusLine = %v", sl)
		}
		env := mustObj(t, doc["env"])
		if env["DISABLE_TELEMETRY"] != "1" {
			t.Errorf("env = %v", env)
		}
	})
	t.Run("codex renders bare scalars, owned tables, and the override", func(t *testing.T) {
		ch := get("codex", "config.toml")
		requireAction(t, ch, ActionCreate)
		if !strings.Contains(ch.Warning, "statusline has no command-based equivalent") {
			t.Errorf("warning = %q, want a statusline-unsupported note", ch.Warning)
		}
		var doc struct {
			Model                string `toml:"model"`
			ModelProvider        string `toml:"model_provider"`
			ModelReasoningEffort string `toml:"model_reasoning_effort"`
			ModelVerbosity       string `toml:"model_verbosity"`
			Tui                  struct {
				Theme string `toml:"theme"`
			} `toml:"tui"`
			Analytics struct {
				Enabled bool `toml:"enabled"`
			} `toml:"analytics"`
		}
		if err := toml.Unmarshal(ch.NewContent, &doc); err != nil {
			t.Fatalf("invalid TOML: %v\n%s", err, ch.NewContent)
		}
		if doc.Model != "claude-sonnet-4-5" || doc.ModelProvider != "anthropic" {
			t.Errorf("model/model_provider = %q/%q", doc.Model, doc.ModelProvider)
		}
		// Canonical "off" is outside codex's own minimal..xhigh range, so it
		// clamps to the nearest legal value rather than writing a token codex
		// would reject.
		if doc.ModelReasoningEffort != "minimal" {
			t.Errorf("model_reasoning_effort = %q, want %q (canonical \"off\" clamped into codex's range)", doc.ModelReasoningEffort, "minimal")
		}
		if doc.ModelVerbosity != "low" {
			t.Errorf("model_verbosity (override) = %q, want %q", doc.ModelVerbosity, "low")
		}
		if doc.Tui.Theme != "github" {
			t.Errorf("tui.theme = %q", doc.Tui.Theme)
		}
		if doc.Analytics.Enabled {
			t.Error("analytics.enabled = true, want false")
		}
	})
	t.Run("copilot clamps off to low and reports telemetry unsupported", func(t *testing.T) {
		ch := get("copilot", "settings.json")
		requireAction(t, ch, ActionCreate)
		if !strings.Contains(ch.Warning, `clamped to "low"`) {
			t.Errorf("warning = %q, want an effort-clamp note", ch.Warning)
		}
		if !strings.Contains(ch.Warning, "not configurable at user scope") {
			t.Errorf("warning = %q, want a telemetry-unsupported note", ch.Warning)
		}
		doc := decodeJSONMap(t, ch.NewContent)
		if doc["effortLevel"] != "low" || doc["theme"] != "github" {
			t.Errorf("effortLevel/theme = %v/%v", doc["effortLevel"], doc["theme"])
		}
		if _, hasEnv := doc["env"]; hasEnv {
			t.Error("copilot must never receive claude's env.DISABLE_TELEMETRY shape")
		}
	})
	t.Run("opencode splits model+telemetry from theme across two files", func(t *testing.T) {
		settings := get("opencode", "opencode.json")
		requireAction(t, settings, ActionCreate)
		if !strings.Contains(settings.Warning, "catalog-defined model variant") {
			t.Errorf("opencode.json warning = %q, want an effort-unsupported note", settings.Warning)
		}
		if !strings.Contains(settings.Warning, "statusline has no equivalent") {
			t.Errorf("opencode.json warning = %q, want a statusline-unsupported note", settings.Warning)
		}
		sdoc := decodeJSONMap(t, settings.NewContent)
		if sdoc["model"] != "anthropic/claude-sonnet-4-5" {
			t.Errorf("model = %v, want provider/name", sdoc["model"])
		}
		if _, hasTheme := sdoc["theme"]; hasTheme {
			t.Error("opencode.json must never carry theme")
		}

		tui := get("opencode", "tui.json")
		requireAction(t, tui, ActionCreate)
		tdoc := decodeJSONMap(t, tui.NewContent)
		if tdoc["theme"] != "github" {
			t.Errorf("tui.json theme = %v", tdoc["theme"])
		}
		if _, hasModel := tdoc["model"]; hasModel {
			t.Error("tui.json must never carry model")
		}
	})
	t.Run("gemini nests model/theme/telemetry and reports effort+statusline unsupported", func(t *testing.T) {
		ch := get("gemini", "settings.json")
		requireAction(t, ch, ActionCreate)
		if !strings.Contains(ch.Warning, "thinkingConfig") {
			t.Errorf("warning = %q, want an effort-unsupported note", ch.Warning)
		}
		if !strings.Contains(ch.Warning, "ui.footer.items") {
			t.Errorf("warning = %q, want a statusline-unsupported note", ch.Warning)
		}
		doc := decodeJSONMap(t, ch.NewContent)
		if mustObj(t, doc["model"])["name"] != "claude-sonnet-4-5" {
			t.Errorf("model = %v, want nested {name: ...}", doc["model"])
		}
		if mustObj(t, doc["ui"])["theme"] != "github" {
			t.Errorf("ui = %v, want nested {theme: ...}", doc["ui"])
		}
		if mustObj(t, doc["privacy"])["usageStatisticsEnabled"] != false {
			t.Errorf("privacy = %v", doc["privacy"])
		}
	})
}
