package cli

import (
	"context"
	"os"

	ucli "github.com/urfave/cli/v3"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/output"
	"github.com/zhivko-kocev/friday/internal/setupcmd"
)

// newPromoteCommand is setup's inverse: capture project-level agent config
// (e.g. a skill someone dropped into ./.claude/skills/) up into ~/.friday,
// and optionally hand it straight to `remote propose` for team review.
//
//	friday promote .claude/skills/new-skill --propose -m "add new-skill"
func newPromoteCommand() *ucli.Command {
	return &ucli.Command{
		Name:      "promote",
		Category:  "advanced",
		Usage:     "capture project agent config back into ~/.friday",
		ArgsUsage: "[filter...]",
		Flags: []ucli.Flag{
			&ucli.StringFlag{Name: "agent", Usage: "agent preset the project uses (skips the prompt)"},
			&ucli.BoolFlag{Name: "dry-run", Usage: "show what would be captured without writing"},
			&ucli.BoolFlag{Name: "force", Usage: "overwrite store files without prompting"},
			&ucli.BoolFlag{Name: "no-interactive", Usage: "don't prompt on conflicts; treat them as skip"},
			&ucli.BoolFlag{Name: "propose", Usage: "after capturing, push a branch + open an MR (requires -m)"},
			&ucli.StringFlag{Name: "m", Usage: "MR commit message (with --propose)"},
			&ucli.StringFlag{Name: "branch", Usage: "MR branch name (with --propose; default friday/propose-<timestamp>)"},
			&ucli.StringFlag{Name: "target", Usage: "MR target branch (with --propose; default: the remote's HEAD branch)"},
		},
		Action: func(_ context.Context, cmd *ucli.Command) error {
			filters := cmd.Args().Slice()
			propose := cmd.Bool("propose")
			message := cmd.String("m")
			dryRun := cmd.Bool("dry-run")
			noInteractive := cmd.Bool("no-interactive")

			if propose && message == "" {
				output.Err("--propose needs a message: friday promote --propose -m \"...\"")
				return ucli.Exit("", 1)
			}

			cwd, err := os.Getwd()
			if err != nil {
				output.Err("%v", err)
				return ucli.Exit("", 1)
			}

			var resolver engine.ConflictResolver
			if !noInteractive {
				resolver = interactiveResolver()
			}
			changes, err := setupcmd.Promote(os.Stdin, cwd, setupcmd.PromoteOptions{
				Agent:       cmd.String("agent"),
				DryRun:      dryRun,
				Force:       cmd.Bool("force"),
				Interactive: !noInteractive,
				Filters:     filters,
			}, resolver)
			if err != nil {
				output.Err("%v", err)
				return ucli.Exit("", 1)
			}
			if !dryRun {
				recordSnapshot(changes)
			}
			report(changes, false, dryRun)

			captured := 0
			for _, ch := range changes {
				if ch.Action == engine.ActionCreate || ch.Action == engine.ActionUpdate {
					captured++
				}
			}
			if dryRun || !propose {
				if captured > 0 && !dryRun && !propose {
					output.Dim("hint: `friday remote propose -m \"...\"` sends this for team review")
				}
				return exitErr(changes)
			}
			if captured == 0 {
				output.Skip("nothing new captured — skipping the MR")
				return exitErr(changes)
			}

			storeDir, err := config.UserStoreDir()
			if err != nil {
				output.Err("%v", err)
				return ucli.Exit("", 1)
			}
			if code := max(exitCode(changes), proposeStore(storeDir, cmd.String("branch"), cmd.String("target"), message)); code != 0 {
				return ucli.Exit("", code)
			}
			return nil
		},
	}
}
