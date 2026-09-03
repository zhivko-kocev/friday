package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeCopilotSession scaffolds <root>/session-state/<id>/{events.jsonl,workspace.yaml}
// so tests never depend on a real Copilot CLI install.
func writeCopilotSession(t *testing.T, root, id, events, workspaceYAML string) string {
	t.Helper()
	dir := filepath.Join(root, "session-state", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(events), 0o644); err != nil {
		t.Fatal(err)
	}
	if workspaceYAML != "" {
		if err := os.WriteFile(filepath.Join(dir, "workspace.yaml"), []byte(workspaceYAML), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// wellFormedEvents mirrors a real Copilot CLI events.jsonl, shapes verified
// against github/copilot-cli#3551's reference gist and jonmagic.com's
// writeup of ~/.copilot/session-state/<id>/events.jsonl.
const wellFormedEvents = `{"type":"session.start","data":{"sessionId":"sess-1","version":1,"context":{"cwd":"/repo"}},"id":"e1","timestamp":"2026-05-27T06:21:13.180Z","parentId":null}
{"type":"user.message","data":{"content":"Fix the bug"},"id":"e2","timestamp":"2026-05-27T06:21:20.000Z","parentId":null}
{"type":"tool.execution_start","data":{"toolCallId":"t1","toolName":"edit","arguments":{"path":"/repo/src/auth.ts"}},"id":"e3","timestamp":"2026-05-27T06:21:25.000Z","parentId":"e2"}
{"type":"tool.execution_complete","data":{"toolCallId":"t1","model":"claude-opus-4.6","success":true},"id":"e4","timestamp":"2026-05-27T06:21:26.000Z","parentId":"e3"}
{"type":"assistant.message","data":{"content":"Fixed the bug in auth.ts"},"id":"e5","timestamp":"2026-05-27T06:21:30.000Z","parentId":null}
{"type":"session.task_complete","data":{"summary":"Fixed auth bug","success":true},"id":"e6","timestamp":"2026-05-27T06:21:35.000Z","parentId":"e5"}
{"type":"session.shutdown","data":{},"id":"e7","timestamp":"2026-05-27T06:21:40.000Z","parentId":null}
`

const wellFormedWorkspace = "cwd: /repo/subdir\ngit_root: /repo\nbranch: main\n"

func TestCopilotAvailable(t *testing.T) {
	r := NewCopilotReader()

	t.Run("session state exists", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("COPILOT_HOME", dir)
		if err := os.MkdirAll(filepath.Join(dir, "session-state"), 0o755); err != nil {
			t.Fatal(err)
		}
		ok, reason := r.Available()
		if !ok {
			t.Fatalf("Available() = false, reason %q; want true", reason)
		}
	})

	// ~/.copilot is one of friday's own push targets, so it exists on
	// machines that never installed Copilot CLI. Probing it reported the
	// reader available and then listed nothing, which is indistinguishable
	// from "Copilot recorded no sessions".
	t.Run("config root exists but no session state", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("COPILOT_HOME", dir)
		if err := os.WriteFile(filepath.Join(dir, "copilot-instructions.md"), []byte("# pushed by friday\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		ok, reason := r.Available()
		if ok {
			t.Fatal("Available() = true for a friday-pushed config root with no session-state/; want false")
		}
		if !strings.Contains(reason, "session-state") {
			t.Errorf("reason = %q, want it to name the session-state directory friday actually looked at", reason)
		}
	})

	t.Run("root missing", func(t *testing.T) {
		t.Setenv("COPILOT_HOME", filepath.Join(t.TempDir(), "does-not-exist"))
		ok, reason := r.Available()
		if ok {
			t.Fatal("Available() = true; want false")
		}
		if reason == "" {
			t.Error("want a non-empty reason")
		}
	})
}

func TestCopilotList(t *testing.T) {
	root := t.TempDir()
	t.Setenv("COPILOT_HOME", root)
	writeCopilotSession(t, root, "sess-1", wellFormedEvents, wellFormedWorkspace)

	r := NewCopilotReader()
	records, err := r.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	rec := records[0]

	if rec.Agent != "copilot" {
		t.Errorf("Agent = %q, want copilot", rec.Agent)
	}
	if rec.ID != "sess-1" {
		t.Errorf("ID = %q, want sess-1", rec.ID)
	}
	// git_root wins over cwd for Project.
	if rec.Project != "/repo" {
		t.Errorf("Project = %q, want /repo", rec.Project)
	}
	if rec.Model != "claude-opus-4.6" {
		t.Errorf("Model = %q, want claude-opus-4.6", rec.Model)
	}
	// user.message + tool.execution_start + assistant.message + session.task_complete.
	if rec.Turns != 4 {
		t.Errorf("Turns = %d, want 4", rec.Turns)
	}
	if len(rec.Tools) != 1 || rec.Tools[0] != "edit" {
		t.Errorf("Tools = %v, want [edit]", rec.Tools)
	}
	if len(rec.Files) != 1 || rec.Files[0] != "/repo/src/auth.ts" {
		t.Errorf("Files = %v, want [/repo/src/auth.ts]", rec.Files)
	}
	if rec.UnreadableLines != 0 {
		t.Errorf("UnreadableLines = %d, want 0", rec.UnreadableLines)
	}

	wantStarted := time.Date(2026, 5, 27, 6, 21, 13, 180_000_000, time.UTC)
	if !rec.Started.Equal(wantStarted) {
		t.Errorf("Started = %v, want %v", rec.Started, wantStarted)
	}
	wantUpdated := time.Date(2026, 5, 27, 6, 21, 40, 0, time.UTC)
	if !rec.Updated.Equal(wantUpdated) {
		t.Errorf("Updated = %v, want %v", rec.Updated, wantUpdated)
	}

	if rec.CostKnown {
		t.Error("CostKnown = true; events.jsonl never states cost, want false")
	}
}

func TestCopilotListNoWorkspaceYAML(t *testing.T) {
	root := t.TempDir()
	t.Setenv("COPILOT_HOME", root)
	writeCopilotSession(t, root, "sess-1", wellFormedEvents, "")

	r := NewCopilotReader()
	records, err := r.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	if records[0].Project != "" {
		t.Errorf("Project = %q, want empty when workspace.yaml is absent", records[0].Project)
	}
}

func TestCopilotListSkipsDirWithoutEventsFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("COPILOT_HOME", root)
	// A session directory with only workspace.yaml (no events.jsonl) is not
	// a readable transcript and must not surface as a session.
	dir := filepath.Join(root, "session-state", "incomplete")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workspace.yaml"), []byte(wellFormedWorkspace), 0o644); err != nil {
		t.Fatal(err)
	}

	r := NewCopilotReader()
	records, err := r.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("got %d records, want 0", len(records))
	}
}

func TestCopilotListToleratesCorruptLine(t *testing.T) {
	root := t.TempDir()
	t.Setenv("COPILOT_HOME", root)
	events := `{"type":"user.message","data":{"content":"hello"},"id":"e1","timestamp":"2026-05-27T06:21:20.000Z","parentId":null}
not valid json at all {{{
{"type":"assistant.message","data":{"content":"hi there"},"id":"e2","timestamp":"2026-05-27T06:21:21.000Z","parentId":null}
`
	writeCopilotSession(t, root, "sess-2", events, "")

	r := NewCopilotReader()
	records, err := r.List()
	if err != nil {
		t.Fatalf("List() must never fail over a corrupt line: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	rec := records[0]
	if rec.UnreadableLines < 1 {
		t.Errorf("UnreadableLines = %d, want >= 1", rec.UnreadableLines)
	}
	if rec.Turns != 2 {
		t.Errorf("Turns = %d, want 2 (the two valid message lines)", rec.Turns)
	}
}

func TestCopilotLoad(t *testing.T) {
	root := t.TempDir()
	t.Setenv("COPILOT_HOME", root)
	writeCopilotSession(t, root, "sess-1", wellFormedEvents, wellFormedWorkspace)

	r := NewCopilotReader()
	transcript, err := r.Load("sess-1")
	if err != nil {
		t.Fatal(err)
	}

	if transcript.Record.ID != "sess-1" {
		t.Errorf("Record.ID = %q, want sess-1", transcript.Record.ID)
	}
	if len(transcript.Turns) != 4 {
		t.Fatalf("got %d turns, want 4: %+v", len(transcript.Turns), transcript.Turns)
	}

	want := []struct {
		role, text, tool string
		files            []string
	}{
		{"user", "Fix the bug", "", nil},
		{"tool", "", "edit", []string{"/repo/src/auth.ts"}},
		{"assistant", "Fixed the bug in auth.ts", "", nil},
		{"assistant", "Fixed auth bug", "", nil},
	}
	for i, w := range want {
		got := transcript.Turns[i]
		if got.Role != w.role {
			t.Errorf("turn[%d].Role = %q, want %q", i, got.Role, w.role)
		}
		if got.Text != w.text {
			t.Errorf("turn[%d].Text = %q, want %q", i, got.Text, w.text)
		}
		if got.Tool != w.tool {
			t.Errorf("turn[%d].Tool = %q, want %q", i, got.Tool, w.tool)
		}
		if len(got.Files) != len(w.files) {
			t.Errorf("turn[%d].Files = %v, want %v", i, got.Files, w.files)
		}
	}
}

func TestCopilotLoadMissingSession(t *testing.T) {
	root := t.TempDir()
	t.Setenv("COPILOT_HOME", root)
	if err := os.MkdirAll(filepath.Join(root, "session-state"), 0o755); err != nil {
		t.Fatal(err)
	}

	r := NewCopilotReader()
	if _, err := r.Load("does-not-exist"); err == nil {
		t.Fatal("Load() of a nonexistent session id should error")
	}
}

// TestCopilotListCapsOverlongLine covers the boundary the reader previously
// had no ceiling for at all: bufio.ReadString grew its accumulator until a
// newline, so a truncated or hostile events.jsonl was read into one string.
// An over-cap line must count as unreadable and the lines around it must
// still parse.
func TestCopilotListCapsOverlongLine(t *testing.T) {
	root := t.TempDir()
	t.Setenv("COPILOT_HOME", root)

	// A single JSON line whose "content" runs past maxSessionLineBytes.
	overlong := `{"type":"user.message","data":{"content":"` + strings.Repeat("x", maxSessionLineBytes) + `"},"id":"e2","timestamp":"2026-05-27T06:21:21.000Z"}`
	events := `{"type":"user.message","data":{"content":"before"},"id":"e1","timestamp":"2026-05-27T06:21:20.000Z"}
` + overlong + `
{"type":"assistant.message","data":{"content":"after"},"id":"e3","timestamp":"2026-05-27T06:21:22.000Z"}
`
	writeCopilotSession(t, root, "sess-big", events, "")

	records, err := NewCopilotReader().List()
	if err != nil {
		t.Fatalf("List() must never fail over an over-cap line: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	rec := records[0]
	if rec.UnreadableLines != 1 {
		t.Errorf("UnreadableLines = %d, want exactly 1 (the over-cap line)", rec.UnreadableLines)
	}
	if rec.Turns != 2 {
		t.Errorf("Turns = %d, want 2 — the reader must resync on the line after the over-cap one", rec.Turns)
	}
}
