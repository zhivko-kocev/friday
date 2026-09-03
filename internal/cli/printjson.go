package cli

import (
	"encoding/json"
	"fmt"

	ucli "github.com/urfave/cli/v3"

	"github.com/zhivko-kocev/friday/internal/output"
)

// printJSON is the package's single machine-output emitter: every --json
// path marshals indented and writes to stdout through fmt, deliberately
// bypassing internal/output so a --quiet or colorized human channel can
// never contaminate bytes a script parses. A marshal failure is a bug in
// the payload type, so it reports on stderr and exits 1 rather than
// printing half a document.
func printJSON(v any) error {
	blob, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		output.Err("%v", err)
		return ucli.Exit("", 1)
	}
	fmt.Println(string(blob))
	return nil
}
