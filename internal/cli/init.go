package cli

import (
	"flag"
	"os"
	"strings"

	"github.com/zhivko-kocev/friday/internal/initcmd"
	"github.com/zhivko-kocev/friday/internal/output"
)

// cmdInit either prompts for a remote URL (interactive default) or accepts
// one via --remote (CI / scripts). --scaffold forces the empty-store path
// without any prompt at all.
func cmdInit(args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	remote := fs.String("remote", "", "git remote URL to clone into ~/.friday (skips the prompt)")
	scaffold := fs.Bool("scaffold", false, "scaffold an empty store without prompting")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() > 0 {
		output.Err("friday init takes flags only — got positional arg %q", fs.Arg(0))
		return 1
	}
	if *remote != "" && *scaffold {
		output.Err("--remote and --scaffold are mutually exclusive")
		return 1
	}

	var err error
	switch {
	case *scaffold:
		err = initcmd.Run(strings.NewReader("\n"))
	case *remote != "":
		err = initcmd.Run(strings.NewReader(*remote + "\n"))
	default:
		err = initcmd.Run(os.Stdin)
	}
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	return 0
}
