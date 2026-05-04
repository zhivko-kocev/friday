// Package presets ships built-in adapter rule sets so users don't have to
// hand-write friday.yaml for the common agents.
//
// Each preset mirrors the layout that the corresponding agent expects.
// `friday init --adapters claude` writes the claude preset into friday.yaml;
// `friday add cursor` appends the cursor preset to an existing friday.yaml.
package presets

import (
	"fmt"
	"sort"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/rules"
)

type Preset struct {
	Name    string
	Target  string
	Rules   []*rules.Rule
	Comment string // shown by `friday add` to explain what was added
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
		Comment: "Claude Code: identity+rules concatenated into CLAUDE.md, plus agents/commands/skills mirrored",
	},
	"cursor": {
		Name:   "cursor",
		Target: ".cursor",
		Rules: []*rules.Rule{
			{From: rules.FromSpec{"identity.md"}, To: "rules/_identity.md"},
			{From: rules.FromSpec{"rules/*.md"}, To: "rules/{filename}"},
		},
		Comment: "Cursor: identity + rules each as standalone .md files under .cursor/rules/",
	},
	"opencode": {
		Name:   "opencode",
		Target: ".opencode",
		Rules: []*rules.Rule{
			{From: rules.FromSpec{"identity.md"}, To: "AGENTS.md"},
			{From: rules.FromSpec{"rules/*.md"}, To: "rules/{filename}"},
			{
				From:             rules.FromSpec{"skills/**/*"},
				To:               "skills/{relpath}",
				FrontmatterStrip: []string{"when_to_use", "allowed-tools"},
			},
		},
		Comment: "OpenCode: identity → AGENTS.md, rules mirrored, skills with Claude-specific frontmatter stripped",
	},
	"copilot": {
		Name:   "copilot",
		Target: ".github",
		Rules: []*rules.Rule{
			{
				From:     rules.FromSpec{"identity.md", "rules/*.md"},
				To:       "copilot-instructions.md",
				Strategy: rules.StrategyConcatenate,
			},
		},
		Comment: "GitHub Copilot: identity+rules concatenated into .github/copilot-instructions.md",
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

// Resolve looks up a preset by name and errors with a useful message if missing.
func Resolve(name string) (Preset, error) {
	p, ok := Get(name)
	if !ok {
		return Preset{}, fmt.Errorf("unknown preset %q (available: %v)", name, Names())
	}
	return p, nil
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
