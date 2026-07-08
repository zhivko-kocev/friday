# Roadmap

A non-binding view of where friday is headed. Anything here is fair game for a contributor PR — open an issue first if it's a big one.

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
- [x] `friday completion bash|zsh|fish` — generated from the command registry, delegating to a hidden `__complete` callback so it can't drift.
- [x] `friday remote init <url>`.
- [x] `friday remote propose -m "..."` — review-first publishing for team stores: ephemeral commit → new remote branch → MR (GitLab push options; other forges print their PR link). Local store untouched until the MR merges.
- [x] `friday promote` — setup's inverse: project agent config → `~/.friday`, optionally chained into `remote propose`. Closes the loop: knowledge born in a project flows up to the user store and out to the team.
- [x] Per-adapter interactive pull for named adapters.
- [x] `friday push --only <glob>`.
- [x] `windsurf` preset (Windsurf is Devin Desktop by Cognition since June 2026; paths unchanged), `antigravity` preset (`~/.gemini/GEMINI.md`), and `pi` preset (`~/.pi/agent/AGENTS.md` + Agent-Skills-standard `skills/`). Continue, Aider, Zed, and Codeium still have no documented user-level filesystem instruction path — see the rationale comments in `internal/presets/presets.go`.
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

- [ ] **More presets** as agents grow documented filesystem config paths (Cursor, Continue, Aider, Zed — see presets.go for why each is currently absent).
- [ ] **Merge editor integration** — `$EDITOR` on dirty merges as an alternative to conflict markers.
- [ ] **Opt-in hooks wiring** — a confirm-first command that merges a store's `hooks/hooks.json` entries into `~/.claude/settings.json` after showing the exact commands. Never automatic: hooks execute arbitrary shell, and friday's job is syncing cloned repos — auto-registering their commands would be a supply-chain hazard.

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
- **No persistent session-watching or agent supervision.** friday won't tail live
  agent sessions, hold state between invocations, or manage a fleet of running
  agents. `run` hands the terminal to one child and returns its exit code; `review`
  inspects the tree *after* the fact. Anything that needs a always-on process to
  observe agents as they work is out of scope.
- **No proprietary file format.** Markdown in, Markdown out.
- **No hosted service.** Distribution is git, period.
- **No editor extensions.** Editors should call `friday` themselves if they want integration.
- **No project-scope `.friday` stores.** `friday setup` writes into the project's own agent config, which the project's git already versions.
- **No encrypted blobs.** Secrets don't belong in an agent-knowledge repo at all — they live in env vars or a real secret manager; the store is meant to be shared and grep-able. Building crypto into friday would add a `x/crypto` dependency and a passphrase UX to protect content that shouldn't exist in the first place.

## Past releases

See [CHANGELOG.md](CHANGELOG.md) for the per-release log.
