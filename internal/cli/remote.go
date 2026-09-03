package cli

import (
	"context"
	"errors"
	"fmt"

	ucli "github.com/urfave/cli/v3"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/git"
	"github.com/zhivko-kocev/friday/internal/output"
	"github.com/zhivko-kocev/friday/internal/ui"
)

// newRemoteCommand — git operations on the user store. init/pull/push/
// propose/status are real urfave subcommands now (previously one function
// switching on args[0]), so each gets its own accurate --help and shell
// completion for free.
func newRemoteCommand() *ucli.Command {
	return &ucli.Command{
		Name:     "remote",
		Category: "advanced",
		Usage:    "git ops on ~/.friday (init/pull/push/propose/status)",
		Commands: []*ucli.Command{
			newRemoteInitCommand(),
			newRemoteSubcommand("pull", "pull the latest changes into ~/.friday", nil, remotePull),
			newRemoteSubcommand("push", "commit and push ~/.friday", remoteMessageFlags(), remotePush),
			newRemoteSubcommand("propose", "push a branch + open an MR for team review", remoteProposeFlags(), remotePropose),
			newRemoteSubcommand("status", "show git status of ~/.friday", nil, remoteStatus),
		},
		// urfave routes an unmatched leading token to the parent's Action,
		// so this covers both a bare `friday remote` and a mistyped
		// subcommand — the latter naming what it didn't understand, as
		// rootAction does one level up.
		Action: func(_ context.Context, cmd *ucli.Command) error {
			return unknownSubcommand(cmd, "remote", "init|pull|push|propose|status")
		},
	}
}

// unknownSubcommand is the shared parent-Action for a command that only
// dispatches: it names the token urfave could not match (if any) and lists
// every subcommand actually registered, then exits 1.
func unknownSubcommand(cmd *ucli.Command, parent, subcommands string) error {
	if rest := cmd.Args().Slice(); len(rest) > 0 {
		output.Err("unknown %s subcommand %q", parent, rest[0])
	}
	output.Err("usage: friday %s <%s>", parent, subcommands)
	return ucli.Exit("", 1)
}

// requireStoreRepo resolves the store dir and confirms git is available and
// the store is already a repo — the guard every remote subcommand except
// `init` shares.
func requireStoreRepo() (string, error) {
	storeDir, err := config.UserStoreDir()
	if err != nil {
		output.Err("%v", err)
		return "", ucli.Exit("", 1)
	}
	if !git.Available() {
		output.Err("git not found in PATH")
		return "", ucli.Exit("", 1)
	}
	if !git.IsRepo(storeDir) {
		output.Err("user store at %s is not a git repo", storeDir)
		output.Dim("hint: run `friday init` and provide a remote URL to set up a git-backed store")
		return "", ucli.Exit("", 1)
	}
	return storeDir, nil
}

// newRemoteSubcommand wires one of the storeDir-taking handlers into a real
// Command, sharing the requireStoreRepo guard every one of them needs first.
func newRemoteSubcommand(name, usage string, flags []ucli.Flag, run func(*ucli.Command, string) error) *ucli.Command {
	return &ucli.Command{
		Name:  name,
		Usage: usage,
		Flags: flags,
		Action: func(_ context.Context, cmd *ucli.Command) error {
			storeDir, err := requireStoreRepo()
			if err != nil {
				return err
			}
			return run(cmd, storeDir)
		},
	}
}

func newRemoteInitCommand() *ucli.Command {
	return &ucli.Command{
		Name:      "init",
		Usage:     "set (or replace) origin on ~/.friday",
		ArgsUsage: "<url>",
		Action: func(_ context.Context, cmd *ucli.Command) error {
			if cmd.Args().Len() != 1 {
				output.Err("usage: friday remote init <url>")
				return ucli.Exit("", 1)
			}
			storeDir, err := config.UserStoreDir()
			if err != nil {
				output.Err("%v", err)
				return ucli.Exit("", 1)
			}
			if !git.Available() {
				output.Err("git not found in PATH")
				return ucli.Exit("", 1)
			}
			// `remote init` must run before the is-repo guard: its whole point
			// is wiring up a store that was scaffolded without a remote.
			return remoteInit(storeDir, cmd.Args().First())
		},
	}
}

func remotePull(_ *ucli.Command, storeDir string) error {
	if err := ui.WithSpinner("pulling latest changes", func() error { return git.Pull(storeDir) }); err != nil {
		output.Err("%v", err)
		return ucli.Exit("", 1)
	}
	output.OK("pulled latest changes into %s", storeDir)
	return nil
}

// remoteMessageFlags is the -m/--message pair shared by `remote push` and
// `remote propose`/`share`: -m is short, --message is conventional, and
// -m wins when both are set.
func remoteMessageFlags() []ucli.Flag {
	return []ucli.Flag{
		&ucli.StringFlag{Name: "m", Usage: "commit message (required)"},
		&ucli.StringFlag{Name: "message", Usage: "alias for -m"},
	}
}

func messageFlag(cmd *ucli.Command) string {
	if m := cmd.String("m"); m != "" {
		return m
	}
	return cmd.String("message")
}

func remotePush(cmd *ucli.Command, storeDir string) error {
	msg := messageFlag(cmd)
	if msg == "" {
		output.Err("commit message required: friday remote push -m \"...\"")
		return ucli.Exit("", 1)
	}
	if err := ui.WithSpinner("committing and pushing", func() error { return git.StageCommitPush(storeDir, msg) }); err != nil {
		if errors.Is(err, git.ErrNothingToCommit) {
			output.Skip("nothing to commit in %s", storeDir)
			return nil
		}
		output.Err("%v", err)
		return ucli.Exit("", 1)
	}
	output.OK("pushed %s", storeDir)
	return nil
}

func remoteStatus(_ *ucli.Command, storeDir string) error {
	out, err := git.Status(storeDir)
	if err != nil {
		output.Err("%v", err)
		return ucli.Exit("", 1)
	}
	output.Header("Remote status (" + storeDir + ")")
	if out == "" {
		output.Dim("(working tree clean)")
	} else {
		fmt.Println(out)
	}
	return nil
}

// remoteProposeFlags is remote propose's own flag set, reused by `share`.
func remoteProposeFlags() []ucli.Flag {
	return append(remoteMessageFlags(),
		&ucli.StringFlag{Name: "branch", Usage: "remote branch name (default: friday/propose-<timestamp>)"},
		&ucli.StringFlag{Name: "target", Usage: "MR target branch (default: the remote's HEAD branch)"},
	)
}

// remotePropose is `remote push` for team stores with protected branches:
// instead of committing to the local branch and pushing it, the change goes
// to a fresh remote branch as an ephemeral commit — the local store (branch,
// history, working tree) stays untouched. On GitLab, push options open the
// MR server-side; other forges print their PR link in the push output. Once
// the MR merges, `friday remote pull` fast-forwards and the local edits
// coincide with the merged content.
func remotePropose(cmd *ucli.Command, storeDir string) error {
	msg := messageFlag(cmd)
	if msg == "" {
		output.Err("commit message required: pass -m \"...\"")
		return ucli.Exit("", 1)
	}
	if code := proposeStore(storeDir, cmd.String("branch"), cmd.String("target"), msg); code != 0 {
		return ucli.Exit("", code)
	}
	return nil
}

// newShareCommand is the top-level porcelain for `remote propose`: propose
// your store changes for team review without remembering the subcommand.
// Same guards, same flags, same MR flow.
func newShareCommand() *ucli.Command {
	return &ucli.Command{
		Name:     "share",
		Category: "porcelain",
		Usage:    "propose your store changes for team review (opens an MR)",
		Flags:    remoteProposeFlags(),
		Action: func(_ context.Context, cmd *ucli.Command) error {
			storeDir, err := requireStoreRepo()
			if err != nil {
				return err
			}
			return remotePropose(cmd, storeDir)
		},
	}
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
		branch = git.DefaultProposeBranch()
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
func remoteInit(storeDir, url string) error {
	if !git.IsRepo(storeDir) {
		if err := git.Init(storeDir); err != nil {
			output.Err("%v", err)
			return ucli.Exit("", 1)
		}
		output.OK("initialized git repo in %s", storeDir)
	}
	if old := git.OriginURL(storeDir); old != "" && old != url {
		output.Dim("replacing origin %s", old)
	}
	if err := git.SetOrigin(storeDir, url); err != nil {
		output.Err("%v", err)
		return ucli.Exit("", 1)
	}
	output.OK("origin set to %s", url)
	return nil
}
