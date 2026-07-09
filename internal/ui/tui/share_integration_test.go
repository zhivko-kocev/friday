package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/zhivko-kocev/friday/internal/config"
)

// gitCmd runs git in dir with a fixed identity, failing the test on error.
func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// gitBackedStore builds a bare origin + a store repo (identity configured, an
// initial commit pushed) plus a pending edit to propose. Returns the store dir.
// Skips if git is unavailable. Fully offline — the bare repo is the "remote".
func gitBackedStore(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}
	root := t.TempDir()
	bare := filepath.Join(root, "bare.git")
	store := filepath.Join(root, ".friday")
	gitCmd(t, root, "init", "--bare", "-q", bare)
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, store, "init", "-q")
	gitCmd(t, store, "config", "user.email", "t@t")
	gitCmd(t, store, "config", "user.name", "t")
	gitCmd(t, store, "remote", "add", "origin", bare)
	mustWrite(t, filepath.Join(store, "core.md"), "core\n")
	gitCmd(t, store, "add", "-A")
	gitCmd(t, store, "commit", "-qm", "init")
	gitCmd(t, store, "push", "-q", "origin", "HEAD")
	mustWrite(t, filepath.Join(store, "core.md"), "core edited\n") // pending change
	return store
}

// TestShareCmdProposesOffline confirms git.Propose works offline against a bare
// repo (push options unsupported → plain-push retry) and shareCmd surfaces the
// proposed notice.
func TestShareCmdProposesOffline(t *testing.T) {
	store := gitBackedStore(t)
	msg := shareCmd(store, "my proposal")().(shareDoneMsg)
	if msg.err != nil {
		t.Fatalf("share: %v", msg.err)
	}
	if !strings.Contains(msg.notice, "proposed") {
		t.Errorf("expected a 'proposed' notice, got %q", msg.notice)
	}
}

// TestShareCmdNothingToPropose: a clean store (no pending change) reports nothing
// to propose rather than erroring.
func TestShareCmdNothingToPropose(t *testing.T) {
	store := gitBackedStore(t)
	gitCmd(t, store, "checkout", "-q", "--", "core.md") // drop the pending edit
	msg := shareCmd(store, "noop")().(shareDoneMsg)
	if msg.err != nil {
		t.Fatalf("share: %v", msg.err)
	}
	if !strings.Contains(msg.notice, "nothing to propose") {
		t.Errorf("expected 'nothing to propose', got %q", msg.notice)
	}
}

// TestShareGuardNotGitBacked: selecting share on a non-git store shows a notice,
// never opening the input.
func TestShareGuardNotGitBacked(t *testing.T) {
	m := newModel("test", []MenuEntry{{Name: "share", Summary: "propose"}},
		&config.Config{StoreDir: t.TempDir()}, nil, nil)
	next, _ := m.selectCommand()
	nm := next.(model)
	if nm.screen == screenShareInput {
		t.Error("share opened the input on a non-git-backed store")
	}
	if !strings.Contains(nm.body(), "not a git-backed store") {
		t.Errorf("expected the not-git-backed notice, got %q", nm.body())
	}
}

// TestControlRoomShareFlow drives home → share → message → confirm → propose
// through the real Program against the offline bare repo.
func TestControlRoomShareFlow(t *testing.T) {
	store := gitBackedStore(t)
	menu := []MenuEntry{{Name: "share", Summary: "propose your store changes for team review"}}
	m := newModel("test", menu, &config.Config{StoreDir: store}, nil, nil)

	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(100, 30))
	waitForText(t, tm, "share")                                       // home rendered
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})                           // → share input
	waitForText(t, tm, "propose store changes")                       // input screen
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("my edit")}) // type message
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})                           // → confirm
	waitForText(t, tm, "confirm")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter}) // confirm → propose
	waitForText(t, tm, "proposed")
	quit(t, tm)
}
