package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// codexHomeEnv is the environment variable Codex itself honours for a
// relocated home directory. It overrides the *root*, not the sessions
// directory directly: sessions always live at "<root>/sessions/...".
const codexHomeEnv = "CODEX_HOME"

// codexReader reads OpenAI Codex's rollout transcripts.
//
// Format verified 2026-08-26 directly against github.com/openai/codex
// (main branch source, not a pinned release tag):
//   - codex-rs/rollout/src/rollout_file_name.rs: filenames are
//     "rollout-<yyyy-MM-ddTHH-mm-ss>-<uuid>.jsonl" (a reverted thread's
//     rollout appends "_<rollout-uuid>" after the stable thread id),
//     nested under "<CODEX_HOME>/sessions/<yyyy>/<mm>/<dd>/".
//   - codex-rs/rollout/src/recorder.rs's JsonlWriter/RolloutLineRef: every
//     physical line is one flattened JSON object -- {"timestamp","ordinal",
//     ...} with "ordinal" omitted on line 1 -- with the wrapped
//     RolloutItem's own fields (including its "type" tag) flattened
//     directly alongside them.
//   - codex-rs/history/src/rollout_payload.rs's RolloutItemWire: the "type"
//     tag names the variant ("session_meta", "response_item", "event_msg",
//     "turn_context", "compacted", "world_state", "security_risk_score",
//     "inter_agent_communication[_metadata]", "realtime_item"); the
//     variant's own fields live under a "payload" key (plus an optional
//     sibling "metadata" key for "response_item").
//   - codex-rs/protocol/src/protocol.rs's SessionMeta: "id" (a UUID,
//     stable across `thread/revert`), "cwd", "timestamp", and a coarse
//     "model_provider" (e.g. "openai") -- there is no session-level
//     "model" field at all. The precise per-turn model instead lives on
//     each "turn_context" line's TurnContextItem.model, because Codex can
//     switch models mid-session via `/model`; this reader prefers the last
//     turn_context's model over the coarser session-level model_provider,
//     which the original research brief did not distinguish.
//   - codex-rs/protocol/src/models.rs's ResponseItem: the per-turn payload
//     of a "response_item" line, itself tagged "type" (message,
//     agent_message, reasoning, function_call, function_call_output,
//     local_shell_call, custom_tool_call, custom_tool_call_output, plus
//     several newer/rarer variants this reader does not model).
//   - codex-rs/protocol/src/protocol.rs's EventMsg::TokenCount /
//     TokenCountEvent / TokenUsageInfo / TokenUsage: the only usage
//     figures Codex's own rollout format states. It carries no cost
//     figure anywhere, so CostKnown is never set true here (D28).
//
// Every shape is decoded defensively (robustness rule 4): an unrecognised
// "type" at any level is skipped, never treated as an error, and every
// field is read by probing a raw map rather than trusting a fixed schema.
type codexReader struct{}

// NewCodexReader returns the Reader for the Codex CLI's rollout store.
func NewCodexReader() Reader { return codexReader{} }

func (codexReader) Agent() string { return "codex" }

func codexRoot() (string, error) {
	if dir := os.Getenv(codexHomeEnv); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex"), nil
}

func (codexReader) Available() (bool, string) {
	root, err := codexRoot()
	if err != nil {
		return false, "cannot determine home directory"
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return false, root + " not found"
	}
	return true, ""
}

// List enumerates every "<root>/sessions/**/rollout-*.jsonl" file. A
// directory this process cannot open is skipped rather than failing the
// whole listing, and a rollout file that cannot even be opened (a
// mid-write rename, a permissions change) is skipped rather than failing
// the whole listing too.
func (codexReader) List() ([]Record, error) {
	root, err := codexRoot()
	if err != nil {
		return nil, err
	}
	paths, err := codexRolloutFiles(filepath.Join(root, "sessions"))
	if err != nil {
		return nil, err
	}
	var records []Record
	for _, path := range paths {
		// List needs only the summary; see parseCodexRollout.
		rec, _, err := parseCodexRollout(path, false)
		if err != nil {
			continue
		}
		records = append(records, rec)
	}
	return records, nil
}

// Load parses id's full transcript. id must be the exact SessionMeta "id"
// (a UUID) as returned by List, never a prefix.
//
// The canonical filename embeds that same uuid, so the common case opens
// exactly one file. Only a rollout whose filename does not encode the id
// -- a non-canonical name, or a session whose id was recovered from a
// damaged header -- falls back to scanning, and even then the parse is
// metadata-only until the id matches.
func (codexReader) Load(id string) (*Transcript, error) {
	root, err := codexRoot()
	if err != nil {
		return nil, err
	}
	paths, err := codexRolloutFiles(filepath.Join(root, "sessions"))
	if err != nil {
		return nil, err
	}
	var unmatched []string
	for _, path := range paths {
		if codexIDFromFilename(path) != id {
			unmatched = append(unmatched, path)
			continue
		}
		if tr, ok := codexLoadIfID(path, id); ok {
			return tr, nil
		}
	}
	for _, path := range unmatched {
		rec, _, err := parseCodexRollout(path, false)
		if err != nil || rec.ID != id {
			continue
		}
		if tr, ok := codexLoadIfID(path, id); ok {
			return tr, nil
		}
	}
	return nil, fmt.Errorf("codex: session %q not found", id)
}

// codexLoadIfID parses path in full and returns its transcript only if the
// SessionMeta id really is id — the filename is a hint, not proof.
func codexLoadIfID(path, id string) (*Transcript, bool) {
	rec, turns, err := parseCodexRollout(path, true)
	if err != nil || rec.ID != id {
		return nil, false
	}
	return &Transcript{Record: rec, Turns: turns}, true
}

// codexRolloutFiles walks dir (the "sessions" root) collecting every
// "rollout-*.jsonl" file under its <yyyy>/<mm>/<dd> nesting. Plain
// ".jsonl" only: Codex compresses aged-out rollouts to a ".jsonl.zst"
// sibling (codex-rs/rollout/src/compression.rs), and reading those would
// require a zstd dependency this reader has no mandate to add -- an aged,
// compressed session simply stops being listed, degrading gracefully
// rather than erroring. A subdirectory this process cannot read is
// skipped, not fatal.
func codexRolloutFiles(dir string) ([]string, error) {
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return nil, nil
	}
	var paths []string
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, "rollout-") && strings.HasSuffix(name, ".jsonl") {
			paths = append(paths, path)
		}
		return nil
	})
	return paths, nil
}

// codexLine is the shape every physical JSONL line is probed against: the
// RolloutLine wrapper's own "timestamp", plus the flattened RolloutItem
// "type" tag and its "payload".
type codexLine struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

// codexSessionMeta is the subset of SessionMeta this reader needs.
type codexSessionMeta struct {
	id            string
	cwd           string
	modelProvider string
	timestamp     time.Time
}

// parseCodexRollout reads one rollout *.jsonl file, producing the summary
// Record always and the ordered Turn list only when wantTurns — List needs
// the metadata for every rollout on disk, and Codex writes whole tool
// outputs verbatim, so materialising every Turn just to discard it is the
// single largest allocation `friday sessions` can make.
//
// Only a file this process cannot open fails outright; every per-line
// parse failure is counted in UnreadableLines and skipped (robustness
// rule 2), and a file whose very first line -- the SessionMeta -- cannot
// be read at all falls back to an id recovered from the filename rather
// than dropping the session entirely.
func parseCodexRollout(path string, wantTurns bool) (Record, []Turn, error) {
	f, err := os.Open(path)
	if err != nil {
		return Record{}, nil, err
	}
	defer f.Close()

	var mtime time.Time
	if info, statErr := f.Stat(); statErr == nil {
		mtime = info.ModTime()
	}

	rec := Record{Agent: "codex", Path: path, Started: mtime, Updated: mtime}
	var turns []Turn
	turnCount := 0
	tools := map[string]struct{}{}
	files := map[string]struct{}{}
	haveMeta := false
	lineNum := 0
	// Codex has no session-level model field: SessionMeta states only a
	// coarse provider, and the precise model arrives on each turn_context
	// (a session can switch models mid-run via /model, so the last one
	// wins). They are combined into "provider/model" at the end.
	var provider, model string

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxSessionLineBytes)
	for scanner.Scan() {
		raw := scanner.Bytes()
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		lineNum++

		var line codexLine
		if err := json.Unmarshal(raw, &line); err != nil {
			rec.UnreadableLines++
			continue
		}
		at, hasTime := codexParseTime(line.Timestamp)
		if hasTime {
			rec.Updated = at
		} else {
			at = mtime
		}

		if lineNum == 1 {
			if meta, ok := codexParseSessionMeta(raw, line); ok {
				rec.ID = meta.id
				rec.Project = meta.cwd
				provider = meta.modelProvider
				if !meta.timestamp.IsZero() {
					rec.Started = meta.timestamp
				}
				haveMeta = true
				continue
			}
			// A rotated or repaired rollout can begin with a real item
			// line. Falling through keeps that turn instead of dropping
			// it with no trace in either Turns or UnreadableLines.
		}

		switch line.Type {
		case "turn_context":
			if m := codexProbeString(line.Payload, "model"); m != "" {
				model = m
			}
		case "event_msg":
			codexApplyTokenUsage(&rec, line.Payload)
		case "response_item":
			if turn, ok := codexResponseItemTurn(line.Payload, at); ok {
				if wantTurns {
					turns = append(turns, turn)
				}
				turnCount++
				if turn.Tool != "" {
					tools[turn.Tool] = struct{}{}
				}
				for _, fp := range turn.Files {
					files[fp] = struct{}{}
				}
			}
		}
	}
	// Scan() returning false is indistinguishable from a clean EOF unless
	// Err() is consulted: a single line over maxSessionLineBytes (Codex
	// writes whole tool outputs verbatim, so one large file read does it)
	// or a mid-file read error abandons the rest of the file. Counting it
	// makes the CLI warn about an incomplete session instead of
	// presenting a truncated turn count and stale token totals as
	// complete.
	if err := scanner.Err(); err != nil {
		rec.UnreadableLines++
	}

	if !haveMeta {
		rec.ID = codexIDFromFilename(path)
	}
	switch {
	case provider != "" && model != "":
		rec.Model = provider + "/" + model
	case model != "":
		rec.Model = model
	default:
		rec.Model = provider
	}
	rec.Turns = turnCount
	rec.Tools = sortedKeys(tools)
	rec.Files = sortedKeys(files)
	return rec, turns, nil
}

// codexParseSessionMeta decodes line 1's SessionMeta. It first tries the
// current tagged wire shape (type == "session_meta", with fields under
// "payload"); if that yields nothing -- an empty/absent payload, or a
// "type" this reader does not recognise as metadata -- it falls back to
// probing the raw line itself as a flat object, in case an older or a
// future rollout format writes SessionMeta without the type/payload
// wrapper this reader otherwise assumes.
func codexParseSessionMeta(raw []byte, line codexLine) (codexSessionMeta, bool) {
	if line.Type == "" || line.Type == "session_meta" {
		if meta, ok := codexDecodeSessionMeta(line.Payload); ok {
			return meta, true
		}
	}
	return codexDecodeSessionMeta(raw)
}

func codexDecodeSessionMeta(payload json.RawMessage) (codexSessionMeta, bool) {
	if len(payload) == 0 {
		return codexSessionMeta{}, false
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		return codexSessionMeta{}, false
	}
	id := codexStringAt(m, "id")
	if id == "" {
		return codexSessionMeta{}, false
	}
	meta := codexSessionMeta{
		id:            id,
		cwd:           codexStringAt(m, "cwd"),
		modelProvider: codexStringAt(m, "model_provider"),
	}
	if ts, ok := codexParseTime(codexStringAt(m, "timestamp")); ok {
		meta.timestamp = ts
	}
	return meta, true
}

// codexProbeString decodes payload as a generic object and returns key's
// string value, or "" if payload is malformed or key is absent/non-string.
func codexProbeString(payload json.RawMessage, key string) string {
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		return ""
	}
	return codexStringAt(m, key)
}

func codexStringAt(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func codexIntAt(m map[string]any, key string) int {
	f, _ := m[key].(float64)
	return int(f)
}

// codexParseTime parses Codex's rollout timestamps, observed as UTC with
// millisecond precision (e.g. "2026-08-26T09:59:00.000Z"). time.RFC3339
// also accepts a timestamp with no fractional component, so this covers
// every value this reader has seen or can reasonably expect.
func codexParseTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// codexApplyTokenUsage reads an EventMsg::TokenCount payload
// (TokenCountEvent{info: TokenUsageInfo{total_token_usage: TokenUsage}})
// and overwrites rec's token fields with its "total_token_usage" -- a
// running cumulative total, so the last such event seen in the file is the
// session's final count. Codex's rollout format states no cost figure
// anywhere; CostKnown is deliberately never set here (robustness rule 7 /
// D28).
func codexApplyTokenUsage(rec *Record, payload json.RawMessage) {
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		return
	}
	if typ, _ := m["type"].(string); typ != "token_count" {
		return
	}
	info, ok := m["info"].(map[string]any)
	if !ok {
		return
	}
	total, ok := info["total_token_usage"].(map[string]any)
	if !ok {
		return
	}
	rec.InputTokens = codexIntAt(total, "input_tokens")
	rec.OutputTokens = codexIntAt(total, "output_tokens")
	rec.CacheCreationTokens = codexIntAt(total, "cache_write_input_tokens")
	rec.CacheReadTokens = codexIntAt(total, "cached_input_tokens")
	rec.TotalTokens = codexIntAt(total, "total_tokens")
}

// codexResponseItemTurn converts a "response_item" line's payload -- a
// ResponseItem -- into a Turn. Only the variants that carry genuine
// conversational or tool content are recognised; the rest
// (tool_search_call/output, web_search_call, image_generation_call,
// compaction, context_compaction, additional_tools, and any future
// variant) are skipped per robustness rule 1 -- an unrecognised variant is
// not a chat turn and is not counted toward Record.Turns.
func codexResponseItemTurn(payload json.RawMessage, at time.Time) (Turn, bool) {
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		return Turn{}, false
	}
	typ, _ := m["type"].(string)
	switch typ {
	case "message":
		return Turn{
			Role: codexStringAt(m, "role"),
			Text: codexJoinContentText(m["content"]),
			At:   at,
		}, true
	case "agent_message":
		role := codexStringAt(m, "author")
		if role == "" {
			role = "agent"
		}
		return Turn{Role: role, Text: codexJoinContentText(m["content"]), At: at}, true
	case "reasoning":
		return Turn{Role: RoleReasoning, Text: codexReasoningText(m), At: at}, true
	case "function_call":
		args, _ := m["arguments"].(string)
		return Turn{
			Role:  RoleTool,
			Tool:  codexStringAt(m, "name"),
			Text:  args,
			Files: codexFilesFromJSONArgs(args),
			At:    at,
		}, true
	case "function_call_output":
		return Turn{
			Role: RoleToolResult,
			Tool: codexStringAt(m, "name"),
			Text: codexOutputText(m["output"]),
			At:   at,
		}, true
	case "local_shell_call":
		return Turn{Role: RoleTool, Tool: "shell", Text: codexLocalShellCommand(m["action"]), At: at}, true
	case "custom_tool_call":
		input, _ := m["input"].(string)
		return Turn{
			Role:  RoleTool,
			Tool:  codexStringAt(m, "name"),
			Text:  input,
			Files: codexFilesFromJSONArgs(input),
			At:    at,
		}, true
	case "custom_tool_call_output":
		return Turn{
			Role: RoleToolResult,
			Tool: codexStringAt(m, "name"),
			Text: codexOutputText(m["output"]),
			At:   at,
		}, true
	default:
		return Turn{}, false
	}
}

// codexJoinContentText joins the "text" field of every element of a
// ContentItem/AgentMessageInputContent array. Variants that carry no text
// (input_image, input_audio, encrypted_content) are silently skipped since
// they simply have no "text" key to find.
func codexJoinContentText(content any) string {
	items, ok := content.([]any)
	if !ok {
		return ""
	}
	var parts []string
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if text := codexStringAt(obj, "text"); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

// codexReasoningText joins a Reasoning item's "summary" (always present)
// and "content" (present only when raw reasoning content is enabled)
// arrays, in that order.
func codexReasoningText(m map[string]any) string {
	var parts []string
	collect := func(key string) {
		items, ok := m[key].([]any)
		if !ok {
			return
		}
		for _, item := range items {
			obj, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if text := codexStringAt(obj, "text"); text != "" {
				parts = append(parts, text)
			}
		}
	}
	collect("summary")
	collect("content")
	return strings.Join(parts, "\n")
}

// codexOutputText decodes a FunctionCallOutputPayload's wire encoding,
// which is either a plain string or an object carrying "content" (a
// string) or "content_items" (a ContentItem-shaped array). Anything else
// is rendered as its raw JSON rather than dropped -- this is real
// transcript content, not a field this reader is guessing at.
func codexOutputText(output any) string {
	switch v := output.(type) {
	case string:
		return v
	case map[string]any:
		if text := codexStringAt(v, "content"); text != "" {
			return text
		}
		if items, ok := v["content_items"].([]any); ok {
			if text := codexJoinContentText(items); text != "" {
				return text
			}
		}
	case nil:
		return ""
	}
	if raw, err := json.Marshal(output); err == nil {
		return string(raw)
	}
	return ""
}

// codexLocalShellCommand reads a LocalShellCall's "action" (a
// LocalShellAction::Exec, flattened onto the action object) and joins its
// "command" argv array into a single displayable string.
func codexLocalShellCommand(action any) string {
	obj, ok := action.(map[string]any)
	if !ok {
		return ""
	}
	items, ok := obj["command"].([]any)
	if !ok {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " ")
}

// codexFilesFromJSONArgs best-effort-parses a tool call's raw JSON
// arguments/input string and probes it for filePathArgKeys. Codex's own
// built-ins (`shell`, `apply_patch`, `update_plan`) expose no single clean
// file-path argument -- `shell` takes a command array and `apply_patch` a
// unified diff touching arbitrarily many files -- so Files extraction here
// is realistically only useful for third-party/MCP tool calls.
func codexFilesFromJSONArgs(args string) []string {
	if args == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(args), &m); err != nil {
		return nil
	}
	var found []string
	for _, key := range filePathArgKeys {
		if v := codexStringAt(m, key); v != "" {
			found = append(found, v)
		}
	}
	return found
}

// codexIDFromFilename recovers an identifier from the canonical
// "rollout-<yyyy-MM-ddTHH-mm-ss>-<uuid>[_<uuid>].jsonl" filename: Load uses
// it to find one session without parsing every other rollout on disk, and
// parseCodexRollout uses it when line 1's SessionMeta could not be read at
// all. The stable thread id is the first uuid; a second,
// underscore-separated rollout id (present only for a reverted thread's
// rollout) is discarded in favour of it.
func codexIDFromFilename(path string) string {
	name := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	name = strings.TrimPrefix(name, "rollout-")
	const tsLen = len("2006-01-02T15-04-05")
	if len(name) <= tsLen+1 || name[tsLen] != '-' {
		return name
	}
	ids := name[tsLen+1:]
	if idx := strings.IndexByte(ids, '_'); idx >= 0 {
		return ids[:idx]
	}
	return ids
}
