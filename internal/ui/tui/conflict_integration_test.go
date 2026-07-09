package tui

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/presets"
)

// TestBridgeResolverDegradesAndAborts proves the resolver's non-UI paths without
// a running program: with no event loop wired it reports (skips) instead of
// blocking, and once abort is closed it fast-skips.
func TestBridgeResolverDegradesAndAborts(t *testing.T) {
	// No loop wired → skip rather than block forever.
	b := newBridge(&sender{})
	if got := b.resolver()(engine.ConflictInfo{}); got.Choice != engine.ConflictSkip {
		t.Errorf("unwired resolver = %v, want skip", got.Choice)
	}
	// Aborting → fast-skip even with a loop wired.
	b2 := newBridge(&sender{})
	b2.send.set(func(tea.Msg) { t.Error("aborting resolver must not send to the loop") })
	close(b2.abort)
	if got := b2.resolver()(engine.ConflictInfo{}); got.Choice != engine.ConflictSkip {
		t.Errorf("aborting resolver = %v, want skip", got.Choice)
	}
}

// driftedConflictSetup seeds a store, applies once to record the baseline, then
// hand-edits the target so a second apply is a drifted Update — the exact
// condition that makes the engine call OnConflict. Returns cfg and the target
// path. Everything is isolated to a temp HOME + cache.
func driftedConflictSetup(t *testing.T) (*config.Config, string) {
	t.Helper()
	home := isolatedHome(t)
	storeDir := filepath.Join(home, ".friday")
	mustWrite(t, filepath.Join(storeDir, "core.md"), "# Core\n\nCanonical content.\n")

	p, ok := presets.Get("claude")
	if !ok {
		t.Fatal("claude preset missing")
	}
	cfg := config.NewDefault(config.ScopeUser, storeDir, home,
		map[string]*config.Adapter{"claude": p.Adapter()})

	seed := pushCmd(cfg, []string{"claude"}, true, nil)().(engineDoneMsg)
	if seed.err != nil {
		t.Fatalf("seed apply: %v", seed.err)
	}
	dest := firstWrittenDest(seed.changes)
	if dest == "" {
		t.Fatal("seed apply wrote nothing")
	}
	mustWrite(t, dest, "HAND EDIT — local drift\n")
	return cfg, dest
}

// driveToConflict runs the TUI sync flow up to the conflict modal and returns
// the live test model. The event loop is injected into the bridge via
// m.send.set(tm.Send) so the resolver can reach it under teatest.
func driveToConflict(t *testing.T, cfg *config.Config) *teatest.TestModel {
	t.Helper()
	m := newModel("test", []MenuEntry{{Name: "sync", Summary: "sync"}}, cfg, []string{"claude"}, nil)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(100, 30))
	m.send.set(tm.Send)

	tm.Send(tea.KeyMsg{Type: tea.KeyEnter}) // home → adapter picker
	waitForText(t, tm, "fan out")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter}) // picker → dry-run preview
	waitForText(t, tm, "preview — nothing written yet")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter}) // preview → apply → conflict modal
	waitForText(t, tm, "conflict — both sides changed")
	return tm
}

// TestConflictBridgeKeepCanonical is the marquee proof: a real drift conflict
// raises the modal through the live bridge, and choosing "keep" overwrites the
// local edit with canonical.
func TestConflictBridgeKeepCanonical(t *testing.T) {
	cfg, dest := driftedConflictSetup(t)
	tm := driveToConflict(t, cfg)

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")}) // keep canonical
	waitForText(t, tm, "applied")
	quit(t, tm)

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "HAND EDIT") {
		t.Errorf("keep-canonical did not overwrite the drift; file = %q", got)
	}
}

// TestConflictBridgeTakeTarget proves "take" keeps the local edit and adopts it
// as the new baseline — a follow-up dry-run then sees no conflict.
func TestConflictBridgeTakeTarget(t *testing.T) {
	cfg, dest := driftedConflictSetup(t)
	tm := driveToConflict(t, cfg)

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")}) // take target
	waitForText(t, tm, "applied")
	quit(t, tm)

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "HAND EDIT") {
		t.Errorf("take-target should keep the local edit; file = %q", got)
	}
	// Baseline adopted: a re-plan sees no conflict.
	after := pushCmd(cfg, []string{"claude"}, false, nil)().(engineDoneMsg)
	for _, ch := range after.changes {
		if ch.Action == engine.ActionConflict {
			t.Errorf("take-target left a lingering conflict for %s", ch.DestRel)
		}
	}
}

// TestConflictBridgeAbortUnwinds proves ctrl+c during a conflict cancels the
// operation cleanly: the engine unwinds (skips), the result screen appears, and
// the target is left untouched — no deadlock, no half-written state.
func TestConflictBridgeAbortUnwinds(t *testing.T) {
	cfg, dest := driftedConflictSetup(t)
	tm := driveToConflict(t, cfg)

	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC}) // abort the in-flight apply
	waitForText(t, tm, "cancelled")         // engine unwound → results screen (not "applied")
	quit(t, tm)

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "HAND EDIT") {
		t.Errorf("abort should leave the target untouched; file = %q", got)
	}
}

// TestConflictBridgeAbortPreservesWrittenBaselines is the drift-integrity proof:
// with one clean adapter (fresh create) and one drifted adapter (conflict),
// aborting at the modal must still let the engine finish and persist the clean
// adapter's baseline — the whole reason abort unwinds instead of killing. A
// follow-up dry-run then shows the clean adapter in-sync, not phantom drift.
func TestConflictBridgeAbortPreservesWrittenBaselines(t *testing.T) {
	home := isolatedHome(t)
	storeDir := filepath.Join(home, ".friday")
	mustWrite(t, filepath.Join(storeDir, "core.md"), "# Core\n\nCanonical content.\n")

	claude, ok1 := presets.Get("claude")
	codex, ok2 := presets.Get("codex")
	if !ok1 || !ok2 {
		t.Fatal("claude/codex preset missing")
	}
	cfg := config.NewDefault(config.ScopeUser, storeDir, home, map[string]*config.Adapter{
		"claude": claude.Adapter(),
		"codex":  codex.Adapter(),
	})

	// Seed + drift codex only; claude stays a fresh create.
	seed := pushCmd(cfg, []string{"codex"}, true, nil)().(engineDoneMsg)
	if seed.err != nil {
		t.Fatalf("seed codex: %v", seed.err)
	}
	codexDest := firstWrittenDest(seed.changes)
	if codexDest == "" {
		t.Fatal("seed wrote nothing for codex")
	}
	mustWrite(t, codexDest, "HAND EDIT — local drift\n")

	// Drive an apply over both; the one conflict (codex) raises the modal.
	m := newModel("test", []MenuEntry{{Name: "sync", Summary: "sync"}}, cfg, []string{"claude", "codex"}, nil)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(100, 30))
	m.send.set(tm.Send)
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitForText(t, tm, "fan out")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitForText(t, tm, "preview — nothing written yet")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitForText(t, tm, "conflict — both sides changed")

	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC}) // abort mid-apply
	waitForText(t, tm, "cancelled")
	quit(t, tm)

	// claude is pushed before codex — SelectAdapters preserves the requested
	// order (config.go:220), and the picker feeds adapters in installed-list
	// order ["claude","codex"] — so claude's clean create lands before the abort
	// at codex's conflict. The engine still reaches store.Save, so a re-plan sees
	// claude in-sync; had store.Save been skipped (process killed) it would
	// replan as drift.
	after := pushCmd(cfg, []string{"claude"}, false, nil)().(engineDoneMsg)
	if after.err != nil {
		t.Fatalf("re-plan claude: %v", after.err)
	}
	// The created file must re-plan as in-sync with nothing pending. (Rules with
	// no store source are legitimately missing-source and don't count.) A dropped
	// write or lost baseline would show up here as a Create/Update/Conflict.
	inSync := 0
	for _, ch := range after.changes {
		switch ch.Action {
		case engine.ActionCreate, engine.ActionUpdate, engine.ActionConflict:
			t.Errorf("claude has pending work after abort (write not durably applied): %s is %s", ch.DestRel, ch.Action)
		case engine.ActionInSync:
			inSync++
		}
	}
	if inSync == 0 {
		t.Error("expected claude's created file to be in-sync after abort; found none")
	}
}

// seedCopyAgent seeds a store copy-rule agent file, pushes it (recording target
// and canonical baselines), and returns cfg, the store path, and the target path.
func seedCopyAgent(t *testing.T) (cfg *config.Config, storeAgent, targetAgent string) {
	t.Helper()
	home := isolatedHome(t)
	storeDir := filepath.Join(home, ".friday")
	storeAgent = filepath.Join(storeDir, "agents", "foo.md")
	mustWrite(t, storeAgent, "v1\n")

	p, ok := presets.Get("claude")
	if !ok {
		t.Fatal("claude preset missing")
	}
	cfg = config.NewDefault(config.ScopeUser, storeDir, home,
		map[string]*config.Adapter{"claude": p.Adapter()})

	seed := pushCmd(cfg, []string{"claude"}, true, nil)().(engineDoneMsg)
	if seed.err != nil {
		t.Fatalf("seed: %v", seed.err)
	}
	for _, ch := range seed.changes {
		if strings.HasSuffix(filepath.ToSlash(ch.DestPath), "agents/foo.md") {
			targetAgent = ch.DestPath
		}
	}
	if targetAgent == "" {
		t.Fatal("seed did not write agents/foo.md to the target")
	}
	return cfg, storeAgent, targetAgent
}

// TestSyncAbortBetweenPhasesSkipsPush proves the between-phase abort guard: a
// cancel during the pull phase skips the push phase entirely, so the store's
// divergent content is never fanned out.
func TestSyncAbortBetweenPhasesSkipsPush(t *testing.T) {
	cfg, storeAgent, targetAgent := seedCopyAgent(t)

	// Store diverges → a push WOULD overwrite the target with "v2".
	mustWrite(t, storeAgent, "v2\n")

	br := newBridge(&sender{})
	close(br.abort) // simulate cancel during the pull phase
	done := syncCmd(cfg, []string{"claude"}, true, br)().(engineDoneMsg)
	if done.err != nil {
		t.Fatalf("sync: %v", done.err)
	}

	got, err := os.ReadFile(targetAgent)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "v2") {
		t.Errorf("push ran despite a pull-phase abort; target = %q", got)
	}
	for _, ch := range done.changes {
		if ch.Direction == engine.DirPush {
			t.Errorf("aborted sync produced a push change: %s", ch.DestRel)
		}
	}
}

// TestConflictBridgePullPhaseResolves is the headline Slice-5 proof: a pull-phase
// conflict (both the store and the target drifted on a copy-rule file) is
// resolved interactively through the same bridge. Keeping canonical on a pull
// means the target wins into the store.
func TestConflictBridgePullPhaseResolves(t *testing.T) {
	cfg, storeAgent, targetAgent := seedCopyAgent(t)

	// Both sides drift → the pull phase raises a conflict.
	mustWrite(t, targetAgent, "target edit\n")
	mustWrite(t, storeAgent, "store edit\n")

	m := newModel("test", []MenuEntry{{Name: "sync", Summary: "sync"}}, cfg, []string{"claude"}, nil)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(100, 30))
	m.send.set(tm.Send)
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitForText(t, tm, "fan out")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitForText(t, tm, "preview")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitForText(t, tm, "conflict — both sides changed")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")}) // keep canonical (= target, on a pull)
	waitForText(t, tm, "applied")
	quit(t, tm)

	got, err := os.ReadFile(storeAgent)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "target edit") {
		t.Errorf("pull-phase keep-canonical should adopt the target into the store; store = %q", got)
	}
}

// TestEngineAbortStopsApply guards the deep fix for "Ctrl-C doesn't stop a
// conflict-free apply": a signaled Abort makes the engine stop before writing.
func TestEngineAbortStopsApply(t *testing.T) {
	home := isolatedHome(t)
	storeDir := filepath.Join(home, ".friday")
	mustWrite(t, filepath.Join(storeDir, "core.md"), "# Core\n")
	p, _ := presets.Get("claude")
	cfg := config.NewDefault(config.ScopeUser, storeDir, home,
		map[string]*config.Adapter{"claude": p.Adapter()})

	abort := make(chan struct{})
	close(abort) // already cancelled before the first change
	if _, err := engine.Push(cfg, engine.Options{Abort: abort}); err != nil {
		t.Fatalf("push: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "CLAUDE.md")); err == nil {
		t.Error("apply wrote CLAUDE.md despite a pre-closed Abort")
	}
}

func waitForText(t *testing.T, tm *teatest.TestModel, sub string) {
	t.Helper()
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte(sub))
	}, teatest.WithDuration(5*time.Second))
}

func quit(t *testing.T, tm *teatest.TestModel) {
	t.Helper()
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}
