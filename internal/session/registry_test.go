package session

import (
	"slices"
	"testing"

	"github.com/zhivko-kocev/friday/internal/presets"
)

// TestReadersCoverEveryCodingAgentPreset is the guard on Readers()' hand-
// written slice. internal/cli/sessions.go enumerates readers only through
// Readers(), so a new agent whose constructor is never added there compiles,
// passes every other test, and produces a CLI where that agent's sessions
// simply never appear. Cross-checking against the presets registry makes
// either half of the omission -- a preset with no reader, or a reader that
// was written but never registered -- fail here with a readable message.
//
// "agents" is excluded deliberately: it is the shared Agent-Skills
// directory several agents read from, not a coding agent with its own
// session store.
func TestReadersCoverEveryCodingAgentPreset(t *testing.T) {
	var want []string
	for name := range presets.AllAdapters() {
		if name == "agents" {
			continue
		}
		want = append(want, name)
	}
	slices.Sort(want)

	got := slices.Clone(Names())
	slices.Sort(got)

	if !slices.Equal(got, want) {
		t.Errorf("session.Names() = %v, presets (minus \"agents\") = %v;\n"+
			"a preset needs a Reader registered in Readers(), and a Reader needs a preset", got, want)
	}
}

// TestReadersAreDistinctAndFresh pins the two properties the CLI relies on:
// no two readers claim the same agent name (--agent would be ambiguous and
// List would double-count), and Readers() hands out fresh values so
// concurrent callers share no mutable state.
func TestReadersAreDistinctAndFresh(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range Readers() {
		name := r.Agent()
		if name == "" {
			t.Errorf("%T.Agent() is empty", r)
		}
		if seen[name] {
			t.Errorf("two readers both claim agent %q", name)
		}
		seen[name] = true
	}

	first, second := Readers(), Readers()
	if len(first) != len(second) {
		t.Fatalf("Readers() returned %d then %d readers", len(first), len(second))
	}
	if &first[0] == &second[0] {
		t.Error("Readers() returned the same backing slice twice, want a fresh one per call")
	}

	if names := Names(); len(names) != len(first) {
		t.Errorf("len(Names()) = %d, len(Readers()) = %d", len(names), len(first))
	}
}
