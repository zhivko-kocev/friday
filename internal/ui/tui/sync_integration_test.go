package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/presets"
)

// TestSyncCapturesEditedCopyFile proves sync's pull half actually runs and
// captures: a copy-rule agent file (not the concatenate CLAUDE.md, which pull
// can't reverse) is pushed, edited on the target, then a sync must pull that
// edit back into the store.
func TestSyncCapturesEditedCopyFile(t *testing.T) {
	home := isolatedHome(t)
	storeDir := filepath.Join(home, ".friday")
	storeAgent := filepath.Join(storeDir, "agents", "foo.md")
	mustWrite(t, storeAgent, "v1\n")

	p, ok := presets.Get("claude")
	if !ok {
		t.Fatal("claude preset missing")
	}
	cfg := config.NewDefault(config.ScopeUser, storeDir, home,
		map[string]*config.Adapter{"claude": p.Adapter()})

	// Seed: push writes the agent file to the target and records its baseline.
	seed := pushCmd(cfg, []string{"claude"}, true, nil)().(engineDoneMsg)
	if seed.err != nil {
		t.Fatalf("seed: %v", seed.err)
	}
	var targetAgent string
	for _, ch := range seed.changes {
		if strings.HasSuffix(filepath.ToSlash(ch.DestPath), "agents/foo.md") {
			targetAgent = ch.DestPath
		}
	}
	if targetAgent == "" {
		t.Fatal("seed did not write agents/foo.md to the target")
	}

	// Edit the target agent file, then sync — the pull half must capture it.
	mustWrite(t, targetAgent, "v2 edited\n")
	done := syncCmd(cfg, []string{"claude"}, true, nil)().(engineDoneMsg)
	if done.err != nil {
		t.Fatalf("sync: %v", done.err)
	}

	got, err := os.ReadFile(storeAgent)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "v2 edited") {
		t.Errorf("sync pull half did not capture the edit; store agents/foo.md = %q", got)
	}
}
