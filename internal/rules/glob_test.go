package rules

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestAnchor(t *testing.T) {
	cases := []struct {
		pattern, want string
	}{
		{"rules/general.md", "rules"},
		{"agents/*.md", "agents"},
		{"skills/**/*.md", "skills"},
		{"foo.md", ""},
		{"*.md", ""},
		{"a/b/c.md", "a/b"},
		{"a/b/*.md", "a/b"},
		{"a/**/c.md", "a"},
	}
	for _, c := range cases {
		if got := Anchor(c.pattern); got != c.want {
			t.Errorf("Anchor(%q) = %q, want %q", c.pattern, got, c.want)
		}
	}
}

func TestMatchPath(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"rules/general.md", "rules/general.md", true},
		{"rules/general.md", "rules/other.md", false},
		{"agents/*.md", "agents/researcher.md", true},
		{"agents/*.md", "agents/sub/researcher.md", false},
		{"skills/**/*.md", "skills/foo.md", true},
		{"skills/**/*.md", "skills/foo/bar.md", true},
		{"skills/**/*.md", "skills/a/b/c.md", true},
		{"skills/**/*", "skills/.gitkeep", false}, // dotfile excluded
		{"skills/**/.gitkeep", "skills/.gitkeep", true},
		{"*.md", ".hidden.md", false}, // dotfile excluded
		{".*", ".hidden", true},
	}
	for _, c := range cases {
		if got := matchPath(c.pattern, c.path); got != c.want {
			t.Errorf("matchPath(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestExpand(t *testing.T) {
	root := t.TempDir()
	mkfile := func(p string) {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mkfile("identity.md")
	mkfile("rules/a.md")
	mkfile("rules/b.md")
	mkfile("rules/.hidden.md")
	mkfile("skills/one/main.md")
	mkfile("skills/two/sub/deep.md")
	mkfile("skills/.gitkeep")

	cases := []struct {
		pattern string
		want    []string
	}{
		{"identity.md", []string{"identity.md"}},
		{"rules/*.md", []string{"rules/a.md", "rules/b.md"}}, // .hidden.md excluded
		{"skills/**/*.md", []string{"skills/one/main.md", "skills/two/sub/deep.md"}},
		{"skills/**/*", []string{"skills/one/main.md", "skills/two/sub/deep.md"}}, // .gitkeep excluded
		{"missing/*.md", nil},
	}
	for _, c := range cases {
		got, err := Expand(root, c.pattern)
		if err != nil {
			t.Errorf("Expand(%q) err = %v", c.pattern, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Expand(%q) = %v, want %v", c.pattern, got, c.want)
		}
	}
}
