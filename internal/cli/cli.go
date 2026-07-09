// Package cli is the friday command dispatcher.
package cli

import (
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/zhivko-kocev/friday/internal/output"
	"github.com/zhivko-kocev/friday/internal/ui"
)

// version is a fallback default; Run overwrites it with the build-time
// stamp that main passes in (see cmd/friday/main.go).
var version = "dev"

// command is one dispatchable subcommand. The table drives dispatch and
// shell completion from the same data, so they can't drift apart; flags()
// builds a fresh flagset purely for introspection — cmdX functions bind
// their own via the shared *Flags constructors.
type command struct {
	name              string
	aliases           []string
	summary           string // one-line help; "" omits it from listings (e.g. help)
	advanced          bool   // grouped under the "advanced" help tier
	run               func(args []string) int
	flags             func() *flag.FlagSet // nil = command takes no flags
	subcommands       []string             // completed after the command name
	completesAdapters bool                 // positional args complete to adapter names
}

// commandTable is the single source of truth for dispatch, shell completion,
// and the help listings — so none of the three can drift. The first block is
// the everyday porcelain (shown by `friday help`); the rest is plumbing, shown
// only by `friday help --all`. Nothing is ever removed: demoted commands still
// dispatch and complete exactly as before.
func commandTable() []command {
	return []command{
		{name: "init", summary: "clone or scaffold your ~/.friday store", run: cmdInit, flags: func() *flag.FlagSet { return initFlags(&initOpts{}) }},
		{name: "setup", summary: "add friday knowledge to the current project", run: cmdSetup, flags: func() *flag.FlagSet { return setupFlags(&setupOpts{}) }},
		{name: "sync", summary: "capture local edits, then fan them to every agent", run: cmdSync, flags: func() *flag.FlagSet { return syncFlags(&syncOpts{}) }, completesAdapters: true},
		{name: "status", summary: "show what would change (no writes)", run: cmdStatus, flags: func() *flag.FlagSet { return statusFlags(&statusOpts{}) }, completesAdapters: true},
		{name: "share", summary: "propose your store changes for team review (opens an MR)", run: cmdShare, flags: func() *flag.FlagSet { return proposeFlags(&proposeOpts{}) }},

		{name: "push", summary: "one-way sync: store → installed agents", advanced: true, run: cmdPush, flags: func() *flag.FlagSet { return pushFlags(&pushOpts{}) }, completesAdapters: true},
		{name: "pull", summary: "one-way sync: agent edits → store (--discover finds new files)", advanced: true, run: cmdPull, flags: func() *flag.FlagSet { return pullFlags(&pullOpts{}) }, completesAdapters: true},
		{name: "promote", summary: "capture project agent config back into ~/.friday", advanced: true, run: cmdPromote, flags: func() *flag.FlagSet { return promoteFlags(&promoteOpts{}) }},
		{name: "rollback", aliases: []string{"undo"}, summary: "restore files from a pre-write snapshot", advanced: true, run: cmdRollback, flags: func() *flag.FlagSet { return rollbackFlags(&rollbackOpts{}) }},
		{name: "eject", summary: "capture targets, then remove friday's bookkeeping", advanced: true, run: cmdEject, flags: func() *flag.FlagSet { return ejectFlags(&ejectOpts{}) }},
		{name: "remote", summary: "git ops on ~/.friday (init/pull/push/propose/status)", advanced: true, run: cmdRemote, flags: func() *flag.FlagSet { return proposeFlags(&proposeOpts{}) }, subcommands: []string{"init", "pull", "push", "propose", "status"}},
		{name: "doctor", summary: "health-check the install + lint the store; `doctor <file>` explains a mapping", advanced: true, run: cmdDoctor, flags: func() *flag.FlagSet { var b bool; return doctorFlags(&b) }},
		{name: "completion", summary: "print a shell completion script", advanced: true, run: cmdCompletion, subcommands: []string{"bash", "zsh", "fish"}},
		{name: "version", summary: "print version", advanced: true, run: cmdVersion},
		{name: "help", run: cmdHelp},
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
		// A real terminal opens the interactive control room; anything piped or
		// redirected (CI, a shell pipeline, tests) keeps the plain usage + exit-1
		// path byte-for-byte. Usage stays reachable via `friday help` / `-h`.
		if ui.Interactive() {
			return launchTUI(version)
		}
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
			// `friday <cmd> --help` prints that command's own help instead of
			// running it. help handles its own --all flag, so skip it here.
			if c.name != "help" && wantsHelp(args[1:]) {
				printCommandHelp(c)
				return 0
			}
			return c.run(args[1:])
		}
	}
	fmt.Fprintf(os.Stderr, "friday: unknown command %q\n\n", args[0])
	printUsage()
	return 1
}

// cmdHelp prints the tiered usage: the common commands by default, or the full
// manual (advanced commands, per-command flags, examples) with --all.
func cmdHelp(args []string) int {
	if slices.Contains(args, "--all") || slices.Contains(args, "-a") || slices.Contains(args, "all") {
		printUsageAll()
	} else {
		printUsage()
	}
	return 0
}

// wantsHelp reports whether -h/--help appears before an optional "--"
// terminator, so `friday push --help` shows help rather than running push.
func wantsHelp(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == "-h" || a == "--help" {
			return true
		}
	}
	return false
}

// printCommandHelp renders one command's help from the table plus its flagset.
func printCommandHelp(c command) {
	output.Header("friday " + c.name)
	if c.summary != "" {
		output.Dim("%s", c.summary)
	}
	if len(c.aliases) > 0 {
		output.Dim("aliases: %s", strings.Join(c.aliases, ", "))
	}
	if len(c.subcommands) > 0 {
		output.Dim("subcommands: %s", strings.Join(c.subcommands, ", "))
	}
	if c.flags != nil {
		fs := c.flags()
		fs.SetOutput(os.Stdout)
		fmt.Println("\nflags:")
		fs.PrintDefaults()
	}
	fmt.Println("\nrun `friday help --all` for the full manual")
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
