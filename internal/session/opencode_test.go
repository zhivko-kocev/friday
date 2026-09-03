package session

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// opencodeFakeCall records one invocation for assertions on exact args —
// Load must never call `export` with no id (the interactive-picker hang
// this reader is built to avoid).
type opencodeFakeCall struct{ args []string }

func newOpencodeFake(t *testing.T, responses map[string][]byte, errs map[string]error) (*opencodeReader, *[]opencodeFakeCall) {
	t.Helper()
	calls := &[]opencodeFakeCall{}
	run := func(_ context.Context, args ...string) ([]byte, error) {
		key := strings.Join(args, " ")
		*calls = append(*calls, opencodeFakeCall{args: args})
		if err, ok := errs[key]; ok {
			return nil, err
		}
		if out, ok := responses[key]; ok {
			return out, nil
		}
		t.Fatalf("unexpected opencode invocation: %v", args)
		return nil, nil
	}
	return &opencodeReader{run: run}, calls
}

// realSessionListJSON mirrors formatSessionJSON's exact output verbatim,
// verified against packages/opencode/src/cli/cmd/session.ts (github.com/sst/opencode, dev):
//
//	function formatSessionJSON(sessions) {
//	  return sessions.map(s => ({ id: s.id, title: s.title, updated: s.time.updated,
//	    created: s.time.created, projectId: s.projectID, directory: s.directory }))
//	}
const realSessionListJSON = `[
  {
    "id": "ses_2132323b6ffeuRlYHhPcU8DaZ6",
    "title": "What does this fibonacci function do",
    "updated": 1777795392442,
    "created": 1777795391000,
    "projectId": "prj_abc123",
    "directory": "/root/oc-demo"
  }
]`

// realExportJSON mirrors `opencode export <id>`'s exact output shape,
// verified against packages/opencode/src/cli/cmd/export.ts and
// packages/sdk/js/src/gen/types.gen.ts (UserMessage/AssistantMessage/ToolPart)
// on github.com/sst/opencode, branch dev.
const realExportJSON = `{
  "info": {
    "id": "ses_2132323b6ffeuRlYHhPcU8DaZ6",
    "projectID": "prj_abc123",
    "directory": "/root/oc-demo",
    "title": "What does this fibonacci function do",
    "version": "1.14.33",
    "time": { "created": 1777795391000, "updated": 1777795392442 }
  },
  "messages": [
    {
      "info": {
        "id": "msg_1",
        "sessionID": "ses_2132323b6ffeuRlYHhPcU8DaZ6",
        "role": "user",
        "time": { "created": 1777795391000 },
        "agent": "build",
        "model": { "providerID": "anthropic", "modelID": "claude-haiku-4-5" }
      },
      "parts": [
        { "id": "prt_1", "type": "text", "text": "What does this fibonacci function do?" }
      ]
    },
    {
      "info": {
        "id": "msg_2",
        "sessionID": "ses_2132323b6ffeuRlYHhPcU8DaZ6",
        "role": "assistant",
        "time": { "created": 1777795391500, "completed": 1777795392442 },
        "parentID": "msg_1",
        "modelID": "claude-haiku-4-5",
        "providerID": "anthropic",
        "mode": "build",
        "path": { "cwd": "/root/oc-demo", "root": "/root/oc-demo" },
        "cost": 0.014087,
        "tokens": { "input": 2, "output": 34, "reasoning": 6, "cache": { "read": 100, "write": 11132 } }
      },
      "parts": [
        {
          "id": "prt_2", "sessionID": "ses_2132323b6ffeuRlYHhPcU8DaZ6", "messageID": "msg_2",
          "type": "tool", "callID": "call_1", "tool": "read",
          "state": { "status": "completed", "input": { "filePath": "/root/oc-demo/fib.py" }, "output": "def fib...", "title": "fib.py", "metadata": {} }
        },
        { "id": "prt_3", "type": "text", "text": "The Fibonacci function generates a sequence where each number is the sum of the two preceding ones." }
      ]
    }
  ]
}`

func TestOpencodeAgent(t *testing.T) {
	if got := NewOpencodeReader().Agent(); got != "opencode" {
		t.Errorf("Agent() = %q, want opencode", got)
	}
}

// TestOpencodeAvailable covers both branches deterministically. It never
// consults the real PATH: the previous version t.Skip'd whenever a real
// opencode was installed, which silently disabled the not-installed branch
// on exactly the machines where it matters.
func TestOpencodeAvailable(t *testing.T) {
	t.Run("binary on PATH", func(t *testing.T) {
		orig := opencodeLookPath
		opencodeLookPath = func(string) (string, error) { return "/usr/bin/opencode", nil }
		defer func() { opencodeLookPath = orig }()

		// No responses registered: Available must not run any command.
		// newOpencodeFake t.Fatal's on an unexpected invocation.
		r, calls := newOpencodeFake(t, nil, nil)
		ok, reason := r.Available()
		if !ok {
			t.Fatalf("Available() = false (%q), want true", reason)
		}
		if reason != "" {
			t.Errorf("Available() = true but reason %q is non-empty", reason)
		}
		if len(*calls) != 0 {
			t.Errorf("Available() spawned %v; it must only check PATH — List() runs the same command immediately after", *calls)
		}
	})

	t.Run("binary absent", func(t *testing.T) {
		orig := opencodeLookPath
		opencodeLookPath = func(string) (string, error) { return "", errors.New("executable file not found in %PATH%") }
		defer func() { opencodeLookPath = orig }()

		ok, reason := NewOpencodeReader().Available()
		if ok {
			t.Fatal("Available() = true, want false when opencode is not on PATH")
		}
		if !strings.Contains(reason, "PATH") {
			t.Errorf("Available() reason = %q, want it to name PATH", reason)
		}
	})
}

// TestOpencodeListEmptyStdoutIsZeroSessions is the live-reproduced blocker:
// `opencode session list --format json` exits 0 with zero bytes of stdout
// (not `[]`) when the current project has no sessions, and unmarshalling
// that turned "no sessions here" into `opencode: unavailable (unexpected
// output shape: unexpected end of JSON input)`.
func TestOpencodeListEmptyStdoutIsZeroSessions(t *testing.T) {
	for _, stdout := range []string{"", "\n", "  \r\n\t"} {
		r, _ := newOpencodeFake(t, map[string][]byte{
			"session list --format json": []byte(stdout),
		}, nil)

		records, err := r.List()
		if err != nil {
			t.Fatalf("List() with stdout %q = %v, want no error: empty output means zero sessions", stdout, err)
		}
		if len(records) != 0 {
			t.Errorf("List() with stdout %q returned %d records, want 0", stdout, len(records))
		}
	}
}

func TestOpencodeListParsesRealShape(t *testing.T) {
	r, calls := newOpencodeFake(t, map[string][]byte{
		"session list --format json": []byte(realSessionListJSON),
	}, nil)

	records, err := r.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	got := records[0]
	if got.Agent != "opencode" {
		t.Errorf("Agent = %q, want opencode", got.Agent)
	}
	if got.ID != "ses_2132323b6ffeuRlYHhPcU8DaZ6" {
		t.Errorf("ID = %q", got.ID)
	}
	if got.Project != "/root/oc-demo" {
		t.Errorf("Project = %q, want /root/oc-demo", got.Project)
	}
	if got.Path != "" {
		t.Errorf("Path = %q, want empty — opencode has no local session file", got.Path)
	}
	wantStarted := time.UnixMilli(1777795391000)
	if !got.Started.Equal(wantStarted) {
		t.Errorf("Started = %v, want %v", got.Started, wantStarted)
	}
	wantUpdated := time.UnixMilli(1777795392442)
	if !got.Updated.Equal(wantUpdated) {
		t.Errorf("Updated = %v, want %v", got.Updated, wantUpdated)
	}
	// session list --format json carries no model or message-count field
	// (verified against session.ts's formatSessionJSON) — List() cannot
	// populate these without a per-session export call, so they stay zero.
	if got.Model != "" {
		t.Errorf("Model = %q, want empty (not present in session list output)", got.Model)
	}
	if got.Turns != 0 {
		t.Errorf("Turns = %d, want 0 (not present in session list output)", got.Turns)
	}

	if len(*calls) != 1 || strings.Join((*calls)[0].args, " ") != "session list --format json" {
		t.Errorf("unexpected calls: %v", *calls)
	}
}

func TestOpencodeListSkipsMalformedEntry(t *testing.T) {
	r, _ := newOpencodeFake(t, map[string][]byte{
		"session list --format json": []byte(`[42, {"id":"ses_good","title":"ok","updated":1,"created":1,"projectId":"p","directory":"/d"}]`),
	}, nil)

	records, err := r.List()
	if err != nil {
		t.Fatalf("List() must never fail over one malformed entry: %v", err)
	}
	if len(records) != 1 || records[0].ID != "ses_good" {
		t.Fatalf("got %+v, want exactly the well-formed session", records)
	}
}

func TestOpencodeListErrorsOnUnexpectedTopLevelShape(t *testing.T) {
	r, _ := newOpencodeFake(t, map[string][]byte{
		"session list --format json": []byte(`{"not":"an array"}`),
	}, nil)
	if _, err := r.List(); err == nil {
		t.Fatal("List() must error when the top-level shape is not an array — this is a real integration break, not one bad session")
	}
}

func TestOpencodeLoadParsesRealShape(t *testing.T) {
	id := "ses_2132323b6ffeuRlYHhPcU8DaZ6"
	r, calls := newOpencodeFake(t, map[string][]byte{
		"export " + id: []byte(realExportJSON),
	}, nil)

	tr, err := r.Load(id)
	if err != nil {
		t.Fatal(err)
	}

	if tr.ID != id {
		t.Errorf("ID = %q", tr.ID)
	}
	if tr.Project != "/root/oc-demo" {
		t.Errorf("Project = %q", tr.Project)
	}
	if tr.Model != "anthropic/claude-haiku-4-5" {
		t.Errorf("Model = %q, want anthropic/claude-haiku-4-5", tr.Model)
	}
	if tr.Record.Turns != 2 {
		t.Errorf("Record.Turns = %d, want 2", tr.Record.Turns)
	}
	if len(tr.Turns) != 2 {
		t.Fatalf("len(Turns) = %d, want 2", len(tr.Turns))
	}
	if tr.Turns[0].Role != "user" || tr.Turns[0].Text != "What does this fibonacci function do?" {
		t.Errorf("turn 0 = %+v", tr.Turns[0])
	}
	if tr.Turns[1].Role != "assistant" {
		t.Errorf("turn 1 role = %q, want assistant", tr.Turns[1].Role)
	}
	if tr.Turns[1].Tool != "read" {
		t.Errorf("turn 1 tool = %q, want read", tr.Turns[1].Tool)
	}
	if len(tr.Turns[1].Files) != 1 || tr.Turns[1].Files[0] != "/root/oc-demo/fib.py" {
		t.Errorf("turn 1 files = %v, want [/root/oc-demo/fib.py]", tr.Turns[1].Files)
	}
	if !strings.Contains(tr.Turns[1].Text, "Fibonacci function generates") {
		t.Errorf("turn 1 text = %q", tr.Turns[1].Text)
	}

	if len(tr.Tools) != 1 || tr.Tools[0] != "read" {
		t.Errorf("Tools = %v, want [read]", tr.Tools)
	}
	if len(tr.Files) != 1 || tr.Files[0] != "/root/oc-demo/fib.py" {
		t.Errorf("Files = %v, want [/root/oc-demo/fib.py]", tr.Files)
	}

	// Token accounting: input=2, output=34+6(reasoning)=40, cache read=100, cache write=11132.
	if tr.InputTokens != 2 {
		t.Errorf("InputTokens = %d, want 2", tr.InputTokens)
	}
	if tr.OutputTokens != 40 {
		t.Errorf("OutputTokens = %d, want 40 (34 output + 6 reasoning)", tr.OutputTokens)
	}
	if tr.CacheReadTokens != 100 {
		t.Errorf("CacheReadTokens = %d, want 100", tr.CacheReadTokens)
	}
	if tr.CacheCreationTokens != 11132 {
		t.Errorf("CacheCreationTokens = %d, want 11132", tr.CacheCreationTokens)
	}
	if want := 2 + 40 + 100 + 11132; tr.TotalTokens != want {
		t.Errorf("TotalTokens = %d, want %d", tr.TotalTokens, want)
	}

	// cost is present (0.014087) in the fixture but must never be trusted:
	// opencode's own session cost reporting is known-unreliable, so
	// CostKnown must stay false regardless of the observed value.
	if tr.CostKnown {
		t.Error("CostKnown = true, want false — opencode's cost field is never trusted")
	}
	if tr.CostUSD != 0 {
		t.Errorf("CostUSD = %v, want 0 (untouched, since CostKnown is false)", tr.CostUSD)
	}

	if len(*calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(*calls))
	}
	if got := (*calls)[0].args; len(got) != 2 || got[0] != "export" || got[1] != id {
		t.Fatalf("Load must call `export <id>` with the exact id, never bare: got %v", got)
	}
}

// TestOpencodeLoadCountsUnreadableMessages pins the boundary between the two
// kinds of "message friday did not use". Only a payload that fails to decode
// is unreadable; a message that decoded perfectly but carries a role this
// build has never heard of is a future opencode release, not corruption, and
// must not make `friday session show` warn about an intact transcript.
func TestOpencodeLoadCountsUnreadableMessages(t *testing.T) {
	id := "ses_x"
	payload := `{
  "info": { "id": "ses_x", "directory": "/d", "title": "t", "version": "1", "time": {"created": 1, "updated": 2} },
  "messages": [
    { "info": { "id": "m1", "role": "user", "time": {"created": 1} }, "parts": [{"type":"text","text":"hi"}] },
    42,
    { "info": { "id": "m2", "role": "unknown-future-role", "time": {"created": 2} }, "parts": [] }
  ]
}`
	r, _ := newOpencodeFake(t, map[string][]byte{"export " + id: []byte(payload)}, nil)

	tr, err := r.Load(id)
	if err != nil {
		t.Fatalf("Load must never fail over malformed message entries: %v", err)
	}
	if tr.Record.Turns != 1 {
		t.Errorf("Record.Turns = %d, want 1 (only the valid user message)", tr.Record.Turns)
	}
	if tr.UnreadableLines != 1 {
		t.Errorf("UnreadableLines = %d, want exactly 1 — the `42` entry only; the unknown role parsed fine", tr.UnreadableLines)
	}
}

func TestOpencodeLoadErrorsOnUnexpectedTopLevelShape(t *testing.T) {
	id := "ses_x"
	r, _ := newOpencodeFake(t, map[string][]byte{"export " + id: []byte(`"just a string"`)}, nil)
	if _, err := r.Load(id); err == nil {
		t.Fatal("Load() must error when the export payload isn't the documented {info, messages} shape")
	}
}
