package cli

import (
	"fmt"
	"strings"
)

// printUsage prints the short, everyday help: the common commands only.
// printUsageAll prints the full manual — advanced commands, per-command flags,
// and examples. Both derive their command listing from commandTable(), so the
// help can never list a command the binary doesn't dispatch.
func printUsage()    { renderUsage(false) }
func printUsageAll() { renderUsage(true) }

func renderUsage(all bool) {
	fmt.Print(usageIntro)
	printCommandList(all)
	fmt.Print(usageGlobalFlags)
	if all {
		fmt.Print(usageFlagDetails)
		fmt.Print(usageExamples)
	} else {
		fmt.Print(usageCommonFooter)
	}
}

func printCommandList(all bool) {
	fmt.Println("Common commands:")
	printTier(false)
	if all {
		fmt.Println("\nAdvanced commands:")
		printTier(true)
	}
}

func printTier(advanced bool) {
	for _, c := range commandTable() {
		if c.summary == "" || c.advanced != advanced {
			continue
		}
		fmt.Printf("  %-16s %s\n", nameColumn(c), c.summary)
	}
}

func nameColumn(c command) string {
	if len(c.aliases) > 0 {
		return c.name + " (" + strings.Join(c.aliases, ",") + ")"
	}
	return c.name
}

const usageIntro = `friday — manage AI agent configs from a single canonical store

  Store:         ~/.friday — your .md files (core, rules, standards, agents, commands, skills)
  Agents:        ~/.claude, ~/.codex, ~/.config/opencode, ~/.copilot, and more
  Distribution:  any git remote — share with your team, your company, your other machines

Usage:
  friday <command> [flags] [args]

`

const usageGlobalFlags = `
Global flags (work with any command):
  --no-color        Disable colored output (also: NO_COLOR or FRIDAY_NO_COLOR env)

`

const usageCommonFooter = `More:
  friday <command> --help    Flags and details for one command
  friday help --all          Every command, all flags, and examples
`

const usageFlagDetails = `init flags:
  --remote URL      Clone URL into ~/.friday (skips the prompt — for scripts)
  --scaffold        Scaffold an empty store without prompting

setup flags:
  --agent NAME      Agent preset to set up (skips the agent prompt)
  --dry-run         Show changes without writing
  --force           Overwrite without prompting on drift
  --no-interactive  Skip prompts (also disables the rich TUI selection)

Common flags (push / pull / sync / status):
  --dry-run         Show changes without writing
  --force           Overwrite without prompting
  --no-interactive  Skip prompts (CI mode)
  --only GLOB       (push) Limit to changes sourced from store files matching GLOB

share / remote propose flags (push a branch + open an MR):
  -m, --message     Commit message (required)
  --branch NAME     Remote branch name (default: friday/propose-<timestamp>)
  --target BRANCH   MR target branch (default: the remote's HEAD branch)

`

const usageExamples = `Examples:
  friday init                                    # interactive: prompt for remote URL
  friday init --scaffold                         # non-interactive: empty scaffold

  friday setup                                   # in a project dir: pick an agent + knowledge
  friday sync                                    # capture edits, then fan them everywhere
  friday status                                  # what would change, no writes

  friday share -m "tweak rules"                  # propose your store changes for team review
  friday promote .claude/skills/new-skill        # project skill → ~/.friday
  friday doctor                                  # diagnose store, manifest, drift
`
