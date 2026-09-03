package render

import (
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/zhivko-kocev/friday/internal/spec"
)

// decodeTOML decodes a rendered TOML payload into a generic map so a test
// can assert on the keys themselves rather than searching the rendered text,
// where a body containing the word "description" would be indistinguishable
// from the description key.
func decodeTOML(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var v map[string]any
	if err := toml.Unmarshal(data, &v); err != nil {
		t.Fatalf("invalid TOML: %v\n%s", err, data)
	}
	return v
}

func TestCodexAgentFieldsAndMultilineBody(t *testing.T) {
	rec := spec.AgentDoc{
		Name:        "architect",
		Description: "Plans before building",
		Body:        "You are the architect.\n\nThink before you code.",
	}
	payload, err := CodexAgent(rec)
	if err != nil {
		t.Fatal(err)
	}
	got := decodeTOML(t, payload)
	want := map[string]any{
		"name":                   rec.Name,
		"description":            rec.Description,
		"developer_instructions": rec.Body,
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %#v, want %#v", k, got[k], w)
		}
	}
	// A multi-line body should use a """...""" block, not an escaped \n.
	if !strings.Contains(string(payload), `developer_instructions = """`) {
		t.Errorf("multi-line body not rendered as a TOML multiline string:\n%s", payload)
	}
}

func TestCodexAgentSingleLineBodyStaysPlainString(t *testing.T) {
	payload, err := CodexAgent(spec.AgentDoc{Name: "x", Body: "one line"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), `developer_instructions = """`) {
		t.Errorf("single-line body should not use a multiline block:\n%s", payload)
	}
}

// TestCodexAgentAlwaysWritesRequiredKeys pins the fix for a file Codex
// rejects: its custom-agent schema marks name, description and
// developer_instructions all Required, so an agents/*.md source with no
// description: frontmatter must still emit the key. This test replaces
// TestCodexAgentOmitsEmptyDescription, which asserted the defect.
func TestCodexAgentAlwaysWritesRequiredKeys(t *testing.T) {
	payload, err := CodexAgent(spec.AgentDoc{Name: "x", Body: "b"})
	if err != nil {
		t.Fatal(err)
	}
	got := decodeTOML(t, payload)
	for _, key := range []string{"name", "description", "developer_instructions"} {
		if _, ok := got[key]; !ok {
			t.Errorf("%s missing — Codex documents it required:\n%s", key, payload)
		}
	}
	if got["description"] != "" {
		t.Errorf("description = %#v, want the empty string friday was given", got["description"])
	}
}

// TestCodexAgentQuotingEdgeCases covers the bodies that break a naive TOML
// emitter. friday hands an arbitrary user-authored markdown body straight to
// the encoder, so the guarantee under test is round-trip fidelity, not the
// literal bytes: a run of three quotes, a trailing quote against the closing
// delimiter, a Windows path's backslashes, and CRLF input.
func TestCodexAgentQuotingEdgeCases(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"triple quote", "before\n\"\"\"\nafter"},
		{"trailing quotes", "line one\nends with two quotes\"\""},
		{"backslashes", "run C:\\tools\\srv.exe\nthen stop"},
		{"crlf", "first line\r\nsecond line"},
		{"single line with quote", `say "hi"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, err := CodexAgent(spec.AgentDoc{Name: "x", Description: "d", Body: tt.body})
			if err != nil {
				t.Fatal(err)
			}
			got := decodeTOML(t, payload)
			if got["developer_instructions"] != tt.body {
				t.Errorf("body did not round-trip\n got %q\nwant %q\nrendered:\n%s", got["developer_instructions"], tt.body, payload)
			}
		})
	}
}
