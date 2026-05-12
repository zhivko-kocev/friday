// Package cli is the friday command dispatcher.
package cli

import (
	"fmt"
	"os"

	"github.com/zhivko-kocev/friday/internal/output"
)

var version = "dev"

// Run is the CLI entry point. main passes os.Args[1:] and the build-time
// version stamp.
func Run(args []string, ver string) int {
	version = ver
	args = applyGlobalFlags(args)
	if len(args) == 0 {
		printUsage()
		return 1
	}
	switch args[0] {
	case "push":
		return cmdPush(args[1:])
	case "pull":
		return cmdPull(args[1:])
	case "status":
		return cmdStatus(args[1:])
	case "init":
		return cmdInit(args[1:])
	case "list", "ls":
		return cmdList(args[1:])
	case "remote":
		return cmdRemote(args[1:])
	case "doctor":
		return cmdDoctor(args[1:])
	case "version", "--version", "-v":
		fmt.Println("friday " + version)
		return 0
	case "help", "--help", "-h":
		printUsage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "friday: unknown command %q\n\n", args[0])
		printUsage()
		return 1
	}
}

// applyGlobalFlags strips flags that should be honored by every subcommand
// (only --no-color today) before dispatch. Per-command flagsets don't need
// to know about it.
func applyGlobalFlags(args []string) []string {
	out := args[:0:0]
	for _, a := range args {
		if a == "--no-color" {
			output.SetColor(false)
			continue
		}
		out = append(out, a)
	}
	return out
}
