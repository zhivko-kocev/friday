package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/presets"
)

// staleManifest is what the release before v1.0 seeded: the adapter names it
// used (antigravity, pi) and the strategies it wrote (md-to-json, md-to-toml).
// Before config.load learned to degrade, every friday command on this file
// exited 1 with no way forward; `friday migrate` is the documented way out.
const staleManifest = `# friday store manifest — hand-edited, git-tracked.
version: 1
adapters:
  # Google's IDE agent, before the rename.
  antigravity:
    target: .antigravity
    rules:
      - from: core.md
        to: AGENTS.md
      - from: commands/*.md
        to: config/commands/{filename}
        strategy: md-to-json
  claude:
    target: .claude
    rules:
      - from: core.md
        to: CLAUDE.md # the entry file
  codex:
    target: .codex
    rules:
      - from: agents/*.md
        to: config.toml
        strategy: md-to-toml
  pi:
    target: .pi/agent
    rules:
      - from: core.md
        to: AGENTS.md
`

// TestMigrateStaleManifest is the end-to-end regression for the top-severity
// finding: a manifest seeded by the previous release names adapters and
// strategies that no longer exist. Migrating it must produce a manifest the
// current friday loads with no warnings, keep every comment and adapter the
// user authored, and do nothing at all the second time.
func TestMigrateStaleManifest(t *testing.T) {
	path := writeStaleManifest(t)
	before := readFileString(t, path)

	// A dry run reports the same work but must not touch the file.
	preview, err := migrateManifest(path, true)
	if err != nil {
		t.Fatalf("migrateManifest --dry-run: %v", err)
	}
	if len(preview) == 0 {
		t.Fatal("dry run reported no changes for a stale manifest")
	}
	if readFileString(t, path) != before {
		t.Error("dry run rewrote the manifest")
	}

	changes, err := migrateManifest(path, false)
	if err != nil {
		t.Fatalf("migrateManifest: %v", err)
	}
	if len(changes) != len(preview) {
		t.Errorf("migration made %d changes, dry run predicted %d:\n%v\n%v",
			len(changes), len(preview), changes, preview)
	}

	cfg, err := config.LoadUser()
	if err != nil {
		t.Fatalf("migrated manifest is unloadable: %v", err)
	}
	if len(cfg.Warnings) != 0 {
		t.Errorf("migrated manifest still warns: %v", cfg.Warnings)
	}
	for _, legacy := range []string{"antigravity", "pi"} {
		if _, ok := cfg.Adapters[legacy]; ok {
			t.Errorf("legacy adapter %q survived the migration", legacy)
		}
	}
	for legacy, canonical := range config.LegacyAdapterNames {
		a, ok := cfg.Adapters[canonical]
		if !ok {
			t.Fatalf("%q was not renamed to %q", legacy, canonical)
		}
		p, _ := presets.Get(canonical)
		if a.Target != p.Target {
			t.Errorf("adapter %q target = %q, want the preset's %q", canonical, a.Target, p.Target)
		}
		if len(a.Rules) != len(p.Rules) {
			t.Errorf("adapter %q has %d rules, want the preset's %d", canonical, len(a.Rules), len(p.Rules))
		}
	}
	// codex was never renamed, so only its retired rule goes; the adapter
	// itself must stay, and an adapter with nothing stale must be untouched.
	codex, ok := cfg.Adapters["codex"]
	if !ok {
		t.Fatal("codex adapter was dropped")
	}
	if len(codex.Rules) != 0 {
		t.Errorf("codex kept %d rules, want its only (md-to-toml) rule dropped", len(codex.Rules))
	}
	claude, ok := cfg.Adapters["claude"]
	if !ok || claude.Target != ".claude" || len(claude.Rules) != 1 {
		t.Errorf("untouched adapter claude was modified: %+v", claude)
	}

	after := readFileString(t, path)
	for _, want := range []string{
		"# friday store manifest — hand-edited, git-tracked.",
		"# the entry file",
	} {
		if !strings.Contains(after, want) {
			t.Errorf("comment %q did not survive the migration:\n%s", want, after)
		}
	}

	// Idempotence: nothing left to do, and not a byte rewritten.
	again, err := migrateManifest(path, false)
	if err != nil {
		t.Fatalf("second migrateManifest: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("second migration reported changes: %v", again)
	}
	if readFileString(t, path) != after {
		t.Error("second migration rewrote an already-current manifest")
	}
}

// TestMigrateCollidingLegacyAdapter covers the manifest that already carries
// both names — a user who added `gemini` by hand but never removed
// `antigravity`. Renaming would clobber the entry they actually maintain, so
// the stale one is dropped instead.
func TestMigrateCollidingLegacyAdapter(t *testing.T) {
	setDriftEnv(t)
	path := writeManifest(t, `version: 1
adapters:
  antigravity:
    target: .antigravity
    rules:
      - from: core.md
        to: AGENTS.md
  gemini:
    target: .gemini
    rules:
      - from: core.md
        to: GEMINI.md
`)
	changes, err := migrateManifest(path, false)
	if err != nil {
		t.Fatalf("migrateManifest: %v", err)
	}
	if len(changes) != 1 || !strings.Contains(changes[0], "superseded") {
		t.Fatalf("changes = %v, want one 'superseded' drop", changes)
	}
	cfg, err := config.LoadUser()
	if err != nil {
		t.Fatalf("migrated manifest is unloadable: %v", err)
	}
	if _, ok := cfg.Adapters["antigravity"]; ok {
		t.Error("colliding legacy adapter was kept")
	}
	if g := cfg.Adapters["gemini"]; g == nil || g.Target != ".gemini" {
		t.Errorf("the user's own gemini adapter was overwritten: %+v", g)
	}
}

func writeStaleManifest(t *testing.T) string {
	t.Helper()
	setDriftEnv(t)
	return writeManifest(t, staleManifest)
}

func writeManifest(t *testing.T, body string) string {
	t.Helper()
	home, _ := os.UserHomeDir()
	store := filepath.Join(home, ".friday")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store, config.ManifestName)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
