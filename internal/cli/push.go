package cli

import (
	"flag"

	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/output"
)

// cmdPush — apply rules from the user store into installed agent dirs.
func cmdPush(args []string) int {
	fs := flag.NewFlagSet("push", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "show what would change without writing")
	force := fs.Bool("force", false, "overwrite without prompting on drift")
	noInteractive := fs.Bool("no-interactive", false, "don't prompt; treat conflicts as skip")
	showDiff := fs.Bool("diff", false, "show line diff for each change")
	if err := fs.Parse(args); err != nil {
		return 1
	}

	cfg, err := loadUserOrDefault()
	if err != nil {
		output.Err("%v", err)
		return 1
	}

	adapters := fs.Args()
	if len(adapters) == 0 {
		// No args → only target agents that are actually installed on this
		// machine (target dir exists). Explicit names (`friday push claude`)
		// bypass this filter so first-time setup still works.
		adapters = installedAdapters(cfg)
		if len(adapters) == 0 {
			output.Warn("no installed agents detected — nothing to push")
			output.Dim("hint: name an adapter explicitly (e.g. `friday push claude`) to bootstrap its target dir")
			return 0
		}
		output.Dim("pushing to installed agents: %v", adapters)
	}

	opts := engine.Options{
		Adapters: adapters,
		DryRun:   *dryRun,
		Force:    *force,
		ShowDiff: *showDiff,
	}
	if !*noInteractive {
		opts.OnConflict = interactiveResolver()
	}

	changes, err := engine.Push(cfg, opts)
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	report(changes, *showDiff, *dryRun)
	return exitCode(changes)
}
