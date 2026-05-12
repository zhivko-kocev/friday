// Package git wraps the git CLI for friday's manager operations.
//
// It shells out to `git` rather than linking a Go git library — every dev
// box already has git installed, and the operations we need are trivially
// expressed as command lines.
package git

import (
	"fmt"
	"os/exec"
	"strings"
)

// ErrNothingToCommit signals that StageCommitPush had no work to do.
var ErrNothingToCommit = fmt.Errorf("nothing to commit")

// Available reports whether the `git` binary is on PATH.
func Available() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// ValidateURL rejects strings that git would interpret as a flag, plus
// obviously empty input. Without this, `git clone --upload-pack=evil`
// would smuggle a flag through any code path that takes a user-supplied
// URL.
func ValidateURL(url string) error {
	if url == "" {
		return fmt.Errorf("git url is empty")
	}
	if strings.HasPrefix(url, "-") {
		return fmt.Errorf("git url %q starts with '-' (refusing to pass as flag)", url)
	}
	return nil
}

// Clone shallow-clones url into dest. dest must not already exist. The
// `--` separator stops git from interpreting any later argument as a
// flag, so even a URL that slipped past ValidateURL stays positional.
func Clone(url, dest string) error {
	if err := ValidateURL(url); err != nil {
		return err
	}
	return run("clone", "--depth=1", "--", url, dest)
}

// Init creates a fresh git repo at dir (which must already exist).
func Init(dir string) error {
	return run("-C", dir, "init", "-q")
}

// Pull fast-forwards the repo at dir.
func Pull(dir string) error {
	return run("-C", dir, "pull", "--ff-only")
}

// IsRepo returns true if dir is inside a git working tree.
func IsRepo(dir string) bool {
	return exec.Command("git", "-C", dir, "rev-parse", "--git-dir").Run() == nil
}

// HasUncommitted reports whether dir has any staged or unstaged changes.
func HasUncommitted(dir string) (bool, error) {
	out, err := output("-C", dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// StageCommitPush stages every change in dir, commits with msg, and pushes.
// Returns "no changes" without error if there's nothing to commit.
func StageCommitPush(dir, msg string) error {
	dirty, err := HasUncommitted(dir)
	if err != nil {
		return err
	}
	if !dirty {
		return ErrNothingToCommit
	}
	if err := run("-C", dir, "add", "-A"); err != nil {
		return err
	}
	if err := run("-C", dir, "commit", "-m", msg); err != nil {
		return err
	}
	return run("-C", dir, "push")
}

// Status returns short-form status output for dir.
func Status(dir string) (string, error) {
	return output("-C", dir, "status", "--short", "--branch")
}

func run(args ...string) error {
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func output(args ...string) (string, error) {
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimRight(string(out), "\n"), nil
}
