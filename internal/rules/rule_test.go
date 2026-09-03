package rules

import (
	"errors"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestFromSpecUnmarshal(t *testing.T) {
	t.Run("string", func(t *testing.T) {
		var r Rule
		if err := yaml.Unmarshal([]byte("from: identity.md\nto: AGENTS.md\n"), &r); err != nil {
			t.Fatal(err)
		}
		if len(r.From) != 1 || r.From[0] != "identity.md" {
			t.Errorf("got %v", r.From)
		}
	})
	t.Run("list", func(t *testing.T) {
		var r Rule
		yamlSrc := "from:\n  - identity.md\n  - rules/*.md\nto: CLAUDE.md\n"
		if err := yaml.Unmarshal([]byte(yamlSrc), &r); err != nil {
			t.Fatal(err)
		}
		if len(r.From) != 2 || r.From[1] != "rules/*.md" {
			t.Errorf("got %v", r.From)
		}
	})
}

func TestNormalize(t *testing.T) {
	cases := []struct {
		name    string
		r       Rule
		wantErr bool
	}{
		{"defaults to copy", Rule{From: FromSpec{"a.md"}, To: "b.md"}, false},
		{"missing from", Rule{To: "b.md"}, true},
		{"missing to", Rule{From: FromSpec{"a.md"}}, true},
		{"unknown strategy", Rule{From: FromSpec{"a.md"}, To: "b.md", Strategy: "merge"}, true},
		{"concatenate to with token rejected", Rule{From: FromSpec{"a"}, To: "x/{filename}", Strategy: StrategyConcatenate}, true},
		{"concatenate to literal ok", Rule{From: FromSpec{"a"}, To: "x.md", Strategy: StrategyConcatenate}, false},
		{"merge-json ok", Rule{From: FromSpec{"hooks/hooks.json"}, To: "settings.json", Strategy: StrategyMergeJSON}, false},
		{"merge-json rejects glob from", Rule{From: FromSpec{"hooks/*.json"}, To: "settings.json", Strategy: StrategyMergeJSON}, true},
		{"merge-json rejects multi from", Rule{From: FromSpec{"a.json", "b.json"}, To: "settings.json", Strategy: StrategyMergeJSON}, true},
		{"merge-json rejects to token", Rule{From: FromSpec{"a.json"}, To: "{filename}", Strategy: StrategyMergeJSON}, true},
		{"merge-json rejects frontmatter_strip", Rule{From: FromSpec{"a.json"}, To: "settings.json", Strategy: StrategyMergeJSON, FrontmatterStrip: []string{"x"}}, true},
		{"merge-json rejects separator", Rule{From: FromSpec{"a.json"}, To: "settings.json", Strategy: StrategyMergeJSON, Separator: "X"}, true},
		{"merge-json rejects max_bytes", Rule{From: FromSpec{"a.json"}, To: "settings.json", Strategy: StrategyMergeJSON, MaxBytes: 10}, true},
		{"merge-toml ok", Rule{From: FromSpec{"settings/codex.toml"}, To: "config.toml", Strategy: StrategyMergeTOML}, false},
		{"merge-toml rejects glob from", Rule{From: FromSpec{"settings/*.toml"}, To: "config.toml", Strategy: StrategyMergeTOML}, true},
		{"merge-toml rejects multi from", Rule{From: FromSpec{"a.toml", "b.toml"}, To: "config.toml", Strategy: StrategyMergeTOML}, true},
		{"merge-toml rejects to token", Rule{From: FromSpec{"a.toml"}, To: "{stem}.toml", Strategy: StrategyMergeTOML}, true},
		{"merge-toml rejects frontmatter_strip", Rule{From: FromSpec{"a.toml"}, To: "config.toml", Strategy: StrategyMergeTOML, FrontmatterStrip: []string{"tools"}}, true},
		{"merge-toml rejects separator", Rule{From: FromSpec{"a.toml"}, To: "config.toml", Strategy: StrategyMergeTOML, Separator: "XX"}, true},
		{"merge-toml rejects max_bytes", Rule{From: FromSpec{"a.toml"}, To: "config.toml", Strategy: StrategyMergeTOML, MaxBytes: 3}, true},
		{"confirm_hook on copy ok", Rule{From: FromSpec{"h.ts"}, To: "plugins/h.ts", Strategy: StrategyCopy, ConfirmHook: true}, false},
		{"confirm_hook on merge-json rejected", Rule{From: FromSpec{"a.json"}, To: "settings.json", Strategy: StrategyMergeJSON, ConfirmHook: true}, true},
		{"render mcp ok", Rule{From: FromSpec{"mcp.yaml"}, To: "settings.json", Strategy: StrategyRender, Surface: SurfaceMCP, Merge: StrategyMergeJSON}, false},
		{"render permissions ok", Rule{From: FromSpec{"permissions.yaml"}, To: "settings.json", Strategy: StrategyRender, Surface: SurfacePermissions, Merge: StrategyMergeJSON}, false},
		{"render defaults ok", Rule{From: FromSpec{"defaults.yaml"}, To: "config.toml", Strategy: StrategyRender, Surface: SurfaceDefaults, Merge: StrategyMergeTOML}, false},
		{"render codex-agent allows glob from and tokenized to", Rule{From: FromSpec{"agents/*.md"}, To: "agents/{stem}.toml", Strategy: StrategyRender, Surface: SurfaceCodexAgent, Merge: StrategyCopy}, false},
		{"render gemini-command allows glob from and tokenized to", Rule{From: FromSpec{"commands/*.md"}, To: "commands/{filename}.toml", Strategy: StrategyRender, Surface: SurfaceGeminiCommand, Merge: StrategyCopy}, false},
		{"render codex-agent rejects merge-json", Rule{From: FromSpec{"agents/*.md"}, To: "agents/{stem}.toml", Strategy: StrategyRender, Surface: SurfaceCodexAgent, Merge: StrategyMergeJSON}, true},
		{"render gemini-command rejects merge-toml", Rule{From: FromSpec{"commands/*.md"}, To: "commands/{filename}.toml", Strategy: StrategyRender, Surface: SurfaceGeminiCommand, Merge: StrategyMergeTOML}, true},
		{"render requires surface", Rule{From: FromSpec{"mcp.yaml"}, To: "settings.json", Strategy: StrategyRender, Merge: StrategyMergeJSON}, true},
		{"render requires merge", Rule{From: FromSpec{"mcp.yaml"}, To: "settings.json", Strategy: StrategyRender, Surface: SurfaceMCP}, true},
		{"render rejects unknown surface", Rule{From: FromSpec{"mcp.yaml"}, To: "settings.json", Strategy: StrategyRender, Surface: "bogus", Merge: StrategyMergeJSON}, true},
		{"render rejects unknown merge", Rule{From: FromSpec{"mcp.yaml"}, To: "settings.json", Strategy: StrategyRender, Surface: SurfaceMCP, Merge: "bogus"}, true},
		{"render mcp rejects glob from", Rule{From: FromSpec{"mcp*.yaml"}, To: "settings.json", Strategy: StrategyRender, Surface: SurfaceMCP, Merge: StrategyMergeJSON}, true},
		{"render mcp rejects multi from", Rule{From: FromSpec{"a.yaml", "b.yaml"}, To: "settings.json", Strategy: StrategyRender, Surface: SurfaceMCP, Merge: StrategyMergeJSON}, true},
		{"render mcp rejects tokenized to", Rule{From: FromSpec{"mcp.yaml"}, To: "{filename}", Strategy: StrategyRender, Surface: SurfaceMCP, Merge: StrategyMergeJSON}, true},
		{"render rejects frontmatter_strip", Rule{From: FromSpec{"mcp.yaml"}, To: "settings.json", Strategy: StrategyRender, Surface: SurfaceMCP, Merge: StrategyMergeJSON, FrontmatterStrip: []string{"x"}}, true},
		{"render rejects separator", Rule{From: FromSpec{"mcp.yaml"}, To: "settings.json", Strategy: StrategyRender, Surface: SurfaceMCP, Merge: StrategyMergeJSON, Separator: "X"}, true},
		{"render rejects max_bytes", Rule{From: FromSpec{"mcp.yaml"}, To: "settings.json", Strategy: StrategyRender, Surface: SurfaceMCP, Merge: StrategyMergeJSON, MaxBytes: 10}, true},
		{"surface on non-render rule rejected", Rule{From: FromSpec{"a.md"}, To: "b.md", Surface: SurfaceMCP}, true},
		{"merge on non-render rule rejected", Rule{From: FromSpec{"a.md"}, To: "b.md", Merge: StrategyCopy}, true},
	}
	for _, c := range cases {
		err := c.r.Normalize()
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err=%v wantErr=%v", c.name, err, c.wantErr)
		}
	}
}

// TestNormalizeRetiredStrategy pins the migration-quality error every
// pre-v1.0 manifest hits: config.load keys off ErrRetiredStrategy to drop
// the rule instead of bricking every command, and the text must name the
// render/surface replacement plus the command that rewrites the manifest —
// a bare "unknown strategy" leaves the user with a dead CLI and no next step.
func TestNormalizeRetiredStrategy(t *testing.T) {
	cases := []struct {
		strategy    string
		wantSurface string
	}{
		{StrategyMDToTOML, "codex-agent"},
		{StrategyMDToJSON, "gemini-command"},
	}
	for _, c := range cases {
		t.Run(c.strategy, func(t *testing.T) {
			r := &Rule{From: FromSpec{"agents/*.md"}, To: "agents/{stem}.x", Strategy: c.strategy}
			err := r.Normalize()
			if err == nil {
				t.Fatalf("Normalize(%s) = nil, want ErrRetiredStrategy", c.strategy)
			}
			if !errors.Is(err, ErrRetiredStrategy) {
				t.Fatalf("Normalize(%s) err = %v, want ErrRetiredStrategy", c.strategy, err)
			}
			for _, want := range []string{c.strategy, "strategy: render", "surface: " + c.wantSurface, "friday migrate"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("Normalize(%s) err = %q, missing %q", c.strategy, err, want)
				}
			}
		})
	}
}

func TestSepDefault(t *testing.T) {
	r := &Rule{}
	if got := r.Sep(); got != DefaultSeparator {
		t.Errorf("Sep() = %q, want default", got)
	}
	r.Separator = "X"
	if got := r.Sep(); got != "X" {
		t.Errorf("Sep() = %q, want X", got)
	}
}
