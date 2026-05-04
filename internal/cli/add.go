package cli

import (
	"flag"
	"strings"

	"github.com/zhivko-kocev/friday/internal/initcmd"
	"github.com/zhivko-kocev/friday/internal/output"
	"github.com/zhivko-kocev/friday/internal/presets"
)

// cmdAdd — append preset to user store's friday.yaml.
func cmdAdd(args []string) int {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	target := fs.String("target", "", "override the preset's default target directory")
	force := fs.Bool("force", false, "replace an existing adapter entry")
	list := fs.Bool("list", false, "list available presets and exit (alias: friday list presets)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *list {
		printPresets()
		return 0
	}
	if fs.NArg() == 0 {
		output.Err("usage: friday add <preset> [--target dir] [--force]")
		output.Dim("available: %s", strings.Join(presets.Names(), ", "))
		return 1
	}
	cfg, err := loadUserForWrite()
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	for _, name := range fs.Args() {
		if err := initcmd.AddAdapter(cfg, name, *target, *force); err != nil {
			output.Err("%v", err)
			return 1
		}
	}
	return 0
}
