package engine

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/drift"
	"github.com/zhivko-kocev/friday/internal/rules"
	"github.com/zhivko-kocev/friday/internal/spec"
)

// renderAdapter wraps a single render rule the way every other plan test
// wraps its own strategy — one rule, one target dir.
func renderAdapter(targetAbs string, r *rules.Rule) *config.Adapter {
	return &config.Adapter{Target: targetAbs, Rules: []*rules.Rule{r}}
}

func renderRule(from, to, surface, merge string) *rules.Rule {
	return &rules.Rule{From: rules.FromSpec{from}, To: to, Strategy: rules.StrategyRender, Surface: surface, Merge: merge}
}

const testMCPYAML = `version: 1
servers:
  github:
    transport: stdio
    command: npx
    args: ["-y", "server-github"]
    env: {GITHUB_TOKEN: "secret-token-value"}
  disabled:
    transport: stdio
    command: echo
    enabled: false
`

const testPermissionsYAML = `version: 1
read:
  deny: ["**/.env"]
write:
  allow: ["src/**"]
exec:
  allow: ["git status"]
  deny: ["git push --force"]
net:
  deny: ["*"]
`

// --- MCP surface ------------------------------------------------------------

func TestPlanRenderMCPMergeJSONClaude(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{"mcp.yaml": testMCPYAML})
	ad := renderAdapter(targetAbs, renderRule("mcp.yaml", "settings.json", rules.SurfaceMCP, rules.StrategyMergeJSON))
	changes, err := planPush(nil, "claude", ad, singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1: %+v", len(changes), changes)
	}
	ch := changes[0]
	if ch.Action != ActionCreate {
		t.Fatalf("action = %s, want create: %+v", ch.Action, ch)
	}
	if !ch.driftExempt {
		t.Error("mcp merge-json change must be drift-exempt (co-owned target)")
	}
	// mergeSource is the pre-merge rendered payload the confirmer and
	// owned-state see; it need not be byte-identical to the post-merge
	// NewContent (mergeEntries re-serializes independently), only carry the
	// same rendered server.
	if !strings.Contains(string(ch.mergeSource), "github") {
		t.Errorf("mergeSource must be the rendered payload, got %s", ch.mergeSource)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(ch.NewContent, &doc); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, ch.NewContent)
	}
	var servers map[string]json.RawMessage
	if err := json.Unmarshal(doc["mcpServers"], &servers); err != nil {
		t.Fatalf("no valid mcpServers object: %v\n%s", err, ch.NewContent)
	}
	if _, ok := servers["github"]; !ok {
		t.Errorf("missing enabled server 'github': %s", ch.NewContent)
	}
	if _, ok := servers["disabled"]; ok {
		t.Errorf("explicitly-disabled server must not be rendered: %s", ch.NewContent)
	}
}

func TestPlanRenderMCPMergeTOMLCodex(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{"mcp.yaml": testMCPYAML})
	ad := renderAdapter(targetAbs, renderRule("mcp.yaml", "config.toml", rules.SurfaceMCP, rules.StrategyMergeTOML))
	changes, err := planPush(nil, "codex", ad, singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	ch := changes[0]
	if ch.Action != ActionCreate {
		t.Fatalf("action = %s, want create: %+v", ch.Action, ch)
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
		t.Errorf("mcp_servers.github.command = %q, want npx:\n%s", doc.MCPServers["github"].Command, ch.NewContent)
	}
}

// TestPlanRenderMCPUnknownAdapterReportsUnsupported covers a rule whose
// adapter name render.MCP does not recognize (a typo in a hand-authored
// friday.yaml, or any name outside the six built-in adapters): render.MCP
// returns an error rather than silently producing garbage, and
// planRenderCanonical must surface that as an unsupported change — not
// panic, not write anything, not misreport it as missing-source (which
// means "the rule is fine, the source file just isn't there").
func TestPlanRenderMCPUnknownAdapterReportsUnsupported(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{"mcp.yaml": testMCPYAML})
	ad := renderAdapter(targetAbs, renderRule("mcp.yaml", "settings.json", rules.SurfaceMCP, rules.StrategyMergeJSON))
	changes, err := planPush(nil, "nonexistent", ad, singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	ch := changes[0]
	if ch.Action != ActionUnsupported {
		t.Fatalf("action = %s, want unsupported: %+v", ch.Action, ch)
	}
	if !strings.Contains(ch.Reason, "unknown adapter") {
		t.Errorf("reason must explain the unknown adapter: %q", ch.Reason)
	}
	if ch.NewContent != nil {
		t.Errorf("nothing should be staged to write: %+v", ch)
	}
}

func TestPlanRenderMCPMissingSourceFile(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{})
	ad := renderAdapter(targetAbs, renderRule("mcp.yaml", "settings.json", rules.SurfaceMCP, rules.StrategyMergeJSON))
	changes, err := planPush(nil, "claude", ad, singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if changes[0].Action != ActionMissingSource {
		t.Fatalf("action = %s, want missing-source: %+v", changes[0].Action, changes[0])
	}
}

// TestPlanRenderMCPShadowedSourceWarns proves the literal single-source
// resolution reports a lower-precedence root's own copy instead of silently
// discarding it — the only place a render rule's shadowed source is visible,
// since resolution otherwise picks one winner the same way merge-json does.
func TestPlanRenderMCPShadowedSourceWarns(t *testing.T) {
	roots, targetAbs := scaffold2(t,
		map[string]string{"mcp.yaml": "version: 1\nservers:\n  team-only:\n    transport: stdio\n    command: echo\n"},
		map[string]string{"mcp.yaml": testMCPYAML},
	)
	ad := renderAdapter(targetAbs, renderRule("mcp.yaml", "settings.json", rules.SurfaceMCP, rules.StrategyMergeJSON))
	changes, err := planPush(nil, "claude", ad, roots, targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	ch := changes[0]
	if ch.SrcAbs != filepath.Join(roots[1].Dir, "mcp.yaml") {
		t.Errorf("SrcAbs = %q, want local's (highest-precedence) mcp.yaml", ch.SrcAbs)
	}
	if !strings.Contains(ch.Warning, "team") {
		t.Errorf("warning must name the shadowed source: %q", ch.Warning)
	}
	var doc map[string]json.RawMessage
	_ = json.Unmarshal(ch.NewContent, &doc)
	var servers map[string]json.RawMessage
	_ = json.Unmarshal(doc["mcpServers"], &servers)
	if _, ok := servers["team-only"]; ok {
		t.Errorf("team's shadowed server must not leak into the winning root's render: %s", ch.NewContent)
	}
}

// TestPlanRenderMCPUsesOwnedPrevForRetraction proves planRenderCanonical
// threads owned.Get into mergeEntries' prev exactly like planMergeJSON does,
// so removing a server from mcp.yaml retracts its stale settings.json entry
// instead of leaving it behind forever.
func TestPlanRenderMCPUsesOwnedPrevForRetraction(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{
		"mcp.yaml": "version: 1\nservers:\n  github:\n    transport: stdio\n    command: npx\n",
	})
	ad := renderAdapter(targetAbs, renderRule("mcp.yaml", "settings.json", rules.SurfaceMCP, rules.StrategyMergeJSON))
	dest := filepath.Join(targetAbs, "settings.json")

	owned, err := drift.LoadOwned(filepath.Join(t.TempDir(), "hooks-owned.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Simulate an earlier push that rendered both github and a since-removed
	// "linear" server, and the target still carries both entries. The entries
	// carry their real rendered shape: an empty object is the one shape whose
	// retraction succeeds even when scalar leaves are never retracted, so an
	// `{}` fixture would pass against a half-deleting merge.
	prevPayload := []byte(`{"mcpServers":{"github":{"type":"stdio","command":"npx","args":["-y","@modelcontextprotocol/server-github"]},"linear":{"type":"http","url":"https://mcp.linear.app/sse"}}}`)
	owned.Set("claude", "mcp.yaml", dest, prevPayload)
	writeTarget(t, targetAbs, "settings.json", `{"mcpServers":{"github":{"type":"stdio","command":"npx","args":["-y","@modelcontextprotocol/server-github"]},"linear":{"type":"http","url":"https://mcp.linear.app/sse"}},"model":"opus"}`)

	changes, err := planPush(owned, "claude", ad, singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	ch := changes[0]
	if ch.Action != ActionUpdate {
		t.Fatalf("action = %s, want update: %+v", ch.Action, ch)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(ch.NewContent, &doc); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, ch.NewContent)
	}
	var servers map[string]json.RawMessage
	_ = json.Unmarshal(doc["mcpServers"], &servers)
	if raw, ok := servers["linear"]; ok {
		t.Errorf("linear was removed from mcp.yaml and must be retracted whole, got %s: %s", raw, ch.NewContent)
	}
	if _, ok := servers["github"]; !ok {
		t.Errorf("github must survive: %s", ch.NewContent)
	}
	if _, ok := doc["model"]; !ok {
		t.Errorf("user's own model key must survive untouched: %s", ch.NewContent)
	}
}

// TestPlanRenderSharedTargetComposesAcrossRules is the regression case for a
// bug where two merge-strategy rules landing in the same target file (mcp
// and permissions both merging into one adapter's settings.json) each
// planned against the same pre-push snapshot: apply()'s later, sequential
// write of the second rule's independently-computed NewContent silently
// clobbered the first rule's contribution. planPush's pending map must make
// the second rule see the first rule's own NewContent as its baseline.
func TestPlanRenderSharedTargetComposesAcrossRules(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{
		"mcp.yaml":         testMCPYAML,
		"permissions.yaml": testPermissionsYAML,
	})
	ad := &config.Adapter{
		Target: targetAbs,
		Rules: []*rules.Rule{
			renderRule("mcp.yaml", "settings.json", rules.SurfaceMCP, rules.StrategyMergeJSON),
			renderRule("permissions.yaml", "settings.json", rules.SurfacePermissions, rules.StrategyMergeJSON),
		},
	}
	changes, err := planPush(nil, "claude", ad, singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 {
		t.Fatalf("got %d changes, want 2: %+v", len(changes), changes)
	}
	// The second rule (permissions) must see the first rule's (mcp) own
	// NewContent as its baseline, not the empty pre-push file — proving its
	// own NewContent carries both keys.
	perms := changes[1]
	if perms.Action != ActionUpdate {
		t.Fatalf("permissions rule action = %s, want update (mcp rule already staged settings.json): %+v", perms.Action, perms)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(perms.NewContent, &doc); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, perms.NewContent)
	}
	if _, ok := doc["mcpServers"]; !ok {
		t.Errorf("permissions rule's own NewContent lost the mcp rule's contribution (planned against a stale snapshot):\n%s", perms.NewContent)
	}
	if _, ok := doc["permissions"]; !ok {
		t.Errorf("permissions rule's own NewContent is missing its own contribution:\n%s", perms.NewContent)
	}
}

// --- Permissions surface -----------------------------------------------------

func TestPlanRenderPermissionsMergeJSONClaude(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{"permissions.yaml": testPermissionsYAML})
	ad := renderAdapter(targetAbs, renderRule("permissions.yaml", "settings.json", rules.SurfacePermissions, rules.StrategyMergeJSON))
	changes, err := planPush(nil, "claude", ad, singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	ch := changes[0]
	if ch.Action != ActionCreate {
		t.Fatalf("action = %s, want create: %+v", ch.Action, ch)
	}
	if !strings.Contains(string(ch.NewContent), `Bash(git status)`) {
		t.Errorf("missing rendered Bash allow rule:\n%s", ch.NewContent)
	}
	if !strings.Contains(string(ch.NewContent), `Read(**/.env)`) {
		t.Errorf("missing rendered Read deny rule:\n%s", ch.NewContent)
	}
}

// TestPlanRenderPermissionsOpenCodeUsesMergeReplace is the security-critical
// case: opencode's permission.* objects are evaluated last-key-wins, so they
// must reach the file through the atomic, order-preserving mergeReplace path
// rather than the ordinary entry-union mergeEntries — which would decode
// through a Go map and re-serialize with alphabetically sorted keys,
// silently inverting the deny/ask/allow precedence friday just rendered.
func TestPlanRenderPermissionsOpenCodeUsesMergeReplace(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{"permissions.yaml": testPermissionsYAML})
	ad := renderAdapter(targetAbs, renderRule("permissions.yaml", "opencode.json", rules.SurfacePermissions, rules.StrategyMergeJSON))
	// A pre-existing target with an unrelated, alphabetically-earlier key
	// under permission.bash: mergeEntries would sort "allow-all" before
	// "git status", mergeReplace must not (it replaces the whole bash object
	// atomically instead of unioning keys).
	writeTarget(t, targetAbs, "opencode.json", `{"permission":{"bash":{"allow-all":"ask"}}}`)
	changes, err := planPush(nil, "opencode", ad, singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	ch := changes[0]
	raw := string(ch.NewContent)
	// mergeReplace preserves the payload's own hand-built byte sequence for
	// the bash object; a union would keep "allow-all" alongside it.
	if strings.Contains(raw, "allow-all") {
		t.Errorf("mergeReplace must atomically replace permission.bash, not union with the stale target key:\n%s", raw)
	}
	giStatus := strings.Index(raw, `"git status"`)
	pushForce := strings.Index(raw, `"git push --force"`)
	if giStatus < 0 || pushForce < 0 {
		t.Fatalf("missing expected patterns:\n%s", raw)
	}
	if giStatus > pushForce {
		t.Errorf("allow-tier pattern must be written before deny-tier (last-match-wins precedence):\n%s", raw)
	}
}

func TestPlanRenderPermissionsGeminiCopy(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{"permissions.yaml": testPermissionsYAML})
	ad := renderAdapter(targetAbs, renderRule("permissions.yaml", "policies/friday.toml", rules.SurfacePermissions, rules.StrategyCopy))
	changes, err := planPush(nil, "gemini", ad, singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	ch := changes[0]
	if ch.Action != ActionCreate {
		t.Fatalf("action = %s, want create: %+v", ch.Action, ch)
	}
	if ch.driftExempt {
		t.Error("copy-strategy render must not be drift-exempt — it is a whole-file friday-owned write, not a co-owned merge")
	}
	var doc struct {
		Rule []struct {
			ToolName string `toml:"toolName"`
			Decision string `toml:"decision"`
		} `toml:"rule"`
	}
	if err := toml.Unmarshal(ch.NewContent, &doc); err != nil {
		t.Fatalf("invalid TOML: %v\n%s", err, ch.NewContent)
	}
	if len(doc.Rule) == 0 {
		t.Fatalf("expected at least one rendered policy rule:\n%s", ch.NewContent)
	}

	// Pushing the identical content again must be in-sync, never a spurious
	// update from the copy path's own equality check.
	writeTarget(t, targetAbs, "policies/friday.toml", string(ch.NewContent))
	changes, err = planPush(nil, "gemini", ad, singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if changes[0].Action != ActionInSync {
		t.Errorf("action = %s, want in-sync on second push", changes[0].Action)
	}
}

func TestPlanRenderPermissionsMergeTOMLCodex(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{"permissions.yaml": testPermissionsYAML})
	ad := renderAdapter(targetAbs, renderRule("permissions.yaml", "config.toml", rules.SurfacePermissions, rules.StrategyMergeTOML))
	changes, err := planPush(nil, "codex", ad, singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	ch := changes[0]
	if ch.Action != ActionCreate {
		t.Fatalf("action = %s, want create: %+v", ch.Action, ch)
	}
	if !strings.Contains(ch.Warning, `default_permissions`) {
		t.Errorf("must warn that the profile needs manual activation: %q", ch.Warning)
	}
	var doc struct {
		Permissions struct {
			Friday struct {
				Description string `toml:"description"`
			} `toml:"friday"`
		} `toml:"permissions"`
	}
	if err := toml.Unmarshal(ch.NewContent, &doc); err != nil {
		t.Fatalf("invalid TOML: %v\n%s", err, ch.NewContent)
	}
	if doc.Permissions.Friday.Description == "" {
		t.Errorf("missing [permissions.friday] table:\n%s", ch.NewContent)
	}
}

// --- codex-agent / gemini-command surfaces ----------------------------------

func TestPlanRenderCodexAgentSurface(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{
		"agents/architect.md": "---\nname: architect\ndescription: plans work\n---\nYou are the architect.\n",
	})
	ad := renderAdapter(targetAbs, &rules.Rule{
		From: rules.FromSpec{"agents/*.md"}, To: "agents/{stem}.toml",
		Strategy: rules.StrategyRender, Surface: rules.SurfaceCodexAgent, Merge: rules.StrategyCopy,
	})
	changes, err := planPush(nil, "codex", ad, singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].DestRel != "agents/architect.toml" {
		t.Fatalf("got %+v, want one change at agents/architect.toml", changes)
	}
	var doc struct {
		Name                  string `toml:"name"`
		DeveloperInstructions string `toml:"developer_instructions"`
	}
	if err := toml.Unmarshal(changes[0].NewContent, &doc); err != nil {
		t.Fatalf("invalid TOML: %v\n%s", err, changes[0].NewContent)
	}
	if doc.Name != "architect" || doc.DeveloperInstructions != "You are the architect." {
		t.Errorf("unexpected render: %+v\n%s", doc, changes[0].NewContent)
	}
}

func TestPlanRenderGeminiCommandSurface(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{
		"commands/changelog.md": "---\ndescription: Add a changelog entry\n---\nSummarize $ARGUMENTS as a changelog line.\n",
	})
	ad := renderAdapter(targetAbs, &rules.Rule{
		From: rules.FromSpec{"commands/*.md"}, To: "commands/{stem}.toml",
		Strategy: rules.StrategyRender, Surface: rules.SurfaceGeminiCommand, Merge: rules.StrategyCopy,
	})
	changes, err := planPush(nil, "gemini", ad, singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].DestRel != "commands/changelog.toml" {
		t.Fatalf("got %+v, want one change at commands/changelog.toml", changes)
	}
	var doc struct {
		Description string `toml:"description"`
		Prompt      string `toml:"prompt"`
	}
	if err := toml.Unmarshal(changes[0].NewContent, &doc); err != nil {
		t.Fatalf("invalid TOML: %v\n%s", err, changes[0].NewContent)
	}
	if doc.Description != "Add a changelog entry" {
		t.Errorf("description = %q, want %q", doc.Description, "Add a changelog entry")
	}
	if doc.Prompt != "Summarize $ARGUMENTS as a changelog line." {
		t.Errorf("prompt = %q", doc.Prompt)
	}
}

// --- pull guard --------------------------------------------------------------

func TestPlanPullCodexAgentSurfaceUnsupported(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{})
	writeTarget(t, targetAbs, "agents", "not-used") // codex-agent never reads a target file to pull
	ad := &config.Adapter{
		Target: targetAbs,
		Rules: []*rules.Rule{
			{From: rules.FromSpec{"agents/*.md"}, To: "agents/{stem}.toml", Strategy: rules.StrategyRender, Surface: rules.SurfaceCodexAgent, Merge: rules.StrategyCopy},
		},
	}
	changes, err := planPull(nil, "claude", ad, singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Action != ActionUnsupported {
		t.Fatalf("got %+v, want one unsupported change", changes)
	}
	if !strings.Contains(changes[0].Reason, "irreversible") {
		t.Errorf("codex-agent pull reason should call out the lossy transform: %q", changes[0].Reason)
	}
}

func TestPlanPullPermissionsCopyMergeUnsupported(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{})
	writeTarget(t, targetAbs, "policies/friday.toml", "[[rule]]\ntoolName = \"read_file\"\n")
	ad := renderAdapter(targetAbs, renderRule("permissions.yaml", "policies/friday.toml", rules.SurfacePermissions, rules.StrategyCopy))
	changes, err := planPull(nil, "gemini", ad, singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Action != ActionUnsupported {
		t.Fatalf("got %+v, want one unsupported change (gemini's policy file is friday-owned outright)", changes)
	}
	if !strings.Contains(changes[0].Reason, "friday-owned") {
		t.Errorf("reason should name the actual blocker: %q", changes[0].Reason)
	}
}

// --- MCP pull inverse --------------------------------------------------------

func mcpPullAdapter(targetAbs string) *config.Adapter {
	return renderAdapter(targetAbs, renderRule("mcp.yaml", "settings.json", rules.SurfaceMCP, rules.StrategyMergeJSON))
}

func TestPullRenderMCPCreatesNewMCPYAML(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{})
	writeTarget(t, targetAbs, "settings.json", `{"mcpServers":{"github":{"type":"stdio","command":"npx","args":["-y","server-github"]}}}`)
	changes, err := planPull(nil, "claude", mcpPullAdapter(targetAbs), singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1: %+v", len(changes), changes)
	}
	ch := changes[0]
	if ch.Action != ActionCreate {
		t.Fatalf("action = %s, want create: %+v", ch.Action, ch)
	}
	if ch.DestPath != filepath.Join(storeAbs, "mcp.yaml") {
		t.Errorf("DestPath = %q, want mcp.yaml in the store", ch.DestPath)
	}
	got, err := spec.ParseMCP(ch.NewContent)
	if err != nil {
		t.Fatalf("invalid mcp.yaml: %v\n%s", err, ch.NewContent)
	}
	github := got.Servers["github"]
	if github.Transport != spec.TransportStdio || github.Command != "npx" || len(github.Args) != 2 {
		t.Errorf("github = %+v", github)
	}
}

func TestPullRenderMCPMissingTargetSkipped(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{"mcp.yaml": testMCPYAML})
	changes, err := planPull(nil, "claude", mcpPullAdapter(targetAbs), singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Fatalf("got %+v, want no changes (no settings.json to pull from)", changes)
	}
}

// TestPullRenderMCPPreservesEnabledAgentsAndTools pulls a server that IS in
// scope for claude (enabled, no agents restriction): the native form's newly
// changed command is captured, while Tools — which claude's native form does
// not carry at all — survives from the store.
func TestPullRenderMCPPreservesEnabledAgentsAndTools(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{
		"mcp.yaml": "version: 1\nservers:\n  github:\n    transport: stdio\n    command: oldcmd\n    tools: [read_file]\n",
	})
	// The native form now shows an updated command — as if the user (or an
	// earlier friday push under a new mcp.yaml) changed it — but tools is
	// store-only intent claude's native form cannot carry.
	writeTarget(t, targetAbs, "settings.json", `{"mcpServers":{"github":{"type":"stdio","command":"newcmd"}}}`)
	changes, err := planPull(nil, "claude", mcpPullAdapter(targetAbs), singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	ch := changes[0]
	if ch.Action != ActionUpdate {
		t.Fatalf("action = %s, want update: %+v", ch.Action, ch)
	}
	got, err := spec.ParseMCP(ch.NewContent)
	if err != nil {
		t.Fatalf("invalid mcp.yaml: %v\n%s", err, ch.NewContent)
	}
	github := got.Servers["github"]
	if github.Command != "newcmd" {
		t.Errorf("command = %q, want the newly captured newcmd", github.Command)
	}
	if len(github.Tools) != 1 || github.Tools[0] != "read_file" {
		t.Errorf("tools = %v, want [read_file] preserved — claude's native form has no tools field", github.Tools)
	}
}

// TestPullRenderMCPSkipsServerScopedToAnotherAdapter is the canonical-
// corruption regression: mcp.yaml scopes "gh" to codex, so friday never
// rendered it into claude's settings.json. Whatever claude's file holds under
// that name is the user's own entry — adopting it would rewrite the canonical
// definition from a file friday never wrote, and the next `friday push codex`
// would then ship claude's command to codex.
func TestPullRenderMCPSkipsServerScopedToAnotherAdapter(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{
		"mcp.yaml": "version: 1\nservers:\n  gh:\n    transport: stdio\n    command: canonical-cmd\n    agents: [codex]\n",
	})
	writeTarget(t, targetAbs, "settings.json", `{"mcpServers":{"gh":{"type":"stdio","command":"claudes-own-cmd"}}}`)
	changes, err := planPull(nil, "claude", mcpPullAdapter(targetAbs), singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	ch := changes[0]
	if ch.Action != ActionInSync {
		t.Fatalf("action = %s, want in-sync — nothing in scope for claude changed: %+v", ch.Action, ch)
	}
	got, err := spec.ParseMCP(ch.NewContent)
	if err != nil {
		t.Fatalf("invalid mcp.yaml: %v\n%s", err, ch.NewContent)
	}
	if cmd := got.Servers["gh"].Command; cmd != "canonical-cmd" {
		t.Errorf("command = %q, want canonical-cmd — a server scoped to codex must not be recaptured from claude", cmd)
	}
	if !strings.Contains(ch.Warning, "gh") {
		t.Errorf("warning should name the skipped server: %q", ch.Warning)
	}
}

// TestPullRenderMCPSkipsDisabledServer is the same guard for enabled:false.
// push never writes such a server, so an entry present in a target is stale
// or foreign either way.
func TestPullRenderMCPSkipsDisabledServer(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{
		"mcp.yaml": "version: 1\nservers:\n  gh:\n    transport: stdio\n    command: canonical-cmd\n    enabled: false\n",
	})
	writeTarget(t, targetAbs, "settings.json", `{"mcpServers":{"gh":{"type":"stdio","command":"stale-cmd"}}}`)
	changes, err := planPull(nil, "claude", mcpPullAdapter(targetAbs), singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	got, err := spec.ParseMCP(changes[0].NewContent)
	if err != nil {
		t.Fatalf("invalid mcp.yaml: %v\n%s", err, changes[0].NewContent)
	}
	gh := got.Servers["gh"]
	if gh.Command != "canonical-cmd" {
		t.Errorf("command = %q, want canonical-cmd — a disabled server must not be recaptured", gh.Command)
	}
	if gh.IsEnabled() {
		t.Error("enabled:false must survive the pull — no native form carries it")
	}
}

func TestPullRenderMCPNeverDeletesAbsentServer(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{
		"mcp.yaml": "version: 1\nservers:\n  disabled-elsewhere:\n    transport: stdio\n    command: echo\n    enabled: false\n",
	})
	writeTarget(t, targetAbs, "settings.json", `{"mcpServers":{"github":{"type":"stdio","command":"npx"}}}`)
	changes, err := planPull(nil, "claude", mcpPullAdapter(targetAbs), singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	got, err := spec.ParseMCP(changes[0].NewContent)
	if err != nil {
		t.Fatalf("invalid mcp.yaml: %v\n%s", err, changes[0].NewContent)
	}
	if _, ok := got.Servers["disabled-elsewhere"]; !ok {
		t.Error("a server absent from this adapter's native form (disabled) must not be deleted from mcp.yaml")
	}
	if _, ok := got.Servers["github"]; !ok {
		t.Error("the newly captured server must be added")
	}
}

// TestPullRenderMCPAmbiguousTransportPrefersExisting covers opencode/codex's
// one-way http/sse collapse: recovering "remote"/shared-url-shape always
// guesses http, but an existing sse entry whose URL is unchanged must keep
// sse rather than flip transport on every pull.
func TestPullRenderMCPAmbiguousTransportPrefersExisting(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{
		"mcp.yaml": "version: 1\nservers:\n  linear:\n    transport: sse\n    url: https://mcp.linear.app/sse\n",
	})
	writeTarget(t, targetAbs, "opencode.json", `{"mcp":{"linear":{"type":"remote","url":"https://mcp.linear.app/sse"}}}`)
	ad := renderAdapter(targetAbs, renderRule("mcp.yaml", "opencode.json", rules.SurfaceMCP, rules.StrategyMergeJSON))
	changes, err := planPull(nil, "opencode", ad, singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if changes[0].Action != ActionInSync {
		t.Fatalf("action = %s, want in-sync (unchanged URL must keep the existing sse transport, not flip to http)", changes[0].Action)
	}
}

func TestPullRenderMCPInSyncWhenUnchanged(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{"mcp.yaml": testMCPYAML})
	ad := mcpPullAdapter(targetAbs)
	pushed, err := planPush(nil, "claude", ad, singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	writeTarget(t, targetAbs, "settings.json", string(pushed[0].NewContent))
	changes, err := planPull(nil, "claude", ad, singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if changes[0].Action != ActionInSync {
		t.Errorf("action = %s, want in-sync after a clean push/pull round trip: %+v", changes[0].Action, changes[0])
	}
}

// TestPullRenderMCPRefusesWriteBackIntoSource is the read-only-source
// regression: mcp.yaml lives only in the "team" source, so the capture would
// land in a .sources/<name> checkout that init gitignores — `friday remote
// push` could never ship it and the next `friday source update` would discard
// it. Refusing keeps the edit on the target where the user can still act on
// it.
func TestPullRenderMCPRefusesWriteBackIntoSource(t *testing.T) {
	roots, targetAbs := scaffold2(t,
		map[string]string{"mcp.yaml": "version: 1\nservers:\n  github:\n    transport: stdio\n    command: npx\n"},
		nil,
	)
	writeTarget(t, targetAbs, "settings.json", `{"mcpServers":{"github":{"type":"stdio","command":"npx-updated"}}}`)
	changes, err := planPull(nil, "claude", mcpPullAdapter(targetAbs), roots, targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Action != ActionUnsupported {
		t.Fatalf("got %+v, want one unsupported change (mcp.yaml is owned by a read-only source)", changes)
	}
	if changes[0].DestPath != "" {
		t.Errorf("DestPath = %q, must be empty so nothing can be written into the source", changes[0].DestPath)
	}
	if !strings.Contains(changes[0].Reason, `is owned by source "team"`) ||
		!strings.Contains(changes[0].Reason, "copy it into the primary store") {
		t.Errorf("reason must name the source and both escape routes: %q", changes[0].Reason)
	}
}

// TestPullRenderMCPPreservesCommentsAndOrder proves a pull patches the user's
// own mcp.yaml document instead of re-marshaling the decoded struct: comments
// survive, the authored (non-alphabetical) server order survives, and only
// the field that actually changed is rewritten.
func TestPullRenderMCPPreservesCommentsAndOrder(t *testing.T) {
	const authored = `# our shared servers
version: 1
servers:
  zeta:  # last alphabetically, first on purpose
    transport: stdio
    command: zeta-cmd
  alpha:
    transport: stdio
    command: old-alpha
`
	storeAbs, targetAbs := scaffold(t, map[string]string{"mcp.yaml": authored})
	writeTarget(t, targetAbs, "settings.json",
		`{"mcpServers":{"zeta":{"type":"stdio","command":"zeta-cmd"},"alpha":{"type":"stdio","command":"new-alpha"}}}`)
	changes, err := planPull(nil, "claude", mcpPullAdapter(targetAbs), singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	ch := changes[0]
	if ch.Action != ActionUpdate {
		t.Fatalf("action = %s, want update: %+v", ch.Action, ch)
	}
	got := string(ch.NewContent)
	if !strings.Contains(got, "# our shared servers") || !strings.Contains(got, "# last alphabetically") {
		t.Errorf("comments must survive a pull:\n%s", got)
	}
	if strings.Index(got, "zeta:") > strings.Index(got, "alpha:") {
		t.Errorf("authored server order must survive (zeta before alpha):\n%s", got)
	}
	if !strings.Contains(got, "new-alpha") || strings.Contains(got, "old-alpha") {
		t.Errorf("alpha's command must be updated:\n%s", got)
	}
}

func TestPullRenderMCPUnparseableTargetIsUnsupported(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{})
	writeTarget(t, targetAbs, "settings.json", `not valid json`)
	changes, err := planPull(nil, "claude", mcpPullAdapter(targetAbs), singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if changes[0].Action != ActionUnsupported {
		t.Fatalf("action = %s, want unsupported for unparseable settings.json", changes[0].Action)
	}
}

// TestPullRenderMCPCopilotWildcardToolsStaysImplicit is the "every pull pins
// every server's tool list" regression: copilot's renderer always writes
// EffectiveTools, so a server with no tools: key comes back as ["*"].
// Adopting that would convert friday's default into an explicit pinned list
// in the user's source of truth, for every server, on the first pull.
func TestPullRenderMCPCopilotWildcardToolsStaysImplicit(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{
		"mcp.yaml": "version: 1\nservers:\n  github:\n    transport: stdio\n    command: npx\n",
	})
	writeTarget(t, targetAbs, "mcp-config.json",
		`{"mcpServers":{"github":{"type":"local","command":"npx","tools":["*"]}}}`)
	ad := renderAdapter(targetAbs, renderRule("mcp.yaml", "mcp-config.json", rules.SurfaceMCP, rules.StrategyMergeJSON))
	changes, err := planPull(nil, "copilot", ad, singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	if changes[0].Action != ActionInSync {
		t.Fatalf("action = %s, want in-sync — the recovered wildcard is copilot's rendering of \"no tools key\": %+v", changes[0].Action, changes[0])
	}
	if strings.Contains(string(changes[0].NewContent), "tools") {
		t.Errorf("mcp.yaml must not grow an explicit tools list:\n%s", changes[0].NewContent)
	}
}

// TestPullRenderMCPCopilotCapturesRealToolsList is the other half: copilot is
// the one adapter whose native form genuinely carries tools, so a real list
// must still be captured.
func TestPullRenderMCPCopilotCapturesRealToolsList(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{
		"mcp.yaml": "version: 1\nservers:\n  github:\n    transport: stdio\n    command: npx\n",
	})
	writeTarget(t, targetAbs, "mcp-config.json",
		`{"mcpServers":{"github":{"type":"local","command":"npx","tools":["create_issue"]}}}`)
	ad := renderAdapter(targetAbs, renderRule("mcp.yaml", "mcp-config.json", rules.SurfaceMCP, rules.StrategyMergeJSON))
	changes, err := planPull(nil, "copilot", ad, singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	got, err := spec.ParseMCP(changes[0].NewContent)
	if err != nil {
		t.Fatalf("invalid mcp.yaml: %v\n%s", err, changes[0].NewContent)
	}
	if tools := got.Servers["github"].Tools; len(tools) != 1 || tools[0] != "create_issue" {
		t.Errorf("tools = %v, want [create_issue] captured", tools)
	}
}

// TestPullRenderMCPKeepsFieldsTheNativeShapeOmits covers the cross-transport
// deletion: claude's http entry carries url and headers but never env, so a
// pull must leave an existing env alone rather than treating "absent from the
// native form" as "deleted everywhere".
func TestPullRenderMCPKeepsFieldsTheNativeShapeOmits(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{
		"mcp.yaml": "version: 1\nservers:\n  docs:\n    transport: http\n    url: https://docs.example.com/mcp\n    env:\n      TOKEN: abc\n",
	})
	writeTarget(t, targetAbs, "settings.json",
		`{"mcpServers":{"docs":{"type":"http","url":"https://docs.example.com/v2"}}}`)
	changes, err := planPull(nil, "claude", mcpPullAdapter(targetAbs), singleRoot(storeAbs), targetAbs)
	if err != nil {
		t.Fatal(err)
	}
	got, err := spec.ParseMCP(changes[0].NewContent)
	if err != nil {
		t.Fatalf("invalid mcp.yaml: %v\n%s", err, changes[0].NewContent)
	}
	docs := got.Servers["docs"]
	if docs.URL != "https://docs.example.com/v2" {
		t.Errorf("url = %q, want the newly captured one", docs.URL)
	}
	if docs.Env["TOKEN"] != "abc" {
		t.Errorf("env = %v, want TOKEN preserved — claude's http shape has no env field to delete it from", docs.Env)
	}
}

// --- Normalize validation (see internal/rules/rule_test.go for the rest) ---

func TestPlanRenderUnknownSurfaceErrors(t *testing.T) {
	storeAbs, targetAbs := scaffold(t, map[string]string{"mcp.yaml": testMCPYAML})
	r := renderRule("mcp.yaml", "settings.json", "not-a-surface", rules.StrategyMergeJSON)
	// Bypass Normalize (which would already reject this) to exercise
	// planRender's own default case directly.
	_, err := planRender(nil, "claude", r, singleRoot(storeAbs), targetAbs, nil)
	if err == nil {
		t.Fatal("want error for unknown surface")
	}
}
