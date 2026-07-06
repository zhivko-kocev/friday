package cli

import (
	"flag"

	"github.com/zhivko-kocev/friday/internal/output"
)

type syncOpts struct {
	dryRun, force, noInteractive bool
}

func syncFlags(o *syncOpts) *flag.FlagSet {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fs.BoolVar(&o.dryRun, "dry-run", false, "show both phases without writing")
	fs.BoolVar(&o.force, "force", false, "resolve every conflict in favor of the incoming side")
	fs.BoolVar(&o.noInteractive, "no-interactive", false, "skip prompts; conflicts are skipped")
	return fs
}

// cmdSync = pull then push over the same adapters, like `git pull` is fetch
// + merge. Edits are captured first; the push that follows sees the freshly
// pulled files as in-sync (pull updates both baselines) and only fans new
// content out to the other targets. All the conflict smarts live in the
// engine's baseline bookkeeping — sync itself is pure composition.
func cmdSync(args []string) int {
	var o syncOpts
	fs := syncFlags(&o)
	adapters, err := parseInterleaved(fs, args)
	if err != nil {
		return 1
	}

	cfg, err := loadUserOrDefault()
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	if len(adapters) > 0 {
		if _, err := cfg.SelectAdapters(adapters); err != nil {
			output.Err("%v", err)
			return 1
		}
	}

	output.Header("sync: pull")
	var pullCode int
	if o.noInteractive {
		pullCode = pullBatch(cfg, adapters, o.dryRun, o.force)
	} else {
		pullCode = pullPerAdapter(cfg, adapters, o.dryRun, o.force)
	}

	output.Header("sync: push")
	if o.dryRun {
		output.Dim("(dry-run: the push plan below is computed before any pull applies)")
	}
	pushCode := runPush(cfg, adapters, pushOpts{
		dryRun:        o.dryRun,
		force:         o.force,
		noInteractive: o.noInteractive,
	})

	return max(pullCode, pushCode)
}
