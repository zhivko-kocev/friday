package cli

import (
	"context"
	"os"

	ucli "github.com/urfave/cli/v3"

	"github.com/zhivko-kocev/friday/internal/initcmd"
	"github.com/zhivko-kocev/friday/internal/output"
)

// newInitCommand either prompts for a remote URL (interactive default) or
// accepts one via --from-git (CI / scripts). --scaffold forces the
// empty-store path without any prompt at all.
func newInitCommand() *ucli.Command {
	return &ucli.Command{
		Name:     "init",
		Category: "porcelain",
		Usage:    "clone or scaffold your ~/.friday store",
		Flags: []ucli.Flag{
			&ucli.StringFlag{Name: "from-git", Usage: "git remote URL to clone into ~/.friday (skips the prompt)"},
			&ucli.BoolFlag{Name: "scaffold", Usage: "scaffold an empty store without prompting"},
		},
		Action: func(_ context.Context, cmd *ucli.Command) error {
			if cmd.Args().Len() > 0 {
				output.Err("friday init takes flags only — got positional arg %q", cmd.Args().First())
				return ucli.Exit("", 1)
			}
			fromGit := cmd.String("from-git")
			scaffold := cmd.Bool("scaffold")
			if fromGit != "" && scaffold {
				output.Err("--from-git and --scaffold are mutually exclusive")
				return ucli.Exit("", 1)
			}

			var err error
			switch {
			case scaffold:
				err = initcmd.RunWithURL("")
			case fromGit != "":
				err = initcmd.RunWithURL(fromGit)
			default:
				err = initcmd.Run(os.Stdin)
			}
			if err != nil {
				output.Err("%v", err)
				return ucli.Exit("", 1)
			}
			return nil
		},
	}
}
