package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/zhivko-kocev/friday/internal/spec"
)

// Permissions projects p onto adapter's native tool/file/network permission
// surface. payload is the file content to write — routed by the caller
// (planRender) per adapter: claude and copilot via merge-json into
// settings.json, codex via merge-toml into config.toml, opencode via a
// verbatim-key-order merge-json variant into opencode.json (see
// engine.mergeReplace), and gemini as a whole, friday-owned file via copy to
// ~/.gemini/policies/friday.toml. oh-my-pi has no permission surface friday
// can reach: its policy lives in config.yml (YAML), and friday has no
// merge-yaml strategy yet.
//
// unmapped lists every axis/action/pattern friday could not express for
// adapter, formatted for direct display (doctor, confirm prompts) — never
// silently dropped (D20).
func Permissions(adapter string, p spec.Permissions) (payload []byte, unmapped []string, err error) {
	switch adapter {
	case "claude":
		return permissionsClaude(p)
	case "copilot":
		return permissionsCopilot(p)
	case "opencode":
		return permissionsOpenCode(p)
	case "codex":
		return permissionsCodex(p)
	case "gemini":
		return permissionsGemini(p)
	case "oh-my-pi":
		return nil, allUnmapped(p), nil
	default:
		return nil, nil, fmt.Errorf("render.Permissions: unknown adapter %q", adapter)
	}
}

// allUnmapped flags every pattern across every axis as unmapped —
// oh-my-pi's case, which friday cannot reach at all (see the doc comment
// on Permissions above).
func allUnmapped(p spec.Permissions) []string {
	var out []string
	for _, a := range p.Axes() {
		out = append(out, axisUnmapped(a.Name, a.Axis)...)
	}
	return out
}

// axisUnmapped formats every pattern in axis (across all three actions) as a
// human-readable unmapped entry naming the axis, action, and pattern.
func axisUnmapped(axisName string, axis spec.PermissionAxis) []string {
	var out []string
	for _, action := range []string{spec.ActionAllow, spec.ActionAsk, spec.ActionDeny} {
		for _, pat := range patternsForAction(axis, action) {
			out = append(out, fmt.Sprintf("%s %s: %s", axisName, action, pat))
		}
	}
	return out
}

// axisHasPatterns reports whether an axis carries any pattern at all, so a
// renderer only reports what its projection of that axis leaves uncovered
// when the user actually declared something there.
func axisHasPatterns(a spec.PermissionAxis) bool {
	return len(a.Allow)+len(a.Ask)+len(a.Deny) > 0
}

// permissionCatchAll patterns match every possible target of every axis, so
// deciding that one covers another pattern needs no glob engine.
var permissionCatchAll = map[string]bool{"*": true, "**": true, "**/*": true}

// coveringAllows returns the allow patterns that certainly grant everything
// ask names. Only two cases are decided: an identical pattern, and a
// catch-all. Anything subtler is left alone — glob-covers-glob is not
// decidable here, and a wrong "covering" verdict would drop a grant the
// user did ask for.
func coveringAllows(allow []string, ask string) []string {
	var out []string
	for _, pat := range allow {
		if pat == ask || permissionCatchAll[pat] {
			out = append(out, pat)
		}
	}
	return out
}

// askCoveringAllows drops from axis.Allow every pattern that already grants
// everything an ask-tier pattern names, for the two adapters whose policy
// model has no ask tier at all. There, omitting the ask pattern only means
// "ask" while nothing wider survives to auto-approve it: keeping a covering
// allow inverts friday's own deny > ask > allow precedence and silently
// pre-approves the exact target friday was told to prompt for. Dropping the
// allow instead leaves the adapter's own default (prompt, or refuse) in
// effect, and the substitution is reported rather than done quietly.
func askCoveringAllows(axisName, adapter string, axis spec.PermissionAxis) (allow []string, reports []string) {
	drop := map[string]bool{}
	for _, ask := range axis.Ask {
		for _, cov := range coveringAllows(axis.Allow, ask) {
			if drop[cov] {
				continue
			}
			drop[cov] = true
			reports = append(reports, fmt.Sprintf("%s allow: %s dropped — %s has no ask tier, and this entry would auto-approve the %q ask pattern (fail closed)", axisName, cov, adapter, ask))
		}
	}
	if len(drop) == 0 {
		return axis.Allow, nil
	}
	for _, pat := range axis.Allow {
		if !drop[pat] {
			allow = append(allow, pat)
		}
	}
	return allow, reports
}

// --- Claude ---------------------------------------------------------------

// claudeRuleRenderer returns, for one friday axis, the function that turns a
// single pattern into the Claude Code permission rule string(s) it
// corresponds to, or a non-empty reason when Claude's own rule grammar
// cannot express that pattern at all. write produces two rules (Edit and
// Write) because Claude distinguishes them as separate tools; friday's
// write axis is their union.
func claudeRuleRenderer(axis string) func(pattern, action string) (rules []string, unrepresentable string) {
	switch axis {
	case spec.AxisRead:
		return func(p, _ string) ([]string, string) { return []string{"Read(" + p + ")"}, "" }
	case spec.AxisWrite:
		return func(p, _ string) ([]string, string) {
			return []string{"Edit(" + p + ")", "Write(" + p + ")"}, ""
		}
	case spec.AxisExec:
		return func(p, action string) ([]string, string) {
			rule, why := claudeBashRule(p, action)
			if why != "" {
				return nil, why
			}
			return []string{rule}, ""
		}
	case spec.AxisNet:
		return func(p, _ string) ([]string, string) { return []string{"WebFetch(domain:" + p + ")"}, "" }
	case spec.AxisMCP:
		return func(p, _ string) ([]string, string) { return []string{mcpRuleName(p)}, "" }
	default:
		return func(string, string) ([]string, string) { return nil, "" }
	}
}

// claudeBashRule renders one exec pattern as a Claude Bash rule.
//
// Claude matches a bare Bash(cmd) EXACTLY; the trailing ":*" form is its
// command-prefix match. Which one preserves the user's intent depends on the
// tier, so this is deliberately asymmetric — both directions fail closed:
//
//   - deny/ask: widen to the prefix form. A canonical `deny: ["git push
//     --force"]` must also stop "git push --force origin main"; the exact
//     form would leave every argument variant allowed.
//   - allow: keep the exact form. Widening an allow GRANTS permission the
//     user never wrote — `allow: ["git status"]` must not also permit
//     "git status && rm -rf /".
//
// A user who wants a prefixed allow writes the glob themselves ("git
// status*"), which lands in the trailing-wildcard branch below.
//
// Claude recognises its wildcard only at the end of a pattern, so a wildcard
// anywhere else has no equivalent, and a pattern that is nothing but
// wildcards is Claude's bare tool-name rule for the whole tool.
func claudeBashRule(pattern, action string) (rule, unrepresentable string) {
	trailingGlob := strings.HasSuffix(pattern, "*")
	cmd := strings.TrimRight(strings.TrimRight(pattern, "*"), " ")
	if strings.ContainsAny(cmd, "*?") {
		return "", "claude matches a Bash rule as an exact command or a trailing-wildcard prefix; a wildcard elsewhere in the pattern has no equivalent"
	}
	if cmd == "" {
		return "Bash", ""
	}
	if trailingGlob || action != spec.ActionAllow {
		return "Bash(" + cmd + ":*)", ""
	}
	return "Bash(" + cmd + ")", ""
}

// mcpRuleName turns an mcp-axis pattern ("<server>/<tool>", e.g. "github/*")
// into Claude's mcp__<server>__<tool> permission rule name.
func mcpRuleName(pattern string) string {
	return "mcp__" + strings.Replace(pattern, "/", "__", 1)
}

// permissionsClaude renders {"permissions": {"allow": [...], "ask": [...],
// "deny": [...]}}. Array order matters not at all to Claude's own evaluator
// (each of the three lists is checked as a set, deny first) so this is safe
// to route through the ordinary entry-level merge-json path (mergeEntries),
// which also means a user's own hand-approved rule in one of these arrays
// survives untouched. friday's three action names are Claude's three tier
// keys verbatim — friday's model is Claude's model (see spec's own comment
// on the action constants) — so the constants double as the JSON keys.
func permissionsClaude(p spec.Permissions) ([]byte, []string, error) {
	tiers := map[string][]string{}
	var unmapped []string
	for _, a := range p.Axes() {
		render := claudeRuleRenderer(a.Name)
		for _, action := range []string{spec.ActionAllow, spec.ActionAsk, spec.ActionDeny} {
			for _, pat := range patternsForAction(a.Axis, action) {
				rules, why := render(pat, action)
				if why != "" {
					unmapped = append(unmapped, fmt.Sprintf("%s %s: %s (%s)", a.Name, action, pat, why))
					continue
				}
				tiers[action] = append(tiers[action], rules...)
			}
		}
	}
	perms := map[string]any{}
	for _, action := range []string{spec.ActionAllow, spec.ActionAsk, spec.ActionDeny} {
		if len(tiers[action]) > 0 {
			perms[action] = tiers[action]
		}
	}
	if len(perms) == 0 {
		return nil, unmapped, nil
	}
	payload, err := json.Marshal(map[string]any{"permissions": perms})
	return payload, unmapped, err
}

// --- Copilot ----------------------------------------------------------------

// permissionsCopilot renders {"allowedUrls": [...], "deniedUrls": [...]}.
// Only the net axis is projected; read/write/exec/mcp are reported
// unmapped. Copilot CLI does document a per-path/per-command surface —
// ~/.copilot/permissions-config.json, whose tool_approvals kinds are
// commands/read/write/mcp — but it expresses the *allow* tier only (no
// deny, no ask, no URL rules) and is keyed by absolute location path, so
// reaching it is a scope decision friday has not made rather than one more
// renderer. Reporting those four axes unmapped is the fail-safe stand-in.
//
// net's ask-tier patterns have no explicit Copilot representation; omitting
// them from both lists leaves Copilot's own default (interactive prompt for
// anything not pre-approved or denied) in effect, which is exactly "ask" —
// so they are not reported unmapped. That equivalence only holds while no
// surviving allow entry covers the ask pattern, which is what
// askCoveringAllows guarantees.
func permissionsCopilot(p spec.Permissions) ([]byte, []string, error) {
	var unmapped []string
	for _, a := range p.Axes() {
		if a.Name == spec.AxisNet {
			continue
		}
		unmapped = append(unmapped, axisUnmapped(a.Name, a.Axis)...)
	}
	allowedURLs, dropped := askCoveringAllows(spec.AxisNet, "copilot", p.Net)
	unmapped = append(unmapped, dropped...)
	payload := map[string]any{}
	if len(allowedURLs) > 0 {
		payload["allowedUrls"] = allowedURLs
	}
	if len(p.Net.Deny) > 0 {
		payload["deniedUrls"] = p.Net.Deny
	}
	if len(payload) == 0 {
		return nil, unmapped, nil
	}
	b, err := json.Marshal(payload)
	return b, unmapped, err
}

// --- OpenCode -----------------------------------------------------------

// opencodeAxisKey maps a friday axis to the opencode.json permission.<key>
// object it renders into. mcp has no OpenCode equivalent.
var opencodeAxisKey = map[string]string{
	spec.AxisRead:  "read",
	spec.AxisWrite: "edit",
	spec.AxisExec:  "bash",
	spec.AxisNet:   "webfetch",
}

// permissionsOpenCode renders {"permission": {"<key>": {"<pattern>":
// "<action>", ...}, ...}}. OpenCode resolves an overlapping pattern within
// one of these objects by *last object key wins* (confirmed against its
// source: Object.entries() order == declaration order, findLast() over the
// resulting rule list) — not by pattern specificity. Every object here is
// therefore built by hand, allow-tier keys first, then ask-tier, then
// deny-tier last, so friday's own deny-beats-ask-beats-allow precedence is
// encoded directly into position: two patterns in the same tier always
// agree on the decision for anything they both match, so within-tier order
// carries no meaning and correctness never depends on getting it "right".
//
// This exact byte sequence must reach the file unchanged — see
// engine.mergeReplace, which this payload is designed to flow through
// instead of the ordinary entry-union merge-json path (mergeEntries would
// decode this object into a map[string]any and re-marshal it, and
// encoding/json always sorts map keys, silently inverting the precedence
// this function exists to encode).
func permissionsOpenCode(p spec.Permissions) ([]byte, []string, error) {
	var unmapped []string
	var buf bytes.Buffer
	buf.WriteString(`{"permission":{`)
	first := true
	for _, a := range p.Axes() {
		key, ok := opencodeAxisKey[a.Name]
		if !ok {
			unmapped = append(unmapped, axisUnmapped(a.Name, a.Axis)...)
			continue
		}
		obj, axUnmapped, err := opencodeTierOrderedObject(a.Name, a.Axis)
		if err != nil {
			return nil, nil, err
		}
		unmapped = append(unmapped, axUnmapped...)
		if obj == nil {
			continue
		}
		if !first {
			buf.WriteByte(',')
		}
		first = false
		keyJSON, err := json.Marshal(key)
		if err != nil {
			return nil, nil, err
		}
		buf.Write(keyJSON)
		buf.WriteByte(':')
		buf.Write(obj)
	}
	buf.WriteString("}}")
	if first {
		return nil, unmapped, nil
	}
	return buf.Bytes(), unmapped, nil
}

// opencodeIntegerLike matches a pattern ECMAScript would treat as an
// "integer index" property name (bare, no leading zero unless "0" itself).
// JS hoists such keys to the front of object enumeration in ascending
// numeric order regardless of source position — defeating the tier
// ordering opencodeTierOrderedObject exists to guarantee. Vanishingly
// unlikely for a real bash/edit/webfetch glob, but cheap to guard.
var opencodeIntegerLike = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)

// opencodeTierOrderedObject builds one permission.<key> object's patterns in
// allow-then-ask-then-deny order. See permissionsOpenCode for why order and
// hand-built (non-map, non-encoding/json) construction both matter here.
func opencodeTierOrderedObject(axisName string, axis spec.PermissionAxis) (obj json.RawMessage, unmapped []string, err error) {
	// The same pattern may legally sit in two tiers (and twice in one), but
	// a JSON object name must be unique — and this payload round-trips
	// through engine.mergeReplace's decode. friday's own precedence picks
	// the single tier each pattern is written in; emitting it there and
	// nowhere else keeps the decision identical without a duplicate key.
	decided := map[string]string{}
	for _, action := range []string{spec.ActionDeny, spec.ActionAsk, spec.ActionAllow} {
		for _, pat := range patternsForAction(axis, action) {
			if _, seen := decided[pat]; !seen {
				decided[pat] = action
			}
		}
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	first := true
	for _, action := range []string{spec.ActionAllow, spec.ActionAsk, spec.ActionDeny} {
		for _, pat := range patternsForAction(axis, action) {
			if decided[pat] != action {
				continue
			}
			delete(decided, pat)
			if opencodeIntegerLike.MatchString(pat) {
				unmapped = append(unmapped, fmt.Sprintf("%s %s: %s (all-digit pattern is unsafe for OpenCode's key-order precedence)", axisName, action, pat))
				continue
			}
			if !first {
				buf.WriteByte(',')
			}
			first = false
			key, merr := json.Marshal(pat)
			if merr != nil {
				return nil, nil, fmt.Errorf("pattern %q: %w", pat, merr)
			}
			buf.Write(key)
			buf.WriteString(`:"` + action + `"`) // action is always one of friday's own fixed literals
		}
	}
	buf.WriteByte('}')
	if first {
		return nil, unmapped, nil
	}
	return buf.Bytes(), unmapped, nil
}

// --- Codex ----------------------------------------------------------------

// codexRestrictiveness ranks Codex's three filesystem actions from least to
// most restrictive.
var codexRestrictiveness = map[string]int{"read": 0, "write": 1, "deny": 2}

// codexMoreRestrictive resolves a path independently touched by more than
// one friday axis (e.g. denied for read but allowed for write) the same way
// Codex's own path-specificity rule resolves two entries at the same path:
// deny beats write beats read. This keeps a security-relevant ambiguity
// fail-closed instead of depending on Go map iteration order.
func codexMoreRestrictive(a, b string) string {
	if codexRestrictiveness[a] >= codexRestrictiveness[b] {
		return a
	}
	return b
}

// permissionsCodex renders a [permissions.friday] profile table:
// https://learn.chatgpt.com/docs/permissions (verified 2026-08 against
// OpenAI's own docs — this is a beta surface under active development).
//
// Two structural facts this renderer must honour, both particular to
// Codex's schema:
//   - a bare relative glob is not a valid filesystem key on its own; it must
//     nest under the ":workspace_roots" path, which scopes it to the
//     session's (and profile's) workspace roots.
//   - Codex's filesystem/network models are binary (read/write/deny;
//     allow/deny) with no "ask" tier at all, so friday's ask-bucket patterns
//     have no representation here and are reported unmapped, the same as
//     the fully-unmapped exec and mcp axes (Codex's approval_policy — the
//     closest thing it has to a middle ground — is a session-wide setting,
//     not a per-pattern rule, and MCP tool access has no ACL in this
//     schema).
//
// Writing this table does not, by itself, change what Codex enforces: it
// only takes effect once the user sets default_permissions = "friday" (and,
// if any network rule exists, features.network_proxy = true) in
// config.toml — permission profiles are also mutually exclusive with the
// older sandbox_mode/sandbox_workspace_write settings, which take priority
// when present. That activation step is reported via unmapped rather than
// performed automatically, since flipping either is a global change to how
// every Codex session on the machine is sandboxed, not something scoped to
// friday's own owned keys.
func permissionsCodex(p spec.Permissions) ([]byte, []string, error) {
	var unmapped []string
	workspaceRoots := map[string]string{}
	setFS := func(pat, action string) {
		if existing, ok := workspaceRoots[pat]; ok {
			workspaceRoots[pat] = codexMoreRestrictive(existing, action)
		} else {
			workspaceRoots[pat] = action
		}
	}
	for _, fs := range []struct {
		name        string
		axis        spec.PermissionAxis
		allowAction string
	}{
		{spec.AxisRead, p.Read, "read"},
		{spec.AxisWrite, p.Write, "write"},
	} {
		allow, dropped := askCoveringAllows(fs.name, "codex", fs.axis)
		for _, pat := range allow {
			setFS(pat, fs.allowAction)
		}
		for _, pat := range fs.axis.Deny {
			setFS(pat, "deny")
		}
		for _, pat := range fs.axis.Ask {
			unmapped = append(unmapped, fmt.Sprintf("%s ask: %s (codex permission profiles have no ask tier)", fs.name, pat))
		}
		unmapped = append(unmapped, dropped...)
	}
	unmapped = append(unmapped, axisUnmapped(spec.AxisExec, p.Exec)...)
	unmapped = append(unmapped, axisUnmapped(spec.AxisMCP, p.MCP)...)

	netAllow, netDropped := askCoveringAllows(spec.AxisNet, "codex", p.Net)
	domains := map[string]string{}
	for _, h := range netAllow {
		domains[h] = "allow"
	}
	for _, h := range p.Net.Deny {
		domains[h] = "deny" // deny wins over an accidental duplicate allow entry too
	}
	for _, h := range p.Net.Ask {
		unmapped = append(unmapped, fmt.Sprintf("net ask: %s (codex permission profiles have no ask tier)", h))
	}
	unmapped = append(unmapped, netDropped...)

	profile := map[string]any{
		"description": "Rendered by friday from permissions.yaml.",
	}
	hasContent := false
	if len(workspaceRoots) > 0 {
		profile["filesystem"] = map[string]any{":workspace_roots": workspaceRoots}
		hasContent = true
	}
	if len(domains) > 0 {
		// network.enabled is the master switch for command network access,
		// so it follows the allow tier alone: derived from "any net rule"
		// it turned a deny-only policy into a widening.
		enabled := len(netAllow) > 0
		profile["network"] = map[string]any{"enabled": enabled, "domains": domains}
		hasContent = true
		if !enabled {
			unmapped = append(unmapped, "net: codex has no allow-everything-but mode, so a deny-only network policy renders network.enabled = false — all command network access off, broader than the deny patterns asked for but fail closed")
		}
	}
	if !hasContent {
		return nil, unmapped, nil
	}
	activation := `codex: profile written but inactive until default_permissions = "friday" is set in config.toml`
	if len(domains) > 0 {
		activation += "; its network rules additionally need features.network_proxy = true"
	}
	unmapped = append(unmapped, activation)
	payload, err := toml.Marshal(map[string]any{"permissions": map[string]any{"friday": profile}})
	return payload, unmapped, err
}

// --- Gemini -----------------------------------------------------------------

// geminiPriority mirrors friday's deny-beats-ask-beats-allow precedence as
// explicit numeric priorities, exactly as OpenAI's — sorry, Google's —
// policy engine's own docs illustrate (docs/reference/policy-engine.md).
// Because gemini-cli's PolicyEngine sorts rules by priority and takes the
// first match (packages/core/src/policy/policy-engine.ts), the *order*
// rules appear in the TOML file carries no meaning: unlike OpenCode, this
// axis needs no ordered-writer discipline.
var geminiPriority = map[string]int{
	spec.ActionDeny:  900,
	spec.ActionAsk:   500,
	spec.ActionAllow: 100,
}

// geminiDecision maps a friday action to gemini-cli's PolicyDecision enum
// value (packages/core/src/policy/types.ts).
var geminiDecision = map[string]string{
	spec.ActionDeny:  "deny",
	spec.ActionAsk:   "ask_user",
	spec.ActionAllow: "allow",
}

type geminiRule struct {
	ToolName      string `toml:"toolName,omitempty"`
	ArgsPattern   string `toml:"argsPattern,omitempty"`
	CommandPrefix string `toml:"commandPrefix,omitempty"`
	MCPName       string `toml:"mcpName,omitempty"`
	Decision      string `toml:"decision"`
	Priority      int    `toml:"priority"`
}

// geminiArgsPattern builds an argsPattern regex matching paramName's value
// against glob in gemini-cli's stable-stringified tool-args representation.
// It generalizes gemini-cli's own buildParamArgsPattern
// (packages/core/src/policy/utils.ts) — verified there for an *exact*
// value, including the leading/trailing "\0" sentinels that anchor a
// top-level JSON property inside that representation — to a *glob*: literal
// segments are regex-escaped, "*"/"**" become an unanchored ".*", and "?"
// becomes ".". This glob generalization is friday's own construction, not a
// literal gemini-cli feature; it is exercised in this package's tests
// against representative stringified-args samples, but has not been
// cross-checked against a live gemini-cli process.
func geminiArgsPattern(paramName, glob string) string {
	return "\x00" + geminiParamGlob(paramName, glob) + regexp.QuoteMeta(`"`) + "\x00"
}

// geminiCommandArgsPattern is geminiArgsPattern for run_shell_command's
// "command" argument minus the closing-quote anchor: friday's exec patterns
// name a command *prefix* (the same semantics as gemini's own commandPrefix
// shorthand), so whatever the user typed after the matched command must
// still fall inside the match.
func geminiCommandArgsPattern(glob string) string {
	return "\x00" + geminiParamGlob("command", glob)
}

// geminiParamGlob is the shared body of the two patterns above: the
// '"<paramName>":"' property prefix followed by glob translated to regex.
func geminiParamGlob(paramName, glob string) string {
	var re strings.Builder
	re.WriteString(regexp.QuoteMeta(`"` + paramName + `":"`))
	for i := 0; i < len(glob); i++ {
		switch {
		case glob[i] == '*' && i+2 < len(glob) && glob[i+1] == '*' && glob[i+2] == '/':
			// "**/" matches zero or more path segments, including zero, so
			// "**/.env" must also match a root-level ".env" — not only
			// "dir/.env". The following "/" is folded into the optional
			// group instead of emitted as a required literal.
			re.WriteString("(?:.*/)?")
			i += 2
		case glob[i] == '*' && i+1 < len(glob) && glob[i+1] == '*':
			re.WriteString(".*")
			i++
		case glob[i] == '*':
			re.WriteString(".*")
		case glob[i] == '?':
			re.WriteString(".")
		default:
			re.WriteString(regexp.QuoteMeta(string(glob[i])))
		}
	}
	return re.String()
}

// permissionsGemini renders a complete, friday-owned policy TOML file for
// ~/.gemini/policies/friday.toml (user tier — the only currently-functional
// per-machine tier; the workspace tier is hard-disabled in gemini-cli's own
// code as of this writing). Intended Merge is copy: gemini-cli independently
// loads and combines every *.toml file in that directory, so friday owning
// one whole file never conflicts with the user's own separate policy files.
//
// read/write axes are scoped to read_file/write_file only (both confirmed
// to take a "file_path" argument) and net to web_fetch's "url" argument —
// read_many_files, the edit/replace tool and google_web_search are
// deliberately excluded rather than guessed at, and every axis that leaves
// a tool uncovered says so through unmapped rather than presenting itself
// as full coverage.
//
// exec uses run_shell_command's commandPrefix shorthand (schema-valid only
// paired with that exact toolName) for a glob-free pattern, where a literal
// prefix test is exactly what friday's exec patterns mean. commandPrefix
// has no glob dialect at all, so a pattern carrying "*" or "?" would match
// only those characters themselves — never firing — and is routed through
// an argsPattern regex instead.
//
// mcp uses mcpName, gemini-cli's documented "recommended approach" for
// MCP-scoped rules, which names a whole MCP *server*: friday's
// "<server>/<tool>" pattern is split on its first "/", and a pattern naming
// one specific tool is reported unmapped because gemini has no
// server-plus-tool policy key to put it in.
func permissionsGemini(p spec.Permissions) ([]byte, []string, error) {
	var rules []geminiRule
	var unmapped []string

	fileRule := func(toolName string, axis spec.PermissionAxis) {
		for _, action := range []string{spec.ActionDeny, spec.ActionAsk, spec.ActionAllow} {
			for _, pat := range patternsForAction(axis, action) {
				rules = append(rules, geminiRule{
					ToolName:    toolName,
					ArgsPattern: geminiArgsPattern("file_path", pat),
					Decision:    geminiDecision[action],
					Priority:    geminiPriority[action],
				})
			}
		}
	}
	fileRule("read_file", p.Read)
	fileRule("write_file", p.Write)

	for _, action := range []string{spec.ActionDeny, spec.ActionAsk, spec.ActionAllow} {
		for _, cmd := range patternsForAction(p.Exec, action) {
			rule := geminiRule{
				ToolName: "run_shell_command",
				Decision: geminiDecision[action],
				Priority: geminiPriority[action],
			}
			if strings.ContainsAny(cmd, "*?") {
				rule.ArgsPattern = geminiCommandArgsPattern(cmd)
			} else {
				rule.CommandPrefix = cmd
			}
			rules = append(rules, rule)
		}
		for _, host := range patternsForAction(p.Net, action) {
			rules = append(rules, geminiRule{
				ToolName:    "web_fetch",
				ArgsPattern: geminiArgsPattern("url", host),
				Decision:    geminiDecision[action],
				Priority:    geminiPriority[action],
			})
		}
		for _, pat := range patternsForAction(p.MCP, action) {
			server, tool := pat, "*"
			if i := strings.Index(pat, "/"); i >= 0 {
				server, tool = pat[:i], pat[i+1:]
			}
			if server == "" || tool != "*" {
				unmapped = append(unmapped, fmt.Sprintf("mcp %s: %s (gemini's mcpName scopes a rule to a whole MCP server; a single tool cannot be named)", action, pat))
				continue
			}
			rules = append(rules, geminiRule{
				MCPName:  server,
				Decision: geminiDecision[action],
				Priority: geminiPriority[action],
			})
		}
	}

	unmapped = append(unmapped, geminiUncovered(p)...)

	if len(rules) == 0 {
		return nil, unmapped, nil
	}
	body, err := toml.Marshal(map[string]any{"rule": rules})
	if err != nil {
		return nil, nil, err
	}
	header := "# Managed by friday from permissions.yaml — re-run `friday push gemini` to\n" +
		"# regenerate. Do not hand-edit; other *.toml files in this directory are\n" +
		"# yours and are never touched by friday.\n\n"
	return append([]byte(header), body...), unmapped, nil
}

// geminiUncovered names the gemini tools each projected axis does not
// reach. The scoping itself is deliberate (only tools whose argument shape
// was verified), but a user denying "secrets/**" for write must know the
// replace/edit tool is still free to touch it.
func geminiUncovered(p spec.Permissions) []string {
	var out []string
	for _, u := range []struct {
		axis  spec.PermissionAxis
		entry string
	}{
		{p.Read, "read: gemini rules cover read_file only; read_many_files is not scoped by these patterns"},
		{p.Write, "write: gemini rules cover write_file only; the replace/edit tool is not scoped by these patterns"},
		{p.Net, "net: gemini rules cover web_fetch only; google_web_search is not scoped by these patterns"},
	} {
		if axisHasPatterns(u.axis) {
			out = append(out, u.entry)
		}
	}
	return out
}

// patternsForAction returns axis's patterns for one of friday's three
// actions, keeping the deny/ask/allow constants as the single source of
// truth for the mapping instead of three separate switch statements.
func patternsForAction(axis spec.PermissionAxis, action string) []string {
	switch action {
	case spec.ActionAllow:
		return axis.Allow
	case spec.ActionAsk:
		return axis.Ask
	case spec.ActionDeny:
		return axis.Deny
	default:
		return nil
	}
}

// --- Inverse ----------------------------------------------------------------

// axisField returns a pointer to p's field for axis, so a single append call
// works for every axis without a five-way switch at each call site. nil for
// an axis name Permissions does not define.
func axisField(p *spec.Permissions, axis string) *spec.PermissionAxis {
	switch axis {
	case spec.AxisRead:
		return &p.Read
	case spec.AxisWrite:
		return &p.Write
	case spec.AxisExec:
		return &p.Exec
	case spec.AxisNet:
		return &p.Net
	case spec.AxisMCP:
		return &p.MCP
	default:
		return nil
	}
}

// appendPattern adds pattern to axis's action bucket on p.
func appendPattern(p *spec.Permissions, axis, action, pattern string) {
	f := axisField(p, axis)
	if f == nil {
		return
	}
	switch action {
	case spec.ActionAllow:
		f.Allow = append(f.Allow, pattern)
	case spec.ActionAsk:
		f.Ask = append(f.Ask, pattern)
	case spec.ActionDeny:
		f.Deny = append(f.Deny, pattern)
	}
}

// PermissionsInverse recovers spec.Permissions axis patterns from adapter's
// native permissions payload — the inverse of Permissions, to the extent
// each adapter's own shape allows a reverse mapping at all (gemini's whole
// file is friday-owned via copy, not a co-owned merge, so there is nothing
// to pull it back from; err is returned for it exactly like an adapter
// MCPInverse does not support).
//
// residue is populated only for claude: a permission string that is not one
// of the five shapes Permissions itself renders (Read/Edit+Write/Bash/
// WebFetch domain/mcp__) is neither dropped nor guessed at — residue holds
// it, still wrapped in claude's own {"permissions":{...}} shape, for the
// caller to fold into claude's native passthrough fragment
// (settings/claude.json) so a later push re-contributes it losslessly
// (D20). copilot/opencode/codex are narrow, fully-structured schemas with
// no comparable safe residue destination (opencode's own tier-ordering and
// codex's single owned [permissions.friday] table would each risk a second
// writer corrupting what the render rule just wrote); anything they cannot
// recognise is instead named in unmapped for display only.
func PermissionsInverse(adapter string, payload []byte) (recovered spec.Permissions, residue []byte, unmapped []string, err error) {
	switch adapter {
	case "claude":
		recovered, residue, err = permissionsInverseClaude(payload)
	case "copilot":
		recovered, err = permissionsInverseCopilot(payload)
	case "opencode":
		recovered, unmapped, err = permissionsInverseOpenCode(payload)
	case "codex":
		recovered, unmapped, err = permissionsInverseCodex(payload)
	default:
		err = fmt.Errorf("no permissions pull-inverse for adapter %q", adapter)
	}
	return recovered, residue, unmapped, err
}

var (
	claudeReadRe  = regexp.MustCompile(`^Read\((.*)\)$`)
	claudeEditRe  = regexp.MustCompile(`^Edit\((.*)\)$`)
	claudeWriteRe = regexp.MustCompile(`^Write\((.*)\)$`)
	claudeBashRe  = regexp.MustCompile(`^Bash\((.*)\)$`)
	claudeNetRe   = regexp.MustCompile(`^WebFetch\(domain:(.*)\)$`)
)

// claudeRuleInverse reverses claudeRuleRenderer: it maps one Claude Code
// permission rule string back to the friday axis+pattern it would have been
// rendered from, or ok=false when the string is not one of the shapes
// friday itself produces — a tool friday's axes don't model at all (Task,
// TodoWrite, Glob, NotebookEdit, ...) or an mcp entry without the
// mcp__<server>__<tool> shape.
//
// Bash is tier-dependent, exactly mirroring claudeBashRule: friday emits the
// exact form only for an allow and the ":*" prefix form only for a
// deny/ask or an explicitly globbed pattern. So a Bash(cmd) found in
// deny/ask is NOT friday's shape — recovering it would widen the user's own
// exact rule into a prefix on the next push — and goes to residue verbatim.
func claudeRuleInverse(s, action string) (axis, pattern string, ok bool) {
	switch {
	case claudeReadRe.MatchString(s):
		return spec.AxisRead, claudeReadRe.FindStringSubmatch(s)[1], true
	case claudeEditRe.MatchString(s):
		return spec.AxisWrite, claudeEditRe.FindStringSubmatch(s)[1], true
	case claudeWriteRe.MatchString(s):
		return spec.AxisWrite, claudeWriteRe.FindStringSubmatch(s)[1], true
	case s == "Bash":
		// claudeBashRule's rendering of a wildcard-only exec pattern.
		return spec.AxisExec, "*", true
	case claudeBashRe.MatchString(s):
		cmd := claudeBashRe.FindStringSubmatch(s)[1]
		if strings.HasSuffix(cmd, ":*") {
			cmd = strings.TrimSuffix(cmd, ":*")
			// An allow only carries ":*" when the user globbed it, so the
			// glob is part of the canonical pattern; deny/ask always carry
			// it as friday's own prefix encoding.
			if action == spec.ActionAllow {
				return spec.AxisExec, cmd + "*", true
			}
			return spec.AxisExec, cmd, true
		}
		if action == spec.ActionAllow {
			return spec.AxisExec, cmd, true
		}
		return "", "", false
	case claudeNetRe.MatchString(s):
		return spec.AxisNet, claudeNetRe.FindStringSubmatch(s)[1], true
	case strings.HasPrefix(s, "mcp__"):
		// mcpRuleName replaces at most the pattern's first "/" with "__"; a
		// pattern with none (a bare "*" naming every server) leaves rest
		// with no "__" to replace back, so it round-trips unchanged.
		return spec.AxisMCP, strings.Replace(strings.TrimPrefix(s, "mcp__"), "__", "/", 1), true
	default:
		return "", "", false
	}
}

// permissionsInverseClaude reverses permissionsClaude. Each of the three
// native tiers is inverted independently — a residue string always keeps
// the tier (allow/ask/deny) it was found in, never re-bucketed — and
// Edit(X)/Write(X) collapse back to one write pattern per tier, mirroring
// the forward renderer's one-pattern-to-two-rules expansion.
func permissionsInverseClaude(payload []byte) (spec.Permissions, []byte, error) {
	recovered := spec.Permissions{Version: 1}
	if len(payload) == 0 {
		return recovered, nil, nil
	}
	var doc struct {
		Permissions struct {
			Allow []string `json:"allow"`
			Ask   []string `json:"ask"`
			Deny  []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(payload, &doc); err != nil {
		return spec.Permissions{}, nil, fmt.Errorf("invalid JSON: %w", err)
	}
	tiers := []struct {
		action string
		rules  []string
	}{
		{spec.ActionAllow, doc.Permissions.Allow},
		{spec.ActionAsk, doc.Permissions.Ask},
		{spec.ActionDeny, doc.Permissions.Deny},
	}
	residueByTier := map[string][]string{}
	for _, tier := range tiers {
		writeSeen := map[string]bool{}
		for _, r := range tier.rules {
			axis, pattern, ok := claudeRuleInverse(r, tier.action)
			if !ok {
				residueByTier[tier.action] = append(residueByTier[tier.action], r)
				continue
			}
			if axis == spec.AxisWrite {
				if writeSeen[pattern] {
					continue
				}
				writeSeen[pattern] = true
			}
			appendPattern(&recovered, axis, tier.action, pattern)
		}
	}
	if len(residueByTier) == 0 {
		return recovered, nil, nil
	}
	obj := map[string]any{}
	if r := residueByTier[spec.ActionAllow]; len(r) > 0 {
		obj["allow"] = r
	}
	if r := residueByTier[spec.ActionAsk]; len(r) > 0 {
		obj["ask"] = r
	}
	if r := residueByTier[spec.ActionDeny]; len(r) > 0 {
		obj["deny"] = r
	}
	residue, err := json.Marshal(map[string]any{"permissions": obj})
	if err != nil {
		return recovered, nil, fmt.Errorf("marshal residue: %w", err)
	}
	return recovered, residue, nil
}

// permissionsInverseCopilot reverses permissionsCopilot. Both native arrays
// are net-axis by construction, so nothing here is ever unrecognised.
func permissionsInverseCopilot(payload []byte) (spec.Permissions, error) {
	recovered := spec.Permissions{Version: 1}
	if len(payload) == 0 {
		return recovered, nil
	}
	var doc struct {
		AllowedURLs []string `json:"allowedUrls"`
		DeniedURLs  []string `json:"deniedUrls"`
	}
	if err := json.Unmarshal(payload, &doc); err != nil {
		return spec.Permissions{}, fmt.Errorf("invalid JSON: %w", err)
	}
	recovered.Net.Allow = doc.AllowedURLs
	recovered.Net.Deny = doc.DeniedURLs
	return recovered, nil
}

// permissionsInverseOpenCode reverses permissionsOpenCode by inverting
// opencodeAxisKey. A pattern whose value is not one of friday's three plain
// action strings (the {pattern:action} sub-object variant OpenCode's own
// docs mention, which friday never renders) is reported in unmapped rather
// than guessed at.
func permissionsInverseOpenCode(payload []byte) (spec.Permissions, []string, error) {
	recovered := spec.Permissions{Version: 1}
	if len(payload) == 0 {
		return recovered, nil, nil
	}
	var doc struct {
		Permission map[string]map[string]any `json:"permission"`
	}
	if err := json.Unmarshal(payload, &doc); err != nil {
		return spec.Permissions{}, nil, fmt.Errorf("invalid JSON: %w", err)
	}
	keyToAxis := make(map[string]string, len(opencodeAxisKey))
	for axis, key := range opencodeAxisKey {
		keyToAxis[key] = axis
	}
	var unmapped []string
	for _, key := range sortedStringKeys(doc.Permission) {
		patterns := doc.Permission[key]
		axis, ok := keyToAxis[key]
		if !ok {
			unmapped = append(unmapped, fmt.Sprintf("permission.%s: opencode key has no friday axis", key))
			continue
		}
		for _, pattern := range sortedAnyKeys(patterns) {
			v := patterns[pattern]
			action, ok := v.(string)
			if !ok {
				unmapped = append(unmapped, fmt.Sprintf("permission.%s.%s: value %v is not a plain action string", key, pattern, v))
				continue
			}
			switch action {
			case spec.ActionAllow, spec.ActionAsk, spec.ActionDeny:
				appendPattern(&recovered, axis, action, pattern)
			default:
				unmapped = append(unmapped, fmt.Sprintf("permission.%s.%s: unrecognised action %q", key, pattern, action))
			}
		}
	}
	return recovered, unmapped, nil
}

// sortedStringKeys returns m's keys in ascending order — every inverse
// function below reads a decoded JSON/TOML object as a Go map, whose
// iteration order Go deliberately randomises; the recovered Permissions'
// axis slices must be deterministic across runs (byte-identical writes,
// stable test assertions), so every such map is walked in sorted key
// order rather than range's native order.
func sortedStringKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sortedAnyKeys is sortedStringKeys for a map[string]any — Go generics
// can't unify map[string]any with map[string]V at one instantiation the
// way the two call sites below need, so this is its own tiny wrapper.
func sortedAnyKeys(m map[string]any) []string { return sortedStringKeys(m) }

// permissionsInverseCodex reverses permissionsCodex's filesystem/network
// tables. A codex "deny" cannot tell whether it originated from Read.Deny,
// Write.Deny, or both (codexMoreRestrictive collapses all three into
// whichever is most restrictive) — recovering it onto both axes is the
// fail-closed choice: a spurious extra deny is safe, a dropped one is not.
// ask-tier and exec/mcp axes never reach this table at all (permissionsCodex
// reports them unmapped on the way out), so there is nothing to recover for
// them here.
func permissionsInverseCodex(payload []byte) (spec.Permissions, []string, error) {
	recovered := spec.Permissions{Version: 1}
	if len(payload) == 0 {
		return recovered, nil, nil
	}
	var doc struct {
		Permissions struct {
			Friday struct {
				Filesystem map[string]any `toml:"filesystem"`
				Network    struct {
					Domains map[string]string `toml:"domains"`
				} `toml:"network"`
			} `toml:"friday"`
		} `toml:"permissions"`
	}
	if err := toml.Unmarshal(payload, &doc); err != nil {
		return spec.Permissions{}, nil, fmt.Errorf("invalid TOML: %w", err)
	}
	var unmapped []string
	for _, key := range sortedAnyKeys(doc.Permissions.Friday.Filesystem) {
		v := doc.Permissions.Friday.Filesystem[key]
		if key != ":workspace_roots" {
			unmapped = append(unmapped, fmt.Sprintf("permissions.friday.filesystem[%q]: unrecognised key", key))
			continue
		}
		roots, ok := v.(map[string]any)
		if !ok {
			unmapped = append(unmapped, "permissions.friday.filesystem: :workspace_roots is not a table")
			continue
		}
		for _, pat := range sortedAnyKeys(roots) {
			av := roots[pat]
			action, ok := av.(string)
			if !ok {
				unmapped = append(unmapped, fmt.Sprintf("permissions.friday.filesystem[%q]: value is not a string", pat))
				continue
			}
			switch action {
			case "read":
				recovered.Read.Allow = append(recovered.Read.Allow, pat)
			case "write":
				recovered.Write.Allow = append(recovered.Write.Allow, pat)
			case "deny":
				recovered.Read.Deny = append(recovered.Read.Deny, pat)
				recovered.Write.Deny = append(recovered.Write.Deny, pat)
			default:
				unmapped = append(unmapped, fmt.Sprintf("permissions.friday.filesystem[%q]: unrecognised action %q", pat, action))
			}
		}
	}
	for _, host := range sortedStringKeys(doc.Permissions.Friday.Network.Domains) {
		action := doc.Permissions.Friday.Network.Domains[host]
		switch action {
		case "allow":
			recovered.Net.Allow = append(recovered.Net.Allow, host)
		case "deny":
			recovered.Net.Deny = append(recovered.Net.Deny, host)
		default:
			unmapped = append(unmapped, fmt.Sprintf("permissions.friday.network.domains[%q]: unrecognised action %q", host, action))
		}
	}
	return recovered, unmapped, nil
}
