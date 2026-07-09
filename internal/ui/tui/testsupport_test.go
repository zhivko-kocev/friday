package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/engine"
)

// pushCmd fans the store out to the named adapters (apply=false previews). It is
// a test-only seed helper: the control room reaches push only through sync
// (syncCmd), so there is no production pushCmd. Tests use it to establish a
// baseline before exercising sync/discover/conflict flows.
func pushCmd(cfg *config.Config, adapters []string, apply bool, br *bridge) tea.Cmd {
	return func() tea.Msg {
		var warnings []string
		opts := engine.Options{Adapters: adapters, DryRun: !apply}
		if apply {
			opts = applyOpts(opts, br, &warnings)
		}
		ch, err := engine.Push(cfg, opts)
		if err == nil && apply {
			warnings = append(warnings, recordSnapshot(ch)...)
		}
		return engineDoneMsg{changes: ch, err: err, applied: apply, warnings: warnings}
	}
}
