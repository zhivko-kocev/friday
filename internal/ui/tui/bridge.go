package tui

import (
	"sync/atomic"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/snapshot"
)

// sender carries the running program's Send into the resolver goroutine. It is
// set once, before any key is delivered — from Run (p.Send) in production or the
// teatest model (tm.Send) in tests — so the same bridge works under both. atomic
// so the resolver goroutine reads it without a race.
type sender struct {
	fn atomic.Pointer[func(tea.Msg)]
}

func (s *sender) set(f func(tea.Msg)) { s.fn.Store(&f) }

// send delivers msg to the event loop, reporting whether a loop is wired.
// Returns false only when no program has been wired yet (set never called), so
// the resolver degrades to report-only instead of blocking.
//
// Caveat: once a program IS wired this returns true even after it has quit
// (bubbletea's Send is a no-op post-quit, with no signal back). The resolver is
// safe only because the control room never quits while an apply is in flight —
// Ctrl-C cancels via abortApply, and no reachable screen offers quit while
// activeBridge != nil. Do NOT add a quit-during-apply path without also giving
// the bridge a done signal to unblock on, or the resolver will hang.
func (s *sender) send(msg tea.Msg) bool {
	if p := s.fn.Load(); p != nil {
		(*p)(msg)
		return true
	}
	return false
}

// bridge resolves engine conflicts through the control room's modal. The engine
// calls resolver() on its own Cmd goroutine; each conflict is shipped to the
// event loop as a needConflictMsg and the goroutine blocks on the reply the
// modal sends back. Closing abort unblocks a waiting resolver and makes every
// later conflict fast-skip, so the engine still unwinds to store.Save().
type bridge struct {
	send  *sender
	base  func(string) ([]byte, bool)
	abort chan struct{}
}

func newBridge(s *sender) *bridge {
	return &bridge{send: s, base: snapshot.BaseLookup(), abort: make(chan struct{})}
}

func (b *bridge) resolver() engine.ConflictResolver {
	return func(info engine.ConflictInfo) engine.Resolution {
		// Already cancelling → skip without touching the UI.
		select {
		case <-b.abort:
			return engine.Resolution{Choice: engine.ConflictSkip}
		default:
		}
		reply := make(chan engine.Resolution, 1)
		if !b.send.send(needConflictMsg{info: info, reply: reply}) {
			// No event loop wired (shouldn't happen on a real apply) → behave
			// like the non-interactive path and report the conflict.
			return engine.Resolution{Choice: engine.ConflictSkip}
		}
		select {
		case r := <-reply:
			// Both cases can be ready at once (a resolve keystroke buffered on
			// reply, then Ctrl-C closes abort). Prefer the cancellation so the
			// outcome is deterministic — skip, don't apply a choice the user is
			// trying to undo.
			select {
			case <-b.abort:
				return engine.Resolution{Choice: engine.ConflictSkip}
			default:
			}
			return r
		case <-b.abort:
			return engine.Resolution{Choice: engine.ConflictSkip}
		}
	}
}

// confirmer bridges engine's write-confirmation contract to the control room's
// modal, mirroring resolver(). The engine calls it on its apply goroutine for
// each drift-exempt (merge-json) write; the request is shipped to the event loop
// and the goroutine blocks on the modal's yes/no reply. The safe default is
// false — a cancel, an unwired loop, or a declined prompt all skip the write, so
// friday never installs hook commands unattended.
func (b *bridge) confirmer() engine.ConfirmWriter {
	return func(info engine.WriteConfirmInfo) bool {
		select {
		case <-b.abort:
			return false
		default:
		}
		reply := make(chan bool, 1)
		if !b.send.send(needConfirmMsg{info: info, reply: reply}) {
			return false
		}
		select {
		case ok := <-reply:
			// Prefer a concurrent cancel over a buffered approval, like resolver().
			select {
			case <-b.abort:
				return false
			default:
			}
			return ok
		case <-b.abort:
			return false
		}
	}
}

// needConflictMsg ships one conflict to the event loop; the modal sends the
// user's choice back down reply (buffered, so Update never blocks).
type needConflictMsg struct {
	info  engine.ConflictInfo
	reply chan engine.Resolution
}

// needConfirmMsg ships one drift-exempt write to the event loop; the modal
// sends the user's yes/no back down reply (buffered, so Update never blocks).
type needConfirmMsg struct {
	info  engine.WriteConfirmInfo
	reply chan bool
}

// confirmState is the modal's live confirmation plus the channel to answer on.
// body is rendered once when the request arrives so View doesn't rebuild it on
// every keystroke.
type confirmState struct {
	info  engine.WriteConfirmInfo
	reply chan bool
	body  string
}

// conflictState is the modal's live conflict plus the channel to answer on.
// body is the rendered modal, computed once when the conflict arrives so View
// doesn't re-run the O(N·M) line diff on every keystroke while the modal is up.
type conflictState struct {
	info  engine.ConflictInfo
	reply chan engine.Resolution
	body  string
}
