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
}

func (p Preset) Adapter() *config.Adapter {
	return &config.Adapter{Target: p.Target, Rules: p.Rules}
}

var registry = map[string]Preset{
	"claude": {
		Name:   "claude",
		Target: ".claude",
		Rules: []*rules.Rule{
			{
				From:     rules.FromSpec{"identity.md", "rules/*.md"},
				To:       "CLAUDE.md",
				Strategy: rules.StrategyConcatenate,
			},
			{From: rules.FromSpec{"agents/*.md"}, To: "agents/{filename}"},
			{From: rules.FromSpec{"commands/*.md"}, To: "commands/{filename}"},
			{From: rules.FromSpec{"skills/**/*"}, To: "skills/{relpath}"},
		},
	},
	// Cursor user-level rules live inside Cursor's settings UI, not on the
	// filesystem (see https://cursor.com/docs/rules). A user-level preset has
	// nothing to write that Cursor would read; the project-level pattern
	// (.cursor/rules/*.mdc) belongs to project scope, which friday does not
	// support yet. Once Cursor ships filesystem-backed global rules, or
	// friday gains project scope, this preset can come back.
	"codex": {
		Name: "codex",
		// Codex CLI reads ~/.codex/AGENTS.md (and AGENTS.override.md if present).
		// https://developers.openai.com/codex/guides/agents-md
		Target: ".codex",
		Rules: []*rules.Rule{
			{
				From:     rules.FromSpec{"identity.md", "rules/*.md"},
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
			{From: rules.FromSpec{"identity.md"}, To: "AGENTS.md"},
			{From: rules.FromSpec{"rules/*.md"}, To: "rules/{filename}"},
			{
				From:             rules.FromSpec{"skills/**/*"},
				To:               "skills/{relpath}",
				FrontmatterStrip: []string{"when_to_use", "allowed-tools"},
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
				From:     rules.FromSpec{"identity.md", "rules/*.md"},
				To:       "copilot-instructions.md",
				Strategy: rules.StrategyConcatenate,
			},
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
	clone.Rules = make([]*rules.Rule, len(p.Rules))
	for i, r := range p.Rules {
		c := *r
		c.From = append(rules.FromSpec(nil), r.From...)
		c.FrontmatterStrip = append([]string(nil), r.FrontmatterStrip...)
		clone.Rules[i] = &c
	}
	return clone, true
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
