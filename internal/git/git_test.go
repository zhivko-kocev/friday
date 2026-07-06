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
		{"https://github.com/foo/bar.git", false},
		{"git@github.com:foo/bar.git", false},
		{"./local/path", false},
		{"/abs/path", false},
	}
	for _, c := range cases {
		err := ValidateURL(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("ValidateURL(%q): err=%v wantErr=%v", c.in, err, c.wantErr)
		}
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
