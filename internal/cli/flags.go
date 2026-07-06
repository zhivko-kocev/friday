package cli

import (
	"flag"
	"slices"
)

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
