// Package render projects friday's canonical schemas (internal/spec) onto
// each adapter's native configuration surface. Every function here is a
// pure, adapter-agnostic-input/adapter-specific-output mapping — the merge
// or write onto disk is internal/engine's job (planRender), not this
// package's.
package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/tailscale/hujson"
	"github.com/zhivko-kocev/friday/internal/secret"
	"github.com/zhivko-kocev/friday/internal/spec"
	"github.com/zhivko-kocev/friday/internal/textnorm"
)

// Per-adapter MCP shape, each line corroborated against that vendor's own
// documentation (read 2026-08-28). friday vendors no adapter schemas, and
// the CHANGELOG records more than one release that shipped a path or key
// nobody had checked — so every claim below names where it came from, and
// anything friday could *not* corroborate is called out rather than left
// looking verified.
//
//	claude    ~/.claude.json — mcpServers.<name> = {type, command/args/env |
//	          url/headers}. type is stdio|http|sse, plus streamable-http and
//	          ws; omitted type means stdio, but a url with no type is a
//	          documented configuration error, so friday always writes it.
//	          code.claude.com/docs/en/mcp
//	codex     ~/.codex/config.toml — [mcp_servers.<name>] = command/args/env
//	          | url/http_headers, plus enabled. There is no sse transport:
//	          url is streamable HTTP and nothing else.
//	          learn.chatgpt.com/docs/config-file/config-reference
//	copilot   ~/.copilot/mcp-config.json — mcpServers.<name> = {type
//	          local|http|sse (streamable-http aliases http), command/args/env
//	          | url/headers, tools}. tools is required on every entry.
//	          docs.github.com .../copilot-cli-reference/cli-command-reference
//	opencode  ~/.config/opencode/opencode.json — mcp.<name> = {type
//	          local|remote, command [exe, args...], environment | url/headers,
//	          enabled}. $defs.McpLocalConfig and $defs.McpRemoteConfig are
//	          both additionalProperties:false, so only these keys are legal.
//	          opencode.ai/config.json
//	gemini    ~/.gemini/settings.json — mcpServers.<name> = {command/args/env
//	          | httpUrl | url, headers}. No type discriminant: url is SSE and
//	          httpUrl is streamable HTTP.
//	          github.com/google-gemini/gemini-cli docs/tools/mcp-server.md
//	oh-my-pi  ~/.omp/agent/mcp.json — mcpServers.<name> = {type stdio|http|
//	          sse, command/args/env | url/headers, enabled}; omitted type
//	          means stdio.
//	          github.com/can1357/oh-my-pi docs/mcp-config.md
//
// NOT corroborated, and therefore not emitted: a per-entry disable key for
// claude, copilot or gemini. All three publish an exhaustive field list for
// a server entry and none of them contains one — each disables a server
// from somewhere friday's rules do not target: claude from
// projects.<path>.disabledMcpServers (a per-project sibling of mcpServers,
// inside the same file but not inside the entry), copilot from
// ~/.copilot/settings.json's disabledMcpServers, gemini from
// ~/.gemini/mcp-server-enablement.json. Copilot's `"enabled": false` is
// `copilot mcp list --json` *output*, not an input key. See mcpDisableFlag.

// mcpAdapters lists every adapter with a Model Context Protocol surface —
// every built-in adapter. Kept as a set literal (rather than deriving it
// from a switch's default case) so an unknown adapter name is reported
// precisely, matching AppliesTo's own semantics.
var mcpAdapters = map[string]bool{
	"claude": true, "codex": true, "copilot": true, "opencode": true, "gemini": true, "oh-my-pi": true,
}

// mcpDisableFlag lists the adapters whose own server entry can say "keep
// this configured but do not start it". Only these get a disabled server
// written at all; for the rest friday omits the entry and reports it,
// because inventing a key is worse than naming the gap — opencode's schema
// is additionalProperties:false, and Copilot documents unknown fields as
// "may ignore them, and later writes may remove them".
var mcpDisableFlag = map[string]bool{"codex": true, "opencode": true, "oh-my-pi": true}

// MCP projects m onto adapter's native MCP server configuration. payload is
// the file content a merge-json rule (claude, copilot, opencode, gemini,
// oh-my-pi) or merge-toml rule (codex) writes into that adapter's settings;
// unmapped names each server the adapter cannot carry in full, with the
// reason, for the caller to surface (engine's formatUnmapped).
//
// A disabled server is written with the adapter's own disable flag wherever
// one exists (mcpDisableFlag), never omitted. Omitting would leave removal
// to the merge step's retraction of the keys friday previously wrote, and a
// partially retracted entry is worse than no change: the server stays
// registered with a truncated command line. Where no native flag exists the
// entry is omitted — and reported, so the user learns the disable did not
// reach that agent.
//
// A server whose Agents list excludes adapter is omitted silently, and that
// asymmetry is deliberate: "disabled" is a property of the server that every
// config should record, while "not for this agent" means the entry has no
// business appearing in that adapter's file at all, off or on.
func MCP(adapter string, m spec.MCP) (payload []byte, unmapped []string, err error) {
	if !mcpAdapters[adapter] {
		return nil, nil, fmt.Errorf("render.MCP: unknown adapter %q", adapter)
	}

	names := make([]string, 0, len(m.Servers))
	for name, srv := range m.Servers {
		if srv.AppliesTo(adapter) {
			names = append(names, name)
		}
	}
	// Sorted so unmapped reads the same on every run. The payload's own key
	// order does not depend on this: both marshalers sort map keys.
	sort.Strings(names)

	keep := make([]string, 0, len(names))
	for _, name := range names {
		srv := m.Servers[name]
		if lost := mcpLosses(adapter, srv); len(lost) > 0 {
			unmapped = append(unmapped, name+" ("+strings.Join(lost, ", ")+")")
		}
		if srv.IsEnabled() || mcpDisableFlag[adapter] {
			keep = append(keep, name)
		}
	}
	if len(keep) == 0 {
		return nil, unmapped, nil
	}

	if adapter == "codex" {
		payload, err := mcpCodexTOML(m, keep)
		return payload, unmapped, err
	}

	entries := make(map[string]any, len(keep))
	for _, name := range keep {
		entries[name] = mcpEntry(adapter, m.Servers[name])
	}
	wrapperKey := "mcpServers"
	if adapter == "opencode" {
		wrapperKey = "mcp"
	}
	payload, err = json.Marshal(map[string]any{wrapperKey: entries})
	return payload, unmapped, err
}

// mcpLosses names everything adapter drops from srv, phrased to slot into a
// "<adapter> cannot represent: <name> (…)" sentence. Empty for a server the
// adapter carries in full, which is the common case.
func mcpLosses(adapter string, srv spec.MCPServer) []string {
	var lost []string
	if !srv.IsEnabled() && !mcpDisableFlag[adapter] {
		lost = append(lost, "disabled — no per-server off switch, so the entry is omitted")
	}
	if adapter == "codex" && srv.Transport == spec.TransportSSE {
		lost = append(lost, "sse transport — written as streamable http, which codex may not be able to reach")
	}
	if len(srv.Tools) > 0 && (adapter == "codex" || adapter == "gemini") {
		// Both have a native equivalent (codex enabled_tools, gemini
		// includeTools) that friday does not render, so the server is
		// exposed with every tool it advertises — a permissive loss, worth
		// saying out loud.
		lost = append(lost, "tools restriction — every tool stays exposed")
	}
	return lost
}

// MCPSecretWarnings reports every mcp.yaml value that looks like a literal
// credential rather than an ${ENV_VAR} reference, worded for push's output.
// friday resolves no variable references — a ${VAR} reaches the agent's
// config verbatim, which is the form all six document — so the only way a
// real secret lands in those files (frequently ~/.claude.json and
// settings.json, world-readable in most setups) is a user pasting one into
// mcp.yaml. secret.Scan otherwise runs only behind `friday doctor`, which
// push does not invoke.
func MCPSecretWarnings(m spec.MCP) []string {
	findings := secret.Scan(m)
	if len(findings) == 0 {
		return nil
	}
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		out = append(out, fmt.Sprintf("mcp.yaml: %s looks like a literal credential — use an ${ENV_VAR} reference instead", f.Key))
	}
	return out
}

// mcpEntry builds one server's native JSON entry for claude, copilot,
// opencode, gemini, or oh-my-pi. codex is TOML and handled separately by
// mcpCodexTOML, since its shape (a http_headers field, no client-visible
// "type" discriminant) doesn't fit this JSON-object shape at all.
func mcpEntry(adapter string, s spec.MCPServer) map[string]any {
	var e map[string]any
	switch adapter {
	case "opencode":
		e = mcpEntryOpenCode(s)
	case "gemini":
		e = mcpEntryGemini(s)
	default: // claude, copilot, oh-my-pi
		e = mcpEntryClaudeLike(adapter, s)
	}
	if !s.IsEnabled() {
		// Only reachable for an adapter in mcpDisableFlag: MCP omits a
		// disabled server for the rest rather than inventing a key. Written
		// only to say "off" — every adapter that has the field defaults it
		// to true, so restating that in each entry would be noise.
		e["enabled"] = false
	}
	return e
}

// mcpEntryClaudeLike renders claude and copilot's shared shape: a "type"
// discriminant ("stdio"/"http"/"sse" for claude, "local"/"http"/"sse" for
// copilot — Claude Code errors a url-bearing entry with no "type" as a
// misconfigured stdio server, so type is always written explicitly, never
// left implicit) plus command/args/env or url/headers. copilot additionally
// requires "tools" on every entry.
func mcpEntryClaudeLike(adapter string, s spec.MCPServer) map[string]any {
	e := map[string]any{}
	switch s.Transport {
	case spec.TransportStdio:
		e["type"] = "stdio"
		if adapter == "copilot" {
			e["type"] = "local"
		}
		e["command"] = s.Command
		if len(s.Args) > 0 {
			e["args"] = s.Args
		}
		if len(s.Env) > 0 {
			e["env"] = s.Env
		}
	case spec.TransportHTTP:
		e["type"] = "http"
		e["url"] = s.URL
		if len(s.Headers) > 0 {
			e["headers"] = s.Headers
		}
	case spec.TransportSSE:
		e["type"] = "sse"
		e["url"] = s.URL
		if len(s.Headers) > 0 {
			e["headers"] = s.Headers
		}
	}
	if adapter == "copilot" {
		e["tools"] = s.EffectiveTools()
	}
	return e
}

// mcpEntryOpenCode renders OpenCode's shape: "type": "local"|"remote",
// command as a single [executable, args...] array (not separate command +
// args fields), "environment" rather than "env", and both transports collapse
// onto "remote" since OpenCode does not distinguish http from sse.
func mcpEntryOpenCode(s spec.MCPServer) map[string]any {
	e := map[string]any{}
	switch s.Transport {
	case spec.TransportStdio:
		e["type"] = "local"
		e["command"] = append([]string{s.Command}, s.Args...)
		if len(s.Env) > 0 {
			e["environment"] = s.Env
		}
	case spec.TransportHTTP, spec.TransportSSE:
		e["type"] = "remote"
		e["url"] = s.URL
		if len(s.Headers) > 0 {
			e["headers"] = s.Headers
		}
	}
	return e
}

// mcpEntryGemini renders Gemini CLI's shape: no "type" discriminant at all —
// the field present selects the transport. command (+args/env) is stdio,
// url is SSE, httpUrl is streamable HTTP.
func mcpEntryGemini(s spec.MCPServer) map[string]any {
	e := map[string]any{}
	switch s.Transport {
	case spec.TransportStdio:
		e["command"] = s.Command
		if len(s.Args) > 0 {
			e["args"] = s.Args
		}
		if len(s.Env) > 0 {
			e["env"] = s.Env
		}
	case spec.TransportHTTP:
		e["httpUrl"] = s.URL
		if len(s.Headers) > 0 {
			e["headers"] = s.Headers
		}
	case spec.TransportSSE:
		e["url"] = s.URL
		if len(s.Headers) > 0 {
			e["headers"] = s.Headers
		}
	}
	return e
}

// codexMCPServer is one [mcp_servers.<name>] table. Field order here is
// go-toml/v2's output order for a struct, so a stdio entry always lists
// command/args/env before any (unused, omitted) remote fields — order has
// no semantic weight for codex's MCP tables (unlike its permission tables),
// it only reads better than an alphabetical map would.
type codexMCPServer struct {
	Command     string            `toml:"command,omitempty"`
	Args        []string          `toml:"args,omitempty"`
	Env         map[string]string `toml:"env,omitempty"`
	URL         string            `toml:"url,omitempty"`
	HTTPHeaders map[string]string `toml:"http_headers,omitempty"`
	// Enabled is a pointer so omitempty drops it when unset: codex defaults
	// it to true, and friday only ever writes it to say "off".
	Enabled *bool `toml:"enabled,omitempty"`
}

// mcpCodexTOML renders names' servers as Codex's [mcp_servers.<name>]
// tables. go-toml/v2 sorts map keys (here, server names) for deterministic
// output, so re-rendering the same mcp.yaml twice never reorders tables.
func mcpCodexTOML(m spec.MCP, names []string) ([]byte, error) {
	servers := make(map[string]codexMCPServer, len(names))
	for _, name := range names {
		s := m.Servers[name]
		cs := codexMCPServer{}
		switch s.Transport {
		case spec.TransportStdio:
			cs.Command = s.Command
			cs.Args = s.Args
			cs.Env = s.Env
		case spec.TransportHTTP, spec.TransportSSE:
			cs.URL = s.URL
			cs.HTTPHeaders = s.Headers
		}
		if !s.IsEnabled() {
			off := false
			cs.Enabled = &off
		}
		servers[name] = cs
	}
	return toml.Marshal(map[string]any{"mcp_servers": servers})
}

// MCPInverse parses adapter's native MCP configuration payload back into the
// servers it defines — the inverse of MCP. Every field the forward renderer
// writes round-trips exactly (Command/Args/Env/URL/Headers, and Tools for
// copilot, which is the one adapter that carries it natively). Fields
// mcp.yaml carries but no native form does at all — Enabled and Agents, plus
// Tools outside copilot — are always zero here; the caller merges this
// result into any existing mcp.yaml entry rather than replacing it, so
// those store-only fields survive a pull untouched.
//
// unmapped names each entry friday recognised as a server but could not
// express as a spec.MCPServer — claude's "ws" and "sdk" types, an opencode
// enable/disable override with no type, a codex table with neither command
// nor url. Those are skipped, not fatal: one entry friday has no canonical
// shape for must not cost the user every other server in the file.
//
// opencode's "remote" type and codex's shared url/http_headers shape cannot
// distinguish http from sse on the way back (the forward renderer collapses
// both to the same native shape) — both default to TransportHTTP here; the
// caller resolves the ambiguity by preferring an existing entry's transport
// when its URL is unchanged.
func MCPInverse(adapter string, payload []byte) (servers map[string]spec.MCPServer, unmapped []string, err error) {
	switch adapter {
	case "codex":
		return mcpServersFromCodexTOML(payload)
	case "claude", "copilot", "opencode", "gemini", "oh-my-pi":
		return mcpServersFromJSON(adapter, payload)
	default:
		return nil, nil, fmt.Errorf("render.MCPInverse: unknown adapter %q", adapter)
	}
}

// mcpWrapperKey is MCPInverse's JSON counterpart to MCP's own wrapperKey
// selection: opencode nests servers under "mcp", every other JSON adapter
// under "mcpServers".
func mcpWrapperKey(adapter string) string {
	if adapter == "opencode" {
		return "mcp"
	}
	return "mcpServers"
}

// mcpServersFromJSON decodes the adapter's live config through hujson, the
// same way the push side reads it (engine's decodeJSON): every one of these
// files — Claude's ~/.claude.json, Copilot's mcp-config.json, Gemini's
// settings.json — is one users routinely comment, and a JSONC target that
// merges without complaint must not then fail to pull.
func mcpServersFromJSON(adapter string, payload []byte) (map[string]spec.MCPServer, []string, error) {
	var doc map[string]json.RawMessage
	if trimmed := bytes.TrimSpace(textnorm.Newlines(payload)); len(trimmed) > 0 {
		// Standardize blanks comments in place to preserve byte offsets, so
		// it gets a copy and never reaches back into the caller's buffer.
		std, serr := hujson.Standardize(append([]byte(nil), trimmed...))
		if serr != nil {
			return nil, nil, fmt.Errorf("parse: %w", serr)
		}
		if err := json.Unmarshal(std, &doc); err != nil {
			return nil, nil, fmt.Errorf("parse: %w", err)
		}
	}
	key := mcpWrapperKey(adapter)
	var entries map[string]map[string]any
	if raw, ok := doc[key]; ok {
		if err := json.Unmarshal(raw, &entries); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", key, err)
		}
	}

	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names) // unmapped must not reorder between runs

	out := make(map[string]spec.MCPServer, len(entries))
	var unmapped []string
	for _, name := range names {
		var (
			srv spec.MCPServer
			ok  bool
		)
		switch adapter {
		case "opencode":
			srv, ok = mcpServerFromOpenCode(entries[name])
		case "gemini":
			srv, ok = mcpServerFromGemini(entries[name])
		default: // claude, copilot, oh-my-pi
			srv, ok = mcpServerFromClaudeLike(adapter, entries[name])
		}
		if !ok {
			unmapped = append(unmapped, name)
			continue
		}
		out[name] = srv
	}
	return out, unmapped, nil
}

// mcpServerFromClaudeLike reverses mcpEntryClaudeLike. ok is false for an
// entry whose type has no spec.MCPServer counterpart — claude's "ws" and
// "sdk", anything a future release adds — leaving the caller to skip and
// report it.
func mcpServerFromClaudeLike(adapter string, e map[string]any) (spec.MCPServer, bool) {
	var s spec.MCPServer
	typ, _ := e["type"].(string)
	if typ == "" && e["command"] != nil {
		// Claude and oh-my-pi both document stdio as the default when type
		// is omitted, and `claude mcp add-json <name> '{"command":…}'`
		// writes exactly that; copilot's default is "local", the same
		// transport under a different name.
		typ = "stdio"
	}
	switch typ {
	case "stdio", "local":
		s.Transport = spec.TransportStdio
		s.Command, _ = e["command"].(string)
		s.Args = toStringSlice(e["args"])
		s.Env = toStringMap(e["env"])
	case "http", "streamable-http": // claude and copilot both alias the two
		s.Transport = spec.TransportHTTP
		s.URL, _ = e["url"].(string)
		s.Headers = toStringMap(e["headers"])
	case "sse":
		s.Transport = spec.TransportSSE
		s.URL, _ = e["url"].(string)
		s.Headers = toStringMap(e["headers"])
	default:
		return spec.MCPServer{}, false
	}
	if adapter == "copilot" {
		s.Tools = toStringSlice(e["tools"])
	}
	return s, true
}

// mcpServerFromOpenCode reverses mcpEntryOpenCode. ok is false for the
// schema's third entry shape — a bare {"enabled": …} override of an
// org-provided server, which carries no transport to recover.
func mcpServerFromOpenCode(e map[string]any) (spec.MCPServer, bool) {
	var s spec.MCPServer
	switch typ, _ := e["type"].(string); typ {
	case "local":
		s.Transport = spec.TransportStdio
		if cmd := toStringSlice(e["command"]); len(cmd) > 0 {
			s.Command = cmd[0]
			if len(cmd) > 1 {
				s.Args = cmd[1:]
			}
		}
		s.Env = toStringMap(e["environment"])
	case "remote":
		s.Transport = spec.TransportHTTP // ambiguous vs sse — see MCPInverse doc
		s.URL, _ = e["url"].(string)
		s.Headers = toStringMap(e["headers"])
	default:
		return spec.MCPServer{}, false
	}
	return s, true
}

// mcpServerFromGemini reverses mcpEntryGemini. Field presence alone selects
// the transport, exactly as the forward renderer chose it; ok is false for
// an entry with none of the three (gemini's tcp servers, say).
func mcpServerFromGemini(e map[string]any) (spec.MCPServer, bool) {
	var s spec.MCPServer
	switch {
	case e["command"] != nil:
		s.Transport = spec.TransportStdio
		s.Command, _ = e["command"].(string)
		s.Args = toStringSlice(e["args"])
		s.Env = toStringMap(e["env"])
	case e["httpUrl"] != nil:
		s.Transport = spec.TransportHTTP
		s.URL, _ = e["httpUrl"].(string)
		s.Headers = toStringMap(e["headers"])
	case e["url"] != nil:
		s.Transport = spec.TransportSSE
		s.URL, _ = e["url"].(string)
		s.Headers = toStringMap(e["headers"])
	default:
		return spec.MCPServer{}, false
	}
	return s, true
}

// mcpServersFromCodexTOML reverses mcpCodexTOML. A table with neither
// command nor url is reported rather than recovered as an http server with
// an empty URL.
func mcpServersFromCodexTOML(payload []byte) (map[string]spec.MCPServer, []string, error) {
	var doc struct {
		MCPServers map[string]codexMCPServer `toml:"mcp_servers"`
	}
	if len(payload) > 0 {
		if err := toml.Unmarshal(payload, &doc); err != nil {
			return nil, nil, fmt.Errorf("parse: %w", err)
		}
	}
	names := make([]string, 0, len(doc.MCPServers))
	for name := range doc.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names) // unmapped must not reorder between runs

	out := make(map[string]spec.MCPServer, len(doc.MCPServers))
	var unmapped []string
	for _, name := range names {
		cs := doc.MCPServers[name]
		var s spec.MCPServer
		switch {
		case cs.Command != "":
			s.Transport = spec.TransportStdio
			s.Command = cs.Command
			s.Args = cs.Args
			s.Env = cs.Env
		case cs.URL != "":
			s.Transport = spec.TransportHTTP // ambiguous vs sse — see MCPInverse doc
			s.URL = cs.URL
			s.Headers = cs.HTTPHeaders
		default:
			unmapped = append(unmapped, name)
			continue
		}
		out[name] = s
	}
	return out, unmapped, nil
}

// toStringSlice converts a decoded JSON array ([]any of strings) to []string,
// dropping any non-string element rather than erroring — a native config
// file with a malformed array is the user's to fix, not friday's to reject
// on pull, and MCPInverse's job is to recover what it safely can.
//
// An absent or empty array yields nil, never an empty slice: the pull-side
// merge compares recovered against existing with reflect.DeepEqual, which
// separates nil from []string{}, so an empty one would report an update on
// every single pull for a file that never changed.
func toStringSlice(v any) []string {
	arr, ok := v.([]any)
	if !ok || len(arr) == 0 {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// toStringMap converts a decoded JSON object (map[string]any of strings) to
// map[string]string, dropping any non-string value, and returning nil for an
// empty result (see toStringSlice for both rules).
func toStringMap(v any) map[string]string {
	m, ok := v.(map[string]any)
	if !ok || len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, x := range m {
		if s, ok := x.(string); ok {
			out[k] = s
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
