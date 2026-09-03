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
	// friday's own source clones live here; a `**` segment must not walk in.
	mkfile(".sources/team/rules/team.md")
	mkfile("native/claude/.hidden/x.md")

	cases := []struct {
		pattern string
		want    []string
	}{
		{"identity.md", []string{"identity.md"}},
		{"rules/*.md", []string{"rules/a.md", "rules/b.md"}}, // .hidden.md excluded
		{"skills/**/*.md", []string{"skills/one/main.md", "skills/two/sub/deep.md"}},
		{"skills/**/*", []string{"skills/one/main.md", "skills/two/sub/deep.md"}}, // .gitkeep excluded
		{"missing/*.md", nil},
		// An unanchored ** must not reach into any dot-dir: .sources/ is
		// friday's plumbing, and mirroring it into an agent dir would push
		// every configured source's content twice.
		{"**/*.md", []string{"identity.md", "rules/a.md", "rules/b.md", "skills/one/main.md", "skills/two/sub/deep.md"}},
		{"native/claude/**/*", nil},
		// A literal anchor naming the dot-dir still opts in, same as the
		// dotfile convention for a final path segment.
		{".sources/team/rules/*.md", []string{".sources/team/rules/team.md"}},
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

// mkfileIn writes content at root/rel, creating parent dirs.
func mkfileIn(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestExpandAllSingleRootMatchesExpand is the regression guard: a
// single-element roots slice (today's single-source store) must behave
// byte-for-byte like plain Expand — same matches, no shadowed entries.
func TestExpandAllSingleRootMatchesExpand(t *testing.T) {
	root := t.TempDir()
	mkfileIn(t, root, "rules/a.md", "a")
	mkfileIn(t, root, "rules/b.md", "b")

	want, err := Expand(root, "rules/*.md")
	if err != nil {
		t.Fatal(err)
	}
	matched, shadowed, err := ExpandAll([]Root{{Name: "local", Dir: root}}, "rules/*.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(shadowed) != 0 {
		t.Errorf("single root produced shadowed matches: %v", shadowed)
	}
	var gotRels []string
	for _, m := range matched {
		if m.Source != "local" || m.Root != root {
			t.Errorf("match %+v has wrong source/root", m)
		}
		gotRels = append(gotRels, m.Rel)
	}
	if !reflect.DeepEqual(gotRels, want) {
		t.Errorf("ExpandAll rels = %v, want %v", gotRels, want)
	}
}

// TestExpandAllUnionsDistinctFilenames covers D11: two sources each
// contributing a differently-named file both apply.
func TestExpandAllUnionsDistinctFilenames(t *testing.T) {
	team := t.TempDir()
	local := t.TempDir()
	mkfileIn(t, team, "rules/team.md", "team")
	mkfileIn(t, local, "rules/local.md", "local")

	matched, shadowed, err := ExpandAll([]Root{{Name: "team", Dir: team}, {Name: "local", Dir: local}}, "rules/*.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(shadowed) != 0 {
		t.Errorf("distinct filenames should never shadow: %v", shadowed)
	}
	if len(matched) != 2 {
		t.Fatalf("matched = %v, want 2 entries", matched)
	}
	if matched[0].Rel != "rules/local.md" || matched[1].Rel != "rules/team.md" {
		t.Errorf("matched = %+v, want local.md and team.md sorted by Rel", matched)
	}
}

// TestExpandAllCollisionPrimaryWins is D11's collision rule: the
// higher-precedence (later-in-slice) root wins an identical relative path,
// and the loser comes back as shadowed — lint's source-shadow Finding is
// only correct if it reads this set rather than recomputing shadowing.
func TestExpandAllCollisionPrimaryWins(t *testing.T) {
	team := t.TempDir()
	local := t.TempDir()
	mkfileIn(t, team, "rules/a.md", "TEAM")
	mkfileIn(t, local, "rules/a.md", "LOCAL")

	matched, shadowed, err := ExpandAll([]Root{{Name: "team", Dir: team}, {Name: "local", Dir: local}}, "rules/*.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(matched) != 1 || matched[0].Source != "local" {
		t.Fatalf("matched = %+v, want exactly the local (higher-precedence) copy", matched)
	}
	if len(shadowed) != 1 || shadowed[0].Source != "team" {
		t.Fatalf("shadowed = %+v, want exactly the team copy", shadowed)
	}
}

// TestExpandAllThreeRootShadowOrder is the multi-shadow path a two-root test
// cannot reach: with three roots holding one relative path, both losers must
// come back in ascending precedence order. sort.Slice left that order
// arbitrary because the two entries share a Rel.
func TestExpandAllThreeRootShadowOrder(t *testing.T) {
	org, team, local := t.TempDir(), t.TempDir(), t.TempDir()
	for _, dir := range []string{org, team, local} {
		mkfileIn(t, dir, "rules/a.md", "x")
	}
	roots := []Root{{Name: "org", Dir: org}, {Name: "team", Dir: team}, {Name: "local", Dir: local}}

	matched, shadowed, err := ExpandAll(roots, "rules/*.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(matched) != 1 || matched[0].Source != "local" {
		t.Fatalf("matched = %+v, want only local", matched)
	}
	var names []string
	for _, m := range shadowed {
		names = append(names, m.Source)
	}
	if !reflect.DeepEqual(names, []string{"org", "team"}) {
		t.Errorf("shadowed = %v, want [org team] (lowest precedence first)", names)
	}
}

// TestExpandAllSkipsSourcePlumbing is the reproduction that motivated
// Expand's dot-dir prune: a primary rule globbing `**/*.md` used to reach
// into .sources/<name>, pushing every configured source's content a second
// time and mirroring friday's internal layout into the agent's config dir.
// It also never registered as a collision — the primary's match had a
// different Rel (.sources/team/rules/team.md), so lint could not see it.
func TestExpandAllSkipsSourcePlumbing(t *testing.T) {
	local := t.TempDir()
	team := filepath.Join(local, ".sources", "team")
	mkfileIn(t, local, "core.md", "core")
	mkfileIn(t, team, "rules/team.md", "team")

	matched, shadowed, err := ExpandAll([]Root{{Name: "team", Dir: team}, {Name: "local", Dir: local}}, "**/*.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(shadowed) != 0 {
		t.Errorf("shadowed = %+v, want none", shadowed)
	}
	// team's own root still contributes rules/team.md once, via that root —
	// what must not happen is the primary also matching it as
	// .sources/team/rules/team.md.
	want := map[string]string{"core.md": "local", "rules/team.md": "team"}
	if len(matched) != len(want) {
		t.Fatalf("matched = %+v, want exactly %v", matched, want)
	}
	for _, m := range matched {
		if src, ok := want[m.Rel]; !ok || m.Source != src {
			t.Errorf("unexpected match %+v (want %v)", m, want)
		}
	}
}

// TestExpandLiteralAcrossRootsEveryVariantContributes is the entry-file
// exception (D11): unlike ExpandAll, every root's copy of a literal pattern
// contributes, in precedence order, none dropped as shadowed.
func TestExpandLiteralAcrossRootsEveryVariantContributes(t *testing.T) {
	team := t.TempDir()
	local := t.TempDir()
	mkfileIn(t, team, "core.md", "TEAM IDENTITY")
	mkfileIn(t, local, "core.md", "LOCAL IDENTITY")

	matches, err := ExpandLiteralAcrossRoots([]Root{{Name: "team", Dir: team}, {Name: "local", Dir: local}}, "core.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 {
		t.Fatalf("matches = %+v, want both sources' core.md", matches)
	}
	if matches[0].Source != "team" || matches[1].Source != "local" {
		t.Errorf("matches = %+v, want team (lowest precedence) first", matches)
	}
}

// TestExpandLiteralAcrossRootsSingleSource preserves single-source behavior:
// only the primary has the file, so exactly one match comes back.
func TestExpandLiteralAcrossRootsSingleSource(t *testing.T) {
	local := t.TempDir()
	mkfileIn(t, local, "core.md", "x")
	matches, err := ExpandLiteralAcrossRoots([]Root{{Name: "local", Dir: local}}, "core.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].Source != "local" {
		t.Errorf("matches = %+v, want exactly one local match", matches)
	}
}

// TestResolveSingleHighestPrecedenceWins covers merge-json's single-winner
// need: the primary (last root) wins when it has the file; otherwise an
// earlier (lower-precedence) source is used.
func TestResolveSingleHighestPrecedenceWins(t *testing.T) {
	team := t.TempDir()
	local := t.TempDir()
	mkfileIn(t, team, "hooks/hooks.json", "{}")

	// Only team has it: team wins by elimination, nothing shadowed.
	root, shadowed, ok := ResolveSingle([]Root{{Name: "team", Dir: team}, {Name: "local", Dir: local}}, "hooks/hooks.json")
	if !ok || root.Name != "team" {
		t.Fatalf("ResolveSingle = %+v, %v; want team", root, ok)
	}
	if len(shadowed) != 0 {
		t.Errorf("shadowed = %+v, want none", shadowed)
	}

	// Once local also has it, the higher-precedence local wins and team is
	// reported shadowed instead of silently dropped.
	mkfileIn(t, local, "hooks/hooks.json", "{}")
	root, shadowed, ok = ResolveSingle([]Root{{Name: "team", Dir: team}, {Name: "local", Dir: local}}, "hooks/hooks.json")
	if !ok || root.Name != "local" {
		t.Fatalf("ResolveSingle = %+v, %v; want local", root, ok)
	}
	if len(shadowed) != 1 || shadowed[0].Name != "team" {
		t.Fatalf("shadowed = %+v, want [team]", shadowed)
	}

	// Neither has it.
	if _, _, ok := ResolveSingle([]Root{{Name: "team", Dir: team}}, "missing.json"); ok {
		t.Error("ResolveSingle found a file that doesn't exist")
	}
}

// TestResolveSingleShadowedOrderIsPrecedenceOrder pins the order the
// merge-json/merge-toml warning reads back: lowest precedence first, the
// same order `sources:` declares. Three roots is the case a two-root test
// cannot distinguish from an arbitrary map/sort order.
func TestResolveSingleShadowedOrderIsPrecedenceOrder(t *testing.T) {
	org, team, local := t.TempDir(), t.TempDir(), t.TempDir()
	for _, dir := range []string{org, team, local} {
		mkfileIn(t, dir, "settings/codex.toml", "x")
	}
	roots := []Root{{Name: "org", Dir: org}, {Name: "team", Dir: team}, {Name: "local", Dir: local}}

	winner, shadowed, ok := ResolveSingle(roots, "settings/codex.toml")
	if !ok || winner.Name != "local" {
		t.Fatalf("winner = %+v, %v; want local", winner, ok)
	}
	var names []string
	for _, r := range shadowed {
		names = append(names, r.Name)
	}
	if !reflect.DeepEqual(names, []string{"org", "team"}) {
		t.Errorf("shadowed = %v, want [org team] (lowest precedence first)", names)
	}
}

// TestResolveSingleIgnoresDirectories guards the merge planners: a directory
// named like the wanted file is not a source, and must not win over a real
// file in a lower-precedence root.
func TestResolveSingleIgnoresDirectories(t *testing.T) {
	team, local := t.TempDir(), t.TempDir()
	mkfileIn(t, team, "hooks/hooks.json", "{}")
	if err := os.MkdirAll(filepath.Join(local, "hooks", "hooks.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	winner, shadowed, ok := ResolveSingle([]Root{{Name: "team", Dir: team}, {Name: "local", Dir: local}}, "hooks/hooks.json")
	if !ok || winner.Name != "team" {
		t.Fatalf("winner = %+v, %v; want team (local's is a directory)", winner, ok)
	}
	if len(shadowed) != 0 {
		t.Errorf("shadowed = %+v, want none", shadowed)
	}
}

// Two roots holding the same logical file under different casing collide on a
// case-insensitive filesystem: both resolve to one destination path, so keying
// them apart would let the second write clobber the first with nothing
// reported as shadowed. On a case-sensitive filesystem they are genuinely two
// files and both must survive.
func TestExpandAllFoldsCaseOnlyCollisionWhereTheFSDoes(t *testing.T) {
	team, local := t.TempDir(), t.TempDir()
	mkfileIn(t, team, "rules/Team.md", "TEAM")
	mkfileIn(t, local, "rules/team.md", "LOCAL")

	matched, shadowed, err := ExpandAll([]Root{{Name: "team", Dir: team}, {Name: "local", Dir: local}}, "rules/*.md")
	if err != nil {
		t.Fatal(err)
	}
	if caseInsensitiveFS {
		if len(matched) != 1 || matched[0].Source != "local" {
			t.Fatalf("matched = %+v, want only local's copy", matched)
		}
		if len(shadowed) != 1 || shadowed[0].Source != "team" {
			t.Fatalf("shadowed = %+v, want team's copy", shadowed)
		}
		// The winner keeps the casing that is actually on disk — only the
		// collision key folds, because that is what gets read and reported.
		if matched[0].Rel != "rules/team.md" {
			t.Errorf("Rel = %q, want the on-disk casing rules/team.md", matched[0].Rel)
		}
		return
	}
	if len(matched) != 2 {
		t.Fatalf("matched = %+v, want both files on a case-sensitive FS", matched)
	}
	if len(shadowed) != 0 {
		t.Errorf("shadowed = %+v, want none on a case-sensitive FS", shadowed)
	}
}

// Matches promises "same semantics as Expand". A pattern naming a dot segment
// after a wildcard used to break that: Matches accepted the path while
// Expand's blanket dot-directory prune never walked to it, so import's
// invertToFrom could capture content into a store path a later push could
// never find again.
func TestMatchesAndExpandAgree(t *testing.T) {
	root := t.TempDir()
	files := []string{
		"core.md",
		"rules/a.md",
		"rules/.hidden.md",
		"skills/one/main.md",
		"skills/two/sub/deep.md",
		"sub/.hidden/x.md",
		".hidden/y.md",
		"native/claude/.hidden/z.md",
		".sources/team/rules/team.md",
	}
	for _, f := range files {
		mkfileIn(t, root, f, "x")
	}

	patterns := []string{
		"*.md",
		"**/*.md",
		"rules/*.md",
		"rules/.*.md",
		"skills/**/*",
		"**/.hidden/*.md",
		".hidden/*.md",
		"native/*/.hidden/*.md",
		".sources/*/rules/*.md",
	}
	for _, pattern := range patterns {
		expanded, err := Expand(root, pattern)
		if err != nil {
			t.Fatalf("Expand(%q): %v", pattern, err)
		}
		found := map[string]bool{}
		for _, rel := range expanded {
			found[rel] = true
		}
		for _, f := range files {
			if got, want := found[f], Matches(pattern, f); got != want {
				t.Errorf("pattern %q, path %q: Expand found=%v, Matches=%v", pattern, f, got, want)
			}
		}
	}
}
