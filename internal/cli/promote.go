package cli

import (
	"flag"
	"os"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/output"
	"github.com/zhivko-kocev/friday/internal/setupcmd"
)

type promoteOpts struct {
	agent, message, branch, target        string
	dryRun, force, noInteractive, propose bool
}

func promoteFlags(o *promoteOpts) *flag.FlagSet {
	fs := flag.NewFlagSet("promote", flag.ContinueOnError)
	fs.StringVar(&o.agent, "agent", "", "agent preset the project uses (skips the prompt)")
	fs.BoolVar(&o.dryRun, "dry-run", false, "show what would be captured without writing")
	fs.BoolVar(&o.force, "force", false, "overwrite store files without prompting")
	fs.BoolVar(&o.noInteractive, "no-interactive", false, "don't prompt on conflicts; treat them as skip")
	fs.BoolVar(&o.propose, "propose", false, "after capturing, push a branch + open an MR (requires -m)")
	fs.StringVar(&o.message, "m", "", "MR commit message (with --propose)")
	fs.StringVar(&o.branch, "branch", "", "MR branch name (with --propose; default friday/propose-<timestamp>)")
	fs.StringVar(&o.target, "target", "", "MR target branch (with --propose; default: the remote's HEAD branch)")
	return fs
}

// cmdPromote is setup's inverse: capture project-level agent config (e.g. a
// skill someone dropped into ./.claude/skills/) up into ~/.friday, and
// optionally hand it straight to `remote propose` for team review.
//
//	friday promote .claude/skills/new-skill --propose -m "add new-skill"
func cmdPromote(args []string) int {
	var o promoteOpts
	fs := promoteFlags(&o)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if o.propose && o.message == "" {
		output.Err("--propose needs a message: friday promote --propose -m \"...\"")
		return 1
	}

	cwd, err := os.Getwd()
	if err != nil {
		output.Err("%v", err)
		return 1
	}

	var resolver engine.ConflictResolver
	if !o.noInteractive {
		resolver = interactiveResolver()
	}
	changes, err := setupcmd.Promote(os.Stdin, cwd, setupcmd.PromoteOptions{
		Agent:   o.agent,
		DryRun:  o.dryRun,
		Force:   o.force,
		Filters: fs.Args(),
	}, resolver)
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	report(changes, false, o.dryRun)

	captured := 0
	for _, ch := range changes {
		if ch.Action == engine.ActionCreate || ch.Action == engine.ActionUpdate {
			captured++
		}
	}
	if o.dryRun || !o.propose {
		if captured > 0 && !o.dryRun && !o.propose {
			output.Dim("hint: `friday remote propose -m \"...\"` sends this for team review")
		}
		return exitCode(changes)
	}
	if captured == 0 {
		output.Skip("nothing new captured — skipping the MR")
		return exitCode(changes)
	}

	storeDir, err := config.UserStoreDir()
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	return max(exitCode(changes), proposeStore(storeDir, o.branch, o.target, o.message))
}
