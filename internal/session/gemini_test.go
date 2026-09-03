package session

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestGeminiAvailable(t *testing.T) {
	t.Run("session tmp exists", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv(geminiHomeEnv, home)
		if err := os.MkdirAll(filepath.Join(home, ".gemini", "tmp"), 0o755); err != nil {
			t.Fatal(err)
		}
		ok, reason := NewGeminiReader().Available()
		if !ok {
			t.Fatalf("want available, got reason %q", reason)
		}
	})

	// ~/.gemini is the gemini preset's push target, so it exists on any
	// machine that ran `friday push` — with no Gemini CLI installed and no
	// tmp/ underneath. Probing the root reported available and then listed
	// nothing.
	t.Run("config root exists but no tmp", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv(geminiHomeEnv, home)
		root := filepath.Join(home, ".gemini")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "GEMINI.md"), []byte("# pushed by friday\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		ok, reason := NewGeminiReader().Available()
		if ok {
			t.Fatal("want unavailable for a friday-pushed ~/.gemini with no tmp/")
		}
		// The reason must name the resolved path, not a literal ~/.gemini:
		// GEMINI_CLI_HOME has relocated the root here.
		if !strings.Contains(reason, home) {
			t.Errorf("reason = %q, want it to name the resolved root under %q", reason, home)
		}
	})

	t.Run("root missing", func(t *testing.T) {
		home := t.TempDir() // no .gemini created under it
		t.Setenv(geminiHomeEnv, home)
		ok, reason := NewGeminiReader().Available()
		if ok {
			t.Fatal("want unavailable when ~/.gemini is absent")
		}
		if reason == "" {
			t.Fatal("want a human reason when unavailable")
		}
	})
}

// writeGeminiSession creates <home>/.gemini/tmp/<idDirName>/chats/<filename>
// with the given raw JSONL body, returning the session file's path.
func writeGeminiSession(t *testing.T, home, idDirName, filename, body string) string {
	t.Helper()
	chatsDir := filepath.Join(home, ".gemini", "tmp", idDirName, "chats")
	if err := os.MkdirAll(chatsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(chatsDir, filename)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// wellFormedGeminiSession carries thoughts and tool token axes alongside
// input/output/cached, and a deliberately stale "total": TokensSummary's own
// total is not decoded, so 175 must not appear anywhere in the Record —
// friday sums its own four axes like every other reader.
const wellFormedGeminiSession = `{"sessionId":"a1b2c3d4-e5f6-7890-abcd-ef1234567890","projectHash":"deadbeef","startTime":"2026-01-15T10:30:00.000Z","lastUpdated":"2026-01-15T10:35:00.000Z"}
{"id":"m1","timestamp":"2026-01-15T10:30:05.000Z","type":"user","content":"Fix the bug in auth.go"}
{"id":"m2","timestamp":"2026-01-15T10:30:10.000Z","type":"gemini","content":[{"text":"I'll read the file first."}],"model":"gemini-2.5-pro","tokens":{"input":120,"output":45,"cached":10,"thoughts":30,"tool":15,"total":175},"toolCalls":[{"id":"t1","name":"read_file","args":{"file_path":"/repo/project/auth.go"},"status":"success","timestamp":"2026-01-15T10:30:11.000Z"}]}
{"$set":{"lastUpdated":"2026-01-15T10:40:00.000Z"}}
`

func TestGeminiListWellFormedSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv(geminiHomeEnv, home)

	// The .project_root ownership marker (current gemini-cli: registry-slug
	// directory names) takes precedence over hash matching.
	idDir := filepath.Join(home, ".gemini", "tmp", "deadbeef")
	if err := os.MkdirAll(idDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idDir, geminiProjectRootMarker), []byte("/repo/project\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeGeminiSession(t, home, "deadbeef", "session-2026-01-15T10-30-a1b2c3d4.jsonl", wellFormedGeminiSession)

	r := NewGeminiReader()
	records, err := r.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("want 1 record, got %d", len(records))
	}
	rec := records[0]

	if rec.Agent != "gemini" {
		t.Errorf("Agent = %q, want gemini", rec.Agent)
	}
	if rec.ID != "a1b2c3d4-e5f6-7890-abcd-ef1234567890" {
		t.Errorf("ID = %q", rec.ID)
	}
	// The marker path does not exist on this machine, so normalization
	// falls back to filepath.Clean — which is separator-native.
	if want := filepath.Clean("/repo/project"); rec.Project != want {
		t.Errorf("Project = %q, want %q (from marker)", rec.Project, want)
	}
	if rec.Model != "gemini-2.5-pro" {
		t.Errorf("Model = %q", rec.Model)
	}
	if rec.Turns != 2 {
		t.Errorf("Turns = %d, want 2 (messageCount is never on disk; must be counted)", rec.Turns)
	}
	if want := []string{"read_file"}; !equalStrSlices(rec.Tools, want) {
		t.Errorf("Tools = %v, want %v", rec.Tools, want)
	}
	if want := []string{"/repo/project/auth.go"}; !equalStrSlices(rec.Files, want) {
		t.Errorf("Files = %v, want %v", rec.Files, want)
	}
	// thoughts (30) are billed model output and fold into OutputTokens the
	// way opencode's `reasoning` does; tool (15) are tool-response tokens fed
	// back to the model and fold into InputTokens. TotalTokens is friday's
	// own sum (135+75+10), never TokensSummary's stale 175.
	if rec.InputTokens != 135 || rec.OutputTokens != 75 || rec.CacheReadTokens != 10 || rec.TotalTokens != 220 {
		t.Errorf("tokens = in:%d out:%d cacheRead:%d total:%d, want in:135 out:75 cacheRead:10 total:220",
			rec.InputTokens, rec.OutputTokens, rec.CacheReadTokens, rec.TotalTokens)
	}
	if rec.CacheCreationTokens != 0 {
		t.Errorf("CacheCreationTokens = %d, gemini's TokensSummary has no such axis", rec.CacheCreationTokens)
	}
	if rec.CostKnown {
		t.Error("CostKnown must stay false: gemini-cli's session format states no cost field")
	}
	wantStarted := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	if !rec.Started.Equal(wantStarted) {
		t.Errorf("Started = %v, want %v", rec.Started, wantStarted)
	}
	// The trailing $set record's lastUpdated (10:40) postdates the initial
	// metadata's lastUpdated (10:35) and must win.
	wantUpdated := time.Date(2026, 1, 15, 10, 40, 0, 0, time.UTC)
	if !rec.Updated.Equal(wantUpdated) {
		t.Errorf("Updated = %v, want %v", rec.Updated, wantUpdated)
	}
}

func TestGeminiLoadTurns(t *testing.T) {
	home := t.TempDir()
	t.Setenv(geminiHomeEnv, home)
	writeGeminiSession(t, home, "deadbeef", "session-2026-01-15T10-30-a1b2c3d4.jsonl", wellFormedGeminiSession)

	r := NewGeminiReader()
	ts, err := r.Load("a1b2c3d4-e5f6-7890-abcd-ef1234567890")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(ts.Turns) != 2 {
		t.Fatalf("want 2 turns, got %d", len(ts.Turns))
	}

	user := ts.Turns[0]
	if user.Role != RoleUser || user.Text != "Fix the bug in auth.go" {
		t.Errorf("turn 0 = %+v", user)
	}

	// gemini-cli's wire type for an assistant message is "gemini"; the
	// Record vocabulary is shared across readers, so it maps to RoleAssistant
	// rather than leaking the agent's own spelling into the turn list.
	assistant := ts.Turns[1]
	if assistant.Role != RoleAssistant || assistant.Text != "I'll read the file first." {
		t.Errorf("turn 1 text/role = %+v", assistant)
	}
	if assistant.Tool != "read_file" {
		t.Errorf("turn 1 Tool = %q", assistant.Tool)
	}
	if want := []string{"/repo/project/auth.go"}; !equalStrSlices(assistant.Files, want) {
		t.Errorf("turn 1 Files = %v, want %v", assistant.Files, want)
	}
}

func TestGeminiLoadUnknownIDErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv(geminiHomeEnv, home)
	writeGeminiSession(t, home, "deadbeef", "session-2026-01-15T10-30-a1b2c3d4.jsonl", wellFormedGeminiSession)

	if _, err := NewGeminiReader().Load("does-not-exist"); err == nil {
		t.Fatal("want error for unknown session id")
	}
}

// TestGeminiForwardHashMatch exercises the one-way sha256(projectRoot)
// fallback used when a session directory carries no .project_root marker
// (i.e. an older/unmigrated gemini-cli install using the hash directly, per
// docs/cli/session-management.md). The hash is over the RAW cwd string, with
// no realpath and no case folding, because that is literally what
// gemini-cli's getProjectHash does — hashing a resolved path could never
// match.
func TestGeminiForwardHashMatch(t *testing.T) {
	home := t.TempDir()
	t.Setenv(geminiHomeEnv, home)

	project := t.TempDir()
	t.Chdir(project) // t.Chdir restores the previous cwd once this test ends

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(cwd))
	hash := hex.EncodeToString(sum[:])

	// Deliberately no .project_root marker in this hash-named directory, to
	// force the forward-hash-match path rather than the marker path.
	writeGeminiSession(t, home, hash, "session-2026-01-15T10-30-b2c3d4e5.jsonl",
		`{"sessionId":"b2c3d4e5-0000-0000-0000-000000000000","projectHash":"`+hash+`","startTime":"2026-01-15T10:30:00.000Z","lastUpdated":"2026-01-15T10:30:00.000Z"}
`)

	records, err := NewGeminiReader().List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("want 1 record, got %d", len(records))
	}
	want, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		want = filepath.Clean(cwd)
	}
	if records[0].Project != want {
		t.Errorf("Project = %q, want forward-hash-matched cwd %q", records[0].Project, want)
	}
}

// TestGeminiProjectPathRecoversOnDiskCasing is the Windows defect: gemini-cli's
// projectRegistry.ts lowercases the whole path on win32 before writing
// .project_root, so the marker said `d:\friday` while oh-my-pi's header said
// `D:\friday` — one Project column with two spellings for one directory, and
// `friday sessions --project D:\friday` (case-sensitive Contains) silently
// omitting the gemini rows. Only reachable where the filesystem is
// case-insensitive, which is where the defect lives.
func TestGeminiProjectPathRecoversOnDiskCasing(t *testing.T) {
	home := t.TempDir()
	t.Setenv(geminiHomeEnv, home)

	project := t.TempDir()
	lowered := strings.ToLower(project)
	if lowered == project {
		t.Skip("temp dir has no upper-case component; nothing to recover")
	}
	if _, err := os.Stat(lowered); err != nil {
		t.Skip("case-sensitive filesystem: gemini-cli's lowercased marker is unrecoverable here")
	}

	idDir := filepath.Join(home, ".gemini", "tmp", "slug-1")
	if err := os.MkdirAll(idDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idDir, geminiProjectRootMarker), []byte(lowered+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeGeminiSession(t, home, "slug-1", "session-2026-01-15T10-30-e5f6a7b8.jsonl",
		`{"sessionId":"e5f6a7b8-0000-0000-0000-000000000000","projectHash":"slug-1","startTime":"2026-01-15T10:30:00.000Z","lastUpdated":"2026-01-15T10:30:00.000Z"}
`)

	records, err := NewGeminiReader().List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("want 1 record, got %d", len(records))
	}
	want, err := filepath.EvalSymlinks(project)
	if err != nil {
		want = filepath.Clean(project)
	}
	if records[0].Project != want {
		t.Errorf("Project = %q, want the on-disk casing %q, not gemini-cli's lowercased marker", records[0].Project, want)
	}
}

// TestGeminiProjectFromSlugRegistry covers the slug-named directory whose
// .project_root marker is absent. Forward hash matching cannot help there —
// the directory is named by a registry slug, not by a hash — so without
// reading gemini-cli's own projects.json the Project column would be empty
// for every session in the store.
func TestGeminiProjectFromSlugRegistry(t *testing.T) {
	home := t.TempDir()
	t.Setenv(geminiHomeEnv, home)

	project := t.TempDir()
	registry := `{"projects": {` + strconv.Quote(project) + `: "slug-7"}}`
	if err := os.MkdirAll(filepath.Join(home, ".gemini"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".gemini", "projects.json"), []byte(registry), 0o644); err != nil {
		t.Fatal(err)
	}

	// Deliberately no .project_root marker under tmp/slug-7.
	writeGeminiSession(t, home, "slug-7", "session-2026-01-15T10-30-f6a7b8c9.jsonl",
		`{"sessionId":"f6a7b8c9-0000-0000-0000-000000000000","projectHash":"slug-7","startTime":"2026-01-15T10:30:00.000Z","lastUpdated":"2026-01-15T10:30:00.000Z"}
`)

	records, err := NewGeminiReader().List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("want 1 record, got %d", len(records))
	}
	want, err := filepath.EvalSymlinks(project)
	if err != nil {
		want = filepath.Clean(project)
	}
	if records[0].Project != want {
		t.Errorf("Project = %q, want %q resolved through projects.json", records[0].Project, want)
	}
}

// TestGeminiUnknownHashNeverGuessesProject covers the common case: a session
// directory whose hash matches neither the current cwd nor any marker file
// must leave Project empty rather than guessing.
func TestGeminiUnknownHashNeverGuessesProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv(geminiHomeEnv, home)
	writeGeminiSession(t, home, "some-other-projects-hash", "session-2026-01-15T10-30-c3d4e5f6.jsonl",
		`{"sessionId":"c3d4e5f6-0000-0000-0000-000000000000","projectHash":"some-other-projects-hash","startTime":"2026-01-15T10:30:00.000Z","lastUpdated":"2026-01-15T10:30:00.000Z"}
`)

	records, err := NewGeminiReader().List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("want 1 record, got %d", len(records))
	}
	if records[0].Project != "" {
		t.Errorf("Project = %q, want empty (no verifiable candidate)", records[0].Project)
	}
}

// TestGeminiUnreadableLine is the robustness case: one corrupt line must
// never fail List(), and must be tallied rather than silently swallowed.
func TestGeminiUnreadableLine(t *testing.T) {
	home := t.TempDir()
	t.Setenv(geminiHomeEnv, home)

	body := `{"sessionId":"d4e5f6a7-0000-0000-0000-000000000000","projectHash":"cafef00d","startTime":"2026-01-15T10:30:00.000Z","lastUpdated":"2026-01-15T10:30:00.000Z"}
this is not json at all {{{
{"id":"m1","timestamp":"2026-01-15T10:30:05.000Z","type":"user","content":"still readable after the corrupt line"}
`
	writeGeminiSession(t, home, "cafef00d", "session-2026-01-15T10-30-d4e5f6a7.jsonl", body)

	records, err := NewGeminiReader().List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("want session still listed despite the corrupt line, got %d records", len(records))
	}
	rec := records[0]
	if rec.UnreadableLines < 1 {
		t.Errorf("UnreadableLines = %d, want >= 1", rec.UnreadableLines)
	}
	if rec.Turns != 1 {
		t.Errorf("Turns = %d, want 1 (the one valid message survives)", rec.Turns)
	}
}

func equalStrSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
