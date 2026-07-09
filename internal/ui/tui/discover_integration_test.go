package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/presets"
)

// discoverFixture seeds a store plus one target-only agent file (a copy-rule
// dest, so it's importable — unlike the concatenate CLAUDE.md) that is not yet
// in the store. Returns cfg.
func discoverFixture(t *testing.T) (*config.Config, string) {
	t.Helper()
	home := isolatedHome(t)
	storeDir := filepath.Join(home, ".friday")
	mustWrite(t, filepath.Join(storeDir, "core.md"), "# Core\n")
	mustWrite(t, filepath.Join(home, ".claude", "agents", "bar.md"), "discovered agent\n")

	p, ok := presets.Get("claude")
	if !ok {
		t.Fatal("claude preset missing")
	}
	cfg := config.NewDefault(config.ScopeUser, storeDir, home,
		map[string]*config.Adapter{"claude": p.Adapter()})
	return cfg, storeDir
}

// TestDiscoverFindsAndImportsTargetOnlyFile proves the scan surfaces a file that
// exists only in an agent dir and that importing it captures it into the store.
func TestDiscoverFindsAndImportsTargetOnlyFile(t *testing.T) {
	cfg, storeDir := discoverFixture(t)

	scan := discoverCmd(cfg, []string{"claude"})().(discoverReadyMsg)
	if scan.err != nil {
		t.Fatalf("discover scan: %v", scan.err)
	}
	if scan.empty || len(scan.changes) == 0 {
		t.Fatal("discover found nothing; expected agents/bar.md")
	}
	found := false
	for _, ch := range scan.changes {
		if strings.HasSuffix(filepath.ToSlash(ch.DestRel), "agents/bar.md") {
			found = true
		}
	}
	if !found {
		t.Fatalf("scan missed agents/bar.md; got %+v", scan.changes)
	}

	sel := map[string][]string{}
	for _, ch := range scan.changes {
		sel[ch.Adapter] = append(sel[ch.Adapter], ch.Sources...)
	}
	done := importCmd(cfg, sel, true, nil)().(engineDoneMsg)
	if done.err != nil {
		t.Fatalf("import: %v", done.err)
	}
	if _, err := os.Stat(filepath.Join(storeDir, "agents", "bar.md")); err != nil {
		t.Errorf("import did not capture agents/bar.md into the store: %v", err)
	}
}

// TestDiscoverImportIsAdapterScoped guards the cross-adapter leak: two agents
// with a target-only file at the SAME store-relative path (skills/foo/x.md), and
// importing only one must not capture the other's copy.
func TestDiscoverImportIsAdapterScoped(t *testing.T) {
	home := isolatedHome(t)
	storeDir := filepath.Join(home, ".friday")
	mustWrite(t, filepath.Join(storeDir, "core.md"), "# Core\n")
	mustWrite(t, filepath.Join(home, ".claude", "skills", "foo", "x.md"), "claude version\n")
	mustWrite(t, filepath.Join(home, ".codex", "skills", "foo", "x.md"), "codex version\n")

	claude, _ := presets.Get("claude")
	codex, _ := presets.Get("codex")
	cfg := config.NewDefault(config.ScopeUser, storeDir, home, map[string]*config.Adapter{
		"claude": claude.Adapter(),
		"codex":  codex.Adapter(),
	})

	// Import ONLY claude's copy (both map to store skills/foo/x.md).
	done := importCmd(cfg, map[string][]string{"claude": {"skills/foo/x.md"}}, true, nil)().(engineDoneMsg)
	if done.err != nil {
		t.Fatal(done.err)
	}
	got, err := os.ReadFile(filepath.Join(storeDir, "skills", "foo", "x.md"))
	if err != nil {
		t.Fatalf("claude's selection was not imported: %v", err)
	}
	if strings.Contains(string(got), "codex version") {
		t.Errorf("codex's file leaked into the store despite selecting only claude: %q", got)
	}
	if !strings.Contains(string(got), "claude version") {
		t.Errorf("expected claude's content in the store, got %q", got)
	}
}

// TestControlRoomDiscoverFlow drives the whole discover path through the real
// Program: home → scan → checklist → import, and confirms the file lands in the
// store.
func TestControlRoomDiscoverFlow(t *testing.T) {
	cfg, storeDir := discoverFixture(t)

	menu := []MenuEntry{{Name: "discover", Summary: "import agent files not yet in your store"}}
	m := newModel("test", menu, cfg, []string{"claude"}, nil)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(100, 30))
	m.send.set(tm.Send)

	waitForText(t, tm, "import agent files") // home rendered & consuming input
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})  // home → scan → discover checklist
	// One wait per frame: WaitFor consumes the reader, so a second wait for text
	// in the same frame would never re-match. This item text proves the checklist
	// rendered with content.
	waitForText(t, tm, "agents/bar.md")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter}) // import the (pre-checked) selection
	waitForText(t, tm, "applied")
	quit(t, tm)

	if _, err := os.Stat(filepath.Join(storeDir, "agents", "bar.md")); err != nil {
		t.Errorf("discover flow did not import agents/bar.md into the store: %v", err)
	}
}
