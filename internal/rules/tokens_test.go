package rules

import "testing"

func TestTokensFor(t *testing.T) {
	cases := []struct {
		match, anchor     string
		filename, stem    string
		ext, relpath, dir string
	}{
		{"rules/general.md", "rules", "general.md", "general", ".md", "general.md", ""},
		{"agents/researcher.md", "agents", "researcher.md", "researcher", ".md", "researcher.md", ""},
		{"skills/foo/bar.md", "skills", "bar.md", "bar", ".md", "foo/bar.md", "foo"},
		{"skills/a/b/c.md", "skills", "c.md", "c", ".md", "a/b/c.md", "a/b"},
		{"identity.md", "", "identity.md", "identity", ".md", "identity.md", ""},
	}
	for _, c := range cases {
		tok := TokensFor(c.match, c.anchor)
		if tok.Filename != c.filename || tok.Stem != c.stem || tok.Ext != c.ext ||
			tok.Relpath != c.relpath || tok.Dir != c.dir {
			t.Errorf("TokensFor(%q, %q) = %+v\n want filename=%q stem=%q ext=%q relpath=%q dir=%q",
				c.match, c.anchor, tok, c.filename, c.stem, c.ext, c.relpath, c.dir)
		}
	}
}

func TestExpandTemplate(t *testing.T) {
	tok := Tokens{Filename: "foo.md", Stem: "foo", Ext: ".md", Relpath: "sub/foo.md", Dir: "sub"}
	cases := []struct {
		template, want string
	}{
		{"agents/{filename}", "agents/foo.md"},
		{"agents/{stem}.txt", "agents/foo.txt"},
		{"out/{dir}/{stem}{ext}", "out/sub/foo.md"},
		{"verbatim", "verbatim"},
		{"{relpath}", "sub/foo.md"},
	}
	for _, c := range cases {
		if got := tok.Expand(c.template); got != c.want {
			t.Errorf("Expand(%q) = %q, want %q", c.template, got, c.want)
		}
	}
}
