// Package rules defines the Rule type and YAML parsing.
//
// A rule maps one or more files in the canonical store to a single target
// path (or, with copy strategy and a glob source, to many target paths via
// token expansion).
package rules

import (
	"errors"
	"fmt"

	"go.yaml.in/yaml/v3"
)

// ErrRetiredStrategy marks a rule naming a strategy that existed in an
// earlier release and has since been replaced. It is distinguishable from a
// typo so config.load can degrade (drop the rule, warn) instead of refusing
// to load a manifest the user never hand-wrote.
var ErrRetiredStrategy = errors.New("retired strategy")

const (
	StrategyCopy        = "copy"
	StrategyConcatenate = "concatenate"
	// StrategyMergeJSON merges the top-level keys of a single source JSON
	// file into a co-owned target JSON file (e.g. the store's hooks.json
	// into ~/.claude/settings.json). It is push-only and exempt from the
	// whole-file drift model — see engine.planMergeJSON.
	StrategyMergeJSON = "merge-json"
	// StrategyMergeTOML is merge-json's TOML analog: it splices the owned
	// leaves of a single source TOML file into a co-owned target TOML file
	// (e.g. settings/codex.toml into ~/.codex/config.toml). Ownership is
	// per-leaf, not per-table, so a user's own sibling key inside a table
	// friday also writes into survives — see engine.Splice for exactly what
	// it preserves and what it may reformat. Named directly by a preset's
	// native passthrough rule and by a render rule's Merge field.
	StrategyMergeTOML = "merge-toml"
	// StrategyRender projects a canonical schema (internal/spec: mcp.yaml,
	// permissions.yaml) or a per-file transform (an agents/*.md record) onto
	// one adapter's native surface via internal/render, then lands the
	// result through Merge. Surface and Merge are both required.
	StrategyRender = "render"

	// StrategyMDToTOML and StrategyMDToJSON are RETIRED. They shipped before
	// v1.0 and every manifest seeded by an older `friday init` still names
	// them, so Normalize must recognise them and fail with ErrRetiredStrategy
	// rather than a bare "unknown strategy" — config.load drops such a rule
	// with a warning instead of bricking every command. `friday migrate`
	// rewrites the manifest to the render equivalents.
	StrategyMDToTOML = "md-to-toml"
	StrategyMDToJSON = "md-to-json"

	// Surface values for a render rule (Rule.Surface).
	SurfaceMCP           = "mcp"            // internal/spec.MCP → each adapter's MCP config
	SurfacePermissions   = "permissions"    // internal/spec.Permissions → each adapter's policy
	SurfaceCodexAgent    = "codex-agent"    // agents/*.md → Codex TOML subagent
	SurfaceGeminiCommand = "gemini-command" // commands/*.md → Gemini custom command
	SurfaceDefaults      = "defaults"       // internal/spec.Defaults → each adapter's settings

	DefaultSeparator = "\n\n---\n\n"
)

// Rule describes a single from→to mapping inside an adapter.
type Rule struct {
	From             FromSpec          `yaml:"from"`
	To               string            `yaml:"to"`
	Strategy         string            `yaml:"strategy,omitempty"`
	Separator        string            `yaml:"separator,omitempty"`
	FrontmatterStrip []string          `yaml:"frontmatter_strip,omitempty"`
	Replace          map[string]string `yaml:"replace,omitempty"`
	// MaxBytes flags outputs larger than the agent can consume (e.g. an agent
	// that caps a concatenated global-rules file at N chars). No built-in
	// preset sets it today, but it stays for hand-written friday.yaml rules:
	// friday still writes the file — the agent's truncation behavior is its
	// own — but every push and status report carries the warning. 0 = no limit.
	MaxBytes int `yaml:"max_bytes,omitempty"`
	// ConfirmHook marks a copy rule whose file the agent can execute the
	// moment it lands: a plugin/extension the agent auto-loads (OpenCode,
	// oh-my-pi), or a native/<adapter>/**/* mirror, which drops arbitrary
	// files — including a settings.json carrying hook commands — verbatim
	// into the agent's own directory. Such a write is confirmed first,
	// exactly like merge-json hook wiring, and skipped outright when there
	// is no confirmer: friday never installs executable code unattended
	// from a store that may be a clone of someone else's repo.
	ConfirmHook bool `yaml:"confirm_hook,omitempty"`
	// Surface names the canonical schema or transform a render rule projects
	// (mcp | permissions | defaults | codex-agent | gemini-command).
	// Required, and only valid, when Strategy is render.
	Surface string `yaml:"surface,omitempty"`
	// Merge names how a render rule's output lands in the target (copy |
	// merge-json | merge-toml). Required, and only valid, when Strategy is
	// render.
	Merge string `yaml:"merge,omitempty"`
}

// FromSpec accepts either a single string or a list of strings in YAML.
type FromSpec []string

func (f *FromSpec) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		var s string
		if err := node.Decode(&s); err != nil {
			return err
		}
		*f = FromSpec{s}
		return nil
	case yaml.SequenceNode:
		var s []string
		if err := node.Decode(&s); err != nil {
			return err
		}
		*f = FromSpec(s)
		return nil
	default:
		return fmt.Errorf("rule.from must be a string or list of strings (line %d)", node.Line)
	}
}

func (f FromSpec) MarshalYAML() (any, error) {
	if len(f) == 1 {
		return f[0], nil
	}
	return []string(f), nil
}

// Normalize validates the rule. It does NOT fill defaults into the rule
// itself — defaults are resolved at use-time via Sep() — so saving the
// config back out doesn't leak a verbose default separator into the YAML.
func (r *Rule) Normalize() error {
	if r.Strategy == "" {
		r.Strategy = StrategyCopy
	}
	switch r.Strategy {
	case StrategyCopy, StrategyConcatenate, StrategyMergeJSON, StrategyMergeTOML, StrategyRender:
	case StrategyMDToTOML:
		return fmt.Errorf("%w %q — replaced by `strategy: render` with `surface: codex-agent`; run `friday migrate` to update friday.yaml", ErrRetiredStrategy, r.Strategy)
	case StrategyMDToJSON:
		return fmt.Errorf("%w %q — replaced by `strategy: render` with `surface: gemini-command`; run `friday migrate` to update friday.yaml", ErrRetiredStrategy, r.Strategy)
	default:
		return fmt.Errorf("unknown strategy %q (must be copy, concatenate, merge-json, merge-toml, or render)", r.Strategy)
	}
	if len(r.From) == 0 {
		return fmt.Errorf("rule.from is required")
	}
	if r.To == "" {
		return fmt.Errorf("rule.to is required")
	}
	if r.Strategy == StrategyConcatenate && hasToken(r.To) {
		return fmt.Errorf("concatenate rule.to %q cannot contain tokens (single output file)", r.To)
	}
	// merge-json and merge-toml each splice exactly one source file into one
	// literal target: globs, tokens, and the text-oriented options are
	// meaningless. engine.planMergeJSON/planMergeTOML read r.From[0] and
	// write r.To verbatim, so anything else is a silent no-op at best and a
	// file literally named "{stem}.toml" at worst.
	if r.Strategy == StrategyMergeJSON || r.Strategy == StrategyMergeTOML {
		if len(r.From) != 1 || HasWildcard(r.From[0]) {
			return fmt.Errorf("%s rule.from must be a single non-glob path", r.Strategy)
		}
		if hasToken(r.To) {
			return fmt.Errorf("%s rule.to %q cannot contain tokens (single output file)", r.Strategy, r.To)
		}
		if len(r.FrontmatterStrip) > 0 {
			return fmt.Errorf("%s does not support frontmatter_strip", r.Strategy)
		}
		if r.Separator != "" {
			return fmt.Errorf("%s does not support separator", r.Strategy)
		}
		if r.MaxBytes != 0 {
			return fmt.Errorf("%s does not support max_bytes", r.Strategy)
		}
	}
	// render projects a canonical schema (or per-file transform) onto one
	// adapter's native surface; the text-oriented options are meaningless the
	// same way they are for merge-json/merge-toml, and surface fixes the
	// shape of an acceptable from/to pair.
	if r.Strategy == StrategyRender {
		if r.Surface == "" {
			return fmt.Errorf("render rule requires surface")
		}
		if r.Merge == "" {
			return fmt.Errorf("render rule requires merge")
		}
		switch r.Merge {
		case StrategyCopy, StrategyMergeJSON, StrategyMergeTOML:
		default:
			return fmt.Errorf("render rule merge %q must be copy, merge-json, or merge-toml", r.Merge)
		}
		switch r.Surface {
		case SurfaceMCP, SurfacePermissions, SurfaceDefaults:
			// One canonical schema file per source — planRender resolves it
			// the same single-winner way merge-json resolves its source.
			if len(r.From) != 1 || HasWildcard(r.From[0]) {
				return fmt.Errorf("surface %q rule.from must be a single non-glob path", r.Surface)
			}
			if hasToken(r.To) {
				return fmt.Errorf("surface %q rule.to %q cannot contain tokens (single output file)", r.Surface, r.To)
			}
		case SurfaceCodexAgent, SurfaceGeminiCommand:
			// Per-file transform: a glob From and a tokenized To are both
			// expected (one output per matched markdown source). Each output is
			// entirely friday's own file (no co-owned keys to preserve), so
			// only a plain write makes sense — planRender's per-file skeleton
			// (planTokenized) never consults Merge, so merge-json/merge-toml
			// here would silently do nothing rather than the user's intent.
			if r.Merge != StrategyCopy {
				return fmt.Errorf("surface %q merge must be copy", r.Surface)
			}
		default:
			return fmt.Errorf("unknown surface %q (must be mcp, permissions, defaults, codex-agent, or gemini-command)", r.Surface)
		}
		if len(r.FrontmatterStrip) > 0 {
			return fmt.Errorf("render does not support frontmatter_strip")
		}
		if r.Separator != "" {
			return fmt.Errorf("render does not support separator")
		}
		if r.MaxBytes != 0 {
			return fmt.Errorf("render does not support max_bytes")
		}
	} else if r.Surface != "" || r.Merge != "" {
		return fmt.Errorf("surface/merge are only valid on a render rule")
	}
	if r.MaxBytes < 0 {
		return fmt.Errorf("max_bytes cannot be negative")
	}
	if r.ConfirmHook && r.Strategy != StrategyCopy {
		return fmt.Errorf("confirm_hook is only valid on a copy rule")
	}
	// Replace must stay invertible: pull maps values back to keys, so every
	// value must round-trip to exactly one key and neither side may be empty.
	seen := make(map[string]string, len(r.Replace))
	for k, v := range r.Replace {
		if k == "" {
			return fmt.Errorf("replace key cannot be empty")
		}
		if v == "" {
			return fmt.Errorf("replace %q cannot map to an empty string (not invertible)", k)
		}
		if k == v {
			return fmt.Errorf("replace %q maps to itself", k)
		}
		if prev, dup := seen[v]; dup {
			return fmt.Errorf("replace keys %q and %q map to the same value %q (not invertible)", prev, k, v)
		}
		seen[v] = k
	}
	return nil
}

// Sep returns the effective separator (configured or default).
func (r *Rule) Sep() string {
	if r.Separator != "" {
		return r.Separator
	}
	return DefaultSeparator
}
