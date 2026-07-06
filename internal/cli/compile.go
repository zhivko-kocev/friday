package cli

import (
	"flag"

	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/output"
)

type compileOpts struct {
	from, to                                 string
	dryRun, force, allowLossy, noInteractive bool
}

func compileFlags(o *compileOpts) *flag.FlagSet {
	fs := flag.NewFlagSet("compile", flag.ContinueOnError)
	fs.StringVar(&o.from, "from", "", "source adapter (reads its installed target dir)")
	fs.StringVar(&o.to, "to", "", "destination adapter (writes its target dir)")
	fs.BoolVar(&o.dryRun, "dry-run", false, "show what would be written without writing")
	fs.BoolVar(&o.force, "force", false, "overwrite drifted destination files without prompting")
	fs.BoolVar(&o.allowLossy, "allow-lossy", false, "proceed even when the conversion drops files")
	fs.BoolVar(&o.noInteractive, "no-interactive", false, "don't prompt; treat conflicts as skip")
	return fs
}

// cmdCompile converts one agent's installed config into another's format via
// a throwaway store — no round trip through ~/.friday. Useful for migrations
// ("what would my working .claude look like as opencode?") and for sharing a
// config flavour without adopting friday's canonical layout.
func cmdCompile(args []string) int {
	var o compileOpts
	fs := compileFlags(&o)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if o.from == "" || o.to == "" {
		output.Err("usage: friday compile --from <adapter> --to <adapter> [--allow-lossy]")
		return 1
	}

	cfg, err := loadUserOrDefault()
	if err != nil {
		output.Err("%v", err)
		return 1
	}

	opts := engine.Options{DryRun: o.dryRun, Force: o.force}
	if !o.noInteractive {
		opts.OnConflict = interactiveResolver()
	}
	res, err := engine.Compile(cfg, o.from, o.to, o.allowLossy, opts)
	if err != nil {
		output.Err("%v", err)
		return 1
	}

	if len(res.Lossy) > 0 {
		output.Warn("lossy conversion %s → %s:", o.from, o.to)
		for _, l := range res.Lossy {
			output.Skip("%s", l)
		}
	}
	if res.Emitted == nil {
		output.Err("nothing written — re-run with --allow-lossy to accept the losses above")
		return 2
	}
	if !o.dryRun {
		recordSnapshot(res.Emitted)
	}
	report(res.Emitted, false, o.dryRun)
	return exitCode(res.Emitted)
}
