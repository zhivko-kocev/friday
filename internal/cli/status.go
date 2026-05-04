package cli

import (
	"flag"

	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/output"
)

// cmdStatus — user-level diff (no writes).
func cmdStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	cfg, err := loadUserOrDefault()
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	if len(fs.Args()) > 0 {
		if _, err := cfg.SelectAdapters(fs.Args()); err != nil {
			output.Err("%v", err)
			return 1
		}
	}
	changes, err := engine.Push(cfg, engine.Options{
		Adapters: fs.Args(),
		DryRun:   true,
	})
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	output.Header("Friday Status (user)")
	output.Dim("store: %s", cfg.StoreDir)
	report(changes, false, true)
	return 0
}
