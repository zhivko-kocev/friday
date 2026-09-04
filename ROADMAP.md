# Roadmap

A non-binding view of where friday is headed. Anything here is fair game for a contributor PR — open an issue first if it's a big one.

## Landed (v1.0)

A CLI-only rewrite: gone is v0.5.0's interactive control room (the porcelain
commands below are the whole surface again — no TUI, no second frontend to
keep in sync), and friday now renders **all** of an agent's configuration,
not just Markdown knowledge.

- [x] **CLI-only, again.** The full-screen control room TUI (`internal/ui/tui`,
  bubbletea) is deleted outright — bare `friday` prints usage and a
  state-aware next-step hint (no store yet → `init`/`claim`; pending renders
  on an installed adapter → `sync`; an unclearable conflict → `status`, then
  `sync` per file or `push --force`) instead of opening a screen. Every
  command is the same one-shot CLI whether stdout is a terminal or a pipe.
- [x] **Command framework migration to `urfave/cli/v3`.** The hand-rolled
  dispatcher, flag parser, and completion generator are gone; every command,
  alias, flag name, and exit code stayed identical. Gained: real shell
  completion (`friday completion bash|zsh|fish`, still generated so it can't
  drift) and a two-tier `help`/`help --all` split (porcelain vs. advanced).
- [x] **Multi-source stores.** `sources:` in `friday.yaml` composes one or
  more additional git remotes (cloned under `.sources/<name>`) underneath
  the primary store, lowest precedence first — a shared org-wide rule pack
  layered under your personal store, expanded through the same glob engine
  as before. `status --origin` and `doctor` both understand it
  (`source add/list/update/rm`, source-shadow and source-manifest checks).
  A configured source is read-only: a capture whose canonical file one owns
  is refused, naming both escape routes (change it in that source's own
  repo, or copy it into the primary store, which outranks every source).
  `source add` validates the name before cloning, so it can never escape
  `.sources/` or persist a manifest the loader would reject.
- [x] **friday renders an agent's full config, not just knowledge.** Three
  new canonical, adapter-agnostic YAML schemas — `mcp.yaml` (Model Context
  Protocol servers), `permissions.yaml` (a five-axis read/write/exec/net/mcp
  allow/ask/deny policy), and `defaults.yaml` (model, theme, statusline,
  telemetry opt-out) — each project onto every adapter's own native shape
  (JSON or TOML) via a new `render` strategy (`surface` + `merge`). A
  `merge-toml` strategy splices the leaves friday owns into a co-owned TOML
  file (e.g. Codex's `config.toml`), the TOML analog of `merge-json`:
  unmanaged tables and keys survive, but the file is re-emitted through
  go-toml's encoder, so quoting can normalize and an owned entry can move —
  only `merge-json` round-trips untouched input byte for byte. Two
  passthrough dirs round out the surface: `native/<adapter>/**/*` mirrors
  anything a schema doesn't model verbatim, and `settings/<adapter>.{json,toml}`
  merges arbitrary extra keys into the same native file. `mcp-secret`
  (`internal/secret`) flags a literal credential in a store config file
  instead of a required `${VAR}` reference.
- [x] **Presets rebuilt on the new schemas.** `claude`, `codex`, `copilot`,
  `opencode`, and `gemini` each gained `mcp.yaml`, `permissions.yaml`, and
  `defaults.yaml` rules, plus a `settings/<adapter>.{json,toml}` merge
  fragment (Codex also renders subagents — `agents/*.md` → TOML
  `name`/`description`/`developer_instructions` — via the same `render`
  strategy). `oh-my-pi` gained `mcp.yaml` only: `permissions.yaml` and
  `defaults.yaml` would both target its `config.yml`, and friday has no
  YAML co-owned-merge strategy — tracked under "Later" below. The
  `antigravity` preset is retired; **`gemini`** replaces it
  (`~/.gemini/GEMINI.md`, `settings.json` hooks, all three schemas with
  `permissions.yaml` landing as `policies/friday.toml`, and a
  `gemini-command` surface for `commands/*.md`). A shared `agents` adapter
  (`~/.agents/skills`) now serves Codex/Copilot/Gemini/OpenCode's shared
  Agent-Skills discovery in one tree instead of four duplicate copies —
  claude and oh-my-pi each keep reading their own skills dir natively.
- [x] **Breaking: two preset keys were renamed, and two rule strategies
  retired.** `antigravity` is now `gemini`, and `pi` is now `oh-my-pi` with
  its target moved from `~/.pi/agent` to `~/.omp/agent`. `md-to-toml` and
  `md-to-json` (the old Codex/Antigravity subagent-rendering strategies) are
  replaced by `render` with `surface: codex-agent`/`gemini-command`. The two
  renames fail differently, which is why both need naming: the old
  `antigravity` preset carried an `md-to-json` rule, so it announces itself,
  while the old `pi` preset used only `copy`/`concatenate` — it keeps
  parsing and keeps writing to the dead pi-mono path, with no `oh-my-pi`
  adapter for `friday sessions`, `friday transfer`, or any render rule to
  reach.
- [x] **A stale manifest degrades instead of bricking the CLI.** A rule
  naming a retired strategy is dropped at load time with a warning that
  names the adapter, the rule index, and the replacement surface; a legacy
  adapter key gets a rename warning. Both are collected on the config and
  printed once per run, on stderr, so `--json` output stays parseable. Before
  this, every command exited 1 on a manifest seeded by the previous release.
  Fix: **`friday migrate`** (`--dry-run` to preview; idempotent) renames the
  legacy keys, refreshes those adapters' target and rules from the current
  preset, and drops the retired-strategy rules — editing `friday.yaml`
  through a yaml.Node editor so comments, key order, and every other adapter
  survive. Your knowledge files (`core.md`, `rules/`, `agents/`, `commands/`,
  `skills/`) are never touched.
- [x] **Project scope for custom adapters.** `friday.yaml` adapters can carry
  `project_target`/`project_rules` alongside their user-scope `target`/`rules`,
  so `friday setup --dry-run` and `promote` work for an adapter you define
  yourself, not only the built-ins. An adapter declared with user-scope rules
  only — which is what `friday init` seeds — falls back to the built-in
  preset's project mapping instead of failing to resolve, and project-scope
  destinations are clamped to the project directory.
- [x] **`friday claim`** — capture one or more agents' existing on-disk
  config into the store in one step (init's per-adapter discovery, promoted
  to a first-class command).
- [x] **`friday transfer <from> <to>`** — move one agent's config to another
  *through* the store: capture the source agent's config in, push it back out
  under the destination agent's rules. The single point where a developer
  compiles a session's config from one agent to another.
- [x] **`friday sessions`** — a read-only, cross-agent session index over
  Claude Code, Codex, Copilot, Gemini CLI, OpenCode, and oh-my-pi's own on-disk
  transcript stores (`--agent`, `--project`, `--since`, `--json`, `--stats`
  rollups by agent/model/period). `friday session show <id>` prints a
  transcript; `friday session export <id>` writes a deterministic markdown
  handoff brief at `0o600` (a brief embeds raw transcript text), refusing to
  overwrite an existing file without `--force`. friday never writes into any
  agent's own session store — every reader only ever reads what that agent
  already produced, and each probes that agent's session-state directory for
  availability rather than friday's own push target.
- [x] **`friday upgrade`** — chezmoi-style self-update: detects the install
  method (brew/scoop print their own upgrade command instead of downloading),
  otherwise downloads the matching release asset, verifies its published
  checksum, and replaces the binary atomically. Every release URL, metadata
  and asset alike, must be https on an allowlisted official host — checked
  before any bytes move and re-checked on each redirect. The checksums file
  ships from the same release as the asset, so this is integrity, not
  authenticity. Explicit invocation only — no background version checks.
- [x] **`friday doctor` delegates content linting to `agnix`.** friday's own
  checks stay strictly render-graph (destination collisions, drift,
  hooks-wiring staleness, source-shadow, source-manifest, `mcp-secret`, store
  structure); when an [`agnix`](https://github.com/agent-sh/agnix) binary is
  on PATH, `doctor` runs it over every source root and folds its findings in
  under an `agnix/`-prefixed rule and an `agnix` origin — otherwise it prints
  one line naming where to get it. Never a reason to fail `doctor`: an
  unusable agnix response degrades to that same hint — the delegation runs
  under a timeout with capped output, and the reported SARIF version is
  validated, so non-2.x output is reported as failed rather than read as a
  clean bill of health. Fixed alongside it: `unquotedExpansion` (the
  unquoted-`$HOME`-in-a-hook-command check) now really parses the command
  instead of clearing on any unrelated quote.
- [x] **Hooks for every agent, confirm-first.** An agent activates only the
  hooks its own config declares, so friday merges the store's hook source
  into that config file rather than dropping an inert copy: claude's
  `hooks/hooks.json` into `~/.claude/settings.json`, codex's into
  `~/.codex/hooks.json`, gemini's into `~/.gemini/settings.json`, and
  copilot's into its own `~/.copilot/hooks/friday-git-guard.json`. All four
  drive one shared, mode-parametrized guard whose deny signal is selected
  per agent (`exit 2` for codex; stdout JSON for copilot and gemini, whose
  event names and deny shapes differ from claude's). OpenCode and oh-my-pi
  expose hooks only as imperative TS plugins/extensions, so they get a small
  guard plugin that shells out to the same script and blocks by throwing /
  returning `{block:true}`. The scripts run from the store in place;
  `friday setup` wires project scope through `${CLAUDE_PROJECT_DIR}` so the
  in-repo copies resolve for anyone who clones the repo. Consent is keyed on
  hook *content*, not on a rule flag: any merge whose source lives under
  `hooks/`, plus every `confirm_hook` copy — which includes all six
  `native/<adapter>/**/*` mirrors, since a mirror lands verbatim in a
  directory the agent auto-executes from. friday prints the exact commands
  and prompts first; a nil confirmer (`--no-interactive`) skips the write and
  only `--force` bypasses the prompt. All six agents covered; live-block
  verification against each running agent is still pending.
- [x] **DX baseline.** `FRIDAY_HOME` overrides the store root (following
  `RUSTUP_HOME`/`CARGO_HOME`); `--quiet` suppresses every non-error line
  without changing exit codes — including the bare-invocation usage banner,
  which prints outside the usual output helpers, and the deprecated-spelling
  nudge, routed through `output.Warn` once urfave has parsed the flag;
  `--verbose` names the resolved source behind each rendered file,
  tagging it `<source>@<root>` once more than one root is configured;
  `FORCE_COLOR` forces color even off a terminal, alongside the existing
  `NO_COLOR`/`FRIDAY_NO_COLOR`; and `status`/`sessions`/`doctor`'s tables are
  column-width-aware (`lipgloss/table`) instead of a hardcoded
  `%-10s`-style printf width.

## Landed (v0.5.0)

Superseded: the control room was deleted in v1.0 (see above), and the
porcelain tier has since grown to seven commands.

- [x] **Interactive control room.** Bare `friday` on a real terminal opens a full-screen TUI (Charm stack) over the existing engine and verbs — a new frontend, not new commands, and never a resident process. The menu is the maintain-loop — **sync, setup, status, share, discover** — with cold-start (clone or scaffold a fresh `~/.friday`) on first run. Sync captures + fans out with a per-agent checklist; setup applies a knowledge checklist to a project; status shows the pending-render plan (drift surfaces as a conflict row); share proposes to the team (confirm-first, git-backed only); discover imports target-only files. Conflicts resolve in a diff modal (keep / take / skip, push and pull), and Ctrl-C halts an apply cleanly (atomic writes → never half-written). A `?` help overlay, consistent keys, an opt-in `d` diff toggle, and windowed lists round out the UX. Any flag/subcommand — and any piped / CI / `--no-interactive` run — keeps the exact one-shot, plain-text CLI, byte-for-byte. See [CHANGELOG.md](CHANGELOG.md).
- [x] **Leaner command surface.** Cut `friday list`/`ls` (redundant with `status`/`doctor`; its per-adapter rule view moved to `status --origin`). The porcelain tier is now `init`, `setup`, `sync`, `status`, `share`.

## Landed (shipped through v0.2.1)

The developer-os integration and the bulk of the former roadmap — all of the
following has shipped. See [CHANGELOG.md](CHANGELOG.md) for the release each
item landed in (v0.1.0–v0.2.1).

- [x] **Knowledge-repo support.** The canonical entry file is `core.md` (also matched at `core/core.md`; legacy `identity.md` still works). Presets understand plugin-shaped repos like developer-os and rewrite `${CLAUDE_PLUGIN_ROOT}` references to `~/.friday` via the new generic `replace:` rule transform (inverted on pull). Presets copy only what agents discover on disk; everything else is reached by reference into the store.
- [x] **`friday setup`** — interactive project-level apply. Pick an agent, pick knowledge from `~/.friday` (core, rules, agents, commands, skills), and friday writes it into the project's git-tracked config (`CLAUDE.md`, `.claude/…`, `.opencode/…`, `.github/copilot-instructions.md`). This replaces the previously planned `<repo>/.friday` project stores — projects stay friday-optional, git does the versioning.
- [x] `friday init --from-git URL` (alias of `--remote`).
- [x] **Bidirectional drift detection on pull** — canonical-side baselines; both-sides-drifted files prompt instead of eating store edits.
- [x] `friday doctor <target-file>` — which adapter + rule produced a file, from which sources (was `friday explain`).
- [x] `friday pull --discover` — bootstrap/enrich `~/.friday` from an existing installation (reverse expansion; sees files a normal pull can't). Was `friday import`.
- [x] `friday status --json` (exit 2 on conflicts, for CI).
- [x] `friday completion bash|zsh|fish` — generated from the command registry so it can't drift from the binary. Since v1.0 the generator is `urfave/cli/v3`'s own: the script calls the binary back with `--generate-shell-completion`, and dynamic values (adapter names) come from per-command `ShellComplete` funcs. A script installed before v1.0 calls a hidden `__complete` callback instead; friday still implements that callback (`internal/cli/shellcomplete.go`), so an old script keeps working — regenerate only to pick up the richer flag and adapter introspection.
- [x] `friday remote init <url>`.
- [x] `friday remote propose -m "..."` — review-first publishing for team stores: ephemeral commit → new remote branch → MR (GitLab push options; other forges print their PR link). Local store untouched until the MR merges.
- [x] `friday promote` — setup's inverse: project agent config → `~/.friday`, optionally chained into `remote propose`. Closes the loop: knowledge born in a project flows up to the user store and out to the team.
- [x] Per-adapter interactive pull for named adapters.
- [x] `friday push --only <glob>`.
- [x] `windsurf` preset (Windsurf is Devin Desktop by Cognition since June 2026; paths unchanged), a global GEMINI.md preset for Gemini CLI (`~/.gemini/GEMINI.md`), and `pi` preset (`~/.pi/agent/AGENTS.md` + Agent-Skills-standard `skills/`). All three are gone: windsurf was dropped in v1.0, and the other two were superseded there by the `gemini` and `oh-my-pi` presets. Continue, Aider, Zed, and Codeium still have no documented user-level filesystem instruction path — see the rationale comments in `internal/presets/presets.go`.
- [x] `friday sync` — pull then push with shared baseline bookkeeping.
- [x] `friday rollback` — push snapshots (content-addressed blobs + journal) with restore.
- [x] **Three-way merge** in the conflict prompt (`[m]`) when a merge base is recoverable from the snapshot store; clean merges apply, overlaps offer conflict markers.
- [x] `friday eject` — capture targets into the store, then remove friday's bookkeeping.
- [x] Store checks — malformed frontmatter, oversized files, broken relative refs, destination collisions — run as part of `friday doctor` (was `friday lint`).

## Landed (v0.3.2)

- [x] **Output & experience polish.** `push`/`pull`/`sync` collapse to one count-plus-folder-breakdown line per adapter (in-sync files fold to a tally; only conflicts are named); the `status` grid does the same for large pending-render groups while keeping hand-edits and conflicts per file. `--diff` is windowed (context around each edit, elided regions marked, per-file cap). Flag cleanup: `pull --force` → `--all` (soft-deprecated), `init --from-git` is the documented clone flag (`--remote` soft-deprecated), `doctor --json` and the `remote` subcommand flags now show in help/completion, and `list` no longer takes the ignored `adapters`/`all` positional.

## Landed (v0.3.0)

- [x] **Two-axis `friday status`** — a chezmoi-style two-column grid (local edits to capture + pending renders), with `--diff`, `--origin`, and a `--check` CI exit code. The `--json` body and default exit code are unchanged.
- [x] **Best-practice advisor** — `friday doctor` gained severity-ranked static-config lint rules (`long-instructions`, `skill-description`, plus the existing store checks), a per-store `.friday-doctor.yaml` disable list, and `doctor --json`. A one-shot linter for how you author agent config — never a resident process; runtime operating judgment stays out of scope by design.

## Later

- [ ] **`devin` preset** to replace the removed `windsurf` one, once Devin Desktop's config surface settles under its new name (Windsurf was dropped in v1.0 — Cascade EOL 2026-07-01 + the Cognition rebrand).
- [ ] **More presets** as agents grow documented filesystem config paths (Cursor, Continue, Aider, Zed — see presets.go for why each is currently absent).
- [ ] **Merge editor integration** — `$EDITOR` on dirty merges as an alternative to conflict markers, from the plain conflict prompt's `[m]` merge option.
- [ ] **A YAML co-owned-merge strategy** (`merge-yaml`), the analog of
  `merge-json`/`merge-toml` for a settings file that is YAML. This is the one
  thing blocking `permissions.yaml` and `defaults.yaml` rules for `oh-my-pi`,
  whose policy and settings both live in its own `config.yml`. Until then that
  adapter renders knowledge, subagents, commands, skills, and `mcp.yaml` only.

## Later — orchestration (one-shot)

The larger idea: friday sits *above* the agents, not just beside them. Today it
compiles one source of truth into every agent's config. The natural next step is
to also *drive* the agent that config describes — while staying the same
one-shot CLI, never a resident process.

- [ ] **`friday run [agent]`** — read the project's agent config (what `friday setup`
  wrote), spawn that agent as a child process, wait for it to finish, and exit.
  One command instead of remembering which binary and flags each project's agent
  needs. Still one-shot: friday launches, hands over the terminal, and returns
  the child's exit code — no supervision loop, no background daemon.
- [ ] **`friday review`** — after a session, summarize what the agent changed in
  the working tree and surface reusable knowledge (a skill it leaned on, a rule
  it kept restating) as candidates to `promote` up into the store and `share`
  with the team. Closes the loop: knowledge born in a session flows back to the
  single source of truth.

Both are read-a-config → do-one-thing → exit. If a design ever requires friday
to *keep running* to deliver it, that's the signal it belongs under "Not on the
roadmap," not here.

## Not on the roadmap *yet*

These are deliberate non-goals **for now** — not permanent bans. friday's near-term
identity is a one-shot, no-service CLI, and everything below would pull against that,
so it stays out of scope until the core is mature enough to revisit them on purpose.
When one does get taken on, it earns its own design discussion first. Until then, so
contributors don't sink effort into a direction the project isn't ready for:

- **No daemon.** friday is a one-shot CLI by design. Even the orchestration ideas
  above (`run`, `review`) spawn, wait, and exit — friday never sits resident.
- **No persistent session-watching or agent supervision.** friday won't tail a
  *live* agent session, hold state between invocations, or manage a fleet of
  running agents. Reading a *finished* transcript on demand is a different
  thing and already in scope — `friday sessions` / `session show` / `session
  export`, read-only, one-shot, never writing back into an agent's own
  session store. `run` hands the terminal to one child and returns its exit
  code; `review` inspects the tree *after* the fact. Anything that needs an
  always-on process to observe agents *as they work* stays out of scope.
- **No proprietary knowledge format.** Knowledge (core, rules, agents,
  commands, skills) is Markdown in, Markdown out. Structured agent config
  (`mcp.yaml`, `permissions.yaml`, `defaults.yaml`) is YAML — plain,
  human-editable, diffable — because its rendering targets are JSON and
  TOML, the formats the agents themselves already speak.
- **No hosted service.** Distribution is git, period.
- **No online catalogs.** Browsing or pulling skills/agents from a remote index beyond your own git store crosses the "distribution is git" line and implies a service to host and curate the index. The one-shot-shaped version that could land instead: git-sourced community rule packs added as an ordinary store remote and pulled through the existing engine — no new network surface, no catalog service.
- **No editor extensions.** Editors should call `friday` themselves if they want integration.
- **No project-scope `.friday` stores.** `friday setup` writes into the project's own agent config, which the project's git already versions.
- **No encrypted blobs.** Secrets don't belong in an agent-knowledge repo at all — they live in env vars or a real secret manager; the store is meant to be shared and grep-able. Building crypto into friday would add a `x/crypto` dependency and a passphrase UX to protect content that shouldn't exist in the first place.

## Past releases

See [CHANGELOG.md](CHANGELOG.md) for the per-release log.
