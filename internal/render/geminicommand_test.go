package render

import (
	"strings"
	"testing"

	"github.com/zhivko-kocev/friday/internal/spec"
)

func TestGeminiCommandFieldsAndMultilinePrompt(t *testing.T) {
	rec := spec.CommandDoc{
		Description: "Adds a changelog entry",
		Body:        "Add an entry to CHANGELOG.md.\n\nUse {{args}} as the version.",
	}
	payload, err := GeminiCommand(rec)
	if err != nil {
		t.Fatal(err)
	}
	got := decodeTOML(t, payload)
	if got["description"] != rec.Description {
		t.Errorf("description = %#v, want %#v", got["description"], rec.Description)
	}
	if got["prompt"] != rec.Body {
		t.Errorf("prompt = %#v, want %#v", got["prompt"], rec.Body)
	}
	if !strings.Contains(rec.Body, "{{args}}") || !strings.Contains(got["prompt"].(string), "{{args}}") {
		t.Error("argument placeholder must pass through untranslated")
	}
	if !strings.Contains(string(payload), `prompt = """`) {
		t.Errorf("multi-line prompt not rendered as a TOML multiline string:\n%s", payload)
	}
}

// TestGeminiCommandOmitsEmptyDescription asserts on the decoded key, not on
// the whole rendered file: the prompt body is user-authored and may itself
// contain the word "description".
func TestGeminiCommandOmitsEmptyDescription(t *testing.T) {
	payload, err := GeminiCommand(spec.CommandDoc{Body: "just a prompt about the description field"})
	if err != nil {
		t.Fatal(err)
	}
	got := decodeTOML(t, payload)
	if _, ok := got["description"]; ok {
		// Gemini documents description optional and generates one from the
		// filename when absent — the opposite of Codex's subagent files.
		t.Errorf("empty description should be omitted:\n%s", payload)
	}
}

// TestGeminiCommandQuotingEdgeCases mirrors TestCodexAgentQuotingEdgeCases:
// both surfaces hand an arbitrary user-authored body to the same encoder, so
// both need the same guard against an encoder swap regressing quoting.
func TestGeminiCommandQuotingEdgeCases(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"triple quote", "before\n\"\"\"\nafter"},
		{"trailing quotes", "line one\nends with two quotes\"\""},
		{"backslashes", "open C:\\tools\\srv.exe\nthen stop"},
		{"crlf", "first line\r\nsecond line"},
		{"shell injection block", "Summarize:\n\n!{grep -r {{args}} .}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, err := GeminiCommand(spec.CommandDoc{Description: "d", Body: tt.body})
			if err != nil {
				t.Fatal(err)
			}
			got := decodeTOML(t, payload)
			if got["prompt"] != tt.body {
				t.Errorf("prompt did not round-trip\n got %q\nwant %q\nrendered:\n%s", got["prompt"], tt.body, payload)
			}
		})
	}
}
