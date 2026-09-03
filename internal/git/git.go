// Package git wraps the git CLI for friday's manager operations.
//
// It shells out to `git` rather than linking a Go git library — every dev
// box already has git installed, and the operations we need are trivially
// expressed as command lines.
package git

import (
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// DefaultProposeBranch is the timestamped branch name a proposal is pushed to
// when the caller doesn't specify one. Centralized so `friday remote propose`
// and the control room's `share` can't drift on the naming scheme.
func DefaultProposeBranch() string {
	return "friday/propose-" + time.Now().UTC().Format("20060102-150405")
}

// ErrNothingToCommit signals that StageCommitPush had no work to do.
var ErrNothingToCommit = fmt.Errorf("nothing to commit")

// Available reports whether the `git` binary is on PATH.
func Available() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// urlSchemes are the transports friday supports. A positive allowlist, because
// git resolves any unknown `scheme://` to a `git-remote-<scheme>` helper
// binary, which is a program lookup driven by untrusted input.
var urlSchemes = []string{"https", "http", "ssh", "git", "file"}

// ValidateURL rejects strings git would read as something other than a plain
// remote address. `sources[].url` is untrusted input — a manifest can arrive
// from someone else's clone via `friday init --from-git`, and
// `friday source update` then feeds it straight to Clone.
//
// Three separate things are refused, because each escapes differently:
//
//   - A leading '-', which any subcommand would read as a flag
//     (`--upload-pack=evil`).
//   - git's `<transport>::<address>` helper syntax. Clone's `--` terminator
//     only stops flag parsing; it does nothing about the transport layer, and
//     `ext::<command>` runs that command as its way of "connecting". The
//     terminator is why this has to be checked here rather than assumed away.
//   - A `scheme://` outside urlSchemes, for the same helper-lookup reason.
//
// A local path and the scp-like `user@host:path` form carry no scheme and pass
// through; a Windows drive letter is a single colon, never '::'.
func ValidateURL(url string) error {
	if url == "" {
		return fmt.Errorf("git url is empty")
	}
	if strings.HasPrefix(url, "-") {
		return fmt.Errorf("git url %q starts with '-' (refusing to pass as flag)", url)
	}
	head, _, _ := strings.Cut(url, "/")
	if strings.Contains(head, "::") {
		return fmt.Errorf("git url %q names a transport helper (`transport::address`), which can execute a command; use an https, ssh or file url", url)
	}
	if scheme, _, ok := strings.Cut(url, "://"); ok {
		if !slices.Contains(urlSchemes, strings.ToLower(scheme)) {
			return fmt.Errorf("git url %q uses unsupported scheme %q (allowed: %s)", url, scheme, strings.Join(urlSchemes, ", "))
		}
	}
	return nil
}

// ValidateRef rejects strings `git checkout` would read as an option rather
// than a ref. Unlike Clone, checkout has no usable `--` terminator — after
// it, arguments are pathspecs, not refs — so validation is the only control
// available, and `sources[].ref` is untrusted input (a manifest can arrive
// from someone else's clone via `friday init --from-git`). `ref: --force`
// would otherwise become `git checkout --detach --force` and throw away
// local modifications in the source's clone.
func ValidateRef(ref string) error {
	if ref == "" {
		return fmt.Errorf("git ref is empty")
	}
	if strings.HasPrefix(ref, "-") {
		return fmt.Errorf("git ref %q starts with '-' (refusing to pass as flag)", ref)
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
	return command("-C", dir, "rev-parse", "--git-dir").Run() == nil
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

// DefaultBranch returns origin's HEAD branch (e.g. "main"). Falls back to
// "main" when the remote doesn't advertise one (empty repo, offline, ...).
func DefaultBranch(dir string) string {
	out, err := output("-C", dir, "ls-remote", "--symref", "origin", "HEAD")
	if err != nil {
		return "main"
	}
	// First line: "ref: refs/heads/<branch>\tHEAD"
	for _, line := range strings.Split(out, "\n") {
		rest, ok := strings.CutPrefix(line, "ref: refs/heads/")
		if !ok {
			continue
		}
		if branch, _, ok := strings.Cut(rest, "\t"); ok {
			return strings.TrimSpace(branch)
		}
	}
	return "main"
}

// Propose stages everything, builds an ephemeral commit via plumbing, and
// pushes it to a new remote branch — the local branch, history, and working
// tree stay exactly as they were (the index is re-reset afterwards). GitLab
// push options ride along so the server opens an MR against target; servers
// without push-option support get a plain retry and print their own PR link
// in the returned push output.
func Propose(dir, branch, target, msg string) (string, error) {
	dirty, err := HasUncommitted(dir)
	if err != nil {
		return "", err
	}
	if !dirty {
		return "", ErrNothingToCommit
	}
	if err := run("-C", dir, "add", "-A"); err != nil {
		return "", err
	}
	// Whatever happens next, put the index back so the store looks untouched.
	defer func() { _ = run("-C", dir, "reset", "-q") }()

	tree, err := output("-C", dir, "write-tree")
	if err != nil {
		return "", err
	}
	commitArgs := []string{"-C", dir, "commit-tree", tree, "-m", msg}
	if head, err := output("-C", dir, "rev-parse", "--verify", "HEAD"); err == nil {
		commitArgs = append(commitArgs, "-p", head)
	} // else: repo has no commits yet — root commit
	commit, err := output(commitArgs...)
	if err != nil {
		return "", err
	}

	refspec := commit + ":refs/heads/" + branch
	pushOut, err := output("-C", dir, "push",
		"-o", "merge_request.create",
		"-o", "merge_request.target="+target,
		"origin", refspec)
	if err != nil {
		// Server may not support push options at all — retry plain.
		pushOut, err = output("-C", dir, "push", "origin", refspec)
	}
	return pushOut, err
}

// SetOrigin points origin at url, adding the remote if it doesn't exist yet.
// URL safety relies on ValidateURL — `git remote set-url` has no `--`
// separator, so flag-like URLs must never reach it.
func SetOrigin(dir, url string) error {
	if err := ValidateURL(url); err != nil {
		return err
	}
	if err := run("-C", dir, "remote", "set-url", "origin", url); err != nil {
		return run("-C", dir, "remote", "add", "origin", url)
	}
	return nil
}

// OriginURL returns the configured origin URL, or "" when none is set.
func OriginURL(dir string) string {
	out, err := output("-C", dir, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// Fetch downloads new commits, refs, and tags for dir's origin remote
// without touching the working tree — the read half of `friday source
// update` for a ref-less (tracking-the-default-branch) source. Clone's
// --depth=1 implies --single-branch, narrowing the remote's fetch refspec
// to just the branch cloned; Fetch widens it back to every branch first, so
// a source can still be checked out onto a different branch, tag, or sha
// later — CheckoutRef would otherwise never see anything Clone didn't.
func Fetch(dir string) error {
	if err := run("-C", dir, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*"); err != nil {
		return err
	}
	return run("-C", dir, "fetch", "--tags", "origin")
}

// CheckoutRef switches dir's working tree to ref. A branch checks out
// attached, so a later Pull can fast-forward it; a tag or commit sha checks
// out detached — there is no branch tip to fast-forward a fixed point to,
// and `friday source update` skips both instead of trying.
func CheckoutRef(dir, ref string) error {
	if err := ValidateRef(ref); err != nil {
		return err
	}
	if isRemoteBranch(dir, ref) {
		return run("-C", dir, "checkout", ref)
	}
	return run("-C", dir, "checkout", "--detach", ref)
}

// isRemoteBranch reports whether ref names a branch on origin.
func isRemoteBranch(dir, ref string) bool {
	return command("-C", dir, "show-ref", "--verify", "--quiet", "refs/remotes/origin/"+ref).Run() == nil
}

// HeadSHA returns dir's checked-out commit sha, whether HEAD is attached to
// a branch or detached — `source list`'s sha column wants the commit
// identity regardless, unlike CurrentRef's branch-name preference.
func HeadSHA(dir string) string {
	sha, err := output("-C", dir, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return sha
}

// CurrentRef reports dir's checked-out branch name, or its commit sha when
// detached — a pinned tag or sha ref never resolves back to a branch name.
func CurrentRef(dir string) string {
	if branch, err := output("-C", dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil && branch != "HEAD" {
		return branch
	}
	return HeadSHA(dir)
}

// Status returns short-form status output for dir.
func Status(dir string) (string, error) {
	return output("-C", dir, "status", "--short", "--branch")
}

// command builds every git invocation friday makes, with the set of allowed
// transports pinned to urlSchemes. ValidateURL is the first line of defence;
// this is the one that still holds if a future callsite skips it, so it is
// applied to all invocations rather than to the network subcommands only.
//
// It has to be the environment variable, not `-c protocol.ext.allow=never`:
// GIT_ALLOW_PROTOCOL overrides protocol.*.allow config outright, so a machine
// that already exports a relaxed list for unrelated tooling defeats the config
// form completely — measured, with ext:: going on to execute its command.
// Setting the variable ourselves overrides both the ambient value and any
// system/global config, and it is inherited by submodule fetches too.
//
// args are reported verbatim in errors so a failure names the command the
// caller asked for.
func command(args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	// A later duplicate wins in exec, so this overrides any inherited value.
	cmd.Env = append(os.Environ(), "GIT_ALLOW_PROTOCOL="+strings.Join(urlSchemes, ":"))
	return cmd
}

func run(args ...string) error {
	out, err := command(args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func output(args ...string) (string, error) {
	out, err := command(args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimRight(string(out), "\n"), nil
}
