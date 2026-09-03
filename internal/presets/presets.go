// Package presets ships built-in adapter rule sets so users don't have to
// hand-write friday.yaml for the common agents.
//
// Each preset mirrors the layout that the corresponding agent expects.
// `friday init` seeds friday.yaml with all built-in presets; to disable one,
// delete its entry from friday.yaml.
package presets

import (
	"maps"
	"slices"
	"sort"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/rules"
)

type Preset struct {
	Name   string
	Target string
	Rules  []*rules.Rule
	// InstalledIf names other adapters whose target dir presence counts
	// this preset as installed, instead of its own Target — for a preset
	// like the shared "agents" adapter, whose own directory friday itself
	// creates only once something needs to write there, so checking it
	// directly would never detect a fresh machine as having it "installed".
	//
	// It applies ONLY when friday.yaml has no adapter of this name. The
	// manifest is authoritative (docs/USE_CASES.md), so a user who declares
	// their own `agents:` adapter pointing somewhere else gets the ordinary
	// "does my Target exist" test — never this preset's proxy list, which
	// would both hide their existing dir and recreate one they deleted.
	// See internal/cli's adapterInstalled.
	InstalledIf []string
	// ProjectTarget and ProjectRules describe the same agent at project
	// scope: `friday setup` resolves ProjectTarget against the project dir
	// ("." for every built-in) and applies ProjectRules, whose To paths
	// carry the in-repo config-dir prefixes (.claude/, .github/, ...).
	ProjectTarget string
	ProjectRules  []*rules.Rule
}

func (p Preset) Adapter() *config.Adapter {
	return &config.Adapter{Target: p.Target, Rules: p.Rules}
}

// ProjectAdapter renders the preset's project-scope shape. Nil rules means
// the preset has no project-scope story yet.
func (p Preset) ProjectAdapter() *config.Adapter {
	return &config.Adapter{Target: p.ProjectTarget, Rules: p.ProjectRules}
}

// EntryFiles are the store entry-file variants, most-preferred first: core.md
// is the canonical name, core/core.md matches knowledge repos that nest it
// (e.g. developer-os), identity.md is the legacy pre-0.0.5 name. Absent
// variants expand to nothing, so listing all three costs nothing; `friday
// doctor` warns when a store carries more than one. The single source of the
// list — setup's catalog and doctor's entry-file check consume it too.
var EntryFiles = rules.FromSpec{"core.md", "core/core.md", "identity.md"}

// entryPlus returns a fresh from-list of the entry-file variants followed by
// any extra patterns.
func entryPlus(more ...string) rules.FromSpec {
	return append(slices.Clone(EntryFiles), more...)
}

// pluginRootMarker is the path variable Claude Code plugins use to reference
// sibling files. Knowledge repos authored as plugins (e.g. developer-os) carry
// it in skill/agent/standards bodies.
const pluginRootMarker = "${CLAUDE_PLUGIN_ROOT}"

// storeReplace rewrites the marker to the canonical store, which always
// exists on a machine running friday and holds every referenced file.
// Presets deliberately do NOT rewrite to the adapter's own dir: agent
// content legitimately mentions paths like ~/.claude/..., and pull's
// textual inverse would corrupt those; ~/.friday never occurs naturally in
// a friday-free knowledge repo. The rewrite is a store-wide invariant, so
// Get stamps it onto every built-in rule — the registry stays pure layout
// data and a new rule can't forget it.
var storeReplace = map[string]string{pluginRootMarker: "~/.friday"}

// The presets map every store directory into the place each agent documents
// for it (paths verified against each harness's docs, August 2026):
//
//	store          claude         codex          copilot            opencode       gemini             oh-my-pi
//	core+rules     CLAUDE.md      AGENTS.md      copilot-instr..md  AGENTS.md      GEMINI.md          AGENTS.md
//	agents/        agents/        *.toml render  agents/*.agent.md  agents/        agents/            agents/
//	commands/      commands/      prompts/       —                  commands/      *.toml render      commands/
//	skills/        claude and oh-my-pi each keep their own skills/ (native, higher discovery priority); codex/copilot/gemini/opencode share ~/.agents/skills/ via the "agents" preset below
//	mcp.yaml       ../.claude.json config.toml   mcp-config.json    opencode.json  settings.json      mcp.json
//	permissions.yaml settings.json config.toml  settings.json      opencode.json  policies/friday.toml —
//	defaults.yaml  settings.json  config.toml    settings.json      opencode.json+tui.json settings.json —
//	hooks/         settings.json  hooks.json     hooks/friday-git-guard.json plugins/*.ts settings.json extensions/*.ts
//	native/<a>/    verbatim mirror of that adapter's own config dir, for anything the rows above don't model
//	settings/<a>   extra keys/tables merged into the adapter's own settings file (the mcp/permissions/defaults destination above), for what those schemas don't canonicalize
//
// "—" means the agent has no such surface at all, or friday cannot yet reach
// the one it has: oh-my-pi's permissions/defaults both live in its own
// config.yml, which is YAML, and friday only has merge-json/merge-toml
// co-owned-merge strategies today (no merge-yaml) — tracked in ROADMAP.md
// rather than built here.
//
// mcp.yaml, permissions.yaml, and defaults.yaml are friday's own canonical
// schemas (internal/spec), projected per adapter through internal/render —
// see the render strategy's Surface/Merge fields on each rule below, not a
// literal file copy. Every other row above is copy/concatenate/merge-json/
// merge-toml against a native, adapter-shaped source in the store.
//
// Every agent has a hook path: the four with a declarative hook file
// (Claude/Codex/Gemini settings.json or hooks.json, Copilot hooks/*.json)
// are wired by merge-json; OpenCode and oh-my-pi expose hooks only as
// imperative TS plugins/extensions, so friday drops a guard plugin that
// shells out to the shared script (confirm-first, like the merge writes).
//
// hooks/ is never a plain reference copy. For the declarative agents an
// agent activates only the hooks its config declares (Claude Code, e.g.,
// ignores a loose ~/.claude/hooks/hooks.json and reads only settings.json),
// so friday merges the store's hooks into that config file (merge-json)
// rather than dropping an inert copy. For OpenCode/oh-my-pi the plugin file
// IS the activation, so its copy is gated behind the same confirmer
// (confirm_hook).
//
// Every native/<a>/**/* mirror is confirm_hook too, for the same reason the
// plugin copies are: a mirror lands verbatim in the directory the agent
// auto-executes from, so a store carrying native/claude/settings.json with a
// SessionStart command, or native/opencode/plugins/anything.ts, installs
// running code. The mirror is a deliberate escape hatch, so friday cannot
// tell hook wiring from a theme palette inside it and must treat the whole
// tree as hook-bearing: --no-interactive skips it, --force writes it, and
// interactive asks per file. Without this the mirror is a one-file bypass of
// every confirm-first guarantee below.
//
// A mirror must never carry a file some other rule in the same preset also
// writes (settings.json, config.toml, opencode.json, ...): a whole-file copy
// discards the merge composition, including friday's own confirmed hook
// wiring, and the copy's whole-file drift baseline then fights the merge
// rules for the same destination. Extra keys belong in settings/<a>.{json,
// toml}, which merges. `friday doctor` reports the overlap as
// `dest-collision`.

// nativeMirror is the verbatim passthrough every adapter carries: whatever the
// rows above do not model, dropped into that adapter's own config dir exactly
// as the store holds it.
//
// One constructor rather than six identical literals, so ConfirmHook cannot be
// forgotten when a seventh adapter lands — the mirror writes into a directory
// the agent auto-executes from, which is the whole reason consent is required.
func nativeMirror(adapter string) *rules.Rule {
	return &rules.Rule{
		From:        rules.FromSpec{"native/" + adapter + "/**/*"},
		To:          "{relpath}",
		ConfirmHook: true,
	}
}

var registry = map[string]Preset{
	// Shared "agents" adapter: codex, copilot, gemini, and opencode discover
	// Agent-Skills-standard skills under this one directory natively (unlike
	// agents/commands/hooks, which each still have their own agent-specific
	// layout) — one skills tree instead of four duplicate copies. claude and
	// oh-my-pi each keep their own skills/**/* rule below: claude reads only
	// ~/.claude/skills, oh-my-pi's own .omp/agent/skills/ outranks this
	// shared path at its own higher native discovery priority — neither
	// ever reads ~/.agents. InstalledIf means this counts as installed when
	// any of those four is, never by its own ~/.agents existing — friday
	// creates that directory itself only once a push actually needs it, so
	// a fresh machine would otherwise never be seen as "having" it.
	"agents": {
		Name:        "agents",
		Target:      "~/.agents",
		InstalledIf: []string{"codex", "copilot", "gemini", "opencode"},
		Rules: []*rules.Rule{
			{From: rules.FromSpec{"skills/**/*"}, To: "skills/{relpath}"},
		},
	},
	"claude": {
		Name:   "claude",
		Target: ".claude",
		Rules: []*rules.Rule{
			{
				From:     entryPlus("rules/*.md"),
				To:       "CLAUDE.md",
				Strategy: rules.StrategyConcatenate,
			},
			{From: rules.FromSpec{"agents/*.md"}, To: "agents/{filename}"},
			{From: rules.FromSpec{"commands/*.md"}, To: "commands/{filename}"},
			{From: rules.FromSpec{"skills/**/*"}, To: "skills/{relpath}"},
			// Claude's user-scope MCP config lives only in ~/.claude.json (not
			// settings.json), which also holds the OAuth session; resolving
			// ".." against Target (".claude") lands exactly there. render's
			// merge-json sets driftExempt (planRenderCanonical), so this write
			// is confirmed first exactly like the hooks merge-json rule below.
			{
				From:     rules.FromSpec{"mcp.yaml"},
				To:       "../.claude.json",
				Strategy: rules.StrategyRender,
				Surface:  rules.SurfaceMCP,
				Merge:    rules.StrategyMergeJSON,
			},
			{
				From:     rules.FromSpec{"permissions.yaml"},
				To:       "settings.json",
				Strategy: rules.StrategyRender,
				Surface:  rules.SurfacePermissions,
				Merge:    rules.StrategyMergeJSON,
			},
			// friday's model/theme/statusline/telemetry defaults land in the
			// same settings.json the permissions rule above already owns.
			{
				From:     rules.FromSpec{"defaults.yaml"},
				To:       "settings.json",
				Strategy: rules.StrategyRender,
				Surface:  rules.SurfaceDefaults,
				Merge:    rules.StrategyMergeJSON,
			},
			// Claude Code only activates hooks declared in settings.json — a
			// loose ~/.claude/hooks/hooks.json is never loaded — so merge the
			// store's hooks.json into settings.json's `hooks` key. The scripts
			// run from the store in place; the command paths rewrite the plugin
			// marker to $HOME/.friday (not ~/.friday: a hook command runs in a
			// shell that expands $HOME but need not expand ~). merge-json is
			// push-only and drift-exempt so the user's other settings survive.
			// This shares settings.json with the permissions rule above —
			// planPush's pending map composes the two writes correctly.
			{
				From:     rules.FromSpec{"hooks/hooks.json"},
				To:       "settings.json",
				Strategy: rules.StrategyMergeJSON,
				Replace:  map[string]string{pluginRootMarker: "$HOME/.friday"},
			},
			// Escape hatch for whatever this preset does not model — theme
			// palettes, output-styles/*.md, or anything a vendor ships next.
			// native/claude/**/* lands verbatim at ~/.claude/.
			// confirm_hook: a mirrored settings.json can carry hook commands —
			// see the mirror paragraph in the package comment.
			nativeMirror("claude"),
			// Arbitrary additional settings.json keys this preset does not
			// canonicalize (permissions.yaml/defaults.yaml/hooks.json own
			// their own keys in this same file).
			{
				From:     rules.FromSpec{"settings/claude.json"},
				To:       "settings.json",
				Strategy: rules.StrategyMergeJSON,
			},
		},
		// Claude Code reads ./CLAUDE.md at the repo root and discovers
		// agents/commands/skills under ./.claude/.
		// https://code.claude.com/docs/en/skills
		ProjectTarget: ".",
		ProjectRules: []*rules.Rule{
			{
				From:     entryPlus("rules/*.md"),
				To:       "CLAUDE.md",
				Strategy: rules.StrategyConcatenate,
			},
			{From: rules.FromSpec{"agents/*.md"}, To: ".claude/agents/{filename}"},
			{From: rules.FromSpec{"commands/*.md"}, To: ".claude/commands/{filename}"},
			{From: rules.FromSpec{"skills/**/*"}, To: ".claude/skills/{relpath}"},
			// Project scope keeps the hooks tree in-repo (a teammate cloning it
			// has no ~/.friday), so copy it into .claude/hooks/ and wire
			// settings.json to run the scripts via ${CLAUDE_PROJECT_DIR} — the
			// project path variable Claude Code provides to hooks. The copied
			// hooks.json is inert (Claude Code loads settings.json, not it); the
			// merge-json rule is confirm-first, so a committed shared settings.json
			// is never written unattended.
			{From: rules.FromSpec{"hooks/**/*"}, To: ".claude/hooks/{relpath}"},
			{
				From:     rules.FromSpec{"hooks/hooks.json"},
				To:       ".claude/settings.json",
				Strategy: rules.StrategyMergeJSON,
				Replace:  map[string]string{pluginRootMarker: "${CLAUDE_PROJECT_DIR}/.claude"},
			},
		},
	},
	// Cursor user-level rules live inside Cursor's settings UI, not on the
	// filesystem (see https://cursor.com/docs/rules). A user-level preset has
	// nothing to write that Cursor would read; the project-level pattern
	// (.cursor/rules/*.mdc) belongs to project scope, which friday does not
	// support yet. Once Cursor ships filesystem-backed global rules, or
	// friday gains project scope, this preset can come back.
	//
	// Also intentionally absent, for the same reason a wrong preset is worse
	// than none (it writes junk into home dirs):
	//   - Continue auto-loads only the workspace .continue/rules/; a global
	//     ~/.continue/rules is not documented (https://docs.continue.dev/customize/deep-dives/rules).
	//   - Aider takes context via `read:` entries in ~/.aider.conf.yml, not
	//     a conventional instructions dir.
	//   - Zed keeps global rules in its internal Rules Library, not files.
	//   - Codeium has no filesystem instruction path.
	//   - Windsurf / Cascade: dropped in v0.6.0. Cognition is folding it into
	//     Devin Desktop (docs.windsurf.com 307-redirects to docs.devin.ai) and
	//     the legacy Cascade line reached EOL 2026-07-01, so its paths are a
	//     moving target. It can return as a `devin` preset once Devin Desktop's
	//     config surface settles under its new name.
	"codex": {
		Name: "codex",
		// Codex CLI reads ~/.codex/AGENTS.md and scans top-level markdown in
		// ~/.codex/prompts/ as custom prompts (deprecated in favor of
		// skills, still supported). Skills come from the shared "agents"
		// adapter (~/.agents/skills), which Codex also discovers natively.
		// https://developers.openai.com/codex/guides/agents-md
		// https://developers.openai.com/codex/custom-prompts
		Target: ".codex",
		Rules: []*rules.Rule{
			{
				From:     entryPlus("rules/*.md"),
				To:       "AGENTS.md",
				Strategy: rules.StrategyConcatenate,
			},
			// Codex subagents are TOML at ~/.codex/agents/<name>.toml
			// (name/description/developer_instructions) — confirmed by
			// codex-rs/core/config.schema.json and
			// https://learn.chatgpt.com/docs/agent-configuration/subagents.
			// Render the Claude-shaped agents/*.md into that shape (push-only —
			// other frontmatter is dropped).
			{
				From:     rules.FromSpec{"agents/*.md"},
				To:       "agents/{stem}.toml",
				Strategy: rules.StrategyRender,
				Surface:  rules.SurfaceCodexAgent,
				Merge:    rules.StrategyCopy,
			},
			{From: rules.FromSpec{"commands/*.md"}, To: "prompts/{filename}"},
			// MCP servers and the friday permission profile both live in
			// ~/.codex/config.toml — spliced in as owned tables
			// ([mcp_servers.*] and [permissions.friday]) alongside whatever
			// else the user's own config.toml already has.
			{
				From:     rules.FromSpec{"mcp.yaml"},
				To:       "config.toml",
				Strategy: rules.StrategyRender,
				Surface:  rules.SurfaceMCP,
				Merge:    rules.StrategyMergeTOML,
			},
			{
				From:     rules.FromSpec{"permissions.yaml"},
				To:       "config.toml",
				Strategy: rules.StrategyRender,
				Surface:  rules.SurfacePermissions,
				Merge:    rules.StrategyMergeTOML,
			},
			// friday's model/effort land as bare top-level scalars
			// (model, model_provider, model_reasoning_effort); theme and the
			// telemetry opt-out own the [tui] and [analytics] tables
			// respectively — both reserved from the passthrough fragment
			// below, the same way [mcp_servers]/[permissions] already are.
			{
				From:     rules.FromSpec{"defaults.yaml"},
				To:       "config.toml",
				Strategy: rules.StrategyRender,
				Surface:  rules.SurfaceDefaults,
				Merge:    rules.StrategyMergeTOML,
			},
			// Codex CLI reads ~/.codex/hooks.json and runs the same PreToolUse
			// hooks dialect as Claude Code (top-level `hooks` key; a matched
			// PreToolUse command that exits 2 blocks the tool call). The store
			// carries a Codex-shaped source that runs the shared guard in
			// exit-2 mode; the plugin marker rewrites to $HOME/.friday like the
			// claude rule. merge-json is push-only, drift-exempt, confirm-first.
			// NOTE: the tool matcher ("Bash") and the exit-2 deny are per Codex's
			// docs; verify against a live Codex install (see ROADMAP).
			// https://learn.chatgpt.com/docs/hooks
			{
				From:     rules.FromSpec{"hooks/codex/hooks.json"},
				To:       "hooks.json",
				Strategy: rules.StrategyMergeJSON,
				Replace:  map[string]string{pluginRootMarker: "$HOME/.friday"},
			},
			// Escape hatch for whatever this preset does not model — theme
			// files, or anything a vendor ships next. native/codex/**/* lands
			// verbatim at ~/.codex/.
			// confirm_hook: a mirrored hooks.json can carry hook commands —
			// see the mirror paragraph in the package comment.
			nativeMirror("codex"),
			// Arbitrary additional config.toml tables this preset does not
			// canonicalize (mcp.yaml/permissions.yaml/defaults.yaml own
			// [mcp_servers], [permissions], model/model_provider/
			// model_reasoning_effort, [tui], and [analytics] in this same
			// file). Tables() derives the owned-table set from the fragment
			// itself, so writing only [my_table] here touches only
			// [my_table].
			{
				From:     rules.FromSpec{"settings/codex.toml"},
				To:       "config.toml",
				Strategy: rules.StrategyMergeTOML,
			},
		},
		// Codex reads AGENTS.md at the repo root at project scope.
		// https://developers.openai.com/codex/guides/agents-md
		ProjectTarget: ".",
		ProjectRules: []*rules.Rule{
			{
				From:     entryPlus("rules/*.md"),
				To:       "AGENTS.md",
				Strategy: rules.StrategyConcatenate,
			},
		},
	},
	"opencode": {
		Name: "opencode",
		// OpenCode follows XDG: global config at $HOME/.config/opencode.
		Target: ".config/opencode",
		Rules: []*rules.Rule{
			// OpenCode has no native rules/ dir — AGENTS.md is the instruction
			// file (auto-loaded, walked cwd-up). Concatenate core + rules into
			// it (matching project scope and the other AGENTS.md agents) rather
			// than writing an inert rules/ tree OpenCode never reads. A store can
			// still register individual files via opencode.json's `instructions`
			// glob by hand. https://opencode.ai/docs/rules/
			{
				From:     entryPlus("rules/*.md"),
				To:       "AGENTS.md",
				Strategy: rules.StrategyConcatenate,
			},
			// OpenCode agents live in agents/ and take their name from the
			// filename; `tools` is a true/false map there (deprecated for
			// `permission`), so Claude's comma-list form is stripped along
			// with `name` and Claude's `model` sentinels (inherit/sonnet/...).
			// description/color survive; tool boundaries don't
			// carry over — configure `permission` in opencode.json if needed.
			// https://opencode.ai/docs/agents/
			{
				From:             rules.FromSpec{"agents/*.md"},
				To:               "agents/{filename}",
				FrontmatterStrip: []string{"name", "tools", "model"},
			},
			// Custom commands: markdown with $ARGUMENTS, same dialect as
			// Claude's. https://opencode.ai/docs/commands/
			{From: rules.FromSpec{"commands/*.md"}, To: "commands/{filename}"},
			// MCP servers land under opencode.json's "mcp" key (type:
			// local|remote, command as a [exe, args...] array). friday's
			// permission profile lands under the same file's "permission" key
			// (routed through the order-preserving mergeReplace path — see
			// plan.go — instead of the ordinary entry-union merge, since
			// OpenCode evaluates permission.* by last-key-wins). Never write
			// theme/keybinds/tui here — those live in a separate tui.json
			// upstream (see the defaults.yaml rule below).
			{
				From:     rules.FromSpec{"mcp.yaml"},
				To:       "opencode.json",
				Strategy: rules.StrategyRender,
				Surface:  rules.SurfaceMCP,
				Merge:    rules.StrategyMergeJSON,
			},
			{
				From:     rules.FromSpec{"permissions.yaml"},
				To:       "opencode.json",
				Strategy: rules.StrategyRender,
				Surface:  rules.SurfacePermissions,
				Merge:    rules.StrategyMergeJSON,
			},
			// friday's model and telemetry defaults land in the same
			// opencode.json the mcp/permissions rules above already own.
			// effort has no equivalent here (see render.Defaults) and theme
			// lands in tui.json instead, via the dedicated rule below.
			{
				From:     rules.FromSpec{"defaults.yaml"},
				To:       "opencode.json",
				Strategy: rules.StrategyRender,
				Surface:  rules.SurfaceDefaults,
				Merge:    rules.StrategyMergeJSON,
			},
			// OpenCode keeps theme (and keybinds/tui, which friday does not
			// model) in a separate tui.json, not opencode.json — a second
			// merge-json destination for the same defaults.yaml source.
			{
				From:     rules.FromSpec{"defaults.yaml"},
				To:       "tui.json",
				Strategy: rules.StrategyRender,
				Surface:  rules.SurfaceDefaults,
				Merge:    rules.StrategyMergeJSON,
			},
			// OpenCode has no declarative hook file — hooks are TS plugins that
			// auto-load from plugins/ and block by throwing in tool.execute.before.
			// Drop the store's guard plugin (it shells out to the shared
			// git-guard.sh) there. confirm_hook gates the copy: the plugin runs
			// the moment it lands, so friday never installs it unattended.
			// https://opencode.ai/docs/plugins/
			{From: rules.FromSpec{"hooks/opencode/git-guard.ts"}, To: "plugins/git-guard.ts", ConfirmHook: true},
			// Escape hatch for whatever this preset does not model — never
			// theme/keybinds/tui here (those live in tui.json), but covers
			// anything else a vendor ships next. native/opencode/**/* lands
			// verbatim at ~/.config/opencode/.
			// confirm_hook: plugins/ auto-loads, so a mirrored file can be
			// running code — see the mirror paragraph in the package comment.
			nativeMirror("opencode"),
			// Arbitrary additional opencode.json keys this preset does not
			// canonicalize (mcp.yaml/permissions.yaml/defaults.yaml cover
			// their own keys in this same file; theme lives in tui.json).
			{
				From:     rules.FromSpec{"settings/opencode.json"},
				To:       "opencode.json",
				Strategy: rules.StrategyMergeJSON,
			},
		},
		// OpenCode reads AGENTS.md at the repo root and discovers project
		// skills and agents under ./.opencode/. https://opencode.ai/docs/skills/
		ProjectTarget: ".",
		ProjectRules: []*rules.Rule{
			{
				From:     entryPlus("rules/*.md"),
				To:       "AGENTS.md",
				Strategy: rules.StrategyConcatenate,
			},
			{
				From:             rules.FromSpec{"skills/**/*"},
				To:               ".opencode/skills/{relpath}",
				FrontmatterStrip: []string{"when_to_use", "allowed-tools"},
			},
			{
				From:             rules.FromSpec{"agents/*.md"},
				To:               ".opencode/agents/{filename}",
				FrontmatterStrip: []string{"name", "tools", "model"},
			},
			{From: rules.FromSpec{"commands/*.md"}, To: ".opencode/commands/{filename}"},
		},
	},
	"copilot": {
		Name: "copilot",
		// Copilot CLI reads ~/.copilot/copilot-instructions.md and custom
		// agents as ~/.copilot/agents/*.agent.md (name/description in
		// frontmatter; Claude's tools comma-list and model sentinels don't
		// translate, so they're stripped). Skills come from the shared
		// "agents" adapter (~/.agents/skills), which Copilot also discovers
		// natively — ~/.copilot/skills/ is no longer written.
		// https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-custom-instructions
		// https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-skills
		// https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/create-custom-agents-for-cli
		Target: ".copilot",
		Rules: []*rules.Rule{
			{
				From:     entryPlus("rules/*.md"),
				To:       "copilot-instructions.md",
				Strategy: rules.StrategyConcatenate,
			},
			{
				From:             rules.FromSpec{"agents/*.md"},
				To:               "agents/{stem}.agent.md",
				FrontmatterStrip: []string{"tools", "model"},
			},
			// MCP servers land in mcp-config.json. friday's permission profile
			// covers only the net axis (Copilot CLI has no per-path/per-command
			// permission surface) and lands in settings.json's
			// allowedUrls/deniedUrls — never permissions-config.json, which is a
			// record of interactive user consent, not something friday may
			// synthesise.
			{
				From:     rules.FromSpec{"mcp.yaml"},
				To:       "mcp-config.json",
				Strategy: rules.StrategyRender,
				Surface:  rules.SurfaceMCP,
				Merge:    rules.StrategyMergeJSON,
			},
			{
				From:     rules.FromSpec{"permissions.yaml"},
				To:       "settings.json",
				Strategy: rules.StrategyRender,
				Surface:  rules.SurfacePermissions,
				Merge:    rules.StrategyMergeJSON,
			},
			// friday's model/theme/statusline defaults land in the same
			// settings.json the permissions rule above already owns.
			// telemetry has no user-scope knob on Copilot and is reported
			// rather than written (see render.Defaults).
			{
				From:     rules.FromSpec{"defaults.yaml"},
				To:       "settings.json",
				Strategy: rules.StrategyRender,
				Surface:  rules.SurfaceDefaults,
				Merge:    rules.StrategyMergeJSON,
			},
			// Copilot CLI loads every ~/.copilot/hooks/*.json alphabetically,
			// so friday writes its own dedicated file (no co-ownership). Copilot
			// denies differently from Claude/Codex: a `preToolUse` hook returns
			// {"permissionDecision":"deny",...} on stdout and exits 0 (a non-zero
			// exit there is only a warning), so the shared guard runs in
			// copilot-json mode. The store source must carry "version": 1 and
			// camelCase event names (sessionStart, preToolUse, ...).
			// https://docs.github.com/en/copilot/reference/hooks-reference
			{
				From:     rules.FromSpec{"hooks/copilot/hooks.json"},
				To:       "hooks/friday-git-guard.json",
				Strategy: rules.StrategyMergeJSON,
				Replace:  map[string]string{pluginRootMarker: "$HOME/.friday"},
			},
			// Escape hatch for whatever this preset does not model — theme
			// palettes, or anything a vendor ships next. native/copilot/**/*
			// lands verbatim at ~/.copilot/.
			// confirm_hook: a mirrored hooks/*.json or settings.json can carry
			// hook commands — see the mirror paragraph in the package comment.
			nativeMirror("copilot"),
			// Arbitrary additional settings.json keys this preset does not
			// canonicalize (permissions.yaml covers only allowedUrls/
			// deniedUrls, defaults.yaml covers model/effortLevel/theme/
			// statusLine, in this same file).
			{
				From:     rules.FromSpec{"settings/copilot.json"},
				To:       "settings.json",
				Strategy: rules.StrategyMergeJSON,
			},
		},
		// Project scope: .github/copilot-instructions.md, .github/skills/,
		// and repo-level custom agents in .github/agents/*.agent.md.
		// https://docs.github.com/en/copilot/how-tos/configure-custom-instructions/add-repository-instructions
		ProjectTarget: ".",
		ProjectRules: []*rules.Rule{
			{
				From:     entryPlus("rules/*.md"),
				To:       ".github/copilot-instructions.md",
				Strategy: rules.StrategyConcatenate,
			},
			{From: rules.FromSpec{"skills/**/*"}, To: ".github/skills/{relpath}"},
			{
				From:             rules.FromSpec{"agents/*.md"},
				To:               ".github/agents/{stem}.agent.md",
				FrontmatterStrip: []string{"tools", "model"},
			},
		},
	},
	"gemini": {
		Name: "gemini",
		// Gemini CLI reads ~/.gemini/GEMINI.md by default; AGENTS.md is only
		// honoured when the user's own settings.json opts in via
		// context.fileName, so GEMINI.md (not AGENTS.md) is the correct
		// target here.
		// https://github.com/google-gemini/gemini-cli/blob/main/docs/cli/gemini-md.md
		Target: ".gemini",
		Rules: []*rules.Rule{
			{
				From:     entryPlus("rules/*.md"),
				To:       "GEMINI.md",
				Strategy: rules.StrategyConcatenate,
			},
			{
				From:             rules.FromSpec{"agents/*.md"},
				To:               "agents/{filename}",
				FrontmatterStrip: []string{"tools"},
			},
			// Gemini custom commands are TOML at ~/.gemini/commands/<name>.toml
			// ({prompt, description}); a subdirectory becomes a ":"-namespaced
			// command. Render the Claude-shaped commands/*.md into that shape.
			{
				From:     rules.FromSpec{"commands/*.md"},
				To:       "commands/{stem}.toml",
				Strategy: rules.StrategyRender,
				Surface:  rules.SurfaceGeminiCommand,
				Merge:    rules.StrategyCopy,
			},
			{
				From:     rules.FromSpec{"mcp.yaml"},
				To:       "settings.json",
				Strategy: rules.StrategyRender,
				Surface:  rules.SurfaceMCP,
				Merge:    rules.StrategyMergeJSON,
			},
			// friday's model/theme/telemetry defaults land in the same
			// settings.json the MCP rule above already owns. effort and
			// statusline have no equivalent here (see render.Defaults).
			{
				From:     rules.FromSpec{"defaults.yaml"},
				To:       "settings.json",
				Strategy: rules.StrategyRender,
				Surface:  rules.SurfaceDefaults,
				Merge:    rules.StrategyMergeJSON,
			},
			// policies/ is a directory of independent policy files gemini-cli
			// loads and combines on its own, so friday owns friday.toml
			// outright (copy) rather than merging into a user's own policy.
			{
				From:     rules.FromSpec{"permissions.yaml"},
				To:       "policies/friday.toml",
				Strategy: rules.StrategyRender,
				Surface:  rules.SurfacePermissions,
				Merge:    rules.StrategyCopy,
			},
			// Gemini's hooks live inside settings.json's own "hooks" key, not a
			// separate file (unlike Claude/Codex/Copilot).
			{
				From:     rules.FromSpec{"hooks/gemini/hooks.json"},
				To:       "settings.json",
				Strategy: rules.StrategyMergeJSON,
				Replace:  map[string]string{pluginRootMarker: "$HOME/.friday"},
			},
			// Escape hatch for whatever this preset does not model — theme
			// palettes, or anything a vendor ships next. native/gemini/**/*
			// lands verbatim at ~/.gemini/.
			// confirm_hook: a mirrored settings.json can carry hook commands —
			// see the mirror paragraph in the package comment.
			nativeMirror("gemini"),
			// Arbitrary additional settings.json keys this preset does not
			// canonicalize (mcp.yaml/defaults.yaml/hooks cover their own
			// keys in this file).
			{
				From:     rules.FromSpec{"settings/gemini.json"},
				To:       "settings.json",
				Strategy: rules.StrategyMergeJSON,
			},
		},
		// Gemini CLI searches workspace directories and their parents for
		// GEMINI.md, exactly like the user-scope file.
		// https://github.com/google-gemini/gemini-cli/blob/main/docs/cli/gemini-md.md
		ProjectTarget: ".",
		ProjectRules: []*rules.Rule{
			{
				From:     entryPlus("rules/*.md"),
				To:       "GEMINI.md",
				Strategy: rules.StrategyConcatenate,
			},
		},
	},
	"oh-my-pi": {
		Name: "oh-my-pi",
		// oh-my-pi (github.com/can1357/oh-my-pi, binary `omp`) is a
		// maintained fork of pi-mono. Its native config lives under
		// ~/.omp/agent/ (relocatable via its own PI_CODING_AGENT_DIR),
		// the highest-priority discovery source for its own context
		// files, subagents, commands, skills, and extensions alike.
		// https://github.com/can1357/oh-my-pi
		Target: ".omp/agent",
		Rules: []*rules.Rule{
			{
				From:     entryPlus("rules/*.md"),
				To:       "AGENTS.md",
				Strategy: rules.StrategyConcatenate,
			},
			// Native subagents: same frontmatter contract as Claude's
			// (name/description/model/tools) — no stripping needed.
			{From: rules.FromSpec{"agents/*.md"}, To: "agents/{filename}"},
			// Native slash commands: $1/$ARGUMENTS templating, the same
			// dialect as Claude/OpenCode's commands/*.md.
			{From: rules.FromSpec{"commands/*.md"}, To: "commands/{filename}"},
			// Native skills discovery outranks the shared ~/.agents/skills
			// path (highest provider priority), so oh-my-pi gets its own
			// rule here instead of joining the "agents" adapter above.
			{From: rules.FromSpec{"skills/**/*"}, To: "skills/{relpath}"},
			// mcp.json's shape (a "type": stdio/http/sse discriminant,
			// command/args/env or url/headers) is the same family as
			// Claude/Copilot's — reuses mcpEntryClaudeLike/
			// mcpServerFromClaudeLike with no per-adapter render code.
			{
				From:     rules.FromSpec{"mcp.yaml"},
				To:       "mcp.json",
				Strategy: rules.StrategyRender,
				Surface:  rules.SurfaceMCP,
				Merge:    rules.StrategyMergeJSON,
			},
			// oh-my-pi calls hooks "extensions": a TS module that default-
			// exports a factory registering pi.on("tool_call", handler),
			// which returns {block, reason} to deny a call. Drop the
			// store's guard extension (it shells out to the shared
			// git-guard.sh) there. confirm_hook gates the copy: it runs
			// the moment it lands.
			// https://github.com/can1357/oh-my-pi (docs/extensions.md)
			{From: rules.FromSpec{"hooks/oh-my-pi/git-guard.ts"}, To: "extensions/git-guard.ts", ConfirmHook: true},
			// Escape hatch for whatever this preset does not model —
			// RULES.md (a sticky always-apply rule file, a concept
			// friday's own rules/*.md concatenation does not have a
			// native equivalent for), keybindings, themes, or anything a
			// future oh-my-pi version ships. native/oh-my-pi/**/* lands
			// verbatim at ~/.omp/agent/.
			// confirm_hook: extensions/ auto-loads, so a mirrored file can be
			// running code — see the mirror paragraph in the package comment.
			nativeMirror("oh-my-pi"),
			// permissions.yaml/defaults.yaml: no rule for either. Both
			// would target config.yml, oh-my-pi's own settings file —
			// but config.yml is YAML, and friday's merge-json/merge-toml
			// co-owned-merge strategies have no YAML analog yet. A rule
			// that could only ever report missing-source is noise, not a
			// feature; tracked as a follow-up in ROADMAP.md rather than
			// built here.
		},
		// Project scope: the nearest-non-empty-ancestor .omp/AGENTS.md
		// (not a bare root AGENTS.md — that is the lower-priority
		// agents-md provider), plus .omp/agents, .omp/commands, and
		// .omp/skills. No project-scope hooks/extensions rule: claude is the
		// one preset that wires hooks at project scope (via
		// ${CLAUDE_PROJECT_DIR}), and oh-my-pi has no documented
		// project-scope extension path to wire them into.
		ProjectTarget: ".omp",
		ProjectRules: []*rules.Rule{
			{
				From:     entryPlus("rules/*.md"),
				To:       "AGENTS.md",
				Strategy: rules.StrategyConcatenate,
			},
			{From: rules.FromSpec{"agents/*.md"}, To: "agents/{filename}"},
			{From: rules.FromSpec{"commands/*.md"}, To: "commands/{filename}"},
			{From: rules.FromSpec{"skills/**/*"}, To: "skills/{relpath}"},
		},
	},
}

// Get returns the preset with the given name (or false).
func Get(name string) (Preset, bool) {
	p, ok := registry[name]
	if !ok {
		return Preset{}, false
	}
	// Return a deep copy of rules so callers can't mutate the registry.
	clone := p
	clone.Rules = cloneRules(p.Rules)
	clone.ProjectRules = cloneRules(p.ProjectRules)
	return clone, true
}

func cloneRules(rs []*rules.Rule) []*rules.Rule {
	if rs == nil {
		return nil
	}
	out := make([]*rules.Rule, len(rs))
	for i, r := range rs {
		c := *r
		c.From = slices.Clone(r.From)
		c.FrontmatterStrip = slices.Clone(r.FrontmatterStrip)
		c.Replace = maps.Clone(r.Replace)
		if c.Replace == nil {
			c.Replace = maps.Clone(storeReplace)
		}
		out[i] = &c
	}
	return out
}

// Names returns all known preset names in alphabetical order.
func Names() []string {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// AllAdapters returns every built-in preset, deep-copied and keyed by name.
// Used when friday.yaml is absent — friday falls back to the full preset set
// so repos can be pure md content with no manifest.
func AllAdapters() map[string]Preset {
	out := make(map[string]Preset, len(registry))
	for n := range registry {
		out[n], _ = Get(n)
	}
	return out
}
