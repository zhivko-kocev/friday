package spec

import (
	"fmt"
	"strings"
)

// Permission actions, in friday's fixed evaluation priority: deny beats ask
// beats allow, first match wins. This is Claude Code's own model — the
// strictest of the six adapters, and the only one every other adapter's
// policy surface can be driven to emulate.
const (
	ActionDeny  = "deny"
	ActionAsk   = "ask"
	ActionAllow = "allow"
)

// Axis names — the five fixed permission dimensions permissions.yaml may
// declare. No adapter needs a sixth, and no rule needs fewer.
const (
	AxisRead  = "read"
	AxisWrite = "write"
	AxisExec  = "exec"
	AxisNet   = "net"
	AxisMCP   = "mcp"
)

// Permissions is the parsed, validated content of permissions.yaml.
type Permissions struct {
	Version int            `yaml:"version"`
	Read    PermissionAxis `yaml:"read,omitempty"`
	Write   PermissionAxis `yaml:"write,omitempty"`
	Exec    PermissionAxis `yaml:"exec,omitempty"`
	Net     PermissionAxis `yaml:"net,omitempty"`
	MCP     PermissionAxis `yaml:"mcp,omitempty"`
}

// PermissionAxis is one axis's three action buckets: glob-ish patterns
// (the axis defines what a pattern matches — a path, a command, a host) each
// adapter's renderer resolves in deny → ask → allow order, first match wins.
type PermissionAxis struct {
	Allow []string `yaml:"allow,omitempty"`
	Ask   []string `yaml:"ask,omitempty"`
	Deny  []string `yaml:"deny,omitempty"`
}

// NamedAxis pairs one axis's name with its value, for renderers that need to
// iterate every axis identically.
type NamedAxis struct {
	Name string
	Axis PermissionAxis
}

// Axes returns the five axes in their fixed declaration order, so every
// renderer iterating them produces output in the same order.
func (p *Permissions) Axes() []NamedAxis {
	return []NamedAxis{
		{AxisRead, p.Read},
		{AxisWrite, p.Write},
		{AxisExec, p.Exec},
		{AxisNet, p.Net},
		{AxisMCP, p.MCP},
	}
}

// ParsePermissions parses and validates permissions.yaml content. Strict
// decoding rejects an unrecognized axis or action name anywhere in the
// document (D-level typo protection); validate additionally rejects an
// empty pattern, which strict decoding cannot catch on its own.
func ParsePermissions(data []byte) (*Permissions, error) {
	var p Permissions
	if err := decodeStrict(data, &p); err != nil {
		return nil, err
	}
	if p.Version == 0 {
		p.Version = 1
	}
	if p.Version != 1 {
		return nil, fmt.Errorf("unsupported version %d", p.Version)
	}
	for _, a := range p.Axes() {
		if err := a.Axis.validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", a.Name, err)
		}
	}
	return &p, nil
}

func (a PermissionAxis) validate() error {
	for _, action := range []struct {
		name     string
		patterns []string
	}{
		{ActionAllow, a.Allow},
		{ActionAsk, a.Ask},
		{ActionDeny, a.Deny},
	} {
		for _, pat := range action.patterns {
			// A whitespace-only pattern renders verbatim into every
			// adapter's policy surface as a rule that can never match — on
			// a deny list, a protection the user believes they have.
			if strings.TrimSpace(pat) == "" {
				return fmt.Errorf("%s: empty pattern", action.name)
			}
		}
	}
	return nil
}
