package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// codexFixture writes a synthetic rollout at
// <root>/sessions/2026/08/26/rollout-2026-08-26T09-59-00-<id>.jsonl,
// matching codex-rs/rollout/src/rollout_file_name.rs's canonical naming.
// lines are joined with "\n" verbatim, so a caller can inject a
// deliberately malformed line, and the file is newline-terminated.
func codexFixture(t *testing.T, root, id string, lines []string) string {
	t.Helper()
	return codexFixtureRaw(t, root, id, strings.Join(lines, "\n")+"\n")
}

// codexFixtureRaw writes content byte-for-byte, for the shapes a []string
// cannot express -- an unterminated final line, an oversized line.
func codexFixtureRaw(t *testing.T, root, id, content string) string {
	t.Helper()
	dir := filepath.Join(root, "sessions", "2026", "08", "26")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-2026-08-26T09-59-00-"+id+".jsonl")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// wellFormedCodexLines builds a realistic rollout: a SessionMeta header, a
// turn_context carrying the precise per-turn model, a user message, an
// assistant message, an MCP-style function_call with a "path" argument,
// its function_call_output, and a token_count event_msg -- matching the
// wire shapes verified against github.com/openai/codex's
// codex-rs/history/src/rollout_payload.rs and
// codex-rs/protocol/src/{protocol,models}.rs.
func wellFormedCodexLines(id, cwd string) []string {
	return []string{
		`{"timestamp":"2026-08-26T09:59:00.000Z","type":"session_meta","payload":{"id":"` + id + `","timestamp":"2026-08-26T09:59:00.000Z","cwd":"` + cwd + `","originator":"codex_cli_rs","cli_version":"0.55.0","model_provider":"openai"}}`,
		`{"timestamp":"2026-08-26T09:59:05.000Z","ordinal":1,"type":"turn_context","payload":{"cwd":"` + cwd + `","model":"gpt-5.1-codex"}}`,
		`{"timestamp":"2026-08-26T09:59:06.000Z","ordinal":2,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Fix the bug in main.go"}]}}`,
		`{"timestamp":"2026-08-26T09:59:10.000Z","ordinal":3,"type":"response_item","payload":{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"I'll take a look."}]}}`,
		`{"timestamp":"2026-08-26T09:59:11.000Z","ordinal":4,"type":"response_item","payload":{"type":"function_call","id":"fc_1","name":"mcp__fs__read_file","call_id":"call_1","arguments":"{\"path\":\"/repo/main.go\"}"}}`,
		`{"timestamp":"2026-08-26T09:59:12.000Z","ordinal":5,"type":"response_item","payload":{"type":"function_call_output","call_id":"call_1","output":"package main\n"}}`,
		`{"timestamp":"2026-08-26T09:59:15.000Z","ordinal":6,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":120,"cached_input_tokens":20,"cache_write_input_tokens":0,"output_tokens":45,"reasoning_output_tokens":0,"total_tokens":165},"last_token_usage":{"input_tokens":120,"cached_input_tokens":20,"cache_write_input_tokens":0,"output_tokens":45,"reasoning_output_tokens":0,"total_tokens":165},"model_context_window":128000},"rate_limits":null}}`,
	}
}

func TestCodexAvailable(t *testing.T) {
	t.Run("root exists", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv(codexHomeEnv, dir)
		r := NewCodexReader()
		ok, reason := r.Available()
		if !ok {
			t.Fatalf("Available() = false, %q; want true", reason)
		}
		if reason != "" {
			t.Errorf("Available() reason = %q, want empty when available", reason)
		}
	})

	t.Run("root missing", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "does-not-exist")
		t.Setenv(codexHomeEnv, dir)
		r := NewCodexReader()
		ok, reason := r.Available()
		if ok {
			t.Fatal("Available() = true for a missing root, want false")
		}
		if reason == "" {
			t.Error("Available() reason is empty, want a human-readable explanation")
		}
	})
}

func TestCodexListWellFormedSession(t *testing.T) {
	root := t.TempDir()
	t.Setenv(codexHomeEnv, root)
	const id = "11111111-1111-1111-1111-111111111111"
	const cwd = "/repo"
	codexFixture(t, root, id, wellFormedCodexLines(id, cwd))

	r := NewCodexReader()
	records, err := r.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("List() returned %d records, want 1", len(records))
	}
	rec := records[0]

	if rec.Agent != "codex" {
		t.Errorf("Agent = %q, want codex", rec.Agent)
	}
	if rec.ID != id {
		t.Errorf("ID = %q, want %q", rec.ID, id)
	}
	if rec.Project != cwd {
		t.Errorf("Project = %q, want %q", rec.Project, cwd)
	}
	wantStarted := time.Date(2026, 8, 26, 9, 59, 0, 0, time.UTC)
	if !rec.Started.Equal(wantStarted) {
		t.Errorf("Started = %v, want %v", rec.Started, wantStarted)
	}
	// turn_context's precise per-turn model must win over SessionMeta's
	// coarse model_provider, which becomes the "provider/" prefix every
	// reader with a provider on the wire uses.
	if rec.Model != "openai/gpt-5.1-codex" {
		t.Errorf("Model = %q, want openai/gpt-5.1-codex (model_provider + turn_context.model)", rec.Model)
	}
	// Turns is the chat-turn count, comparable with every other reader's:
	// 2 messages, function_call, function_call_output. turn_context and
	// event_msg are transport, not turns.
	if rec.Turns != 4 {
		t.Errorf("Turns = %d, want 4", rec.Turns)
	}
	if len(rec.Tools) != 1 || rec.Tools[0] != "mcp__fs__read_file" {
		t.Errorf("Tools = %v, want [mcp__fs__read_file]", rec.Tools)
	}
	if len(rec.Files) != 1 || rec.Files[0] != "/repo/main.go" {
		t.Errorf("Files = %v, want [/repo/main.go]", rec.Files)
	}
	if rec.CostKnown {
		t.Error("CostKnown = true, want false: Codex's rollout format states no cost figure")
	}
	if rec.InputTokens != 120 || rec.OutputTokens != 45 || rec.CacheReadTokens != 20 || rec.TotalTokens != 165 {
		t.Errorf("token counts = in:%d out:%d cacheRead:%d total:%d, want in:120 out:45 cacheRead:20 total:165",
			rec.InputTokens, rec.OutputTokens, rec.CacheReadTokens, rec.TotalTokens)
	}
	if rec.UnreadableLines != 0 {
		t.Errorf("UnreadableLines = %d, want 0 for a well-formed fixture", rec.UnreadableLines)
	}
}

func TestCodexLoadReturnsOrderedTurns(t *testing.T) {
	root := t.TempDir()
	t.Setenv(codexHomeEnv, root)
	const id = "22222222-2222-2222-2222-222222222222"
	codexFixture(t, root, id, wellFormedCodexLines(id, "/repo"))

	r := NewCodexReader()
	transcript, err := r.Load(id)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(transcript.Turns) != 4 {
		t.Fatalf("len(Turns) = %d, want 4 (2 messages, function_call, function_call_output)", len(transcript.Turns))
	}

	user := transcript.Turns[0]
	if user.Role != "user" || user.Text != "Fix the bug in main.go" {
		t.Errorf("Turns[0] = %+v, want role=user text=%q", user, "Fix the bug in main.go")
	}
	assistant := transcript.Turns[1]
	if assistant.Role != "assistant" || assistant.Text != "I'll take a look." {
		t.Errorf("Turns[1] = %+v, want role=assistant text=%q", assistant, "I'll take a look.")
	}
	call := transcript.Turns[2]
	if call.Role != "tool" || call.Tool != "mcp__fs__read_file" {
		t.Errorf("Turns[2] = %+v, want role=tool tool=mcp__fs__read_file", call)
	}
	if len(call.Files) != 1 || call.Files[0] != "/repo/main.go" {
		t.Errorf("Turns[2].Files = %v, want [/repo/main.go]", call.Files)
	}
	result := transcript.Turns[3]
	if result.Role != "tool_result" || result.Text != "package main\n" {
		t.Errorf("Turns[3] = %+v, want role=tool_result text=%q", result, "package main\n")
	}

	// Record.Turns and len(Transcript.Turns) must agree — the shadowing in
	// session.go is two spellings of one number, not two numbers.
	if transcript.Record.Turns != len(transcript.Turns) {
		t.Errorf("Record.Turns = %d, len(Turns) = %d, want equal", transcript.Record.Turns, len(transcript.Turns))
	}
}

func TestCodexLoadUnknownIDErrors(t *testing.T) {
	root := t.TempDir()
	t.Setenv(codexHomeEnv, root)
	codexFixture(t, root, "33333333-3333-3333-3333-333333333333", wellFormedCodexLines("33333333-3333-3333-3333-333333333333", "/repo"))

	r := NewCodexReader()
	if _, err := r.Load("does-not-exist"); err == nil {
		t.Fatal("Load() with an unknown id returned nil error, want an error")
	}
}

func TestCodexListToleratesCorruptLine(t *testing.T) {
	root := t.TempDir()
	t.Setenv(codexHomeEnv, root)
	const id = "44444444-4444-4444-4444-444444444444"
	lines := wellFormedCodexLines(id, "/repo")
	// Splice one deliberately malformed JSON line into the middle of an
	// otherwise well-formed transcript -- session.go's robustness contract
	// requires this to increment UnreadableLines, never fail List().
	lines = append(lines[:3], append([]string{`{"type":"response_item","payload":{"type":"message","role":"user"`}, lines[3:]...)...)
	codexFixture(t, root, id, lines)

	r := NewCodexReader()
	records, err := r.List()
	if err != nil {
		t.Fatalf("List() error = %v, want nil even with a corrupt line", err)
	}
	if len(records) != 1 {
		t.Fatalf("List() returned %d records, want 1 (the session must still list)", len(records))
	}
	if records[0].UnreadableLines < 1 {
		t.Errorf("UnreadableLines = %d, want >= 1", records[0].UnreadableLines)
	}
	// The corrupt line never became a turn, so the 4 well-formed chat
	// turns are still all that Turns reports.
	if records[0].Turns != 4 {
		t.Errorf("Turns = %d, want 4 (the corrupt line is unreadable, not a turn)", records[0].Turns)
	}
}

func TestCodexListSkipsMissingRoot(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	t.Setenv(codexHomeEnv, dir)
	r := NewCodexReader()
	records, err := r.List()
	if err != nil {
		t.Fatalf("List() on a missing root returned error = %v, want nil (empty list)", err)
	}
	if len(records) != 0 {
		t.Errorf("List() on a missing root returned %d records, want 0", len(records))
	}
}

func TestCodexIDFromFilenameFallback(t *testing.T) {
	// A session whose SessionMeta line is entirely unparseable must still
	// list, with its id recovered from the canonical filename.
	root := t.TempDir()
	t.Setenv(codexHomeEnv, root)
	const wantID = "55555555-5555-5555-5555-555555555555"
	lines := []string{
		`not json at all`,
		`{"timestamp":"2026-08-26T09:59:06.000Z","ordinal":1,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}}`,
	}
	codexFixture(t, root, wantID, lines)

	r := NewCodexReader()
	records, err := r.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("List() returned %d records, want 1", len(records))
	}
	if records[0].ID != wantID {
		t.Errorf("ID = %q, want %q recovered from the filename", records[0].ID, wantID)
	}
	if records[0].UnreadableLines < 1 {
		t.Errorf("UnreadableLines = %d, want >= 1 for the corrupt header line", records[0].UnreadableLines)
	}
}

// TestCodexOversizedLineIsReportedNotSilentlyTruncated is the regression
// test for the silent-truncation bug: a single line over
// maxSessionLineBytes stops bufio.Scanner, and without a scanner.Err()
// check the record came back with err == nil, UnreadableLines == 0 and
// every turn and token total after that line simply missing -- presented
// by `friday sessions` as a complete session. Codex writes whole tool
// outputs verbatim, so one read of a large file does this.
func TestCodexOversizedLineIsReportedNotSilentlyTruncated(t *testing.T) {
	root := t.TempDir()
	t.Setenv(codexHomeEnv, root)
	const id = "66666666-6666-6666-6666-666666666666"

	lines := wellFormedCodexLines(id, "/repo")
	oversized := `{"timestamp":"2026-08-26T09:59:13.000Z","ordinal":7,"type":"response_item","payload":{"type":"function_call_output","call_id":"call_2","output":"` +
		strings.Repeat("x", maxSessionLineBytes) + `"}}`
	// Header, turn_context, user, assistant, function_call,
	// function_call_output, THEN the oversized line, then the token_count
	// event that carries the session's final totals.
	spliced := append(append([]string{}, lines[:6]...), oversized, lines[6])
	codexFixture(t, root, id, spliced)

	records, err := NewCodexReader().List()
	if err != nil {
		t.Fatalf("List() error = %v, want nil: an oversized line degrades, it does not fail", err)
	}
	if len(records) != 1 {
		t.Fatalf("List() returned %d records, want 1", len(records))
	}
	rec := records[0]

	if rec.UnreadableLines < 1 {
		t.Errorf("UnreadableLines = %d, want >= 1: the oversized line must be reported, not silently dropped", rec.UnreadableLines)
	}
	// Proof the record really is partial, so the warning is not cosmetic:
	// the token_count event after the oversized line was never reached.
	if rec.TotalTokens != 0 {
		t.Errorf("TotalTokens = %d, want 0: everything after the oversized line is unread", rec.TotalTokens)
	}
	// The turns before the oversized line must survive.
	if rec.Turns != 4 {
		t.Errorf("Turns = %d, want 4 (the turns read before the cap was hit)", rec.Turns)
	}
	if rec.ID != id {
		t.Errorf("ID = %q, want %q: the header was read before the cap was hit", rec.ID, id)
	}
}

// TestCodexUnterminatedFinalLine covers the shape every live session has:
// a transcript still being appended to, whose last line has no newline yet.
func TestCodexUnterminatedFinalLine(t *testing.T) {
	root := t.TempDir()
	t.Setenv(codexHomeEnv, root)
	const id = "77777777-7777-7777-7777-777777777777"
	codexFixtureRaw(t, root, id, strings.Join(wellFormedCodexLines(id, "/repo"), "\n"))

	transcript, err := NewCodexReader().Load(id)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(transcript.Turns) != 4 {
		t.Fatalf("len(Turns) = %d, want 4 — same as the newline-terminated fixture", len(transcript.Turns))
	}
	if transcript.UnreadableLines != 0 {
		t.Errorf("UnreadableLines = %d, want 0: a missing final newline is not corruption", transcript.UnreadableLines)
	}
	// The unterminated line is the token_count event, so its totals prove
	// the final line was read.
	if transcript.TotalTokens != 165 {
		t.Errorf("TotalTokens = %d, want 165: the unterminated final line must still be parsed", transcript.TotalTokens)
	}
}

// TestCodexNonMetaFirstLineIsKept covers a rotated or repaired rollout
// whose first line is a real item rather than the SessionMeta header: the
// line used to be dropped unconditionally, appearing in neither Turns nor
// UnreadableLines.
func TestCodexNonMetaFirstLineIsKept(t *testing.T) {
	root := t.TempDir()
	t.Setenv(codexHomeEnv, root)
	const id = "88888888-8888-8888-8888-888888888888"
	lines := []string{
		`{"timestamp":"2026-08-26T09:59:06.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"first line is a real turn"}]}}`,
		`{"timestamp":"2026-08-26T09:59:07.000Z","ordinal":1,"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}}`,
	}
	codexFixture(t, root, id, lines)

	transcript, err := NewCodexReader().Load(id)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(transcript.Turns) != 2 {
		t.Fatalf("len(Turns) = %d, want 2: a non-SessionMeta first line is still a turn", len(transcript.Turns))
	}
	if transcript.Turns[0].Text != "first line is a real turn" {
		t.Errorf("Turns[0].Text = %q, want the first line's content", transcript.Turns[0].Text)
	}
	if transcript.UnreadableLines != 0 {
		t.Errorf("UnreadableLines = %d, want 0: the line parsed fine, it just was not metadata", transcript.UnreadableLines)
	}
}

// TestCodexLoadFallsBackWhenFilenameIDDiffers guards the filename
// pre-filter: the filename is a hint, so a rollout whose SessionMeta id
// does not match its own filename must still be findable.
func TestCodexLoadFallsBackWhenFilenameIDDiffers(t *testing.T) {
	root := t.TempDir()
	t.Setenv(codexHomeEnv, root)
	const metaID = "99999999-9999-9999-9999-999999999999"
	const filenameID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	codexFixture(t, root, filenameID, wellFormedCodexLines(metaID, "/repo"))

	transcript, err := NewCodexReader().Load(metaID)
	if err != nil {
		t.Fatalf("Load(%q) error = %v, want the session found by its SessionMeta id", metaID, err)
	}
	if transcript.ID != metaID {
		t.Errorf("ID = %q, want %q", transcript.ID, metaID)
	}
	if len(transcript.Turns) != 4 {
		t.Errorf("len(Turns) = %d, want 4", len(transcript.Turns))
	}
	// And the filename id must not be resolvable, since no session claims it.
	if _, err := NewCodexReader().Load(filenameID); err == nil {
		t.Errorf("Load(%q) succeeded; the filename is a hint, not the id", filenameID)
	}
}
