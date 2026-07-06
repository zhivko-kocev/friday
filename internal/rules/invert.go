package rules

import (
	"path"
	"strings"
)

// invertibleTokens are the templates reverse expansion understands: a literal
// prefix followed by exactly one trailing {filename} or {relpath}. That
// covers every shipped preset; anything fancier ({stem}{ext} splits, {dir},
// mid-template tokens) degrades to "not invertible" and the caller reports
// the rule as unsupported. Predictable beats clever.
var invertibleTokens = []string{"{filename}", "{relpath}"}

// ToGlob converts a rule's `to` template into a target-relative glob that
// matches everything the rule could have written:
//
//	"CLAUDE.md"         → "CLAUDE.md"  (literal)
//	"agents/{filename}" → "agents/*"
//	"skills/{relpath}"  → "skills/**/*"
//
// ok is false when the template isn't invertible.
func ToGlob(template string) (glob string, ok bool) {
	t := path.Clean(strings.ReplaceAll(template, "\\", "/"))
	if !hasToken(t) {
		return t, true
	}
	prefix, tok, invertible := splitTemplate(t)
	if !invertible {
		return "", false
	}
	if tok == "{filename}" {
		return prefix + "*", true
	}
	return prefix + "**/*", true
}

// Invert maps a path the rule wrote back to the store-relative path a push
// would have read it from. targetRel is relative to the adapter target;
// fromAnchor is Anchor() of the rule's from-pattern. Literal templates are
// not handled here — with no token there is no path structure to recover,
// so the caller maps them straight to the rule's from-pattern.
func Invert(template, targetRel, fromAnchor string) (storeRel string, ok bool) {
	t := path.Clean(strings.ReplaceAll(template, "\\", "/"))
	p := path.Clean(strings.ReplaceAll(targetRel, "\\", "/"))
	prefix, tok, invertible := splitTemplate(t)
	if !invertible {
		return "", false
	}
	rest := strings.TrimPrefix(p, prefix)
	if rest == p && prefix != "" {
		return "", false // target doesn't live under the template's prefix
	}
	if tok == "{filename}" && strings.Contains(rest, "/") {
		return "", false // {filename} never expands across segments
	}
	if fromAnchor == "" {
		return rest, true
	}
	return path.Join(fromAnchor, rest), true
}

// splitTemplate breaks "prefix{token}" templates into their literal prefix
// (including the trailing slash) and the token. ok is false for templates
// with no token, multiple tokens, non-trailing tokens, or unsupported ones.
func splitTemplate(t string) (prefix, token string, ok bool) {
	for _, tok := range invertibleTokens {
		if !strings.HasSuffix(t, tok) {
			continue
		}
		prefix = strings.TrimSuffix(t, tok)
		if hasToken(prefix) || hasWildcard(prefix) {
			return "", "", false
		}
		return prefix, tok, true
	}
	return "", "", false
}
