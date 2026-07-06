package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/rules"
)

// compileFixture builds a config with a populated claude-ish target and an
// empty opencode-ish target, both under one root.
func compileFixture(t *testing.T) (*config.Config, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("LocalAppData", t.TempDir())

	fromTarget := filepath.Join(root, "dot-claude")
	toTarget := filepath.Join(root, "dot-opencode")
	populateTarget(t, fromTarget, map[string]string{
		"agents/a.md":   "agent A",
		"skills/x/S.md": "skill S",
	})
	if err := os.MkdirAll(toTarget, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := config.NewDefault(config.ScopeUser, filepath.Join(root, "unused-store"), root,
		map[string]*config.Adapter{
			"claudeish": {
				Target: fromTarget,
				Rules: []*rules.Rule{
					{From: rules.FromSpec{"agents/*.md"}, To: "agents/{filename}"},
					{From: rules.FromSpec{"skills/**/*"}, To: "skills/{relpath}"},
				},
			},
			// opencode-ish: has skills but no agents concept → lossy.
			"openish": {
				Target: toTarget,
				Rules: []*rules.Rule{
					{From: rules.FromSpec{"skills/**/*"}, To: "skills/{relpath}"},
				},
			},
		})
	return cfg, toTarget
}

func TestCompileLossyGate(t *testing.T) {
	cfg, toTarget := compileFixture(t)

	// Without --allow-lossy: agents/a.md has no openish rule → refuse.
	res, err := Compile(cfg, "claudeish", "openish", false, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Lossy) == 0 || res.Emitted != nil {
		t.Fatalf("want lossy gate to block, got %+v", res)
	}
	if _, err := os.Stat(filepath.Join(toTarget, "skills", "x", "S.md")); !os.IsNotExist(err) {
		t.Errorf("lossy gate must not write anything (err=%v)", err)
	}

	// With --allow-lossy: skills land, agents reported lossy.
	res, err = Compile(cfg, "claudeish", "openish", true, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Lossy) == 0 {
		t.Error("lossy list should still be reported")
	}
	blob, err := os.ReadFile(filepath.Join(toTarget, "skills", "x", "S.md"))
	if err != nil || string(blob) != "skill S" {
		t.Errorf("emitted skill = %q, %v", blob, err)
	}
}

func TestCompileCleanConversion(t *testing.T) {
	cfg, toTarget := compileFixture(t)
	// Same rule shapes on both sides → lossless... except claudeish has
	// agents which openish lacks; use the reverse direction instead:
	// openish (skills only) → claudeish consumes skills too.
	populateTarget(t, filepath.Join(filepath.Dir(toTarget), "dot-opencode"), map[string]string{
		"skills/y/S.md": "skill Y",
	})
	res, err := Compile(cfg, "openish", "claudeish", false, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Lossy) != 0 {
		t.Fatalf("clean conversion flagged lossy: %v", res.Lossy)
	}
	if res.Emitted == nil {
		t.Fatal("nothing emitted")
	}
	blob, err := os.ReadFile(filepath.Join(filepath.Dir(toTarget), "dot-claude", "skills", "y", "S.md"))
	if err != nil || string(blob) != "skill Y" {
		t.Errorf("emitted = %q, %v", blob, err)
	}
}

func TestCompileValidatesAdapters(t *testing.T) {
	cfg, _ := compileFixture(t)
	if _, err := Compile(cfg, "claudeish", "claudeish", false, Options{}); err == nil {
		t.Error("same from/to accepted")
	}
	if _, err := Compile(cfg, "nope", "openish", false, Options{}); err == nil {
		t.Error("unknown adapter accepted")
	}
}

func TestCompileLeavesRealDriftStateUntouched(t *testing.T) {
	// Compile reads the from-adapter's real target dir. If its import phase
	// recorded those files in the real drift store, a hand-edited target
	// would be re-baselined to its edited content and the next real push
	// would overwrite it without a prompt.
	root := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("LocalAppData", t.TempDir())

	storeAbs := filepath.Join(root, "store")
	fromTarget := filepath.Join(root, "dot-claude")
	toTarget := filepath.Join(root, "dot-open")
	populateTarget(t, storeAbs, map[string]string{"agents/a.md": "v1"})
	if err := os.MkdirAll(toTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	rule := []*rules.Rule{{From: rules.FromSpec{"agents/*.md"}, To: "agents/{filename}"}}
	cfg := config.NewDefault(config.ScopeUser, storeAbs, root, map[string]*config.Adapter{
		"claudeish": {Target: fromTarget, Rules: rule},
		"openish":   {Target: toTarget, Rules: rule},
	})

	// Real push seeds the baseline, then the user hand-edits the target.
	if _, err := Push(cfg, Options{Adapters: []string{"claudeish"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fromTarget, "agents", "a.md"), []byte("hand-edit"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Compile(cfg, "claudeish", "openish", true, Options{Force: true}); err != nil {
		t.Fatal(err)
	}

	// The pending drift must still be detected: push with no resolver has to
	// surface the hand-edit as a conflict, not overwrite it.
	got, err := Push(cfg, Options{Adapters: []string{"claudeish"}})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Action != ActionConflict {
		t.Errorf("post-compile push = %v, want Conflict — compile re-baselined the real drift state", got[0].Action)
	}
	data, _ := os.ReadFile(filepath.Join(fromTarget, "agents", "a.md"))
	if string(data) != "hand-edit" {
		t.Errorf("hand edit overwritten: %q", data)
	}
}
