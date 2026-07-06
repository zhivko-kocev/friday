package cli

import (
	"flag"

	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/output"
)

type statusOpts struct {
	asJSON bool
}

func statusFlags(o *statusOpts) *flag.FlagSet {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.BoolVar(&o.asJSON, "json", false, "machine-readable output")
	return fs
}

// cmdStatus — user-level diff (no writes). --json emits a machine-readable
// blob for CI; its exit code matches push's (2 on any conflict) so pipelines
// can gate on drift.
func cmdStatus(args []string) int {
	var o statusOpts
	fs := statusFlags(&o)
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
	if o.asJSON {
		if err := printStatusJSON(cfg, changes); err != nil {
			output.Err("%v", err)
			return 1
		}
		return exitCode(changes)
	}
	output.Header("Friday Status (user)")
	output.Dim("store: %s", cfg.StoreDir)
	for _, name := range cfg.AdapterNames() {
		abs, _ := cfg.AdapterTargetAbs(name)
		if dirExists(abs) {
			output.OK("%-10s [installed]  %s", name, abs)
		} else {
			output.Skip("%-10s [missing]    %s", name, abs)
		}
	}
	report(changes, false, true)
	return exitCode(changes)
}
