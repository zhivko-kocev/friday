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
// for it (paths verified against each harness's docs, July 2026):
//
//	store        claude        codex     copilot            opencode   antigravity                 pi
//	core+rules   CLAUDE.md     AGENTS.md copilot-instr..md  AGENTS.md  GEMINI.md                   AGENTS.md
//	agents/      agents/       —         agents/*.agent.md  agents/    —                           —
//	commands/    commands/     prompts/  —                  commands/  antigravity/global_workfl.  prompts/
//	skills/      skills/       skills/   skills/            skills/    —                           skills/
//	standards/   ✓             ✓         ✓                  ✓          ✓                           ✓
//	connectors/  ✓             ✓         ✓                  ✓          ✓                           ✓
//	hooks/       settings.json hooks.json hooks/*.json       —          config/hooks.json           —
//
// standards/ and connectors/ have no native discovery mechanism anywhere, so
// they land as reference copies in each agent's config home; the live copies
// referenced by skill bodies stay in ~/.friday.
//
// "—" means friday maps nothing there yet — NOT that the agent lacks the
// surface. Since these were first written, Codex, Copilot, and Antigravity
// have all gained file-based hook surfaces (and Codex/Antigravity skills and
// subagents); those cells are being wired progressively — see ROADMAP. Only
// OpenCode and pi hooks stay unmappable here, being imperative TS plugins
// rather than a declarative file.
//
// hooks/ is not a plain reference copy: an agent activates only the hooks its
// config declares (Claude Code, e.g., ignores a loose ~/.claude/hooks/hooks.json
// and reads only settings.json), so the claude preset merges the store's
// hooks.json into settings.json's `hooks` key (merge-json strategy) rather than
// dropping an inert copy the user must wire up by hand.

var registry = map[string]Preset{
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
			{From: rules.FromSpec{"standards/*.md"}, To: "standards/{filename}"},
			{From: rules.FromSpec{"connectors/*.md"}, To: "connectors/{filename}"},
			// Claude Code only activates hooks declared in settings.json — a
			// loose ~/.claude/hooks/hooks.json is never loaded — so merge the
			// store's hooks.json into settings.json's `hooks` key. The scripts
			// run from the store in place; the command paths rewrite the plugin
			// marker to $HOME/.friday (not ~/.friday: a hook command runs in a
			// shell that expands $HOME but need not expand ~). merge-json is
			// push-only and drift-exempt so the user's other settings survive.
			{
				From:     rules.FromSpec{"hooks/hooks.json"},
				To:       "settings.json",
				Strategy: rules.StrategyMergeJSON,
				Replace:  map[string]string{pluginRootMarker: "$HOME/.friday"},
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
			{From: rules.FromSpec{"standards/*.md"}, To: ".claude/standards/{filename}"},
			{From: rules.FromSpec{"connectors/*.md"}, To: ".claude/connectors/{filename}"},
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
		// Codex CLI reads ~/.codex/AGENTS.md, discovers Agent-Skills-standard
		// skills under ~/.codex/skills/**/SKILL.md, and scans top-level
		// markdown in ~/.codex/prompts/ as custom prompts (deprecated in
		// favor of skills, still supported).
		// https://developers.openai.com/codex/guides/agents-md
		// https://developers.openai.com/codex/custom-prompts
		Target: ".codex",
		Rules: []*rules.Rule{
			{
				From:     entryPlus("rules/*.md"),
				To:       "AGENTS.md",
				Strategy: rules.StrategyConcatenate,
			},
			{From: rules.FromSpec{"skills/**/*"}, To: "skills/{relpath}"},
			{From: rules.FromSpec{"commands/*.md"}, To: "prompts/{filename}"},
			{From: rules.FromSpec{"standards/*.md"}, To: "standards/{filename}"},
			{From: rules.FromSpec{"connectors/*.md"}, To: "connectors/{filename}"},
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
			{From: entryPlus(), To: "AGENTS.md"},
			{From: rules.FromSpec{"rules/*.md"}, To: "rules/{filename}"},
			{
				From:             rules.FromSpec{"skills/**/*"},
				To:               "skills/{relpath}",
				FrontmatterStrip: []string{"when_to_use", "allowed-tools"},
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
			{From: rules.FromSpec{"standards/*.md"}, To: "standards/{filename}"},
			{From: rules.FromSpec{"connectors/*.md"}, To: "connectors/{filename}"},
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
			{From: rules.FromSpec{"standards/*.md"}, To: ".opencode/standards/{filename}"},
			{From: rules.FromSpec{"connectors/*.md"}, To: ".opencode/connectors/{filename}"},
		},
	},
	"copilot": {
		Name: "copilot",
		// Copilot CLI reads ~/.copilot/copilot-instructions.md, discovers
		// Agent-Skills-standard skills in ~/.copilot/skills/, and custom
		// agents as ~/.copilot/agents/*.agent.md (name/description in
		// frontmatter; Claude's tools comma-list and model sentinels don't
		// translate, so they're stripped).
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
			{From: rules.FromSpec{"skills/**/*"}, To: "skills/{relpath}"},
			{
				From:             rules.FromSpec{"agents/*.md"},
				To:               "agents/{stem}.agent.md",
				FrontmatterStrip: []string{"tools", "model"},
			},
			{From: rules.FromSpec{"standards/*.md"}, To: "standards/{filename}"},
			{From: rules.FromSpec{"connectors/*.md"}, To: "connectors/{filename}"},
			// Copilot CLI loads every ~/.copilot/hooks/*.json alphabetically,
			// so friday writes its own dedicated file (no co-ownership). Copilot
			// denies differently from Claude/Codex: a `preToolUse` hook returns
			// {"permissionDecision":"deny",...} on stdout and exits 0 (a non-zero
			// exit there is only a warning), so the shared guard runs in
			// copilot-json mode. NOTE: the tool matcher ("bash") follows Copilot's
			// docs; verify on a live install (see ROADMAP).
			// https://docs.github.com/en/copilot/reference/hooks-reference
			{
				From:     rules.FromSpec{"hooks/copilot/hooks.json"},
				To:       "hooks/friday-git-guard.json",
				Strategy: rules.StrategyMergeJSON,
				Replace:  map[string]string{pluginRootMarker: "$HOME/.friday"},
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
	"antigravity": {
		Name: "antigravity",
		// Google Antigravity reads global rules from ~/.gemini/GEMINI.md
		// (and, since v1.20.3, the cross-tool ~/.gemini/AGENTS.md, applied
		// after GEMINI.md). Global workflows live under
		// ~/.gemini/antigravity/global_workflows/; workspace rules in
		// .agent/rules/, workspace workflows in .agent/workflows/, and a
		// root AGENTS.md is read by every agent in the workspace.
		// https://codelabs.developers.google.com/getting-started-google-antigravity
		Target: ".gemini",
		Rules: []*rules.Rule{
			{
				From:     entryPlus("rules/*.md"),
				To:       "GEMINI.md",
				Strategy: rules.StrategyConcatenate,
			},
			{From: rules.FromSpec{"commands/*.md"}, To: "antigravity/global_workflows/{filename}"},
			{From: rules.FromSpec{"standards/*.md"}, To: "standards/{filename}"},
			{From: rules.FromSpec{"connectors/*.md"}, To: "connectors/{filename}"},
			// Antigravity reads global hooks from ~/.gemini/config/hooks.json.
			// Its wrapper is a user-named group → event → matcher+hooks[], and it
			// denies via {"decision":"deny","reason":...} on stdout with exit 0
			// (NOT exit 2), so the shared guard runs in antigravity-json mode.
			// LOW CONFIDENCE: Antigravity's docs are a client-rendered SPA, so this
			// dialect (paths, the `decision` field, and especially the
			// absolute-path command requirement vs. $HOME shell-expansion) is
			// corroborated only from secondary sources — verify on a live install
			// before relying on it (see ROADMAP).
			{
				From:     rules.FromSpec{"hooks/antigravity/hooks.json"},
				To:       "config/hooks.json",
				Strategy: rules.StrategyMergeJSON,
				Replace:  map[string]string{pluginRootMarker: "$HOME/.friday"},
			},
		},
		ProjectTarget: ".",
		ProjectRules: []*rules.Rule{
			{
				From:     entryPlus("rules/*.md"),
				To:       "AGENTS.md",
				Strategy: rules.StrategyConcatenate,
			},
			{From: rules.FromSpec{"commands/*.md"}, To: ".agent/workflows/{filename}"},
		},
	},
	"pi": {
		Name: "pi",
		// Pi (badlogic/pi-mono) loads the global AGENTS.md from
		// ~/.pi/agent/AGENTS.md (CLAUDE.md also accepted) and global skills
		// from ~/.pi/agent/skills/ following the Agent Skills standard —
		// Claude-shaped SKILL.md files work as-is.
		// https://github.com/badlogic/pi-mono/tree/main/packages/coding-agent
		Target: ".pi/agent",
		Rules: []*rules.Rule{
			{
				From:     entryPlus("rules/*.md"),
				To:       "AGENTS.md",
				Strategy: rules.StrategyConcatenate,
			},
			{From: rules.FromSpec{"skills/**/*"}, To: "skills/{relpath}"},
			// Pi prompt templates are markdown files invoked as /name —
			// the closest surface to Claude slash commands.
			{From: rules.FromSpec{"commands/*.md"}, To: "prompts/{filename}"},
			{From: rules.FromSpec{"standards/*.md"}, To: "standards/{filename}"},
			{From: rules.FromSpec{"connectors/*.md"}, To: "connectors/{filename}"},
		},
		// Project scope: root AGENTS.md (loaded cwd-up) + .pi/skills/ +
		// .pi/prompts/.
		ProjectTarget: ".",
		ProjectRules: []*rules.Rule{
			{
				From:     entryPlus("rules/*.md"),
				To:       "AGENTS.md",
				Strategy: rules.StrategyConcatenate,
			},
			{From: rules.FromSpec{"skills/**/*"}, To: ".pi/skills/{relpath}"},
			{From: rules.FromSpec{"commands/*.md"}, To: ".pi/prompts/{filename}"},
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
