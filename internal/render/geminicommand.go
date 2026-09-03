package render

import (
	"github.com/pelletier/go-toml/v2"
	"github.com/zhivko-kocev/friday/internal/spec"
)

// geminiCommandFile is one Gemini CLI custom command TOML file's shape —
// {project-or-user}/.gemini/commands/<name>.toml:
// https://github.com/google-gemini/gemini-cli/blob/main/docs/cli/custom-commands.md
//
// description keeps omitempty because Gemini documents it optional ("If you
// omit this field, a generic description will be generated from the
// filename") — the opposite of Codex's subagent files, where all three keys
// are required (see codexSubagent).
type geminiCommandFile struct {
	Description string `toml:"description,omitempty"`
	Prompt      string `toml:"prompt,multiline"`
}

// GeminiCommand renders rec as a Gemini CLI custom command TOML file. rec.Body
// becomes prompt verbatim — friday does not translate argument-placeholder
// syntax between agents, so a body written for Gemini's own {{args}}
// convention passes through unchanged, and one written for another agent's
// convention (e.g. Claude's $ARGUMENTS) needs the author to adapt it.
func GeminiCommand(rec spec.CommandDoc) ([]byte, error) {
	return toml.Marshal(geminiCommandFile{
		Description: rec.Description,
		Prompt:      rec.Body,
	})
}
