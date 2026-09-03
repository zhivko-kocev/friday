package render

import (
	"github.com/pelletier/go-toml/v2"
	"github.com/zhivko-kocev/friday/internal/spec"
)

// codexSubagent is one Codex subagent TOML file's shape:
// https://learn.chatgpt.com/docs/agent-configuration/subagents
//
// No omitempty on any field: Codex's custom-agent file schema marks all
// three Required=Yes ("Every standalone custom agent file must define:
// name, description, developer_instructions"), and it rejects a file that
// leaves one out. Gemini's command files are the opposite — description is
// explicitly optional there (see geminiCommandFile) — so the two surfaces
// must not share a tag convention.
type codexSubagent struct {
	Name                  string `toml:"name"`
	Description           string `toml:"description"`
	DeveloperInstructions string `toml:"developer_instructions,multiline"`
}

// CodexAgent renders rec as a Codex subagent TOML file. The multiline tag
// only takes effect when Body actually contains a newline, so a one-line
// instruction body stays a plain quoted string instead of an unnecessary
// """...""" block.
func CodexAgent(rec spec.AgentDoc) ([]byte, error) {
	return toml.Marshal(codexSubagent{
		Name:                  rec.Name,
		Description:           rec.Description,
		DeveloperInstructions: rec.Body,
	})
}
