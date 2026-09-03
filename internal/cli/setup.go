package cli

import (
	"context"
	"os"

	ucli "github.com/urfave/cli/v3"

	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/output"
	"github.com/zhivko-kocev/friday/internal/setupcmd"
)

// newSetupCommand interactively applies selected store knowledge into the
// current project's agent config (git-tracked by the project, no .friday
// folder).
func newSetupCommand() *ucli.Command {
	return &ucli.Command{
		Name:     "setup",
		Category: "porcelain",
		Usage:    "add friday knowledge to the current project",
		Flags: []ucli.Flag{
			&ucli.StringFlag{Name: "agent", Usage: "agent preset to set up (skips the prompt)"},
			&ucli.BoolFlag{Name: "dry-run", Usage: "show what would change without writing"},
			&ucli.BoolFlag{Name: "force", Usage: "overwrite without prompting on drift"},
			&ucli.BoolFlag{Name: "no-interactive", Usage: "don't prompt on drift; treat conflicts as skip"},
		},
		Action: func(_ context.Context, cmd *ucli.Command) error {
			if cmd.Args().Len() > 0 {
				output.Err("friday setup takes no positional arguments (use --agent NAME)")
				return ucli.Exit("", 1)
			}
			dryRun := cmd.Bool("dry-run")
			noInteractive := cmd.Bool("no-interactive")

			cwd, err := os.Getwd()
			if err != nil {
				output.Err("%v", err)
				return ucli.Exit("", 1)
			}

			var resolver engine.ConflictResolver
			var confirm engine.ConfirmWriter
			if !noInteractive {
				resolver = interactiveResolver()
				confirm = hookWriteConfirmer()
			}
			changes, err := setupcmd.Run(os.Stdin, cwd, setupcmd.Options{
				Agent:       cmd.String("agent"),
				DryRun:      dryRun,
				Force:       cmd.Bool("force"),
				Interactive: !noInteractive,
			}, resolver, confirm)
			if err != nil {
				output.Err("%v", err)
				return ucli.Exit("", 1)
			}
			if !dryRun {
				recordSnapshot(changes)
			}
			report(changes, false, dryRun)
			return exitErr(changes)
		},
	}
}
