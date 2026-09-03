package rules

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
)

// Anchor returns the longest literal directory prefix of a pattern.
//
//	"rules/general.md" → "rules"        (literal — anchor is parent dir)
//	"agents/*.md"      → "agents"
//	"skills/**/*.md"   → "skills"
//	"foo.md"           → ""
//	"*.md"             → ""
func Anchor(pattern string) string {
	parts := strings.Split(filepath.ToSlash(pattern), "/")
	literal := []string{}
	wildcardSeen := false
	for _, p := range parts {
		if HasWildcard(p) {
			wildcardSeen = true
			break
		}
		literal = append(literal, p)
	}
	// Pure literal: anchor is the parent directory of the file.
	if !wildcardSeen && len(literal) > 0 {
		literal = literal[:len(literal)-1]
	}
	return strings.Join(literal, "/")
}

// Expand resolves a `from` pattern against storeRoot to a sorted list of
// store-relative paths. Returns an empty slice (no error) if nothing matches —
// the caller decides whether that's a warning or fatal.
//
// Dot-directories are not descended into: `.sources/` (friday's own source
// clones) and `.git/` are plumbing, never store content, and a `**` segment
// would otherwise walk straight through them. A pattern naming a dot segment
// at that position opts back in, matching the dotfile convention for the
// final path segment. The prune is position-aware — not "every dot-dir below
// the walk root" — so that Expand and Matches cannot disagree about a
// pattern like `**/.hidden/*.md`.
func Expand(storeRoot, pattern string) ([]string, error) {
	pattern = filepath.ToSlash(pattern)
	if !HasWildcard(pattern) {
		abs := filepath.Join(storeRoot, pattern)
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			return nil, nil
		}
		return []string{pattern}, nil
	}

	walkRoot := storeRoot
	if a := Anchor(pattern); a != "" {
		walkRoot = filepath.Join(storeRoot, a)
	}
	patSegs := strings.Split(pattern, "/")

	var matches []string
	err := filepath.WalkDir(walkRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return filepath.SkipDir
			}
			return err
		}
		if d.IsDir() && path == walkRoot {
			return nil
		}
		rel, rerr := filepath.Rel(storeRoot, path)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && !dirCouldMatch(patSegs, strings.Split(rel, "/")) {
				return filepath.SkipDir
			}
			return nil
		}
		if matchPath(pattern, rel) {
			matches = append(matches, rel)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	sort.Strings(matches)
	return matches, nil
}

// Root is one directory a rule's glob patterns are expanded against —
// either the primary store or a cloned source under .sources/<name>.
type Root struct {
	Name string // "local" for the primary, else the source name
	Dir  string // absolute directory
}

// Match is one expanded file, tagged with the root it came from.
type Match struct {
	Source string // root name
	Root   string // root absolute dir
	Rel    string // path relative to Root
}

// ExpandAll runs Expand against every root and unions the results by
// relative path: a distinct filename from every source applies (D11), but
// when two roots hold the same relative path only the highest-precedence
// root's copy is kept — returned in matched — and every loser comes back in
// shadowed. roots is ordered lowest precedence first (source.Set's own
// convention), so a later root in the slice always wins a collision. A
// single-element roots slice reproduces Expand exactly, so an unconfigured
// (single-source) store renders byte-for-byte as before.
//
// shadowed is the authoritative answer to "which root's copy did a push
// actually drop": lint reports it as a Finding, and it must be derived here
// rather than from an independent walk, because only this function applies
// the same collision rule the renderer does. Order is by Rel, then ascending
// precedence within a Rel — a stable sort, so three roots holding one path
// report their two losers lowest-precedence first.
//
// A literal (glob-free) pattern must NOT be routed here: per D11 every
// root's copy of an entry file contributes, so nothing is shadowed. Use
// ExpandLiteralAcrossRoots and report no collision.
func ExpandAll(roots []Root, pattern string) (matched []Match, shadowed []Match, err error) {
	winners := map[string]Match{}
	for _, root := range roots {
		rels, err := Expand(root.Dir, pattern)
		if err != nil {
			return nil, nil, err
		}
		for _, rel := range rels {
			m := Match{Source: root.Name, Root: root.Dir, Rel: rel}
			key := dedupKey(rel)
			if prev, ok := winners[key]; ok {
				shadowed = append(shadowed, prev)
			}
			winners[key] = m
		}
	}
	matched = make([]Match, 0, len(winners))
	for _, m := range winners {
		matched = append(matched, m)
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].Rel < matched[j].Rel })
	sort.SliceStable(shadowed, func(i, j int) bool { return shadowed[i].Rel < shadowed[j].Rel })
	return matched, shadowed, nil
}

// caseInsensitiveFS reports whether the host's default filesystem collapses
// names that differ only in case: NTFS on Windows and APFS/HFS+ on macOS do,
// ext4 and friends do not.
const caseInsensitiveFS = runtime.GOOS == "windows" || runtime.GOOS == "darwin"

// dedupKey is the collision key ExpandAll unions on. It folds case where the
// filesystem does, because two roots holding rules/Team.md and rules/team.md
// resolve to one destination file there: keying them apart would let the
// second write silently clobber the first with nothing reported as shadowed.
// The Match keeps the unfolded Rel — only the comparison folds, so the winner
// is still read and reported under the casing that is actually on disk.
func dedupKey(rel string) string {
	if caseInsensitiveFS {
		return strings.ToLower(rel)
	}
	return rel
}

// ExpandLiteralAcrossRoots resolves a non-glob pattern against every root,
// returning one Match per root that has it — in root order (lowest
// precedence first). Unlike ExpandAll, nothing is dropped as shadowed: this
// is the entry-file exception (D11) a concatenate rule's literal from-entries
// need, where every source's own variant contributes a part instead of the
// primary silently shadowing the rest. Callers using it for genuinely
// glob-free rules (the common case) get exactly one match when only the
// primary has the file — today's single-source behavior.
func ExpandLiteralAcrossRoots(roots []Root, pattern string) ([]Match, error) {
	var out []Match
	for _, root := range roots {
		rels, err := Expand(root.Dir, pattern)
		if err != nil {
			return nil, err
		}
		for _, rel := range rels {
			out = append(out, Match{Source: root.Name, Root: root.Dir, Rel: rel})
		}
	}
	return out, nil
}

// ResolveSingle finds a literal store-relative path across roots, preferring
// the highest-precedence root (last in the slice) that has it. Used by
// single-file strategies (merge-json, merge-toml, a render rule's canonical
// schema) where exactly one winner is wanted, unlike a concatenate rule's
// every-source-contributes semantics.
//
// shadowed carries every lower-precedence root that also holds rel, lowest
// precedence first, because AGENTS.md promises a Warning rather than a silent
// drop for exactly these single-file rules — the caller cannot reconstruct
// that from the winner alone.
func ResolveSingle(roots []Root, rel string) (winner Root, shadowed []Root, found bool) {
	sub := filepath.FromSlash(rel)
	for i := len(roots) - 1; i >= 0; i-- {
		info, err := os.Stat(filepath.Join(roots[i].Dir, sub))
		if err != nil || info.IsDir() {
			continue
		}
		if !found {
			winner, found = roots[i], true
			continue
		}
		shadowed = append(shadowed, roots[i])
	}
	slices.Reverse(shadowed)
	return winner, shadowed, found
}

// Matches reports whether a store-relative path matches a from-pattern,
// without touching the filesystem. Same semantics as Expand, including the
// dotfile opt-in convention.
func Matches(pattern, path string) bool {
	return matchPath(filepath.ToSlash(pattern), filepath.ToSlash(path))
}

// matchPath returns true if path matches pattern.
//
//   - — any sequence except /
//     ?  — any single char except /
//     ** — any sequence including / (must be its own path segment)
func matchPath(pattern, path string) bool {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(path, "/"))
}

func matchSegments(p, s []string) bool {
	if len(p) == 0 {
		return len(s) == 0
	}
	if p[0] == "**" {
		// ** matches zero or more path segments, but stops dead at a
		// dotfile/dotdir unless the next pattern segment explicitly starts
		// with a dot — the convention most shells use. Stopping (rather than
		// skipping past) is what keeps `**/*.md` out of .sources/<name>/...:
		// merely refusing to *start* the remainder at a dot segment still let
		// ** swallow it and match deeper, which mirrored friday's own source
		// plumbing into agent dirs. Expand's WalkDir prunes the same dirs so
		// the two agree without walking what can never match.
		for i := 0; i <= len(s); i++ {
			if i < len(s) && strings.HasPrefix(s[i], ".") && !(len(p) > 1 && strings.HasPrefix(p[1], ".")) {
				return false
			}
			if matchSegments(p[1:], s[i:]) {
				return true
			}
		}
		return false
	}
	if len(s) == 0 {
		return false
	}
	// Hide dotfiles unless the pattern explicitly opts in.
	if strings.HasPrefix(s[0], ".") && !strings.HasPrefix(p[0], ".") {
		return false
	}
	ok, _ := filepath.Match(p[0], s[0])
	if !ok {
		return false
	}
	return matchSegments(p[1:], s[1:])
}

// dirCouldMatch reports whether any file under the directory whose
// store-relative segments are s could still match pattern p. It is
// matchSegments with the tail left open, so Expand prunes exactly the
// dot-directories matchSegments would refuse to cross. Without it a pattern
// like `**/.hidden/*.md` matches under Matches — which import's invertToFrom
// captures store content with — a path a later Expand could never find again.
func dirCouldMatch(p, s []string) bool {
	if len(s) == 0 {
		return true
	}
	if len(p) == 0 {
		return false
	}
	if p[0] == "**" {
		if strings.HasPrefix(s[0], ".") && !(len(p) > 1 && strings.HasPrefix(p[1], ".")) {
			return false
		}
		// ** either swallows this segment or ends here, letting p[1] take it.
		return dirCouldMatch(p, s[1:]) || dirCouldMatch(p[1:], s)
	}
	if strings.HasPrefix(s[0], ".") && !strings.HasPrefix(p[0], ".") {
		return false
	}
	if ok, _ := filepath.Match(p[0], s[0]); !ok {
		return false
	}
	return dirCouldMatch(p[1:], s[1:])
}

func HasWildcard(s string) bool { return strings.ContainsAny(s, "*?[") }
