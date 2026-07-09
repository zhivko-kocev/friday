package cli

import (
	"flag"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/output"
	"github.com/zhivko-kocev/friday/internal/snapshot"
)

type pushOpts struct {
	dryRun, force, noInteractive, showDiff bool
	only                                   string
}

func pushFlags(o *pushOpts) *flag.FlagSet {
	fs := flag.NewFlagSet("push", flag.ContinueOnError)
	fs.BoolVar(&o.dryRun, "dry-run", false, "show what would change without writing")
	fs.BoolVar(&o.force, "force", false, "overwrite without prompting on drift")
	fs.BoolVar(&o.noInteractive, "no-interactive", false, "don't prompt; treat conflicts as skip")
	fs.BoolVar(&o.showDiff, "diff", false, "show line diff for each change")
	fs.StringVar(&o.only, "only", "", "push only changes sourced from files matching this store-relative glob")
	return fs
}

// cmdPush — apply rules from the user store into installed agent dirs.
func cmdPush(args []string) int {
	var o pushOpts
	fs := pushFlags(&o)
	adapters, err := parseInterleaved(fs, args)
	if err != nil {
		return 1
	}

	cfg, err := loadUserOrDefault()
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	return runPush(cfg, adapters, o)
}

// runPush executes the push phase against the given adapters (empty = every
// installed one). Shared by push and the push half of sync.
func runPush(cfg *config.Config, adapters []string, o pushOpts) int {
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
		DryRun:   o.dryRun,
		Force:    o.force,
		ShowDiff: o.showDiff,
	}
	if o.only != "" {
		opts.Only = []string{o.only}
	}
	if !o.noInteractive {
		opts.OnConflict = interactiveResolver()
		opts.BaseLookup = baseLookup()
	}

	changes, err := engine.Push(cfg, opts)
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	if !o.dryRun {
		recordSnapshot(changes)
	}
	report(changes, o.showDiff, o.dryRun)
	return exitCode(changes)
}

// recordSnapshot journals what a command wrote so `friday rollback` can undo
// it. Every write-capable command (push, pull, sync, setup, promote, import,
// compile) records one. Best-effort: a failed snapshot warns but never fails
// the writes that already succeeded.
func recordSnapshot(changes []engine.Change) {
	writes := engine.SnapshotWrites(changes)
	if len(writes) == 0 {
		return
	}
	dir, err := snapshot.Dir()
	if err != nil {
		output.Warn("snapshot skipped: %v", err)
		return
	}
	snap, err := snapshot.Record(dir, writes)
	if err != nil {
		output.Warn("snapshot failed: %v", err)
		return
	}
	output.Dim("snapshot %s recorded (`friday rollback` restores it)", snap.ID)
}
