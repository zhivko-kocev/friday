# Roadmap

A non-binding view of where friday is headed. Anything here is fair game for a contributor PR — open an issue first if it's a big one.

## Landed (unreleased, in master)

The developer-os integration and the bulk of the former roadmap:

- [x] **Knowledge-repo support.** The canonical entry file is `core.md` (also matched at `core/core.md`; legacy `identity.md` still works). Presets understand plugin-shaped repos like developer-os and rewrite `${CLAUDE_PLUGIN_ROOT}` references to `~/.friday` via the new generic `replace:` rule transform (inverted on pull). Presets copy only what agents discover on disk; everything else is reached by reference into the store.
- [x] **`friday setup`** — interactive project-level apply. Pick an agent, pick knowledge from `~/.friday` (core, rules, agents, commands, skills), and friday writes it into the project's git-tracked config (`CLAUDE.md`, `.claude/…`, `.opencode/…`, `.github/copilot-instructions.md`). This replaces the previously planned `<repo>/.friday` project stores — projects stay friday-optional, git does the versioning.
- [x] `friday init --from-git URL` (alias of `--remote`).
- [x] `friday compile --from X --to Y` with a `--allow-lossy` gate and loss summary.
- [x] **Bidirectional drift detection on pull** — canonical-side baselines; both-sides-drifted files prompt instead of eating store edits.
- [x] `friday explain <target-file>` — which adapter + rule produced a file, from which sources.
- [x] `friday import <adapter|dir>` — bootstrap/enrich `~/.friday` from an existing installation (reverse expansion; sees files pull can't).
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
- [x] `friday plugin` — out-of-tree YAML presets in `~/.friday/plugins/`, layered between built-ins and `friday.yaml`.
- [x] `friday eject` — capture targets into the store, then remove friday's bookkeeping.
- [x] `friday lint` — malformed frontmatter, oversized files, broken relative refs, destination collisions.

## Later

- [ ] **More presets** as agents grow documented filesystem config paths (Cursor, Continue, Aider, Zed — see presets.go for why each is currently absent).
- [ ] **Merge editor integration** — `$EDITOR` on dirty merges as an alternative to conflict markers.
- [ ] **Opt-in hooks wiring** — a confirm-first command that merges a store's `hooks/hooks.json` entries into `~/.claude/settings.json` after showing the exact commands. Never automatic: hooks execute arbitrary shell, and friday's job is syncing cloned repos — auto-registering their commands would be a supply-chain hazard.

## Not on the roadmap

So future contributors don't waste effort:

- **No daemon.** friday is a one-shot CLI by design.
- **No proprietary file format.** Markdown in, Markdown out.
- **No hosted service.** Distribution is git, period.
- **No editor extensions.** Editors should call `friday` themselves if they want integration.
- **No project-scope `.friday` stores.** `friday setup` writes into the project's own agent config, which the project's git already versions.
- **No encrypted blobs.** Secrets don't belong in an agent-knowledge repo at all — they live in env vars or a real secret manager; the store is meant to be shared and grep-able. Building crypto into friday would add a `x/crypto` dependency and a passphrase UX to protect content that shouldn't exist in the first place.

## Past releases

See [CHANGELOG.md](CHANGELOG.md) for the per-release log.
