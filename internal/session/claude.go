package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// claudeConfigDirEnv is the environment variable Claude Code itself
// honours for a relocated config directory.
const claudeConfigDirEnv = "CLAUDE_CONFIG_DIR"

// claudeReader reads Claude Code's own session transcripts. Format verified
// 2026-08-26 against:
//   - https://code.claude.com/docs/en/claude-directory (session location)
//   - https://github.com/neilberkman/ccrider/blob/main/research/schema.md
//     (reverse-engineered record shapes; ccrider's own README notes Claude
//     Code's transcript schema is undocumented and version-unstable, which
//     is exactly why every field below is probed rather than assumed)
//   - anthropics/claude-code issues #7009, #35162, #40946 (the project-path
//     slug encoding, and its documented, acknowledged irreversibility)
//
// Claude Code itself states its JSONL entry format is internal and can
// change between releases (see the "Manage sessions" doc), so every record
// is decoded defensively: unknown line shapes are skipped, and every field
// is read by probing a raw map/struct rather than trusting a fixed schema.
type claudeReader struct{}

// NewClaudeReader returns the Reader for Claude Code's session store.
func NewClaudeReader() Reader { return claudeReader{} }

func (claudeReader) Agent() string { return "claude" }

// root resolves the Claude Code home directory: $CLAUDE_CONFIG_DIR if set,
// else ~/.claude.
func (claudeReader) root() (string, error) {
	if dir := os.Getenv(claudeConfigDirEnv); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude"), nil
}

func (r claudeReader) Available() (bool, string) {
	root, err := r.root()
	if err != nil {
		return false, "cannot determine home directory"
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return false, root + " not found"
	}
	return true, ""
}

// List enumerates every <root>/projects/<slug>/<id>.jsonl file. A slug
// directory or session file this process cannot open (permissions, a
// mid-write rename) is skipped rather than failing the whole listing --
// only a single corrupt *line* inside an otherwise-readable file is
// recorded via UnreadableLines instead of being skipped outright.
func (r claudeReader) List() ([]Record, error) {
	root, err := r.root()
	if err != nil {
		return nil, err
	}
	projectsDir := filepath.Join(root, "projects")
	slugDirs, err := os.ReadDir(projectsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var records []Record
	for _, sd := range slugDirs {
		if !sd.IsDir() {
			continue
		}
		slugPath := filepath.Join(projectsDir, sd.Name())
		files, err := os.ReadDir(slugPath)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			// List needs only the summary — parsing turns into a slice
			// would hold a second copy of every message in the largest
			// transcript on disk for a table that never shows them.
			rec, _, err := parseClaudeSession(filepath.Join(slugPath, f.Name()), sd.Name(), false)
			if err != nil {
				continue
			}
			records = append(records, rec)
		}
	}
	return records, nil
}

// Load parses id's full transcript. id must be the exact filename stem as
// returned by List, never a prefix.
func (r claudeReader) Load(id string) (*Transcript, error) {
	root, err := r.root()
	if err != nil {
		return nil, err
	}
	path, slug, err := r.findSession(root, id)
	if err != nil {
		return nil, err
	}
	rec, turns, err := parseClaudeSession(path, slug, true)
	if err != nil {
		return nil, err
	}
	return &Transcript{Record: rec, Turns: turns}, nil
}

func (r claudeReader) findSession(root, id string) (path, slug string, err error) {
	// id is interpolated into a path below, so a separator or a ".."
	// component would let Load read any *.jsonl file on the machine.
	// Reader.Load's contract is "the exact ID as returned by List", and
	// every such ID is a bare filename stem.
	if id == "" || id != filepath.Base(id) || strings.ContainsAny(id, `/\`) {
		return "", "", fmt.Errorf("claude: invalid session id %q", id)
	}
	projectsDir := filepath.Join(root, "projects")
	slugDirs, err := os.ReadDir(projectsDir)
	if err != nil {
		return "", "", err
	}
	for _, sd := range slugDirs {
		if !sd.IsDir() {
			continue
		}
		candidate := filepath.Join(projectsDir, sd.Name(), id+".jsonl")
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			return candidate, sd.Name(), nil
		}
	}
	return "", "", fmt.Errorf("claude: session %q not found", id)
}

// claudeRawRecord is the outer shape every JSONL line is probed against.
// Only the fields this reader actually needs are declared; everything else
// (parentUuid, gitBranch, isSidechain, toolUseResult, ...) is left
// unrecognized on purpose -- reading a field this reader doesn't use is not
// a reason to grow the struct.
type claudeRawRecord struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	CWD       string          `json:"cwd"`
	Message   json.RawMessage `json:"message"`
	// CostUSD is an optional, version-dependent field seen on some older
	// Claude Code releases. Its presence is the *only* thing that sets
	// Record.CostKnown -- friday never derives a cost from a pricing table.
	CostUSD *float64 `json:"costUSD"`
}

// claudeMessage is the inner "message" object on a user/assistant record.
type claudeMessage struct {
	Role    string          `json:"role"`
	Model   string          `json:"model"` // present on assistant records only
	Content json.RawMessage `json:"content"`
	Usage   *claudeUsage    `json:"usage"`
}

// claudeUsage mirrors the Anthropic Messages API usage object Claude Code
// records verbatim on each assistant turn.
type claudeUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// claudeContentBlock is one element of a "content" array. message.content
// is either a bare string (plain user text) or an array of these typed
// blocks (assistant text interleaved with tool_use calls, or a user turn
// carrying tool_result blocks back to the assistant).
type claudeContentBlock struct {
	Type  string         `json:"type"`
	Text  string         `json:"text"`            // type == "text"
	Name  string         `json:"name"`            // type == "tool_use"
	Input map[string]any `json:"input,omitempty"` // type == "tool_use"
}

// parseClaudeSession reads one session file, producing the summary Record
// always and the ordered Turn list only when wantTurns — List needs the
// metadata for every session on disk, and materialising every Turn's full
// text just to discard it is what Record's doc comment promises not to do.
func parseClaudeSession(path, slug string, wantTurns bool) (Record, []Turn, error) {
	f, err := os.Open(path)
	if err != nil {
		return Record{}, nil, err
	}
	defer f.Close()

	rec := Record{
		Agent: "claude",
		ID:    strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		Path:  path,
	}
	var turns []Turn
	turnCount := 0
	tools := map[string]struct{}{}
	filesSeen := map[string]struct{}{}
	var cwd string
	var costTotal float64
	var costFound bool
	var started, updated time.Time

	var mtime time.Time
	if info, statErr := f.Stat(); statErr == nil {
		mtime = info.ModTime()
	}

	reader := bufio.NewReader(f)
	for {
		// ReadBytes, not ReadString: the line is handed straight to
		// json.Unmarshal, and a string round-trip would allocate a copy
		// of every transcript line on the hottest loop in the package.
		// The final line of an in-progress session has no terminator, so
		// ReadBytes returns (partial, io.EOF) — it is processed here
		// BEFORE readErr is tested, and reordering these two blocks would
		// silently drop the last turn of every live session.
		line, readErr := reader.ReadBytes('\n')
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			var raw claudeRawRecord
			if err := json.Unmarshal(trimmed, &raw); err != nil {
				rec.UnreadableLines++
			} else {
				ts, tsOK := parseClaudeTimestamp(raw.Timestamp)
				// Started/Updated use the first/last record anywhere in the
				// file that actually states a parseable timestamp, not
				// strictly line 1: Claude Code's own "summary" record (when
				// present) is often the first line and carries no
				// timestamp, which would otherwise discard every real
				// timestamp in the file in favour of the mtime fallback.
				if tsOK {
					if started.IsZero() {
						started = ts
					}
					updated = ts
				}
				if raw.CWD != "" && cwd == "" {
					cwd = raw.CWD
				}
				if raw.CostUSD != nil {
					costTotal += *raw.CostUSD
					costFound = true
				}

				switch raw.Type {
				case "user", "assistant":
					var msg claudeMessage
					if len(raw.Message) > 0 {
						_ = json.Unmarshal(raw.Message, &msg) // best-effort; zero value on failure
					}
					text, tool, files, isToolResultOnly := extractClaudeContent(msg.Content)

					role := msg.Role
					if role == "" {
						role = raw.Type
					}
					if isToolResultOnly {
						// The synthesized "user" turn carrying a tool's
						// output back to the model is a RESULT, not a
						// call — `friday sessions --open` distinguishes an
						// interrupted tool loop from a clean finish by
						// exactly this.
						role = RoleToolResult
					}

					// A record with no parseable timestamp would otherwise
					// render as 0001-01-01 in the handoff export's
					// transcript tail.
					at := ts
					if !tsOK {
						at = mtime
					}
					if wantTurns {
						turns = append(turns, Turn{
							Role:  role,
							Text:  text,
							At:    at,
							Tool:  tool,
							Files: files,
						})
					}
					turnCount++

					if tool != "" {
						tools[tool] = struct{}{}
					}
					for _, fp := range files {
						filesSeen[fp] = struct{}{}
					}

					if raw.Type == "assistant" {
						if rec.Model == "" && msg.Model != "" {
							rec.Model = msg.Model
						}
						if msg.Usage != nil {
							rec.InputTokens += msg.Usage.InputTokens
							rec.OutputTokens += msg.Usage.OutputTokens
							rec.CacheCreationTokens += msg.Usage.CacheCreationInputTokens
							rec.CacheReadTokens += msg.Usage.CacheReadInputTokens
						}
					}
				default:
					// summary, system, file-history-snapshot, and any
					// future record type: not a chat turn, skipped per
					// robustness rule 1.
				}
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return rec, turns, readErr
		}
	}

	rec.Turns = turnCount
	rec.Tools = sortedKeys(tools)
	rec.Files = sortedKeys(filesSeen)
	rec.TotalTokens = rec.InputTokens + rec.OutputTokens + rec.CacheCreationTokens + rec.CacheReadTokens
	rec.CostKnown = costFound
	rec.CostUSD = costTotal

	if started.IsZero() {
		started = mtime
	}
	if updated.IsZero() {
		updated = mtime
	}
	rec.Started = started
	rec.Updated = updated

	rec.Project = reverseClaudeSlug(cwd, slug)

	return rec, turns, nil
}

// extractClaudeContent decodes a message.content field, which Claude Code
// writes as either a bare string (plain text) or an array of typed blocks.
// It returns the concatenated text of any "text" blocks, the name and
// file-path-shaped parameters of the first "tool_use" block found, and
// whether every block in the array was a "tool_result" (the synthesized
// reply Claude Code writes when submitting a tool's output back to the
// model as a "user"-role turn -- distinct from an actual human message).
func extractClaudeContent(content json.RawMessage) (text, tool string, files []string, allToolResult bool) {
	if len(content) == 0 {
		return "", "", nil, false
	}

	var plain string
	if err := json.Unmarshal(content, &plain); err == nil {
		return plain, "", nil, false
	}

	var blocks []claudeContentBlock
	if err := json.Unmarshal(content, &blocks); err != nil || len(blocks) == 0 {
		return "", "", nil, false
	}

	var textParts []string
	fileSet := map[string]struct{}{}
	sawToolResult, sawOther := false, false
	for _, b := range blocks {
		switch b.Type {
		case "text":
			textParts = append(textParts, b.Text)
			sawOther = true
		case "tool_use":
			if tool == "" {
				tool = b.Name
			}
			for _, key := range filePathArgKeys {
				if v, ok := b.Input[key].(string); ok && v != "" {
					fileSet[v] = struct{}{}
				}
			}
			sawOther = true
		case "tool_result":
			sawToolResult = true
		default:
			sawOther = true
		}
	}
	return strings.Join(textParts, "\n"), tool, sortedKeys(fileSet), sawToolResult && !sawOther
}

// parseClaudeTimestamp parses Claude Code's ISO-8601 timestamps (observed
// as millisecond-precision UTC, e.g. "2025-11-01T01:23:44.615Z"). RFC3339Nano
// also accepts a timestamp with no fractional component, so this covers
// every record version seen.
func parseClaudeTimestamp(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// reverseClaudeSlug recovers a session's absolute project directory.
//
// Every record already carries its own "cwd" field stating that path
// verbatim, so that is used whenever present -- it is exact, unlike the
// slug.
//
// If no record in the file has a "cwd" (e.g. every line failed to parse),
// this falls back to best-effort-reversing the directory name Claude Code
// derived it from. Claude Code's own slug algorithm keeps ASCII
// alphanumerics and '-' verbatim and replaces every other character --
// including the path separator '/' -- with '-'. That makes the mapping
// genuinely irreversible whenever a real path segment itself contains a
// literal '-': anthropics/claude-code#7009, #35162 and #40946 all document
// that "/a/data-analysis" and "/a/data/analysis" both encode to the same
// slug "-a-data-analysis", with no way to tell which dash was which after
// the fact. This fallback resolves the ambiguity the same way most
// third-party Claude Code tooling does: treat every '-' as a path
// separator. That is exact whenever the real path contains no literal
// dashes, and only approximate otherwise -- never a crash, never an error.
//
// The reversal is rejected outright when it cannot be a real absolute
// path, because a fabricated one is worse than none: it is printed in the
// Project column and matched by `friday sessions --project <substr>`. On
// Windows -- friday's primary platform -- Claude Code slugifies
// "D:\dev\proj" to "D--dev-proj", whose naive reversal is "D//dev/proj":
// no drive letter, an empty segment, wrong separators. Any reversal that
// is not rooted, or that contains an empty segment (the signature of two
// adjacent dashes), yields "" so the CLI renders "unknown" instead.
func reverseClaudeSlug(cwd, slug string) string {
	if cwd != "" {
		return cwd
	}
	if slug == "" {
		return ""
	}
	reversed := strings.ReplaceAll(slug, "-", "/")
	if !strings.HasPrefix(reversed, "/") {
		return ""
	}
	for _, segment := range strings.Split(strings.TrimPrefix(reversed, "/"), "/") {
		if segment == "" {
			return ""
		}
	}
	return reversed
}
