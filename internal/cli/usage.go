package cli

import "fmt"

func printUsage() {
	fmt.Print(`friday — manage AI-agent config across user and project scopes

Usage:
  friday <command> [flags] [args]

Commands:
  init                       Scaffold (or clone) the user store at $UserConfigDir/friday
  add <preset>               Add (or override) a preset adapter in the user store's friday.yaml
  remove <adapter>           Remove an adapter entry from friday.yaml (alias: rm)
  list [presets|adapters]    Show available presets and configured adapters (alias: ls)
  push [adapters...]         Apply user store rules to ~/.claude, ~/.cursor, ...
  push --from-git URL        Transient: clone repo, apply rules to ./.claude, ..., discard
  pull [adapters...]         Read user-level targets back into the user store
  status [adapters...]       Show user-level diff (no writes)
  remote pull                git pull in the user store
  remote push -m MSG         git add+commit+push in the user store
  remote status              git status in the user store
  version                    Print version

Global flags (work with any command):
  --no-color        Disable colored output (also: NO_COLOR or FRIDAY_NO_COLOR env)

Common flags (push / pull):
  --dry-run         Show changes without writing
  --force           Overwrite without prompting on drift
  --no-interactive  Skip conflict prompts (CI mode); conflicts become skip
  --diff            Print line diff for each change

Init flags:
  --from-git URL    Clone the user store from a git repo
  --remote URL      After scaffold, register this URL as the origin remote
  --no-git          Skip the git init step on scaffold
  --adapters list   Comma-separated preset list (claude,cursor,opencode,copilot)
  --force           Overwrite an existing user store (refuses if a .git/ is present)
  --really-force    Allow --force to wipe a store that contains a .git/ dir

Add flags:
  --target dir      Override the preset's default target directory
  --force           Replace an existing adapter entry
  --list            List available presets and exit

Remote push flags:
  -m, --message     Commit message (required)

Examples:
  friday init                                   # empty user store
  friday init --adapters claude,cursor          # user store with two adapters
  friday init --from-git https://...            # clone existing config
  friday add opencode                           # add a preset later
  friday remove opencode                        # take it back out
  friday list adapters                          # what's configured today
  friday push                                          # apply user store to ~/.claude etc.
  friday push --from-git https://github.com/me/cfg     # apply repo content to ./.claude etc.
  friday push --from-git ../local-config-repo          # local path also works
  friday pull cursor                            # capture ~/.cursor edits back
  friday remote push -m "tweak rules"           # commit & push the user store
`)
}
