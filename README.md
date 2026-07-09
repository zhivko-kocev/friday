# friday

[![test](https://github.com/zhivko-kocev/friday/actions/workflows/test.yml/badge.svg)](https://github.com/zhivko-kocev/friday/actions/workflows/test.yml)
[![release](https://img.shields.io/github/v/release/zhivko-kocev/friday?sort=semver&display_name=tag)](https://github.com/zhivko-kocev/friday/releases)
[![Go Reference](https://pkg.go.dev/badge/github.com/zhivko-kocev/friday.svg)](https://pkg.go.dev/github.com/zhivko-kocev/friday)
[![Go Report Card](https://goreportcard.com/badge/github.com/zhivko-kocev/friday)](https://goreportcard.com/report/github.com/zhivko-kocev/friday)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Every AI coding agent wants its config in a different folder, in a different format. Claude Code reads `~/.claude/CLAUDE.md`. OpenAI Codex reads `~/.codex/AGENTS.md`. OpenCode reads `~/.config/opencode/AGENTS.md`. GitHub Copilot reads `~/.copilot/copilot-instructions.md`. The same rule, four times in four places, drifts the moment you stop maintaining all of them.

**friday** keeps one canonical store at `~/.friday/` and moves it where you need it. Five commands cover the whole workflow:

- **`friday init`** — create or clone your store
- **`friday sync`** — reconcile the store with every agent on this machine
- **`friday setup`** — drop chosen knowledge into a project
- **`friday share`** — propose store changes to your team
- **`friday status`** — preview what would change

Plain Markdown in, each agent's native format out — versioned by any git repo you point it at. `friday help` shows those five; `friday help --all` shows the full toolbox underneath.

```text
$ friday push
  pushing to installed agents: [claude codex copilot opencode]

changes:
  claude    2 created  (CLAUDE.md, agents/)
  codex     1 created  (AGENTS.md)
  copilot   1 created  (copilot-instructions.md)
  opencode  2 created  (AGENTS.md, rules/)
  summary: 6 created
```

Each adapter folds into one line — a count plus a folder breakdown — instead of
a row per file; files already in sync collapse to a tally, and only conflicts
are named individually. `--diff` appends a windowed hunk view (a few lines of
context around each edit, not the whole file).

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
# 1. Create your store — clone a team/dotfiles repo, or scaffold a fresh one.
friday init                                    # interactive: prompt for remote URL
friday init --scaffold                         # non-interactive: empty store
friday init --from-git git@example.com:me/developer-os.git   # clone an existing store

# 2. Reconcile the store with every agent installed on this machine.
#    sync captures edits you made in an agent dir, then fans the store back out.
friday sync

# 3. Preview first, any time — no writes.
friday status

# 4. In a project: pick an agent + knowledge from ~/.friday, and write it into
#    the project's git-tracked config (.claude/, CLAUDE.md, .github/, ...).
friday setup

# 5. Send store changes to your team for review (opens an MR).
friday share -m "tighten the review rules"
```

Everyday work is those five. Under the hood `friday sync` is `friday pull`
(agent edits → store) then `friday push` (store → agents) — run either alone
for one-way flow, e.g. `friday push claude`. First time trying friday on a
machine that already has a configured agent? `friday pull --discover` walks the
agent dir and seeds the store from it. `--no-interactive` on any of these gives
the CI/scripting form.

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

Every store directory maps into every agent that has a documented place for
it (paths verified against each harness's docs):

| Store dir     | `claude`<br>`~/.claude` | `codex`<br>`~/.codex` | `copilot`<br>`~/.copilot` | `opencode`<br>`~/.config/opencode` | `windsurf`<br>`~/.codeium/windsurf` | `antigravity`<br>`~/.gemini` | `pi`<br>`~/.pi/agent` |
| ------------- | ----------- | ---------- | ------------------ | ----------- | -------------------- | ----------------------- | ---------- |
| core + rules  | `CLAUDE.md` | `AGENTS.md`| `copilot-instructions.md` | `AGENTS.md` + `rules/` | `memories/global_rules.md` | `GEMINI.md` | `AGENTS.md` |
| `agents/`     | `agents/`   | —          | `agents/*.agent.md`| `agents/`†  | —                    | —                       | —          |
| `commands/`   | `commands/` | `prompts/` | —                  | `commands/` | `global_workflows/`  | `antigravity/global_workflows/` | `prompts/` |
| `skills/`     | `skills/`   | `skills/`  | `skills/`          | `skills/`†  | —                    | —                       | `skills/`  |
| `standards/`  | ✓           | ✓          | ✓                  | ✓           | ✓                    | ✓                       | ✓          |
| `connectors/` | ✓           | ✓          | ✓                  | ✓           | ✓                    | ✓                       | ✓          |
| `hooks/`      | `hooks/`    | —          | —                  | —           | —                    | —                       | —          |

† frontmatter adapted to the harness's dialect. `—` means the harness has no
documented surface for that content. `standards/` and `connectors/` have no
native discovery mechanism anywhere, so they land as reference copies in each
agent's config home. Every rule rewrites the Claude-plugin path variable
`${CLAUDE_PLUGIN_ROOT}` to `~/.friday` on push (and back on pull), so
knowledge repos authored as Claude Code plugins — like developer-os — work
unmodified, and cross-references always resolve against the store.

Paths verified against each agent's current documentation (Claude Code, [Codex CLI](https://developers.openai.com/codex/guides/agents-md), [OpenCode](https://opencode.ai/docs/config/), [Copilot CLI](https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-custom-instructions)).

**Cursor** does not currently expose user-level rules through the filesystem; its global rules live inside Cursor's settings UI. The cursor preset was removed in v0.0.4. If Cursor adds filesystem-backed global rules (open feature request in their forum), the preset will return.

To disable an adapter, delete its entry from `friday.yaml`. To customize a target dir or rule, edit it. The presets only seed the manifest at init time; they don't run again.

## Commands

The everyday five:

```
init                Create or clone your ~/.friday store
sync                Reconcile the store with every installed agent
setup               In a project: pick an agent + knowledge, write .claude/ etc.
share               Propose store changes to your team (opens an MR)
status              Two-axis view of every managed file, without writing
```

`friday status` shows a two-column grid — column 1 flags a target you edited
directly (an edit `sync`/`pull` would capture), column 2 flags a pending render
(the store changed and `sync`/`push` would update the agent). Add `--diff` for
the content diff, `--origin` to see where each adapter is defined, or `--check`
for a CI exit code (2 when anything is out of sync).

Underneath (`friday help --all`): `push` / `pull` (one-way sync; `pull --discover`
seeds the store from an existing install), `promote` (project → store), `doctor`
(health check + a best-practice advisor over your store; `doctor <file>` explains
which rule produces a file; `doctor --json` for CI), `remote` (git bridge for the
store), `rollback`/`undo`, `eject`, `completion`.

Run `friday <command> --help` for a command's flags.

## Safety

- **Atomic writes** — every target file is written via a temp file + rename, so a Ctrl-C mid-write leaves the previous version intact.
- **Drift detection** — friday tracks SHA256 of every file it writes and refuses to clobber edits you've made directly in an agent's dir. Resolve interactively or with `--force`.
- **Pull captures edits; `--discover` captures new files** — a plain `friday pull` only updates files the store already knows about. A file *created* directly in an agent dir (e.g. a new skill authored in `~/.claude/skills/`) is caught by `friday pull --discover`, which walks the agent dir and enriches the store.
- **CRLF tolerance** — Windows checkouts don't get flagged as drift.
- **No secrets** — the scaffolded `.gitignore` filters `.env`, `*.key`, `*.pem`, and Claude Code's runtime state dirs.

## Contributing

Issues and PRs welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) for build/test instructions and the [ROADMAP](ROADMAP.md) for where we're headed.

## License

MIT — see [LICENSE](LICENSE).
