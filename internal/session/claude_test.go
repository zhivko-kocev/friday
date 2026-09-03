package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeClaudeSession writes lines (already-serialized JSONL rows, one per
// call site) into <dir>/projects/<slug>/<id>.jsonl and returns that path.
func writeClaudeSession(t *testing.T, root, slug, id string, lines []string) string {
	t.Helper()
	dir := filepath.Join(root, "projects", slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".jsonl")
	content := ""
	for _, l := range lines {
		content += l + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestClaudeAvailable(t *testing.T) {
	t.Run("root exists", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "projects"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv(claudeConfigDirEnv, root)
		ok, reason := NewClaudeReader().Available()
		if !ok {
			t.Fatalf("want available, got reason %q", reason)
		}
	})
	t.Run("root missing", func(t *testing.T) {
		t.Setenv(claudeConfigDirEnv, filepath.Join(t.TempDir(), "does-not-exist"))
		ok, reason := NewClaudeReader().Available()
		if ok {
			t.Fatal("want unavailable")
		}
		if reason == "" {
			t.Error("want a non-empty reason")
		}
	})
}

// wellFormedSession is a synthetic transcript mirroring the researched
// shape: a leading summary line (no timestamp), a user turn, an assistant
// turn with a tool_use call (Read, carrying file_path) plus token usage and
// a legacy costUSD, a tool-result-only user turn echoing that tool's
// output, and a final assistant text turn.
func wellFormedSession() []string {
	return []string{
		`{"type":"summary","summary":"Fix the bug","leafUuid":"u4"}`,
		`{"type":"user","uuid":"u1","timestamp":"2026-01-15T10:00:00.000Z","cwd":"/home/dev/my-project","sessionId":"sess-1","message":{"role":"user","content":"Please read config.go"}}`,
		`{"type":"assistant","uuid":"u2","timestamp":"2026-01-15T10:00:05.500Z","cwd":"/home/dev/my-project","sessionId":"sess-1","costUSD":0.0123,"message":{"role":"assistant","model":"claude-sonnet-4-5-20250929","content":[{"type":"text","text":"Reading it now."},{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"/home/dev/my-project/config.go"}}],"usage":{"input_tokens":500,"output_tokens":40,"cache_creation_input_tokens":10,"cache_read_input_tokens":5}}}`,
		`{"type":"user","uuid":"u3","timestamp":"2026-01-15T10:00:06.000Z","cwd":"/home/dev/my-project","sessionId":"sess-1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"package main\n"}]}}`,
		`{"type":"assistant","uuid":"u4","timestamp":"2026-01-15T10:00:10.250Z","cwd":"/home/dev/my-project","sessionId":"sess-1","costUSD":0.0045,"message":{"role":"assistant","model":"claude-sonnet-4-5-20250929","content":[{"type":"text","text":"Looks fine to me."}],"usage":{"input_tokens":600,"output_tokens":20,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`,
	}
}

func TestClaudeListWellFormed(t *testing.T) {
	root := t.TempDir()
	t.Setenv(claudeConfigDirEnv, root)
	writeClaudeSession(t, root, "-home-dev-my-project", "sess-1", wellFormedSession())

	recs, err := NewClaudeReader().List()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("want 1 record, got %d", len(recs))
	}
	r := recs[0]

	if r.Agent != "claude" {
		t.Errorf("Agent = %q", r.Agent)
	}
	if r.ID != "sess-1" {
		t.Errorf("ID = %q", r.ID)
	}
	// The record-level "cwd" field is authoritative and exact -- it must
	// win over any slug reversal.
	if r.Project != "/home/dev/my-project" {
		t.Errorf("Project = %q", r.Project)
	}
	if r.Model != "claude-sonnet-4-5-20250929" {
		t.Errorf("Model = %q", r.Model)
	}
	// 4 message records: user, assistant, user(tool-result), assistant.
	// The leading "summary" line is not a chat turn.
	if r.Turns != 4 {
		t.Errorf("Turns = %d, want 4", r.Turns)
	}
	wantStart := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	if !r.Started.Equal(wantStart) {
		t.Errorf("Started = %v, want %v", r.Started, wantStart)
	}
	wantUpdated := time.Date(2026, 1, 15, 10, 0, 10, 250_000_000, time.UTC)
	if !r.Updated.Equal(wantUpdated) {
		t.Errorf("Updated = %v, want %v", r.Updated, wantUpdated)
	}

	if r.InputTokens != 1100 || r.OutputTokens != 60 || r.CacheCreationTokens != 10 || r.CacheReadTokens != 5 {
		t.Errorf("token counts = %+v", r)
	}
	if r.TotalTokens != 1175 {
		t.Errorf("TotalTokens = %d, want 1175", r.TotalTokens)
	}
	if !r.CostKnown {
		t.Fatal("want CostKnown true: costUSD was present")
	}
	if got, want := r.CostUSD, 0.0168; got < want-1e-9 || got > want+1e-9 {
		t.Errorf("CostUSD = %v, want %v", got, want)
	}

	if len(r.Tools) != 1 || r.Tools[0] != "Read" {
		t.Errorf("Tools = %v, want [Read]", r.Tools)
	}
	if len(r.Files) != 1 || r.Files[0] != "/home/dev/my-project/config.go" {
		t.Errorf("Files = %v", r.Files)
	}
	if r.UnreadableLines != 0 {
		t.Errorf("UnreadableLines = %d, want 0", r.UnreadableLines)
	}
}

func TestClaudeLoad(t *testing.T) {
	root := t.TempDir()
	t.Setenv(claudeConfigDirEnv, root)
	writeClaudeSession(t, root, "-home-dev-my-project", "sess-1", wellFormedSession())

	tr, err := NewClaudeReader().Load("sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Turns) != 4 {
		t.Fatalf("want 4 turns, got %d", len(tr.Turns))
	}

	u1 := tr.Turns[0]
	if u1.Role != "user" || u1.Text != "Please read config.go" {
		t.Errorf("turn 0 = %+v", u1)
	}

	a1 := tr.Turns[1]
	if a1.Role != "assistant" {
		t.Errorf("turn 1 role = %q", a1.Role)
	}
	if a1.Text != "Reading it now." {
		t.Errorf("turn 1 text = %q", a1.Text)
	}
	if a1.Tool != "Read" {
		t.Errorf("turn 1 tool = %q", a1.Tool)
	}
	if len(a1.Files) != 1 || a1.Files[0] != "/home/dev/my-project/config.go" {
		t.Errorf("turn 1 files = %v", a1.Files)
	}

	// A "user" record whose content is entirely tool_result blocks is a
	// synthesized echo, not a human message. It is a tool RESULT, not a
	// tool call: "tool" means a call for codex and copilot, so claude
	// spelling a result "tool" made the shared vocabulary ambiguous and
	// made `friday sessions --open` report an interrupted tool loop as a
	// clean finish.
	toolTurn := tr.Turns[2]
	if toolTurn.Role != RoleToolResult {
		t.Errorf("turn 2 role = %q, want %q", toolTurn.Role, RoleToolResult)
	}
	if toolTurn.Tool != "" {
		t.Errorf("turn 2 tool = %q, want empty: a tool_result block names no tool", toolTurn.Tool)
	}

	a2 := tr.Turns[3]
	if a2.Text != "Looks fine to me." {
		t.Errorf("turn 3 text = %q", a2.Text)
	}

	if _, err := NewClaudeReader().Load("does-not-exist"); err == nil {
		t.Error("want error for unknown id")
	}
}

// TestClaudeUnreadableLine proves robustness rule 2: a corrupt line never
// fails List/Load for that session -- it is counted and skipped.
func TestClaudeUnreadableLine(t *testing.T) {
	root := t.TempDir()
	t.Setenv(claudeConfigDirEnv, root)
	lines := append([]string{}, wellFormedSession()...)
	lines = append(lines, `{"type":"assistant","this is not valid json`)
	writeClaudeSession(t, root, "-home-dev-my-project", "sess-1", lines)

	recs, err := NewClaudeReader().List()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("want 1 record despite the corrupt line, got %d", len(recs))
	}
	if recs[0].UnreadableLines < 1 {
		t.Errorf("UnreadableLines = %d, want >= 1", recs[0].UnreadableLines)
	}
	// The well-formed turns before the corrupt line must still be intact.
	if recs[0].Turns != 4 {
		t.Errorf("Turns = %d, want 4", recs[0].Turns)
	}

	if _, err := NewClaudeReader().Load("sess-1"); err != nil {
		t.Fatalf("Load must still succeed: %v", err)
	}
}

// TestClaudeUnknownRecordTypesSkipped proves robustness rule 1: record
// shapes this reader doesn't recognize (system, file-history-snapshot) are
// silently skipped, never counted as unreadable and never counted as turns.
func TestClaudeUnknownRecordTypesSkipped(t *testing.T) {
	root := t.TempDir()
	t.Setenv(claudeConfigDirEnv, root)
	lines := []string{
		`{"type":"system","subtype":"local_command","content":"<command-name>/resume</command-name>","timestamp":"2026-02-01T00:00:00Z"}`,
		`{"type":"file-history-snapshot","messageId":"m1","snapshot":{"trackedFileBackups":{}}}`,
		`{"type":"user","timestamp":"2026-02-01T00:00:01Z","cwd":"/tmp/proj","message":{"role":"user","content":"hi"}}`,
	}
	writeClaudeSession(t, root, "-tmp-proj", "sess-2", lines)

	recs, err := NewClaudeReader().List()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("want 1 record, got %d", len(recs))
	}
	if recs[0].Turns != 1 {
		t.Errorf("Turns = %d, want 1 (only the user record)", recs[0].Turns)
	}
	if recs[0].UnreadableLines != 0 {
		t.Errorf("UnreadableLines = %d, want 0: unknown types are skipped, not unreadable", recs[0].UnreadableLines)
	}
}

// TestClaudeTimestampFallsBackToMtime proves robustness rule 5: when no
// record in the file states a parseable timestamp, Started/Updated *and
// every Turn.At* fall back to the file's own mtime instead of staying zero
// — a zero Turn.At rendered as 0001-01-01T00:00:00Z in the handoff
// export's transcript tail.
func TestClaudeTimestampFallsBackToMtime(t *testing.T) {
	root := t.TempDir()
	t.Setenv(claudeConfigDirEnv, root)
	lines := []string{
		`{"type":"user","cwd":"/tmp/proj","message":{"role":"user","content":"hi"}}`,
	}
	path := writeClaudeSession(t, root, "-tmp-proj", "sess-3", lines)

	mtime := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}

	recs, err := NewClaudeReader().List()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("want 1 record, got %d", len(recs))
	}
	if !recs[0].Started.Equal(mtime) {
		t.Errorf("Started = %v, want mtime %v", recs[0].Started, mtime)
	}

	tr, err := NewClaudeReader().Load("sess-3")
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Turns) != 1 {
		t.Fatalf("want 1 turn, got %d", len(tr.Turns))
	}
	if !tr.Turns[0].At.Equal(mtime) {
		t.Errorf("Turns[0].At = %v, want mtime %v (never the zero time)", tr.Turns[0].At, mtime)
	}
}

// TestClaudeSlugReversalFallback proves the best-effort slug reversal used
// only when no record states its own "cwd". Per anthropics/claude-code
// #7009/#35162/#40946 this is lossy whenever a real path segment contains a
// literal '-': it is documented as approximate, not exact. A reversal that
// cannot be a real absolute path is refused outright rather than shown as
// a fabricated project.
func TestClaudeSlugReversalFallback(t *testing.T) {
	tests := []struct {
		name string
		slug string
		want string
	}{
		{
			name: "posix slug reverses",
			slug: "-Users-neil-xuku-invoice",
			want: "/Users/neil/xuku/invoice",
		},
		{
			// Claude Code slugifies "D:\dev\proj" to "D--dev-proj"; the
			// naive reversal "D//dev/proj" has no drive letter, an empty
			// segment and the wrong separators. Windows is friday's
			// primary platform, so this must not reach the Project column
			// or --project matching.
			name: "windows drive slug is refused",
			slug: "D--dev-proj",
			want: "",
		},
		{
			name: "adjacent dashes are refused",
			slug: "-home-dev--proj",
			want: "",
		},
		{
			name: "unrooted slug is refused",
			slug: "relative-path",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv(claudeConfigDirEnv, root)
			lines := []string{
				`{"type":"user","timestamp":"2026-04-01T00:00:00Z","message":{"role":"user","content":"hi"}}`,
			}
			writeClaudeSession(t, root, tt.slug, "sess-4", lines)

			recs, err := NewClaudeReader().List()
			if err != nil {
				t.Fatal(err)
			}
			if len(recs) != 1 {
				t.Fatalf("want 1 record, got %d", len(recs))
			}
			if recs[0].Project != tt.want {
				t.Errorf("Project = %q, want %q", recs[0].Project, tt.want)
			}
		})
	}

	// A record's own "cwd" is exact and always wins, whatever the slug.
	if got := reverseClaudeSlug(`D:\dev\proj`, "D--dev-proj"); got != `D:\dev\proj` {
		t.Errorf("reverseClaudeSlug with a cwd = %q, want the cwd verbatim", got)
	}
}

// TestClaudeLoadRejectsTraversalID guards Load's only path-building step.
// id is interpolated into <root>/projects/<slug>/<id>.jsonl, so an id
// carrying a separator or ".." would let an exported method read any
// readable *.jsonl file on the machine.
func TestClaudeLoadRejectsTraversalID(t *testing.T) {
	root := t.TempDir()
	t.Setenv(claudeConfigDirEnv, root)
	writeClaudeSession(t, root, "-home-dev-my-project", "sess-1", wellFormedSession())

	// A real session file outside the projects tree, to prove it stays
	// unreachable rather than merely erroring for some unrelated reason.
	outside := filepath.Join(root, "private.jsonl")
	if err := os.WriteFile(outside, []byte(wellFormedSession()[1]+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{
		"",
		"../private",
		`..\private`,
		"../../private",
		"sub/sess-1",
		`sub\sess-1`,
	} {
		if _, err := NewClaudeReader().Load(id); err == nil {
			t.Errorf("Load(%q) succeeded, want a rejection", id)
		}
	}

	// The guard must not reject a legitimate id.
	if _, err := NewClaudeReader().Load("sess-1"); err != nil {
		t.Errorf("Load(\"sess-1\") = %v, want success", err)
	}
}

// TestClaudeUnterminatedFinalLine covers the shape every live session has:
// a transcript still being appended to, whose last line has no newline.
// parseClaudeSession relies on ReadBytes returning (partial, io.EOF) and
// processing the line before testing the error, so reordering those two
// blocks would drop the final turn of every in-progress session.
func TestClaudeUnterminatedFinalLine(t *testing.T) {
	root := t.TempDir()
	t.Setenv(claudeConfigDirEnv, root)
	dir := filepath.Join(root, "projects", "-home-dev-my-project")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := wellFormedSession()
	content := ""
	for i, l := range lines {
		content += l
		if i < len(lines)-1 {
			content += "\n"
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "sess-5.jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tr, err := NewClaudeReader().Load("sess-5")
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Turns) != 4 {
		t.Fatalf("len(Turns) = %d, want 4 — same as the newline-terminated fixture", len(tr.Turns))
	}
	if tr.Turns[3].Text != "Looks fine to me." {
		t.Errorf("last turn text = %q, want the unterminated final line's content", tr.Turns[3].Text)
	}
	if tr.UnreadableLines != 0 {
		t.Errorf("UnreadableLines = %d, want 0: a missing final newline is not corruption", tr.UnreadableLines)
	}
}

// TestClaudeListSkipsTurnMaterialisation pins Record's documented promise
// that a Record is "cheap to produce for every session in List(), without
// parsing every turn" — List must not build the Turn slice, while still
// reporting the same turn count Load does.
func TestClaudeListSkipsTurnMaterialisation(t *testing.T) {
	root := t.TempDir()
	t.Setenv(claudeConfigDirEnv, root)
	path := writeClaudeSession(t, root, "-home-dev-my-project", "sess-6", wellFormedSession())

	rec, turns, err := parseClaudeSession(path, "-home-dev-my-project", false)
	if err != nil {
		t.Fatal(err)
	}
	if turns != nil {
		t.Errorf("parseClaudeSession(wantTurns=false) returned %d turns, want none", len(turns))
	}

	withTurns, full, err := parseClaudeSession(path, "-home-dev-my-project", true)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Turns != len(full) {
		t.Errorf("Turns = %d without materialising, len(turns) = %d with; want equal", rec.Turns, len(full))
	}
	if withTurns.Turns != rec.Turns {
		t.Errorf("Turns differs by wantTurns: %d vs %d", withTurns.Turns, rec.Turns)
	}
}
