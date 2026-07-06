package cli

import "fmt"

func printUsage() {
	fmt.Print(`friday — manage AI agent configs from a single canonical store

  Store:         ~/.friday — your .md files (core, rules, standards, agents, commands, skills)
  Agents:        ~/.claude, ~/.codex, ~/.config/opencode, ~/.copilot (Claude Code, OpenAI Codex, OpenCode, GitHub Copilot)
  Distribution:  any git remote — share with your team, your company, your other machines

Usage:
  friday <command> [flags] [args]

Commands:
  init [flags]               Prompt for a remote URL; clone it into ~/.friday, or scaffold an empty store on blank input
  setup [flags]              Interactively apply selected store knowledge into the current project's agent config
  promote [paths...]         Capture project agent config (e.g. a hand-added skill) up into ~/.friday; --propose opens an MR
  list [adapters]            Show every adapter in friday.yaml + whether it's installed on this machine (alias: ls)
  push [adapters...]         Compile ~/.friday into installed agents' dirs (no args = every installed agent)
  pull [adapters...]         Capture edits from agent dirs back into ~/.friday (no args = per-agent prompt + diff)
  sync [adapters...]         Pull then push in one go — capture edits first, then fan them out everywhere
  status [adapters...]       Show user-level diff (no writes); --json for machine-readable output
  explain <target-file>      Show which adapter + rule produces a target file, and from which sources
  import <adapter|dir>       Capture an existing agent installation into ~/.friday (reverse of push)
  compile --from X --to Y    Convert one agent's installed config into another's format (no ~/.friday round trip)
  rollback [--list] [<id>]   Restore the file state recorded before a write (snapshots taken by push/pull/sync/setup/promote/import/compile)
  plugin list|validate       Manage out-of-tree presets in ~/.friday/plugins/*.yaml
  lint                       Static store checks: frontmatter, oversized files, broken refs, dest collisions
  eject [--yes]              Capture targets into the store, then remove friday.yaml + caches (clean exit)
  doctor                     Run a health check on the local install (store, manifest, drift)
  completion bash|zsh|fish   Print a shell completion script (e.g. eval "$(friday completion bash)")
  remote init|pull|push|propose|status   set origin / git pull / commit+push / MR branch / status on ~/.friday
  version                    Print version

Global flags (work with any command):
  --no-color        Disable colored output (also: NO_COLOR or FRIDAY_NO_COLOR env)

init flags:
  --remote URL      Clone URL into ~/.friday (skips the prompt — for scripts)
  --scaffold        Scaffold an empty store without prompting

setup flags:
  --agent NAME      Agent preset to set up (skips the agent prompt)
  --dry-run         Show changes without writing
  --force           Overwrite without prompting on drift

Common flags (push / pull):
  --dry-run         Show changes without writing
  --force           Overwrite without prompting
  --no-interactive  Skip prompts (CI mode); push proceeds without confirmation, pull falls back to legacy batch flow
  --only GLOB       (push) Limit to changes sourced from store files matching GLOB

Remote push flags:
  -m, --message     Commit message (required)

Remote propose flags (push to a new branch + open an MR instead of pushing directly):
  -m, --message     Commit message (required)
  --branch NAME     Remote branch name (default: friday/propose-<timestamp>)
  --target BRANCH   MR target branch (default: the remote's HEAD branch)

Examples:
  friday init                                    # interactive: prompt for remote URL
  friday init --scaffold                         # non-interactive: empty scaffold
  friday init --from-git git@example.com:me/developer-os.git   # non-interactive: clone (--remote works too)

  friday push                                    # push to every installed agent
  friday push claude                             # push only to claude (creates target dir if missing)

  friday pull                                    # walk each installed agent: show diff, ask apply / skip / quit
  friday pull claude                             # same flow, restricted to claude

  friday setup                                   # in a project dir: pick an agent + store items, write .claude/ etc.
  friday promote .claude/skills/new-skill --propose -m "add new-skill"   # project skill → ~/.friday → team MR
  friday doctor                                  # diagnose store, manifest, drift in one read-only pass
  friday remote push -m "tweak rules"            # commit & push ~/.friday to its git remote
  friday remote propose -m "tweak rules"         # team stores: push a branch + open an MR, local store untouched
`)
}
