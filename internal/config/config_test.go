package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadUserVersionMismatch(t *testing.T) {
	writeUserManifest(t, "version: 99\nadapters: {}\n")
	if _, err := LoadUser(); err == nil {
		t.Errorf("expected unsupported-version error")
	}
}

func TestUserStoreDirDefaultsToHomeFriday(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("FRIDAY_HOME", "")

	got, err := UserStoreDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".friday"); got != want {
		t.Errorf("UserStoreDir() = %q, want %q", got, want)
	}
}

// TestUserStoreDirHonoursFridayHome guards D29: FRIDAY_HOME is the store
// dir itself -- following RUSTUP_HOME/CARGO_HOME, not a parent directory
// friday appends ".friday" to.
func TestUserStoreDirHonoursFridayHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	override := filepath.Join(t.TempDir(), "friday-store")
	t.Setenv("FRIDAY_HOME", override)

	got, err := UserStoreDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != override {
		t.Errorf("UserStoreDir() = %q, want %q (no .friday appended)", got, override)
	}
}

// TestUserStoreDirResolvesFridayHome: the value is an env var, not a
// manifest path, so no shell has expanded it. A tilde must expand the same
// way it does in a manifest, and a relative value must be anchored — the
// store dir seeds ManifestPath and every drift key, so a CWD-dependent
// StoreDir means a different store per invocation.
func TestUserStoreDirResolvesFridayHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	t.Run("tilde expands", func(t *testing.T) {
		t.Setenv("FRIDAY_HOME", "~/store")
		got, err := UserStoreDir()
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(home, "store"); got != want {
			t.Errorf("UserStoreDir() = %q, want %q (no literal ~ directory)", got, want)
		}
	})

	t.Run("relative is made absolute", func(t *testing.T) {
		t.Setenv("FRIDAY_HOME", "store")
		got, err := UserStoreDir()
		if err != nil {
			t.Fatal(err)
		}
		if !filepath.IsAbs(got) {
			t.Errorf("UserStoreDir() = %q, want an absolute path", got)
		}
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(cwd, "store"); got != want {
			t.Errorf("UserStoreDir() = %q, want %q", got, want)
		}
	})
}

func TestResolvePathAbsolute(t *testing.T) {
	cfg := &Config{TargetRoot: filepath.Join(t.TempDir(), "root")}
	abs := mustAbs(t)
	if got := cfg.resolvePath(abs); got != abs {
		t.Errorf("resolvePath(abs) = %q, want %q", got, abs)
	}
}

func TestResolvePathRelative(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	cfg := &Config{TargetRoot: root}
	got := cfg.resolvePath(".claude")
	want := filepath.Join(root, ".claude")
	if got != want {
		t.Errorf("resolvePath(rel) = %q, want %q", got, want)
	}
}

func TestResolvePathTilde(t *testing.T) {
	cfg := &Config{TargetRoot: "/should-not-be-used"}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir on this platform")
	}
	got := cfg.resolvePath("~/foo")
	want := filepath.Join(home, "foo")
	if got != want {
		t.Errorf("resolvePath(~/) = %q, want %q", got, want)
	}
}

func TestSelectAdaptersValidates(t *testing.T) {
	cfg := &Config{Adapters: map[string]*Adapter{"claude": {Target: ".claude"}}}
	if _, err := cfg.SelectAdapters([]string{"missing"}); err == nil {
		t.Errorf("expected error for unknown adapter")
	}
	got, err := cfg.SelectAdapters(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "claude" {
		t.Errorf("got %v", got)
	}
}

func TestSaveRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("FRIDAY_HOME", "")
	storeDir := filepath.Join(home, ".friday")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{
		Version:      1,
		ManifestPath: filepath.Join(storeDir, "friday.yaml"),
		Adapters:     map[string]*Adapter{"claude": {Target: ".claude"}},
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	out, err := LoadUser()
	if err != nil {
		t.Fatal(err)
	}
	if out.Adapters["claude"].Target != ".claude" {
		t.Errorf("round-trip lost target")
	}
}

// TestValidateSourceName is the security-critical table: the name is joined
// onto SourcesDir to build a clone destination, so anything that can escape
// .sources/ must be rejected here. `friday source add ../../pwned <url>`
// once exited 0 and cloned two levels above the store.
func TestValidateSourceName(t *testing.T) {
	cases := []struct {
		name    string
		wantErr bool
	}{
		{"team", false},
		{"team-rules", false},
		{"0team", false},
		{"", true},
		{"local", true}, // reserved for the primary
		{"Team", true},
		{"Team_Rules", true}, // benign-looking; used to brick the manifest
		{"team_rules", true},
		{"-team", true},
		{"team.rules", true},
		{"..", true},
		{"../x", true},
		{"../../pwned", true},
		{"..\\..\\pwned", true},
		{"a/b", true},
		{"a\\b", true},
		{"/abs", true},
		{`C:\x`, true},
		{"C:", true},
		{".hidden", true},
		{"team ", true},
		{"team\n", true},
		{".", true},
		{" ", true},
		{"C:x", true},   // drive-relative, no separator
		{"team.", true}, // Windows strips a trailing dot
		// Windows resolves a DOS device name anywhere in a path: mkdir
		// reports success and creates nothing, so the source would be
		// permanently indistinguishable from an empty one.
		{"con", true},
		{"prn", true},
		{"aux", true},
		{"nul", true},
		{"com1", true},
		{"com9", true},
		{"lpt1", true},
		// A device name is only reserved on its own; as a prefix it is fine.
		{"console", false},
		{"nullify", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := ValidateSourceName(c.name); (err != nil) != c.wantErr {
				t.Errorf("ValidateSourceName(%q) err = %v, wantErr %v", c.name, err, c.wantErr)
			}
		})
	}
}

func TestValidateSources(t *testing.T) {
	cases := []struct {
		name    string
		specs   []*SourceSpec
		wantErr bool
	}{
		{"nil is valid", nil, false},
		{"single valid entry", []*SourceSpec{{Name: "team", URL: "https://example.com/team.git"}}, false},
		{"ref is optional", []*SourceSpec{{Name: "team", URL: "https://example.com/team.git", Ref: "v1"}}, false},
		{"nil entry", []*SourceSpec{nil}, true},
		{"reserved name local", []*SourceSpec{{Name: "local", URL: "https://example.com/x.git"}}, true},
		{"uppercase name rejected", []*SourceSpec{{Name: "Team", URL: "https://example.com/x.git"}}, true},
		{"leading dash rejected", []*SourceSpec{{Name: "-team", URL: "https://example.com/x.git"}}, true},
		{"empty name rejected", []*SourceSpec{{Name: "", URL: "https://example.com/x.git"}}, true},
		{"parent traversal rejected", []*SourceSpec{{Name: "..", URL: "https://example.com/x.git"}}, true},
		{"relative traversal rejected", []*SourceSpec{{Name: "../../pwned", URL: "https://example.com/x.git"}}, true},
		{"slash rejected", []*SourceSpec{{Name: "a/b", URL: "https://example.com/x.git"}}, true},
		{"backslash rejected", []*SourceSpec{{Name: `a\b`, URL: "https://example.com/x.git"}}, true},
		{"absolute rejected", []*SourceSpec{{Name: "/abs", URL: "https://example.com/x.git"}}, true},
		{"drive letter rejected", []*SourceSpec{{Name: `C:\x`, URL: "https://example.com/x.git"}}, true},
		{"dotfile rejected", []*SourceSpec{{Name: ".hidden", URL: "https://example.com/x.git"}}, true},
		{"underscore rejected", []*SourceSpec{{Name: "Team_Rules", URL: "https://example.com/x.git"}}, true},
		{"duplicate name rejected", []*SourceSpec{
			{Name: "team", URL: "https://example.com/a.git"},
			{Name: "team", URL: "https://example.com/b.git"},
		}, true},
		{"empty url rejected", []*SourceSpec{{Name: "team", URL: ""}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateSources(c.specs)
			if (err != nil) != c.wantErr {
				t.Errorf("validateSources(%+v) err = %v, wantErr %v", c.specs, err, c.wantErr)
			}
		})
	}
}

// TestSaveRejectsUnloadableSources is the un-brick guard: Save used to
// marshal Sources straight out while load validated strictly, so `friday
// source add Team_Rules <url>` persisted a manifest every later command —
// including `source rm`, the only recovery — refused to open. Save must
// reject it, and the file on disk must be left exactly as it was.
func TestSaveRejectsUnloadableSources(t *testing.T) {
	good := "version: 1\nadapters:\n  claude:\n    target: .claude\n"
	manifestPath := filepath.Join(writeUserManifest(t, good), "friday.yaml")

	for _, name := range []string{"Team_Rules", "../../pwned"} {
		t.Run(name, func(t *testing.T) {
			cfg := &Config{
				Version:      1,
				ManifestPath: manifestPath,
				Adapters:     map[string]*Adapter{"claude": {Target: ".claude"}},
				Sources:      []*SourceSpec{{Name: name, URL: "https://example.com/x.git"}},
			}
			if err := cfg.Save(); err == nil {
				t.Fatalf("Save() persisted source name %q that load rejects", name)
			}
			got, err := os.ReadFile(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != good {
				t.Errorf("Save() modified the manifest on the reject path:\n%s", got)
			}
			if _, err := LoadUser(); err != nil {
				t.Errorf("store is no longer loadable: %v", err)
			}
		})
	}
}

// TestLoadUserWithSources is the end-to-end path TestValidateSources'
// table doesn't cover: a real friday.yaml with a `sources:` block parses
// into Config.Sources and SourcesDir points under the store.
func TestLoadUserWithSources(t *testing.T) {
	storeDir := writeUserManifest(t, "version: 1\nadapters: {}\nsources:\n  - name: team\n    url: https://example.com/team.git\n    ref: v1.2.3\n")
	cfg, err := LoadUser()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Sources) != 1 {
		t.Fatalf("cfg.Sources = %+v, want 1 entry", cfg.Sources)
	}
	got := cfg.Sources[0]
	if got.Name != "team" || got.URL != "https://example.com/team.git" || got.Ref != "v1.2.3" {
		t.Errorf("cfg.Sources[0] = %+v, want team/https://example.com/team.git/v1.2.3", got)
	}
	wantSourcesDir := filepath.Join(storeDir, ".sources")
	if got := cfg.SourcesDir(); got != wantSourcesDir {
		t.Errorf("SourcesDir() = %q, want %q", got, wantSourcesDir)
	}
}

// TestLoadUserRejectsInvalidSource confirms an invalid sources: entry
// surfaces as a load error rather than a silently broken adapter set.
func TestLoadUserRejectsInvalidSource(t *testing.T) {
	writeUserManifest(t, "version: 1\nadapters: {}\nsources:\n  - name: local\n    url: https://example.com/x.git\n")
	if _, err := LoadUser(); err == nil {
		t.Error("expected error for source named \"local\"")
	}
}

// writeUserManifest sets up an isolated user store containing manifest and
// returns its store dir.
func writeUserManifest(t *testing.T, manifest string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("FRIDAY_HOME", "")
	storeDir := filepath.Join(home, ".friday")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "friday.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return storeDir
}

// TestLoadUserDegradesRetiredStrategy is the un-brick guard for every store
// created before v1.0: `friday init` seeded md-to-toml/md-to-json into
// friday.yaml, so refusing to load such a manifest killed every command
// including the one that would repair it. The rule is dropped with a
// warning; the rest of the adapter still works.
func TestLoadUserDegradesRetiredStrategy(t *testing.T) {
	writeUserManifest(t, `version: 1
adapters:
  codex:
    target: .codex
    rules:
      - from: agents/*.md
        to: agents/{stem}.toml
        strategy: md-to-toml
      - from: core.md
        to: AGENTS.md
    project_rules:
      - from: commands/*.md
        to: prompts/{stem}.json
        strategy: md-to-json
`)
	cfg, err := LoadUser()
	if err != nil {
		t.Fatalf("LoadUser() err = %v; a retired strategy must degrade, not fail", err)
	}
	codex := cfg.Adapters["codex"]
	if len(codex.Rules) != 1 || codex.Rules[0].To != "AGENTS.md" {
		t.Errorf("codex.Rules = %+v, want only the surviving core.md rule", codex.Rules)
	}
	if len(codex.ProjectRules) != 0 {
		t.Errorf("codex.ProjectRules = %+v, want the retired rule dropped", codex.ProjectRules)
	}
	// One note per dropped rule, plus the one that matters most: the adapter's
	// project_rules are now empty, so `friday setup` will apply nothing for it
	// however many times it is run.
	if len(cfg.Warnings) != 3 {
		t.Fatalf("Warnings = %v, want one per dropped rule plus the emptied-rule-set note", cfg.Warnings)
	}
	joined := strings.Join(cfg.Warnings, "\n")
	for _, want := range []string{`rule[0]`, "md-to-toml", "codex-agent", `project_rule[0]`, "md-to-json", "gemini-command", "friday migrate", "every project_rule was retired"} {
		if !strings.Contains(joined, want) {
			t.Errorf("Warnings missing %q:\n%s", want, joined)
		}
	}
}

// TestLoadUserWarnsLegacyAdapterName: a renamed adapter key still loads (its
// rules are valid), but the store silently stops tracking the agent under
// its current name, so the rename must be reported.
func TestLoadUserWarnsLegacyAdapterName(t *testing.T) {
	writeUserManifest(t, `version: 1
adapters:
  antigravity:
    target: .gemini
    rules:
      - from: core.md
        to: GEMINI.md
`)
	cfg, err := LoadUser()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(cfg.Warnings, "\n")
	for _, want := range []string{"antigravity", "gemini", "friday migrate"} {
		if !strings.Contains(joined, want) {
			t.Errorf("Warnings missing %q:\n%s", want, joined)
		}
	}
}

// TestLoadUserRejectsUnknownStrategy separates the two cases: a typo is a
// hard error (the user wrote it and can fix it), unlike a retired strategy
// friday's own installer seeded.
func TestLoadUserRejectsUnknownStrategy(t *testing.T) {
	writeUserManifest(t, `version: 1
adapters:
  codex:
    target: .codex
    rules:
      - from: core.md
        to: AGENTS.md
        strategy: mrege-json
`)
	if _, err := LoadUser(); err == nil {
		t.Error("expected a hard error for a misspelled strategy")
	}
}

func mustAbs(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return `C:\never\used`
	}
	return "/never/used"
}
