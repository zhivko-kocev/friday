package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/presets"
)

const tuiStoreHooks = `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"bash ${CLAUDE_PLUGIN_ROOT}/hooks/scripts/git-guard.sh"}]}]}}`

// TestBridgeConfirmerDegradesAndAborts mirrors the resolver degrade test: with no
// loop wired the confirmer skips (false) instead of blocking, and once abort is
// closed it fast-skips without touching the loop.
func TestBridgeConfirmerDegradesAndAborts(t *testing.T) {
	b := newBridge(&sender{})
	if b.confirmer()(engine.WriteConfirmInfo{}) {
		t.Error("unwired confirmer = true, want false (skip)")
	}
	b2 := newBridge(&sender{})
	b2.send.set(func(tea.Msg) { t.Error("aborting confirmer must not send to the loop") })
	close(b2.abort)
	if b2.confirmer()(engine.WriteConfirmInfo{}) {
		t.Error("aborting confirmer = true, want false (skip)")
	}
}

// hookStoreCfg seeds an isolated store with core.md + hooks/hooks.json and a
// claude adapter, returning cfg and the settings.json path the merge would write.
func hookStoreCfg(t *testing.T) (*config.Config, string) {
	t.Helper()
	home := isolatedHome(t)
	storeDir := filepath.Join(home, ".friday")
	mustWrite(t, filepath.Join(storeDir, "core.md"), "# Core\n")
	mustWrite(t, filepath.Join(storeDir, "hooks", "hooks.json"), tuiStoreHooks)
	p, ok := presets.Get("claude")
	if !ok {
		t.Fatal("claude preset missing")
	}
	cfg := config.NewDefault(config.ScopeUser, storeDir, home,
		map[string]*config.Adapter{"claude": p.Adapter()})
	return cfg, filepath.Join(home, ".claude", "settings.json")
}

// driveToConfirm runs the control room's sync flow up to the hook-wiring modal.
func driveToConfirm(t *testing.T, cfg *config.Config) *teatest.TestModel {
	t.Helper()
	m := newModel("test", []MenuEntry{{Name: "sync", Summary: "sync"}}, cfg, []string{"claude"}, nil)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(100, 40))
	m.send.set(tm.Send)
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter}) // home → adapter picker
	waitForText(t, tm, "fan out")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter}) // picker → dry-run preview
	waitForText(t, tm, "preview")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter}) // preview → apply → confirm modal
	waitForText(t, tm, "install hooks?")
	return tm
}

// TestConfirmBridgeInstallsHooks is the marquee proof: the sync apply raises the
// hook-wiring modal through the live bridge, and pressing "y" wires the store's
// hooks into settings.json.
func TestConfirmBridgeInstallsHooks(t *testing.T) {
	cfg, settings := hookStoreCfg(t)
	tm := driveToConfirm(t, cfg)

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")}) // install
	waitForText(t, tm, "applied")
	quit(t, tm)

	got, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("settings.json not written after confirm: %v", err)
	}
	if !strings.Contains(string(got), "git-guard.sh") {
		t.Errorf("hooks not wired into settings.json: %s", got)
	}
}

// TestConfirmBridgeDeclineSkips proves "n" skips the wiring: the apply completes
// but settings.json is never created.
func TestConfirmBridgeDeclineSkips(t *testing.T) {
	cfg, settings := hookStoreCfg(t)
	tm := driveToConfirm(t, cfg)

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}) // skip
	waitForText(t, tm, "applied")
	quit(t, tm)

	if _, err := os.Stat(settings); !os.IsNotExist(err) {
		t.Error("settings.json was written despite declining the confirm")
	}
}
