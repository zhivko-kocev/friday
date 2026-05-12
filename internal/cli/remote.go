package cli

import (
	"errors"
	"flag"
	"fmt"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/git"
	"github.com/zhivko-kocev/friday/internal/output"
)

// cmdRemote — git operations on the user store.
func cmdRemote(args []string) int {
	if len(args) == 0 {
		output.Err("usage: friday remote <pull|push|status>")
		return 1
	}
	storeDir, err := config.UserStoreDir()
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	if !git.Available() {
		output.Err("git not found in PATH")
		return 1
	}
	if !git.IsRepo(storeDir) {
		output.Err("user store at %s is not a git repo", storeDir)
		output.Dim("hint: run `friday init` and provide a remote URL to set up a git-backed store")
		return 1
	}

	switch args[0] {
	case "pull":
		if err := git.Pull(storeDir); err != nil {
			output.Err("%v", err)
			return 1
		}
		output.OK("pulled latest changes into %s", storeDir)
		return 0

	case "push":
		fs := flag.NewFlagSet("remote push", flag.ContinueOnError)
		// Two flags pointing at the same string: -m is short, --message is conventional.
		// First-set-wins: if both are provided, -m takes priority.
		short := fs.String("m", "", "commit message (required)")
		long := fs.String("message", "", "alias for -m")
		if err := fs.Parse(args[1:]); err != nil {
			return 1
		}
		msg := *short
		if msg == "" {
			msg = *long
		}
		if msg == "" {
			output.Err("commit message required: friday remote push -m \"...\"")
			return 1
		}
		if err := git.StageCommitPush(storeDir, msg); err != nil {
			if errors.Is(err, git.ErrNothingToCommit) {
				output.Skip("nothing to commit in %s", storeDir)
				return 0
			}
			output.Err("%v", err)
			return 1
		}
		output.OK("pushed %s", storeDir)
		return 0

	case "status":
		out, err := git.Status(storeDir)
		if err != nil {
			output.Err("%v", err)
			return 1
		}
		output.Header("Remote status (" + storeDir + ")")
		if out == "" {
			output.Dim("(working tree clean)")
		} else {
			fmt.Println(out)
		}
		return 0

	default:
		output.Err("unknown remote subcommand %q (want: pull, push, status)", args[0])
		return 1
	}
}
