package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ohMyPiFixture writes a synthetic session at
// <sessionsDir>/--<slug>--/<ts>_<uuid>.jsonl and returns its path. lines are
// joined with "\n" verbatim, so a caller can inject a deliberately malformed
// line or omit the title slot to test the pre-title-slot fallback path.
func ohMyPiFixture(t *testing.T, sessionsDir, slug, uuid string, lines []string) string {
	t.Helper()
	projDir := filepath.Join(sessionsDir, "--"+slug+"--")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(projDir, "2024-12-03T14-00-00-000Z_"+uuid+".jsonl")
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// ohMyPiTitleSlot builds a physical title-slot line matching real files:
// {"type":"title",...} padded to a fixed width. The exact pad width does
// not matter to this reader (it only checks "type", never a byte offset),
// so a short filler is enough to exercise the skip path.
const ohMyPiTitleSlot = `{"type":"title","v":1,"title":"","updatedAt":"2024-12-03T14:00:00.000Z","pad":"    "}`

// wellFormedOhMyPiLines builds a realistic transcript matching a real
// install's own session files: the title slot, a header, a user message, an
// assistant message with a toolCall (real field names: "name"/"arguments",
// not "toolName"/"input") and real usage/cost, a toolResult, and a
// model_change (real shape: a single combined "model" field, not separate
// provider/modelId fields).
func wellFormedOhMyPiLines(id, cwd string) []string {
	return []string{
		ohMyPiTitleSlot,
		`{"type":"session","version":3,"id":"` + id + `","timestamp":"2024-12-03T14:00:00.000Z","cwd":"` + cwd + `"}`,
		`{"type":"message","id":"a1b2c3d4","parentId":null,"timestamp":"2024-12-03T14:00:01.000Z","message":{"role":"user","content":"Fix the bug in main.go","timestamp":1733234401000}}`,
		`{"type":"message","id":"b2c3d4e5","parentId":"a1b2c3d4","timestamp":"2024-12-03T14:00:05.000Z","message":{"role":"assistant","content":[{"type":"text","text":"I'll fix it."},{"type":"toolCall","id":"call_1","name":"edit","arguments":{"path":"/repo/main.go","old":"foo","new":"bar"}}],"api":"anthropic-messages","provider":"anthropic","model":"claude-sonnet-4-5","stopReason":"toolUse","usage":{"input":120,"output":45,"cacheRead":0,"cacheWrite":0,"totalTokens":165,"cost":{"input":0.0006,"output":0.0009,"cacheRead":0,"cacheWrite":0,"total":0.0015}}}}`,
		`{"type":"message","id":"c3d4e5f6","parentId":"b2c3d4e5","timestamp":"2024-12-03T14:00:06.000Z","message":{"role":"toolResult","toolCallId":"call_1","toolName":"edit","content":[{"type":"text","text":"applied"}],"isError":false,"timestamp":1733234406000}}`,
		`{"type":"model_change","id":"d4e5f6g7","parentId":"c3d4e5f6","timestamp":"2024-12-03T14:00:11.000Z","model":"openai/gpt-4o","resolvedModelIsFallback":false}`,
	}
}

func TestOhMyPiAvailable(t *testing.T) {
	t.Run("root exists", func(t *testing.T) {
		agentDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(agentDir, "sessions"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv(ohMyPiAgentDirEnv, agentDir)
		r := NewOhMyPiReader()
		ok, reason := r.Available()
		if !ok {
			t.Fatalf("Available() = false, %q; want true", reason)
		}
		if reason != "" {
			t.Errorf("Available() reason = %q, want empty when available", reason)
		}
	})

	t.Run("root missing", func(t *testing.T) {
		agentDir := filepath.Join(t.TempDir(), "does-not-exist")
		t.Setenv(ohMyPiAgentDirEnv, agentDir)
		r := NewOhMyPiReader()
		ok, reason := r.Available()
		if ok {
			t.Fatal("Available() = true for a missing root, want false")
		}
		if reason == "" {
			t.Error("Available() reason is empty, want a human-readable explanation")
		}
	})
}

func TestOhMyPiListWellFormedSession(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv(ohMyPiAgentDirEnv, agentDir)
	sessionsDir := filepath.Join(agentDir, "sessions")
	const id = "11111111-1111-1111-1111-111111111111"
	const cwd = "/repo"
	ohMyPiFixture(t, sessionsDir, "repo", "aaaa", wellFormedOhMyPiLines(id, cwd))

	r := NewOhMyPiReader()
	records, err := r.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("List() returned %d records, want 1", len(records))
	}
	rec := records[0]

	if rec.Agent != "oh-my-pi" {
		t.Errorf("Agent = %q, want oh-my-pi", rec.Agent)
	}
	if rec.ID != id {
		t.Errorf("ID = %q, want %q", rec.ID, id)
	}
	if rec.Project != cwd {
		t.Errorf("Project = %q, want %q", rec.Project, cwd)
	}
	wantStarted := time.Date(2024, 12, 3, 14, 0, 0, 0, time.UTC)
	if !rec.Started.Equal(wantStarted) {
		t.Errorf("Started = %v, want %v", rec.Started, wantStarted)
	}
	// The assistant message carries "provider":"anthropic" next to
	// "model":"claude-sonnet-4-5"; MODEL is rendered "<provider>/<model>"
	// across every reader that has a provider on the wire, so a rollup does
	// not split one model into two buckets.
	if rec.Model != "anthropic/claude-sonnet-4-5" {
		t.Errorf("Model = %q, want anthropic/claude-sonnet-4-5", rec.Model)
	}
	// 3 type:"message" lines: user, assistant, toolResult. The title slot
	// and model_change are not type:"message" and must not count.
	if rec.Turns != 3 {
		t.Errorf("Turns = %d, want 3", rec.Turns)
	}
	if len(rec.Tools) != 1 || rec.Tools[0] != "edit" {
		t.Errorf("Tools = %v, want [edit]", rec.Tools)
	}
	if len(rec.Files) != 1 || rec.Files[0] != "/repo/main.go" {
		t.Errorf("Files = %v, want [/repo/main.go]", rec.Files)
	}
	if !rec.CostKnown {
		t.Error("CostKnown = false, want true: usage.cost was present")
	}
	if rec.CostUSD != 0.0015 {
		t.Errorf("CostUSD = %v, want 0.0015", rec.CostUSD)
	}
	if rec.InputTokens != 120 || rec.OutputTokens != 45 || rec.TotalTokens != 165 {
		t.Errorf("token counts = in:%d out:%d total:%d, want in:120 out:45 total:165",
			rec.InputTokens, rec.OutputTokens, rec.TotalTokens)
	}
	if rec.UnreadableLines != 0 {
		t.Errorf("UnreadableLines = %d, want 0 for a well-formed fixture", rec.UnreadableLines)
	}
}

// TestOhMyPiHeaderWithoutTitleSlot covers the reader's other tolerated
// shape: a file with no physical title-slot line at all, header first —
// the reader must not require the slot, only skip it when present.
func TestOhMyPiHeaderWithoutTitleSlot(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv(ohMyPiAgentDirEnv, agentDir)
	sessionsDir := filepath.Join(agentDir, "sessions")
	const id = "no-title-slot-id"
	lines := wellFormedOhMyPiLines(id, "/repo")[1:] // drop the title-slot line
	ohMyPiFixture(t, sessionsDir, "repo", "notitle", lines)

	r := NewOhMyPiReader()
	records, err := r.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 1 || records[0].ID != id {
		t.Fatalf("List() = %+v, want the single session without a title slot", records)
	}
}

func TestOhMyPiLoadReturnsOrderedTurns(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv(ohMyPiAgentDirEnv, agentDir)
	sessionsDir := filepath.Join(agentDir, "sessions")
	const id = "22222222-2222-2222-2222-222222222222"
	ohMyPiFixture(t, sessionsDir, "repo", "bbbb", wellFormedOhMyPiLines(id, "/repo"))

	r := NewOhMyPiReader()
	tr, err := r.Load(id)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(tr.Turns) != 3 {
		t.Fatalf("len(Turns) = %d, want 3", len(tr.Turns))
	}

	user, assistant, toolResult := tr.Turns[0], tr.Turns[1], tr.Turns[2]

	if user.Role != RoleUser || user.Text != "Fix the bug in main.go" {
		t.Errorf("turn[0] = %+v, want role=%s text=%q", user, RoleUser, "Fix the bug in main.go")
	}

	if assistant.Role != RoleAssistant || assistant.Text != "I'll fix it." {
		t.Errorf("turn[1] text/role = %q/%q, want %q/%s", assistant.Text, assistant.Role, "I'll fix it.", RoleAssistant)
	}
	if assistant.Tool != "edit" {
		t.Errorf("turn[1].Tool = %q, want edit", assistant.Tool)
	}
	if len(assistant.Files) != 1 || assistant.Files[0] != "/repo/main.go" {
		t.Errorf("turn[1].Files = %v, want [/repo/main.go]", assistant.Files)
	}

	// oh-my-pi's wire word is camelCase "toolResult"; the package's shared
	// role vocabulary spells a tool output RoleToolResult, so one role name
	// means one thing across every reader.
	if toolResult.Role != RoleToolResult || toolResult.Tool != "edit" || toolResult.Text != "applied" {
		t.Errorf("turn[2] = %+v, want role=%s tool=edit text=applied", toolResult, RoleToolResult)
	}
}

func TestOhMyPiLoadUnknownIDErrors(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv(ohMyPiAgentDirEnv, agentDir)
	sessionsDir := filepath.Join(agentDir, "sessions")
	ohMyPiFixture(t, sessionsDir, "repo", "cccc", wellFormedOhMyPiLines("known-id", "/repo"))

	r := NewOhMyPiReader()
	if _, err := r.Load("does-not-exist"); err == nil {
		t.Fatal("Load() with an unknown id returned nil error, want an error")
	}
}

func TestOhMyPiListToleratesCorruptLine(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv(ohMyPiAgentDirEnv, agentDir)
	sessionsDir := filepath.Join(agentDir, "sessions")
	const id = "33333333-3333-3333-3333-333333333333"
	lines := wellFormedOhMyPiLines(id, "/repo")
	// Splice one deliberately malformed JSON line into the middle of an
	// otherwise well-formed transcript — session.go's robustness contract
	// requires this to increment UnreadableLines, never fail List().
	lines = append(lines[:4], append([]string{`{"type":"message","id":"broken`}, lines[4:]...)...)
	ohMyPiFixture(t, sessionsDir, "repo", "dddd", lines)

	r := NewOhMyPiReader()
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
	// The 3 lines that still parse (user, assistant, toolResult) count; the
	// splice sits between them and the corrupt line is skipped, not
	// counted as a turn.
	if records[0].Turns != 3 {
		t.Errorf("Turns = %d, want 3 (only the spliced line is unreadable)", records[0].Turns)
	}
}

func TestOhMyPiListSkipsMissingRoot(t *testing.T) {
	agentDir := filepath.Join(t.TempDir(), "missing")
	t.Setenv(ohMyPiAgentDirEnv, agentDir)
	r := NewOhMyPiReader()
	records, err := r.List()
	if err != nil {
		t.Fatalf("List() on a missing root returned error = %v, want nil (empty list)", err)
	}
	if len(records) != 0 {
		t.Errorf("List() on a missing root returned %d records, want 0", len(records))
	}
}

func TestOhMyPiEnvOverrideRelocatesAgentDir(t *testing.T) {
	// PI_CODING_AGENT_DIR relocates oh-my-pi's whole native agent
	// directory, not the sessions directory directly — verify the
	// sessions root actually resolves to <override>/sessions, not
	// <override> itself.
	override := t.TempDir()
	t.Setenv(ohMyPiAgentDirEnv, override)
	sessionsDir := filepath.Join(override, "sessions")
	ohMyPiFixture(t, sessionsDir, "proj", "eeee", wellFormedOhMyPiLines("override-id", "/proj"))

	r := NewOhMyPiReader()
	records, err := r.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 1 || records[0].ID != "override-id" {
		t.Fatalf("List() = %+v, want the single session under <override>/sessions", records)
	}
}

// TestOhMyPiListKeepsPartialRecordOnOverCapLine is the whole-session-loss
// path: scanner.Err() used to abort the parse, List() dropped the session on
// the error, and a real 50 MB transcript on the author's machine already
// carries a 2.3 MB single line. One base64 image block past the cap must cost
// the lines after it, not the entire session.
func TestOhMyPiListKeepsPartialRecordOnOverCapLine(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv(ohMyPiAgentDirEnv, agentDir)
	sessionsDir := filepath.Join(agentDir, "sessions")

	const id = "44444444-4444-4444-4444-444444444444"
	lines := wellFormedOhMyPiLines(id, "/repo")
	overCap := `{"type":"message","id":"huge","timestamp":"2024-12-03T14:00:07.000Z","message":{"role":"user","content":"` +
		strings.Repeat("x", maxSessionLineBytes) + `"}}`
	lines = append(lines, overCap)
	ohMyPiFixture(t, sessionsDir, "repo", "ffff", lines)

	records, err := NewOhMyPiReader().List()
	if err != nil {
		t.Fatalf("List() error = %v, want nil: an over-cap line must not fail the store", err)
	}
	if len(records) != 1 {
		t.Fatalf("List() returned %d records, want 1 — the partial session must survive", len(records))
	}
	rec := records[0]
	if rec.ID != id {
		t.Errorf("ID = %q, want %q", rec.ID, id)
	}
	// The 3 well-formed message lines precede the over-cap one, so all of
	// them are still accounted for.
	if rec.Turns != 3 {
		t.Errorf("Turns = %d, want 3 (everything scanned before the over-cap line)", rec.Turns)
	}
	if rec.UnreadableLines < 1 {
		t.Errorf("UnreadableLines = %d, want >= 1 — the loss must be warned about, not silent", rec.UnreadableLines)
	}
}

// TestOhMyPiListSkipsUnusableHeader exercises readOhMyPiHeader's error
// returns, each of which silently removes a session from List(). A file with
// no session identity cannot produce a Record, but it must not fail the
// store either.
func TestOhMyPiListSkipsUnusableHeader(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
	}{
		{"blank first line", []string{}},
		{"garbage first line", []string{`not json at all {{{`}},
		{"first line is neither title nor session", []string{`{"type":"message","id":"x"}`}},
		{"title slot with no header after it", []string{ohMyPiTitleSlot}},
		{"title slot then garbage header", []string{ohMyPiTitleSlot, `{"type":"message","id":"x"}`}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			agentDir := t.TempDir()
			t.Setenv(ohMyPiAgentDirEnv, agentDir)
			sessionsDir := filepath.Join(agentDir, "sessions")
			ohMyPiFixture(t, sessionsDir, "repo", "gggg", tc.lines)

			records, err := NewOhMyPiReader().List()
			if err != nil {
				t.Fatalf("List() error = %v, want nil", err)
			}
			if len(records) != 0 {
				t.Errorf("List() = %+v, want 0 records for a file with no session identity", records)
			}
		})
	}
}

// TestOhMyPiAvailableNamesLegacyLayout covers the returning user whose
// transcripts still live in the pre-rename ~/.pi/agent. Without the hint,
// Available() names only ~/.omp/agent/sessions — a path such a user has
// never heard of — and `friday sessions` looks simply broken.
func TestOhMyPiAvailableNamesLegacyLayout(t *testing.T) {
	home := t.TempDir()
	// os.UserHomeDir reads USERPROFILE on Windows and HOME elsewhere; set
	// both so this test is platform-independent.
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(ohMyPiAgentDirEnv, "") // force the default ~/.omp/agent path

	legacy := filepath.Join(home, ".pi", "agent", "sessions")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}

	ok, reason := NewOhMyPiReader().Available()
	if ok {
		t.Fatal("Available() = true, want false: only the legacy layout exists")
	}
	if !strings.Contains(reason, legacy) {
		t.Errorf("reason = %q, want it to name the legacy directory %q it found", reason, legacy)
	}
	if !strings.Contains(reason, ohMyPiAgentDirEnv) {
		t.Errorf("reason = %q, want it to name %s as the way out", reason, ohMyPiAgentDirEnv)
	}
}
