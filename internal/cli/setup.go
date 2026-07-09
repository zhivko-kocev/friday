package cli

import (
	"flag"
	"os"

	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/output"
	"github.com/zhivko-kocev/friday/internal/setupcmd"
)

type setupOpts struct {
	agent                        string
	dryRun, force, noInteractive bool
}

func setupFlags(o *setupOpts) *flag.FlagSet {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.StringVar(&o.agent, "agent", "", "agent preset to set up (skips the prompt)")
	fs.BoolVar(&o.dryRun, "dry-run", false, "show what would change without writing")
	fs.BoolVar(&o.force, "force", false, "overwrite without prompting on drift")
	fs.BoolVar(&o.noInteractive, "no-interactive", false, "don't prompt on drift; treat conflicts as skip")
	return fs
}

// cmdSetup — interactively apply selected store knowledge into the current
// project's agent config (git-tracked by the project, no .friday folder).
func cmdSetup(args []string) int {
	var o setupOpts
	fs := setupFlags(&o)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() > 0 {
		output.Err("friday setup takes no positional arguments (use --agent NAME)")
		return 1
	}

	cwd, err := os.Getwd()
	if err != nil {
		output.Err("%v", err)
		return 1
	}

	var resolver engine.ConflictResolver
	var confirm engine.ConfirmWriter
	if !o.noInteractive {
		resolver = interactiveResolver()
		confirm = hookWriteConfirmer()
	}
	changes, err := setupcmd.Run(os.Stdin, cwd, setupcmd.Options{
		Agent:       o.agent,
		DryRun:      o.dryRun,
		Force:       o.force,
		Interactive: !o.noInteractive,
	}, resolver, confirm)
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
