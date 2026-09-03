package spec

import (
	"errors"
	"strings"
)

// AgentDoc is the neutral shape lifted from an agents/*.md source file: its
// name/description frontmatter and its body (the agent's instructions).
// render.CodexAgent projects it onto Codex's subagent TOML shape. Unlike MCP
// and Permissions, there is no ParseAgentDoc here — extracting frontmatter
// from markdown is internal/frontmatter's job, and internal/engine's
// planRender assembles the AgentDoc from it.
type AgentDoc struct {
	Name        string
	Description string
	Body        string
}

// Validate rejects an agent file that carries frontmatter and nothing else.
// Codex's subagent shape has no omitempty on its instructions field, so an
// empty Body renders a syntactically valid subagent that instructs the model
// to do nothing — and friday reports a successful push. Nothing between the
// store and the target catches it otherwise.
func (d AgentDoc) Validate() error {
	if strings.TrimSpace(d.Body) == "" {
		return errors.New("no instructions: the file has frontmatter but an empty body")
	}
	return nil
}

// CommandDoc is the neutral shape lifted from a commands/*.md source file.
// render.GeminiCommand projects it onto Gemini CLI's custom command TOML
// shape (description + prompt), which the built-in preset lands at
// ~/.gemini/commands/<stem>.toml.
type CommandDoc struct {
	Description string
	Body        string
}
