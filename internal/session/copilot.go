// Copilot CLI's own docs are explicit that its session-state format is an
// internal implementation detail with no stability contract: "Treat
// session-store.db and the files under session-state/ as internal
// implementation details. The schema and event format can change between
// Copilot releases." (docs.github.com/en/copilot/concepts/agents/copilot-cli/chronicle,
// and github/copilot-cli#3551). So this reader never binds to a struct that
// can fail a whole line on one unexpected field — every event is decoded as
// {type, timestamp, data} and Data is probed by key (robustness rule 4).
package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.yaml.in/yaml/v3"
)

const copilotAgent = "copilot"

// copilotEvent is one line of events.jsonl. The envelope shape
// {type, data, id, timestamp, parentId} is confirmed by multiple
// independent sources (github/copilot-cli#3551's reference gist, and
// jonmagic.com's writeup of real ~/.copilot/ session data). Data is decoded
// lazily per event, since which keys exist depends on Type.
type copilotEvent struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

// copilotWorkspace mirrors the handful of workspace.yaml keys documented in
// github/copilot-cli#2655 ("The workspace.yaml files ... correctly contain:
// cwd: ..., git_root: ..., branch: ..."). Sibling to events.jsonl in each
// session directory.
type copilotWorkspace struct {
	CWD     string `yaml:"cwd"`
	GitRoot string `yaml:"git_root"`
	Branch  string `yaml:"branch"`
}

// projectPath prefers the git repository root over the raw cwd: cwd can be
// a subdirectory of the project the session was started from, while
// git_root is the stable project identity. Falls back to cwd when git_root
// is empty (a non-git working directory).
func (w copilotWorkspace) projectPath() string {
	if w.GitRoot != "" {
		return w.GitRoot
	}
	return w.CWD
}

type copilotReader struct{}

// NewCopilotReader returns a Reader over GitHub Copilot CLI's local session
// store. It only ever opens files Copilot CLI already wrote — never writes
// into ~/.copilot itself (D4).
func NewCopilotReader() Reader { return copilotReader{} }

func (copilotReader) Agent() string { return copilotAgent }

// copilotHome resolves the Copilot CLI config root: $COPILOT_HOME when set
// (docs.github.com/en/copilot/reference/copilot-cli-reference/cli-config-dir-reference
// — "COPILOT_HOME replaces the entire ~/.copilot path"), else
// $HOME/.copilot (robustness rule 8).
func copilotHome() string {
	if v := os.Getenv("COPILOT_HOME"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".copilot")
}

// copilotStateDir is where Copilot CLI records sessions:
// <config-root>/session-state/<id>/{events.jsonl,workspace.yaml}
// ("session-state — Session history and workspace data", per
// docs.github.com/en/copilot/reference/copilot-cli-reference/cli-config-dir-reference).
func copilotStateDir() string {
	root := copilotHome()
	if root == "" {
		return ""
	}
	return filepath.Join(root, "session-state")
}

// Available probes the session-state directory, never the config root.
// ~/.copilot is one of friday's own push targets, so it exists — full of
// friday-written copilot-instructions.md, skills/, agents/ — on machines
// where Copilot CLI was never installed. Stat'ing it reported the reader as
// available and then returned zero sessions, which is indistinguishable from
// "Copilot recorded nothing" and is exactly the silent-zero failure a
// session reader must not have.
func (copilotReader) Available() (bool, string) {
	stateDir := copilotStateDir()
	if stateDir == "" {
		return false, "could not resolve home directory"
	}
	info, err := os.Stat(stateDir)
	if err != nil || !info.IsDir() {
		return false, stateDir + " not found"
	}
	return true, ""
}

// List enumerates every ~/.copilot/session-state/<id>/events.jsonl this
// reader can find. A session directory with no events.jsonl (an
// interrupted or otherwise incomplete session — Copilot's own docs describe
// events.jsonl as present "for active sessions") is silently skipped rather
// than treated as a corrupt session.
func (copilotReader) List() ([]Record, error) {
	stateDir := copilotStateDir()
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var records []Record
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		sessionDir := filepath.Join(stateDir, id)
		eventsPath := filepath.Join(sessionDir, "events.jsonl")

		info, statErr := os.Stat(eventsPath)
		if statErr != nil || info.IsDir() {
			continue
		}

		var sum copilotSummary
		unreadable, readErr := scanCopilotEvents(eventsPath, sum.add)
		if readErr != nil {
			continue // unreadable file (e.g. permissions) — skip, never fail the whole index
		}

		ws, _ := readCopilotWorkspace(sessionDir)
		records = append(records, sum.record(id, eventsPath, unreadable, info.ModTime(), ws.projectPath()))
	}
	return records, nil
}

// Load reads one session's full events.jsonl by its exact directory name.
func (copilotReader) Load(id string) (*Transcript, error) {
	sessionDir := filepath.Join(copilotStateDir(), id)
	eventsPath := filepath.Join(sessionDir, "events.jsonl")

	var sum copilotSummary
	var turns []Turn
	unreadable, err := scanCopilotEvents(eventsPath, func(ev copilotEvent, data map[string]any) {
		sum.add(ev, data)
		if turn, ok := copilotTurn(ev, data); ok {
			turns = append(turns, turn)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("copilot: load session %s: %w", id, err)
	}

	mtime := time.Time{}
	if info, statErr := os.Stat(eventsPath); statErr == nil {
		mtime = info.ModTime()
	}
	ws, _ := readCopilotWorkspace(sessionDir)

	rec := sum.record(id, eventsPath, unreadable, mtime, ws.projectPath())
	return &Transcript{Record: rec, Turns: turns}, nil
}

// maxSessionLineBytes caps how much of one JSONL line any reader in this
// package holds in memory. Multi-megabyte lines are routine — tool output
// and base64 image blocks put a 2.3 MB line in a real oh-my-pi transcript —
// so the cap has to be generous; what it must not be is absent, which is
// what an unbounded bufio.ReadString on a newline-less file amounts to.
const maxSessionLineBytes = 16 << 20

// readCappedLine reads one newline-terminated line, keeping at most
// maxSessionLineBytes of it and discarding the rest so the next line still
// resyncs. truncated reports that the returned bytes are an incomplete
// prefix, which no caller may parse.
func readCappedLine(r *bufio.Reader) (line []byte, truncated bool, err error) {
	for {
		frag, readErr := r.ReadSlice('\n')
		if len(line)+len(frag) > maxSessionLineBytes {
			truncated = true
			frag = frag[:maxSessionLineBytes-len(line)]
		}
		line = append(line, frag...)
		if readErr != bufio.ErrBufferFull {
			return line, truncated, readErr
		}
	}
}

// scanCopilotEvents streams events.jsonl, handing each decoded event and its
// decoded data payload to visit. Streaming rather than returning a slice is
// what lets List() summarise a session without materialising the event list
// or the Turn list. A line that fails to parse as JSON, or that runs past
// maxSessionLineBytes, increments unreadable and scanning continues
// (robustness rule 2) — the only error this returns is a failure to open the
// file at all.
func scanCopilotEvents(path string, visit func(ev copilotEvent, data map[string]any)) (unreadable int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	reader := bufio.NewReader(f)
	for {
		line, truncated, readErr := readCappedLine(reader)
		if truncated {
			unreadable++ // an incomplete prefix can never be valid JSON
		} else if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			var ev copilotEvent
			if json.Unmarshal(trimmed, &ev) != nil {
				unreadable++
			} else {
				visit(ev, copilotDecodeData(ev.Data))
			}
		}
		if readErr != nil {
			break // io.EOF, or a genuine read error — either way, stop here
		}
	}
	return unreadable, nil
}

// readCopilotWorkspace reads the sibling workspace.yaml. Its absence, or a
// malformed body, is not an error — Project simply stays empty.
func readCopilotWorkspace(sessionDir string) (copilotWorkspace, bool) {
	data, err := os.ReadFile(filepath.Join(sessionDir, "workspace.yaml"))
	if err != nil {
		return copilotWorkspace{}, false
	}
	var ws copilotWorkspace
	if err := yaml.Unmarshal(data, &ws); err != nil {
		return copilotWorkspace{}, false
	}
	return ws, true
}

// copilotTimestamp events are documented ISO-8601 with millisecond
// fractional seconds (e.g. "2026-05-27T06:21:13.180Z"). Both RFC3339Nano
// and RFC3339 parse that layout in Go; tried in order for defensiveness
// against a future build that drops the fraction.
func parseCopilotTimestamp(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// copilotIsTurnEvent reports the event types that represent an actual exchange —
// something a user or the agent said or did — as opposed to session
// lifecycle/bookkeeping events (session.start/shutdown/model_change,
// assistant.turn_start/end, hook.start/end, subagent.*, skill.invoked,
// session.compaction_*): github/copilot-cli#3551's own event-type table
// draws exactly this line between "conversational" and "internal runtime
// detail" events.
func copilotIsTurnEvent(eventType string) bool {
	switch eventType {
	case "user.message", "assistant.message", "session.task_complete", "tool.execution_start":
		return true
	default:
		return false
	}
}

// copilotProbeFile reads the first file path a tool.execution_start's
// arguments carry. It walks the package-wide filePathArgKeys rather than a
// copilot-specific list: Copilot CLI's own reference examples show the edit
// tool's arguments as {"path": ...} (github/copilot-cli#3551's gist), and
// the format has no stability contract at all, so narrowing the candidates
// per agent only creates blind spots — an MCP tool naming its argument
// filePath was attributed for claude and not for copilot.
func copilotProbeFile(args map[string]any) string {
	for _, key := range filePathArgKeys {
		if v, ok := args[key].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// copilotProbeModel looks for a model name on this event. tool.execution_complete
// carries a top-level "model" field, and session.compaction_complete nests
// one under compactionTokensUsed.model (both confirmed in
// github/copilot-cli#3551's reference examples). No confirmed schema exists
// for session.model_change, so this only probes the two verified shapes.
func copilotProbeModel(data map[string]any) string {
	if data == nil {
		return ""
	}
	if m, ok := data["model"].(string); ok && m != "" {
		return m
	}
	if nested, ok := data["compactionTokensUsed"].(map[string]any); ok {
		if m, ok := nested["model"].(string); ok && m != "" {
			return m
		}
	}
	return ""
}

func copilotStringField(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	s, _ := data[key].(string)
	return s
}

func copilotDecodeData(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m
}

// copilotArgs is the tool-argument object on a tool.execution_start event.
// A nil result is safe to index: copilotProbeFile reads a nil map.
func copilotArgs(data map[string]any) map[string]any {
	args, _ := data["arguments"].(map[string]any)
	return args
}

// copilotSummary accumulates every Record-level fact from a stream of
// events, so List() never holds the event list or the Turn list. Turns
// counts copilotIsTurnEvent matches, not raw line count — events.jsonl
// carries 20+ event types (github/copilot-cli#3551) and most of them are
// tool-pairing/lifecycle bookkeeping, not something a human would call a
// turn.
type copilotSummary struct {
	started, updated time.Time
	model            string
	turns            int
	tools, files     []string
}

func (s *copilotSummary) add(ev copilotEvent, data map[string]any) {
	if ts, ok := parseCopilotTimestamp(ev.Timestamp); ok {
		if s.started.IsZero() {
			s.started = ts
		}
		s.updated = ts
	}

	if s.model == "" {
		s.model = copilotProbeModel(data)
	}

	if copilotIsTurnEvent(ev.Type) {
		s.turns++
	}
	if ev.Type == "tool.execution_start" {
		if name := copilotStringField(data, "toolName"); name != "" {
			s.tools = append(s.tools, name)
		}
		if f := copilotProbeFile(copilotArgs(data)); f != "" {
			s.files = append(s.files, f)
		}
	}
}

func (s *copilotSummary) record(id, path string, unreadable int, mtime time.Time, project string) Record {
	started, updated := s.started, s.updated
	if started.IsZero() {
		started = mtime // robustness rule 5: fall back to file mtime
	}
	if updated.IsZero() {
		updated = mtime
	}

	return Record{
		Agent:           copilotAgent,
		ID:              id,
		Path:            path,
		Project:         project,
		Model:           s.model,
		Started:         started,
		Updated:         updated,
		Turns:           s.turns,
		Tools:           sortedDistinct(s.tools),
		Files:           sortedDistinct(s.files),
		UnreadableLines: unreadable,
	}
}

// copilotTurn converts one turn-shaped event into a Turn. ok is false for
// every lifecycle/bookkeeping event, which produces no turn.
func copilotTurn(ev copilotEvent, data map[string]any) (Turn, bool) {
	ts, _ := parseCopilotTimestamp(ev.Timestamp)

	switch ev.Type {
	case "user.message":
		return Turn{Role: RoleUser, Text: copilotStringField(data, "content"), At: ts}, true
	case "assistant.message":
		return Turn{Role: RoleAssistant, Text: copilotStringField(data, "content"), At: ts}, true
	case "session.task_complete":
		return Turn{Role: RoleAssistant, Text: copilotStringField(data, "summary"), At: ts}, true
	case "tool.execution_start":
		var files []string
		if f := copilotProbeFile(copilotArgs(data)); f != "" {
			files = []string{f}
		}
		return Turn{Role: RoleTool, Tool: copilotStringField(data, "toolName"), Files: files, At: ts}, true
	}
	return Turn{}, false
}
