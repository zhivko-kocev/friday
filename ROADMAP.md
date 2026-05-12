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

- [ ] **Shell completion** (`friday completion bash|zsh|fish`). Spec dispatch through `flag.FlagSet` introspection.
- [ ] **`friday remote init <url>`** — set the origin of an already-scaffolded store without re-cloning. Closes the "I scaffolded blank, now I want to publish" gap.
- [ ] **Per-adapter dry-run for `pull`** when an adapter is named — currently only the no-arg pull surfaces diffs interactively.
- [ ] **`friday push --only <glob>`** — push a subset of files matching a glob, useful for editing a single rule and pushing just that.
- [ ] **More presets** — Aider, Continue, Codeium, Zed AI, Windsurf. Each is ~30 LOC plus a test. Standardize via [CONTRIBUTING.md](CONTRIBUTING.md).

## Later

Bigger swings — likely 1.0 milestones:

- [ ] **Project-scope stores.** A second store at `<repo>/.friday/` that overrides user-scope on a per-project basis. Reuses the same engine; the work is in path resolution and dispatch.
- [ ] **Conflict resolution: three-way merge.** When both sides have diverged from the recorded baseline, present a real merge UI (instead of "keep / take / skip").
- [ ] **`friday plugin` for out-of-tree presets.** Drop a `.yaml` preset into `~/.friday/plugins/` and friday picks it up alongside built-ins.
- [ ] **Encrypted blobs** for skill payloads that should ride along the store but not be plaintext in git.

## Not on the roadmap

So future contributors don't waste effort:

- **No daemon.** friday is a one-shot CLI by design.
- **No proprietary file format.** Markdown in, Markdown out.
- **No hosted service.** Distribution is git, period.
- **No editor extensions.** Editors should call `friday` themselves if they want integration.

## Past releases

See [CHANGELOG.md](CHANGELOG.md) for the per-release log.
