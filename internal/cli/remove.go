package cli

import (
	"flag"

	"github.com/zhivko-kocev/friday/internal/initcmd"
	"github.com/zhivko-kocev/friday/internal/output"
)

// cmdRemove — delete an adapter entry from the user store's friday.yaml.
// Symmetric with `friday add`. Does not touch the on-disk target dir.
func cmdRemove(args []string) int {
	fs := flag.NewFlagSet("remove", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() == 0 {
		output.Err("usage: friday remove <adapter>")
		return 1
	}
	cfg, err := loadUserForWrite()
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	for _, name := range fs.Args() {
		if err := initcmd.RemoveAdapter(cfg, name); err != nil {
			output.Err("%v", err)
			return 1
		}
	}
	return 0
}
