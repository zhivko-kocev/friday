// Package session gives friday a read-only, cross-agent view of coding-agent
// transcripts — the analysis half of "friday should be the single point
// where a developer analyses agent work and compiles a session from one
// agent to another." friday never writes into any agent's session store
// (D4): every Reader here only ever opens files an agent already wrote, or
// shells out to that agent's own export command.
//
// Six agents, six incompatible on-disk formats, one shared vocabulary: field
// names on Record deliberately match the ccusage / OTel-GenAI vocabulary
// (D28) so friday's --json output is readable by anyone who already uses
// those tools. No agent's own cost field is uniformly trustworthy, so
// CostUSD is reported only when a reader's transcript states it directly —
// never computed from a pricing table, and never rendered as a bare 0
// (CostKnown distinguishes "free" from "unknown").
package session

import (
	"encoding/json"
	"slices"
	"time"
)

// Record is one session's metadata — cheap to produce for every session in
// List(), without parsing every turn.
type Record struct {
	Agent   string `json:"agent"`
	ID      string `json:"id"`
	Path    string `json:"path,omitempty"`
	Project string `json:"project,omitempty"`
	Model   string `json:"model,omitempty"`

	Started time.Time `json:"started"`
	Updated time.Time `json:"updated"`

	// Turns is the number of chat turns in the transcript, and every
	// reader must set it to exactly len(Transcript.Turns) — a raw
	// transcript-line count is not comparable across agents, and this
	// number is printed side by side for all six in `friday sessions`
	// and folded into its --stats aggregate.
	Turns int      `json:"turns"`
	Tools []string `json:"tools,omitempty"` // distinct, sorted
	Files []string `json:"files,omitempty"` // distinct, sorted

	// Token counts and cost. Field names match the ccusage / OTel-GenAI
	// vocabulary verbatim (D28): gen_ai.usage.input_tokens,
	// gen_ai.usage.output_tokens, and what ccusage/sniffly report as
	// cache_creation_tokens / cache_read_tokens / total_tokens.
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheCreationTokens int `json:"cache_creation_tokens"`
	CacheReadTokens     int `json:"cache_read_tokens"`
	TotalTokens         int `json:"total_tokens"`

	CostUSD   float64 `json:"-"` // only when the transcript itself states it; see MarshalJSON
	CostKnown bool    `json:"-"` // false => CostUSD omitted from JSON entirely, never rendered as 0

	// UnreadableLines counts transcript lines a reader could not parse as a
	// known record shape. A reader never fails List() or Load() over a
	// handful of bad lines — it counts them here and keeps going, so one
	// corrupt line never hides an otherwise-good session.
	UnreadableLines int `json:"unreadable_lines,omitempty"`
}

// recordJSON is Record's on-the-wire shape. CostUSD is a pointer here
// specifically so a session with no known cost omits the key entirely
// (via omitempty on a nil pointer) rather than ever writing a misleading
// "cost_usd": 0 that a ccusage-compatible consumer could mistake for a
// verified free session.
type recordJSON struct {
	Agent   string `json:"agent"`
	ID      string `json:"id"`
	Path    string `json:"path,omitempty"`
	Project string `json:"project,omitempty"`
	Model   string `json:"model,omitempty"`

	Started time.Time `json:"started"`
	Updated time.Time `json:"updated"`
	Turns   int       `json:"turns"`
	Tools   []string  `json:"tools,omitempty"`
	Files   []string  `json:"files,omitempty"`

	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheCreationTokens int `json:"cache_creation_tokens"`
	CacheReadTokens     int `json:"cache_read_tokens"`
	TotalTokens         int `json:"total_tokens"`

	CostUSD *float64 `json:"cost_usd,omitempty"`

	UnreadableLines int `json:"unreadable_lines,omitempty"`
}

func (r Record) toJSON() recordJSON {
	out := recordJSON{
		Agent: r.Agent, ID: r.ID, Path: r.Path, Project: r.Project, Model: r.Model,
		Started: r.Started, Updated: r.Updated,
		Turns: r.Turns, Tools: r.Tools, Files: r.Files,
		InputTokens: r.InputTokens, OutputTokens: r.OutputTokens,
		CacheCreationTokens: r.CacheCreationTokens, CacheReadTokens: r.CacheReadTokens,
		TotalTokens:     r.TotalTokens,
		UnreadableLines: r.UnreadableLines,
	}
	if r.CostKnown {
		cost := r.CostUSD
		out.CostUSD = &cost
	}
	return out
}

// MarshalJSON renders CostUSD only when CostKnown — see recordJSON.
func (r Record) MarshalJSON() ([]byte, error) { return json.Marshal(r.toJSON()) }

// Transcript is one session's full detail: its Record plus the ordered turn
// list. Turns shadows the embedded Record.Turns (an int count) with the
// full []Turn slice — Load callers want the list, List callers want the
// count, and Go's shallower-field-wins rule makes both spellings valid on
// whichever type actually has what the caller asked for. The two always
// agree numerically: Record.Turns == len(Transcript.Turns) for every
// reader.
type Transcript struct {
	Record
	Turns []Turn `json:"turns"`
}

// MarshalJSON is required because Record's own MarshalJSON would otherwise
// be promoted to Transcript unchanged (Go promotes methods of embedded
// types), silently dropping Transcript's own Turns field from the JSON
// output. transcriptJSON's embedded recordJSON.Turns (an int, promoted
// from Record) is shadowed by the outer, explicit []Turn field for the
// same "turns" key — the identical shadowing rule Transcript itself uses.
func (t Transcript) MarshalJSON() ([]byte, error) {
	type transcriptJSON struct {
		recordJSON
		Turns []Turn `json:"turns"`
	}
	return json.Marshal(transcriptJSON{recordJSON: t.Record.toJSON(), Turns: t.Turns})
}

// Turn is one exchange in a transcript: a user message, an assistant
// message, or a tool invocation. Tool and Files are populated only for
// turns that represent a tool call; a plain chat turn leaves both zero.
type Turn struct {
	Role  string    `json:"role"`
	Text  string    `json:"text,omitempty"`
	At    time.Time `json:"at"`
	Tool  string    `json:"tool,omitempty"`
	Files []string  `json:"files,omitempty"`
}

// The Turn.Role vocabulary every reader normalises to. Six agents name
// these differently on disk; friday's consumers (the sessions table, the
// handoff export's transcript tail, --json) branch on Role, so a reader
// that invents its own spelling silently breaks them. RoleTool is a tool
// *call*; RoleToolResult is that call's output — the distinction
// `friday sessions --open` needs to tell an interrupted tool loop from a
// cleanly finished session.
//
// The set is deliberately only what a reader can actually produce from a
// transcript. A wire role that denotes something else — gemini's
// "info"/"error"/"warning" UI notices, oh-my-pi's "custom", an
// agent-authored message with its own author name — is passed through
// verbatim, never flattened into one of these, because the distinction it
// carries is real and nothing here can represent it.
const (
	RoleUser       = "user"
	RoleAssistant  = "assistant"
	RoleReasoning  = "reasoning"
	RoleTool       = "tool"        // a tool call
	RoleToolResult = "tool_result" // that call's output
)

// filePathArgKeys are the tool-call argument names known to carry a file
// path, shared by every reader so `friday sessions`'s Files column has no
// per-agent blind spots. Claude Code's built-ins use file_path (Read /
// Write / Edit), path (Glob / Grep) and notebook_path (NotebookEdit);
// filePath is the camelCase spelling some MCP and third-party tools use.
// Order matters for readers that stop at the first hit. A tool whose
// arguments use none of these is left unattributed, never guessed
// (robustness rule 6).
var filePathArgKeys = []string{"file_path", "path", "filePath", "notebook_path"}

// sortedKeys returns m's keys in sorted order, or nil for an empty set so
// the corresponding omitempty JSON key disappears instead of rendering as
// an empty array.
func sortedKeys(m map[string]struct{}) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// sortedDistinct sorts s in place and drops duplicates, for readers that
// accumulate into a slice rather than a set.
func sortedDistinct(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	slices.Sort(s)
	return slices.Compact(s)
}

// Reader is one agent's session-store adapter. Every method is read-only.
type Reader interface {
	// Agent is this reader's fixed adapter name (e.g. "claude"), matching
	// the adapter names in internal/presets.
	Agent() string

	// Available reports whether this agent's session store can be read on
	// this machine. false is not an error: an agent that was never
	// installed, or whose export tool is missing from PATH, is reported
	// with a reason and skipped — it never fails the caller's command.
	Available() (bool, string)

	// List enumerates every session this reader can find, newest data
	// first is not guaranteed — callers sort. Each Record's UnreadableLines
	// reports lines List had to skip while computing Turns/Model/etc.; it
	// never causes List itself to fail.
	List() ([]Record, error)

	// Load reads one session's full transcript by its exact ID, as
	// returned by List — never a prefix. Prefix matching against the
	// combined index across every reader is the CLI layer's job, not a
	// Reader's, since disambiguating a collision needs the full session
	// list, not just this one reader's slice of it.
	Load(id string) (*Transcript, error)
}
