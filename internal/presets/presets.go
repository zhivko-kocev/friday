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
// exists on a machine running friday and holds every referenced file
// (core/, standards/, hooks/, ...). Presets deliberately do NOT rewrite to
// the adapter's own dir: agent content legitimately mentions paths like
// ~/.claude/..., and pull's textual inverse would corrupt those; ~/.friday
// never occurs naturally in a friday-free knowledge repo. It follows that
// only files agents DISCOVER (skills/, agents/, commands/, the concatenated
// instructions) need copying — everything else is reached by reference.
var storeReplace = map[string]string{pluginRootMarker: "~/.friday"}

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
		// Codex CLI reads ~/.codex/AGENTS.md (and AGENTS.override.md if present).
		// https://developers.openai.com/codex/guides/agents-md
		Target: ".codex",
		Rules: []*rules.Rule{
			{
				From:     entryPlus("rules/*.md"),
				To:       "AGENTS.md",
				Strategy: rules.StrategyConcatenate,
				Replace:  storeReplace,
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
		},
	},
	"copilot": {
		Name: "copilot",
		// Copilot CLI reads ~/.copilot/copilot-instructions.md. VS Code Copilot
		// honors the same path via chat.instructionsFilesLocations.
		// https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-custom-instructions
		Target: ".copilot",
		Rules: []*rules.Rule{
			{
				From:     entryPlus("rules/*.md"),
				To:       "copilot-instructions.md",
				Strategy: rules.StrategyConcatenate,
				Replace:  storeReplace,
			},
		},
		// Copilot reads .github/copilot-instructions.md at project scope.
		// https://docs.github.com/en/copilot/how-tos/configure-custom-instructions/add-repository-instructions
		ProjectTarget: ".",
		ProjectRules: []*rules.Rule{
			{
				From:     entryPlus("rules/*.md"),
				To:       ".github/copilot-instructions.md",
				Strategy: rules.StrategyConcatenate,
				Replace:  storeReplace,
			},
		},
	},
	"windsurf": {
		Name: "windsurf",
		// Windsurf (rebranded Devin Desktop by Cognition, June 2026; the
		// docs and paths still say windsurf) reads user-level rules from a
		// single global_rules.md capped at 6000 characters — keep core.md +
		// rules lean or trim the manifest's from-list.
		// https://docs.windsurf.com/windsurf/cascade/memories
		Target: ".codeium/windsurf/memories",
		Rules: []*rules.Rule{
			{
				From:     entryPlus("rules/*.md"),
				To:       "global_rules.md",
				Strategy: rules.StrategyConcatenate,
				Replace:  storeReplace,
			},
		},
		// At project scope it honors a root AGENTS.md (always-on, no
		// frontmatter); current builds prefer .devin/rules/ for split rule
		// files, but the single AGENTS.md is the cross-tool safe bet.
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
	"antigravity": {
		Name: "antigravity",
		// Google Antigravity reads global rules from ~/.gemini/GEMINI.md
		// (and, since v1.20.3, the cross-tool ~/.gemini/AGENTS.md, applied
		// after GEMINI.md). Workspace rules live in .agent/rules/ but a root
		// AGENTS.md is read by every agent in the workspace.
		// https://codelabs.developers.google.com/getting-started-google-antigravity
		Target: ".gemini",
		Rules: []*rules.Rule{
			{
				From:     entryPlus("rules/*.md"),
				To:       "GEMINI.md",
				Strategy: rules.StrategyConcatenate,
				Replace:  storeReplace,
			},
		},
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
		},
		// Project scope: root AGENTS.md (loaded cwd-up) + .pi/skills/.
		ProjectTarget: ".",
		ProjectRules: []*rules.Rule{
			{
				From:     entryPlus("rules/*.md"),
				To:       "AGENTS.md",
				Strategy: rules.StrategyConcatenate,
				Replace:  storeReplace,
			},
			{From: rules.FromSpec{"skills/**/*"}, To: ".pi/skills/{relpath}", Replace: storeReplace},
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
