package frontmatter

import (
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/zhivko-kocev/friday/internal/textnorm"
)

const sep = "\n---\n"

// Parse splits markdown content into frontmatter fields and body.
// Returns nil map if no frontmatter present. CRLF inputs are handled
// transparently — the returned body uses LF line endings. Malformed YAML is
// swallowed (fields come back empty) — use ParseStrict to surface it.
func Parse(content string) (fields map[string]any, body string) {
	fields, body, _ = ParseStrict(content)
	return fields, body
}

// ParseStrict is Parse with the YAML error surfaced, for lint-style callers
// that need to report malformed frontmatter instead of ignoring it.
func ParseStrict(content string) (fields map[string]any, body string, err error) {
	norm := string(textnorm.Newlines([]byte(content)))
	if !strings.HasPrefix(norm, "---\n") {
		return nil, content, nil
	}
	rest := norm[4:]
	end := strings.Index(rest, sep)
	if end < 0 {
		return nil, content, nil
	}
	fields = map[string]any{}
	err = yaml.Unmarshal([]byte(rest[:end]), &fields)
	return fields, rest[end+len(sep):], err
}

// Strip removes listed keys from the frontmatter and returns the modified content.
// If no frontmatter or no fields to strip, returns content unchanged.
func Strip(content string, keys []string) string {
	if len(keys) == 0 {
		return content
	}
	fields, body := Parse(content)
	if fields == nil {
		return content
	}
	strip := make(map[string]bool, len(keys))
	for _, k := range keys {
		strip[k] = true
	}
	filtered := map[string]any{}
	for k, v := range fields {
		if !strip[k] {
			filtered[k] = v
		}
	}
	if len(filtered) == 0 {
		return strings.TrimLeft(body, "\n")
	}
	out, err := yaml.Marshal(filtered)
	if err != nil {
		return content
	}
	return "---\n" + string(out) + "---\n" + body
}
