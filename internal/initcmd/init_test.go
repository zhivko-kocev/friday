package initcmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/presets"
)

// withTempHome redirects $HOME (and $USERPROFILE on Windows) so config.UserStoreDir
// resolves into a t.TempDir(). Returns the resolved store path for assertions.
func withTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir prefers this on Windows
	return filepath.Join(home, ".friday")
}

func TestRunRejectsExistingStore(t *testing.T) {
	storeDir := withTempHome(t)
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "marker"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Run(strings.NewReader("\n"))
	if err == nil {
		t.Fatal("expected error when ~/.friday already exists")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error message missing 'already exists': %v", err)
	}
}

func TestRunScaffoldsOnBlankInput(t *testing.T) {
	storeDir := withTempHome(t)
	if err := Run(strings.NewReader("\n")); err != nil {
		t.Fatalf("Run with blank input failed: %v", err)
	}

	// Skeleton files
	for _, rel := range []string{"core.md", "rules/general.md", ".gitignore", "friday.yaml"} {
		if _, err := os.Stat(filepath.Join(storeDir, rel)); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}
	// Empty subdirs with .gitkeep
	for _, sub := range []string{"rules", "agents", "commands", "skills"} {
		if _, err := os.Stat(filepath.Join(storeDir, sub, ".gitkeep")); err != nil {
			t.Errorf("missing %s/.gitkeep: %v", sub, err)
		}
	}

	// friday.yaml seeded with every built-in preset
	cfg, err := config.LoadUser()
	if err != nil {
		t.Fatalf("LoadUser after scaffold: %v", err)
	}
	if len(cfg.Adapters) != len(presets.Names()) {
		t.Errorf("manifest has %d adapters, want %d (all presets)", len(cfg.Adapters), len(presets.Names()))
	}
}

func TestRunCloneRejectsFlagLikeURL(t *testing.T) {
	withTempHome(t)
	err := Run(strings.NewReader("--upload-pack=evil\n"))
	if err == nil {
		t.Fatal("expected ValidateURL to reject flag-like input")
	}
}

func TestRunTrimsURLWhitespace(t *testing.T) {
	withTempHome(t)
	// Whitespace-only input should behave as blank → scaffold.
	if err := Run(strings.NewReader("   \n")); err != nil {
		t.Fatalf("whitespace-only input should scaffold, got: %v", err)
	}
}

// TestRunWithURLBlankNeverPrompts is the regression guard for `friday init
// --scaffold`, documented as non-interactive but implemented as
// Run(strings.NewReader("\n")) — which still printed readRemoteURL's prompt
// (and, on a real terminal, still blocked on it, because readRemoteURL
// prefers ui.Input over its reader entirely).
func TestRunWithURLBlankNeverPrompts(t *testing.T) {
	storeDir := withTempHome(t)
	out := captureStdout(t, func() {
		if err := RunWithURL(""); err != nil {
			t.Errorf("RunWithURL(\"\") failed: %v", err)
		}
	})
	if strings.Contains(out, "Remote repository URL") {
		t.Errorf("--scaffold prompted for a remote URL:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(storeDir, config.ManifestName)); err != nil {
		t.Errorf("scaffold wrote no manifest: %v", err)
	}
}

// TestRunWithURLRejectsExistingStore keeps RunWithURL behind the same
// overwrite guard Run has — the two share initableStore, and a --scaffold
// that silently adopted a populated ~/.friday would be worse than the prompt.
func TestRunWithURLRejectsExistingStore(t *testing.T) {
	storeDir := withTempHome(t)
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "marker"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RunWithURL(""); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("RunWithURL on a populated store = %v, want an 'already exists' error", err)
	}
}

// TestEnsureSourcesIgnored covers the three states a store's .gitignore can
// be in when `friday source add` first clones into .sources/. A store
// scaffolded before that command existed has no such pattern, and `friday
// remote push` runs `git add -A`, committing the nested clone as a bare
// gitlink.
func TestEnsureSourcesIgnored(t *testing.T) {
	cases := []struct {
		name     string
		existing string // "" = no .gitignore at all
		want     func(t *testing.T, before, after string)
	}{
		{
			name: "absent gets the full scaffold template",
			want: func(t *testing.T, _, after string) {
				if after != placeholderGitignore {
					t.Errorf("wrote %q, want the scaffold template", after)
				}
			},
		},
		{
			name:     "scaffold template already covers it",
			existing: placeholderGitignore,
			want: func(t *testing.T, before, after string) {
				if after != before {
					t.Errorf("rewrote a .gitignore that already ignores %s", sourcesIgnoreEntry)
				}
			},
		},
		{
			name:     "pre-existing store gains the pattern, keeping its own",
			existing: "# mine\n.env\n",
			want: func(t *testing.T, before, after string) {
				if !strings.HasPrefix(after, before) {
					t.Errorf("clobbered the user's own patterns:\n%s", after)
				}
				if !strings.Contains(after, "\n"+sourcesIgnoreEntry+"\n") {
					t.Errorf("did not append %s:\n%s", sourcesIgnoreEntry, after)
				}
			},
		},
		{
			name:     "no trailing newline still appends cleanly",
			existing: ".env",
			want: func(t *testing.T, _, after string) {
				if !strings.Contains(after, "\n"+sourcesIgnoreEntry+"\n") {
					t.Errorf("did not append %s on its own line:\n%s", sourcesIgnoreEntry, after)
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := t.TempDir()
			path := filepath.Join(store, ".gitignore")
			if c.existing != "" {
				if err := os.WriteFile(path, []byte(c.existing), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := EnsureSourcesIgnored(store); err != nil {
				t.Fatalf("EnsureSourcesIgnored: %v", err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read .gitignore: %v", err)
			}
			c.want(t, c.existing, string(data))

			// Idempotence: a second call must be a no-op, or every `source
			// add` grows the file.
			if err := EnsureSourcesIgnored(store); err != nil {
				t.Fatalf("second EnsureSourcesIgnored: %v", err)
			}
			again, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(again) != string(data) {
				t.Errorf("EnsureSourcesIgnored is not idempotent:\n%s", again)
			}
		})
	}
}

// captureStdout swaps os.Stdout for a pipe while fn runs and returns what was
// written. The pipe is drained WHILE fn runs: an OS pipe buffer is small
// (especially on Windows), so reading only afterwards deadlocks as soon as fn
// outgrows it.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	_ = w.Close()
	os.Stdout = old
	return <-done
}
