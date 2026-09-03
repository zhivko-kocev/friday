// OpenCode is the one agent whose session store is a database
// (~/.local/share/opencode/opencode.db, Drizzle-migrated SQLite), not a
// directory of files this package can open directly. friday never links a
// Go SQLite driver (it would break the project's CGO_ENABLED=0 release
// matrix), so this reader shells out to the user's own `opencode` binary,
// exactly the way internal/git shells out to `git`: every operation is one
// short-lived, context-timed exec.Command against a command opencode itself
// documents and already ships.
//
// Two invocations, both verified against the CLI's own source
// (packages/opencode/src/cli/cmd/session.ts and .../export.ts on
// github.com/sst/opencode, branch dev):
//
//   - `opencode session list --format json` prints exactly
//     [{id,title,updated,created,projectId,directory}, ...] — note there is
//     no model or message count in this shape, so List() leaves Model and
//     Turns at their zero values; only Load() (which parses the full
//     transcript) can populate them.
//   - `opencode export <id>` prints exactly {"info": Session.Info,
//     "messages": [{"info": Message, "parts": Part[]}, ...]}. The id
//     argument is always supplied by the caller (List()'s own output, or
//     the CLI layer) — export with no id drops into an interactive
//     @clack/prompts picker with no TTY check, which would hang friday
//     forever, so this reader never calls export without one.
//
// Scope, measured rather than assumed: `opencode session list` enumerates
// only the sessions of the project the command runs in (746/746/534/0 bytes
// of output from four different directories on one machine), and the CLI
// exposes no flag to widen it — `session list` takes only --max-count and
// --format, and the top level has no project-enumeration command. So this
// one reader is cwd-scoped where every other Reader in this package
// enumerates the whole store, and `friday sessions` shows a different
// opencode slice depending on where it is invoked. That is a limit of the
// only interface opencode offers, not a choice, and it is stated here
// because callers cannot discover it from the Reader interface.
package session

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const opencodeAgent = "opencode"

// opencodeTimeout bounds every exec.Command this reader runs. A hung or
// misbehaving opencode binary (cold MCP boot, a wedged server it tried to
// attach to, ...) must never hang friday itself.
const opencodeTimeout = 5 * time.Second

// opencodeRunner executes `opencode <args...>` and returns its stdout.
// Extracted as a seam so tests can exercise the JSON-parsing logic below
// against synthetic payloads without a real opencode binary on PATH.
type opencodeRunner func(ctx context.Context, args ...string) ([]byte, error)

// opencodeLookPath resolves the opencode binary. A package-level var for
// the same reason as opencodeRunner: tests inject a fake to make
// Available()'s "binary present but the probe fails" branch reachable
// without needing opencode actually installed.
var opencodeLookPath = exec.LookPath

// runOpencode is the real opencodeRunner used outside tests. stdout and
// stderr are captured separately — unlike internal/git's CombinedOutput,
// opencode's own commands write human progress text to stderr even on
// success (export.ts: `process.stderr.write("Exporting session: ...")`),
// so stdout must stay pure JSON.
func runOpencode(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "opencode", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("opencode %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// opencodeReader shells out to the opencode CLI for every operation; it
// never reads ~/.local/share/opencode/opencode.db directly (D4 plus the
// no-SQLite-driver constraint).
type opencodeReader struct {
	run opencodeRunner
}

// NewOpencodeReader returns the Reader for OpenCode's session store.
func NewOpencodeReader() Reader {
	return &opencodeReader{run: runOpencode}
}

func (r *opencodeReader) Agent() string { return opencodeAgent }

// Available reports true when the opencode binary is on PATH. It
// deliberately does not probe by running a command: `session list` costs
// 1.0-1.4s per spawn (the binary boots a JS runtime), List() runs the exact
// same command immediately afterwards, and a probe failure carries strictly
// less information than the List() error the CLI layer already reports as
// "unavailable".
func (r *opencodeReader) Available() (bool, string) {
	if _, err := opencodeLookPath("opencode"); err != nil {
		return false, "opencode not found on PATH"
	}
	return true, ""
}

// opencodeListItem mirrors formatSessionJSON's exact output shape in
// session.ts:
//
//	{ id, title, updated, created, projectId, directory }
//
// Deliberately absent: a model or message-count field. Those only exist
// per-message inside `opencode export`, so List() cannot cheaply populate
// Record.Model/Turns the way it does for file-based readers — Load() is
// the only place that data is available. This is the tradeoff the brief
// calls out explicitly as acceptable. The payload's own `title` and
// `projectId` are not decoded either: Record has no field for either, and
// parsing a value nothing reads is dead code.
type opencodeListItem struct {
	ID        string `json:"id"`
	Updated   int64  `json:"updated"`
	Created   int64  `json:"created"`
	Directory string `json:"directory"`
}

// List runs `opencode session list --format json` and maps each entry to a
// Record. Empty stdout means zero sessions, not a broken payload: the
// command exits 0 with no bytes at all (not `[]`) when the current project
// has none, so unmarshalling first would turn "you have no opencode
// sessions here" into `opencode: unavailable`. Beyond that the top level
// must be a JSON array — if opencode's own output stops being that (a
// genuine integration break, not "one bad session"), List reports it as an
// error rather than silently returning nothing. Individual array entries
// that don't decode into opencodeListItem's shape are skipped (robustness
// rule 1: unrecognized records are dropped, not fatal) rather than counted
// against any particular Record's UnreadableLines, since a malformed entry
// has no session ID to attach a count to.
func (r *opencodeReader) List() ([]Record, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opencodeTimeout)
	defer cancel()
	out, err := r.run(ctx, "session", "list", "--format", "json")
	if err != nil {
		return nil, fmt.Errorf("opencode session list: %w", err)
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return nil, nil
	}

	var raw []json.RawMessage
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("opencode session list: unexpected output shape: %w", err)
	}

	records := make([]Record, 0, len(raw))
	for _, entry := range raw {
		var item opencodeListItem
		if err := json.Unmarshal(entry, &item); err != nil || item.ID == "" {
			continue // unrecognized/corrupt entry — skip, never fatal
		}
		records = append(records, Record{
			Agent:   opencodeAgent,
			ID:      item.ID,
			Project: item.Directory,
			Started: opencodeMillis(item.Created),
			Updated: opencodeMillis(item.Updated),
		})
	}
	return records, nil
}

// Load runs `opencode export <id>` — always with the exact id the caller
// supplied, never bare — and parses the full transcript. The export
// payload's own field names (info.role, info.providerID/modelID,
// info.tokens.{input,output,reasoning,cache.{read,write}}, part.type,
// part.tool, part.state.input.filePath, ...) come straight from
// types.gen.ts (UserMessage/AssistantMessage/ToolPart) — verified against
// source, not guessed — so decoding into map[string]any and probing by
// name (robustness rule 4) is a deliberate defensive choice, not a
// shortcut: a future opencode release can add or rename fields without
// making every existing session unreadable.
func (r *opencodeReader) Load(id string) (*Transcript, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opencodeTimeout)
	defer cancel()
	out, err := r.run(ctx, "export", id)
	if err != nil {
		return nil, fmt.Errorf("opencode export %s: %w", id, err)
	}

	var payload struct {
		Info     map[string]any    `json:"info"`
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return nil, fmt.Errorf("opencode export %s: unexpected output shape: %w", id, err)
	}

	rec := Record{
		Agent:   opencodeAgent,
		ID:      id,
		Project: opencodeStr(payload.Info, "directory"),
		Started: opencodeTime(opencodeObj(payload.Info, "time"), "created"),
		Updated: opencodeTime(opencodeObj(payload.Info, "time"), "updated"),
	}

	tools := map[string]struct{}{}
	files := map[string]struct{}{}
	turns := make([]Turn, 0, len(payload.Messages))
	haveModel := false

	for _, raw := range payload.Messages {
		var envelope struct {
			Info  map[string]any   `json:"info"`
			Parts []map[string]any `json:"parts"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Info == nil {
			rec.UnreadableLines++
			continue
		}

		// A role this build of friday does not know (a future opencode
		// release adding a third author) parsed perfectly, so it is not an
		// unreadable line — robustness rule 1 skips it silently.
		role := opencodeStr(envelope.Info, "role")
		if role != RoleUser && role != RoleAssistant {
			continue
		}

		at := opencodeTime(opencodeObj(envelope.Info, "time"), "created")

		var textParts []string
		var turnTool string
		var turnFiles []string
		for _, part := range envelope.Parts {
			switch opencodeStr(part, "type") {
			case "text":
				if text := opencodeStr(part, "text"); text != "" {
					textParts = append(textParts, text)
				}
			case "tool":
				tool := opencodeStr(part, "tool")
				if tool != "" {
					tools[tool] = struct{}{}
					if turnTool == "" {
						turnTool = tool
					}
				}
				input := opencodeObj(opencodeObj(part, "state"), "input")
				for _, key := range []string{"filePath", "path", "file_path", "notebook_path"} {
					if fp := opencodeStr(input, key); fp != "" {
						files[fp] = struct{}{}
						turnFiles = append(turnFiles, fp)
					}
				}
			}
		}

		if role == "assistant" {
			if !haveModel {
				if model := opencodeStr(envelope.Info, "modelID"); model != "" {
					if provider := opencodeStr(envelope.Info, "providerID"); provider != "" {
						rec.Model = provider + "/" + model
					} else {
						rec.Model = model
					}
					haveModel = true
				}
			}
			// Cost: AssistantMessage.cost is real per the SDK's own schema,
			// but the plan's own research found opencode's session cost
			// reporting unreliable in practice (writes 0 regardless of
			// actual spend). Never set CostKnown from it, regardless of
			// the value observed — CostUSD stays at its zero value.
			if tokens := opencodeObj(envelope.Info, "tokens"); tokens != nil {
				rec.InputTokens += int(opencodeNum(tokens, "input"))
				// Reasoning tokens are additional assistant-generated
				// output (a subset of what the model billed as
				// completion), so they fold into OutputTokens — there is
				// no separate Record field for them.
				rec.OutputTokens += int(opencodeNum(tokens, "output")) + int(opencodeNum(tokens, "reasoning"))
				if cache := opencodeObj(tokens, "cache"); cache != nil {
					rec.CacheReadTokens += int(opencodeNum(cache, "read"))
					rec.CacheCreationTokens += int(opencodeNum(cache, "write"))
				}
			}
		}

		turns = append(turns, Turn{
			Role:  role,
			Text:  strings.Join(textParts, "\n"),
			At:    at,
			Tool:  turnTool,
			Files: turnFiles,
		})
	}

	rec.TotalTokens = rec.InputTokens + rec.OutputTokens + rec.CacheCreationTokens + rec.CacheReadTokens
	rec.Turns = len(turns)
	rec.Tools = sortedKeys(tools)
	rec.Files = sortedKeys(files)

	// Session-level time can be absent on a hand-built or heavily-edited
	// export; fall back to the transcript's own first/last turn rather
	// than leaving a zero time.Time (robustness rule 5 — there is no local
	// file to os.Stat for an mtime fallback, since this reader has none).
	if rec.Started.IsZero() && len(turns) > 0 {
		rec.Started = turns[0].At
	}
	if rec.Updated.IsZero() && len(turns) > 0 {
		rec.Updated = turns[len(turns)-1].At
	}

	return &Transcript{Record: rec, Turns: turns}, nil
}

func opencodeStr(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

func opencodeObj(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	o, _ := m[key].(map[string]any)
	return o
}

func opencodeNum(m map[string]any, key string) float64 {
	if m == nil {
		return 0
	}
	n, _ := m[key].(float64)
	return n
}

// opencodeTime reads a millisecond-epoch number field. Used both for the
// nested {created,updated} time objects and (defensively) for a bare
// top-level field, since json.Number-typed maps decode every JSON number
// as float64 regardless of nesting.
func opencodeTime(m map[string]any, key string) time.Time {
	n := opencodeNum(m, key)
	if n == 0 {
		return time.Time{}
	}
	return opencodeMillis(int64(n))
}

func opencodeMillis(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}
