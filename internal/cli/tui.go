package cli

import (
	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/initcmd"
	"github.com/zhivko-kocev/friday/internal/ui/tui"
)

// launchTUI opens the interactive control room. It is reached only from bare
// `friday` on a real terminal (see Run); every flag/subcommand and every
// non-interactive invocation keeps the plain path. The porcelain command names
// and summaries come straight from commandTable so the menu can't drift from
// the CLI. When ~/.friday is absent or empty the control room opens on its
// cold-start input; otherwise the store is loaded here (loadUserOrDefault's
// policy) and reload lets cold-start re-load it after scaffolding/cloning.
func launchTUI(version string) int {
	menu := porcelainMenu()
	reload := func() (*config.Config, []string, error) {
		cfg, err := loadUserOrDefault()
		if err != nil {
			return nil, nil, err
		}
		return cfg, installedAdapters(cfg), nil
	}

	coldStart := storeAbsentOrEmpty()
	var (
		cfg       *config.Config
		installed []string
		err       error
	)
	if !coldStart {
		cfg, installed, err = reload()
	}
	return tui.Run(version, menu, cfg, installed, err, coldStart, reload)
}

// porcelainMenu builds the control-room menu from the command table's porcelain
// tier, minus init (cold-start only), plus the synthetic discover tile. Sourcing
// names/summaries from commandTable keeps the menu from drifting; discover is a
// deliberate synthetic entry surfacing `pull --discover` as a first-class action.
func porcelainMenu() []tui.MenuEntry {
	var menu []tui.MenuEntry
	for _, c := range commandTable() {
		if c.advanced || c.summary == "" || c.name == "init" {
			continue
		}
		menu = append(menu, tui.MenuEntry{Name: c.name, Summary: c.summary})
	}
	return append(menu, tui.MenuEntry{Name: "discover", Summary: "import agent files not yet in your store"})
}

// storeAbsentOrEmpty reports whether ~/.friday needs initializing — missing or
// empty — delegating to initcmd.NeedsInit so the cold-start gate and `friday
// init`'s overwrite guard share one definition. Any read error falls through to
// the normal load path, which surfaces it on the error screen.
func storeAbsentOrEmpty() bool {
	storeDir, err := config.UserStoreDir()
	if err != nil {
		return false
	}
	initable, err := initcmd.NeedsInit(storeDir)
	if err != nil {
		return false
	}
	return initable
}
