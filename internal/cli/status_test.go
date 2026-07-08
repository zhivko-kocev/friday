package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/drift"
	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/output"
	"github.com/zhivko-kocev/friday/internal/rules"
)

// setDriftEnv points the drift store at throwaway dirs for one test.
func setDriftEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if runtime.GOOS == "windows" {
		t.Setenv("LocalAppData", t.TempDir())
	}
}

// TestStatusColumn1DetectsHandEdit is the end-to-end guard for the flagship
// axis: push writes a target and records its baseline, then a direct edit to
// that target must light up column 1 — proving handEditedLookup returns true
// AND that its (adapter, DestPath) keys match what the engine recorded at push
// time. An untouched sibling target must stay clean.
func TestStatusColumn1DetectsHandEdit(t *testing.T) {
	setDriftEnv(t)
	store, target := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(store, "a.md"), []byte("aaa"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "b.md"), []byte("bbb"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Version: 1, StoreDir: store, TargetRoot: target,
		Adapters: map[string]*config.Adapter{
			"test": {Target: target, Rules: []*rules.Rule{
				{From: rules.FromSpec{"a.md"}, To: "a.md", Strategy: rules.StrategyCopy},
				{From: rules.FromSpec{"b.md"}, To: "b.md", Strategy: rules.StrategyCopy},
			}},
		},
	}
	if _, err := engine.Push(cfg, engine.Options{}); err != nil {
		t.Fatalf("push: %v", err)
	}

	// Hand-edit the a.md target directly; leave b.md as friday wrote it.
	aTarget := filepath.Join(target, "a.md")
	if err := os.WriteFile(aTarget, []byte("aaa — edited by hand"), 0o644); err != nil {
		t.Fatal(err)
	}

	lookup := handEditedLookup()
	if !lookup("test", aTarget) {
		t.Errorf("hand-edited target not detected as drifted (column 1 dead)")
	}
	if lookup("test", filepath.Join(target, "b.md")) {
		t.Errorf("untouched target wrongly flagged as a hand edit")
	}

	// And it surfaces in the grid: a.md column 1 = M.
	changes, err := engine.Push(cfg, engine.Options{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	rows := buildStatusRows(changes, lookup)
	var seen bool
	for _, r := range rows {
		if r.dest == "a.md" {
			seen = true
			if r.col1() != "M" {
				t.Errorf("grid column 1 for hand-edited a.md = %q, want M", r.col1())
			}
		}
		if r.dest == "b.md" && r.col1() != " " {
			t.Errorf("grid column 1 for untouched b.md = %q, want blank", r.col1())
		}
	}
	if !seen {
		t.Fatalf("a.md row missing from grid: %+v", rows)
	}
}

// TestStatusCheckJSONGate pins the reviewer fix: --check must gate the exit
// code even under --json, while a plain --json keeps the conflict-only exit.
func TestStatusCheckJSONGate(t *testing.T) {
	output.SetColor(false)
	t.Cleanup(func() { output.SetColor(false) })
	setDriftEnv(t)
	home, _ := os.UserHomeDir()
	store := filepath.Join(home, ".friday")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "core.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	// One adapter whose target dir does not exist → a pending create (not a
	// conflict), so plain exit is 0 but --check is 2.
	tgt := filepath.ToSlash(filepath.Join(home, "tgt"))
	manifest := "version: 1\nadapters:\n  test:\n    target: " + tgt +
		"\n    rules:\n      - from: [core.md]\n        to: OUT.md\n        strategy: concatenate\n"
	if err := os.WriteFile(filepath.Join(store, "friday.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	var code int
	_ = captureStdout(t, func() { code = cmdStatus([]string{"--json"}) })
	if code != 0 {
		t.Errorf("plain --json with a pending create exited %d, want 0 (no conflict)", code)
	}
	_ = captureStdout(t, func() { code = cmdStatus([]string{"--json", "--check"}) })
	if code != 2 {
		t.Errorf("--json --check with a pending create exited %d, want 2", code)
	}
}

// captureStdout swaps os.Stdout for a pipe while fn runs and returns what was
// written, mirroring the output package's own test helper.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	fn()
	_ = w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("copy: %v", err)
	}
	return buf.String()
}

// TestStatusRowGlyphs pins the two-column encoding across the reconcile
// matrix, including the independent-axis case: a target hand-edited to match
// canonical renders in-sync on column 2 yet drifted on column 1.
func TestStatusRowGlyphs(t *testing.T) {
	cases := []struct {
		name       string
		row        statusRow
		col1, col2 string
		clean      bool
		wantLevel  output.Level
	}{
		{"clean", statusRow{handEdit: false, render: engine.ActionInSync}, " ", " ", true, output.LevelInfo},
		{"pending-create", statusRow{render: engine.ActionCreate}, " ", "A", false, output.LevelInfo},
		{"pending-update", statusRow{render: engine.ActionUpdate}, " ", "M", false, output.LevelInfo},
		{"drift-only", statusRow{handEdit: true, render: engine.ActionInSync}, "M", " ", false, output.LevelWarn},
		{"conflict", statusRow{handEdit: true, render: engine.ActionConflict}, "M", "!", false, output.LevelWarn},
		{"missing-source", statusRow{render: engine.ActionMissingSource}, " ", "-", false, output.LevelSkip},
		{"unsupported", statusRow{render: engine.ActionUnsupported}, " ", "-", false, output.LevelSkip},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.row.col1(); got != c.col1 {
				t.Errorf("col1 = %q, want %q", got, c.col1)
			}
			if got := c.row.col2(); got != c.col2 {
				t.Errorf("col2 = %q, want %q", got, c.col2)
			}
			if got := c.row.clean(); got != c.clean {
				t.Errorf("clean = %v, want %v", got, c.clean)
			}
			if got := c.row.level(); got != c.wantLevel {
				t.Errorf("level = %v, want %v", got, c.wantLevel)
			}
		})
	}
}

// TestBuildStatusRows checks the lookup wiring: column 1 comes from the drift
// callback keyed by (adapter, absolute dest), independent of the push action.
func TestBuildStatusRows(t *testing.T) {
	changes := []engine.Change{
		{Adapter: "claude", DestPath: "/abs/CLAUDE.md", DestRel: "CLAUDE.md", Action: engine.ActionInSync},
		{Adapter: "codex", DestPath: "/abs/AGENTS.md", DestRel: "AGENTS.md", Action: engine.ActionUpdate},
	}
	// Only the claude target reads as hand-edited.
	handEdited := func(adapter, dest string) bool { return dest == "/abs/CLAUDE.md" }
	rows := buildStatusRows(changes, handEdited)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if !rows[0].handEdit || rows[0].render != engine.ActionInSync {
		t.Errorf("row0 = %+v; want handEdit && in-sync (drift-only)", rows[0])
	}
	if rows[1].handEdit || rows[1].render != engine.ActionUpdate {
		t.Errorf("row1 = %+v; want !handEdit && update", rows[1])
	}
}

func TestStatusExit(t *testing.T) {
	conflict := []engine.Change{{Action: engine.ActionConflict}}
	pending := []engine.Change{{Action: engine.ActionUpdate}}
	rowsClean := []statusRow{{render: engine.ActionInSync}}
	rowsDrift := []statusRow{{handEdit: true, render: engine.ActionInSync}}
	rowsPending := []statusRow{{render: engine.ActionUpdate}}

	// Legacy (no --check): 2 only on a real conflict.
	if got := statusExit(conflict, rowsPending, false); got != 2 {
		t.Errorf("legacy conflict exit = %d, want 2", got)
	}
	if got := statusExit(pending, rowsPending, false); got != 0 {
		t.Errorf("legacy pending exit = %d, want 0 (drift is not a conflict)", got)
	}
	// --check: 2 whenever anything is non-clean, including uncaptured drift.
	if got := statusExit(pending, rowsDrift, true); got != 2 {
		t.Errorf("--check drift exit = %d, want 2", got)
	}
	if got := statusExit(nil, rowsClean, true); got != 0 {
		t.Errorf("--check clean exit = %d, want 0", got)
	}
}

// TestPrintStatusGridPlain pins the plain-mode grid: non-clean rows with their
// two-column code, an in-sync tally, no escape sequences.
func TestPrintStatusGridPlain(t *testing.T) {
	output.SetColor(false)
	t.Cleanup(func() { output.SetColor(false) })

	rows := []statusRow{
		{render: engine.ActionInSync}, // clean, hidden
		{handEdit: true, render: engine.ActionConflict, adapter: "claude", dest: "CLAUDE.md"},
		{render: engine.ActionCreate, adapter: "codex", dest: "AGENTS.md"},
	}
	installed := map[string]bool{"claude": true, "codex": true}
	got := captureStdout(t, func() { printStatusGrid(rows, installed) })

	for _, want := range []string{"M!  claude  CLAUDE.md", " A  codex   AGENTS.md", "1 file(s) in sync"} {
		if !strings.Contains(got, want) {
			t.Errorf("grid missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\x1b") {
		t.Errorf("plain grid leaked an escape sequence: %q", got)
	}
}

// TestPrintStatusGridCollapsesUninstalled — a not-installed adapter's pending
// creates collapse into one summary line instead of flooding the grid.
func TestPrintStatusGridCollapsesUninstalled(t *testing.T) {
	output.SetColor(false)
	t.Cleanup(func() { output.SetColor(false) })
	rows := []statusRow{
		{render: engine.ActionCreate, adapter: "codex", dest: "AGENTS.md"},
		{render: engine.ActionCreate, adapter: "codex", dest: "skills/a/SKILL.md"},
		{render: engine.ActionMissingSource, adapter: "claude", dest: ""}, // noise, dropped
	}
	got := captureStdout(t, func() { printStatusGrid(rows, map[string]bool{}) })
	if !strings.Contains(got, "codex (2 files — not installed") {
		t.Errorf("uninstalled adapter not collapsed:\n%s", got)
	}
	if strings.Contains(got, "AGENTS.md") {
		t.Errorf("uninstalled files should be collapsed, not listed:\n%s", got)
	}
	if strings.Contains(got, "claude") {
		t.Errorf("empty-dest noise row should be dropped:\n%s", got)
	}
}

func TestPrintStatusGridAllClean(t *testing.T) {
	output.SetColor(false)
	t.Cleanup(func() { output.SetColor(false) })
	got := captureStdout(t, func() {
		printStatusGrid([]statusRow{{render: engine.ActionInSync}, {render: engine.ActionInSync}}, map[string]bool{})
	})
	if !strings.Contains(got, "everything in sync (2 files)") {
		t.Errorf("all-clean grid = %q", got)
	}
}

// TestPrintStatusOrigin covers both loader paths: a present friday.yaml is
// authoritative; its absence falls back to built-in presets.
func TestPrintStatusOrigin(t *testing.T) {
	output.SetColor(false)
	t.Cleanup(func() { output.SetColor(false) })

	dir := t.TempDir()
	cfg := &config.Config{
		StoreDir:     dir,
		ManifestPath: filepath.Join(dir, "friday.yaml"),
		Adapters:     map[string]*config.Adapter{"claude": {Target: "~/.claude"}},
	}

	// No manifest on disk → fallback.
	got := captureStdout(t, func() { printStatusOrigin(cfg) })
	if !strings.Contains(got, "built-in") || !strings.Contains(got, "no friday.yaml") {
		t.Errorf("fallback origin view = %q", got)
	}

	// Manifest present → authoritative.
	if err := os.WriteFile(cfg.ManifestPath, []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got = captureStdout(t, func() { printStatusOrigin(cfg) })
	if !strings.Contains(got, "friday.yaml") || !strings.Contains(got, "defined in") {
		t.Errorf("manifest origin view = %q", got)
	}
}

// TestHandEditedLookupNeverWritesState is the crown-jewel regression guard:
// the column-1 drift read must never mutate the on-disk drift store.
func TestHandEditedLookupNeverWritesState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if runtime.GOOS == "windows" {
		t.Setenv("LocalAppData", t.TempDir())
	}
	path, err := drift.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	// Seed a real drift store with one baseline.
	st, _ := drift.Load(path)
	st.Set("claude", "/abs/CLAUDE.md", []byte("baseline"))
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	lookup := handEditedLookup()
	_ = lookup("claude", "/abs/CLAUDE.md") // known baseline
	_ = lookup("codex", "/abs/AGENTS.md")  // unknown target

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("status drift read mutated state.json:\nbefore=%q\nafter =%q", before, after)
	}
}
