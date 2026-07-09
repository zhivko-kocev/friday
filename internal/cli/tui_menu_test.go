package cli

import (
	"slices"
	"testing"

	"github.com/zhivko-kocev/friday/internal/ui/tui"
)

// TestPorcelainMenu pins the control-room menu: the maintain-loop tiles are
// present, init is cold-start-only (not a tile), and the cut `list` is gone.
func TestPorcelainMenu(t *testing.T) {
	var names []string
	for _, e := range porcelainMenu() {
		names = append(names, e.Name)
	}
	want := []string{"setup", "sync", "status", "share", "discover"}
	if !slices.Equal(names, want) {
		t.Errorf("porcelainMenu = %v, want %v", names, want)
	}
	for _, gone := range []string{"init", "list", "ls"} {
		if slices.Contains(names, gone) {
			t.Errorf("menu should not include %q", gone)
		}
	}
}

// TestPorcelainTilesDispatch binds the derived menu to its dispatch: every tile
// porcelainMenu() produces must have a control-room branch, so a porcelain
// command added to commandTable() can't ship as a tile that does nothing.
func TestPorcelainTilesDispatch(t *testing.T) {
	for _, e := range porcelainMenu() {
		if !tui.HandlesCommand(e.Name) {
			t.Errorf("menu tile %q has no selectCommand dispatch — dead tile", e.Name)
		}
	}
}
