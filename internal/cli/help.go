package cli

import (
	"context"
	"fmt"
	"strings"

	ucli "github.com/urfave/cli/v3"
)

// Custom two-tier help. Porcelain commands always show; advanced commands
// show only under `--all`. This intentionally does not reuse urfave's own
// help templates — Phase 10 grants explicit latitude here ("the only
// intended user-visible deltas are help layout and completion scripts"),
// and a hand-written renderer keeps the exact porcelain/advanced split this
// tool has always presented.
//
// completionAdvancedEntry is synthesized rather than read from root.Commands:
// EnableShellCompletion injects urfave's own "completion" command with an
// empty Category (and Hidden=true) at dispatch time, so it never appears in
// the introspected list below. It is still a real, documented advanced
// command, so it gets one static line here.
var completionAdvancedEntry = struct{ name, usage string }{completionCommandName, "print a shell completion script"}

const introText = `friday — manage AI agent configs from a single canonical store

  Store:         ~/.friday — your knowledge base (core, rules, agents, commands, skills)
  Agents:        ~/.claude, ~/.codex, ~/.config/opencode, ~/.copilot, and more
  Distribution:  any git remote — share with your team, your company, your other machines

Usage:
  friday <command> [flags] [args]

`

const globalFlagsText = `
Global flags (work with any command):
  --no-color        Disable colored output (also: NO_COLOR or FRIDAY_NO_COLOR env;
                     FORCE_COLOR forces color even when not writing to a terminal)
  --quiet           Suppress all non-error output (exit codes unchanged)
  --verbose         Show per-file decisions and the resolved source for each rendered path

`

const shortFooterText = `More:
  friday <command> --help    Flags and details for one command
  friday help --all          Every command, all flags, and examples
`

const examplesText = `Examples:
  friday init                                    # interactive: prompt for remote URL
  friday init --scaffold                         # non-interactive: empty scaffold

  friday setup                                   # in a project dir: pick an agent + knowledge
  friday sync                                    # capture edits, then fan them everywhere
  friday status                                  # what would change, no writes

  friday share -m "tweak rules"                  # propose your store changes for team review
  friday promote .claude/skills/new-skill        # project skill → ~/.friday
  friday doctor                                  # diagnose store, manifest, drift
`

// renderUsage prints the intro banner, the command listing (porcelain only,
// or both tiers with all=true), and a footer. root is the fully constructed
// command graph so the listing can never drift from what actually dispatches.
func renderUsage(root *ucli.Command, all bool) {
	fmt.Print(introText)
	printCommandList(root, all)
	if all {
		printFlagDetails(root)
	}
	fmt.Print(globalFlagsText)
	if all {
		fmt.Print(examplesText)
	} else {
		fmt.Print(shortFooterText)
	}
}

func printCommandList(root *ucli.Command, all bool) {
	fmt.Println("Common commands:")
	printTier(root, "porcelain")
	if all {
		fmt.Println("\nAdvanced commands:")
		printTier(root, "advanced")
		fmt.Printf("  %-16s %s\n", completionAdvancedEntry.name, completionAdvancedEntry.usage)
	}
}

func printTier(root *ucli.Command, category string) {
	for _, c := range root.Commands {
		if c.Category != category || c.Hidden {
			continue
		}
		fmt.Printf("  %-16s %s\n", nameColumn(c), c.Usage)
	}
}

// printFlagDetails is the `--all` flag section the three "all flags"
// promises above advertise. It is derived from the live command graph
// rather than hand-maintained, so it lists exactly the flags that parse.
func printFlagDetails(root *ucli.Command) {
	fmt.Println("\nCommand flags:")
	for _, c := range root.Commands {
		if c.Hidden || c.Category == "" {
			continue
		}
		printCommandFlags(c, c.Name)
	}
}

// printCommandFlags prints one command's own flags, then recurses into its
// subcommands so `remote propose --branch` and friends are reachable too.
// A flagless command contributes nothing — this section is about flags.
func printCommandFlags(c *ucli.Command, path string) {
	if flags := visibleFlags(c); len(flags) > 0 {
		fmt.Printf("\n  friday %s\n", path)
		for _, f := range flags {
			fmt.Printf("    %-24s %s\n", flagColumn(f), flagUsage(f))
		}
	}
	for _, sub := range c.Commands {
		if !sub.Hidden {
			printCommandFlags(sub, path+" "+sub.Name)
		}
	}
}

func visibleFlags(c *ucli.Command) []ucli.Flag {
	var out []ucli.Flag
	for _, f := range c.Flags {
		if vf, ok := f.(ucli.VisibleFlag); ok && !vf.IsVisible() {
			continue
		}
		out = append(out, f)
	}
	return out
}

// flagColumn renders "-m, --message" from a flag's names: one dash for a
// single-rune shorthand, two for a word, matching how they must be typed.
func flagColumn(f ucli.Flag) string {
	names := f.Names()
	spelled := make([]string, 0, len(names))
	for _, n := range names {
		if len([]rune(n)) == 1 {
			spelled = append(spelled, "-"+n)
		} else {
			spelled = append(spelled, "--"+n)
		}
	}
	return strings.Join(spelled, ", ")
}

func flagUsage(f ucli.Flag) string {
	if d, ok := f.(ucli.DocGenerationFlag); ok {
		return d.GetUsage()
	}
	return ""
}

func nameColumn(c *ucli.Command) string {
	if len(c.Aliases) > 0 {
		return c.Name + " (" + strings.Join(c.Aliases, ",") + ")"
	}
	return c.Name
}

// newHelpCommand is friday's own "help" — registered in place of urfave's
// built-in one (root sets HideHelpCommand) so `--all` can select the full
// manual. Per-command `--help`/`-h` still goes through urfave's own
// generated flag help (see the ShowSubcommandHelp override in cli.go), which
// is guaranteed accurate since it reads the command's real Flags.
func newHelpCommand() *ucli.Command {
	return &ucli.Command{
		Name:  "help",
		Usage: "show this help (or `friday help --all` for the full manual)",
		Flags: []ucli.Flag{
			&ucli.BoolFlag{Name: "all", Aliases: []string{"a"}, Usage: "show advanced commands, flag details, and examples too"},
		},
		Action: func(_ context.Context, cmd *ucli.Command) error {
			all := cmd.Bool("all")
			for _, a := range cmd.Args().Slice() {
				if a == "all" {
					all = true
				}
			}
			renderUsage(cmd.Root(), all)
			return nil
		},
	}
}
