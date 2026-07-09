package tui

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/zhivko-kocev/friday/internal/config"
)

// loadUserStore mirrors what launchTUI's reload closure does, using only the
// config package (the TUI can't import cli). After a scaffold this succeeds.
func loadUserStore() (*config.Config, []string, error) {
	cfg, err := config.LoadUser()
	return cfg, nil, err
}

// TestControlRoomColdStartScaffold drives the offline cold-start path: a fresh
// machine opens on the input, a blank entry scaffolds ~/.friday, and the control
// room lands on home with a usable store.
func TestControlRoomColdStartScaffold(t *testing.T) {
	home := isolatedHome(t) // ~/.friday absent

	menu := []MenuEntry{{Name: "status", Summary: "show what would change (no writes)"}}
	m := newModel("test", menu, nil, nil, nil)
	m.reload = loadUserStore
	m.screen = screenColdStart
	m.input.Focus()

	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(100, 30))
	waitForText(t, tm, "first run")         // cold-start input rendered
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter}) // blank → scaffold → home
	waitForText(t, tm, "status")            // home menu after the store is ready
	quit(t, tm)

	storeDir := filepath.Join(home, ".friday")
	if _, err := os.Stat(filepath.Join(storeDir, "core.md")); err != nil {
		t.Errorf("cold-start scaffold did not create the store: %v", err)
	}
	if _, err := os.Stat(filepath.Join(storeDir, config.ManifestName)); err != nil {
		t.Errorf("cold-start scaffold did not write the manifest: %v", err)
	}
}

// TestColdStartCmdScaffolds proves the blank-URL path scaffolds and reloads —
// on an existing-but-empty ~/.friday, the "or empty" half of the cold-start
// trigger (idempotent MkdirAll + writeIfMissing must tolerate it).
func TestColdStartCmdScaffolds(t *testing.T) {
	home := isolatedHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".friday"), 0o755); err != nil {
		t.Fatal(err)
	}
	msg := coldStartCmd("", loadUserStore)().(coldStartDoneMsg)
	if msg.err != nil {
		t.Fatalf("scaffold: %v", msg.err)
	}
	if msg.cfg == nil {
		t.Error("reload returned a nil cfg after scaffold")
	}
	if _, err := os.Stat(filepath.Join(home, ".friday", "core.md")); err != nil {
		t.Errorf("scaffold missing core.md: %v", err)
	}
}

// TestColdStartCmdRejectsBadURL covers the clone seam's validation without a
// network: a flag-like URL is rejected rather than shelling out.
func TestColdStartCmdRejectsBadURL(t *testing.T) {
	isolatedHome(t)
	msg := coldStartCmd("--not-a-url", loadUserStore)().(coldStartDoneMsg)
	if msg.err == nil {
		t.Error("expected cold-start to reject a flag-like URL")
	}
}

// TestColdStartErrorStaysOnInput verifies a failed clone returns to the input
// (with the error shown) instead of stranding the user on a terminal screen.
func TestColdStartErrorStaysOnInput(t *testing.T) {
	m := newModel("test", nil, nil, nil, nil)
	m.screen = screenRunning
	next, _ := m.Update(coldStartDoneMsg{err: errors.New("bad url")})
	nm := next.(model)
	if nm.screen != screenColdStart {
		t.Errorf("after a cold-start error, screen = %v, want screenColdStart", nm.screen)
	}
	if nm.opErr == nil {
		t.Error("cold-start error not surfaced")
	}
}
