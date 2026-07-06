// Package rules defines the Rule type and YAML parsing.
//
// A rule maps one or more files in the canonical store to a single target
// path (or, with copy strategy and a glob source, to many target paths via
// token expansion).
package rules

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

const (
	StrategyCopy        = "copy"
	StrategyConcatenate = "concatenate"

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
	case StrategyCopy, StrategyConcatenate:
	default:
		return fmt.Errorf("unknown strategy %q (must be copy or concatenate)", r.Strategy)
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
