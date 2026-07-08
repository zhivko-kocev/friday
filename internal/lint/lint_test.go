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
