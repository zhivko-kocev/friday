package cli

import (
	"flag"
	"fmt"
	"path/filepath"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/output"
)

type importOpts struct {
	dryRun, force, noInteractive bool
}

func importFlags(o *importOpts) *flag.FlagSet {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	fs.BoolVar(&o.dryRun, "dry-run", false, "show what would be captured without writing")
	fs.BoolVar(&o.force, "force", false, "overwrite store files without prompting")
	fs.BoolVar(&o.noInteractive, "no-interactive", false, "don't prompt; treat conflicts as skip")
	return fs
}

// cmdImport bootstraps (or enriches) ~/.friday from an existing agent
// installation — the onboarding path for users with a working ~/.claude who
// shouldn't have to re-author everything to try friday.
func cmdImport(args []string) int {
	var o importOpts
	fs := importFlags(&o)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return 1
	}
	if len(pos) != 1 {
		output.Err("usage: friday import <adapter-name-or-target-dir>")
		return 1
	}

	cfg, err := loadUserOrDefault()
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	adapter, err := resolveAdapterArg(cfg, pos[0])
	if err != nil {
		output.Err("%v", err)
		return 1
	}

	opts := engine.Options{
		Adapters: []string{adapter},
		DryRun:   o.dryRun,
		Force:    o.force,
	}
	if !o.noInteractive {
		opts.OnConflict = interactiveResolver()
		opts.BaseLookup = baseLookup()
	}
	changes, err := engine.Import(cfg, opts)
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	if !o.dryRun {
		recordSnapshot(changes)
	}
	report(changes, false, o.dryRun)
	return exitCode(changes)
}

// resolveAdapterArg accepts either an adapter name or a path to an adapter's
// target dir and returns the adapter name.
func resolveAdapterArg(cfg *config.Config, arg string) (string, error) {
	if _, ok := cfg.Adapters[arg]; ok {
		return arg, nil
	}
	abs, err := filepath.Abs(arg)
	if err != nil {
		return "", err
	}
	for _, name := range cfg.AdapterNames() {
		target, err := cfg.AdapterTargetAbs(name)
		if err != nil {
			continue
		}
		if samePath(target, abs) {
			return name, nil
		}
	}
	return "", fmt.Errorf("%q is neither an adapter name nor a known target dir (defined: %v)", arg, cfg.AdapterNames())
}
