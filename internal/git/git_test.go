package git

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateURL(t *testing.T) {
	cases := []struct {
		in      string
		wantErr bool
	}{
		{"", true},
		{"-x", true}, // would be parsed as a flag by git
		{"--upload-pack=evil", true},
		// Transport helpers: `--` stops flag parsing, not the transport layer,
		// and ext:: runs its argument as a command.
		{"ext::touch pwned", true},
		{"ext::sh -c 'touch pwned'", true},
		{"fd::7", true},
		{"hg::https://example.com/repo", true},
		{"EXT::touch pwned", true},
		// An unknown scheme resolves to a git-remote-<scheme> helper binary.
		{"weird://example.com/repo", true},
		{"https://github.com/foo/bar.git", false},
		{"http://example.com/repo.git", false},
		{"ssh://git@example.com:2222/foo/bar.git", false},
		{"git://example.com/repo.git", false},
		{"file:///tmp/repo", false},
		{"HTTPS://github.com/foo/bar.git", false},
		{"git@github.com:foo/bar.git", false},
		{"./local/path", false},
		{"/abs/path", false},
		{`C:\Users\me\repo`, false},
		{"C:/Users/me/repo", false},
		// '::' past the first '/' is part of a path, not helper syntax.
		{"/tmp/odd::name/repo", false},
	}
	for _, c := range cases {
		err := ValidateURL(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("ValidateURL(%q): err=%v wantErr=%v", c.in, err, c.wantErr)
		}
	}
}

// Every invocation carries the transport allowlist, so a callsite added after
// ValidateURL — or one that forgets it — still cannot reach ext::/fd::. It has
// to be the environment variable: GIT_ALLOW_PROTOCOL overrides
// protocol.*.allow config, so the config form alone is defeated by a machine
// that already exports a relaxed list.
func TestEveryInvocationPinsTransports(t *testing.T) {
	t.Setenv("GIT_ALLOW_PROTOCOL", "ext:fd:file:https")
	env := command("rev-parse", "HEAD").Env

	var got string
	for _, kv := range env {
		if strings.HasPrefix(kv, "GIT_ALLOW_PROTOCOL=") {
			got = kv // last one wins in exec, so keep scanning
		}
	}
	if got == "" {
		t.Fatal("no GIT_ALLOW_PROTOCOL in the child environment")
	}
	for _, banned := range []string{"ext", "fd"} {
		for _, proto := range strings.Split(strings.TrimPrefix(got, "GIT_ALLOW_PROTOCOL="), ":") {
			if proto == banned {
				t.Errorf("%s permits the %q transport", got, banned)
			}
		}
	}
	if !strings.Contains(got, "https") {
		t.Errorf("%s does not permit https", got)
	}
}

// The end-to-end proof, and the reason this fix exists: with the ambient
// allowlist relaxed the way a CI or corporate machine may already have it,
// ext:: goes on to execute its command. Clone must refuse and run nothing.
func TestCloneRefusesExtTransportEvenWhenAmbientlyAllowed(t *testing.T) {
	if !Available() {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "pwned")
	t.Setenv("GIT_ALLOW_PROTOCOL", "ext:fd:file:https")

	err := Clone("ext::"+touchCommand(marker), filepath.Join(dir, "clone"))
	if err == nil {
		t.Fatal("Clone accepted an ext:: url")
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatalf("ext:: transport executed its command: %s exists", marker)
	}
}

// touchCommand is a command that creates path. ext:: runs its argument through
// a shell, and Git for Windows ships one, so `touch` resolves on every
// platform friday supports. Kept quote-free: Windows argv handling mangles
// nested quotes, which would make the test pass for the wrong reason.
func touchCommand(path string) string {
	return "touch " + filepath.ToSlash(path)
}

func TestValidateRef(t *testing.T) {
	cases := []struct {
		in      string
		wantErr bool
	}{
		{"", true},
		{"-x", true},
		{"--force", true},      // git checkout: "throw away local modifications"
		{"--detach", true},     // would double the flag CheckoutRef adds itself
		{"--bogus-flag", true}, // reproduces the reported `--ref=--bogus-flag`
		{"main", false},
		{"v1.2.3", false},
		{"feature/hooks", false},
		{"deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", false},
	}
	for _, c := range cases {
		err := ValidateRef(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("ValidateRef(%q): err=%v wantErr=%v", c.in, err, c.wantErr)
		}
	}
}

// TestCheckoutRefRejectsFlagLikeRef is the sink-side guard: git checkout has
// no `--` terminator for refs, so a flag-shaped ref must never reach it.
func TestCheckoutRefRejectsFlagLikeRef(t *testing.T) {
	work, _, _, _ := refFixture(t)
	before := CurrentRef(work)
	err := CheckoutRef(work, "--force")
	if err == nil {
		t.Fatal("CheckoutRef(--force) = nil, want refusal")
	}
	if !strings.Contains(err.Error(), "refusing to pass as flag") {
		t.Errorf("err = %v, want the flag refusal (not git's own option parser)", err)
	}
	if got := CurrentRef(work); got != before {
		t.Errorf("working tree moved to %q, want %q untouched", got, before)
	}
}

func TestSetOriginAndOriginURL(t *testing.T) {
	if !Available() {
		t.Skip("git not in PATH")
	}
	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	if got := OriginURL(dir); got != "" {
		t.Fatalf("fresh repo OriginURL = %q, want empty", got)
	}
	// First call adds the remote.
	if err := SetOrigin(dir, "https://example.com/a.git"); err != nil {
		t.Fatal(err)
	}
	if got := OriginURL(dir); got != "https://example.com/a.git" {
		t.Errorf("OriginURL = %q", got)
	}
	// Second call replaces it.
	if err := SetOrigin(dir, "https://example.com/b.git"); err != nil {
		t.Fatal(err)
	}
	if got := OriginURL(dir); got != "https://example.com/b.git" {
		t.Errorf("OriginURL after replace = %q", got)
	}
	// Flag-like URLs are rejected before reaching git.
	if err := SetOrigin(dir, "--upload-pack=evil"); err == nil {
		t.Error("SetOrigin accepted a flag-like URL")
	}
}

// proposeFixture builds a bare origin (optionally advertising push options)
// and a working clone with one commit.
func proposeFixture(t *testing.T, pushOptions bool) (work, bare string) {
	t.Helper()
	if !Available() {
		t.Skip("git not in PATH")
	}
	root := t.TempDir()
	bare = filepath.Join(root, "origin.git")
	work = filepath.Join(root, "work")
	mustGit := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	mustGit("init", "--bare", "-b", "main", bare)
	if pushOptions {
		mustGit("-C", bare, "config", "receive.advertisePushOptions", "true")
	}
	mustGit("clone", bare, work)
	mustGit("-C", work, "config", "user.email", "t@t")
	mustGit("-C", work, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(work, "core.md"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit("-C", work, "add", "-A")
	mustGit("-C", work, "commit", "-m", "seed")
	mustGit("-C", work, "push", "origin", "main")
	return work, bare
}

func TestProposeLeavesLocalUntouched(t *testing.T) {
	for name, pushOptions := range map[string]bool{"with push options": true, "plain fallback": false} {
		t.Run(name, func(t *testing.T) {
			work, bare := proposeFixture(t, pushOptions)
			if err := os.WriteFile(filepath.Join(work, "core.md"), []byte("v2"), 0o644); err != nil {
				t.Fatal(err)
			}

			if _, err := Propose(work, "friday/propose-test", "main", "tweak core"); err != nil {
				t.Fatal(err)
			}

			// Remote branch exists and carries the change.
			refs, err := output("-C", bare, "for-each-ref", "--format=%(refname:short)", "refs/heads/")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(refs, "friday/propose-test") {
				t.Fatalf("remote refs = %q, want the propose branch", refs)
			}
			blob, err := output("-C", bare, "show", "friday/propose-test:core.md")
			if err != nil || blob != "v2" {
				t.Errorf("proposed content = %q, %v", blob, err)
			}

			// Local branch/history untouched, edit still in the working tree.
			local, _ := output("-C", work, "rev-parse", "main")
			remoteMain, _ := output("-C", bare, "rev-parse", "main")
			if local != remoteMain {
				t.Errorf("local main moved: %s vs %s", local, remoteMain)
			}
			tree, _ := os.ReadFile(filepath.Join(work, "core.md"))
			if string(tree) != "v2" {
				t.Errorf("working tree = %q, want the uncommitted edit preserved", tree)
			}
			if dirty, _ := HasUncommitted(work); !dirty {
				t.Error("working tree should still be dirty (nothing committed locally)")
			}
		})
	}
}

func TestProposeNothingToCommit(t *testing.T) {
	work, _ := proposeFixture(t, false)
	if _, err := Propose(work, "friday/x", "main", "msg"); !errors.Is(err, ErrNothingToCommit) {
		t.Errorf("err = %v, want ErrNothingToCommit", err)
	}
}

func TestDefaultBranch(t *testing.T) {
	work, _ := proposeFixture(t, false)
	if got := DefaultBranch(work); got != "main" {
		t.Errorf("DefaultBranch = %q, want main", got)
	}
}

// refFixture builds on proposeFixture: pushes a "dev" branch with a second
// commit and tags the original main commit "v1.0.0", giving tests a branch,
// a tag, and a sha to check out.
func refFixture(t *testing.T) (work, bare, mainSHA, devSHA string) {
	t.Helper()
	work, bare = proposeFixture(t, false)
	mustGit := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	mainSHA, err := output("-C", work, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	mustGit("-C", work, "tag", "v1.0.0")
	mustGit("-C", work, "push", "origin", "v1.0.0")
	mustGit("-C", work, "checkout", "-b", "dev")
	if err := os.WriteFile(filepath.Join(work, "core.md"), []byte("dev"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit("-C", work, "commit", "-am", "dev change")
	mustGit("-C", work, "push", "origin", "dev")
	devSHA, err = output("-C", work, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	mustGit("-C", work, "checkout", "main")
	return work, bare, mainSHA, devSHA
}

func TestCheckoutRefBranchStaysAttached(t *testing.T) {
	work, _, _, devSHA := refFixture(t)
	if err := CheckoutRef(work, "dev"); err != nil {
		t.Fatal(err)
	}
	if got := CurrentRef(work); got != "dev" {
		t.Errorf("CurrentRef = %q, want the attached branch %q", got, "dev")
	}
	head, err := output("-C", work, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if head != devSHA {
		t.Errorf("HEAD = %s, want dev's tip %s", head, devSHA)
	}
}

func TestCheckoutRefTagDetaches(t *testing.T) {
	work, _, mainSHA, _ := refFixture(t)
	if err := CheckoutRef(work, "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	if got := CurrentRef(work); got != mainSHA {
		t.Errorf("CurrentRef = %q, want the detached sha %q (a tag never resolves to a branch name)", got, mainSHA)
	}
}

func TestCheckoutRefSHADetaches(t *testing.T) {
	work, _, mainSHA, _ := refFixture(t)
	if err := CheckoutRef(work, mainSHA); err != nil {
		t.Fatal(err)
	}
	if got := CurrentRef(work); got != mainSHA {
		t.Errorf("CurrentRef = %q, want %q", got, mainSHA)
	}
}

func TestFetchThenCheckoutRefSeesNewBranch(t *testing.T) {
	// A fresh shallow-ish clone (proposeFixture's plain `clone`, not
	// --depth=1, but still only carries the branch it was on at clone time
	// in refs/remotes/origin — dev is pushed to bare only afterwards, via a
	// second work in refFixture) must Fetch before it can see "dev".
	work, bare := proposeFixture(t, false)
	other := filepath.Join(filepath.Dir(work), "other")
	if err := Clone(bare, other); err != nil {
		t.Fatal(err)
	}
	mustGit := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	mustGit("-C", work, "checkout", "-b", "dev")
	mustGit("-C", work, "push", "origin", "dev")

	if err := CheckoutRef(other, "dev"); err == nil {
		t.Fatal("expected checkout to fail before fetching the new branch")
	}
	if err := Fetch(other); err != nil {
		t.Fatal(err)
	}
	if err := CheckoutRef(other, "dev"); err != nil {
		t.Fatalf("checkout after fetch: %v", err)
	}
	if got := CurrentRef(other); got != "dev" {
		t.Errorf("CurrentRef = %q, want dev", got)
	}
}
