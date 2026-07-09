package engine

import (
	"strings"

	"github.com/zhivko-kocev/friday/internal/frontmatter"
)

// mdRecord is the neutral shape lifted from a Claude-style agent markdown file:
// its name/description frontmatter and its body (the agent's instructions).
type mdRecord struct {
	name        string
	description string
	body        string
}

// parseAgentMD reads name/description from the frontmatter (falling back to the
// file stem for name) and returns the trimmed body. replace is applied to the
// body so ${CLAUDE_PLUGIN_ROOT} references resolve to the store, like copied
// content elsewhere.
func parseAgentMD(raw []byte, stem string, replace func([]byte) []byte) mdRecord {
	fields, body := frontmatter.Parse(string(raw))
	rec := mdRecord{name: stem, body: strings.TrimSpace(string(replace([]byte(body))))}
	if s, ok := fields["name"].(string); ok && strings.TrimSpace(s) != "" {
		rec.name = strings.TrimSpace(s)
	}
	if s, ok := fields["description"].(string); ok {
		rec.description = strings.TrimSpace(s)
	}
	return rec
}

// mdToTOML renders the record as a Codex subagent TOML file (name, description,
// developer_instructions). https://learn.chatgpt.com/docs/agent-configuration/subagents
func mdToTOML(rec mdRecord) []byte {
	var b strings.Builder
	b.WriteString("name = " + tomlBasicString(rec.name) + "\n")
	if rec.description != "" {
		b.WriteString("description = " + tomlBasicString(rec.description) + "\n")
	}
	b.WriteString("developer_instructions = " + tomlMultiline(rec.body) + "\n")
	return []byte(b.String())
}

// mdToJSON renders the record as an Antigravity subagent agent.json.
//
// LOW CONFIDENCE: Antigravity's docs are a client-rendered SPA, so this schema
// is a best guess — name/description are per the audit, and the instructions
// land under "prompt". Verify the real field names on a live install before
// relying on it (see ROADMAP).
func mdToJSON(rec mdRecord) ([]byte, error) {
	obj := map[string]any{"name": rec.name, "prompt": rec.body}
	if rec.description != "" {
		obj["description"] = rec.description
	}
	return marshalCanonical(obj)
}

// tomlBasicString quotes s as a single-line TOML basic string, escaping the
// characters the spec requires.
func tomlBasicString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// tomlMultiline renders s as a TOML multiline basic string. Backslash and
// double-quote are escaped so no accidental `"""` can close the string early
// and the value decodes back to s exactly; TOML trims the newline right after
// the opening delimiter, so the added one just keeps the body on its own line.
func tomlMultiline(s string) string {
	esc := strings.ReplaceAll(s, `\`, `\\`)
	esc = strings.ReplaceAll(esc, `"`, `\"`)
	return "\"\"\"\n" + esc + "\"\"\""
}
