package engine

import (
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/zhivko-kocev/friday/internal/render"
)

func noReplace(b []byte) []byte { return b }

func TestParseAgentMD(t *testing.T) {
	raw := []byte("---\nname: architect\ndescription: plans work\ntools: Read, Grep\n---\nYou are the architect.\nPlan first.\n")
	doc := parseAgentMD(raw, "fallback", noReplace)
	if doc.Name != "architect" {
		t.Errorf("name = %q, want architect", doc.Name)
	}
	if doc.Description != "plans work" {
		t.Errorf("description = %q", doc.Description)
	}
	if doc.Body != "You are the architect.\nPlan first." {
		t.Errorf("body = %q", doc.Body)
	}
}

func TestParseAgentMDNameFallsBackToStem(t *testing.T) {
	doc := parseAgentMD([]byte("---\ndescription: d\n---\nbody"), "reviewer", noReplace)
	if doc.Name != "reviewer" {
		t.Errorf("name = %q, want stem 'reviewer'", doc.Name)
	}
}

func TestParseAgentMDAppliesReplace(t *testing.T) {
	upper := func(b []byte) []byte { return []byte(strings.ToUpper(string(b))) }
	doc := parseAgentMD([]byte("---\nname: n\n---\nsee ${x}"), "s", upper)
	if doc.Body != "SEE ${X}" {
		t.Errorf("replace not applied to body: %q", doc.Body)
	}
}

// TestParseAgentMDAdversarialBodyRoundTripsThroughCodexTOML is the escaping
// regression the retired md-to-toml strategy used to carry: a body holding
// quotes, backslashes, a literal """ run and a trailing quote must not
// terminate the multiline string early, and must decode back byte for byte.
// go-toml is pinned to an unreleased pseudo-version, so the guarantee is
// asserted here rather than assumed.
func TestParseAgentMDAdversarialBodyRoundTripsThroughCodexTOML(t *testing.T) {
	body := "say \"hi\" and a \\ backslash\nthen a \"\"\"triple\"\"\" run\nand a trailing quote\""
	raw := []byte("---\nname: a\ndescription: 'd\"q'\n---\n" + body + "\n")

	doc := parseAgentMD(raw, "stem", noReplace)
	out, err := render.CodexAgent(doc)
	if err != nil {
		t.Fatal(err)
	}

	var got struct {
		Name                  string `toml:"name"`
		Description           string `toml:"description"`
		DeveloperInstructions string `toml:"developer_instructions"`
	}
	if err := toml.Unmarshal(out, &got); err != nil {
		t.Fatalf("invalid TOML: %v\n%s", err, out)
	}
	if got.Name != "a" {
		t.Errorf("name = %q, want a", got.Name)
	}
	if got.Description != `d"q` {
		t.Errorf("description = %q, want d\"q — the quote must be escaped, not dropped", got.Description)
	}
	if got.DeveloperInstructions != body {
		t.Errorf("body did not round-trip:\n got %q\nwant %q\nrendered:\n%s", got.DeveloperInstructions, body, out)
	}
}
