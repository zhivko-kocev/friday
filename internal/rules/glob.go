package rules

import (
	"io/fs"
	"os"
	"path/filepath"
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
		if hasWildcard(p) {
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
func Expand(storeRoot, pattern string) ([]string, error) {
	pattern = filepath.ToSlash(pattern)
	if !hasWildcard(pattern) {
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

	var matches []string
	err := filepath.WalkDir(walkRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return filepath.SkipDir
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(storeRoot, path)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
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
		// ** matches zero or more path segments — but never traverses into
		// a dotfile/dotdir unless the next pattern segment explicitly starts
		// with a dot. This is the convention most shells use.
		for i := 0; i <= len(s); i++ {
			if i < len(s) && strings.HasPrefix(s[i], ".") {
				if len(p) > 1 && strings.HasPrefix(p[1], ".") {
					// next pattern requests dotfiles — allow
				} else {
					continue
				}
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

func hasWildcard(s string) bool { return strings.ContainsAny(s, "*?[") }
