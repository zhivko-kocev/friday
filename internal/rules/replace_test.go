package rules

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestReplaceUnmarshal(t *testing.T) {
	var r Rule
	yamlSrc := "from: skills/**/*\nto: skills/{relpath}\nreplace:\n  \"${CLAUDE_PLUGIN_ROOT}\": \"~/.claude\"\n"
	if err := yaml.Unmarshal([]byte(yamlSrc), &r); err != nil {
		t.Fatal(err)
	}
	if r.Replace["${CLAUDE_PLUGIN_ROOT}"] != "~/.claude" {
		t.Errorf("Replace = %v", r.Replace)
	}
	if err := r.Normalize(); err != nil {
		t.Errorf("Normalize: %v", err)
	}
}

func TestNormalizeReplace(t *testing.T) {
	base := Rule{From: FromSpec{"a.md"}, To: "b.md"}
	cases := []struct {
		name    string
		replace map[string]string
		wantErr bool
	}{
		{"valid", map[string]string{"${X}": "~/.claude"}, false},
		{"empty key", map[string]string{"": "x"}, true},
		{"empty value", map[string]string{"${X}": ""}, true},
		{"self-map", map[string]string{"x": "x"}, true},
		{"duplicate values", map[string]string{"${A}": "same", "${B}": "same"}, true},
	}
	for _, c := range cases {
		r := base
		r.Replace = c.replace
		if err := r.Normalize(); (err != nil) != c.wantErr {
			t.Errorf("%s: err=%v wantErr=%v", c.name, err, c.wantErr)
		}
	}
}

func TestApplyReplace(t *testing.T) {
	r := &Rule{Replace: map[string]string{
		"${ROOT}":     "~/.claude",
		"${ROOT}/sub": "/special", // longer key must win over its prefix
	}}
	in := []byte("see ${ROOT}/sub and ${ROOT}/core.md")
	got := string(r.ApplyReplace(in))
	want := "see /special and ~/.claude/core.md"
	if got != want {
		t.Errorf("ApplyReplace = %q, want %q", got, want)
	}
}

func TestApplyReplaceRoundTrip(t *testing.T) {
	r := &Rule{Replace: map[string]string{"${CLAUDE_PLUGIN_ROOT}": "~/.claude"}}
	in := []byte("Read ${CLAUDE_PLUGIN_ROOT}/core/core.md first.\nplain text\n")
	out := r.ApplyReplaceInverse(r.ApplyReplace(in))
	if string(out) != string(in) {
		t.Errorf("round-trip = %q, want %q", out, in)
	}
}

func TestApplyReplaceEmpty(t *testing.T) {
	r := &Rule{}
	in := []byte("untouched")
	if got := string(r.ApplyReplace(in)); got != "untouched" {
		t.Errorf("got %q", got)
	}
}
