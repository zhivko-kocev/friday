package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/output"
)

// TestStatusJSONBodyUnchanged is the tripwire the output redesign must not
// trip: the --json body is built by buildStatusJSON, wholly separate from the
// human render path. This pins the summary counts, the preserved
// reason/warning fields, the stable action vocabulary, and that no
// human-format token (folder breakdowns, "in sync" tallies, diff footers)
// leaks into the machine output.
func TestStatusJSONBodyUnchanged(t *testing.T) {
	cfg := &config.Config{
		StoreDir: "/store",
		Adapters: map[string]*config.Adapter{"claude": {Target: "/tgt"}},
	}
	changes := []engine.Change{
		{Adapter: "claude", DestRel: "CLAUDE.md", Action: engine.ActionUpdate},
		{Adapter: "claude", DestRel: "agents/a.md", Action: engine.ActionInSync},
		{Adapter: "claude", DestRel: "rules/x.md", Action: engine.ActionConflict, Reason: "both changed", Warning: "over max_bytes"},
	}
	got := buildStatusJSON(cfg, changes)

	wantSummary := map[string]int{"created": 0, "updated": 1, "in_sync": 1, "conflict": 1, "skipped": 0}
	for k, v := range wantSummary {
		if got.Summary[k] != v {
			t.Errorf("summary[%s] = %d, want %d", k, got.Summary[k], v)
		}
	}
	var conflict changeJSON
	for _, a := range got.Adapters {
		for _, c := range a.Changes {
			if c.Action == "conflict" {
				conflict = c
			}
		}
	}
	if conflict.Reason != "both changed" || conflict.Warning != "over max_bytes" {
		t.Errorf("conflict change lost reason/warning: %+v", conflict)
	}
	blob, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"×", "file(s) in sync", "hunk", "would-", "! conflict"} {
		if strings.Contains(string(blob), leak) {
			t.Errorf("human-format token %q leaked into --json body:\n%s", leak, blob)
		}
	}
}

func TestFolderBreakdown(t *testing.T) {
	cases := []struct {
		name  string
		dests []string
		want  string
	}{
		{"none", nil, ""},
		{"root file", []string{"CLAUDE.md"}, "(CLAUDE.md)"},
		{"single subdir file", []string{"agents/a.md"}, "(agents/)"},
		{"multi subdir", []string{"agents/a.md", "agents/b.md"}, "(agents/×2)"},
		{"mixed", []string{"CLAUDE.md", "agents/a.md", "agents/b.md"}, "(CLAUDE.md, agents/×2)"},
		{
			"over cap folds tail",
			[]string{"CLAUDE.md", "agents/a.md", "agents/b.md", "skills/x.md", "commands/c.md", "hooks/h.json", "standards/s.md"},
			"(CLAUDE.md, agents/×2, skills/, commands/, +2 more)",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := folderBreakdown(c.dests); got != c.want {
				t.Errorf("folderBreakdown(%v) = %q, want %q", c.dests, got, c.want)
			}
		})
	}
}

func TestChangeCounts(t *testing.T) {
	cases := []struct {
		created, updated int
		dryRun           bool
		want             string
	}{
		{0, 0, false, ""},
		{2, 0, false, "2 created"},
		{0, 3, false, "3 updated"},
		{1, 2, false, "1 created, 2 updated"},
		{1, 0, true, "1 to create"},
		{0, 2, true, "2 to update"},
	}
	for _, c := range cases {
		if got := changeCounts(c.created, c.updated, c.dryRun); got != c.want {
			t.Errorf("changeCounts(%d,%d,%v) = %q, want %q", c.created, c.updated, c.dryRun, got, c.want)
		}
	}
}

// TestReportCollapses pins the headline behavior: created/updated fold into one
// count-plus-breakdown line per adapter, in-sync collapses to a tally, and only
// conflicts are named per file.
func TestReportCollapses(t *testing.T) {
	output.SetColor(false)
	t.Cleanup(func() { output.SetColor(false) })
	changes := []engine.Change{
		{Adapter: "claude", DestRel: "CLAUDE.md", Action: engine.ActionUpdate},
		{Adapter: "claude", DestRel: "agents/a.md", Action: engine.ActionUpdate},
		{Adapter: "claude", DestRel: "agents/b.md", Action: engine.ActionUpdate},
		{Adapter: "claude", DestRel: "skills/x/SKILL.md", Action: engine.ActionInSync},
		{Adapter: "codex", DestRel: "AGENTS.md", Action: engine.ActionCreate},
		{Adapter: "copilot", DestRel: "copilot-instructions.md", Action: engine.ActionConflict, Reason: "drift"},
	}
	got := captureStdout(t, func() { report(changes, false, false) })
	for _, want := range []string{
		"3 updated  (CLAUDE.md, agents/×2)",
		"1 created  (AGENTS.md)",
		"! conflict  copilot-instructions.md",
		"1 file(s) in sync",
		"summary: 1 created, 3 updated, 1 conflict(s), 1 in-sync",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "agents/a.md") {
		t.Errorf("individual updated files should be collapsed, not listed:\n%s", got)
	}
	if strings.Contains(got, "\x1b") {
		t.Errorf("plain report leaked an escape sequence: %q", got)
	}
}

// TestReportSurfacesWarningsAndReasons guards the advisory info the collapse
// must not swallow: a rendered file over max_bytes still gets a warning line,
// and a conflict still shows its reason.
func TestReportSurfacesWarningsAndReasons(t *testing.T) {
	output.SetColor(false)
	t.Cleanup(func() { output.SetColor(false) })
	changes := []engine.Change{
		{Adapter: "claude", DestRel: "CLAUDE.md", Action: engine.ActionUpdate, Warning: "over max_bytes"},
		{Adapter: "claude", DestRel: "agents/a.md", Action: engine.ActionUpdate},
		{Adapter: "codex", DestRel: "AGENTS.md", Action: engine.ActionConflict, Reason: "both sides changed"},
	}
	got := captureStdout(t, func() { report(changes, false, false) })
	for _, want := range []string{
		"2 updated",                                  // both updates still counted
		"! CLAUDE.md (warning: over max_bytes)",      // advisory surfaced
		"! conflict  AGENTS.md (both sides changed)", // conflict reason kept
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report missing %q in:\n%s", want, got)
		}
	}
}

// TestReportCollapsesMissingSources guards the fresh-store case: the presets
// map optional store files, so a brand-new store reports ~5 missing sources
// per adapter — dozens of lines burying the handful of real writes. They fold
// into a count, while an unsupported skip (a decision waiting on the user)
// stays named, and --verbose still lists every one.
func TestReportCollapsesMissingSources(t *testing.T) {
	output.SetColor(false)
	t.Cleanup(func() {
		output.SetColor(false)
		verbose = false
	})
	changes := []engine.Change{
		{Adapter: "claude", DestRel: "CLAUDE.md", Action: engine.ActionCreate},
		{Adapter: "claude", DestRel: "mcp.json", Action: engine.ActionMissingSource, Reason: `no source file at "mcp.yaml"`},
		{Adapter: "claude", DestRel: "settings.json", Action: engine.ActionMissingSource, Reason: `no source file at "settings/claude.json"`},
		{Adapter: "claude", DestRel: "hooks.json", Action: engine.ActionUnsupported, Reason: "hook wiring skipped"},
	}

	verbose = false
	got := captureStdout(t, func() { report(changes, false, false) })
	if !strings.Contains(got, "2 missing source(s)") {
		t.Errorf("missing sources not collapsed:\n%s", got)
	}
	if strings.Contains(got, `no source file at "mcp.yaml"`) {
		t.Errorf("collapsed missing sources should not be named:\n%s", got)
	}
	if !strings.Contains(got, "– hooks.json (hook wiring skipped)") {
		t.Errorf("unsupported skip must stay named — it needs a decision:\n%s", got)
	}
	if !strings.Contains(got, "--verbose") {
		t.Errorf("collapsed report must point at the flag that expands it:\n%s", got)
	}

	verbose = true
	got = captureStdout(t, func() { report(changes, false, false) })
	for _, want := range []string{`no source file at "mcp.yaml"`, `no source file at "settings/claude.json"`} {
		if !strings.Contains(got, want) {
			t.Errorf("--verbose report missing %q in:\n%s", want, got)
		}
	}
}

func TestReportDryRunPhrasing(t *testing.T) {
	output.SetColor(false)
	t.Cleanup(func() { output.SetColor(false) })
	changes := []engine.Change{
		{Adapter: "claude", DestRel: "CLAUDE.md", Action: engine.ActionUpdate},
		{Adapter: "codex", DestRel: "AGENTS.md", Action: engine.ActionCreate},
	}
	got := captureStdout(t, func() { report(changes, false, true) })
	for _, want := range []string{"changes (dry-run):", "1 to update", "1 to create"} {
		if !strings.Contains(got, want) {
			t.Errorf("dry-run report missing %q in:\n%s", want, got)
		}
	}
}

func TestReportNoChanges(t *testing.T) {
	output.SetColor(false)
	t.Cleanup(func() { output.SetColor(false) })
	got := captureStdout(t, func() { report(nil, false, false) })
	if !strings.Contains(got, "no changes") {
		t.Errorf("empty report = %q", got)
	}
}

// TestReportVerboseShowsPerFileSources guards D-verbose: with verbose on, the
// collapsed created/updated line is replaced by one line per file naming the
// resolved source(s) that rendered it, and the collapsed count-plus-folder
// line must not also appear (verbose fully replaces it, not adds to it).
func TestReportVerboseShowsPerFileSources(t *testing.T) {
	output.SetColor(false)
	verbose = true
	t.Cleanup(func() {
		output.SetColor(false)
		verbose = false
	})
	changes := []engine.Change{
		{Adapter: "claude", DestRel: "CLAUDE.md", Action: engine.ActionCreate, Sources: []string{"core.md", "rules/general.md"}},
		{Adapter: "claude", DestRel: "agents/a.md", Action: engine.ActionUpdate, Sources: []string{"agents/a.md"}},
	}
	got := captureStdout(t, func() { report(changes, false, false) })
	for _, want := range []string{
		"claude  + CLAUDE.md  <- core.md, rules/general.md",
		"claude  ~ agents/a.md  <- agents/a.md",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("verbose report missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "1 created, 1 updated  (") {
		t.Errorf("verbose report should not also print the collapsed line:\n%s", got)
	}
}

// TestReportQuietSuppressesAllOutput guards --quiet: report() calls nothing
// but output.* helpers, all of which honor quiet, so the whole function must
// go silent — the exit-code contract (exitCode/exitErr) is computed straight
// from the change list and is unaffected by whether anything was printed.
func TestReportQuietSuppressesAllOutput(t *testing.T) {
	output.SetColor(false)
	output.SetQuiet(true)
	t.Cleanup(func() {
		output.SetColor(false)
		output.SetQuiet(false)
	})
	changes := []engine.Change{
		{Adapter: "claude", DestRel: "CLAUDE.md", Action: engine.ActionUpdate},
		{Adapter: "codex", DestRel: "AGENTS.md", Action: engine.ActionConflict, Reason: "drift"},
	}
	got := captureStdout(t, func() { report(changes, true, false) })
	if got != "" {
		t.Errorf("quiet report produced output: %q", got)
	}
	if code := exitCode(changes); code != 2 {
		t.Errorf("quiet mode must not change the exit code: got %d, want 2", code)
	}
}

// TestPrintStatusGridCollapsesInstalledPending — a large same-action pending
// group folds into a count line, while a hand edit in the same adapter stays an
// individual, actionable row.
func TestPrintStatusGridCollapsesInstalledPending(t *testing.T) {
	output.SetColor(false)
	t.Cleanup(func() { output.SetColor(false) })
	rows := []statusRow{
		{handEdit: true, render: engine.ActionUpdate, adapter: "claude", dest: "CLAUDE.md"},
		{render: engine.ActionUpdate, adapter: "claude", dest: "agents/a.md"},
		{render: engine.ActionUpdate, adapter: "claude", dest: "agents/b.md"},
		{render: engine.ActionUpdate, adapter: "claude", dest: "agents/c.md"},
	}
	got := captureStdout(t, func() { printStatusGrid(rows, map[string]bool{"claude": true}) })
	if !strings.Contains(got, "MM  claude  CLAUDE.md") {
		t.Errorf("hand-edited row should stay individual:\n%s", got)
	}
	if !strings.Contains(got, "3 files (agents/×3)") {
		t.Errorf("pending renders should collapse:\n%s", got)
	}
	if strings.Contains(got, "agents/a.md") {
		t.Errorf("collapsed files should not be listed individually:\n%s", got)
	}
}

// TestReportVerboseNamesRootOnCollision is the multi-root case --verbose
// exists for: the entry-file exception contributes every root's copy under
// the same store-relative name, so an untagged list reads "core.md, core.md"
// and tells the user nothing. A change confined to one root must stay
// untagged — that is the common case and its output is pinned elsewhere.
func TestReportVerboseNamesRootOnCollision(t *testing.T) {
	output.SetColor(false)
	verbose = true
	t.Cleanup(func() {
		output.SetColor(false)
		verbose = false
	})
	store := filepath.Join("/home", "u", ".friday")
	team := filepath.Join(store, ".sources", "team")
	abs := func(root, rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }
	changes := []engine.Change{{
		Adapter: "test", DestRel: "OUT.md", Action: engine.ActionCreate,
		Sources:   []string{"core.md", "core.md", "rules/a.md"},
		SourceAbs: []string{abs(team, "core.md"), abs(store, "core.md"), abs(store, "rules/a.md")},
	}, {
		Adapter: "test", DestRel: "SOLO.md", Action: engine.ActionUpdate,
		Sources:   []string{"core.md", "rules/a.md"},
		SourceAbs: []string{abs(store, "core.md"), abs(store, "rules/a.md")},
	}}
	got := captureStdout(t, func() { report(changes, false, false) })
	if !strings.Contains(got, "+ OUT.md  <- core.md@team, core.md@.friday, rules/a.md@.friday") {
		t.Errorf("multi-root verbose line does not name the roots:\n%s", got)
	}
	if !strings.Contains(got, "~ SOLO.md  <- core.md, rules/a.md") {
		t.Errorf("single-root verbose line should stay untagged:\n%s", got)
	}
}

// TestReportVerboseToleratesMissingSourceAbs pins the placeholder contract:
// a MissingSource entry has no file on disk and carries "" in SourceAbs, so
// there is no root to attribute it to and it must render bare rather than
// panicking or printing a stray "@".
func TestReportVerboseToleratesMissingSourceAbs(t *testing.T) {
	output.SetColor(false)
	verbose = true
	t.Cleanup(func() {
		output.SetColor(false)
		verbose = false
	})
	store := filepath.Join("/home", "u", ".friday")
	team := filepath.Join(store, ".sources", "team")
	changes := []engine.Change{{
		Adapter: "test", DestRel: "OUT.md", Action: engine.ActionCreate,
		Sources:   []string{"core.md", "core.md", "gone.md"},
		SourceAbs: []string{filepath.Join(team, "core.md"), filepath.Join(store, "core.md"), ""},
	}}
	got := captureStdout(t, func() { report(changes, false, false) })
	if !strings.Contains(got, "core.md@team, core.md@.friday, gone.md") {
		t.Errorf("unattributable source should render bare:\n%s", got)
	}
}

// TestRootFlagsReachTheReport guards the --verbose / --quiet root flags end
// to end. Both are package-level state written only by a persistent-flag
// Action, and whether urfave fires that Action for a flag appearing after the
// subcommand is a dependency detail; every other test sets the state directly
// and would keep passing if the wiring broke.
func TestRootFlagsReachTheReport(t *testing.T) {
	output.SetColor(false)
	t.Cleanup(func() {
		output.SetColor(false)
		output.SetQuiet(false)
		verbose = false
	})
	seedPushFixture(t)

	for _, tc := range []struct {
		name string
		argv []string
	}{
		{"verbose after the subcommand", []string{"push", "test", "--dry-run", "--verbose"}},
		{"verbose before the subcommand", []string{"--verbose", "push", "test", "--dry-run"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			verbose = false
			var code int
			got := captureStdout(t, func() { code = Run(tc.argv, "test") })
			if code != 0 {
				t.Fatalf("exit = %d, want 0", code)
			}
			if !strings.Contains(got, "<- core.md") {
				t.Errorf("--verbose did not reach report():\n%s", got)
			}
		})
	}

	for _, tc := range []struct {
		name string
		argv []string
	}{
		{"quiet after the subcommand", []string{"push", "test", "--dry-run", "--quiet"}},
		{"quiet before the subcommand", []string{"--quiet", "push", "test", "--dry-run"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output.SetQuiet(false)
			verbose = false
			var code int
			got := captureStdout(t, func() { code = Run(tc.argv, "test") })
			if code != 0 {
				t.Fatalf("exit = %d, want 0", code)
			}
			if got != "" {
				t.Errorf("--quiet did not reach the output helpers:\n%s", got)
			}
		})
	}
}

// seedPushFixture builds a throwaway store with one adapter whose target dir
// does not exist, so `push test --dry-run` plans exactly one create.
func seedPushFixture(t *testing.T) {
	t.Helper()
	setDriftEnv(t)
	home, _ := os.UserHomeDir()
	store := filepath.Join(home, ".friday")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "core.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	tgt := filepath.ToSlash(filepath.Join(home, "tgt"))
	manifest := "version: 1\nadapters:\n  test:\n    target: " + tgt +
		"\n    rules:\n      - from: [core.md]\n        to: OUT.md\n        strategy: concatenate\n"
	if err := os.WriteFile(filepath.Join(store, "friday.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}
