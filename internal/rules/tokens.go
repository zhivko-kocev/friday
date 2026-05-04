package rules

import (
	"path/filepath"
	"strings"
)

// Tokens is the set of substitutions available in a rule's `to` field.
type Tokens struct {
	Filename string // "researcher.md"
	Stem     string // "researcher"
	Ext      string // ".md"
	Relpath  string // path relative to the rule's anchor
	Dir      string // dir portion of Relpath ("" if file is at anchor root)
}

// TokensFor computes tokens for a matched store-relative path against a rule
// anchor (also store-relative).
func TokensFor(matchPath, anchor string) Tokens {
	matchPath = filepath.ToSlash(matchPath)
	anchor = filepath.ToSlash(anchor)

	rel := matchPath
	if anchor != "" {
		if r, err := filepath.Rel(anchor, matchPath); err == nil {
			rel = filepath.ToSlash(r)
		}
	}
	base := filepath.Base(matchPath)
	ext := filepath.Ext(base)
	dir := filepath.ToSlash(filepath.Dir(rel))
	if dir == "." {
		dir = ""
	}
	return Tokens{
		Filename: base,
		Stem:     strings.TrimSuffix(base, ext),
		Ext:      ext,
		Relpath:  rel,
		Dir:      dir,
	}
}

// Expand performs token substitution on a target template.
func (t Tokens) Expand(template string) string {
	r := strings.NewReplacer(
		"{filename}", t.Filename,
		"{stem}", t.Stem,
		"{ext}", t.Ext,
		"{relpath}", t.Relpath,
		"{dir}", t.Dir,
	)
	return r.Replace(template)
}

func hasToken(s string) bool {
	for _, tok := range []string{"{filename}", "{stem}", "{ext}", "{relpath}", "{dir}"} {
		if strings.Contains(s, tok) {
			return true
		}
	}
	return false
}
