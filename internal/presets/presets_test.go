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

// The claude preset copies exactly what Claude Code discovers on disk;
// everything else in a knowledge repo (core/, standards/, hooks/) is reached
// via the ~/.friday references the replace transform writes.
func TestClaudePresetCoversDiscoveryDirs(t *testing.T) {
	p, _ := Get("claude")
	dests := make(map[string]bool, len(p.Rules))
	for _, r := range p.Rules {
		dests[r.To] = true
	}
	for _, want := range []string{"CLAUDE.md", "agents/{filename}", "commands/{filename}", "skills/{relpath}"} {
		if !dests[want] {
			t.Errorf("claude preset missing rule writing %s (have %v)", want, dests)
		}
	}
	for _, unwanted := range []string{"standards/{filename}", "hooks/{relpath}", "core/core.md"} {
		if dests[unwanted] {
			t.Errorf("claude preset copies %s — non-discovery content must stay in the store", unwanted)
		}
	}
}

func TestNamesIsSortedAndComplete(t *testing.T) {
	names := Names()
	want := []string{"antigravity", "claude", "codex", "copilot", "opencode", "pi", "windsurf"}
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
