package cli

import (
	"flag"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/output"
)

// cmdPush — apply rules from the user store, OR transiently from a repo URL.
func cmdPush(args []string) int {
	fs := flag.NewFlagSet("push", flag.ContinueOnError)
	fromGit := fs.String("from-git", "", "transient project push from a git repo URL or local path")
	dryRun := fs.Bool("dry-run", false, "show what would change without writing")
	force := fs.Bool("force", false, "overwrite without prompting on drift")
	noInteractive := fs.Bool("no-interactive", false, "don't prompt; treat conflicts as skip")
	showDiff := fs.Bool("diff", false, "show line diff for each change")
	if err := fs.Parse(args); err != nil {
		return 1
	}

	var cfg *config.Config
	var cleanup func()
	if *fromGit != "" {
		c, cl, err := loadProjectFromURL(*fromGit)
		if err != nil {
			output.Err("%v", err)
			return 1
		}
		cfg, cleanup = c, cl
		defer cleanup()
		output.Info("project push from %s → %s", *fromGit, cfg.TargetRoot)
	} else {
		c, err := loadUserOrDefault()
		if err != nil {
			output.Err("%v", err)
			return 1
		}
		cfg = c
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

	changes, err := engine.Push(cfg, opts)
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	report(changes, *showDiff, *dryRun)
	return exitCode(changes)
}
