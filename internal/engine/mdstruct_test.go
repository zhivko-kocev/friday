package engine

import (
	"encoding/json"
	"strings"
	"testing"
)

func noReplace(b []byte) []byte { return b }

func TestParseAgentMD(t *testing.T) {
	raw := []byte("---\nname: architect\ndescription: plans work\ntools: Read, Grep\n---\nYou are the architect.\nPlan first.\n")
	rec := parseAgentMD(raw, "fallback", noReplace)
	if rec.name != "architect" {
		t.Errorf("name = %q, want architect", rec.name)
	}
	if rec.description != "plans work" {
		t.Errorf("description = %q", rec.description)
	}
	if rec.body != "You are the architect.\nPlan first." {
		t.Errorf("body = %q", rec.body)
	}
}

func TestParseAgentMDNameFallsBackToStem(t *testing.T) {
	rec := parseAgentMD([]byte("---\ndescription: d\n---\nbody"), "reviewer", noReplace)
	if rec.name != "reviewer" {
		t.Errorf("name = %q, want stem 'reviewer'", rec.name)
	}
}

func TestParseAgentMDAppliesReplace(t *testing.T) {
	upper := func(b []byte) []byte { return []byte(strings.ToUpper(string(b))) }
	rec := parseAgentMD([]byte("---\nname: n\n---\nsee ${x}"), "s", upper)
	if rec.body != "SEE ${X}" {
		t.Errorf("replace not applied to body: %q", rec.body)
	}
}

func TestMDToTOMLEscapesSoBodyRoundTrips(t *testing.T) {
	// A body with a backslash and a run of quotes must not close the multiline
	// string early and must decode back to the original.
	body := `path C:\dir and a "quote" and """triple"""`
	out := string(mdToTOML(mdRecord{name: "a", description: `d"q`, body: body}))
	if !strings.Contains(out, `name = "a"`) {
		t.Errorf("missing name line:\n%s", out)
	}
	if !strings.Contains(out, `description = "d\"q"`) {
		t.Errorf("description not escaped:\n%s", out)
	}
	// The body's literal `"""` must have been escaped so it can't terminate the
	// developer_instructions string prematurely.
	if strings.Contains(out, `"""triple"""`) {
		t.Errorf("unescaped triple-quote run leaks into TOML:\n%s", out)
	}
	// Decode the multiline value the way TOML would (\\ → \, \" → ") and confirm
	// it matches the original body.
	start := strings.Index(out, "\"\"\"\n")
	end := strings.LastIndex(out, "\"\"\"")
	if start < 0 || end <= start {
		t.Fatalf("no multiline string found:\n%s", out)
	}
	enc := out[start+4 : end]
	dec := strings.ReplaceAll(enc, `\"`, `"`)
	dec = strings.ReplaceAll(dec, `\\`, `\`)
	if dec != body {
		t.Errorf("body did not round-trip:\n got %q\nwant %q", dec, body)
	}
}

func TestMDToJSONValidAndFielded(t *testing.T) {
	out, err := mdToJSON(mdRecord{name: "architect", description: "plans", body: "do the thing"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("emitted invalid JSON: %v\n%s", err, out)
	}
	if m["name"] != "architect" || m["description"] != "plans" || m["prompt"] != "do the thing" {
		t.Errorf("unexpected fields: %v", m)
	}
}

func TestMDToJSONOmitsEmptyDescription(t *testing.T) {
	out, _ := mdToJSON(mdRecord{name: "n", body: "b"})
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	if _, ok := m["description"]; ok {
		t.Errorf("empty description should be omitted: %v", m)
	}
}
