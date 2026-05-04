# friday

Manage AI-agent config (Claude Code, Cursor, OpenCode, Copilot) from one canonical store. One source of truth for `identity.md`, `rules/`, `agents/`, `commands/`, `skills/` — push it out to every agent's home dir, pull edits back.

## Install

```bash
# from source
go install github.com/zhivko-kocev/friday/cmd/friday@latest

# or build locally
git clone https://github.com/zhivko-kocev/friday
cd friday
make build      # writes ./friday (Unix) or ./friday.exe (Windows)
```

Pre-built binaries: see the [releases page](https://github.com/zhivko-kocev/friday/releases).

## Quick start

```bash
# scaffold an empty user store
friday init

# or clone an existing config repo as your user store
friday init --from-git https://github.com/zhivko-kocev/dotai

# add a preset adapter
friday add claude

# push to every configured adapter (~/.claude, ~/.cursor, ...)
friday push

# capture edits made directly in ~/.claude back into the store
friday pull claude

# diff without writing
friday status

# commit + push the user store to its remote
friday remote push -m "tweak rules"
```

## Layout for a config store

See [the dotai standard layout](https://github.com/zhivko-kocev/dotai) for the canonical structure.

```
identity.md          Concatenated into CLAUDE.md / AGENTS.md / copilot-instructions.md
rules/*.md           Per-topic rules. Concat for Claude/Copilot, split for Cursor/OpenCode
agents/*.md          Claude subagent definitions
commands/*.md        Claude slash commands
skills/<name>/       Agent skills (Claude + OpenCode)
```

## Built-in presets

| Preset    | Target dir                      | What it writes                                                             |
| --------- | ------------------------------- | -------------------------------------------------------------------------- |
| `claude`  | `~/.claude/`                    | `CLAUDE.md` (concat), `agents/`, `commands/`, `skills/`                    |
| `cursor`  | `~/.cursor/`                    | `rules/_identity.md`, `rules/{filename}`                                   |
| `opencode`| `~/.config/opencode/`           | `AGENTS.md` (identity), `rules/{filename}`, `skills/` (frontmatter stripped)|
| `copilot` | `~/.github/`                    | `copilot-instructions.md` (concat)                                         |

## Commands

```
init                Scaffold or clone the user store
add <preset>        Append a preset adapter to friday.yaml
remove <adapter>    Remove an adapter
list                Show configured adapters and available presets
push [adapters...]  Apply user store → agent dirs
pull [adapters...]  Capture edits in agent dirs → user store
status              Diff store vs targets (no writes)
remote pull|push|status   git operations on the user store
```

Run `friday help` for full flags.

## License

MIT — see [LICENSE](LICENSE).
