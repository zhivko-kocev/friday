package cli

import (
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/git"
	"github.com/zhivko-kocev/friday/internal/output"
	"github.com/zhivko-kocev/friday/internal/ui"
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

	// `remote init` must run before the is-repo guard: its whole point is
	// wiring up a store that was scaffolded without a remote.
	if args[0] == "init" {
		if len(args) != 2 {
			output.Err("usage: friday remote init <url>")
			return 1
		}
		return remoteInit(storeDir, args[1])
	}

	if !git.IsRepo(storeDir) {
		output.Err("user store at %s is not a git repo", storeDir)
		output.Dim("hint: run `friday init` and provide a remote URL to set up a git-backed store")
		return 1
	}

	switch args[0] {
	case "pull":
		if err := ui.WithSpinner("pulling latest changes", func() error { return git.Pull(storeDir) }); err != nil {
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
		if err := ui.WithSpinner("committing and pushing", func() error { return git.StageCommitPush(storeDir, msg) }); err != nil {
			if errors.Is(err, git.ErrNothingToCommit) {
				output.Skip("nothing to commit in %s", storeDir)
				return 0
			}
			output.Err("%v", err)
			return 1
		}
		output.OK("pushed %s", storeDir)
		return 0

	case "propose":
		return remotePropose(storeDir, args[1:])

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
		output.Err("unknown remote subcommand %q (want: init, pull, push, propose, status)", args[0])
		return 1
	}
}

// remotePropose is `remote push` for team stores with protected branches:
// instead of committing to the local branch and pushing it, the change goes
// to a fresh remote branch as an ephemeral commit — the local store (branch,
// history, working tree) stays untouched. On GitLab, push options open the
// MR server-side; other forges print their PR link in the push output. Once
// the MR merges, `friday remote pull` fast-forwards and the local edits
// coincide with the merged content.
// proposeOpts backs both `remote propose` and the `share` porcelain, and is
// the flagset the command table introspects for completion + `--help`.
type proposeOpts struct {
	message, messageLong, branch, target string
}

func proposeFlags(o *proposeOpts) *flag.FlagSet {
	fs := flag.NewFlagSet("propose", flag.ContinueOnError)
	// -m is short, --message is conventional; -m wins when both are set.
	fs.StringVar(&o.message, "m", "", "commit message (required)")
	fs.StringVar(&o.messageLong, "message", "", "alias for -m")
	fs.StringVar(&o.branch, "branch", "", "remote branch name (default: friday/propose-<timestamp>)")
	fs.StringVar(&o.target, "target", "", "MR target branch (default: the remote's HEAD branch)")
	return fs
}

func (o *proposeOpts) msg() string {
	if o.message != "" {
		return o.message
	}
	return o.messageLong
}

func remotePropose(storeDir string, args []string) int {
	var o proposeOpts
	fs := proposeFlags(&o)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if o.msg() == "" {
		output.Err("commit message required: pass -m \"...\"")
		return 1
	}
	return proposeStore(storeDir, o.branch, o.target, o.msg())
}

// cmdShare is the top-level porcelain for `remote propose`: propose your store
// changes for team review without remembering the subcommand. Same guards,
// same flags, same MR flow.
func cmdShare(args []string) int {
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
	return remotePropose(storeDir, args)
}

// proposeStore runs the ephemeral-commit → remote-branch → MR flow. Shared
// by `remote propose` and `promote --propose`. Blank branch/target get the
// timestamped default and the remote's HEAD branch.
func proposeStore(storeDir, branch, target, msg string) int {
	if git.OriginURL(storeDir) == "" {
		output.Err("no origin configured — run `friday remote init <url>` first")
		return 1
	}
	if branch == "" {
		branch = "friday/propose-" + time.Now().UTC().Format("20060102-150405")
	}
	if target == "" {
		target = git.DefaultBranch(storeDir)
	}

	var out string
	err := ui.WithSpinner("pushing proposal branch", func() error {
		var e error
		out, e = git.Propose(storeDir, branch, target, msg)
		return e
	})
	if errors.Is(err, git.ErrNothingToCommit) {
		output.Skip("nothing to propose in %s", storeDir)
		return 0
	}
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	if out != "" {
		fmt.Println(out) // forges print the MR/PR link here
	}
	output.OK("pushed %s (targeting %s) — local store untouched until the MR merges", branch, target)
	output.Dim("after the merge: `friday remote pull`")
	return 0
}

// remoteInit sets (or replaces) origin on the store, running `git init`
// first when the store isn't a repo yet. Closes the "scaffolded blank, now
// I want to publish" gap without re-cloning.
func remoteInit(storeDir, url string) int {
	if !git.IsRepo(storeDir) {
		if err := git.Init(storeDir); err != nil {
			output.Err("%v", err)
			return 1
		}
		output.OK("initialized git repo in %s", storeDir)
	}
	if old := git.OriginURL(storeDir); old != "" && old != url {
		output.Dim("replacing origin %s", old)
	}
	if err := git.SetOrigin(storeDir, url); err != nil {
		output.Err("%v", err)
		return 1
	}
	output.OK("origin set to %s", url)
	return 0
}
