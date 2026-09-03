package cli

import "strings"

// renameFlag rewrites a deprecated flag token to its canonical spelling so the
// old name keeps working while only the new one is documented. It matches
// "--old", "-old", and their "=value" forms. Returns the rewritten args and
// whether the old name was actually used.
//
// The nudge is returned rather than printed: the rewrite has to happen before
// urfave parses anything, which is also before --quiet and --no-color have
// taken effect, so printing here forced the note onto every run including the
// ones that asked for silence.
func renameFlag(args []string, old, canonical string) ([]string, bool) {
	hit := false
	out := make([]string, len(args))
	for i, a := range args {
		switch {
		case a == "--"+old || a == "-"+old:
			out[i] = "--" + canonical
			hit = true
		case strings.HasPrefix(a, "--"+old+"="):
			out[i] = "--" + canonical + "=" + strings.TrimPrefix(a, "--"+old+"=")
			hit = true
		case strings.HasPrefix(a, "-"+old+"="):
			out[i] = "--" + canonical + "=" + strings.TrimPrefix(a, "-"+old+"=")
			hit = true
		default:
			out[i] = a
		}
	}
	return out, hit
}
