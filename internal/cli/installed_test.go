package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/presets"
)

// agentsPreset is the one built-in carrying InstalledIf — the shared
// ~/.agents/skills tree several agents read natively.
func agentsPreset(t *testing.T) presets.Preset {
	t.Helper()
	p, ok := presets.Get("agents")
	if !ok {
		t.Fatal("no agents preset")
	}
	if len(p.InstalledIf) == 0 {
		t.Fatal("agents preset carries no InstalledIf")
	}
	return p
}

// The proxy check exists because friday never creates ~/.agents until one of
// the agents reading it needs something: the adapter is installed when a
// sibling that reads that tree is.
func TestAdapterInstalledFollowsInstalledIf(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	p := agentsPreset(t)

	cfg := &config.Config{
		Version:  1,
		StoreDir: t.TempDir(),
		Adapters: map[string]*config.Adapter{
			"agents": {Target: p.Target},
			"codex":  {Target: "~/.codex"},
		},
	}
	if adapterInstalled(cfg, "agents") {
		t.Error("agents counted as installed with no sibling present")
	}
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !adapterInstalled(cfg, "agents") {
		t.Error("agents not installed even though codex is")
	}
}

// InstalledIf's own contract: the proxy applies only while the manifest still
// describes the built-in preset. A user who repoints the adapter gets the
// ordinary check against the target they actually declared — answering
// "installed" from a directory their adapter has nothing to do with is wrong.
func TestAdapterInstalledHonorsAUserRetarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := t.TempDir()

	cfg := &config.Config{
		Version:  1,
		StoreDir: t.TempDir(),
		Adapters: map[string]*config.Adapter{
			"agents": {Target: filepath.Join(mine, "nowhere")},
			"codex":  {Target: "~/.codex"},
		},
	}
	if adapterInstalled(cfg, "agents") {
		t.Error("a user-retargeted agents adapter used the preset's proxy check")
	}
	cfg.Adapters["agents"].Target = mine
	if !adapterInstalled(cfg, "agents") {
		t.Error("a user-retargeted agents adapter ignored its own existing target")
	}
}

// status must describe what sync will actually do. It used to compute the grid
// with a bare dirExists, so the agents adapter showed [missing] and its pending
// files were folded under "`friday sync` sets it up" while sync was already
// writing there.
func TestStatusGridAgreesWithPushOnInstalledIf(t *testing.T) {
	setDriftEnv(t)
	// setDriftEnv gives HOME and USERPROFILE separate dirs; target resolution
	// reads whichever one os.UserHomeDir picks per platform, so pin both.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	store := t.TempDir()
	t.Setenv("FRIDAY_HOME", store)
	p := agentsPreset(t)
	if err := os.WriteFile(filepath.Join(store, config.ManifestName), []byte(
		"version: 1\nadapters:\n  agents:\n    target: "+p.Target+"\n    rules: []\n  codex:\n    target: ~/.codex\n    rules: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() { Run([]string{"status"}, "test") })
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "agents") && strings.Contains(line, "[missing]") {
			t.Errorf("status reports agents [missing] while push treats it as installed:\n%s", out)
		}
	}
}

// Config.Warnings are machine-hostile on stdout (--json and the completion
// stream are both parsed) and too important to mute: doctor is the command
// whose entire job is explaining a degraded manifest.
func TestDoctorManifestWarningsGoToStderr(t *testing.T) {
	setDriftEnv(t)
	store := t.TempDir()
	t.Setenv("FRIDAY_HOME", store)
	// A legacy adapter key still loads, with a rename warning attached.
	if err := os.WriteFile(filepath.Join(store, config.ManifestName), []byte(
		"version: 1\nadapters:\n  antigravity:\n    target: ~/.gemini\n    rules: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr := captureBothStreams(t, func() { Run([]string{"doctor"}, "test") })
	if strings.Contains(stdout, "renamed") {
		t.Errorf("manifest warning leaked onto stdout:\n%s", stdout)
	}
	if !strings.Contains(stderr, "renamed") {
		t.Errorf("manifest warning missing from stderr:\n%s", stderr)
	}

	// --quiet must not silence it: the exit code is the only thing --quiet owns.
	manifestWarningsShown = false
	_, quietErr := captureBothStreams(t, func() { Run([]string{"--quiet", "doctor"}, "test") })
	if !strings.Contains(quietErr, "renamed") {
		t.Errorf("--quiet silenced doctor's own manifest diagnostic:\n%s", quietErr)
	}
}

// captureBothStreams swaps stdout and stderr for pipes while fn runs.
func captureBothStreams(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	rErr, wErr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = wOut, wErr
	done := make(chan [2]string, 1)
	go func() {
		var o, e strings.Builder
		copyDone := make(chan struct{})
		go func() { _, _ = io.Copy(&e, rErr); close(copyDone) }()
		_, _ = io.Copy(&o, rOut)
		<-copyDone
		done <- [2]string{o.String(), e.String()}
	}()
	fn()
	os.Stdout, os.Stderr = oldOut, oldErr
	_ = wOut.Close()
	_ = wErr.Close()
	got := <-done
	return got[0], got[1]
}
