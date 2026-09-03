package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/rules"
)

func lintFixture(t *testing.T, files map[string]string, adapters map[string]*config.Adapter) *config.Config {
	t.Helper()
	store := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(store, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("LocalAppData", t.TempDir())
	if adapters == nil {
		adapters = map[string]*config.Adapter{}
	}
	return config.NewDefault(config.ScopeUser, store, t.TempDir(), adapters)
}

func rulesOf(fs []Finding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Rule)
	}
	return out
}

func TestLintCleanStore(t *testing.T) {
	cfg := lintFixture(t, map[string]string{
		"core.md":          "# fine\n\nsee [rules](rules/general.md)\n",
		"rules/general.md": "---\ndescription: ok\n---\n\nbody\n",
	}, nil)
	findings, err := Run(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Errorf("clean store flagged: %+v", findings)
	}
}

func TestLintMalformedFrontmatter(t *testing.T) {
	cfg := lintFixture(t, map[string]string{
		"rules/bad.md": "---\n: : bad yaml [\n---\n\nbody\n",
	}, nil)
	findings, _ := Run(cfg)
	if len(findings) != 1 || findings[0].Rule != "frontmatter" {
		t.Errorf("findings = %+v", findings)
	}
}

func TestLintBrokenRef(t *testing.T) {
	cfg := lintFixture(t, map[string]string{
		"core.md":    "see [missing](standards/nope.md) and [ok](rules/a.md)\n",
		"rules/a.md": "x",
	}, nil)
	findings, _ := Run(cfg)
	if len(findings) != 1 || findings[0].Rule != "broken-ref" || !strings.Contains(findings[0].Msg, "standards/nope.md") {
		t.Errorf("findings = %+v", findings)
	}
}

func TestLintOversized(t *testing.T) {
	cfg := lintFixture(t, map[string]string{
		"big.md": strings.Repeat("x", maxFileSize+1),
	}, nil)
	findings, _ := Run(cfg)
	got := rulesOf(findings)
	if len(got) != 1 || got[0] != "oversized" {
		t.Errorf("findings = %+v", findings)
	}
}

func TestLintLongInstructions(t *testing.T) {
	long := strings.Repeat("line\n", maxInstructionLines+5)
	cfg := lintFixture(t, map[string]string{
		"core.md":          long, // instruction file, over limit → flagged
		"rules/general.md": long, // rule file, over limit → flagged
		"notes/scratch.md": long, // not an instruction file → ignored
	}, nil)
	findings, _ := Run(cfg)
	got := 0
	for _, f := range findings {
		if f.Rule == "long-instructions" {
			got++
			if f.Severity != Warn {
				t.Errorf("long-instructions severity = %v, want warn", f.Severity)
			}
		}
	}
	if got != 2 {
		t.Errorf("long-instructions fired %d times, want 2 (core.md + rules/general.md): %+v", got, findings)
	}
}

func TestLintSkillDescription(t *testing.T) {
	cfg := lintFixture(t, map[string]string{
		"skills/good/SKILL.md":  "---\ndescription: Use this when preparing a release changelog from git history\n---\nbody\n",
		"skills/empty/SKILL.md": "---\nname: empty\n---\nbody\n",
		"skills/terse/SKILL.md": "---\ndescription: helps\n---\nbody\n",
	}, nil)
	findings, _ := Run(cfg)
	flagged := map[string]bool{}
	for _, f := range findings {
		if f.Rule == "skill-description" {
			flagged[f.Path] = true
		}
	}
	if flagged["skills/good/SKILL.md"] {
		t.Errorf("good skill wrongly flagged")
	}
	if !flagged["skills/empty/SKILL.md"] || !flagged["skills/terse/SKILL.md"] {
		t.Errorf("missing/terse descriptions not flagged: %+v", findings)
	}
}

func TestLintIgnoreConfig(t *testing.T) {
	files := map[string]string{
		"core.md":  "see [missing](nope.md)\n", // broken-ref
		ConfigName: "disable:\n  - broken-ref\n",
	}
	cfg := lintFixture(t, files, nil)
	findings, _ := Run(cfg)
	for _, f := range findings {
		if f.Rule == "broken-ref" {
			t.Errorf("disabled rule still reported: %+v", findings)
		}
	}
}

func TestLintDestCollision(t *testing.T) {
	cfg := lintFixture(t, map[string]string{
		"a.md": "A",
		"b.md": "B",
	}, map[string]*config.Adapter{
		"test": {Target: "target", Rules: []*rules.Rule{
			{From: rules.FromSpec{"a.md"}, To: "out.md"},
			{From: rules.FromSpec{"b.md"}, To: "out.md"},
		}},
	})
	findings, _ := Run(cfg)
	found := false
	for _, f := range findings {
		if f.Rule == "dest-collision" {
			found = true
		}
	}
	if !found {
		t.Errorf("collision not flagged: %+v", findings)
	}
}

// addSource registers a configured source on cfg and populates its cloned
// directory under cfg.SourcesDir() — as if `friday source add` had already
// run — so Run walks it alongside the primary.
func addSource(t *testing.T, cfg *config.Config, name string, files map[string]string) {
	t.Helper()
	cfg.Sources = append(cfg.Sources, &config.SourceSpec{Name: name, URL: "https://example.com/" + name + ".git"})
	dir := filepath.Join(cfg.SourcesDir(), name)
	for rel, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestLintMultiSourceFindingsArePrefixed is Phase 2's lint regression: a
// finding from a configured source names it ("team:rules/bad.md") so the
// user knows to fix it under .sources/team, never a bare-store guess, while
// the primary's own findings keep their unprefixed path exactly as before.
func TestLintMultiSourceFindingsArePrefixed(t *testing.T) {
	cfg := lintFixture(t, map[string]string{
		"rules/bad.md": "---\n: : bad yaml [\n---\n\nbody\n",
	}, nil)
	addSource(t, cfg, "team", map[string]string{
		"rules/bad.md": "---\n: : bad yaml [\n---\n\nbody\n",
	})
	findings, err := Run(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var localPath, teamPath string
	count := 0
	for _, f := range findings {
		if f.Rule != "frontmatter" {
			continue
		}
		count++
		if strings.HasPrefix(f.Path, "team:") {
			teamPath = f.Path
		} else {
			localPath = f.Path
		}
	}
	if count != 2 {
		t.Fatalf("got %d frontmatter findings, want 2 (one per source): %+v", count, findings)
	}
	if localPath != "rules/bad.md" {
		t.Errorf("primary finding Path = %q, want unprefixed %q", localPath, "rules/bad.md")
	}
	if teamPath != "team:rules/bad.md" {
		t.Errorf("source finding Path = %q, want %q", teamPath, "team:rules/bad.md")
	}
}

// TestLintUnclonedSourceIsSkipped confirms a source declared in friday.yaml
// but not yet cloned (friday source add never ran) contributes no findings
// and never fails the run — mirroring rules.Expand's own missing-root
// tolerance.
func TestLintUnclonedSourceIsSkipped(t *testing.T) {
	cfg := lintFixture(t, map[string]string{"core.md": "hello"}, nil)
	cfg.Sources = append(cfg.Sources, &config.SourceSpec{Name: "team", URL: "https://example.com/team.git"})
	findings, err := Run(cfg)
	if err != nil {
		t.Fatalf("uncloned source must not fail the run: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("findings = %+v, want none", findings)
	}
}

// TestLintSourceShadow covers D11's collision case from the doctor side: a
// file present in both a source and the primary is a source-shadow warning
// naming the winner, and the winner's own copy is never itself flagged.
//
// The adapter rule matters: shadowing is read off the real dry-run plan, so
// a path no rule reads contributes nowhere and is correctly NOT reported
// (that was the old check's README.md/LICENSE noise). A glob rule over
// rules/*.md is what makes local's copy the contributing winner and team's
// copy genuinely dead weight.
func TestLintSourceShadow(t *testing.T) {
	cfg := lintFixture(t, map[string]string{
		"rules/a.md": "LOCAL",
	}, map[string]*config.Adapter{
		"test": {Target: "target", Rules: []*rules.Rule{
			{From: rules.FromSpec{"rules/*.md"}, To: "rules/{filename}"},
		}},
	})
	addSource(t, cfg, "team", map[string]string{
		"rules/a.md": "TEAM", // shadowed by local's copy
		"rules/b.md": "TEAM", // not shadowed — no collision
	})
	findings, err := Run(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var shadows []Finding
	for _, f := range findings {
		if f.Rule == "source-shadow" {
			shadows = append(shadows, f)
		}
	}
	if len(shadows) != 1 {
		t.Fatalf("got %d source-shadow findings, want 1: %+v", len(shadows), findings)
	}
	if shadows[0].Path != "team:rules/a.md" {
		t.Errorf("shadow Path = %q, want %q", shadows[0].Path, "team:rules/a.md")
	}
	if !strings.Contains(shadows[0].Msg, "local") {
		t.Errorf("shadow Msg = %q, want it to name the winning source (local)", shadows[0].Msg)
	}
}

// TestLintSourceShadowSingleSourceIsNoop is the regression guard: a store
// with no configured sources can never collide with itself.
func TestLintSourceShadowSingleSourceIsNoop(t *testing.T) {
	cfg := lintFixture(t, map[string]string{"rules/a.md": "x"}, nil)
	findings, err := Run(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if f.Rule == "source-shadow" {
			t.Errorf("unexpected source-shadow finding in a single-source store: %+v", f)
		}
	}
}

// TestLintSourceManifest is D13: a non-primary source carrying its own
// friday.yaml is warned about, since source.Load never reads it.
func TestLintSourceManifest(t *testing.T) {
	cfg := lintFixture(t, map[string]string{"core.md": "hello"}, nil)
	addSource(t, cfg, "team", map[string]string{
		"friday.yaml": "version: 1\nadapters: {}\n",
	})
	findings, err := Run(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var found *Finding
	for i, f := range findings {
		if f.Rule == "source-manifest" {
			found = &findings[i]
		}
	}
	if found == nil {
		t.Fatalf("no source-manifest finding: %+v", findings)
	}
	if found.Path != "team:friday.yaml" {
		t.Errorf("Path = %q, want %q", found.Path, "team:friday.yaml")
	}
	if found.Severity != Warn {
		t.Errorf("Severity = %v, want Warn", found.Severity)
	}
}

// TestLintSourceManifestIgnoresPrimary confirms the primary's own
// friday.yaml — required for the store to load at all — is never flagged.
func TestLintSourceManifestIgnoresPrimary(t *testing.T) {
	cfg := lintFixture(t, map[string]string{"core.md": "hello"}, nil)
	if err := os.WriteFile(filepath.Join(cfg.StoreDir, "friday.yaml"), []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	findings, err := Run(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if f.Rule == "source-manifest" {
			t.Errorf("primary's own friday.yaml must never be flagged: %+v", f)
		}
	}
}

// ghClassicPAT is assembled from two pieces rather than written out. A
// repository secret scanner rejects a push over any credential-shaped
// literal, fake or not, and the mcp-secret rule keys only on a vendor
// prefix plus a charset and a minimum length — so the split costs the test
// nothing.
const ghClassicPAT = "ghp_" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestLintMCPSecretFlagsLiteralCredential(t *testing.T) {
	cfg := lintFixture(t, map[string]string{
		"core.md": "hello",
		"mcp.yaml": "version: 1\nservers:\n  github:\n    transport: stdio\n    command: npx\n" +
			"    env:\n      GITHUB_TOKEN: " + ghClassicPAT + "\n",
	}, nil)
	findings, err := Run(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var found *Finding
	for i := range findings {
		if findings[i].Rule == "mcp-secret" {
			found = &findings[i]
		}
	}
	if found == nil {
		t.Fatalf("findings = %+v, want an mcp-secret hit", findings)
	}
	if found.Severity != Error {
		t.Errorf("Severity = %v, want Error (mcp-secret fails doctor)", found.Severity)
	}
	if found.Path != "mcp.yaml" {
		t.Errorf("Path = %q, want mcp.yaml", found.Path)
	}
	if !strings.Contains(found.Msg, "github.env.GITHUB_TOKEN") {
		t.Errorf("Msg = %q, want the flagged key path named", found.Msg)
	}
}

func TestLintMCPSecretIgnoresEnvReference(t *testing.T) {
	cfg := lintFixture(t, map[string]string{
		"core.md": "hello",
		"mcp.yaml": "version: 1\nservers:\n  github:\n    transport: stdio\n    command: npx\n" +
			"    env:\n      GITHUB_TOKEN: ${GITHUB_TOKEN}\n",
	}, nil)
	findings, err := Run(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if f.Rule == "mcp-secret" {
			t.Errorf("an env reference must never be flagged: %+v", f)
		}
	}
}

func TestLintMCPSecretSkipsMalformedFileQuietly(t *testing.T) {
	cfg := lintFixture(t, map[string]string{
		"core.md":  "hello",
		"mcp.yaml": "not: [valid, yaml\n",
	}, nil)
	findings, err := Run(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if f.Rule == "mcp-secret" {
			t.Errorf("a malformed mcp.yaml must not surface an mcp-secret finding: %+v", f)
		}
	}
}
