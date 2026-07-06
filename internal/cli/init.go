package cli

import (
	"flag"
	"os"
	"strings"

	"github.com/zhivko-kocev/friday/internal/initcmd"
	"github.com/zhivko-kocev/friday/internal/output"
)

type initOpts struct {
	remote, fromGit string
	scaffold        bool
}

func initFlags(o *initOpts) *flag.FlagSet {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.StringVar(&o.remote, "remote", "", "git remote URL to clone into ~/.friday (skips the prompt)")
	fs.StringVar(&o.fromGit, "from-git", "", "alias of --remote")
	fs.BoolVar(&o.scaffold, "scaffold", false, "scaffold an empty store without prompting")
	return fs
}

// cmdInit either prompts for a remote URL (interactive default) or accepts
// one via --remote / --from-git (CI / scripts). --scaffold forces the
// empty-store path without any prompt at all.
func cmdInit(args []string) int {
	var o initOpts
	fs := initFlags(&o)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() > 0 {
		output.Err("friday init takes flags only — got positional arg %q", fs.Arg(0))
		return 1
	}
	if o.remote != "" && o.fromGit != "" && o.remote != o.fromGit {
		output.Err("--remote and --from-git are aliases — pass one URL, not two")
		return 1
	}
	if o.remote == "" {
		o.remote = o.fromGit
	}
	if o.remote != "" && o.scaffold {
		output.Err("--remote and --scaffold are mutually exclusive")
		return 1
	}

	var err error
	switch {
	case o.scaffold:
		err = initcmd.Run(strings.NewReader("\n"))
	case o.remote != "":
		err = initcmd.Run(strings.NewReader(o.remote + "\n"))
	default:
		err = initcmd.Run(os.Stdin)
	}
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	return 0
}
