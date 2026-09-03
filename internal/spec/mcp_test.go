package spec

import (
	"strings"
	"testing"
)

func TestParseMCPValidStdio(t *testing.T) {
	m, err := ParseMCP([]byte(`
version: 1
servers:
  github:
    transport: stdio
    command: npx
    args: ["-y", "@modelcontextprotocol/server-github"]
    env: { GITHUB_TOKEN: "${GITHUB_TOKEN}" }
`))
	if err != nil {
		t.Fatal(err)
	}
	srv, ok := m.Servers["github"]
	if !ok {
		t.Fatal("missing github server")
	}
	if srv.Transport != TransportStdio || srv.Command != "npx" {
		t.Errorf("got %+v", srv)
	}
	if len(srv.Args) != 2 || srv.Args[0] != "-y" {
		t.Errorf("args = %v", srv.Args)
	}
	if srv.Env["GITHUB_TOKEN"] != "${GITHUB_TOKEN}" {
		t.Errorf("env = %v", srv.Env)
	}
}

func TestParseMCPValidHTTP(t *testing.T) {
	m, err := ParseMCP([]byte(`
version: 1
servers:
  linear:
    transport: http
    url: https://mcp.linear.app/sse
    headers: { Authorization: "Bearer ${LINEAR_KEY}" }
`))
	if err != nil {
		t.Fatal(err)
	}
	if m.Servers["linear"].URL != "https://mcp.linear.app/sse" {
		t.Errorf("got %+v", m.Servers["linear"])
	}
}

func TestParseMCPVersionDefaultsAndRejects(t *testing.T) {
	m, err := ParseMCP([]byte("servers: {}\n"))
	if err != nil || m.Version != 1 {
		t.Fatalf("version default: m=%+v err=%v", m, err)
	}
	if _, err := ParseMCP([]byte("version: 2\nservers: {}\n")); err == nil {
		t.Error("expected error for unsupported version")
	}
}

func TestParseMCPRejectsMissingTransport(t *testing.T) {
	_, err := ParseMCP([]byte("servers:\n  x:\n    command: npx\n"))
	if err == nil || !strings.Contains(err.Error(), "transport is required") {
		t.Errorf("err = %v", err)
	}
}

func TestParseMCPRejectsUnknownTransport(t *testing.T) {
	_, err := ParseMCP([]byte("servers:\n  x:\n    transport: carrier-pigeon\n"))
	if err == nil || !strings.Contains(err.Error(), "unknown transport") {
		t.Errorf("err = %v", err)
	}
}

func TestParseMCPRejectsStdioWithoutCommand(t *testing.T) {
	_, err := ParseMCP([]byte("servers:\n  x:\n    transport: stdio\n"))
	if err == nil || !strings.Contains(err.Error(), "requires command") {
		t.Errorf("err = %v", err)
	}
}

func TestParseMCPRejectsHTTPWithoutURL(t *testing.T) {
	_, err := ParseMCP([]byte("servers:\n  x:\n    transport: http\n"))
	if err == nil || !strings.Contains(err.Error(), "requires url") {
		t.Errorf("err = %v", err)
	}
}

func TestParseMCPRejectsUnknownTopLevelField(t *testing.T) {
	_, err := ParseMCP([]byte("version: 1\nserverz: {}\n"))
	if err == nil || !strings.Contains(err.Error(), `unknown field "serverz"`) || !strings.Contains(err.Error(), "line 2") {
		t.Errorf("err = %v, want the key and line named", err)
	}
}

func TestParseMCPRejectsUnknownServerField(t *testing.T) {
	_, err := ParseMCP([]byte("servers:\n  x:\n    transport: stdio\n    command: npx\n    argz: []\n"))
	if err == nil || !strings.Contains(err.Error(), `unknown field "argz"`) {
		t.Errorf("err = %v, want the key named", err)
	}
	if strings.Contains(err.Error(), "spec.") {
		t.Errorf("err = %q, want no Go type name", err)
	}
}

// An unknown agents: entry removes the server from every adapter with no
// diagnostic anywhere: AppliesTo simply never matches, and render skips a
// non-applying server silently by design.
func TestParseMCPRejectsUnknownAgent(t *testing.T) {
	_, err := ParseMCP([]byte("servers:\n  x:\n    transport: stdio\n    command: npx\n    agents: [claud]\n"))
	if err == nil || !strings.Contains(err.Error(), `unknown agent "claud"`) {
		t.Errorf("err = %v, want the typo'd agent named", err)
	}
	if !strings.Contains(err.Error(), "oh-my-pi") {
		t.Errorf("err = %v, want the valid set listed", err)
	}
}

func TestParseMCPAcceptsEveryAdapterInAgents(t *testing.T) {
	for _, adapter := range Adapters {
		if _, err := ParseMCP([]byte("servers:\n  x:\n    transport: stdio\n    command: npx\n    agents: [" + adapter + "]\n")); err != nil {
			t.Errorf("agents: [%s]: %v", adapter, err)
		}
	}
}

// A stray trailing colon produces one, and every adapter keys its native
// MCP config by this name.
func TestParseMCPRejectsEmptyServerName(t *testing.T) {
	_, err := ParseMCP([]byte("servers:\n  \"\":\n    transport: stdio\n    command: npx\n"))
	if err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Errorf("err = %v", err)
	}
	if _, err := ParseMCP([]byte("servers:\n  \"  \":\n    transport: stdio\n    command: npx\n")); err == nil {
		t.Error("expected error for a whitespace-only server name")
	}
}

func TestParseMCPRejectsSecondDocument(t *testing.T) {
	_, err := ParseMCP([]byte("version: 1\nservers: {}\n---\nservers:\n  x:\n    transport: stdio\n    command: npx\n"))
	if err == nil || !strings.Contains(err.Error(), "single YAML document") {
		t.Errorf("err = %v, want a multi-document refusal", err)
	}
}

func TestParseMCPRejectsWrongScalarType(t *testing.T) {
	_, err := ParseMCP([]byte("servers: \"github\"\n"))
	if err == nil || !strings.Contains(err.Error(), "cannot unmarshal") {
		t.Errorf("err = %v", err)
	}
}

func TestParseMCPErrorDoesNotNameTheFile(t *testing.T) {
	_, err := ParseMCP([]byte("servers:\n  x:\n    transport: carrier-pigeon\n"))
	if err == nil || strings.Contains(err.Error(), "mcp.yaml") {
		t.Errorf("err = %v, want no filename prefix (the caller supplies the path)", err)
	}
}

func TestMCPServerIsEnabled(t *testing.T) {
	unset := MCPServer{}
	if !unset.IsEnabled() {
		t.Error("unset Enabled should default to true")
	}
	no := false
	disabled := MCPServer{Enabled: &no}
	if disabled.IsEnabled() {
		t.Error("explicit false should report disabled")
	}
	yes := true
	enabled := MCPServer{Enabled: &yes}
	if !enabled.IsEnabled() {
		t.Error("explicit true should report enabled")
	}
}

func TestMCPServerEffectiveTools(t *testing.T) {
	if got := (MCPServer{}).EffectiveTools(); len(got) != 1 || got[0] != "*" {
		t.Errorf("empty Tools = %v, want wildcard default", got)
	}
	explicit := MCPServer{Tools: []string{"search", "read"}}
	if got := explicit.EffectiveTools(); len(got) != 2 {
		t.Errorf("explicit Tools = %v, want passthrough", got)
	}
}

func TestMCPServerAppliesTo(t *testing.T) {
	allAgents := MCPServer{}
	if !allAgents.AppliesTo("codex") {
		t.Error("empty Agents should apply to every adapter")
	}
	restricted := MCPServer{Agents: []string{"claude", "codex"}}
	if !restricted.AppliesTo("claude") || restricted.AppliesTo("oh-my-pi") {
		t.Errorf("AppliesTo mismatch for restricted allowlist")
	}
}
