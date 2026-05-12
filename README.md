# friday

[![test](https://github.com/zhivko-kocev/friday/actions/workflows/test.yml/badge.svg)](https://github.com/zhivko-kocev/friday/actions/workflows/test.yml)
[![release](https://img.shields.io/github/v/release/zhivko-kocev/friday?sort=semver&display_name=tag)](https://github.com/zhivko-kocev/friday/releases)
[![Go Reference](https://pkg.go.dev/badge/github.com/zhivko-kocev/friday.svg)](https://pkg.go.dev/github.com/zhivko-kocev/friday)
[![Go Report Card](https://goreportcard.com/badge/github.com/zhivko-kocev/friday)](https://goreportcard.com/report/github.com/zhivko-kocev/friday)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

One CLI to manage AI agent configs (Claude Code, Cursor, OpenCode, GitHub Copilot) from a single canonical store. Push to every agent, pull edits back, sync across machines via git.

- **Store**: `~/.friday` — your `.md` files (`identity`, `rules/`, `agents/`, `commands/`, `skills/`)
- **Agents**: `~/.claude`, `~/.cursor`, `~/.config/opencode`, `~/.github` (path conventions; configurable per adapter)
- **Distribution**: any git remote — share with your team, your company, your other machines

Write your rules once. `friday push` writes them into each agent's expected layout. `friday pull` captures edits you made directly in an agent's dir. `friday remote push` ships the whole store via git so teammates run `friday init --remote <url>` and get the same setup instantly.

## Install

**Linux / macOS** — one-liner installs the latest release into `/usr/local/bin`:

```bash
curl -fsSL https://raw.githubusercontent.com/zhivko-kocev/friday/master/install.sh | bash
```

Override the install dir with `FRIDAY_INSTALL_DIR=$HOME/bin`.

**Windows** (PowerShell) — installs into `%LOCALAPPDATA%\Programs\friday\`:

```powershell
iwr -useb https://raw.githubusercontent.com/zhivko-kocev/friday/master/install.ps1 | iex
```

Override with `$env:FRIDAY_INSTALL_DIR` before piping.

**With the Go toolchain**:

```bash
go install github.com/zhivko-kocev/friday/cmd/friday@latest
```

**From source**:

```bash
git clone https://github.com/zhivko-kocev/friday
cd friday
make build      # produces ./friday (Unix) or ./friday.exe (Windows)
```

Pre-built binaries: see the [releases page](https://github.com/zhivko-kocev/friday/releases).

## Quick start

```bash
# Initialize ~/.friday.
friday init                                    # interactive: prompt for remote URL
friday init --scaffold                         # non-interactive: empty store
friday init --remote https://github.com/me/dotai   # non-interactive: clone

# Push to every agent that's installed on this machine
friday push

# Push only to one agent (creates target dir on first run)
friday push claude

# Walk each installed agent: show diff, ask apply / skip / quit
friday pull

# Pull only one agent (legacy file-by-file conflict flow)
friday pull cursor

# Diff without writing
friday status

# Read-only health check on the local install
friday doctor

# Commit + push ~/.friday to its git remote
friday remote push -m "tweak rules"
```

Non-interactive forms for CI / scripting also exist for `push`/`pull` via `--no-interactive`.

## The `~/.friday` layout

See the [dotai reference repo](https://github.com/zhivko-kocev/dotai) for a working example.

```
identity.md          Concatenated into CLAUDE.md / AGENTS.md / copilot-instructions.md
rules/*.md           Per-topic rules. Concat for Claude/Copilot, split for Cursor/OpenCode
agents/*.md          Claude subagent definitions
commands/*.md        Claude slash commands
skills/<name>/       Agent skills (Claude + OpenCode)
friday.yaml          Adapter manifest. Auto-seeded by `friday init` with all four presets.
```

## Built-in presets

| Preset     | Target dir            | Output                                                                       |
| ---------- | --------------------- | ---------------------------------------------------------------------------- |
| `claude`   | `~/.claude/`          | `CLAUDE.md` (concat), `agents/`, `commands/`, `skills/`                      |
| `cursor`   | `~/.cursor/`          | `rules/_identity.md`, `rules/{filename}`                                     |
| `opencode` | `~/.config/opencode/` | `AGENTS.md` (identity), `rules/{filename}`, `skills/` (frontmatter stripped) |
| `copilot`  | `~/.github/`          | `copilot-instructions.md` (concat)                                           |

To disable an adapter, delete its entry from `friday.yaml`. To customize a target dir or rule, edit it. The presets only seed the manifest at init time — they don't run again.

## Commands

```
init [flags]               Prompt for a remote URL; clone or scaffold ~/.friday
list                       Adapters in friday.yaml + whether each is installed
push [adapters...]         Write ~/.friday into each installed agent's dir
pull [adapters...]         No args = per-agent diff + apply prompt; with args = legacy batch
status [adapters...]       Show diff without writing
doctor                     Read-only health check (store, manifest, drift)
remote pull|push|status    git operations on ~/.friday
```

Run `friday help` for full flags.

## Safety

- **Atomic writes** — every target file is written via a temp file + rename, so a Ctrl-C mid-write leaves the previous version intact.
- **Drift detection** — friday tracks SHA256 of every file it writes and refuses to clobber edits you've made directly in an agent's dir. Resolve interactively or with `--force`.
- **CRLF tolerance** — Windows checkouts don't get flagged as drift.
- **No secrets** — the scaffolded `.gitignore` filters `.env`, `*.key`, `*.pem`, and Claude Code's runtime state dirs.

## Contributing

Issues and PRs welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) for build/test instructions and the [ROADMAP](ROADMAP.md) for where we're headed.

## License

MIT — see [LICENSE](LICENSE).
