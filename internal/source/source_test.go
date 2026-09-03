package source

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/rules"
)

// TestLoadSingleSourceIsJustPrimary is the regression guard: an
// unconfigured store (no `sources:` in friday.yaml) must resolve to exactly
// one root, so every existing single-source behavior is unaffected.
func TestLoadSingleSourceIsJustPrimary(t *testing.T) {
	cfg := &config.Config{StoreDir: t.TempDir()}
	set, err := Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(set) != 1 {
		t.Fatalf("Load() = %+v, want exactly the primary", set)
	}
	want := Source{Name: "local", Dir: cfg.StoreDir, Primary: true, Cloned: true}
	if set[0] != want {
		t.Errorf("set[0] = %+v, want %+v", set[0], want)
	}
	if got := set.Uncloned(); len(got) != 0 {
		t.Errorf("Uncloned() = %v, want none", got)
	}
}

// TestLoadOrdersSourcesBeforePrimary confirms manifest sources keep their
// declared order and the primary is always appended last (highest
// precedence).
func TestLoadOrdersSourcesBeforePrimary(t *testing.T) {
	cfg := &config.Config{
		StoreDir: t.TempDir(),
		Sources: []*config.SourceSpec{
			{Name: "team", URL: "https://example.com/team.git"},
			{Name: "pinned", URL: "https://example.com/pinned.git", Ref: "v1.2.3"},
		},
	}
	for _, name := range []string{"team", "pinned"} {
		if err := os.MkdirAll(filepath.Join(cfg.SourcesDir(), name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	set, err := Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(set) != 3 {
		t.Fatalf("Load() = %+v, want 3 entries", set)
	}
	wantTeam := Source{Name: "team", Dir: filepath.Join(cfg.SourcesDir(), "team"), URL: "https://example.com/team.git", Cloned: true}
	wantPinned := Source{Name: "pinned", Dir: filepath.Join(cfg.SourcesDir(), "pinned"), URL: "https://example.com/pinned.git", Ref: "v1.2.3", Cloned: true}
	wantPrimary := Source{Name: "local", Dir: cfg.StoreDir, Primary: true, Cloned: true}
	if set[0] != wantTeam {
		t.Errorf("set[0] = %+v, want %+v", set[0], wantTeam)
	}
	if set[1] != wantPinned {
		t.Errorf("set[1] = %+v, want %+v", set[1], wantPinned)
	}
	if set[2] != wantPrimary {
		t.Errorf("set[2] (primary) = %+v, want %+v", set[2], wantPrimary)
	}
}

// TestLoadFlagsUnclonedSource is the second-machine case: .sources/ is
// gitignored by design, so a manifest carrying `sources:` normally arrives
// without its clones. An absent source must be visible, not silently
// resolve to an empty root that renders a partial store as if it succeeded.
func TestLoadFlagsUnclonedSource(t *testing.T) {
	cfg := &config.Config{
		StoreDir: t.TempDir(),
		Sources: []*config.SourceSpec{
			{Name: "team", URL: "https://example.com/team.git"},
			{Name: "org", URL: "https://example.com/org.git"},
		},
	}
	if err := os.MkdirAll(filepath.Join(cfg.SourcesDir(), "org"), 0o755); err != nil {
		t.Fatal(err)
	}
	set, err := Load(cfg)
	if err != nil {
		t.Fatalf("Load() err = %v; an uncloned source is not a load failure", err)
	}
	if set[0].Cloned {
		t.Errorf("set[0] = %+v, want Cloned false", set[0])
	}
	if !set[1].Cloned {
		t.Errorf("set[1] = %+v, want Cloned true", set[1])
	}
	got := set.Uncloned()
	if len(got) != 1 || got[0] != "team" {
		t.Errorf("Uncloned() = %v, want [team]", got)
	}
}

// TestLoadTreatsNonDirectoryAsUncloned: a path that exists but is not a
// directory cannot be a source root, so it must not be reported as cloned —
// otherwise every glob against it fails with a raw OS error instead of the
// "not cloned" story the user can act on.
func TestLoadTreatsNonDirectoryAsUncloned(t *testing.T) {
	cfg := &config.Config{
		StoreDir: t.TempDir(),
		Sources:  []*config.SourceSpec{{Name: "team", URL: "https://example.com/team.git"}},
	}
	writeFile(t, cfg.SourcesDir(), "team", "not a directory")
	set, err := Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if set[0].Cloned {
		t.Errorf("set[0] = %+v, want Cloned false for a non-directory", set[0])
	}
	if got := set.Uncloned(); len(got) != 1 || got[0] != "team" {
		t.Errorf("Uncloned() = %v, want [team]", got)
	}
}

func TestSetRoots(t *testing.T) {
	set := Set{{Name: "team", Dir: "/a"}, {Name: "local", Dir: "/b", Primary: true}}
	want := []rules.Root{{Name: "team", Dir: "/a"}, {Name: "local", Dir: "/b"}}
	got := set.Roots()
	if len(got) != len(want) {
		t.Fatalf("Roots() = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Roots()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
