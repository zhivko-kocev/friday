package render

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/zhivko-kocev/friday/internal/spec"
)

// --- Claude -----------------------------------------------------------------

func TestPermissionsInverseClaudeRoundTripsFullMapping(t *testing.T) {
	payload, _, err := Permissions("claude", fullPermissions())
	if err != nil {
		t.Fatal(err)
	}
	recovered, residue, unmapped, err := PermissionsInverse("claude", payload)
	if err != nil {
		t.Fatal(err)
	}
	if residue != nil {
		t.Errorf("residue = %s, want none (every claude rule here is friday's own shape)", residue)
	}
	if unmapped != nil {
		t.Errorf("unmapped = %v, want none (claude reports the unrecognised via residue only)", unmapped)
	}
	want := fullPermissions()
	if !reflect.DeepEqual(recovered, want) {
		t.Errorf("recovered = %+v, want %+v", recovered, want)
	}
}

// TestPermissionsInverseClaudePreservesUnknownRulesAsResidue also covers the
// tier-dependent Bash shapes. claudeBashRule emits the exact form Bash(cmd)
// only for an allow and the prefix form Bash(cmd:*) only for a deny/ask (or
// an explicitly globbed allow), so the inverse must mirror that: a
// Bash(cmd) found in DENY is not friday's own shape and must survive
// verbatim in residue rather than be recovered as a pattern the next push
// would silently widen to Bash(cmd:*).
func TestPermissionsInverseClaudePreservesUnknownRulesAsResidue(t *testing.T) {
	payload := []byte(`{"permissions":{"allow":["Bash(git status:*)","Bash(npm run build)","Task"],"deny":["Bash(rm -rf)","TodoWrite"]}}`)
	recovered, residue, unmapped, err := PermissionsInverse("claude", payload)
	if err != nil {
		t.Fatal(err)
	}
	if unmapped != nil {
		t.Errorf("unmapped = %v, want none (claude's unrecognised rules go to residue, not unmapped)", unmapped)
	}
	// Allow tier: ":*" means the user globbed it, so the glob is part of the
	// canonical pattern; the bare form round-trips as the exact command.
	for _, want := range []string{"git status*", "npm run build"} {
		if !contains(recovered.Exec.Allow, want) {
			t.Errorf("exec.allow = %v, want %q recovered", recovered.Exec.Allow, want)
		}
	}
	if contains(recovered.Exec.Allow, "Task") {
		t.Errorf("%q must not leak into a recovered axis pattern: %v", "Task", recovered.Exec.Allow)
	}
	// Deny tier: friday always writes the prefix form here, so an exact-form
	// deny is the user's own and must not be recovered (recovering it would
	// widen their exact rule into a prefix on the next push).
	if contains(recovered.Exec.Deny, "rm -rf") {
		t.Errorf("an exact-form Bash deny must stay residue, not be recovered: %v", recovered.Exec.Deny)
	}
	var doc struct {
		Permissions struct{ Allow, Deny []string } `json:"permissions"`
	}
	if err := json.Unmarshal(residue, &doc); err != nil {
		t.Fatalf("residue is not valid JSON: %v\n%s", err, residue)
	}
	if !contains(doc.Permissions.Allow, "Task") {
		t.Errorf("residue.allow = %v, want %q preserved verbatim", doc.Permissions.Allow, "Task")
	}
	for _, want := range []string{"TodoWrite", "Bash(rm -rf)"} {
		if !contains(doc.Permissions.Deny, want) {
			t.Errorf("residue.deny = %v, want %q preserved verbatim", doc.Permissions.Deny, want)
		}
	}
}

func TestPermissionsInverseClaudeBareBashToolRoundTrips(t *testing.T) {
	// claudeBashRule renders a wildcard-only exec pattern as claude's bare
	// tool-name rule, so that shape must come back as "*", not residue.
	recovered, residue, _, err := PermissionsInverse("claude", []byte(`{"permissions":{"deny":["Bash"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if residue != nil {
		t.Errorf("residue = %s, want none", residue)
	}
	if !contains(recovered.Exec.Deny, "*") {
		t.Errorf("exec.deny = %v, want bare \"*\" recovered", recovered.Exec.Deny)
	}
}

func TestPermissionsInverseClaudeMCPBarePatternRoundTrips(t *testing.T) {
	// mcpRuleName only replaces a pattern's first "/"; a bare pattern like
	// "*" (every server) has none, so "mcp__*" must recover as "*", not be
	// rejected as unrecognised.
	payload := []byte(`{"permissions":{"ask":["mcp__*"]}}`)
	recovered, residue, _, err := PermissionsInverse("claude", payload)
	if err != nil {
		t.Fatal(err)
	}
	if residue != nil {
		t.Errorf("residue = %s, want none", residue)
	}
	if !contains(recovered.MCP.Ask, "*") {
		t.Errorf("mcp.ask = %v, want bare \"*\" recovered", recovered.MCP.Ask)
	}
}

func TestPermissionsInverseClaudeEmptyPayload(t *testing.T) {
	recovered, residue, unmapped, err := PermissionsInverse("claude", nil)
	if err != nil {
		t.Fatal(err)
	}
	if residue != nil || unmapped != nil {
		t.Errorf("residue=%s unmapped=%v, want both nil", residue, unmapped)
	}
	if !reflect.DeepEqual(recovered, spec.Permissions{Version: 1}) {
		t.Errorf("recovered = %+v, want empty", recovered)
	}
}

// --- Copilot ------------------------------------------------------------

func TestPermissionsInverseCopilotRecoversNetAxis(t *testing.T) {
	payload, _, err := Permissions("copilot", fullPermissions())
	if err != nil {
		t.Fatal(err)
	}
	recovered, residue, unmapped, err := PermissionsInverse("copilot", payload)
	if err != nil {
		t.Fatal(err)
	}
	if residue != nil || unmapped != nil {
		t.Errorf("residue=%s unmapped=%v, want both nil (copilot's shape is always net)", residue, unmapped)
	}
	want := spec.Permissions{Version: 1, Net: spec.PermissionAxis{Allow: []string{"docs.anthropic.com"}, Deny: []string{"*"}}}
	if !reflect.DeepEqual(recovered, want) {
		t.Errorf("recovered = %+v, want %+v (copilot has no ask tier)", recovered, want)
	}
}

// --- OpenCode -------------------------------------------------------------

func TestPermissionsInverseOpenCodeRecoversFourAxes(t *testing.T) {
	payload, _, err := Permissions("opencode", fullPermissions())
	if err != nil {
		t.Fatal(err)
	}
	recovered, residue, unmapped, err := PermissionsInverse("opencode", payload)
	if err != nil {
		t.Fatal(err)
	}
	if residue != nil {
		t.Errorf("residue = %s, want none (opencode never routes residue)", residue)
	}
	if unmapped != nil {
		t.Errorf("unmapped = %v, want none (every present key is friday's own)", unmapped)
	}
	want := fullPermissions()
	want.MCP = spec.PermissionAxis{} // opencode has no mcp surface; it never reached the file
	if !reflect.DeepEqual(recovered, want) {
		t.Errorf("recovered = %+v, want %+v", recovered, want)
	}
}

func TestPermissionsInverseOpenCodeUnmappedForNonStringValue(t *testing.T) {
	payload := []byte(`{"permission":{"bash":{"git status":"allow"}},"theme":"dark"}`)
	recovered, _, unmapped, err := PermissionsInverse("opencode", payload)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(recovered.Exec.Allow, "git status") {
		t.Errorf("exec.allow = %v, want git status recovered", recovered.Exec.Allow)
	}
	if unmapped != nil {
		t.Errorf("unmapped = %v, want none (theme lives outside the permission object entirely)", unmapped)
	}

	payload2 := []byte(`{"permission":{"bash":{"git status":"allow"},"theme":{"x":"y"}}}`)
	_, _, unmapped2, err := PermissionsInverse("opencode", payload2)
	if err != nil {
		t.Fatal(err)
	}
	if !anyContains(unmapped2, "theme") {
		t.Errorf("unmapped must name an unrecognised permission.* key: %v", unmapped2)
	}
}

// --- Codex ------------------------------------------------------------------

func TestPermissionsInverseCodexRecoversFilesystemAndNetwork(t *testing.T) {
	payload, _, err := Permissions("codex", fullPermissions())
	if err != nil {
		t.Fatal(err)
	}
	recovered, residue, unmapped, err := PermissionsInverse("codex", payload)
	if err != nil {
		t.Fatal(err)
	}
	if residue != nil {
		t.Errorf("residue = %s, want none (codex never routes residue)", residue)
	}
	if unmapped != nil {
		t.Errorf("unmapped = %v, want none for a payload codex's own renderer produced", unmapped)
	}
	if !contains(recovered.Read.Deny, "**/.env") {
		t.Errorf("read.deny = %v", recovered.Read.Deny)
	}
	if !contains(recovered.Write.Allow, "src/**") {
		t.Errorf("write.allow = %v", recovered.Write.Allow)
	}
	if !contains(recovered.Net.Allow, "docs.anthropic.com") || !contains(recovered.Net.Deny, "*") {
		t.Errorf("net = %+v", recovered.Net)
	}
	// codex has no ask tier and no exec/mcp surface at all — none of those
	// ever reached the native file, so none come back on pull.
	if len(recovered.Write.Ask) != 0 || len(recovered.Exec.Allow) != 0 || len(recovered.MCP.Allow) != 0 {
		t.Errorf("recovered picked up axes codex's renderer never wrote: %+v", recovered)
	}
}

func TestPermissionsInverseCodexDenyAppliesToBothReadAndWrite(t *testing.T) {
	payload, _, err := Permissions("codex", spec.Permissions{Version: 1, Write: spec.PermissionAxis{Deny: []string{"secrets/**"}}})
	if err != nil {
		t.Fatal(err)
	}
	recovered, _, _, err := PermissionsInverse("codex", payload)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(recovered.Read.Deny, "secrets/**") || !contains(recovered.Write.Deny, "secrets/**") {
		t.Errorf("a codex deny must recover onto both read and write (fail-closed): %+v", recovered)
	}
}

// --- Gemini / errors ----------------------------------------------------

func TestPermissionsInverseGeminiUnsupported(t *testing.T) {
	if _, _, _, err := PermissionsInverse("gemini", nil); err == nil {
		t.Error("want error: gemini's policy file is friday-owned via copy, not pullable")
	}
}

func TestPermissionsInverseUnknownAdapterErrors(t *testing.T) {
	if _, _, _, err := PermissionsInverse("bogus", nil); err == nil {
		t.Error("want error for unknown adapter")
	}
}
