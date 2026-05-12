package cli

import "fmt"

func printUsage() {
	fmt.Print(`friday — manage AI agent configs from a single canonical store

  Store:         ~/.friday — your .md files (identity, rules, agents, commands, skills)
  Agents:        ~/.claude, ~/.cursor, ~/.config/opencode, ~/.github (Claude Code, Cursor, OpenCode, Copilot)
  Distribution:  any git remote — share with your team, your company, your other machines

Usage:
  friday <command> [flags] [args]

Commands:
  init [flags]               Prompt for a remote URL; clone it into ~/.friday, or scaffold an empty store on blank input
  list [adapters]            Show every adapter in friday.yaml + whether it's installed on this machine (alias: ls)
  push [adapters...]         Compile ~/.friday into installed agents' dirs (no args = every installed agent)
  pull [adapters...]         Capture edits from agent dirs back into ~/.friday (no args = per-agent prompt + diff)
  status [adapters...]       Show user-level diff (no writes)
  doctor                     Run a health check on the local install (store, manifest, drift)
  remote pull|push|status    git pull / commit+push / status on ~/.friday
  version                    Print version

Global flags (work with any command):
  --no-color        Disable colored output (also: NO_COLOR or FRIDAY_NO_COLOR env)

init flags:
  --remote URL      Clone URL into ~/.friday (skips the prompt — for scripts)
  --scaffold        Scaffold an empty store without prompting

Common flags (push / pull):
  --dry-run         Show changes without writing
  --force           Overwrite without prompting
  --no-interactive  Skip prompts (CI mode); push proceeds without confirmation, pull falls back to legacy batch flow

Remote push flags:
  -m, --message     Commit message (required)

Examples:
  friday init                                    # interactive: prompt for remote URL
  friday init --scaffold                         # non-interactive: empty scaffold
  friday init --remote https://github.com/me/dotai   # non-interactive: clone

  friday push                                    # push to every installed agent
  friday push claude                             # push only to claude (creates target dir if missing)

  friday pull                                    # walk each installed agent: show diff, ask apply / skip / quit
  friday pull cursor                             # legacy: pull cursor only, file-level conflicts

  friday doctor                                  # diagnose store, manifest, drift in one read-only pass
  friday remote push -m "tweak rules"            # commit & push ~/.friday to its git remote
`)
}
