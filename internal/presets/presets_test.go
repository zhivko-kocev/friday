package presets

import (
	"reflect"
	"testing"
)

func TestGetReturnsDeepCopy(t *testing.T) {
	p1, ok := Get("claude")
	if !ok {
		t.Fatal("claude preset missing")
	}
	if len(p1.Rules) == 0 {
		t.Fatal("claude has no rules — registry corrupt")
	}
	// Mutate the returned rule's From slice — must not affect a fresh Get.
	p1.Rules[0].From[0] = "MUTATED"
	p1.Rules[0].Replace["${CLAUDE_PLUGIN_ROOT}"] = "MUTATED"
	p2, _ := Get("claude")
	if reflect.DeepEqual(p1.Rules[0].From, p2.Rules[0].From) {
		t.Errorf("registry leaked: mutating Get's result reflected on next Get")
	}
	if p2.Rules[0].Replace["${CLAUDE_PLUGIN_ROOT}"] == "MUTATED" {
		t.Errorf("registry leaked: mutating Get's Replace map reflected on next Get")
	}
}

// Every preset's instruction sources must lead with the entry-file variants,
// most-preferred first, and every rule must rewrite the plugin-root marker so
// knowledge-repo cross-references resolve after a push.
func TestPresetsEntryFilesAndReplace(t *testing.T) {
	for _, name := range Names() {
		p, _ := Get(name)
		first := p.Rules[0]
		for i, want := range []string{"core.md", "core/core.md", "identity.md"} {
			if i >= len(first.From) || first.From[i] != want {
				t.Errorf("%s: rule 0 from-list = %v, want entry variants first", name, first.From)
				break
			}
		}
		for i, r := range p.Rules {
			if r.Replace["${CLAUDE_PLUGIN_ROOT}"] == "" {
				t.Errorf("%s: rule %d (%s) has no plugin-root replace", name, i, r.To)
			}
			if err := r.Normalize(); err != nil {
				t.Errorf("%s: rule %d invalid: %v", name, i, err)
			}
		}
	}
}

// Every store directory maps into every agent that has a documented place
// for it — the capability matrix in presets.go, verified here per preset by
// which store patterns its rules consume.
func TestPresetCapabilityMatrix(t *testing.T) {
	want := map[string][]string{
		"claude":      {"rules/*.md", "agents/*.md", "commands/*.md", "skills/**/*", "standards/*.md", "connectors/*.md", "hooks/hooks.json"},
		"codex":       {"rules/*.md", "commands/*.md", "skills/**/*", "standards/*.md", "connectors/*.md", "hooks/codex/hooks.json"},
		"copilot":     {"rules/*.md", "agents/*.md", "skills/**/*", "standards/*.md", "connectors/*.md", "hooks/copilot/hooks.json"},
		"opencode":    {"rules/*.md", "agents/*.md", "commands/*.md", "skills/**/*", "standards/*.md", "connectors/*.md"},
		"antigravity": {"rules/*.md", "commands/*.md", "skills/**/*", "standards/*.md", "connectors/*.md", "hooks/antigravity/hooks.json"},
		"pi":          {"rules/*.md", "commands/*.md", "skills/**/*", "standards/*.md", "connectors/*.md"},
	}
	for name, patterns := range want {
		p, ok := Get(name)
		if !ok {
			t.Fatalf("preset %s missing", name)
		}
		consumed := map[string]bool{}
		for _, r := range p.Rules {
			for _, f := range r.From {
				consumed[f] = true
			}
		}
		if !consumed["core.md"] {
			t.Errorf("%s: entry file not consumed", name)
		}
		for _, pat := range patterns {
			if !consumed[pat] {
				t.Errorf("%s: store pattern %s not mapped (have %v)", name, pat, consumed)
			}
		}
	}
}

func TestNamesIsSortedAndComplete(t *testing.T) {
	names := Names()
	want := []string{"antigravity", "claude", "codex", "copilot", "opencode", "pi"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("Names() = %v, want %v", names, want)
	}
}

func TestAllAdaptersHasEveryPreset(t *testing.T) {
	all := AllAdapters()
	for _, n := range Names() {
		if _, ok := all[n]; !ok {
			t.Errorf("AllAdapters missing %s", n)
		}
	}
}

func TestGetStampsDefaultReplaceOnEveryRule(t *testing.T) {
	// The registry stays pure layout data; the store-wide marker rewrite is
	// stamped by Get so a newly added rule can't silently ship without one.
	// Most rules get the default "~/.friday"; hook-wiring rules override it with
	// a shell-expandable form ("$HOME/.friday" at user scope,
	// "${CLAUDE_PROJECT_DIR}/.claude" at project scope). The invariant under
	// test is only that the marker is always rewritten to something non-empty —
	// never left dangling as ${CLAUDE_PLUGIN_ROOT} in a pushed file.
	for _, name := range Names() {
		p, _ := Get(name)
		for i, r := range append(p.Rules, p.ProjectRules...) {
			if r.Replace["${CLAUDE_PLUGIN_ROOT}"] == "" {
				t.Errorf("%s: rule %d (%s) has no plugin-root replace", name, i, r.To)
			}
		}
	}
}
