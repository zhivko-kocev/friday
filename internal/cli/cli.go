// Package cli is the friday command dispatcher.
package cli

import (
	"flag"
	"fmt"
	"os"
	"slices"

	"github.com/zhivko-kocev/friday/internal/output"
)

var version = "dev"

// command is one dispatchable subcommand. The table drives dispatch and
// shell completion from the same data, so they can't drift apart; flags()
// builds a fresh flagset purely for introspection — cmdX functions bind
// their own via the shared *Flags constructors.
type command struct {
	name              string
	aliases           []string
	run               func(args []string) int
	flags             func() *flag.FlagSet // nil = command takes no flags
	subcommands       []string             // completed after the command name
	completesAdapters bool                 // positional args complete to adapter names
}

func commandTable() []command {
	return []command{
		{name: "init", run: cmdInit, flags: func() *flag.FlagSet { return initFlags(&initOpts{}) }},
		{name: "setup", run: cmdSetup, flags: func() *flag.FlagSet { return setupFlags(&setupOpts{}) }},
		{name: "promote", run: cmdPromote, flags: func() *flag.FlagSet { return promoteFlags(&promoteOpts{}) }},
		{name: "push", run: cmdPush, flags: func() *flag.FlagSet { return pushFlags(&pushOpts{}) }, completesAdapters: true},
		{name: "pull", run: cmdPull, flags: func() *flag.FlagSet { return pullFlags(&pullOpts{}) }, completesAdapters: true},
		{name: "sync", run: cmdSync, flags: func() *flag.FlagSet { return syncFlags(&syncOpts{}) }, completesAdapters: true},
		{name: "status", run: cmdStatus, flags: func() *flag.FlagSet { return statusFlags(&statusOpts{}) }, completesAdapters: true},
		{name: "explain", run: cmdExplain},
		{name: "import", run: cmdImport, flags: func() *flag.FlagSet { return importFlags(&importOpts{}) }, completesAdapters: true},
		{name: "compile", run: cmdCompile, flags: func() *flag.FlagSet { return compileFlags(&compileOpts{}) }},
		{name: "list", aliases: []string{"ls"}, run: cmdList},
		{name: "rollback", run: cmdRollback, flags: func() *flag.FlagSet { return rollbackFlags(&rollbackOpts{}) }},
		{name: "plugin", run: cmdPlugin, subcommands: []string{"list", "validate"}},
		{name: "lint", run: cmdLint},
		{name: "eject", run: cmdEject, flags: func() *flag.FlagSet { return ejectFlags(&ejectOpts{}) }},
		{name: "remote", run: cmdRemote, subcommands: []string{"init", "pull", "push", "propose", "status"}},
		{name: "doctor", run: cmdDoctor},
		{name: "completion", run: cmdCompletion, subcommands: []string{"bash", "zsh", "fish"}},
		{name: "version", run: cmdVersion},
		{name: "help", run: func([]string) int { printUsage(); return 0 }},
	}
}

func cmdVersion([]string) int {
	fmt.Println("friday " + version)
	return 0
}

// Run is the CLI entry point. main passes os.Args[1:] and the build-time
// version stamp.
func Run(args []string, ver string) int {
	version = ver
	args = applyGlobalFlags(args)
	if len(args) == 0 {
		printUsage()
		return 1
	}

	name := args[0]
	switch name {
	case "--version", "-v":
		name = "version"
	case "--help", "-h":
		name = "help"
	case "__complete": // hidden callback for the completion scripts
		return cmdComplete(args[1:])
	}

	for _, c := range commandTable() {
		if c.name == name || slices.Contains(c.aliases, name) {
			return c.run(args[1:])
		}
	}
	fmt.Fprintf(os.Stderr, "friday: unknown command %q\n\n", args[0])
	printUsage()
	return 1
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
