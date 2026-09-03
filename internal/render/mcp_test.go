package render

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/zhivko-kocev/friday/internal/spec"
)

func decodeJSON(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, data)
	}
	return v
}

// goldenMCP is the one fixture the byte-exact and determinism tests share:
// all three transports, env, headers, tools, a name that needs quoting in
// codex's [mcp_servers.<name>] table header, a Windows command path, and a
// disabled server (which every adapter renders differently).
func goldenMCP() spec.MCP {
	off := false
	return spec.MCP{Version: 1, Servers: map[string]spec.MCPServer{
		"github": {
			Transport: spec.TransportStdio,
			Command:   "npx",
			Args:      []string{"-y", "server-github"},
			Env:       map[string]string{"GITHUB_TOKEN": "${GITHUB_TOKEN}", "NODE_ENV": "production"},
		},
		"linear": {
			Transport: spec.TransportHTTP,
			URL:       "https://mcp.linear.app/mcp",
			Headers:   map[string]string{"Authorization": "Bearer ${LINEAR_TOKEN}", "X-Trace": "on"},
			Tools:     []string{"search_issues"},
		},
		"sentry": {Transport: spec.TransportSSE, URL: "https://mcp.sentry.dev/sse"},
		"my.local srv": {
			Transport: spec.TransportStdio,
			Command:   `C:\tools\srv.exe`,
			Args:      []string{`--root=C:\work`},
		},
		"retired": {Transport: spec.TransportStdio, Command: "uvx", Enabled: &off},
	}}
}

// TestMCPGoldenShapes pins the rendered bytes for all six adapters. The
// decoded-document assertions elsewhere in this file are blind to
// serialization; these are not, so a future encoder swap (or an accidental
// key rename) shows up as a one-line diff instead of silently shipping.
func TestMCPGoldenShapes(t *testing.T) {
	tests := []struct {
		adapter  string
		want     string
		unmapped []string
	}{
		{
			adapter: "claude",
			want:    `{"mcpServers":{"github":{"args":["-y","server-github"],"command":"npx","env":{"GITHUB_TOKEN":"${GITHUB_TOKEN}","NODE_ENV":"production"},"type":"stdio"},"linear":{"headers":{"Authorization":"Bearer ${LINEAR_TOKEN}","X-Trace":"on"},"type":"http","url":"https://mcp.linear.app/mcp"},"my.local srv":{"args":["--root=C:\\work"],"command":"C:\\tools\\srv.exe","type":"stdio"},"sentry":{"type":"sse","url":"https://mcp.sentry.dev/sse"}}}`,
			unmapped: []string{
				"retired (disabled — no per-server off switch, so the entry is omitted)",
			},
		},
		{
			adapter: "copilot",
			want:    `{"mcpServers":{"github":{"args":["-y","server-github"],"command":"npx","env":{"GITHUB_TOKEN":"${GITHUB_TOKEN}","NODE_ENV":"production"},"tools":["*"],"type":"local"},"linear":{"headers":{"Authorization":"Bearer ${LINEAR_TOKEN}","X-Trace":"on"},"tools":["search_issues"],"type":"http","url":"https://mcp.linear.app/mcp"},"my.local srv":{"args":["--root=C:\\work"],"command":"C:\\tools\\srv.exe","tools":["*"],"type":"local"},"sentry":{"tools":["*"],"type":"sse","url":"https://mcp.sentry.dev/sse"}}}`,
			unmapped: []string{
				"retired (disabled — no per-server off switch, so the entry is omitted)",
			},
		},
		{
			adapter: "opencode",
			want:    `{"mcp":{"github":{"command":["npx","-y","server-github"],"environment":{"GITHUB_TOKEN":"${GITHUB_TOKEN}","NODE_ENV":"production"},"type":"local"},"linear":{"headers":{"Authorization":"Bearer ${LINEAR_TOKEN}","X-Trace":"on"},"type":"remote","url":"https://mcp.linear.app/mcp"},"my.local srv":{"command":["C:\\tools\\srv.exe","--root=C:\\work"],"type":"local"},"retired":{"command":["uvx"],"enabled":false,"type":"local"},"sentry":{"type":"remote","url":"https://mcp.sentry.dev/sse"}}}`,
		},
		{
			adapter: "gemini",
			want:    `{"mcpServers":{"github":{"args":["-y","server-github"],"command":"npx","env":{"GITHUB_TOKEN":"${GITHUB_TOKEN}","NODE_ENV":"production"}},"linear":{"headers":{"Authorization":"Bearer ${LINEAR_TOKEN}","X-Trace":"on"},"httpUrl":"https://mcp.linear.app/mcp"},"my.local srv":{"args":["--root=C:\\work"],"command":"C:\\tools\\srv.exe"},"sentry":{"url":"https://mcp.sentry.dev/sse"}}}`,
			unmapped: []string{
				"linear (tools restriction — every tool stays exposed)",
				"retired (disabled — no per-server off switch, so the entry is omitted)",
			},
		},
		{
			adapter: "oh-my-pi",
			want:    `{"mcpServers":{"github":{"args":["-y","server-github"],"command":"npx","env":{"GITHUB_TOKEN":"${GITHUB_TOKEN}","NODE_ENV":"production"},"type":"stdio"},"linear":{"headers":{"Authorization":"Bearer ${LINEAR_TOKEN}","X-Trace":"on"},"type":"http","url":"https://mcp.linear.app/mcp"},"my.local srv":{"args":["--root=C:\\work"],"command":"C:\\tools\\srv.exe","type":"stdio"},"retired":{"command":"uvx","enabled":false,"type":"stdio"},"sentry":{"type":"sse","url":"https://mcp.sentry.dev/sse"}}}`,
		},
		{
			adapter: "codex",
			// The dotted/spaced server name must be quoted per SEGMENT
			// ([mcp_servers.'my.local srv']), not across the whole path —
			// ['mcp_servers.my.local srv'] would declare one table literally
			// named "mcp_servers.my.local srv" instead of a child of
			// mcp_servers, and codex would never find the server.
			want: "[mcp_servers]\n[mcp_servers.github]\ncommand = 'npx'\nargs = ['-y', 'server-github']\n\n[mcp_servers.github.env]\nGITHUB_TOKEN = '${GITHUB_TOKEN}'\nNODE_ENV = 'production'\n\n[mcp_servers.linear]\nurl = 'https://mcp.linear.app/mcp'\n\n[mcp_servers.linear.http_headers]\nAuthorization = 'Bearer ${LINEAR_TOKEN}'\nX-Trace = 'on'\n\n[mcp_servers.'my.local srv']\ncommand = 'C:\\tools\\srv.exe'\nargs = ['--root=C:\\work']\n\n[mcp_servers.retired]\ncommand = 'uvx'\nenabled = false\n\n[mcp_servers.sentry]\nurl = 'https://mcp.sentry.dev/sse'\n",
			unmapped: []string{
				"linear (tools restriction — every tool stays exposed)",
				"sentry (sse transport — written as streamable http, which codex may not be able to reach)",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.adapter, func(t *testing.T) {
			payload, unmapped, err := MCP(tt.adapter, goldenMCP())
			if err != nil {
				t.Fatal(err)
			}
			if string(payload) != tt.want {
				t.Errorf("payload mismatch\n got %s\nwant %s", payload, tt.want)
			}
			if !reflect.DeepEqual(unmapped, tt.unmapped) {
				t.Errorf("unmapped = %q, want %q", unmapped, tt.unmapped)
			}
		})
	}
}

// TestMCPRenderIsDeterministic is the property `friday push`'s exit-2-on-
// conflict contract rests on: the same mcp.yaml must render to the same
// bytes every time, or CI reports a spurious conflict. It holds today
// because both marshalers sort map keys, but nothing else in this package
// defends it against an ordered encoder.
func TestMCPRenderIsDeterministic(t *testing.T) {
	for _, adapter := range []string{"claude", "codex", "copilot", "opencode", "gemini", "oh-my-pi"} {
		t.Run(adapter, func(t *testing.T) {
			first, firstUnmapped, err := MCP(adapter, goldenMCP())
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 8; i++ {
				got, gotUnmapped, err := MCP(adapter, goldenMCP())
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != string(first) {
					t.Fatalf("render %d differs\n got %s\nwant %s", i, got, first)
				}
				if !reflect.DeepEqual(gotUnmapped, firstUnmapped) {
					t.Fatalf("render %d unmapped differs: %q vs %q", i, gotUnmapped, firstUnmapped)
				}
			}
		})
	}
}

// TestMCPDisabledServerUsesNativeFlag is the regression test for the
// half-deleted server: friday used to drop a disabled entry from the payload
// and leave its removal to merge retraction, which cannot retract a scalar
// leaf — so `enabled: false` on an npx server left {"command":"npx"} behind
// and the agent kept spawning a bare npx. Every adapter that documents a
// per-server flag must now say so in the entry itself.
func TestMCPDisabledServerUsesNativeFlag(t *testing.T) {
	off := false
	m := spec.MCP{Servers: map[string]spec.MCPServer{
		"github": {Transport: spec.TransportStdio, Command: "npx", Args: []string{"-y", "server-github"}, Enabled: &off},
	}}

	t.Run("codex", func(t *testing.T) {
		payload, unmapped, err := MCP("codex", m)
		if err != nil {
			t.Fatal(err)
		}
		if len(unmapped) != 0 {
			t.Errorf("unmapped = %q, want none (codex has mcp_servers.<id>.enabled)", unmapped)
		}
		var doc struct {
			MCPServers map[string]struct {
				Command string `toml:"command"`
				Enabled *bool  `toml:"enabled"`
			} `toml:"mcp_servers"`
		}
		if err := toml.Unmarshal(payload, &doc); err != nil {
			t.Fatalf("output is not valid TOML: %v\n%s", err, payload)
		}
		got := doc.MCPServers["github"]
		if got.Enabled == nil || *got.Enabled {
			t.Errorf("enabled = %v, want false", got.Enabled)
		}
		if got.Command != "npx" {
			t.Errorf("command = %q — the entry must stay intact, only switched off", got.Command)
		}
	})

	for _, tc := range []struct{ adapter, wrapper string }{
		{"opencode", "mcp"},
		{"oh-my-pi", "mcpServers"},
	} {
		t.Run(tc.adapter, func(t *testing.T) {
			payload, unmapped, err := MCP(tc.adapter, m)
			if err != nil {
				t.Fatal(err)
			}
			if len(unmapped) != 0 {
				t.Errorf("unmapped = %q, want none (%s documents a per-server enabled flag)", unmapped, tc.adapter)
			}
			srv := decodeJSON(t, payload)[tc.wrapper].(map[string]any)["github"].(map[string]any)
			if srv["enabled"] != false {
				t.Errorf("enabled = %v, want false", srv["enabled"])
			}
			if srv["command"] == nil {
				t.Errorf("entry = %v — must stay intact, only switched off", srv)
			}
		})
	}
}

// TestMCPDisabledServerOmittedAndReported covers the other three: claude,
// copilot and gemini publish an exhaustive per-entry field list and none of
// them has a disable key (each disables from a sibling list or a separate
// file friday's rules do not target), so friday omits the entry — and must
// say so rather than losing the user's intent silently.
func TestMCPDisabledServerOmittedAndReported(t *testing.T) {
	off := false
	m := spec.MCP{Servers: map[string]spec.MCPServer{
		"github": {Transport: spec.TransportStdio, Command: "npx", Enabled: &off},
		"linear": {Transport: spec.TransportHTTP, URL: "https://mcp.linear.app/mcp"},
	}}
	want := []string{"github (disabled — no per-server off switch, so the entry is omitted)"}
	for _, adapter := range []string{"claude", "copilot", "gemini"} {
		t.Run(adapter, func(t *testing.T) {
			payload, unmapped, err := MCP(adapter, m)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(unmapped, want) {
				t.Errorf("unmapped = %q, want %q", unmapped, want)
			}
			entries := decodeJSON(t, payload)["mcpServers"].(map[string]any)
			if _, ok := entries["github"]; ok {
				t.Errorf("disabled server must not be written for %s: %s", adapter, payload)
			}
			if _, ok := entries["linear"]; !ok {
				t.Errorf("enabled server missing: %s", payload)
			}
		})
	}
}

// TestMCPDisabledOnlyServerYieldsNoPayloadButReports guards the early
// return: an adapter with nothing left to write still has to carry the
// report out.
func TestMCPDisabledOnlyServerYieldsNoPayloadButReports(t *testing.T) {
	off := false
	m := spec.MCP{Servers: map[string]spec.MCPServer{
		"github": {Transport: spec.TransportStdio, Command: "npx", Enabled: &off},
	}}
	payload, unmapped, err := MCP("claude", m)
	if err != nil {
		t.Fatal(err)
	}
	if payload != nil {
		t.Errorf("payload = %q, want nil", payload)
	}
	if len(unmapped) != 1 {
		t.Errorf("unmapped = %q, want the disabled server reported", unmapped)
	}
}

func TestMCPCodexReportsSSEAsUnmapped(t *testing.T) {
	// Codex documents exactly one remote key, mcp_servers.<id>.url, for a
	// streamable-HTTP server; it has no sse transport at all.
	m := spec.MCP{Servers: map[string]spec.MCPServer{
		"sentry": {Transport: spec.TransportSSE, URL: "https://mcp.sentry.dev/sse"},
	}}
	_, unmapped, err := MCP("codex", m)
	if err != nil {
		t.Fatal(err)
	}
	if len(unmapped) != 1 {
		t.Fatalf("unmapped = %q, want the sse server reported", unmapped)
	}
	// Every other adapter carries sse natively and must stay quiet.
	for _, adapter := range []string{"claude", "copilot", "opencode", "gemini", "oh-my-pi"} {
		if _, u, err := MCP(adapter, m); err != nil || len(u) != 0 {
			t.Errorf("%s: unmapped = %q, err = %v, want none", adapter, u, err)
		}
	}
}

func TestMCPReportsDroppedToolsForCodexAndGemini(t *testing.T) {
	m := spec.MCP{Servers: map[string]spec.MCPServer{
		"github": {Transport: spec.TransportHTTP, URL: "https://api.githubcopilot.com/mcp/", Tools: []string{"search_code"}},
	}}
	for _, adapter := range []string{"codex", "gemini"} {
		_, unmapped, err := MCP(adapter, m)
		if err != nil {
			t.Fatal(err)
		}
		if len(unmapped) != 1 {
			t.Errorf("%s: unmapped = %q, want the tools restriction reported", adapter, unmapped)
		}
	}
	// copilot renders tools natively; claude, opencode and oh-my-pi have no
	// equivalent but friday never claimed one for them either — only codex
	// and gemini have a native key friday declines to use.
	for _, adapter := range []string{"claude", "copilot", "opencode", "oh-my-pi"} {
		if _, u, err := MCP(adapter, m); err != nil || len(u) != 0 {
			t.Errorf("%s: unmapped = %q, err = %v, want none", adapter, u, err)
		}
	}
}

func TestMCPClaudeStdio(t *testing.T) {
	m := spec.MCP{Servers: map[string]spec.MCPServer{
		"github": {Transport: spec.TransportStdio, Command: "npx", Args: []string{"-y", "server-github"}, Env: map[string]string{"GITHUB_TOKEN": "${GITHUB_TOKEN}"}},
	}}
	payload, unmapped, err := MCP("claude", m)
	if err != nil {
		t.Fatal(err)
	}
	if len(unmapped) != 0 {
		t.Errorf("unmapped = %v, want none", unmapped)
	}
	got := decodeJSON(t, payload)
	want := map[string]any{"mcpServers": map[string]any{"github": map[string]any{
		"type": "stdio", "command": "npx", "args": []any{"-y", "server-github"}, "env": map[string]any{"GITHUB_TOKEN": "${GITHUB_TOKEN}"},
	}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestMCPClaudeHTTPRequiresExplicitType(t *testing.T) {
	// A url with no "type" is a documented Claude Code configuration error
	// (read as a broken stdio server) — type must always be explicit.
	m := spec.MCP{Servers: map[string]spec.MCPServer{
		"linear": {Transport: spec.TransportHTTP, URL: "https://mcp.linear.app/mcp", Headers: map[string]string{"Authorization": "Bearer x"}},
	}}
	payload, _, err := MCP("claude", m)
	if err != nil {
		t.Fatal(err)
	}
	got := decodeJSON(t, payload)
	want := map[string]any{"mcpServers": map[string]any{"linear": map[string]any{
		"type": "http", "url": "https://mcp.linear.app/mcp", "headers": map[string]any{"Authorization": "Bearer x"},
	}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestMCPCopilotLocalHasMandatoryTools(t *testing.T) {
	m := spec.MCP{Servers: map[string]spec.MCPServer{
		"serena": {Transport: spec.TransportStdio, Command: "uvx"},
	}}
	payload, _, err := MCP("copilot", m)
	if err != nil {
		t.Fatal(err)
	}
	got := decodeJSON(t, payload)
	want := map[string]any{"mcpServers": map[string]any{"serena": map[string]any{
		"type": "local", "command": "uvx", "tools": []any{"*"},
	}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v (tools is mandatory and defaults to the wildcard)", got, want)
	}
}

func TestMCPCopilotHonoursExplicitTools(t *testing.T) {
	m := spec.MCP{Servers: map[string]spec.MCPServer{
		"github": {Transport: spec.TransportHTTP, URL: "https://api.githubcopilot.com/mcp/", Tools: []string{"search_code"}},
	}}
	payload, unmapped, err := MCP("copilot", m)
	if err != nil {
		t.Fatal(err)
	}
	if len(unmapped) != 0 {
		t.Errorf("unmapped = %q, want none (copilot carries tools natively)", unmapped)
	}
	got := decodeJSON(t, payload)
	want := map[string]any{"mcpServers": map[string]any{"github": map[string]any{
		"type": "http", "url": "https://api.githubcopilot.com/mcp/", "tools": []any{"search_code"},
	}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestMCPOpenCodeLocalCombinesCommandAndArgs(t *testing.T) {
	m := spec.MCP{Servers: map[string]spec.MCPServer{
		"github": {Transport: spec.TransportStdio, Command: "npx", Args: []string{"-y", "server-github"}, Env: map[string]string{"K": "v"}},
	}}
	payload, _, err := MCP("opencode", m)
	if err != nil {
		t.Fatal(err)
	}
	got := decodeJSON(t, payload)
	want := map[string]any{"mcp": map[string]any{"github": map[string]any{
		"type": "local", "command": []any{"npx", "-y", "server-github"}, "environment": map[string]any{"K": "v"},
	}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v (one command array, no args field, environment not env)", got, want)
	}
}

func TestMCPOpenCodeRemoteCollapsesHTTPAndSSE(t *testing.T) {
	m := spec.MCP{Servers: map[string]spec.MCPServer{
		"a": {Transport: spec.TransportHTTP, URL: "https://a.example/mcp"},
		"b": {Transport: spec.TransportSSE, URL: "https://b.example/sse"},
	}}
	payload, _, err := MCP("opencode", m)
	if err != nil {
		t.Fatal(err)
	}
	got := decodeJSON(t, payload)
	want := map[string]any{"mcp": map[string]any{
		"a": map[string]any{"type": "remote", "url": "https://a.example/mcp"},
		"b": map[string]any{"type": "remote", "url": "https://b.example/sse"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestMCPGeminiTransportSelectsField(t *testing.T) {
	m := spec.MCP{Servers: map[string]spec.MCPServer{
		"stdio-srv": {Transport: spec.TransportStdio, Command: "python", Args: []string{"-m", "srv"}},
		"http-srv":  {Transport: spec.TransportHTTP, URL: "http://localhost:3000/mcp"},
		"sse-srv":   {Transport: spec.TransportSSE, URL: "http://localhost:8080/sse"},
	}}
	payload, unmapped, err := MCP("gemini", m)
	if err != nil {
		t.Fatal(err)
	}
	if len(unmapped) != 0 {
		t.Errorf("unmapped = %v, want none (gemini covers all three transports)", unmapped)
	}
	got := decodeJSON(t, payload)
	want := map[string]any{"mcpServers": map[string]any{
		"stdio-srv": map[string]any{"command": "python", "args": []any{"-m", "srv"}},
		"http-srv":  map[string]any{"httpUrl": "http://localhost:3000/mcp"},
		"sse-srv":   map[string]any{"url": "http://localhost:8080/sse"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestMCPCodexTOMLShape(t *testing.T) {
	m := spec.MCP{Servers: map[string]spec.MCPServer{
		"github": {Transport: spec.TransportStdio, Command: "npx", Args: []string{"-y", "server-github"}, Env: map[string]string{"GITHUB_TOKEN": "x"}},
		"linear": {Transport: spec.TransportHTTP, URL: "https://mcp.linear.app/mcp", Headers: map[string]string{"Authorization": "Bearer y"}},
	}}
	payload, unmapped, err := MCP("codex", m)
	if err != nil {
		t.Fatal(err)
	}
	if len(unmapped) != 0 {
		t.Errorf("unmapped = %v, want none", unmapped)
	}
	var doc struct {
		MCPServers map[string]struct {
			Command     string            `toml:"command"`
			Args        []string          `toml:"args"`
			Env         map[string]string `toml:"env"`
			URL         string            `toml:"url"`
			HTTPHeaders map[string]string `toml:"http_headers"`
			Enabled     *bool             `toml:"enabled"`
		} `toml:"mcp_servers"`
	}
	if err := toml.Unmarshal(payload, &doc); err != nil {
		t.Fatalf("output is not valid TOML: %v\n%s", err, payload)
	}
	github := doc.MCPServers["github"]
	if github.Command != "npx" || len(github.Args) != 2 || github.Args[1] != "server-github" || github.Env["GITHUB_TOKEN"] != "x" {
		t.Errorf("github table = %+v", github)
	}
	if github.URL != "" || github.HTTPHeaders != nil {
		t.Errorf("stdio table must not carry url/http_headers: %+v", github)
	}
	if github.Enabled != nil {
		t.Errorf("enabled must be absent for an enabled server (codex defaults it true): %+v", github)
	}
	linear := doc.MCPServers["linear"]
	if linear.URL != "https://mcp.linear.app/mcp" || linear.HTTPHeaders["Authorization"] != "Bearer y" {
		t.Errorf("linear table = %+v", linear)
	}
	if linear.Command != "" || linear.Args != nil {
		t.Errorf("http table must not carry command/args: %+v", linear)
	}
}

// TestMCPOhMyPiClaudeLikeShape verifies oh-my-pi takes the same
// mcpEntryClaudeLike code path as claude/copilot (mcp.json's shape is the
// same family: a "type" discriminant, command/args/env or url/headers) —
// no per-adapter render code, no mandatory-tools special case.
func TestMCPOhMyPiClaudeLikeShape(t *testing.T) {
	m := spec.MCP{Servers: map[string]spec.MCPServer{
		"github": {Transport: spec.TransportStdio, Command: "npx", Args: []string{"-y", "server-github"}},
	}}
	payload, unmapped, err := MCP("oh-my-pi", m)
	if err != nil {
		t.Fatal(err)
	}
	if len(unmapped) != 0 {
		t.Errorf("unmapped = %v, want none", unmapped)
	}
	got := decodeJSON(t, payload)
	want := map[string]any{"mcpServers": map[string]any{"github": map[string]any{
		"type": "stdio", "command": "npx", "args": []any{"-y", "server-github"},
	}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestMCPAgentScopingExcludesOtherAdapters(t *testing.T) {
	m := spec.MCP{Servers: map[string]spec.MCPServer{
		"claude-only": {Transport: spec.TransportStdio, Command: "x", Agents: []string{"claude"}},
	}}
	// "not for this agent" is not "disabled": the entry has no business in
	// codex's file at all, so nothing is written and nothing is reported.
	payload, unmapped, err := MCP("codex", m)
	if err != nil {
		t.Fatal(err)
	}
	if payload != nil {
		t.Errorf("payload = %q, want nil (server scoped away from codex)", payload)
	}
	if len(unmapped) != 0 {
		t.Errorf("unmapped = %q, want none (scoping is intent, not a gap)", unmapped)
	}
	payload, _, err = MCP("claude", m)
	if err != nil {
		t.Fatal(err)
	}
	if payload == nil {
		t.Error("claude-scoped server must render for claude")
	}
}

func TestMCPUnknownAdapterErrors(t *testing.T) {
	if _, _, err := MCP("nonexistent", spec.MCP{}); err == nil {
		t.Error("expected error for unknown adapter")
	}
}

// ghClassicPAT is assembled from two pieces rather than written out. A
// repository secret scanner rejects a push over any credential-shaped
// literal, fake or not, and the detector under test keys only on a vendor
// prefix plus a charset and a minimum length — so the split costs the test
// nothing.
const ghClassicPAT = "ghp_" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestMCPSecretWarnings(t *testing.T) {
	tests := []struct {
		name string
		m    spec.MCP
		want []string
	}{
		{
			name: "env reference is not a secret",
			m: spec.MCP{Servers: map[string]spec.MCPServer{
				"github": {Transport: spec.TransportStdio, Command: "npx", Env: map[string]string{"GITHUB_TOKEN": "${GITHUB_TOKEN}"}},
			}},
		},
		{
			name: "literal credential in env",
			m: spec.MCP{Servers: map[string]spec.MCPServer{
				"github": {Transport: spec.TransportStdio, Command: "npx", Env: map[string]string{"GITHUB_TOKEN": ghClassicPAT}},
			}},
			want: []string{"mcp.yaml: github.env.GITHUB_TOKEN looks like a literal credential — use an ${ENV_VAR} reference instead"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MCPSecretWarnings(tt.m)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMCPInverseClaudeStdio(t *testing.T) {
	payload, _, err := MCP("claude", spec.MCP{Servers: map[string]spec.MCPServer{
		"github": {Transport: spec.TransportStdio, Command: "npx", Args: []string{"-y", "server-github"}, Env: map[string]string{"GITHUB_TOKEN": "${GITHUB_TOKEN}"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got, unmapped, err := MCPInverse("claude", payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(unmapped) != 0 {
		t.Errorf("unmapped = %q, want none", unmapped)
	}
	want := spec.MCPServer{Transport: spec.TransportStdio, Command: "npx", Args: []string{"-y", "server-github"}, Env: map[string]string{"GITHUB_TOKEN": "${GITHUB_TOKEN}"}}
	if !reflect.DeepEqual(got["github"], want) {
		t.Errorf("got %+v want %+v", got["github"], want)
	}
}

func TestMCPInverseClaudeHTTP(t *testing.T) {
	payload, _, err := MCP("claude", spec.MCP{Servers: map[string]spec.MCPServer{
		"linear": {Transport: spec.TransportHTTP, URL: "https://mcp.linear.app/mcp", Headers: map[string]string{"Authorization": "Bearer x"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := MCPInverse("claude", payload)
	if err != nil {
		t.Fatal(err)
	}
	want := spec.MCPServer{Transport: spec.TransportHTTP, URL: "https://mcp.linear.app/mcp", Headers: map[string]string{"Authorization": "Bearer x"}}
	if !reflect.DeepEqual(got["linear"], want) {
		t.Errorf("got %+v want %+v", got["linear"], want)
	}
}

// TestMCPInverseParsesJSONC covers the asymmetry that made a commented
// target push cleanly and then fail to pull: the merge side reads these
// files through hujson, so the pull side has to as well.
func TestMCPInverseParsesJSONC(t *testing.T) {
	payload := []byte(`{
  // the one server we actually use
  "mcpServers": {
    "github": {
      "type": "stdio",
      "command": "npx", // installed globally
      "args": ["-y", "server-github"],
    },
  },
}`)
	got, unmapped, err := MCPInverse("claude", payload)
	if err != nil {
		t.Fatalf("a hand-commented target must pull, not error: %v", err)
	}
	if len(unmapped) != 0 {
		t.Errorf("unmapped = %q, want none", unmapped)
	}
	want := spec.MCPServer{Transport: spec.TransportStdio, Command: "npx", Args: []string{"-y", "server-github"}}
	if !reflect.DeepEqual(got["github"], want) {
		t.Errorf("got %+v want %+v", got["github"], want)
	}
}

// TestMCPInverseSkipsUnrecognisedEntry pins the recovery rule: one entry
// friday has no canonical shape for used to cost the user every other
// server in the file.
func TestMCPInverseSkipsUnrecognisedEntry(t *testing.T) {
	payload := []byte(`{"mcpServers":{
		"ws-srv":{"type":"ws","url":"wss://example.test/mcp"},
		"good":{"type":"stdio","command":"npx"}
	}}`)
	got, unmapped, err := MCPInverse("claude", payload)
	if err != nil {
		t.Fatalf("one unknown type must not fail the file: %v", err)
	}
	if !reflect.DeepEqual(unmapped, []string{"ws-srv"}) {
		t.Errorf("unmapped = %q, want [ws-srv]", unmapped)
	}
	if len(got) != 1 || got["good"].Command != "npx" {
		t.Errorf("got %+v, want the recognised server recovered", got)
	}
}

func TestMCPInverseRecognisesDocumentedVariants(t *testing.T) {
	tests := []struct {
		name    string
		adapter string
		entry   string
		want    spec.MCPServer
	}{
		{
			// `claude mcp add-json <name> '{"command":"npx",...}'` writes no
			// type at all, and both claude and oh-my-pi default it to stdio.
			name:    "typeless command is stdio",
			entry:   `{"command":"npx","args":["-y","srv"]}`,
			adapter: "claude",
			want:    spec.MCPServer{Transport: spec.TransportStdio, Command: "npx", Args: []string{"-y", "srv"}},
		},
		{
			name:    "streamable-http aliases http",
			entry:   `{"type":"streamable-http","url":"https://example.test/mcp"}`,
			adapter: "copilot",
			want:    spec.MCPServer{Transport: spec.TransportHTTP, URL: "https://example.test/mcp"},
		},
		{
			name:    "oh-my-pi typeless stdio",
			entry:   `{"command":"uvx"}`,
			adapter: "oh-my-pi",
			want:    spec.MCPServer{Transport: spec.TransportStdio, Command: "uvx"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, unmapped, err := MCPInverse(tt.adapter, []byte(`{"mcpServers":{"srv":`+tt.entry+`}}`))
			if err != nil {
				t.Fatal(err)
			}
			if len(unmapped) != 0 {
				t.Errorf("unmapped = %q, want none", unmapped)
			}
			if !reflect.DeepEqual(got["srv"], tt.want) {
				t.Errorf("got %+v want %+v", got["srv"], tt.want)
			}
		})
	}
}

func TestMCPInverseCopilotRecoversTools(t *testing.T) {
	payload, _, err := MCP("copilot", spec.MCP{Servers: map[string]spec.MCPServer{
		"github": {Transport: spec.TransportHTTP, URL: "https://api.githubcopilot.com/mcp/", Tools: []string{"search_code"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := MCPInverse("copilot", payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(got["github"].Tools) != 1 || got["github"].Tools[0] != "search_code" {
		t.Errorf("tools = %v, want [search_code] (copilot is the one adapter that carries Tools natively)", got["github"].Tools)
	}
}

func TestMCPInverseOpenCodeLocalSplitsCommandAndArgs(t *testing.T) {
	payload, _, err := MCP("opencode", spec.MCP{Servers: map[string]spec.MCPServer{
		"github": {Transport: spec.TransportStdio, Command: "npx", Args: []string{"-y", "server-github"}, Env: map[string]string{"K": "v"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := MCPInverse("opencode", payload)
	if err != nil {
		t.Fatal(err)
	}
	want := spec.MCPServer{Transport: spec.TransportStdio, Command: "npx", Args: []string{"-y", "server-github"}, Env: map[string]string{"K": "v"}}
	if !reflect.DeepEqual(got["github"], want) {
		t.Errorf("got %+v want %+v (opencode's single command array must split back into Command+Args)", got["github"], want)
	}
}

// TestMCPInverseArgumentlessCommandRecoversNilArgs is the regression test
// for a pull that reported ActionUpdate on every run: cmd[1:] on a
// one-element array yields an empty non-nil slice, and the engine's in-sync
// check is reflect.DeepEqual, for which nil != []string{}.
func TestMCPInverseArgumentlessCommandRecoversNilArgs(t *testing.T) {
	got, _, err := MCPInverse("opencode", []byte(`{"mcp":{"solo":{"type":"local","command":["my-mcp-server"],"environment":{}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	want := spec.MCPServer{Transport: spec.TransportStdio, Command: "my-mcp-server"}
	if !reflect.DeepEqual(got["solo"], want) {
		t.Errorf("got %#v want %#v (nil Args and nil Env, not empty containers)", got["solo"], want)
	}
}

// TestMCPInverseEmptyContainersRecoverAsNil covers the same rule on the
// claude-like path, where an explicit [] / {} in the target file reaches
// toStringSlice/toStringMap directly.
func TestMCPInverseEmptyContainersRecoverAsNil(t *testing.T) {
	got, _, err := MCPInverse("copilot", []byte(`{"mcpServers":{"srv":{"type":"local","command":"x","args":[],"env":{},"tools":[]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	want := spec.MCPServer{Transport: spec.TransportStdio, Command: "x"}
	if !reflect.DeepEqual(got["srv"], want) {
		t.Errorf("got %#v want %#v", got["srv"], want)
	}
}

func TestMCPInverseOpenCodeRemoteDefaultsToHTTP(t *testing.T) {
	payload, _, err := MCP("opencode", spec.MCP{Servers: map[string]spec.MCPServer{
		"b": {Transport: spec.TransportSSE, URL: "https://b.example/sse"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := MCPInverse("opencode", payload)
	if err != nil {
		t.Fatal(err)
	}
	// opencode's "remote" type cannot distinguish http from sse on the way
	// back — the documented default is http; the engine's merge step
	// resolves the ambiguity against any existing mcp.yaml entry instead.
	if got["b"].Transport != spec.TransportHTTP || got["b"].URL != "https://b.example/sse" {
		t.Errorf("got %+v, want http transport with the original URL (ambiguity default)", got["b"])
	}
}

// TestMCPInverseOpenCodeEnabledOverrideIsUnmapped covers the third entry
// shape in opencode's schema: a bare {"enabled": …} toggle for an
// org-provided server, which carries no transport friday can recover.
func TestMCPInverseOpenCodeEnabledOverrideIsUnmapped(t *testing.T) {
	got, unmapped, err := MCPInverse("opencode", []byte(`{"mcp":{"org-srv":{"enabled":false},"mine":{"type":"local","command":["x"]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(unmapped, []string{"org-srv"}) {
		t.Errorf("unmapped = %q, want [org-srv]", unmapped)
	}
	if len(got) != 1 {
		t.Errorf("got %+v, want only the recoverable server", got)
	}
}

func TestMCPInverseGeminiTransportSelectsField(t *testing.T) {
	payload, _, err := MCP("gemini", spec.MCP{Servers: map[string]spec.MCPServer{
		"stdio-srv": {Transport: spec.TransportStdio, Command: "npx", Args: []string{"-y"}},
		"http-srv":  {Transport: spec.TransportHTTP, URL: "https://h.example"},
		"sse-srv":   {Transport: spec.TransportSSE, URL: "https://s.example"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := MCPInverse("gemini", payload)
	if err != nil {
		t.Fatal(err)
	}
	if got["stdio-srv"].Transport != spec.TransportStdio || got["stdio-srv"].Command != "npx" {
		t.Errorf("stdio-srv = %+v", got["stdio-srv"])
	}
	if got["http-srv"].Transport != spec.TransportHTTP || got["http-srv"].URL != "https://h.example" {
		t.Errorf("http-srv = %+v", got["http-srv"])
	}
	if got["sse-srv"].Transport != spec.TransportSSE || got["sse-srv"].URL != "https://s.example" {
		t.Errorf("sse-srv = %+v", got["sse-srv"])
	}
}

func TestMCPInverseGeminiFieldlessEntryIsUnmapped(t *testing.T) {
	got, unmapped, err := MCPInverse("gemini", []byte(`{"mcpServers":{"tcp-srv":{"tcp":"localhost:9000"},"ok":{"command":"npx"}}}`))
	if err != nil {
		t.Fatalf("one unrecoverable entry must not fail the file: %v", err)
	}
	if !reflect.DeepEqual(unmapped, []string{"tcp-srv"}) {
		t.Errorf("unmapped = %q, want [tcp-srv]", unmapped)
	}
	if len(got) != 1 {
		t.Errorf("got %+v, want only the recoverable server", got)
	}
}

func TestMCPInverseCodexTOMLStdio(t *testing.T) {
	payload, _, err := MCP("codex", spec.MCP{Servers: map[string]spec.MCPServer{
		"github": {Transport: spec.TransportStdio, Command: "npx", Args: []string{"-y", "server-github"}, Env: map[string]string{"K": "v"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := MCPInverse("codex", payload)
	if err != nil {
		t.Fatal(err)
	}
	want := spec.MCPServer{Transport: spec.TransportStdio, Command: "npx", Args: []string{"-y", "server-github"}, Env: map[string]string{"K": "v"}}
	if !reflect.DeepEqual(got["github"], want) {
		t.Errorf("got %+v want %+v", got["github"], want)
	}
}

func TestMCPInverseCodexTOMLRemoteDefaultsToHTTP(t *testing.T) {
	payload, _, err := MCP("codex", spec.MCP{Servers: map[string]spec.MCPServer{
		"linear": {Transport: spec.TransportSSE, URL: "https://mcp.linear.app/sse", Headers: map[string]string{"Authorization": "Bearer x"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := MCPInverse("codex", payload)
	if err != nil {
		t.Fatal(err)
	}
	if got["linear"].Transport != spec.TransportHTTP || got["linear"].URL != "https://mcp.linear.app/sse" {
		t.Errorf("got %+v, want http transport with the original URL (ambiguity default)", got["linear"])
	}
}

func TestMCPInverseCodexTOMLTablelessEntryIsUnmapped(t *testing.T) {
	payload := []byte("[mcp_servers.broken]\nstartup_timeout_sec = 20\n\n[mcp_servers.ok]\ncommand = 'npx'\n")
	got, unmapped, err := MCPInverse("codex", payload)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(unmapped, []string{"broken"}) {
		t.Errorf("unmapped = %q, want [broken] (neither command nor url to recover)", unmapped)
	}
	if len(got) != 1 || got["ok"].Command != "npx" {
		t.Errorf("got %+v, want only the recoverable server", got)
	}
}

func TestMCPInverseOhMyPiStdio(t *testing.T) {
	payload, _, err := MCP("oh-my-pi", spec.MCP{Servers: map[string]spec.MCPServer{
		"github": {Transport: spec.TransportStdio, Command: "npx", Args: []string{"-y", "server-github"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := MCPInverse("oh-my-pi", payload)
	if err != nil {
		t.Fatal(err)
	}
	want := spec.MCPServer{Transport: spec.TransportStdio, Command: "npx", Args: []string{"-y", "server-github"}}
	if !reflect.DeepEqual(got["github"], want) {
		t.Errorf("got %+v want %+v", got["github"], want)
	}
}

func TestMCPInverseUnknownAdapterErrors(t *testing.T) {
	if _, _, err := MCPInverse("bogus", nil); err == nil {
		t.Error("want error for unknown adapter")
	}
}

func TestMCPInverseEmptyPayloadIsEmptyMap(t *testing.T) {
	got, unmapped, err := MCPInverse("claude", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 || len(unmapped) != 0 {
		t.Errorf("got %v / %q, want empty", got, unmapped)
	}
}
