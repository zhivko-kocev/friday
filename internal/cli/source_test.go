package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhivko-kocev/friday/internal/config"
)

// manifestWithComments is the shape AGENTS.md promises friday.yaml keeps: a
// hand-edited, git-tracked file with explanatory comments and an authored key
// order. `source add`/`rm` must leave every byte of it alone except the
// `sources:` block they own.
const manifestWithComments = `# friday store manifest — hand-edited, git-tracked.
version: 1
adapters:
  # Claude Code reads CLAUDE.md at the root of ~/.claude.
  claude:
    target: .claude
    rules:
      - from: core.md
        to: CLAUDE.md # the entry file
  codex:
    target: .codex
    rules:
      - from: core.md
        to: AGENTS.md
`

// TestValidateSourceSpec is the guard for the `source add` BLOCKER: the
// command validated nothing before cloning and saving, so "../../pwned"
// cloned outside the store (filepath.Join cleans the "..' away) and
// "Team_Rules" saved a manifest config.load then refused to open — bricking
// every later command, including the `source rm` that would have undone it.
// validateSourceSpec runs before the store is even loaded, so a rejection
// here is a rejection before anything touches the filesystem.
func TestValidateSourceSpec(t *testing.T) {
	cases := []struct {
		name    string
		spec    config.SourceSpec
		wantErr string // substring; "" = must be accepted
	}{
		{
			name: "plain slug",
			spec: config.SourceSpec{Name: "team", URL: "git@github.com:org/team.git"},
		},
		{
			name: "slug with digits and dashes",
			spec: config.SourceSpec{Name: "team-2", URL: "https://example.com/t.git", Ref: "v1.2.0"},
		},
		{
			name:    "path traversal escapes the store",
			spec:    config.SourceSpec{Name: "../../pwned", URL: "https://example.com/t.git"},
			wantErr: "must match",
		},
		{
			name:    "single dot-dot segment",
			spec:    config.SourceSpec{Name: "..", URL: "https://example.com/t.git"},
			wantErr: "must match",
		},
		{
			name:    "uppercase and underscore brick config.load",
			spec:    config.SourceSpec{Name: "Team_Rules", URL: "https://example.com/t.git"},
			wantErr: "must match",
		},
		{
			name:    "reserved primary name",
			spec:    config.SourceSpec{Name: "local", URL: "https://example.com/t.git"},
			wantErr: "reserved",
		},
		{
			name:    "empty name",
			spec:    config.SourceSpec{Name: "", URL: "https://example.com/t.git"},
			wantErr: "must match",
		},
		{
			name:    "empty url",
			spec:    config.SourceSpec{Name: "team", URL: ""},
			wantErr: "url is empty",
		},
		{
			name:    "flag-like url",
			spec:    config.SourceSpec{Name: "team", URL: "--upload-pack=evil"},
			wantErr: "refusing to pass as flag",
		},
		{
			name:    "flag-like ref",
			spec:    config.SourceSpec{Name: "team", URL: "https://example.com/t.git", Ref: "--recurse-submodules"},
			wantErr: "refusing to pass as flag",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateSourceSpec(&c.spec)
			switch {
			case c.wantErr == "" && err != nil:
				t.Errorf("validateSourceSpec(%+v) = %v, want accepted", c.spec, err)
			case c.wantErr != "" && err == nil:
				t.Errorf("validateSourceSpec(%+v) accepted it, want %q", c.spec, c.wantErr)
			case c.wantErr != "" && !strings.Contains(err.Error(), c.wantErr):
				t.Errorf("validateSourceSpec(%+v) = %v, want an error containing %q", c.spec, err, c.wantErr)
			}
		})
	}
}

// TestPersistSourcesPreservesManifest is the guard for the comment-and-order
// MAJOR: `source add`/`rm` used cfg.Save(), a whole-struct yaml.Marshal that
// cannot emit comments and re-emits every preset rule the user never touched
// — one `source add` became a hundred-line reformat that `friday remote push`
// then committed to the team store.
func TestPersistSourcesPreservesManifest(t *testing.T) {
	setDriftEnv(t)
	home, _ := os.UserHomeDir()
	store := filepath.Join(home, ".friday")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store, config.ManifestName)
	if err := os.WriteFile(path, []byte(manifestWithComments), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{Version: 1, ManifestPath: path, StoreDir: store}

	// A no-op persist establishes what the yaml round-trip alone produces, so
	// the assertions below measure this command's edit and nothing else.
	if err := persistSources(cfg); err != nil {
		t.Fatalf("persistSources (no sources): %v", err)
	}
	baseline := readFileString(t, path)
	for _, want := range []string{
		"# friday store manifest — hand-edited, git-tracked.",
		"# Claude Code reads CLAUDE.md at the root of ~/.claude.",
		"# the entry file",
	} {
		if !strings.Contains(baseline, want) {
			t.Errorf("comment %q did not survive the manifest edit:\n%s", want, baseline)
		}
	}
	if strings.Index(baseline, "claude:") > strings.Index(baseline, "codex:") {
		t.Errorf("authored adapter order was not preserved:\n%s", baseline)
	}

	cfg.Sources = []*config.SourceSpec{{Name: "team", URL: "git@github.com:org/team.git"}}
	if err := persistSources(cfg); err != nil {
		t.Fatalf("persistSources (add): %v", err)
	}
	added := readFileString(t, path)
	if !strings.HasPrefix(added, baseline) {
		t.Errorf("adding a source rewrote content above `sources:`:\n%s", added)
	}
	if !strings.Contains(added, "name: team") {
		t.Errorf("added source is missing from the manifest:\n%s", added)
	}
	loaded, err := config.LoadUser()
	if err != nil {
		t.Fatalf("the manifest `source add` wrote is unloadable: %v", err)
	}
	if len(loaded.Sources) != 1 || loaded.Sources[0].Name != "team" {
		t.Errorf("loaded sources = %+v, want one named \"team\"", loaded.Sources)
	}

	cfg.Sources = nil
	if err := persistSources(cfg); err != nil {
		t.Fatalf("persistSources (rm): %v", err)
	}
	if removed := readFileString(t, path); removed != baseline {
		t.Errorf("add+rm was not a round trip:\ngot:\n%s\nwant:\n%s", removed, baseline)
	}
}

// TestPersistSourcesAnnouncesGeneratedManifest covers the store documented as
// the common case — content but no friday.yaml. loadUserOrDefault hands back
// the full built-in preset set with ManifestPath already filled in, so a bare
// Save() silently materializes a six-preset manifest that `remote push` then
// commits. Creating it is fine; doing so silently is not.
func TestPersistSourcesAnnouncesGeneratedManifest(t *testing.T) {
	store := t.TempDir()
	path := filepath.Join(store, config.ManifestName)
	cfg := &config.Config{
		Version:      1,
		Adapters:     presetAdapters(),
		ManifestPath: path,
		StoreDir:     store,
		Sources:      []*config.SourceSpec{{Name: "team", URL: "https://example.com/t.git"}},
	}
	out := captureStdout(t, func() {
		if err := persistSources(cfg); err != nil {
			t.Errorf("persistSources: %v", err)
		}
	})
	if !strings.Contains(out, "creating "+path) {
		t.Errorf("generating a manifest was not announced:\n%s", out)
	}
	if !strings.Contains(readFileString(t, path), "name: team") {
		t.Error("generated manifest is missing the source it was written for")
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
