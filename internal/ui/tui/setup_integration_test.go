package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/list"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/setupcmd"
)

// TestCatalogCmdBuildsBaselineChecklist verifies the agent→items step: the
// catalog read builds checklist rows and pre-checks the fresh baseline
// (core + rules), matching the CLI setup's suggested set via setupcmd.Suggestions.
func TestCatalogCmdBuildsBaselineChecklist(t *testing.T) {
	home := isolatedHome(t)
	storeDir := filepath.Join(home, ".friday")
	mustWrite(t, filepath.Join(storeDir, "core.md"), "# Core\n")
	mustWrite(t, filepath.Join(storeDir, "rules", "general.md"), "Be precise.\n")

	msg := catalogCmd("claude", storeDir, t.TempDir())().(catalogReadyMsg)
	if msg.err != nil {
		t.Fatalf("catalogCmd: %v", msg.err)
	}
	if len(msg.items) == 0 || len(msg.choices) == 0 {
		t.Fatal("catalog/choices not populated")
	}
	checked := 0
	for _, c := range msg.choices {
		if c.checked {
			checked++
		}
	}
	if checked == 0 {
		t.Error("fresh project: expected the core/rules baseline pre-checked, got none")
	}
}

// TestChooseSetupAgentGoesToRunning verifies choosing an agent launches the
// catalog read off the Update goroutine (running screen + a command) rather
// than blocking inline.
func TestChooseSetupAgentGoesToRunning(t *testing.T) {
	m := newModel("test", nil, &config.Config{StoreDir: t.TempDir()}, nil, nil)
	m.agents = newList("", []list.Item{commandItem{name: "claude"}}, 80, 24, m.styles)

	next, cmd := m.chooseSetupAgent()
	if got := next.(model).screen; got != screenRunning {
		t.Errorf("screen = %v, want screenRunning", got)
	}
	if cmd == nil {
		t.Error("choosing an agent should issue a catalog command")
	}
}

// TestSelectedCatalogItems pins the index→Item mapping that turns the user's
// ticks into what actually gets written.
func TestSelectedCatalogItems(t *testing.T) {
	m := model{catalog: []setupcmd.Item{
		{Category: "core", Name: "core"},
		{Category: "rules", Name: "general"},
		{Category: "skills", Name: "foo"},
	}}
	m.pick = newChecklist("x", []checklistItem{
		{value: "0", checked: true},
		{value: "1", checked: false},
		{value: "2", checked: true},
	})
	got := m.selectedCatalogItems()
	if len(got) != 2 || got[0].Name != "core" || got[1].Name != "foo" {
		t.Errorf("selectedCatalogItems = %+v, want [core foo]", got)
	}
}

// TestSetupCmdPreviewThenApply is the end-to-end proof that guided setup writes
// into the PROJECT dir (not $HOME): preview writes nothing, apply writes the
// project's agent config, all isolated.
func TestSetupCmdPreviewThenApply(t *testing.T) {
	home := isolatedHome(t)
	storeDir := filepath.Join(home, ".friday")
	mustWrite(t, filepath.Join(storeDir, "core.md"), "# Core\n\nProject brief.\n")
	mustWrite(t, filepath.Join(storeDir, "rules", "general.md"), "Be precise.\n")
	project := t.TempDir()

	items, err := setupcmd.Catalog(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatal("empty catalog")
	}

	preview := setupCmd("claude", items, storeDir, project, false, nil)().(engineDoneMsg)
	if preview.err != nil {
		t.Fatalf("preview: %v", preview.err)
	}
	dest := firstWrittenDest(preview.changes)
	if dest == "" {
		t.Fatalf("preview produced no create/update; got %d changes", len(preview.changes))
	}
	if _, err := os.Stat(dest); err == nil {
		t.Fatalf("dry-run preview wrote %s — it must not touch disk", dest)
	}

	applied := setupCmd("claude", items, storeDir, project, true, nil)().(engineDoneMsg)
	if applied.err != nil {
		t.Fatalf("apply: %v", applied.err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Errorf("apply did not write %s: %v", dest, err)
	}
	if !strings.HasPrefix(dest, project) {
		t.Errorf("setup wrote outside the project dir: %s (project %s)", dest, project)
	}
}
