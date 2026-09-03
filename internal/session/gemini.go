// Package session: gemini reader.
//
// Format verified 2026-08-26 against github.com/google-gemini/gemini-cli
// (main branch): packages/core/src/services/chatRecordingService.ts,
// chatRecordingTypes.ts, config/storage.ts, config/projectRegistry.ts and
// utils/paths.ts, plus docs/cli/session-management.md.
package session

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// geminiProjectRootMarker is a small text file gemini-cli writes at
// <root>/tmp/<id>/.project_root, holding the literal absolute project path
// that owns that temp/session directory (see config/projectRegistry.ts,
// verifySlugOwnership/ensureOwnershipMarkers). Recent gemini-cli builds
// generate a short opaque slug for <id> via a project registry
// (~/.gemini/projects.json) instead of the sha256(project root) hash the
// public docs still describe; the marker file is how a slug-named directory
// can still be resolved back to its project without inverting anything.
// Older/unmigrated installs have no marker file, so the reader falls back to
// forward sha256 hash matching against the current process's cwd — see
// geminiProjectHashes for the exact strings getProjectHash hashes.
const geminiProjectRootMarker = ".project_root"

// geminiHomeEnv overrides the home directory gemini-cli resolves ~/.gemini
// under (utils/paths.ts homedir()); it is not a direct override of the
// ".gemini" path itself.
const geminiHomeEnv = "GEMINI_CLI_HOME"

type geminiReader struct{}

// NewGeminiReader returns a Reader for Gemini CLI's session transcripts.
func NewGeminiReader() Reader { return &geminiReader{} }

func (r *geminiReader) Agent() string { return "gemini" }

func geminiHome() (string, error) {
	if h := os.Getenv(geminiHomeEnv); h != "" {
		return h, nil
	}
	return os.UserHomeDir()
}

func geminiRoot() (string, error) {
	home, err := geminiHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".gemini"), nil
}

// Available probes <root>/tmp — where sessions actually live — never
// <root> itself. ~/.gemini is the gemini preset's own push target, so it
// exists, full of friday-written config, on machines where Gemini CLI was
// never installed; stat'ing it reported the reader available and then
// returned zero sessions. The reason echoes the resolved root rather than a
// literal "~/.gemini", so a user who relocated the install with
// GEMINI_CLI_HOME is told about the path friday really looked at.
func (r *geminiReader) Available() (bool, string) {
	root, err := geminiRoot()
	if err != nil {
		return false, "cannot resolve home directory"
	}
	tmp := filepath.Join(root, "tmp")
	info, err := os.Stat(tmp)
	if err != nil || !info.IsDir() {
		return false, tmp + " not found"
	}
	return true, ""
}

// sessionGlob matches every current-format main-agent session file across
// every project the user has ever worked in, without assuming which
// project-slug directories exist ahead of time. Verified against
// chatRecordingService.ts: chatsDir = join(getProjectTempDir(), "chats"),
// filename = SESSION_FILE_PREFIX + timestamp + "-" + sessionId.slice(0,8) +
// ".jsonl".
//
// Two categories are invisible to this pattern by construction, both of
// which `friday sessions --stats` therefore under-reports for gemini:
//
//   - Legacy monolithic pre-JSONL session-*.json files. They don't match
//     the .jsonl suffix, so they are never visited (which satisfies "must
//     not crash on them" for free), but gemini-cli's own --resume still
//     lists them, so a user mid-migration sees sessions in gemini that
//     friday does not.
//   - Subagent transcripts, written to chats/<parentSessionId>/<sessionId>.jsonl
//     — one directory deeper, with no "session-" prefix. Leaving them out
//     of the session list is deliberate (they are not sessions a user
//     started), but it also leaves their token spend out of the rollup,
//     where claude and oh-my-pi include every transcript their store holds.
func sessionGlob(root string) string {
	return filepath.Join(root, "tmp", "*", "chats", "session-*.jsonl")
}

func (r *geminiReader) List() ([]Record, error) {
	root, err := geminiRoot()
	if err != nil {
		return nil, err
	}
	matches, err := filepath.Glob(sessionGlob(root))
	if err != nil {
		return nil, err
	}
	slugs := geminiSlugProjects(root)
	records := make([]Record, 0, len(matches))
	for _, path := range matches {
		parsed, err := parseGeminiSession(path, slugs)
		if err != nil {
			// The file vanished or is unreadable at the OS level (permissions,
			// concurrent delete). One bad session must never fail the whole
			// index (robustness rule 2/3) - skip it and keep going.
			continue
		}
		records = append(records, parsed.record)
	}
	return records, nil
}

func (r *geminiReader) Load(id string) (*Transcript, error) {
	root, err := geminiRoot()
	if err != nil {
		return nil, err
	}
	matches, err := filepath.Glob(sessionGlob(root))
	if err != nil {
		return nil, err
	}
	slugs := geminiSlugProjects(root)
	for _, path := range matches {
		parsed, err := parseGeminiSession(path, slugs)
		if err != nil {
			continue
		}
		if parsed.record.ID == id {
			return &Transcript{Record: parsed.record, Turns: parsed.turns}, nil
		}
	}
	return nil, fmt.Errorf("gemini: session %q not found", id)
}

type geminiParsed struct {
	record Record
	turns  []Turn
}

// geminiTokens mirrors TokensSummary in chatRecordingTypes.ts. Gemini's own
// wire format has no cost field anywhere (ConversationRecord, MessageRecord
// and TokensSummary were all read end to end) - only token counts - so
// Record.CostKnown stays false for every gemini session.
//
// Thoughts is thoughtsTokenCount: thinking tokens the model billed as
// completion, so they fold into Record.OutputTokens exactly as opencode's
// equivalent `reasoning` field does. Tool is tool-response tokens fed back
// to the model, so they fold into Record.InputTokens. TokensSummary's own
// `total` is deliberately not decoded: it already includes both, and
// Record.TotalTokens is the sum of friday's four axes in every other
// reader, so taking gemini's total would make one column mean two things.
type geminiTokens struct {
	Input    int `json:"input"`
	Output   int `json:"output"`
	Cached   int `json:"cached"`
	Thoughts int `json:"thoughts"`
	Tool     int `json:"tool"`
}

// geminiToolCall mirrors ToolCallRecord in chatRecordingTypes.ts.
type geminiToolCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

// geminiMessage mirrors MessageRecord (BaseMessageRecord + the 'gemini'
// variant of ConversationRecordExtra) in chatRecordingTypes.ts.
type geminiMessage struct {
	ID        string           `json:"id"`
	Timestamp string           `json:"timestamp"`
	Type      string           `json:"type"` // user | gemini | info | error | warning
	Content   json.RawMessage  `json:"content"`
	Model     string           `json:"model"`
	Tokens    *geminiTokens    `json:"tokens"`
	ToolCalls []geminiToolCall `json:"toolCalls"`
}

// geminiMeta mirrors PartialMetadataRecord in chatRecordingTypes.ts - the
// first line of every session file.
type geminiMeta struct {
	SessionID   string `json:"sessionId"`
	StartTime   string `json:"startTime"`
	LastUpdated string `json:"lastUpdated"`
}

// geminiUpdate mirrors MetadataUpdateRecord ({"$set": Partial<ConversationRecord>}).
type geminiUpdate struct {
	Set struct {
		SessionID   *string `json:"sessionId"`
		LastUpdated *string `json:"lastUpdated"`
	} `json:"$set"`
}

// parseGeminiSession scans one session-*.jsonl file once, producing both the
// summary Record (for List) and the full Turn list (for Load), so both
// callers share one parse instead of duplicating the line-format knowledge.
// slugs is the inverted project registry, read once by the caller.
func parseGeminiSession(path string, slugs map[string]string) (*geminiParsed, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, statErr := f.Stat()

	rec := Record{Agent: "gemini", Path: path}
	var turns []Turn
	toolSet := map[string]struct{}{}
	fileSet := map[string]struct{}{}
	var firstModel string
	var started, updated time.Time

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxSessionLineBytes) // tool output records can run large
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}

		var probe map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &probe); err != nil {
			rec.UnreadableLines++
			continue
		}

		switch {
		case has(probe, "$rewindTo"):
			// A resume-time rewind marker. friday reports the full recorded
			// history rather than replaying a live rewind, so this control
			// record carries nothing to extract (robustness rule 1: unknown/
			// not-needed shapes are skipped, not errors).

		case has(probe, "$set"):
			var upd geminiUpdate
			if err := json.Unmarshal([]byte(line), &upd); err != nil {
				rec.UnreadableLines++
				continue
			}
			if upd.Set.SessionID != nil && *upd.Set.SessionID != "" {
				rec.ID = *upd.Set.SessionID
			}
			if upd.Set.LastUpdated != nil {
				if t, err := time.Parse(time.RFC3339, *upd.Set.LastUpdated); err == nil {
					updated = t
				}
			}

		case has(probe, "sessionId") && has(probe, "projectHash"):
			var meta geminiMeta
			if err := json.Unmarshal([]byte(line), &meta); err != nil {
				rec.UnreadableLines++
				continue
			}
			if rec.ID == "" {
				rec.ID = meta.SessionID
			}
			if t, err := time.Parse(time.RFC3339, meta.StartTime); err == nil {
				started = t
			}
			if t, err := time.Parse(time.RFC3339, meta.LastUpdated); err == nil {
				updated = t
			}

		case has(probe, "id") && has(probe, "timestamp"):
			var msg geminiMessage
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				rec.UnreadableLines++
				continue
			}
			at, _ := time.Parse(time.RFC3339, msg.Timestamp)
			turn := Turn{Role: geminiRole(msg.Type), Text: extractGeminiText(msg.Content), At: at}

			if msg.Type == "gemini" {
				if firstModel == "" && msg.Model != "" {
					firstModel = msg.Model
				}
				if msg.Tokens != nil {
					rec.InputTokens += msg.Tokens.Input + msg.Tokens.Tool
					rec.OutputTokens += msg.Tokens.Output + msg.Tokens.Thoughts
					rec.CacheReadTokens += msg.Tokens.Cached
				}
				if len(msg.ToolCalls) > 0 {
					turn.Tool = msg.ToolCalls[0].Name
					var turnFiles []string
					for _, tc := range msg.ToolCalls {
						if tc.Name != "" {
							toolSet[tc.Name] = struct{}{}
						}
						// filePathArgKeys is the package-wide list, so an MCP
						// tool naming its argument filePath is attributed here
						// too. gemini-cli's own read-many-files is still not
						// covered on purpose: its `include` holds glob
						// patterns, not concrete paths.
						for _, key := range filePathArgKeys {
							v, ok := tc.Args[key]
							if !ok {
								continue
							}
							s, ok := v.(string)
							if !ok || s == "" {
								continue
							}
							fileSet[s] = struct{}{}
							turnFiles = append(turnFiles, s)
						}
					}
					turn.Files = sortedDistinct(turnFiles)
				}
			}
			turns = append(turns, turn)

		default:
			// Unrecognized record shape (future record types, etc.) - skipped
			// per robustness rule 1, not counted as unreadable: it parsed as
			// valid JSON, friday just has no use for its shape.
		}
	}
	if err := sc.Err(); err != nil {
		rec.UnreadableLines++
	}

	rec.Turns = len(turns)
	rec.TotalTokens = rec.InputTokens + rec.OutputTokens + rec.CacheCreationTokens + rec.CacheReadTokens
	rec.Model = firstModel
	rec.Tools = sortedKeys(toolSet)
	rec.Files = sortedKeys(fileSet)

	mtime := time.Time{}
	if statErr == nil {
		mtime = info.ModTime()
	}
	if started.IsZero() {
		started = mtime
	}
	if updated.IsZero() {
		if len(turns) > 0 && !turns[len(turns)-1].At.IsZero() {
			updated = turns[len(turns)-1].At
		} else {
			updated = mtime
		}
	}
	rec.Started = started
	rec.Updated = updated

	if rec.ID == "" {
		// Every line failed (an entirely corrupt file, or one with no
		// metadata record at all). Fall back to the filename stem so the
		// session is still listed and remains addressable by Load - never
		// silently dropped over unreadable content.
		rec.ID = strings.TrimSuffix(filepath.Base(path), ".jsonl")
	}

	rec.Project = resolveGeminiProject(path, slugs)

	return &geminiParsed{record: rec, turns: turns}, nil
}

// resolveGeminiProject recovers the project directory for a session file's
// <root>/tmp/<id>/chats/session-*.jsonl path. It never guesses; it tries
// three sources of truth, most specific first: gemini-cli's own ownership
// marker naming the exact path, then the project registry that named the
// slug, then the one-way sha256(project root) hash matched forward against
// the current process's cwd. No match leaves Project empty.
func resolveGeminiProject(sessionFile string, slugs map[string]string) string {
	idDir := filepath.Dir(filepath.Dir(sessionFile)) // .../tmp/<id>
	id := filepath.Base(idDir)

	if data, err := os.ReadFile(filepath.Join(idDir, geminiProjectRootMarker)); err == nil {
		if p := strings.TrimSpace(string(data)); p != "" {
			return normalizeProjectPath(p)
		}
	}

	if dir, ok := slugs[id]; ok {
		return normalizeProjectPath(dir)
	}

	if dir, ok := geminiProjectHashes()[strings.ToLower(id)]; ok {
		return normalizeProjectPath(dir)
	}
	return ""
}

// geminiSlugProjects inverts gemini-cli's own slug registry,
// <root>/projects.json — `{"projects": {"<normalized project path>":
// "<slug>"}}` per config/projectRegistry.ts, located with
// Storage.getGlobalGeminiDir(). Without it, a slug-named session directory
// whose .project_root marker is missing (an install that generated slugs
// before ensureOwnershipMarkers existed, or a user who cleaned the temp
// dirs) has no recoverable project at all: forward hash matching cannot
// help, because the directory is named by a slug and not by a hash. Read
// once per List/Load, not once per session.
func geminiSlugProjects(root string) map[string]string {
	data, err := os.ReadFile(filepath.Join(root, "projects.json"))
	if err != nil {
		return nil
	}
	var registry struct {
		Projects map[string]string `json:"projects"`
	}
	if err := json.Unmarshal(data, &registry); err != nil {
		return nil
	}
	slugs := make(map[string]string, len(registry.Projects))
	for project, slug := range registry.Projects {
		slugs[slug] = project
	}
	return slugs
}

// normalizeProjectPath brings a project path recovered from another agent's
// bookkeeping onto the same spelling every other reader in this package
// reports, so the Project column and `friday sessions --project <substr>`
// (a case-sensitive Contains) match across agents. This is not cosmetic for
// gemini: projectRegistry.ts lowercases the whole path on win32 before
// writing the .project_root marker, so the marker says `d:\friday` where
// oh-my-pi's header says `D:\friday`.
func normalizeProjectPath(p string) string {
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return filepath.Clean(p)
	}
	return onDiskCasing(real)
}

// onDiskCasing rewrites each component of an existing path to the casing the
// filesystem itself stores.
//
// EvalSymlinks alone is not enough. It recovers casing on Windows, but not on
// macOS, whose default APFS volume is case-insensitive and case-preserving:
// gemini-cli's lowercased marker resolves there and comes back still
// lowercased, leaving one directory wearing two spellings in the Project
// column - exactly the defect normalizeProjectPath exists to prevent, on the
// one platform where it survived. On a case-sensitive filesystem every
// component matches byte-for-byte and this walk changes nothing, so there is
// no per-OS branch to drift.
//
// An exact match always beats a case-insensitive one, so a case-sensitive
// filesystem holding both `Team.md` and `team.md` keeps the one it was asked
// for. A component whose parent cannot be listed leaves the remainder as
// given: a best-effort spelling beats discarding a path that is otherwise
// correct.
func onDiskCasing(p string) string {
	vol := filepath.VolumeName(p)
	rest := p[len(vol):]
	sep := string(filepath.Separator)
	cur := vol
	if strings.HasPrefix(rest, sep) {
		cur += sep
	}
	for _, part := range strings.Split(strings.Trim(rest, sep), sep) {
		if part == "" {
			continue
		}
		cur = filepath.Join(cur, onDiskName(cur, part))
	}
	if cur == "" {
		return filepath.Clean(p)
	}
	return cur
}

// onDiskName returns dir's own spelling of name, or name unchanged when dir
// is unreadable or holds nothing matching it.
func onDiskName(dir, name string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return name
	}
	match := name
	for _, e := range entries {
		if e.Name() == name {
			return name
		}
		if strings.EqualFold(e.Name(), name) {
			match = e.Name()
		}
	}
	return match
}

// geminiProjectHashes maps sha256(project root) to the project root, for
// every spelling of the current directory gemini-cli could have hashed.
// Only the cwd is reachable: getProjectHash is one-way, so friday can only
// hash forward and compare.
//
// getProjectHash in utils/paths.ts is
// `createHash('sha256').update(projectRoot).digest('hex')` — the string
// verbatim, with no realpath and no case folding — so hashing a resolved
// path (which is what this used to do) can never match. The two candidates
// are the two strings gemini-cli itself could have held: the cwd as the OS
// reports it, and the lowercased form projectRegistry.ts normalizes to on
// win32.
func geminiProjectHashes() map[string]string {
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	out := make(map[string]string, 2)
	for _, candidate := range []string{cwd, strings.ToLower(cwd)} {
		sum := sha256.Sum256([]byte(candidate))
		out[hex.EncodeToString(sum[:])] = candidate
	}
	return out
}

// geminiRole maps gemini-cli's MessageRecord.type onto this package's shared
// role vocabulary. Only "gemini" and "user" denote a canonical concept;
// "info", "error" and "warning" are UI notices, not chat roles, and pass
// through verbatim rather than being collapsed into one generic role, which
// would lose the distinction between them.
func geminiRole(msgType string) string {
	switch msgType {
	case "user":
		return RoleUser
	case "gemini":
		return RoleAssistant
	}
	return msgType
}

// extractGeminiText pulls display text out of a PartListUnion content field,
// which gemini-cli's own isTextPart/partListUnionToString helpers treat as
// either a bare string, an array of {text} parts, or a single {text} part.
func extractGeminiText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}

	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parts); err == nil {
		var sb strings.Builder
		for _, p := range parts {
			if t, ok := p["text"]; ok {
				var ts string
				if json.Unmarshal(t, &ts) == nil {
					sb.WriteString(ts)
				}
			}
		}
		return sb.String()
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err == nil {
		if t, ok := obj["text"]; ok {
			var ts string
			if json.Unmarshal(t, &ts) == nil {
				return ts
			}
		}
	}
	return ""
}

func has(m map[string]json.RawMessage, key string) bool {
	_, ok := m[key]
	return ok
}
