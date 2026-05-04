package cli

import (
	"flag"

	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/output"
)

// cmdPull — user-level only. Project pull doesn't make sense (no local store).
func cmdPull(args []string) int {
	fs := flag.NewFlagSet("pull", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "show what would change without writing")
	force := fs.Bool("force", false, "overwrite store without prompting on drift")
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
	opts := engine.Options{
		Adapters: fs.Args(),
		DryRun:   *dryRun,
		Force:    *force,
		ShowDiff: *showDiff,
	}
	if !*noInteractive {
		opts.OnConflict = interactiveResolver()
	}
	changes, err := engine.Pull(cfg, opts)
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	report(changes, *showDiff, *dryRun)
	return exitCode(changes)
}
