package tui

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/engine"
)

// TestControlRoomHomeRenders is the teatest scaffold: it proves the interactive
// path is drivable end-to-end in a test — the home screen lists its commands and
// `q` quits cleanly with exit 0. Richer per-screen golden assertions build on
// this harness in later slices.
func TestControlRoomHomeRenders(t *testing.T) {
	menu := []MenuEntry{{Name: "status", Summary: "show what would change (no writes)"}}
	m := newModel("test", menu, nil, nil, nil) // no store needed: we don't run a verb here

	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))

	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("status"))
	}, teatest.WithDuration(3*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))

	fm, ok := tm.FinalModel(t).(model)
	if !ok {
		t.Fatalf("final model type = %T, want tui.model", tm.FinalModel(t))
	}
	if fm.exit != 0 {
		t.Errorf("clean quit exit = %d, want 0", fm.exit)
	}
}

// TestControlRoomSyncOpensPicker drives home → sync → the adapter picker and
// asserts it renders the installed agents. The picker is built from the passed
// installed list (no engine call), so this stays deterministic and offline; it
// quits before pressing enter, so no push runs.
func TestControlRoomSyncOpensPicker(t *testing.T) {
	menu := []MenuEntry{
		{Name: "status", Summary: "show what would change (no writes)"},
		{Name: "sync", Summary: "capture local edits, then fan them to every agent"},
	}
	m := newModel("test", menu, &config.Config{}, []string{"claude", "codex"}, nil)

	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))
	tm.Send(tea.KeyMsg{Type: tea.KeyDown})  // move to "sync"
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter}) // open the picker

	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("fan out")) && bytes.Contains(b, []byte("claude"))
	}, teatest.WithDuration(3*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

// TestControlRoomSetupOpensAgentPicker drives home → setup → the agent picker,
// which is built from presets.Names() (no store read), so it stays offline. It
// quits before choosing an agent, so no catalog/engine work runs.
func TestControlRoomSetupOpensAgentPicker(t *testing.T) {
	menu := []MenuEntry{{Name: "setup", Summary: "add friday knowledge to the current project"}}
	m := newModel("test", menu, &config.Config{}, nil, nil)

	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter}) // open the agent picker

	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("which agent")) && bytes.Contains(b, []byte("claude"))
	}, teatest.WithDuration(3*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

// TestControlRoomSetupFullPreview drives the deepest interactive path through
// the real Program: home → setup → pick agent → (async catalog load) → item
// checklist → preview. It stops at the dry-run preview, which writes nothing, so
// it is safe regardless of the test's working dir. Exercises the async catalog
// chain and the setup preview end-to-end.
func TestControlRoomSetupFullPreview(t *testing.T) {
	home := isolatedHome(t)
	storeDir := filepath.Join(home, ".friday")
	mustWrite(t, filepath.Join(storeDir, "core.md"), "# Core\n")
	mustWrite(t, filepath.Join(storeDir, "rules", "general.md"), "Be precise.\n")

	menu := []MenuEntry{{Name: "setup", Summary: "add friday knowledge to the current project"}}
	m := newModel("test", menu, &config.Config{StoreDir: storeDir}, nil, nil)

	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter}) // home → agent picker
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("claude"))
	}, teatest.WithDuration(3*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyEnter}) // pick agent → async catalog → item checklist
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("core / core"))
	}, teatest.WithDuration(3*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyEnter}) // confirm selection → dry-run preview
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("preview — nothing written yet"))
	}, teatest.WithDuration(3*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

// TestSyncPickEmptySelectionIsNoOp guards the footgun: an empty checked-set must
// never reach engine.Push (empty Adapters means ALL adapters). Enter on an empty
// picker stays put and issues no command.
func TestSyncPickEmptySelectionIsNoOp(t *testing.T) {
	m := newModel("test", nil, &config.Config{}, []string{"claude"}, nil)
	m.pick = newChecklist("push", []checklistItem{{label: "claude", value: "claude"}})
	m.screen = screenSyncPick

	next, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	nm := next.(model)
	if nm.screen != screenSyncPick {
		t.Errorf("empty-selection enter changed screen to %v, want screenSyncPick", nm.screen)
	}
	if nm.pending != nil {
		t.Errorf("empty-selection enter armed a pending apply, want none")
	}
	if cmd != nil {
		t.Errorf("empty-selection enter issued a command, want none")
	}
}

// TestSyncPickWithSelectionStartsApply verifies enter with a selection arms the
// preview (screen → running, a pending apply set, a command issued). The command
// closure is not executed here, so no engine call happens.
func TestSyncPickWithSelectionStartsApply(t *testing.T) {
	m := newModel("test", nil, &config.Config{}, []string{"claude"}, nil)
	m.pick = newChecklist("push", []checklistItem{{label: "claude", value: "claude", checked: true}})
	m.screen = screenSyncPick

	next, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	nm := next.(model)
	if nm.screen != screenRunning {
		t.Errorf("selection enter screen = %v, want screenRunning", nm.screen)
	}
	if nm.pending == nil {
		t.Errorf("selection enter did not arm a pending apply")
	}
	if cmd == nil {
		t.Errorf("selection enter should issue a command")
	}
}

// TestApplyShowsConfirmation checks that a completed apply flags the result and
// renders a positive "applied N file(s)" confirmation, so applied-state is
// unmistakable from a preview.
func TestApplyShowsConfirmation(t *testing.T) {
	m := newModel("test", nil, &config.Config{}, nil, nil)
	done := engineDoneMsg{
		changes: []engine.Change{{Adapter: "claude", Action: engine.ActionCreate, DestRel: "CLAUDE.md"}},
		applied: true,
	}
	next, _ := m.Update(done)
	nm := next.(model)
	if !nm.applied {
		t.Fatal("applied flag not set after an apply result")
	}
	if got := nm.body(); !strings.Contains(got, "applied 1 file") {
		t.Errorf("no apply confirmation in body: %q", got)
	}
}

// TestSelectCommandResetsPriorApplyState guards the state-leak: a prior apply's
// confirmation and advisories must not bleed onto the next screen when a
// command lands on a terminal notice (here sync with no installed agents).
func TestSelectCommandResetsPriorApplyState(t *testing.T) {
	m := newModel("test", []MenuEntry{{Name: "sync", Summary: "sync"}}, &config.Config{}, nil, nil)
	m.applied = true
	m.warnings = []string{"stale advisory"}
	m.opErr = errors.New("stale error")
	m.result = []engine.Change{{Adapter: "claude", Action: engine.ActionCreate, DestRel: "CLAUDE.md"}}

	next, _ := m.selectCommand() // no installed agents → notice screen
	nm := next.(model)
	if nm.applied || len(nm.warnings) != 0 || nm.opErr != nil || nm.result != nil {
		t.Errorf("prior op state leaked: applied=%v warnings=%v opErr=%v result=%v",
			nm.applied, nm.warnings, nm.opErr, nm.result)
	}
	body := nm.body()
	if strings.Contains(body, "applied") || strings.Contains(body, "stale error") {
		t.Errorf("stale state rendered on notice screen: %q", body)
	}
	if !strings.Contains(body, "no installed agents") {
		t.Errorf("notice not shown: %q", body)
	}
}

// TestControlRoomLoadErrorShowsErrorScreen covers a genuine load failure (a
// store that is present but broken): the control room opens on a friendly error
// screen instead of crashing, and still quits cleanly. (An absent/empty store
// instead opens cold-start — see coldstart_integration_test.go.)
func TestControlRoomLoadErrorShowsErrorScreen(t *testing.T) {
	menu := []MenuEntry{{Name: "status", Summary: "show what would change (no writes)"}}
	m := newModel("test", menu, nil, nil, errors.New("load user store: malformed friday.yaml"))

	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))

	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("malformed friday.yaml"))
	}, teatest.WithDuration(3*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}
