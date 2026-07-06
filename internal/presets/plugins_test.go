package presets

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writePlugin(t *testing.T, storeDir, name, content string) {
	t.Helper()
	dir := filepath.Join(storeDir, PluginsDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadPluginsValid(t *testing.T) {
	store := t.TempDir()
	writePlugin(t, store, "aider.yaml", "target: .aider\nrules:\n  - from: [core.md, rules/*.md]\n    to: CONVENTIONS.md\n    strategy: concatenate\n")
	plugins, errs := LoadPlugins(store)
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	p, ok := plugins["aider"] // name defaults to the file stem
	if !ok || p.Target != ".aider" || len(p.Rules) != 1 {
		t.Fatalf("plugins = %+v", plugins)
	}
	if p.Rules[0].Strategy != "concatenate" {
		t.Errorf("rule not normalized: %+v", p.Rules[0])
	}
}

func TestLoadPluginsRejectsBroken(t *testing.T) {
	cases := map[string]string{
		"no-target.yaml": "rules:\n  - from: a.md\n    to: b.md\n",
		"no-rules.yaml":  "target: .x\n",
		"bad-rule.yaml":  "target: .x\nrules:\n  - from: a.md\n", // missing to
		"reserved.yaml":  "name: \"~evil\"\ntarget: .x\nrules:\n  - from: a.md\n    to: b.md\n",
		"not-yaml.yaml":  "::::",
	}
	for file, content := range cases {
		store := t.TempDir()
		writePlugin(t, store, file, content)
		plugins, errs := LoadPlugins(store)
		if len(errs) != 1 || len(plugins) != 0 {
			t.Errorf("%s: plugins=%v errs=%v — want one error, no preset", file, plugins, errs)
		}
	}
}

func TestLoadPluginsMissingDirIsFine(t *testing.T) {
	plugins, errs := LoadPlugins(t.TempDir())
	if plugins != nil || errs != nil {
		t.Errorf("got %v, %v", plugins, errs)
	}
}

func TestAllAdaptersWithOverlaysBuiltins(t *testing.T) {
	store := t.TempDir()
	// A plugin shadowing the built-in codex preset.
	writePlugin(t, store, "codex.yaml", "target: .custom-codex\nrules:\n  - from: core.md\n    to: AGENTS.md\n")
	all, errs := AllAdaptersWith(store)
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	if all["codex"].Target != ".custom-codex" {
		t.Errorf("plugin did not shadow built-in: %+v", all["codex"])
	}
	if _, ok := all["claude"]; !ok {
		t.Error("built-ins missing from overlay")
	}
}

func TestLoadPluginsLabelsProjectRuleErrors(t *testing.T) {
	store := t.TempDir()
	writePlugin(t, store, "bad.yaml",
		"target: .x\nrules:\n  - from: a.md\n    to: A.md\nproject_rules:\n  - from: b.md\n    to: \"\"\n")
	_, errs := LoadPlugins(store)
	if len(errs) != 1 {
		t.Fatalf("errs = %v, want one", errs)
	}
	if !strings.Contains(errs[0].Error(), "project_rule[0]") {
		t.Errorf("err = %v, want it labeled project_rule[0], not a merged rule index", errs[0])
	}
}

func TestGetWithResolvesPluginPresets(t *testing.T) {
	store := t.TempDir()
	writePlugin(t, store, "aider.yaml", "target: .aider\nrules:\n  - from: core.md\n    to: CONVENTIONS.md\n")
	p, ok := GetWith(store, "aider")
	if !ok || p.Target != ".aider" {
		t.Fatalf("GetWith(aider) = %+v, %v — plugin presets must be visible to setup/promote", p, ok)
	}
	if _, ok := GetWith(store, "claude"); !ok {
		t.Error("GetWith must still resolve built-ins")
	}
	names := NamesWith(store)
	if !slices.Contains(names, "aider") || !slices.Contains(names, "claude") {
		t.Errorf("NamesWith = %v, want plugins and built-ins", names)
	}
	if !slices.IsSorted(names) {
		t.Errorf("NamesWith not sorted: %v", names)
	}
}
