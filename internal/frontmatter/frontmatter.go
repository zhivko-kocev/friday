package frontmatter

import (
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/zhivko-kocev/friday/internal/textnorm"
)

const sep = "\n---\n"

// Parse splits markdown content into frontmatter fields and body.
// Returns nil map if no frontmatter present. CRLF inputs are handled
// transparently — the returned body uses LF line endings.
func Parse(content string) (fields map[string]any, body string) {
	norm := string(textnorm.Newlines([]byte(content)))
	if !strings.HasPrefix(norm, "---\n") {
		return nil, content
	}
	rest := norm[4:]
	end := strings.Index(rest, sep)
	if end < 0 {
		return nil, content
	}
	fields = map[string]any{}
	_ = yaml.Unmarshal([]byte(rest[:end]), &fields)
	return fields, rest[end+len(sep):]
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
