package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// oh-my-pi (github.com/can1357/oh-my-pi, binary `omp`) is a maintained fork
// of pi-mono. Its session format, directly verified against a live install's
// real transcript files (not just docs), is JSONL: one JSON object per line,
// forming a tree via id/parentId.
//
// Two physical details the format shares with — and one it changed from —
// the format's pi-mono ancestor:
//
//   - Current files begin with a fixed-width, 256-byte `{"type":"title",...}`
//     slot before the real session header, used for fast title lookups
//     without a full parse. This reader probes the first line's "type" and
//     skips it when present, then reads the next line as the header —
//     tolerating both current files (title slot present) and any older ones
//     that start directly with the header.
//   - The header carries a version field (1: legacy linear, 2: tree via
//     id/parentId, 3 (current): "hookMessage" role renamed to "custom").
//     This reader never switches on the version number: every entry and
//     message is decoded into a generic map and probed by key (robustness
//     rule 4), so an unrecognised future field degrades to its zero value
//     instead of failing the line, and the one known legacy shape (role
//     "hookMessage") is normalised to its v3 name explicitly.
//
// ohMyPiAgentDirEnv relocates oh-my-pi's entire native agent directory
// (config.yml, mcp.json, sessions/, …), not just the sessions subdirectory —
// matching oh-my-pi's own PI_CODING_AGENT_DIR precedence. The sessions root
// is always <agent-dir>/sessions.
const ohMyPiAgentDirEnv = "PI_CODING_AGENT_DIR"

type ohMyPiReader struct{}

// NewOhMyPiReader returns a Reader for oh-my-pi's coding-agent session
// store.
func NewOhMyPiReader() Reader { return ohMyPiReader{} }

func (ohMyPiReader) Agent() string { return "oh-my-pi" }

// ohMyPiSessionsDir resolves the sessions root: PI_CODING_AGENT_DIR
// relocates oh-my-pi's whole native agent directory, so the sessions root
// is always its "sessions" subdirectory — never a directly-overridden
// sessions path. fromDefault reports whether the fallback ~/.omp/agent was
// used, for a friendlier Available() message.
func ohMyPiSessionsDir() (dir string, fromDefault bool, err error) {
	if v := os.Getenv(ohMyPiAgentDirEnv); v != "" {
		return filepath.Join(v, "sessions"), false, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", true, err
	}
	return filepath.Join(home, ".omp", "agent", "sessions"), true, nil
}

// ohMyPiLegacyAgentDir is the pre-rename home of oh-my-pi's agent directory
// (~/.pi/agent, now ~/.omp/agent). Nothing is ever read from it — it is only
// stat'ed when the current location is missing, so a returning user who
// never migrated learns why their transcripts vanished instead of being
// shown a path they have never heard of.
const ohMyPiLegacyAgentDir = ".pi"

func (ohMyPiReader) Available() (bool, string) {
	dir, fromDefault, err := ohMyPiSessionsDir()
	if err != nil {
		return false, "cannot resolve home directory"
	}
	if info, statErr := os.Stat(dir); statErr == nil && info.IsDir() {
		return true, ""
	}
	if fromDefault {
		if legacy, ok := ohMyPiLegacySessionsDir(); ok {
			return false, fmt.Sprintf("%s not found (found legacy %s: oh-my-pi renamed its agent directory, so migrate it or point %s at it)", dir, legacy, ohMyPiAgentDirEnv)
		}
	}
	return false, dir + " not found"
}

func ohMyPiLegacySessionsDir() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	legacy := filepath.Join(home, ohMyPiLegacyAgentDir, "agent", "sessions")
	if info, err := os.Stat(legacy); err == nil && info.IsDir() {
		return legacy, true
	}
	return "", false
}

// ohMyPiSessionFiles enumerates every "<sessions-dir>/<encoded-cwd>/*.jsonl"
// file. oh-my-pi encodes the owning cwd into the project-directory name
// (differently for a home-relative, temp-relative, or other absolute path),
// but this reader never decodes it: List/Load recover Project from each
// file's own header "cwd" field, which is exact where the directory-name
// encoding is lossy.
func ohMyPiSessionFiles(dir string) ([]string, error) {
	projectDirs, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var paths []string
	for _, pd := range projectDirs {
		if !pd.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(dir, pd.Name()))
		if err != nil {
			continue // one unreadable project dir must not fail the whole store
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			paths = append(paths, filepath.Join(dir, pd.Name(), f.Name()))
		}
	}
	return paths, nil
}

func (ohMyPiReader) List() ([]Record, error) {
	dir, _, err := ohMyPiSessionsDir()
	if err != nil {
		return nil, err
	}
	paths, err := ohMyPiSessionFiles(dir)
	if err != nil {
		return nil, err
	}
	records := make([]Record, 0, len(paths))
	for _, path := range paths {
		t, err := parseOhMyPiSession(path, false)
		if err != nil {
			continue // one unparsable session file must not fail List()
		}
		records = append(records, t.Record)
	}
	return records, nil
}

func (ohMyPiReader) Load(id string) (*Transcript, error) {
	dir, _, err := ohMyPiSessionsDir()
	if err != nil {
		return nil, err
	}
	paths, err := ohMyPiSessionFiles(dir)
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		t, err := parseOhMyPiSession(path, true)
		if err != nil {
			continue
		}
		if t.ID == id {
			return t, nil
		}
	}
	return nil, fmt.Errorf("oh-my-pi: session %q not found", id)
}

// ohMyPiUsageTotals accumulates oh-my-pi's own Usage records across every
// message/compaction/branch_summary entry that carries one. Directly
// confirmed against a real transcript: usage.cost is a real nested object
// (not always-zero), so this is one of the agents where CostKnown is set
// from the transcript itself (D28, robustness rule 7).
type ohMyPiUsageTotals struct {
	input, output, cacheCreate, cacheRead, totalTokens int
	cost                                               float64
	costSeen                                           bool
}

func (u *ohMyPiUsageTotals) accumulate(usage map[string]any) {
	if usage == nil {
		return
	}
	u.input += int(ohMyPiFloatAt(usage, "input"))
	u.output += int(ohMyPiFloatAt(usage, "output"))
	u.cacheCreate += int(ohMyPiFloatAt(usage, "cacheWrite"))
	u.cacheRead += int(ohMyPiFloatAt(usage, "cacheRead"))
	u.totalTokens += int(ohMyPiFloatAt(usage, "totalTokens"))
	if cost := ohMyPiMapAt(usage, "cost"); cost != nil {
		if total, ok := cost["total"].(float64); ok {
			u.cost += total
			u.costSeen = true
		}
	}
}

// readOhMyPiHeader reads the session header line, transparently skipping the
// physical title slot current files begin with
// (`{"type":"title","v":1,...}`, padded to a fixed 256 bytes) when present.
// Older files that start directly with the header (no title slot) are
// tolerated the same way: whichever line is read first, only one that
// actually decodes as {"type":"session",...} is accepted as the header.
func readOhMyPiHeader(scanner *bufio.Scanner) (map[string]any, error) {
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("empty session file")
	}
	var first map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(scanner.Bytes()), &first); err != nil {
		return nil, fmt.Errorf("missing or malformed session header")
	}
	if ohMyPiStringAt(first, "type") != "title" {
		if ohMyPiStringAt(first, "type") != "session" {
			return nil, fmt.Errorf("missing or malformed session header")
		}
		return first, nil
	}
	// The first line was the title slot; the real header is the next line.
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("missing session header after title slot")
	}
	var header map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(scanner.Bytes()), &header); err != nil || ohMyPiStringAt(header, "type") != "session" {
		return nil, fmt.Errorf("missing or malformed session header")
	}
	return header, nil
}

// parseOhMyPiSession reads one *.jsonl transcript in full: the title slot
// (if present), the header, then every entry — producing the summary Record
// fields always and the ordered Turn list only when wantTurns, so List()
// does not build (and immediately discard) a turn slice that can hold an
// entire 50 MB transcript's text. A structurally missing/malformed header
// fails the whole file (there is no session identity to report); every other
// line that fails to parse as JSON is counted in UnreadableLines and skipped
// (robustness rule 2), and every recognised-but-unfamiliar entry type is
// skipped silently (robustness rule 1).
func parseOhMyPiSession(path string, wantTurns bool) (*Transcript, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), maxSessionLineBytes) // tool output/base64 images can make a line large

	header, err := readOhMyPiHeader(scanner)
	if err != nil {
		return nil, fmt.Errorf("oh-my-pi: %s: %w", path, err)
	}

	t := &Transcript{}
	t.Agent = "oh-my-pi"
	t.Path = path
	t.ID = ohMyPiStringAt(header, "id")
	t.Project = ohMyPiStringAt(header, "cwd")
	started := parseOhMyPiTime(ohMyPiStringAt(header, "timestamp"))
	updated := started

	var (
		unreadable int
		turnCount  int
		modelSeen  bool
		usage      ohMyPiUsageTotals
		toolSet    = map[string]struct{}{}
		fileSet    = map[string]struct{}{}
	)

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal(line, &entry); err != nil {
			unreadable++
			continue
		}

		ts := parseOhMyPiTime(ohMyPiStringAt(entry, "timestamp"))
		if !ts.IsZero() {
			if started.IsZero() || ts.Before(started) {
				started = ts
			}
			if ts.After(updated) {
				updated = ts
			}
		}

		switch ohMyPiStringAt(entry, "type") {
		case "message":
			msg := ohMyPiMapAt(entry, "message")
			var turn Turn
			if msg != nil {
				turn = buildOhMyPiTurn(msg, ts)
				usage.accumulate(ohMyPiMapAt(msg, "usage"))
				if !modelSeen && ohMyPiStringAt(msg, "role") == "assistant" {
					if model := ohMyPiModel(msg); model != "" {
						t.Model = model
						modelSeen = true
					}
				}
			} else {
				turn = Turn{At: ts}
			}
			// One counter, incremented where the turn is produced, so the
			// count `friday sessions` prints can never drift from the turns
			// `friday session show` renders.
			turnCount++
			if wantTurns {
				t.Turns = append(t.Turns, turn)
			}
			if turn.Tool != "" {
				toolSet[turn.Tool] = struct{}{}
			}
			for _, fp := range turn.Files {
				fileSet[fp] = struct{}{}
			}
		case "compaction", "branch_summary":
			// Not a turn (not type:"message"), but a real usage-bearing
			// entry — its tokens/cost count toward the session's totals
			// even though it produces no Turn.
			usage.accumulate(ohMyPiMapAt(entry, "usage"))
		default:
			// Every non-message entry type, silently skipped (robustness
			// rule 1). A type histogram over a real 7,372-line transcript
			// gives: custom, custom_message, reset_boundary, model_change,
			// mode_change, title_change, title, thinking_level_change,
			// session, credential_pin — plus whatever a future oh-my-pi
			// release adds.
		}
	}
	if err := scanner.Err(); err != nil {
		// A line past the scanner's cap (one base64 image block is enough)
		// used to discard the entire session — thousands of turns gone from
		// `friday sessions` with no warning at all. Everything scanned up to
		// that point is real; keep it and tally the loss.
		unreadable++
	}

	if started.IsZero() {
		if info, statErr := os.Stat(path); statErr == nil {
			started = info.ModTime()
		}
	}
	if updated.IsZero() {
		updated = started
	}

	t.Started = started
	t.Updated = updated
	t.Record.Turns = turnCount
	t.UnreadableLines = unreadable
	t.Tools = sortedKeys(toolSet)
	t.Files = sortedKeys(fileSet)
	t.InputTokens = usage.input
	t.OutputTokens = usage.output
	t.CacheCreationTokens = usage.cacheCreate
	t.CacheReadTokens = usage.cacheRead
	t.TotalTokens = usage.totalTokens
	t.CostUSD = usage.cost
	t.CostKnown = usage.costSeen

	return t, nil
}

// ohMyPiModel renders an assistant message's model the way every reader with
// a provider on the wire does: "<provider>/<model>". A real transcript
// carries both ("model":"claude-opus-5","provider":"anthropic"), and dropping
// the provider split the MODEL column and the --stats rollup into two
// buckets for one model, since opencode already prefixes its own.
func ohMyPiModel(msg map[string]any) string {
	model := ohMyPiStringAt(msg, "model")
	if model == "" {
		return ""
	}
	if provider := ohMyPiStringAt(msg, "provider"); provider != "" {
		return provider + "/" + model
	}
	return model
}

// buildOhMyPiTurn converts one message entry's "message" field into a Turn.
// Role/content-block shapes below (user/assistant/toolResult with
// text/thinking/toolCall blocks) are directly confirmed against a real
// transcript, not inferred from docs. An unrecognised role still produces a
// turn (it was a real message entry) with Text/Tool/Files left at zero
// value rather than guessed.
func buildOhMyPiTurn(msg map[string]any, at time.Time) Turn {
	role := ohMyPiStringAt(msg, "role")
	if role == "hookMessage" {
		role = "custom" // v3 renamed this role; tolerate pre-v3 sessions.
	}

	switch role {
	case "user":
		return Turn{Role: RoleUser, At: at, Text: extractOhMyPiText(msg["content"])}
	case "assistant":
		text, tool, files := extractOhMyPiAssistantContent(msg["content"])
		return Turn{Role: RoleAssistant, At: at, Text: text, Tool: tool, Files: files}
	case "toolResult":
		return Turn{Role: RoleToolResult, At: at, Tool: ohMyPiStringAt(msg, "toolName"), Text: extractOhMyPiText(msg["content"])}
	case "custom":
		// A hook/extension message. It is not one of the canonical roles, so
		// the agent's own word passes through rather than being flattened
		// into a generic role that would say less about it.
		return Turn{Role: role, At: at, Text: extractOhMyPiText(msg["content"])}
	}
	return Turn{Role: role, At: at}
}

// extractOhMyPiText reads a user/custom/toolResult message's "content"
// field, which is either a plain string or an array of content blocks
// (confirmed: at least {"type":"text","text":...}); only text blocks
// contribute.
func extractOhMyPiText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, item := range v {
			block, ok := item.(map[string]any)
			if !ok || ohMyPiStringAt(block, "type") != "text" {
				continue
			}
			if text := ohMyPiStringAt(block, "text"); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

// extractOhMyPiAssistantContent reads an assistant message's "content"
// array of text/thinking/toolCall blocks (confirmed field names, directly
// against a real transcript: a toolCall block has "name" and "arguments",
// not "toolName"/"input"). Text blocks join into the turn's text (thinking
// blocks are deliberately excluded — they are the model's private
// reasoning, not conversational text); the first toolCall's name becomes
// Tool, and every toolCall's recognised file-path argument contributes to
// Files.
func extractOhMyPiAssistantContent(content any) (text, tool string, files []string) {
	blocks, ok := content.([]any)
	if !ok {
		return "", "", nil
	}
	var textParts []string
	fileSet := map[string]struct{}{}
	for _, item := range blocks {
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		switch ohMyPiStringAt(block, "type") {
		case "text":
			if t := ohMyPiStringAt(block, "text"); t != "" {
				textParts = append(textParts, t)
			}
		case "toolCall":
			if tool == "" {
				tool = ohMyPiStringAt(block, "name")
			}
			args := ohMyPiMapAt(block, "arguments")
			for _, key := range filePathArgKeys {
				if v, ok := args[key].(string); ok && v != "" {
					fileSet[v] = struct{}{}
				}
			}
		}
	}
	files = sortedKeys(fileSet)
	return strings.Join(textParts, "\n"), tool, files
}

func parseOhMyPiTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	return time.Time{}
}

func ohMyPiMapAt(m map[string]any, key string) map[string]any {
	if v, ok := m[key].(map[string]any); ok {
		return v
	}
	return nil
}

func ohMyPiStringAt(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func ohMyPiFloatAt(m map[string]any, key string) float64 {
	if v, ok := m[key].(float64); ok {
		return v
	}
	return 0
}
