package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/rules"
)

// conformanceMCPYAML exercises every transport spec.MCP defines (stdio,
// http, sse) plus a disabled and an adapter-scoped server, so a single push
// proves every adapter's renderer handles the full schema, not just its easy
// case.
const conformanceMCPYAML = `version: 1
servers:
  github:
    transport: stdio
    command: npx
    args: ["-y", "server-github"]
    env: {GITHUB_TOKEN: "${GITHUB_TOKEN}"}
  linear:
    transport: sse
    url: https://mcp.linear.app/sse
    headers: {Authorization: "Bearer ${LINEAR_KEY}"}
  docs:
    transport: http
    url: https://docs.example.com/mcp
  disabled:
    transport: stdio
    command: echo
    enabled: false
  codex-only:
    transport: stdio
    command: internal
    agents: [codex]
`

// conformancePermissionsYAML populates every one of the five fixed axes
// across all three actions, so every adapter's renderer must handle deny,
// ask, and allow simultaneously.
const conformancePermissionsYAML = `version: 1
read:
  deny: ["**/.env"]
write:
  allow: ["src/**"]
  ask: ["**/*.config.js"]
exec:
  allow: ["git status", "npm run build"]
  deny: ["git push --force"]
net:
  allow: ["docs.anthropic.com"]
  deny: ["*"]
mcp:
  allow: ["github/*"]
  ask: ["*"]
`

// selfConsistencyAdapter builds one adapter's config with an mcp render
// rule (unless mcpMerge is empty) and a permissions render rule (unless
// permMerge is empty, matching oh-my-pi's real preset: it has an mcp.json
// surface but no permissions rule at all — config.yml is YAML and friday
// has no merge-yaml strategy to reach it with), landing at the given
// native filenames.
func selfConsistencyAdapter(targetAbs, mcpTo, mcpMerge, permTo, permMerge string) *config.Adapter {
	ad := &config.Adapter{Target: targetAbs}
	if mcpMerge != "" {
		ad.Rules = append(ad.Rules, renderRule("mcp.yaml", mcpTo, rules.SurfaceMCP, mcpMerge))
	}
	if permMerge != "" {
		ad.Rules = append(ad.Rules, renderRule("permissions.yaml", permTo, rules.SurfacePermissions, permMerge))
	}
	return ad
}

// TestRenderSelfConsistencyAcrossAllAdapters pushes one canonical mcp.yaml
// and permissions.yaml to all six adapters in a single Push call and checks
// every adapter's native output — proving Phase 4's core claim (one store,
// six native surfaces) end to end through the public engine.Push API rather
// than planRender in isolation, plus the enablement/scoping filters,
// opencode's allow-before-deny byte order, and idempotency on re-push.
//
// It proves friday is self-consistent, NOT that any expected value conforms
// to a real agent's documented schema: every literal here was chosen to match
// what internal/render already emits, so a wrong-but-consistent discriminant
// (copilot's "local", codex's [mcp_servers.<name>]) passes. There is no
// vendored JSON Schema, no vendor-sourced golden file and no live-install
// check anywhere in the suite — several preset comments still say "verify
// against a live install". Real adapter-schema verification is tracked
// separately; do not read a green run here as it.
func TestRenderSelfConsistencyAcrossAllAdapters(t *testing.T) {
	storeAbs := t.TempDir()
	writeStoreFile(t, storeAbs, "mcp.yaml", conformanceMCPYAML)
	writeStoreFile(t, storeAbs, "permissions.yaml", conformancePermissionsYAML)

	targets := map[string]string{}
	adapters := map[string]*config.Adapter{}
	for _, name := range []string{"claude", "codex", "copilot", "opencode", "gemini", "oh-my-pi"} {
		targets[name] = t.TempDir()
	}
	adapters["claude"] = selfConsistencyAdapter(targets["claude"], "settings.json", rules.StrategyMergeJSON, "settings.json", rules.StrategyMergeJSON)
	adapters["codex"] = selfConsistencyAdapter(targets["codex"], "config.toml", rules.StrategyMergeTOML, "config.toml", rules.StrategyMergeTOML)
	adapters["copilot"] = selfConsistencyAdapter(targets["copilot"], "mcp-config.json", rules.StrategyMergeJSON, "config.json", rules.StrategyMergeJSON)
	adapters["opencode"] = selfConsistencyAdapter(targets["opencode"], "opencode.json", rules.StrategyMergeJSON, "opencode.json", rules.StrategyMergeJSON)
	adapters["gemini"] = selfConsistencyAdapter(targets["gemini"], "settings.json", rules.StrategyMergeJSON, "policies/friday.toml", rules.StrategyCopy)
	adapters["oh-my-pi"] = selfConsistencyAdapter(targets["oh-my-pi"], "mcp.json", rules.StrategyMergeJSON, "", "")

	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if runtime.GOOS == "windows" {
		t.Setenv("LocalAppData", t.TempDir())
	}
	cfg := config.NewDefault(config.ScopeUser, storeAbs, t.TempDir(), adapters)

	changes, err := Push(cfg, Options{Force: true})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	// Keyed by source, not DestRel: claude's mcp and permissions rules both
	// land in settings.json, so DestRel alone can't tell the two changes
	// apart.
	byAdapterSrc := map[string]Change{}
	for _, ch := range changes {
		byAdapterSrc[ch.Adapter+":"+ch.Sources[0]] = ch
	}
	get := func(t *testing.T, adapter, src string) Change {
		t.Helper()
		ch, ok := byAdapterSrc[adapter+":"+src]
		if !ok {
			t.Fatalf("no change for %s:%s (have %v)", adapter, src, changesKeys(byAdapterSrc))
		}
		return ch
	}

	// --- MCP: every enabled, in-scope server must land with the adapter's
	// own required discriminant fields, and the disabled/scoped-out servers
	// must never appear.
	t.Run("claude mcp", func(t *testing.T) {
		ch := get(t, "claude", "mcp.yaml")
		requireAction(t, ch, ActionCreate)
		doc := decodeJSONMap(t, ch.NewContent)
		servers := mustObj(t, doc["mcpServers"])
		assertMCPEntryHasType(t, servers, "github", "stdio")
		assertMCPEntryHasType(t, servers, "linear", "sse")
		assertMCPEntryHasType(t, servers, "docs", "http")
		assertServerAbsent(t, servers, "disabled")
		assertServerAbsent(t, servers, "codex-only")
	})
	t.Run("copilot mcp", func(t *testing.T) {
		ch := get(t, "copilot", "mcp.yaml")
		requireAction(t, ch, ActionCreate)
		doc := decodeJSONMap(t, ch.NewContent)
		servers := mustObj(t, doc["mcpServers"])
		for _, name := range []string{"github", "linear", "docs"} {
			e := mustObj(t, servers[name])
			if _, ok := e["tools"]; !ok {
				t.Errorf("copilot server %q missing mandatory tools field: %v", name, e)
			}
		}
		assertMCPEntryHasType(t, servers, "github", "local") // copilot's stdio spelling
	})
	t.Run("opencode mcp", func(t *testing.T) {
		ch := get(t, "opencode", "mcp.yaml")
		requireAction(t, ch, ActionCreate)
		doc := decodeJSONMap(t, ch.NewContent)
		servers := mustObj(t, doc["mcp"])
		github := mustObj(t, servers["github"])
		cmd, ok := github["command"].([]any)
		if !ok || len(cmd) < 2 {
			t.Errorf("opencode github.command must be a combined [exe, args...] array: %v", github["command"])
		}
	})
	t.Run("gemini mcp", func(t *testing.T) {
		ch := get(t, "gemini", "mcp.yaml")
		requireAction(t, ch, ActionCreate)
		doc := decodeJSONMap(t, ch.NewContent)
		servers := mustObj(t, doc["mcpServers"])
		docsEntry := mustObj(t, servers["docs"])
		if _, ok := docsEntry["httpUrl"]; !ok {
			t.Errorf("gemini http transport must use httpUrl: %v", docsEntry)
		}
		linearEntry := mustObj(t, servers["linear"])
		if _, ok := linearEntry["url"]; !ok {
			t.Errorf("gemini sse transport must use url: %v", linearEntry)
		}
	})
	t.Run("codex mcp", func(t *testing.T) {
		ch := get(t, "codex", "mcp.yaml")
		requireAction(t, ch, ActionCreate)
		var doc struct {
			MCPServers map[string]struct {
				Command string `toml:"command"`
				URL     string `toml:"url"`
			} `toml:"mcp_servers"`
		}
		if err := toml.Unmarshal(ch.NewContent, &doc); err != nil {
			t.Fatalf("invalid TOML: %v\n%s", err, ch.NewContent)
		}
		if doc.MCPServers["github"].Command != "npx" {
			t.Errorf("codex github.command = %q, want npx", doc.MCPServers["github"].Command)
		}
		if doc.MCPServers["linear"].URL == "" {
			t.Error("codex linear.url must be set")
		}
		if _, ok := doc.MCPServers["codex-only"]; !ok {
			t.Error("codex-only server (scoped to codex) must appear in codex's own render")
		}
	})
	t.Run("oh-my-pi mcp", func(t *testing.T) {
		ch := get(t, "oh-my-pi", "mcp.yaml")
		requireAction(t, ch, ActionCreate)
		doc := decodeJSONMap(t, ch.NewContent)
		servers := mustObj(t, doc["mcpServers"])
		assertMCPEntryHasType(t, servers, "github", "stdio")
		assertMCPEntryHasType(t, servers, "linear", "sse")
		assertMCPEntryHasType(t, servers, "docs", "http")
		assertServerDisabled(t, servers, "disabled")
		assertServerAbsent(t, servers, "codex-only")
	})

	// --- Permissions: every adapter's own axis-to-native mapping must
	// produce syntactically valid, non-empty output covering at least one
	// entry per populated axis it can represent.
	t.Run("claude permissions", func(t *testing.T) {
		ch := get(t, "claude", "permissions.yaml")
		requireAction(t, ch, ActionUpdate) // claude's mcp rule already created settings.json above
		doc := decodeJSONMap(t, ch.NewContent)
		perms := mustObj(t, doc["permissions"])
		assertContainsString(t, perms["deny"], "Read(**/.env)")
		assertContainsString(t, perms["allow"], "Bash(git status)")
		assertContainsString(t, perms["ask"], "Edit(**/*.config.js)")
	})
	t.Run("opencode permissions via mergeReplace", func(t *testing.T) {
		ch := get(t, "opencode", "permissions.yaml")
		requireAction(t, ch, ActionUpdate) // opencode's mcp rule already created opencode.json above
		raw := string(ch.NewContent)
		if !strings.Contains(raw, `"permission"`) {
			t.Fatalf("missing permission object:\n%s", raw)
		}
		// allow-tier bash pattern must be written before deny-tier for
		// opencode's last-match-wins evaluation.
		allowPos := strings.Index(raw, `"git status"`)
		denyPos := strings.Index(raw, `"git push --force"`)
		if allowPos < 0 || denyPos < 0 || allowPos > denyPos {
			t.Errorf("bash tier order wrong (allow must precede deny):\n%s", raw)
		}
	})
	t.Run("codex permissions", func(t *testing.T) {
		ch := get(t, "codex", "permissions.yaml")
		requireAction(t, ch, ActionUpdate) // codex's mcp rule already created config.toml above
		var doc struct {
			Permissions struct {
				Friday struct {
					Filesystem map[string]any `toml:"filesystem"`
					Network    map[string]any `toml:"network"`
				} `toml:"friday"`
			} `toml:"permissions"`
		}
		if err := toml.Unmarshal(ch.NewContent, &doc); err != nil {
			t.Fatalf("invalid TOML: %v\n%s", err, ch.NewContent)
		}
		if len(doc.Permissions.Friday.Filesystem) == 0 {
			t.Errorf("codex filesystem workspace_roots must be populated:\n%s", ch.NewContent)
		}
		if len(doc.Permissions.Friday.Network) == 0 {
			t.Errorf("codex network domains must be populated:\n%s", ch.NewContent)
		}
	})
	t.Run("gemini permissions via copy", func(t *testing.T) {
		ch := get(t, "gemini", "permissions.yaml")
		requireAction(t, ch, ActionCreate)
		var doc struct {
			Rule []struct {
				Decision string `toml:"decision"`
				Priority int    `toml:"priority"`
			} `toml:"rule"`
		}
		if err := toml.Unmarshal(ch.NewContent, &doc); err != nil {
			t.Fatalf("invalid TOML: %v\n%s", err, ch.NewContent)
		}
		if len(doc.Rule) == 0 {
			t.Fatal("expected at least one policy rule")
		}
		for _, r := range doc.Rule {
			if r.Decision != "deny" && r.Decision != "ask_user" && r.Decision != "allow" {
				t.Errorf("invalid decision %q", r.Decision)
			}
			if r.Priority != 900 && r.Priority != 500 && r.Priority != 100 {
				t.Errorf("invalid priority %d", r.Priority)
			}
		}
	})

	// --- Idempotency: the first Push already wrote every file for real
	// (Force: true); re-pushing the identical store must report in-sync
	// everywhere — a render that can't recognize its own output is unusable
	// for real `friday push` runs.
	second, err := Push(cfg, Options{Force: true})
	if err != nil {
		t.Fatalf("second Push: %v", err)
	}
	for _, ch := range second {
		if ch.Action != ActionInSync {
			t.Errorf("%s:%s (from %s) action = %s on re-push, want in-sync", ch.Adapter, ch.DestRel, ch.Sources[0], ch.Action)
		}
	}
}

func writeStoreFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func changesKeys(m map[string]Change) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func requireAction(t *testing.T, ch Change, want Action) {
	t.Helper()
	if ch.Action != want {
		t.Fatalf("%s:%s action = %s, want %s (reason: %s)", ch.Adapter, ch.DestRel, ch.Action, want, ch.Reason)
	}
}

func decodeJSONMap(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, data)
	}
	return m
}

func mustObj(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("expected object, got %T: %v", v, v)
	}
	return m
}

func assertMCPEntryHasType(t *testing.T, servers map[string]any, name, wantType string) {
	t.Helper()
	e := mustObj(t, servers[name])
	if e["type"] != wantType {
		t.Errorf("%s.type = %v, want %q", name, e["type"], wantType)
	}
}

func assertServerAbsent(t *testing.T, servers map[string]any, name string) {
	t.Helper()
	if _, ok := servers[name]; ok {
		t.Errorf("server %q must not be rendered", name)
	}
}

// assertServerDisabled is the correct expectation for an adapter that has a
// native per-server off switch: the entry must still be RENDERED, carrying
// enabled=false. Omitting it instead would leave a previously-pushed server
// live in the agent's config, because a merge cannot retract a key that is
// simply absent from the new payload.
func assertServerDisabled(t *testing.T, servers map[string]any, name string) {
	t.Helper()
	e, ok := servers[name].(map[string]any)
	if !ok {
		t.Fatalf("server %q must be rendered with enabled=false, got %T", name, servers[name])
	}
	if e["enabled"] != false {
		t.Errorf("server %q enabled = %v, want false", name, e["enabled"])
	}
}

func assertContainsString(t *testing.T, v any, want string) {
	t.Helper()
	arr, ok := v.([]any)
	if !ok {
		t.Fatalf("expected array, got %T: %v", v, v)
	}
	for _, x := range arr {
		if x == want {
			return
		}
	}
	t.Errorf("array %v does not contain %q", arr, want)
}
