package cli

import (
	"flag"
	"os"
	"strings"

	"github.com/zhivko-kocev/friday/internal/initcmd"
	"github.com/zhivko-kocev/friday/internal/output"
)

type initOpts struct {
	fromGit  string
	scaffold bool
}

func initFlags(o *initOpts) *flag.FlagSet {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.StringVar(&o.fromGit, "from-git", "", "git remote URL to clone into ~/.friday (skips the prompt)")
	fs.BoolVar(&o.scaffold, "scaffold", false, "scaffold an empty store without prompting")
	return fs
}

// cmdInit either prompts for a remote URL (interactive default) or accepts
// one via --from-git (CI / scripts). --scaffold forces the empty-store path
// without any prompt at all.
func cmdInit(args []string) int {
	var o initOpts
	fs := initFlags(&o)
	// --remote was the earlier name for --from-git; keep it working but nudge.
	args = renameFlag(args, "remote", "from-git", "note: --remote is deprecated; use --from-git")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() > 0 {
		output.Err("friday init takes flags only — got positional arg %q", fs.Arg(0))
		return 1
	}
	if o.fromGit != "" && o.scaffold {
		output.Err("--from-git and --scaffold are mutually exclusive")
		return 1
	}

	var err error
	switch {
	case o.scaffold:
		err = initcmd.Run(strings.NewReader("\n"))
	case o.fromGit != "":
		err = initcmd.Run(strings.NewReader(o.fromGit + "\n"))
	default:
		err = initcmd.Run(os.Stdin)
	}
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	return 0
}
