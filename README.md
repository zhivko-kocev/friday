# friday

[![test](https://github.com/zhivko-kocev/friday/actions/workflows/test.yml/badge.svg)](https://github.com/zhivko-kocev/friday/actions/workflows/test.yml)
[![release](https://img.shields.io/github/v/release/zhivko-kocev/friday?sort=semver&display_name=tag)](https://github.com/zhivko-kocev/friday/releases)
[![Go Reference](https://pkg.go.dev/badge/github.com/zhivko-kocev/friday.svg)](https://pkg.go.dev/github.com/zhivko-kocev/friday)
[![Go Report Card](https://goreportcard.com/badge/github.com/zhivko-kocev/friday)](https://goreportcard.com/report/github.com/zhivko-kocev/friday)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Every AI coding agent wants its config in a different folder, in a different format. Claude Code reads `~/.claude/CLAUDE.md`. OpenAI Codex reads `~/.codex/AGENTS.md`. OpenCode reads `~/.config/opencode/AGENTS.md`. GitHub Copilot reads `~/.copilot/copilot-instructions.md`. The same rule, four times in four places, drifts the moment you stop maintaining all of them.

**friday** is a Go CLI that keeps one canonical store at `~/.friday/` and writes it out to every agent in the format that agent expects. Edit a target directly and `friday pull` brings the change back. Optionally back the store with a git repo to version your rules like dotfiles and sync across machines or with a team.

```text
$ friday push
  pushing to installed agents: [claude codex copilot opencode]

  adapter: claude
    create   core.md+rules/general.md      CLAUDE.md
    create   agents/researcher.md          agents/researcher.md
  adapter: codex
    create   core.md+rules/general.md      AGENTS.md
  adapter: copilot
    create   core.md+rules/general.md      copilot-instructions.md
  adapter: opencode
    create   core.md                       AGENTS.md
    create   rules/general.md              rules/general.md

  summary:
    adapters: claude, codex, copilot, opencode
    claude    2 created, 0 updated, 0 in-sync
    codex     1 created, 0 updated, 0 in-sync
    copilot   1 created, 0 updated, 0 in-sync
    opencode  2 created, 0 updated, 0 in-sync
    total     6 created, 0 updated, 0 in-sync
```

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
friday init --from-git git@example.com:me/developer-os.git   # non-interactive: clone (--remote is an alias)

# Push to every agent that's installed on this machine
friday push

# Push only to one agent (creates target dir on first run)
friday push claude

# Walk each installed agent: show diff, ask apply / skip / quit
friday pull

# Pull only one agent (legacy file-by-file conflict flow)
friday pull claude

# Diff without writing
friday status

# In a project directory: pick an agent, pick knowledge from ~/.friday,
# and write it into the project's git-tracked config (.claude/, CLAUDE.md, ...)
friday setup

# Read-only health check on the local install
friday doctor

# Commit + push ~/.friday to its git remote
friday remote push -m "tweak rules"
```

Non-interactive forms for CI / scripting also exist for `push`/`pull` via `--no-interactive`.

## The `~/.friday` layout

Any repo with this shape works — including knowledge repos authored as Claude
Code plugins (a `developer-os`-style repo with `core/core.md`, `skills/`,
`agents/`, `standards/`, `hooks/`): clone it with `friday init --from-git URL`
and push. No `friday.yaml` needed; friday falls back to the built-in presets.

```
core.md              The entry file — leads CLAUDE.md / AGENTS.md / copilot-instructions.md.
                     Also matched at core/core.md; legacy identity.md still works.
rules/*.md           Per-topic rules. Concat for Claude/Codex/Copilot, split for OpenCode
agents/*.md          Claude subagent definitions
commands/*.md        Claude slash commands
skills/<name>/       Agent skills (Claude + OpenCode)
standards/*.md       Per-language baselines — stay in the store, reached by reference
hooks/**             Hook config + scripts — stay in the store (see `friday doctor`)
friday.yaml          Adapter manifest. Auto-seeded by `friday init` with all four presets.
```

## Built-in presets

| Preset     | Target dir            | Output                                                                    |
| ---------- | --------------------- | ------------------------------------------------------------------------- |
| `claude`   | `~/.claude/`          | `CLAUDE.md` (concat), `agents/`, `commands/`, `skills/`                   |
| `codex`    | `~/.codex/`           | `AGENTS.md` (concat)                                                      |
| `opencode` | `~/.config/opencode/` | `AGENTS.md` (core), `rules/{filename}`, `skills/` (frontmatter stripped)  |
| `copilot`  | `~/.copilot/`         | `copilot-instructions.md` (concat)                                        |
| `windsurf` | `~/.codeium/windsurf/memories/` | `global_rules.md` (concat, 6k-char cap; Windsurf is Devin Desktop by Cognition now — same paths) |
| `antigravity` | `~/.gemini/`       | `GEMINI.md` (concat)                                                      |
| `pi`       | `~/.pi/agent/`        | `AGENTS.md` (concat), `skills/` (Agent Skills standard)                   |

Presets copy what each agent *discovers* on disk. Everything else in the store
(`core/`, `standards/`, `hooks/`, ...) stays in `~/.friday` and is reached by
reference: every rule rewrites the Claude-plugin path variable
`${CLAUDE_PLUGIN_ROOT}` to `~/.friday` on push (and back on pull), so knowledge
repos authored as Claude Code plugins — like developer-os — work unmodified.

Paths verified against each agent's current documentation (Claude Code, [Codex CLI](https://developers.openai.com/codex/guides/agents-md), [OpenCode](https://opencode.ai/docs/config/), [Copilot CLI](https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-custom-instructions)).

**Cursor** does not currently expose user-level rules through the filesystem; its global rules live inside Cursor's settings UI. The cursor preset was removed in v0.0.4. If Cursor adds filesystem-backed global rules (open feature request in their forum), the preset will return.

To disable an adapter, delete its entry from `friday.yaml`. To customize a target dir or rule, edit it. The presets only seed the manifest at init time; they don't run again.

## Commands

```
init [flags]               Prompt for a remote URL; clone or scaffold ~/.friday
setup [flags]              In a project dir: pick an agent + store items, write .claude/ etc.
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
- **Pull captures edits, not new files** — a file *created* directly in an agent dir (e.g. a new skill authored in `~/.claude/skills/`) is invisible to `pull`; move it into `~/.friday` yourself. (`friday import` is on the roadmap to close this.)
- **CRLF tolerance** — Windows checkouts don't get flagged as drift.
- **No secrets** — the scaffolded `.gitignore` filters `.env`, `*.key`, `*.pem`, and Claude Code's runtime state dirs.

## Contributing

Issues and PRs welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) for build/test instructions and the [ROADMAP](ROADMAP.md) for where we're headed.

## License

MIT — see [LICENSE](LICENSE).
