# friday

[![test](https://github.com/zhivko-kocev/friday/actions/workflows/test.yml/badge.svg)](https://github.com/zhivko-kocev/friday/actions/workflows/test.yml)
[![release](https://img.shields.io/github/v/release/zhivko-kocev/friday?sort=semver&display_name=tag)](https://github.com/zhivko-kocev/friday/releases)
[![Go Reference](https://pkg.go.dev/badge/github.com/zhivko-kocev/friday.svg)](https://pkg.go.dev/github.com/zhivko-kocev/friday)
[![Go Report Card](https://goreportcard.com/badge/github.com/zhivko-kocev/friday)](https://goreportcard.com/report/github.com/zhivko-kocev/friday)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Every AI coding agent wants its config in a different folder, in a different format. Claude Code reads `~/.claude/CLAUDE.md`. OpenAI Codex reads `~/.codex/AGENTS.md`. OpenCode reads `~/.config/opencode/AGENTS.md`. GitHub Copilot reads `~/.copilot/copilot-instructions.md`. The same rule, four times in four places, drifts the moment you stop maintaining all of them.

**friday** keeps one canonical store at `~/.friday/` and moves it where you need it. Seven commands cover the everyday workflow:

- **`friday init`** — create or clone your store
- **`friday claim`** — adopt the config an agent already has on this machine
- **`friday setup`** — drop chosen knowledge into a project
- **`friday sync`** — reconcile the store with every agent on this machine
- **`friday status`** — preview what would change
- **`friday sessions`** — list coding-agent sessions across every installed agent
- **`friday share`** — propose store changes to your team

Plain Markdown and canonical YAML in, each agent's native format out — versioned by any git repo you point it at. `friday help` shows those seven; `friday help --all` shows the full toolbox underneath.

Bare `friday` prints usage plus the one concrete next step for wherever this
install stands — `friday init`/`friday claim` when there is no store,
`friday sync` when an installed agent has pending changes, `friday status`
then `friday sync` or `friday push --force` when it has drift — and exits 1.
Every command is one-shot and plain-text; prompts appear on stdin only where
a write needs your consent, and `--no-interactive` removes them entirely.

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

Everyday work is those seven. Under the hood `friday sync` is `friday pull`
(agent edits → store) then `friday push` (store → agents) — run either alone
for one-way flow, e.g. `friday push claude`. First time trying friday on a
machine that already has a configured agent? `friday claim` scaffolds the store
if needed and captures what is already installed; `friday pull --discover` is
the same capture against an existing store. `--no-interactive` on any of these
gives the CI/scripting form.

## The `~/.friday` layout

Any repo with this shape works — including knowledge repos authored as Claude
Code plugins (a `developer-os`-style repo with `core/core.md`, `skills/`,
`agents/`, `standards/`, `hooks/`): clone it with `friday init --from-git URL`
and push. No `friday.yaml` needed; friday falls back to the built-in presets.

```
core.md              The entry file — leads CLAUDE.md / AGENTS.md / copilot-instructions.md.
                     Also matched at core/core.md; legacy identity.md still works.
rules/*.md           Per-topic rules, concatenated after core.md into each agent's instruction file
agents/*.md          Subagent definitions (frontmatter adapted per dialect; Codex gets TOML)
commands/*.md        Slash commands / prompts (Gemini gets TOML)
skills/<name>/       Agent skills — every adapter gets them (see the matrix for where)
mcp.yaml             Canonical MCP server list, rendered into each agent's own MCP config
permissions.yaml     Canonical allow/ask/deny policy, rendered into each agent's own policy shape
defaults.yaml        Canonical model / effort / theme / statusline / telemetry opt-out
native/<adapter>/**  Verbatim mirror into that adapter's config dir — the escape hatch
settings/<adapter>.{json,toml}
                     Extra keys/tables merged into that adapter's own settings file
hooks/**             Per-agent hook sources, merged or copied confirm-first; scripts run from the store
friday.yaml          Adapter manifest, plus `sources:`. Auto-seeded by `friday init` with all built-in presets.
.sources/<name>/     Checkouts of the git remotes listed under `sources:` — managed by `friday source`
```

## Built-in presets

Every store directory maps into the place each agent documents for it (paths
verified against each harness's docs). Cells are relative to the column's
target dir:

| Store dir     | `claude`<br>`~/.claude` | `codex`<br>`~/.codex` | `copilot`<br>`~/.copilot` | `opencode`<br>`~/.config/opencode` | `gemini`<br>`~/.gemini` | `oh-my-pi`<br>`~/.omp/agent` |
| ------------- | ----------- | ---------- | ------------------ | ----------- | ----------------------- | ---------- |
| core + rules  | `CLAUDE.md` | `AGENTS.md`| `copilot-instructions.md` | `AGENTS.md` | `GEMINI.md` | `AGENTS.md` |
| `agents/`     | `agents/`   | `agents/*.toml`† | `agents/*.agent.md`†| `agents/`† | `agents/`†     | `agents/`  |
| `commands/`   | `commands/` | `prompts/` | —                  | `commands/` | `commands/*.toml`† | `commands/` |
| `skills/`     | `skills/`   | `~/.agents/skills/` | `~/.agents/skills/` | `~/.agents/skills/` | `~/.agents/skills/` | `skills/` |
| `mcp.yaml`    | `../.claude.json` | `config.toml` | `mcp-config.json` | `opencode.json` | `settings.json` | `mcp.json` |
| `permissions.yaml` | `settings.json` | `config.toml` | `settings.json` | `opencode.json` | `policies/friday.toml` | — |
| `defaults.yaml` | `settings.json` | `config.toml` | `settings.json` | `opencode.json` + `tui.json` | `settings.json` | — |
| `native/<a>/**/*` | verbatim | verbatim | verbatim | verbatim | verbatim | verbatim |
| `settings/<a>.{json,toml}` | `settings.json` | `config.toml` | `settings.json` | `opencode.json` | `settings.json` | — |
| `hooks/`      | `settings.json`‡ | `hooks.json`§ | `hooks/friday-git-guard.json`§ | `plugins/git-guard.ts`§ | `settings.json`§ | `extensions/git-guard.ts`§ |

† Frontmatter is adapted to the harness's dialect — Codex subagents and Gemini
commands are rendered as TOML, and Copilot/OpenCode/Gemini drop the fields
their dialect has no equivalent for.

Three cells don't read literally. `skills/` is the one surface four agents
share: Codex, Copilot, Gemini, and OpenCode all discover
Agent-Skills-standard skills under `~/.agents/skills/`, so friday writes that
tree once — through a shared `agents` adapter, counted as installed when any
of the four is — instead of four separate copies. Claude and oh-my-pi read
their own `skills/` dir at a higher discovery priority and keep a rule of
their own. Claude's user-scope MCP config lives in `~/.claude.json`, not
inside `~/.claude/`, which is why that cell resolves `..`. And
`native/<adapter>/**/*` lands verbatim in the column's target dir: it is a
deliberate escape hatch, so every file in it is confirm-gated on write —
a mirrored `settings.json` or `plugins/*.ts` can carry executable hook code.

`—` means friday maps nothing there **yet**, not that the agent lacks the
surface. oh-my-pi is the one adapter with no `permissions.yaml`/`defaults.yaml`
rule: both would target its own `config.yml`, which is YAML, and friday's
co-owned merge strategies are JSON and TOML only — see [ROADMAP](ROADMAP.md).

`standards/` and `connectors/` have no preset rules. A store may still keep
those directories, but nothing copies them out — a rule's own content reaches
them by path instead, via the `${CLAUDE_PLUGIN_ROOT}` rewrite described below.

‡ Claude Code activates only the hooks declared in `settings.json`, so friday
merges `hooks/hooks.json` into its `hooks` key rather than dropping an inert
copy. Because hook commands run arbitrary shell, `friday push` shows the exact
commands and asks before wiring; a cloned store never registers commands
unattended. Every rule rewrites the Claude-plugin path variable
`${CLAUDE_PLUGIN_ROOT}` to `~/.friday` on push (and back on pull), so
knowledge repos authored as Claude Code plugins — like developer-os — work
unmodified, and cross-references always resolve against the store.

§ Non-Claude hooks are wired the same confirm-first way, from per-agent sources
in the store that all invoke one shared guard script. The four agents with a
declarative hook file — Codex `hooks.json`, Copilot `hooks/*.json`, Gemini via
`settings.json`'s own `hooks` key (the same file its `mcp.yaml`/`defaults.yaml`
renders already co-own) — are merged in; its deny signal is selected per agent
(`exit 2` for Codex; stdout JSON for Copilot/Gemini). OpenCode and oh-my-pi
expose hooks only as imperative TS plugins/extensions, so friday drops a small
guard that shells out to the shared script — the copy is confirm-gated because
it auto-activates on landing. These dialects follow each agent's current docs;
confirm the guard actually blocks on your live install (the Gemini dialect in
particular is corroborated only from secondary sources) — see
[ROADMAP](ROADMAP.md).

Paths verified against each agent's current documentation (Claude Code, [Codex CLI](https://developers.openai.com/codex/guides/agents-md), [OpenCode](https://opencode.ai/docs/config/), [Copilot CLI](https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-custom-instructions)).

**Cursor** does not currently expose user-level rules through the filesystem; its global rules live inside Cursor's settings UI. The cursor preset was removed in v0.0.4. If Cursor adds filesystem-backed global rules (open feature request in their forum), the preset will return.

**Windsurf** was removed in v0.6.0. Cognition is folding Windsurf/Cascade into Devin Desktop (`docs.windsurf.com` now 307-redirects to `docs.devin.ai`) and the legacy Cascade line reached end-of-life on 2026-07-01, so its config paths are a moving target. It can return as a `devin` preset once Devin Desktop's config surface settles under its new name.

To disable an adapter, delete its entry from `friday.yaml`. To customize a target dir or rule, edit it. The presets only seed the manifest at init time; they don't run again.

## Commands

The everyday seven:

```
init                Clone or scaffold your ~/.friday store
claim               Capture an agent's existing config into the store
setup               Add friday knowledge to the current project
sync                Capture local edits, then fan them to every agent
status              Show what would change (no writes)
sessions            List coding-agent sessions across every installed agent
share               Propose your store changes for team review (opens an MR)
```

`friday status` shows a two-column grid — column 1 flags a target you edited
directly (an edit `sync`/`pull` would capture), column 2 flags a pending render
(the store changed and `sync`/`push` would update the agent). Add `--diff` for
the content diff, `--origin` to see where each adapter is defined, or `--check`
for a CI exit code (2 when anything is out of sync).

Underneath (`friday help --all`):

```
push / pull         One-way sync: store → agents, or agent edits → store.
                    `pull --discover` walks an agent dir and captures new files.
promote             Capture project agent config back into ~/.friday
transfer            Move one agent's config to another, through the store
source              Manage the git-backed content sources under `sources:`
                    in friday.yaml (add / list / update / rm)
session             Inspect or export one session (`show`, `export`)
rollback / undo     Restore files from a pre-write snapshot
eject               Capture targets, then remove friday's bookkeeping
remote              Git ops on ~/.friday (init / pull / push / propose / status)
doctor              Health-check the install + lint the store;
                    `doctor <file>` explains a mapping, `doctor --json` for CI
upgrade             Download and install the latest friday release
migrate             Rewrite a friday.yaml written by an older release
version             Print the version
completion          Print a shell completion script
```

Run `friday <command> --help` for a command's flags.
[docs/USE_CASES.md](docs/USE_CASES.md) is the task-oriented manual.

## Safety

- **Atomic writes** — every target file is written via a temp file + rename, so a Ctrl-C mid-write leaves the previous version intact.
- **Drift detection** — friday tracks SHA256 of every file it writes and refuses to clobber edits you've made directly in an agent's dir. Resolve interactively or with `--force`.
- **Pull captures edits; `--discover` captures new files** — a plain `friday pull` only updates files the store already knows about. A file *created* directly in an agent dir (e.g. a new skill authored in `~/.claude/skills/`) is caught by `friday pull --discover`, which walks the agent dir and enriches the store.
- **Not every rule has an inverse** — `concatenate`, `frontmatter_strip`, a plain `merge-json`/`merge-toml` rule, a `render` rule with `surface: defaults`/`codex-agent`/`gemini-command`, and a `render` rule with `surface: permissions` and `merge: copy` are all one-way (no pull) — each target either co-owns content friday does not otherwise track, or was narrowed/stripped on the way out in a way friday cannot reconstruct. `pull` reports those as unsupported instead of guessing. Only `render` rules with `surface: mcp`, or `surface: permissions` merged as `merge-json`/`merge-toml`, have a real pull inverse.
- **Hook code is never installed unattended** — a write that carries hook content is confirmed first, and that decision is keyed on the content, not on which rule produced it. `--no-interactive` (no confirmer) skips such a write; `--force` bypasses the prompt.
- **A configured source is read-only** — friday never writes into `.sources/<name>`, and refuses a capture whose canonical file is owned by one, naming both escape routes: edit it in that source's own repo, or copy it into the primary store, which outranks every configured source.
- **CRLF tolerance** — Windows checkouts don't get flagged as drift.
- **No secrets** — the scaffolded `.gitignore` filters `.env`, `*.key`, `*.pem`, and Claude Code's runtime state dirs.

## Contributing

Issues and PRs welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) for build/test instructions and the [ROADMAP](ROADMAP.md) for where we're headed.

## License

MIT — see [LICENSE](LICENSE).
