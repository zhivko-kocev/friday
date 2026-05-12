# Roadmap

A non-binding view of where friday is headed. Anything here is fair game for a contributor PR — open an issue first if it's a big one.

## Now (the next release)

Already in flight or done in master:

- [x] `friday doctor` — read-only health check.
- [x] `friday init --remote URL` / `--scaffold` flags for non-interactive setup.
- [x] Atomic writes for target files (Ctrl-C safe).
- [x] Push summary line (n created / m updated / k in-sync).

## Next

Things we want, sized as "a focused PR can land it":

- [ ] **`friday compile --from <agent> --to <agent>`** — one-shot conversion between agent formats without going through `~/.friday`. Reads the source adapter's target dir, writes the target adapter's. Internally pipelines existing pull → push primitives over an in-memory store. Useful for migrations ("I have a working `.claude`, what would that look like as opencode?") and for sharing a config flavour without forcing the recipient to adopt friday's canonical layout. Lossy conversions (e.g. claude's `agents/` + `commands/` → opencode, which has neither) need an explicit `--allow-lossy` flag and a warning summary.
- [ ] **Bidirectional drift detection on pull.** Today drift only blocks push. Pull silently overwrites `~/.friday` with the target — if you've edited *both* sides since the last sync, your canonical edits get eaten. Fix: on pull, check whether canonical has drifted from the baseline before overwriting. When both sides drifted → conflict prompt (keep canonical / take target / skip), same UX as push. Direct precursor to the three-way merge listed under "Later".
- [ ] **`friday explain <target-file>`** — prints which adapter + rule produced a given file, with the source paths it pulled from. Critical readability tool once a store has 10+ rules and 30+ generated files.
- [ ] **`friday import <agent-dir>`** — bootstrap `~/.friday` from an existing agent installation. Onboarding shortcut: users with a working `~/.claude/` shouldn't have to re-author everything to try friday.
- [ ] **`friday status --json`** — machine-readable output for CI integration and tooling. Same data, structured.
- [ ] **Shell completion** (`friday completion bash|zsh|fish`). Spec dispatch through `flag.FlagSet` introspection.
- [ ] **`friday remote init <url>`** — set the origin of an already-scaffolded store without re-cloning. Closes the "I scaffolded blank, now I want to publish" gap.
- [ ] **Per-adapter dry-run for `pull`** when an adapter is named — currently only the no-arg pull surfaces diffs interactively.
- [ ] **`friday push --only <glob>`** — push a subset of files matching a glob, useful for editing a single rule and pushing just that.
- [ ] **More presets** — Aider, Continue, Codeium, Zed AI, Windsurf. Each is ~30 LOC plus a test. Standardize via [CONTRIBUTING.md](CONTRIBUTING.md).

## Later

Bigger swings — likely 1.0 milestones:

- [ ] **`friday sync`** — single command that does pull + push with smart conflict handling. Pull edits back first, then push canonical out. Like `git pull` = fetch + merge. Reduces the two-command dance to one.
- [ ] **`friday rollback`** — restore the previous target state from a snapshot taken at each push. Safety net for "I pushed and it ate something". Snapshots stored alongside the drift cache.
- [ ] **Project-scope stores.** A second store at `<repo>/.friday/` (discovered by walking up from CWD, like `.git`) that layers over user-scope on a per-project basis. The infrastructure is mostly in place — `config.Config` already separates `StoreDir` and `TargetRoot`, and `Scope` is an int with only `ScopeUser` defined today. Open design questions:
  - **Merge vs replace?** Project rules override matching user rules by adapter name, or merge per-rule? Lean toward replace-by-adapter — predictable beats clever.
  - **Targets resolve to CWD, not `$HOME`.** A project-scope `claude` adapter writes `./.claude/` next to the project's source, not the user's home. `TargetRoot` flips accordingly.
  - **Drift cache scoping.** Keep the single `$UserCacheDir/friday/state.json` (keyed by absolute path, already works) or shard per-store? Single is simpler and the keying already handles it.
  - **CLI dispatch.** `friday push` auto-detects nearest project store; `--scope user` forces global. Status should show both layers clearly.
  - **Init UX.** `friday init --project` scaffolds `./.friday/` and adds it to `.gitignore` or commits it (user's choice — prompt).
- [ ] **Conflict resolution: three-way merge.** When both sides have diverged from the recorded baseline, present a real merge UI (instead of "keep / take / skip"). Builds on the bidirectional drift detection above.
- [ ] **`friday plugin` for out-of-tree presets.** Drop a `.yaml` preset into `~/.friday/plugins/` and friday picks it up alongside built-ins.
- [ ] **`friday eject`** — copy the current target state back into canonical form, then remove `friday.yaml` and the drift cache. Clean exit door for users who want to leave.
- [ ] **`friday lint`** — static check of `.md` files: malformed frontmatter, oversized files, broken file references, duplicate rule keys. Quality signal before push.
- [ ] **Encrypted blobs** for skill payloads that should ride along the store but not be plaintext in git.

## Not on the roadmap

So future contributors don't waste effort:

- **No daemon.** friday is a one-shot CLI by design.
- **No proprietary file format.** Markdown in, Markdown out.
- **No hosted service.** Distribution is git, period.
- **No editor extensions.** Editors should call `friday` themselves if they want integration.

## Past releases

See [CHANGELOG.md](CHANGELOG.md) for the per-release log.
