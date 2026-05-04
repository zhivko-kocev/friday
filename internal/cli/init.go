package cli

import (
	"flag"

	"github.com/zhivko-kocev/friday/internal/initcmd"
	"github.com/zhivko-kocev/friday/internal/output"
)

// cmdInit — scaffold or git-clone the user store.
func cmdInit(args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fromGit := fs.String("from-git", "", "clone the user store from this git repo URL")
	remote := fs.String("remote", "", "after scaffold, register this URL as `origin`")
	noGit := fs.Bool("no-git", false, "skip the `git init` step on scaffold")
	force := fs.Bool("force", false, "overwrite an existing user store (refuses if a .git/ is present)")
	reallyForce := fs.Bool("really-force", false, "allow --force to wipe a store that contains a .git/ dir")
	var adapters multiFlag
	fs.Var(&adapters, "adapters", "comma- or space-separated preset list (claude,cursor,opencode,copilot)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	adapters = append(adapters, fs.Args()...)
	if err := initcmd.Run(initcmd.Options{
		FromGit:     *fromGit,
		Remote:      *remote,
		Adapters:    adapters,
		NoGit:       *noGit,
		Force:       *force,
		ReallyForce: *reallyForce,
	}); err != nil {
		output.Err("%v", err)
		return 1
	}
	return 0
}
