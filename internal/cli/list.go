package cli

import (
	"os"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/output"
)

// cmdList — show every adapter configured in friday.yaml plus whether its
// target dir is present on this machine. Argument is accepted but ignored
// (back-compat for `friday list adapters`); this UX is deliberately one view.
func cmdList(args []string) int {
	if len(args) > 0 && args[0] != "adapters" && args[0] != "all" {
		output.Err("unknown list target %q (want: adapters)", args[0])
		return 1
	}
	return printAdapters()
}

func printAdapters() int {
	output.Header("Configured adapters")
	exists, err := config.StoreExists()
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	if !exists {
		output.Dim("no user store yet — run `friday init`")
		return 0
	}
	cfg, err := loadUserOrDefault()
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	if len(cfg.Adapters) == 0 {
		output.Dim("(no adapters in friday.yaml — push has nothing to do)")
		return 0
	}
	for _, name := range cfg.AdapterNames() {
		ad := cfg.Adapters[name]
		abs, _ := cfg.AdapterTargetAbs(name)
		if dirExists(abs) {
			output.OK("%-12s target: %s", name, abs)
		} else {
			output.Skip("%-12s target: %s", name, abs)
		}
		for _, r := range ad.Rules {
			strat := r.Strategy
			if strat == "" {
				strat = "copy"
			}
			output.Dim("%s  %v → %s", strat, []string(r.From), r.To)
		}
	}
	return 0
}

// dirExists reports whether path is an existing directory. Used to flag an
// adapter as "installed" — i.e. friday push (no args) will target it.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
