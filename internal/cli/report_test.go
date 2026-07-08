package cli

import (
	"encoding/json"
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
