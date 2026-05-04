package cli

import (
	"fmt"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/output"
	"github.com/zhivko-kocev/friday/internal/presets"
)

// cmdList — show available presets and (if there's a manifest) configured
// adapters. Subcommands: presets | adapters | (default: both).
func cmdList(args []string) int {
	what := ""
	if len(args) > 0 {
		what = args[0]
	}
	switch what {
	case "presets":
		printPresets()
	case "adapters":
		return printAdapters()
	case "", "all":
		printPresets()
		fmt.Println()
		return printAdapters()
	default:
		output.Err("unknown list target %q (want: presets, adapters)", what)
		return 1
	}
	return 0
}

func printPresets() {
	output.Header("Available presets")
	for _, n := range presets.Names() {
		p, _ := presets.Get(n)
		fmt.Printf("  %-10s %s\n", n, p.Comment)
	}
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
		output.Dim("(none configured — push falls back to all built-in presets)")
		return 0
	}
	for _, name := range cfg.AdapterNames() {
		ad := cfg.Adapters[name]
		abs, _ := cfg.AdapterTargetAbs(name)
		fmt.Printf("  %-10s target: %s\n", name, abs)
		for _, r := range ad.Rules {
			strat := r.Strategy
			if strat == "" {
				strat = "copy"
			}
			fmt.Printf("             %s  %v → %s\n", strat, []string(r.From), r.To)
		}
	}
	return 0
}
