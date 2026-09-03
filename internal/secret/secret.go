// Package secret detects credential-shaped values inside friday's mcp.yaml,
// so a real API key never gets committed to a store that's typically a git
// repo. This is friday's only secret handling — no encryption, no secret
// store (D16) — it steers the user toward an env-variable reference
// (${VAR}) instead of catching a leak after the fact.
package secret

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/zhivko-kocev/friday/internal/spec"
)

// vendorPatterns are the well-known credential formats friday recognises by
// shape alone — unambiguous regardless of which field holds them, so they
// apply even to a positional value (an MCP server's Args) that has no key
// name at all. Each alternative carries its own length floor rather than
// deferring to one global one: a real AWS access key ID is exactly 20
// characters and a GitLab PAT 25, both below any floor wide enough to keep
// base64 noise out. Adding a vendor is an edit to this list alone.
var vendorPatterns = []string{
	`sk-[A-Za-z0-9_-]{16,}`,                        // OpenAI / Anthropic
	`gh[pousr]_[A-Za-z0-9]{20,}`,                   // GitHub PAT, OAuth, user-/server-to-server, refresh
	`github_pat_[A-Za-z0-9_]{20,}`,                 // GitHub fine-grained PAT
	`xox[baprse]-[A-Za-z0-9-]{10,}`,                // Slack
	`AIza[A-Za-z0-9_-]{20,}`,                       // Google API key
	`ya29\.[A-Za-z0-9_-]{20,}`,                     // Google OAuth access token
	`A[KS]IA[A-Z0-9]{16}`,                          // AWS access key ID (always exactly 20 chars)
	`glpat-[A-Za-z0-9_-]{16,}`,                     // GitLab personal access token
	`dop_v1_[A-Za-z0-9]{32,}`,                      // DigitalOcean
	`eyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.`, // JWT: header.payload.signature
	`-----BEGIN `,                                  // PEM private key / certificate block
}

var vendorPrefix = regexp.MustCompile(`(?i)^(?:` + strings.Join(vendorPatterns, "|") + `)`)

// authScheme and flagPrefix are the two wrappers a credential arrives in
// when it is pasted where it is actually used — an HTTP Authorization
// header value or a command-line flag. Both defeat vendorPrefix's start
// anchor, and "Bearer <token>" is the single commonest shape a real token
// takes in an MCP config, so unwrap strips them before matching.
var (
	authScheme = regexp.MustCompile(`(?i)^(?:bearer|token|basic|apikey)\s+`)
	flagPrefix = regexp.MustCompile(`(?i)^--?[a-z0-9][a-z0-9-]*=`)
)

// unwrap removes a leading auth scheme word or `--flag=` prefix. It never
// touches a value that has neither, so a PEM block (which starts with the
// dashes flagPrefix would otherwise chew into) survives intact.
func unwrap(value string) string {
	if m := authScheme.FindString(value); m != "" {
		return strings.TrimSpace(value[len(m):])
	}
	if m := flagPrefix.FindString(value); m != "" {
		return strings.TrimSpace(value[len(m):])
	}
	return value
}

// sensitiveKey matches a map key name that suggests its value is a secret.
// Bounded by a non-letter on both sides, so the bare `key` alternative that
// catches API_KEY does not also fire on MONKEY_CONFIG or KEYBOARD_LAYOUT.
var sensitiveKey = regexp.MustCompile(`(?i)(^|[^a-z])(` + strings.Join([]string{
	`tokens?`,
	`keys?`,
	`api[_-]?keys?`,
	`secrets?`,
	`passw(or)?ds?`, `passwd`, `pwd`, `pass`, `passphrase`,
	`credentials?`, `creds?`,
	`auth`, `authorization`,
	`cookie`,
	`bearer`,
	`pat`,
	`signature`,
	`session[_-]?id`,
}, "|") + `)([^a-z]|$)`)

// userinfoURL matches an inline credential in a URL's userinfo component
// (https://user:s3cr3t@host) — a common MCP auth shortcut that no value-shape
// rule can catch, because the password may be any string at all.
var userinfoURL = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*://[^/@\s]*:[^/@\s]+@`)

// randomAlphabet is the character set a base64 or hex credential is drawn
// from; both base64 alphabets are admitted (standard `+/`, URL-safe `-_`)
// because real tokens use either.
var randomAlphabet = regexp.MustCompile(`^[A-Za-z0-9+/=_-]{32,}$`)

// randomLooking reports whether value is a long, high-entropy-looking blob
// rather than an ordinary long string. Two shapes the alphabet alone admits
// are excluded, both observed as doctor Errors on legitimate config: a value
// with no digit or no letter (an ordinary POSIX path), and a value mixing
// `/` with `-` — no credential does that, but every path and every
// provider-qualified model name ("anthropic/claude-sonnet-4-5-20250929")
// does.
func randomLooking(value string) bool {
	if !randomAlphabet.MatchString(value) {
		return false
	}
	if strings.Contains(value, "/") && strings.Contains(value, "-") {
		return false
	}
	var hasDigit, hasLetter bool
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
			hasDigit = true
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			hasLetter = true
		}
	}
	return hasDigit && hasLetter
}

// Finding names one mcp.yaml value that looks like a real credential rather
// than an environment-variable reference. Key is a dotted, display-ready
// path (e.g. "github.env.GITHUB_TOKEN"). The flagged value is deliberately
// not carried on Finding: a lint result may end up pasted into an issue
// tracker or CI log, and the key path alone is enough to find and fix it.
type Finding struct {
	Key string
}

// Message is the single user-facing sentence every caller renders for a
// finding, so doctor, push, claim, and share all say the same thing and none
// of them can accidentally interpolate the credential itself.
func (f Finding) Message() string {
	return fmt.Sprintf("%s looks like a real credential, not an env reference — use ${VAR} instead", f.Key)
}

// ScanYAML parses mcp.yaml content and scans it — the entry point for a
// caller holding raw file bytes rather than a parsed spec.MCP (push, claim,
// share). Content that does not parse yields no findings: the caller's own
// schema rule already reports that, and a second error filed under a
// credential rule would only mislead.
func ScanYAML(data []byte) []Finding {
	m, err := spec.ParseMCP(data)
	if err != nil {
		return nil
	}
	return Scan(*m)
}

// ScanAny walks an arbitrary decoded document — YAML, JSON, and TOML all
// decode to the same map/slice/scalar shapes — and reports every string leaf
// that looks like a credential, so a caller can scan a store file whose
// schema friday does not model at all (native/**, settings/<adapter>.json).
// prefix is the display path findings are built under, typically the store-
// relative filename. A value inside a slice is judged on its own shape
// alone, with no key name to consult, exactly as Scan treats an Args entry.
func ScanAny(prefix string, v any) []Finding {
	return scanAny(prefix, "", v)
}

func scanAny(path, leafKey string, v any) []Finding {
	switch t := v.(type) {
	case string:
		if looksLikeSecret(leafKey, t) {
			return []Finding{{Key: path}}
		}
	case map[string]any:
		return scanAnyMap(path, t)
	case map[string]string:
		typed := make(map[string]any, len(t))
		for k, s := range t {
			typed[k] = s
		}
		return scanAnyMap(path, typed)
	case []any:
		var out []Finding
		for i, e := range t {
			out = append(out, scanAny(fmt.Sprintf("%s[%d]", path, i), "", e)...)
		}
		return out
	case []string:
		var out []Finding
		for i, s := range t {
			if looksLikeSecret("", s) {
				out = append(out, Finding{Key: fmt.Sprintf("%s[%d]", path, i)})
			}
		}
		return out
	}
	return nil
}

// scanAnyMap visits a map's keys in sorted order so a walk over the same
// document always reports the same findings in the same order.
func scanAnyMap(path string, m map[string]any) []Finding {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var out []Finding
	for _, k := range keys {
		child := k
		if path != "" {
			child = path + "." + k
		}
		out = append(out, scanAny(child, k, m[k])...)
	}
	return out
}

// Scan walks m's servers for a literal credential in any free-form value the
// mcp.yaml schema lets a user paste one into: Command, Args, Env, URL, and
// Headers. Server names are sorted first, and each server's fields visited
// in a fixed order, so results are deterministic.
func Scan(m spec.MCP) []Finding {
	names := make([]string, 0, len(m.Servers))
	for name := range m.Servers {
		names = append(names, name)
	}
	sort.Strings(names)

	var out []Finding
	for _, name := range names {
		srv := m.Servers[name]
		if looksLikeSecret("", srv.Command) {
			out = append(out, Finding{Key: name + ".command"})
		}
		for i, arg := range srv.Args {
			if looksLikeSecret("", arg) {
				out = append(out, Finding{Key: fmt.Sprintf("%s.args[%d]", name, i)})
			}
		}
		out = append(out, scanValues(name+".env", srv.Env)...)
		if looksLikeSecret("", srv.URL) {
			out = append(out, Finding{Key: name + ".url"})
		}
		out = append(out, scanValues(name+".headers", srv.Headers)...)
	}
	return out
}

// scanValues checks one env/headers map, returning one Finding per key whose
// value looks like a real credential. Keys are sorted first for determinism.
func scanValues(prefix string, m map[string]string) []Finding {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var out []Finding
	for _, k := range keys {
		if looksLikeSecret(k, m[k]) {
			out = append(out, Finding{Key: prefix + "." + k})
		}
	}
	return out
}

// looksLikeSecret reports whether value is a literal credential rather than
// an env reference. key is empty for a positional value that has no key name
// to check against sensitiveKey — such a value can still be flagged by its
// own shape.
func looksLikeSecret(key, value string) bool {
	value = strings.TrimSpace(value)
	// An env reference is the shape friday steers users toward; it is never
	// a leak, whatever it is wrapped in ("Bearer ${LINEAR_KEY}").
	if value == "" || strings.Contains(value, "${") {
		return false
	}
	if userinfoURL.MatchString(value) {
		return true
	}
	if vendorPrefix.MatchString(value) {
		return true
	}
	bare := unwrap(value)
	if bare != value && vendorPrefix.MatchString(bare) {
		return true
	}
	return key != "" && sensitiveKey.MatchString(key) && randomLooking(bare)
}
