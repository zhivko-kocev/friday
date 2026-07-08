package cli

import (
	"flag"
	"slices"
	"strings"

	"github.com/zhivko-kocev/friday/internal/output"
)

// renameFlag rewrites a deprecated flag token to its canonical spelling so the
// old name keeps working while only the new one is documented, printing a
// one-time nudge when the old name is used. It matches "--old", "-old", and
// their "=value" forms. Returns the rewritten args untouched when the old flag
// is absent.
func renameFlag(args []string, old, canonical, note string) []string {
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
	if hit {
		output.Warn("%s", note)
	}
	return out
}

// parseInterleaved parses a command line where flags may appear before,
// between, or after positional arguments. Stdlib flag.Parse stops at the
// first positional, which silently turns trailing flags into positionals —
// `friday promote <path> --propose -m "..."` would capture the path and drop
// the MR without a word. Everything after a bare "--" stays positional
// verbatim. Returns the positional arguments in order.
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var tail []string
	if i := slices.Index(args, "--"); i >= 0 {
		tail = args[i+1:]
		args = args[:i]
	}
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return append(pos, tail...), nil
		}
		// Parse stops at the first non-flag argument: take it as a
		// positional and resume parsing after it.
		pos = append(pos, args[0])
		args = args[1:]
	}
}
