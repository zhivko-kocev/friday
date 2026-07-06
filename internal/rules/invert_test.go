package rules

import "testing"

func TestToGlob(t *testing.T) {
	cases := []struct {
		template string
		want     string
		ok       bool
	}{
		{"CLAUDE.md", "CLAUDE.md", true},
		{".github/copilot-instructions.md", ".github/copilot-instructions.md", true},
		{"agents/{filename}", "agents/*", true},
		{"skills/{relpath}", "skills/**/*", true},
		{".claude/skills/{relpath}", ".claude/skills/**/*", true},
		{"out/{stem}.txt", "", false},
		{"{dir}/x/{filename}", "", false},
		{"{relpath}/extra", "", false},
	}
	for _, c := range cases {
		got, ok := ToGlob(c.template)
		if ok != c.ok || got != c.want {
			t.Errorf("ToGlob(%q) = %q,%v want %q,%v", c.template, got, ok, c.want, c.ok)
		}
	}
}

func TestInvert(t *testing.T) {
	cases := []struct {
		template, targetRel, anchor string
		want                        string
		ok                          bool
	}{
		{"agents/{filename}", "agents/architect.md", "agents", "agents/architect.md", true},
		{"skills/{relpath}", "skills/onboard/tpl/t1.md", "skills", "skills/onboard/tpl/t1.md", true},
		{".claude/skills/{relpath}", ".claude/skills/x/S.md", "skills", "skills/x/S.md", true},
		{"rules/{filename}", "rules/general.md", "rules", "rules/general.md", true},
		// {filename} must not swallow nested paths.
		{"agents/{filename}", "agents/sub/deep.md", "agents", "", false},
		// target outside the template prefix
		{"agents/{filename}", "commands/x.md", "agents", "", false},
		// literal templates are the caller's job
		{"CLAUDE.md", "CLAUDE.md", "", "", false},
	}
	for _, c := range cases {
		got, ok := Invert(c.template, c.targetRel, c.anchor)
		if ok != c.ok || got != c.want {
			t.Errorf("Invert(%q,%q,%q) = %q,%v want %q,%v", c.template, c.targetRel, c.anchor, got, ok, c.want, c.ok)
		}
	}
}

// Round trip: expanding a store path through a template then inverting it
// must return the original path, for both supported tokens.
func TestInvertRoundTrip(t *testing.T) {
	for _, c := range []struct{ pattern, template, storeRel string }{
		{"agents/*.md", "agents/{filename}", "agents/architect.md"},
		{"skills/**/*", ".claude/skills/{relpath}", "skills/onboard/SKILL.md"},
	} {
		anchor := Anchor(c.pattern)
		targetRel := TokensFor(c.storeRel, anchor).Expand(c.template)
		got, ok := Invert(c.template, targetRel, anchor)
		if !ok || got != c.storeRel {
			t.Errorf("round trip %q via %q: got %q,%v", c.storeRel, c.template, got, ok)
		}
	}
}
