package rules

import (
	"testing"

	"gopkg.in/yaml.v3"
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
	}
	for _, c := range cases {
		err := c.r.Normalize()
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err=%v wantErr=%v", c.name, err, c.wantErr)
		}
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
