// Package spec defines friday's canonical, adapter-agnostic schemas —
// mcp.yaml (Model Context Protocol servers), permissions.yaml (the five-axis
// allow/ask/deny policy), and defaults.yaml (model, theme, statusline,
// telemetry opt-out) — plus the neutral records lifted from agents/*.md and
// commands/*.md. internal/render projects each parsed, validated value onto
// a specific adapter's native config shape.
package spec

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Adapters is every adapter name friday's canonical schemas may name — the
// closed set mcp.yaml's `agents:` and defaults.yaml's `overrides:` keys are
// checked against, sorted so an error message always lists them the same
// way. A surface narrower than "every adapter" is render's business: it
// reports separately on an adapter that is real but has no such surface
// (oh-my-pi's settings are YAML, so no defaults.yaml rule reaches it).
var Adapters = []string{"claude", "codex", "copilot", "gemini", "oh-my-pi", "opencode"}

// yamlUnknownField matches yaml.v3's own unknown-field wording, which names
// the Go type the field decoded into (spec.PermissionAxis) — an internal
// detail the user never wrote and cannot act on. yamlTypeQualifier catches
// the same leak in yaml's type-mismatch wording, where the type name is the
// only hint about the expected shape and so is kept, just unqualified.
var (
	yamlUnknownField  = regexp.MustCompile(`field (\S+) not found in type \S+`)
	yamlTypeQualifier = regexp.MustCompile(`into (\S*)spec\.(\S+)`)
)

// decodeStrict decodes exactly one YAML document from data into out, with
// unknown-field rejection. It refuses a multi-document stream, whose second
// and later documents yaml's Decoder discards silently — taking every
// server, axis, or default in them along; and it rewrites yaml's own error
// text, which leaks a Go type name and spans several lines that a
// single-line status Reason renders as a run-on. No filename is prefixed:
// only the caller knows which of several source roots this content came
// from, and it already names the file it read.
func decodeStrict(data []byte, out any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil && !errors.Is(err, io.EOF) {
		return cleanDecodeError(err)
	}
	var extra yaml.Node
	err := dec.Decode(&extra)
	switch {
	case errors.Is(err, io.EOF):
		return nil
	case err != nil:
		return cleanDecodeError(err)
	default:
		return errors.New("expected a single YAML document (content after a `---` separator would be silently ignored)")
	}
}

func cleanDecodeError(err error) error {
	msg := strings.TrimPrefix(err.Error(), "yaml: unmarshal errors:")
	msg = yamlUnknownField.ReplaceAllString(msg, `unknown field "${1}"`)
	msg = yamlTypeQualifier.ReplaceAllString(msg, "into ${1}${2}")
	var parts []string
	for _, line := range strings.Split(msg, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			parts = append(parts, line)
		}
	}
	return errors.New(strings.Join(parts, "; "))
}

// Transport values for MCPServer.Transport.
const (
	TransportStdio = "stdio"
	TransportHTTP  = "http"
	TransportSSE   = "sse"
)

// MCP is the parsed, validated content of mcp.yaml.
type MCP struct {
	Version int                  `yaml:"version"`
	Servers map[string]MCPServer `yaml:"servers,omitempty"`
}

// MCPServer is one named entry under mcp.yaml's servers map.
type MCPServer struct {
	// Transport selects the connection kind: stdio spawns Command with Args
	// and Env; http and sse instead dial URL with Headers.
	Transport string            `yaml:"transport"`
	Command   string            `yaml:"command,omitempty"`
	Args      []string          `yaml:"args,omitempty"`
	Env       map[string]string `yaml:"env,omitempty"`
	URL       string            `yaml:"url,omitempty"`
	Headers   map[string]string `yaml:"headers,omitempty"`
	// Tools lists the tool names Copilot requires named explicitly; every
	// other target ignores it. Empty means every tool — see EffectiveTools.
	Tools []string `yaml:"tools,omitempty"`
	// Enabled defaults to true when unset — a pointer so an explicit `false`
	// is distinguishable from "not written". See IsEnabled.
	Enabled *bool `yaml:"enabled,omitempty"`
	// Agents restricts the server to a subset of adapters; empty means every
	// adapter. See AppliesTo.
	Agents []string `yaml:"agents,omitempty"`
}

// IsEnabled reports whether the server is active. The default (Enabled
// unset) is enabled.
func (s MCPServer) IsEnabled() bool { return s.Enabled == nil || *s.Enabled }

// EffectiveTools returns Tools, defaulting to the wildcard ("*", every
// tool) when the field is empty.
func (s MCPServer) EffectiveTools() []string {
	if len(s.Tools) == 0 {
		return []string{"*"}
	}
	return s.Tools
}

// AppliesTo reports whether the server should be offered to adapter. An
// empty Agents list means every adapter.
func (s MCPServer) AppliesTo(adapter string) bool {
	return len(s.Agents) == 0 || slices.Contains(s.Agents, adapter)
}

// ParseMCP parses and validates mcp.yaml content. Strict decoding rejects an
// unrecognized field anywhere in the document — the same typo-catching
// friday.yaml already gets from its own struct-shaped decode, applied here
// explicitly since the servers map's value type has a fixed field set too.
// Server names are visited in sorted order so a file with more than one
// problem always reports the same one.
func ParseMCP(data []byte) (*MCP, error) {
	var m MCP
	if err := decodeStrict(data, &m); err != nil {
		return nil, err
	}
	if m.Version == 0 {
		m.Version = 1
	}
	if m.Version != 1 {
		return nil, fmt.Errorf("unsupported version %d", m.Version)
	}
	names := make([]string, 0, len(m.Servers))
	for name := range m.Servers {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			// Every adapter keys its native MCP config by this name; an
			// empty key is a config the agent rejects, and a stray trailing
			// colon produces one.
			return nil, errors.New("a server name must not be empty")
		}
		if err := m.Servers[name].validate(); err != nil {
			return nil, fmt.Errorf("server %q: %w", name, err)
		}
	}
	return &m, nil
}

func (s MCPServer) validate() error {
	switch s.Transport {
	case TransportStdio:
		if s.Command == "" {
			return fmt.Errorf("transport %s requires command", TransportStdio)
		}
	case TransportHTTP, TransportSSE:
		if s.URL == "" {
			return fmt.Errorf("transport %s requires url", s.Transport)
		}
	case "":
		return fmt.Errorf("transport is required (%s, %s, or %s)", TransportStdio, TransportHTTP, TransportSSE)
	default:
		return fmt.Errorf("unknown transport %q (must be %s, %s, or %s)", s.Transport, TransportStdio, TransportHTTP, TransportSSE)
	}
	// An unknown name here silently removes the server from every adapter —
	// AppliesTo just never matches it, and render skips a non-applying
	// server without reporting it. That makes agents: the one open enum in
	// the schema, and the quietest possible typo.
	for _, a := range s.Agents {
		if !slices.Contains(Adapters, a) {
			return fmt.Errorf("unknown agent %q in agents (must be one of %s)", a, strings.Join(Adapters, ", "))
		}
	}
	return nil
}
