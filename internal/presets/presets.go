// Package presets ships built-in adapter rule sets so users don't have to
// hand-write friday.yaml for the common agents.
//
// Each preset mirrors the layout that the corresponding agent expects.
// `friday init` seeds friday.yaml with all built-in presets; to disable one,
// delete its entry from friday.yaml.
package presets

import (
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

// entryFiles are the store entry-file variants, most-preferred first: core.md
// is the canonical name, core/core.md matches knowledge repos that nest it
// (e.g. developer-os), identity.md is the legacy pre-0.0.5 name. Absent
// variants expand to nothing, so listing all three costs nothing; `friday
// doctor` warns when a store carries more than one.
var entryFiles = rules.FromSpec{"core.md", "core/core.md", "identity.md"}

// entryPlus returns a fresh from-list of the entry-file variants followed by
// any extra patterns.
func entryPlus(more ...string) rules.FromSpec {
	return append(append(rules.FromSpec{}, entryFiles...), more...)
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
// a friday-free knowledge repo.
var storeReplace = map[string]string{pluginRootMarker: "~/.friday"}

// The presets map every store directory into every agent that has a
// documented place for it (paths verified against each harness's docs,
// July 2026):
//
//	store        claude     codex     copilot            opencode   windsurf           antigravity                 pi
//	core+rules   CLAUDE.md  AGENTS.md copilot-instr..md  AGENTS.md  memories/global..  GEMINI.md                   AGENTS.md
//	agents/      agents/    —         agents/*.agent.md  agents/    —                  —                           —
//	commands/    commands/  prompts/  —                  commands/  global_workflows/  antigravity/global_workfl.  prompts/
//	skills/      skills/    skills/   skills/            skills/    —                  —                           skills/
//	standards/   ✓          ✓         ✓                  ✓          ✓                  ✓                           ✓
//	connectors/  ✓          ✓         ✓                  ✓          ✓                  ✓                           ✓
//	hooks/       hooks/     —         —                  —          —                  —                           —
//
// standards/ and connectors/ have no native discovery mechanism anywhere, so
// they land as reference copies in each agent's config home; the live copies
// referenced by skill bodies stay in ~/.friday. "—" means the harness has no
// documented surface for that content.

var registry = map[string]Preset{
	"claude": {
		Name:   "claude",
		Target: ".claude",
		Rules: []*rules.Rule{
			{
				From:     entryPlus("rules/*.md"),
				To:       "CLAUDE.md",
				Strategy: rules.StrategyConcatenate,
				Replace:  storeReplace,
			},
			{From: rules.FromSpec{"agents/*.md"}, To: "agents/{filename}", Replace: storeReplace},
			{From: rules.FromSpec{"commands/*.md"}, To: "commands/{filename}", Replace: storeReplace},
			{From: rules.FromSpec{"skills/**/*"}, To: "skills/{relpath}", Replace: storeReplace},
			{From: rules.FromSpec{"standards/*.md"}, To: "standards/{filename}", Replace: storeReplace},
			{From: rules.FromSpec{"connectors/*.md"}, To: "connectors/{filename}", Replace: storeReplace},
			// Reference copy: Claude Code only auto-loads plugin hooks; wire
			// entries into ~/.claude/settings.json by hand (doctor explains).
			{From: rules.FromSpec{"hooks/**/*"}, To: "hooks/{relpath}", Replace: storeReplace},
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
				Replace:  storeReplace,
			},
			{From: rules.FromSpec{"agents/*.md"}, To: ".claude/agents/{filename}", Replace: storeReplace},
			{From: rules.FromSpec{"commands/*.md"}, To: ".claude/commands/{filename}", Replace: storeReplace},
			{From: rules.FromSpec{"skills/**/*"}, To: ".claude/skills/{relpath}", Replace: storeReplace},
			{From: rules.FromSpec{"standards/*.md"}, To: ".claude/standards/{filename}", Replace: storeReplace},
			{From: rules.FromSpec{"connectors/*.md"}, To: ".claude/connectors/{filename}", Replace: storeReplace},
			{From: rules.FromSpec{"hooks/**/*"}, To: ".claude/hooks/{relpath}", Replace: storeReplace},
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
	//   - Codeium (non-Windsurf) has no filesystem instruction path.
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
				Replace:  storeReplace,
			},
			{From: rules.FromSpec{"skills/**/*"}, To: "skills/{relpath}", Replace: storeReplace},
			{From: rules.FromSpec{"commands/*.md"}, To: "prompts/{filename}", Replace: storeReplace},
			{From: rules.FromSpec{"standards/*.md"}, To: "standards/{filename}", Replace: storeReplace},
			{From: rules.FromSpec{"connectors/*.md"}, To: "connectors/{filename}", Replace: storeReplace},
		},
		// Codex reads AGENTS.md at the repo root at project scope.
		// https://developers.openai.com/codex/guides/agents-md
		ProjectTarget: ".",
		ProjectRules: []*rules.Rule{
			{
				From:     entryPlus("rules/*.md"),
				To:       "AGENTS.md",
				Strategy: rules.StrategyConcatenate,
				Replace:  storeReplace,
			},
		},
	},
	"opencode": {
		Name: "opencode",
		// OpenCode follows XDG: global config at $HOME/.config/opencode.
		Target: ".config/opencode",
		Rules: []*rules.Rule{
			{From: entryPlus(), To: "AGENTS.md", Replace: storeReplace},
			{From: rules.FromSpec{"rules/*.md"}, To: "rules/{filename}", Replace: storeReplace},
			{
				From:             rules.FromSpec{"skills/**/*"},
				To:               "skills/{relpath}",
				FrontmatterStrip: []string{"when_to_use", "allowed-tools"},
				Replace:          storeReplace,
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
				Replace:          storeReplace,
			},
			// Custom commands: markdown with $ARGUMENTS, same dialect as
			// Claude's. https://opencode.ai/docs/commands/
			{From: rules.FromSpec{"commands/*.md"}, To: "commands/{filename}", Replace: storeReplace},
			{From: rules.FromSpec{"standards/*.md"}, To: "standards/{filename}", Replace: storeReplace},
			{From: rules.FromSpec{"connectors/*.md"}, To: "connectors/{filename}", Replace: storeReplace},
		},
		// OpenCode reads AGENTS.md at the repo root and discovers project
		// skills and agents under ./.opencode/. https://opencode.ai/docs/skills/
		ProjectTarget: ".",
		ProjectRules: []*rules.Rule{
			{
				From:     entryPlus("rules/*.md"),
				To:       "AGENTS.md",
				Strategy: rules.StrategyConcatenate,
				Replace:  storeReplace,
			},
			{
				From:             rules.FromSpec{"skills/**/*"},
				To:               ".opencode/skills/{relpath}",
				FrontmatterStrip: []string{"when_to_use", "allowed-tools"},
				Replace:          storeReplace,
			},
			{
				From:             rules.FromSpec{"agents/*.md"},
				To:               ".opencode/agents/{filename}",
				FrontmatterStrip: []string{"name", "tools", "model"},
				Replace:          storeReplace,
			},
			{From: rules.FromSpec{"commands/*.md"}, To: ".opencode/commands/{filename}", Replace: storeReplace},
			{From: rules.FromSpec{"standards/*.md"}, To: ".opencode/standards/{filename}", Replace: storeReplace},
			{From: rules.FromSpec{"connectors/*.md"}, To: ".opencode/connectors/{filename}", Replace: storeReplace},
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
				Replace:  storeReplace,
			},
			{From: rules.FromSpec{"skills/**/*"}, To: "skills/{relpath}", Replace: storeReplace},
			{
				From:             rules.FromSpec{"agents/*.md"},
				To:               "agents/{stem}.agent.md",
				FrontmatterStrip: []string{"tools", "model"},
				Replace:          storeReplace,
			},
			{From: rules.FromSpec{"standards/*.md"}, To: "standards/{filename}", Replace: storeReplace},
			{From: rules.FromSpec{"connectors/*.md"}, To: "connectors/{filename}", Replace: storeReplace},
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
				Replace:  storeReplace,
			},
			{From: rules.FromSpec{"skills/**/*"}, To: ".github/skills/{relpath}", Replace: storeReplace},
			{
				From:             rules.FromSpec{"agents/*.md"},
				To:               ".github/agents/{stem}.agent.md",
				FrontmatterStrip: []string{"tools", "model"},
				Replace:          storeReplace,
			},
		},
	},
	"windsurf": {
		Name: "windsurf",
		// Windsurf (rebranded Devin Desktop by Cognition, June 2026; the
		// docs and paths still say windsurf). Global rules are a single
		// memories/global_rules.md capped at 6000 characters — keep core.md
		// + rules lean. User workflows live in global_workflows/.
		// https://docs.windsurf.com/windsurf/cascade/memories
		// https://docs.windsurf.com/windsurf/cascade/workflows
		Target: ".codeium/windsurf",
		Rules: []*rules.Rule{
			{
				From:     entryPlus("rules/*.md"),
				To:       "memories/global_rules.md",
				Strategy: rules.StrategyConcatenate,
				Replace:  storeReplace,
			},
			{From: rules.FromSpec{"commands/*.md"}, To: "global_workflows/{filename}", Replace: storeReplace},
			{From: rules.FromSpec{"standards/*.md"}, To: "standards/{filename}", Replace: storeReplace},
			{From: rules.FromSpec{"connectors/*.md"}, To: "connectors/{filename}", Replace: storeReplace},
		},
		// At project scope it honors a root AGENTS.md (always-on, no
		// frontmatter) and workspace workflows in .windsurf/workflows/.
		ProjectTarget: ".",
		ProjectRules: []*rules.Rule{
			{
				From:     entryPlus("rules/*.md"),
				To:       "AGENTS.md",
				Strategy: rules.StrategyConcatenate,
				Replace:  storeReplace,
			},
			{From: rules.FromSpec{"commands/*.md"}, To: ".windsurf/workflows/{filename}", Replace: storeReplace},
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
				Replace:  storeReplace,
			},
			{From: rules.FromSpec{"commands/*.md"}, To: "antigravity/global_workflows/{filename}", Replace: storeReplace},
			{From: rules.FromSpec{"standards/*.md"}, To: "standards/{filename}", Replace: storeReplace},
			{From: rules.FromSpec{"connectors/*.md"}, To: "connectors/{filename}", Replace: storeReplace},
		},
		ProjectTarget: ".",
		ProjectRules: []*rules.Rule{
			{
				From:     entryPlus("rules/*.md"),
				To:       "AGENTS.md",
				Strategy: rules.StrategyConcatenate,
				Replace:  storeReplace,
			},
			{From: rules.FromSpec{"commands/*.md"}, To: ".agent/workflows/{filename}", Replace: storeReplace},
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
				Replace:  storeReplace,
			},
			{From: rules.FromSpec{"skills/**/*"}, To: "skills/{relpath}", Replace: storeReplace},
			// Pi prompt templates are markdown files invoked as /name —
			// the closest surface to Claude slash commands.
			{From: rules.FromSpec{"commands/*.md"}, To: "prompts/{filename}", Replace: storeReplace},
			{From: rules.FromSpec{"standards/*.md"}, To: "standards/{filename}", Replace: storeReplace},
			{From: rules.FromSpec{"connectors/*.md"}, To: "connectors/{filename}", Replace: storeReplace},
		},
		// Project scope: root AGENTS.md (loaded cwd-up) + .pi/skills/ +
		// .pi/prompts/.
		ProjectTarget: ".",
		ProjectRules: []*rules.Rule{
			{
				From:     entryPlus("rules/*.md"),
				To:       "AGENTS.md",
				Strategy: rules.StrategyConcatenate,
				Replace:  storeReplace,
			},
			{From: rules.FromSpec{"skills/**/*"}, To: ".pi/skills/{relpath}", Replace: storeReplace},
			{From: rules.FromSpec{"commands/*.md"}, To: ".pi/prompts/{filename}", Replace: storeReplace},
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
		c.From = append(rules.FromSpec(nil), r.From...)
		c.FrontmatterStrip = append([]string(nil), r.FrontmatterStrip...)
		if r.Replace != nil {
			c.Replace = make(map[string]string, len(r.Replace))
			for k, v := range r.Replace {
				c.Replace[k] = v
			}
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

// AllAdapters returns every preset rendered as a config.Adapter, keyed by
// preset name. Used when friday.yaml is absent — friday falls back to the
// full preset set so repos can be pure md content with no manifest.
func AllAdapters() map[string]Preset {
	out := make(map[string]Preset, len(registry))
	for _, n := range Names() {
		p, _ := Get(n)
		out[n] = p
	}
	return out
}
