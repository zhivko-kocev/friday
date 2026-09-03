package engine

import (
	"strings"

	"github.com/zhivko-kocev/friday/internal/frontmatter"
	"github.com/zhivko-kocev/friday/internal/spec"
)

// parseAgentMD lifts a Claude-style agent markdown file into the neutral
// AgentDoc the codex-agent render surface consumes: name/description from the
// frontmatter (name falling back to the file stem) plus the trimmed body.
// replace is applied to the body so ${CLAUDE_PLUGIN_ROOT} references resolve
// to the store, like copied content elsewhere.
func parseAgentMD(raw []byte, stem string, replace func([]byte) []byte) spec.AgentDoc {
	fields, body := frontmatter.Parse(string(raw))
	doc := spec.AgentDoc{Name: stem, Body: strings.TrimSpace(string(replace([]byte(body))))}
	if s, ok := fields["name"].(string); ok && strings.TrimSpace(s) != "" {
		doc.Name = strings.TrimSpace(s)
	}
	if s, ok := fields["description"].(string); ok {
		doc.Description = strings.TrimSpace(s)
	}
	return doc
}
