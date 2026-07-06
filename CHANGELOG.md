# Changelog

All notable changes to friday are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] — 2026-07-06

The developer-os integration release: friday now consumes knowledge repos authored as Claude Code plugins, passes user-level knowledge down into projects interactively, and lands the bulk of the roadmap.

### Added
- **`replace:` rule transform** — literal string rewriting on push, inverted on pull. Presets use it to rewrite `${CLAUDE_PLUGIN_ROOT}` (the Claude plugin path variable) to `~/.friday`, so plugin-shaped knowledge repos like developer-os work unmodified.
- **`friday setup`** — interactive project-level apply: pick an agent, pick knowledge from `~/.friday` (core / rules / agents / commands / skills), and friday writes it into the project's git-tracked config (`CLAUDE.md`, `.claude/…`, `.opencode/…`, `.github/copilot-instructions.md`). No project `.friday` folder — the project's git does the versioning.
- `friday init --from-git URL` — alias of `--remote`.
- `friday sync` — pull then push in one command.
- `friday import <adapter|dir>` — bootstrap or enrich `~/.friday` from an existing agent installation, including files authored directly in target dirs (which `pull` cannot see).
- `friday compile --from X --to Y` — one-shot conversion between agent formats via a throwaway store; lossy conversions require `--allow-lossy` and print a loss summary.
- `friday explain <target-file>` — which adapter + rule produces a file, from which sources.
- `friday rollback` — every push records a snapshot (content-addressed blobs + journal, last 10 kept); rollback restores the pre-push target state.
- **Three-way merge** — conflict prompts offer `[m]` when the last-synced base is recoverable from the snapshot store; clean merges apply directly, overlapping edits offer git-style conflict markers.
- **Bidirectional drift detection on pull** — canonical-side baselines mean pull now prompts instead of silently eating store edits when both sides changed.
- `friday status --json` — machine-readable output; exits 2 on conflicts for CI gating.
- `friday completion bash|zsh|fish` — scripts delegate to a hidden `__complete` callback generated from the command registry, so completions can never drift from the binary.
- `friday remote init <url>` — set origin on an already-scaffolded store.
- `friday promote [paths...]` — setup's inverse: capture project-level agent config (e.g. a skill a teammate hand-added under `.claude/skills/`) up into `~/.friday`, with optional path filters and `--propose -m "..."` to chain straight into an MR. Concatenated instruction files are irreversible and reported as unsupported; `${CLAUDE_PLUGIN_ROOT}` markers are restored on the way up.
- `friday remote propose -m "..."` — review-first publishing for team stores: pushes the working tree as an ephemeral commit to a new remote branch (default `friday/propose-<timestamp>`) and opens an MR against the remote's HEAD branch via GitLab push options (other forges print their PR link). The local branch, history, and working tree stay untouched; after the MR merges, `friday remote pull` fast-forwards and the local edits coincide with the merged content.
- `friday push --only <glob>` — push only changes sourced from matching store files.
- `friday plugin list|validate` — out-of-tree YAML presets in `~/.friday/plugins/`, layered between built-ins and `friday.yaml`.
- `friday lint` — malformed frontmatter, oversized files, broken relative refs, destination collisions.
- `friday eject` — capture targets into the store, then remove friday's bookkeeping (manifest, drift cache, snapshots).
- `windsurf` preset (`~/.codeium/windsurf/memories/global_rules.md`; Windsurf is Devin Desktop by Cognition since June 2026 — paths unchanged).
- `antigravity` preset — Google Antigravity global rules at `~/.gemini/GEMINI.md`; project scope writes the root `AGENTS.md`.
- `pi` preset — pi coding agent: `~/.pi/agent/AGENTS.md` + `~/.pi/agent/skills/` (Agent Skills standard, Claude-shaped SKILL.md works as-is); project scope writes root `AGENTS.md` + `.pi/skills/`.
- `friday doctor` now reports the entry-file variant in use, warns on multiple variants, and explains how to wire a store's `hooks/hooks.json` into Claude Code.

### Changed
- **The canonical entry file is `core.md`** (also matched at `core/core.md`); `identity.md` keeps working as a legacy name. `friday init --scaffold` now writes `core.md`.
- `friday pull <adapter>` now uses the same per-adapter diff + confirm flow as bare `friday pull`; the legacy batch flow remains behind `--no-interactive`.
- `friday status` exits 2 when conflicts are present (was always 0).
- Copy rules with multi-pattern from-lists report `missing-source` per rule instead of per pattern.
- Docs now reference developer-os-style knowledge repos instead of dotai.

## [0.0.4] — 2026-05-12

Adapter audit against current official docs. Two breaking changes; one new built-in preset.

### Added
- `codex` preset for OpenAI Codex CLI. Targets `~/.codex/`, concatenates identity + rules into `AGENTS.md` (the file Codex actually reads, per https://developers.openai.com/codex/guides/agents-md).
- Push/pull summary now lists adapters processed and gives a per-adapter tally (created / updated / in-sync), plus a `total` row.

### Changed
- **Breaking: `copilot` preset target moved from `~/.github/` to `~/.copilot/`** to match the path the Copilot CLI and VS Code Copilot extension actually read (`~/.copilot/copilot-instructions.md`). Users on v0.0.3 should move their file or edit `friday.yaml` to keep the old path.
- **Breaking: `cursor` preset removed.** Cursor's user-level rules are stored inside Cursor's settings UI, not the filesystem, so the preset wrote files Cursor never reads. The preset will return when Cursor ships filesystem-backed global rules.

## [0.0.3] — 2026-05-12

First open-source-ready cut. Bug fixes, new commands, and a full set of community files.

### Added
- `friday doctor` — read-only health check that surfaces store presence, git status, manifest validity, per-adapter installation state, and any detected drift.
- `friday init --remote URL` flag — clone a remote without piping into stdin.
- `friday init --scaffold` flag — empty-store scaffold without prompting.
- Push/pull summary line — `n created, m updated, k in-sync, … skipped` after every change report.
- Community files: `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md`, `ROADMAP.md`, `CHANGELOG.md`.
- GitHub issue + PR templates, dependabot config, `.editorconfig`.

### Changed
- **Target file writes are now atomic** (temp file + fsync + rename). A Ctrl-C mid-write leaves the previous version of the file intact instead of corrupting it.
- **`opencode` preset target** moved from `~/.opencode` to `~/.config/opencode` to match the XDG convention OpenCode itself uses. Users with `~/.opencode` from earlier versions should either rename the directory or edit `friday.yaml` to keep the old path.
- Removed unused `friday add` / `friday remove` commands and the `presets.Resolve` helper they relied on. `friday init` already seeds every preset; adapters are toggled by editing `friday.yaml` directly.
- README rewritten with badges, full doctor/init flag docs, and a safety section.

### Fixed
- `output.Dim` / `output.Header` no longer re-interpret literal `%` in pre-formatted strings as `fmt` verbs (would render as `%!(NOVERB)`).

## [0.0.2] — Windows installer

- Added `install.ps1` for PowerShell installation on Windows.
- goreleaser configured for `windows/amd64` archives.

## [0.0.1] — initial tag

- First tagged build. Core commands: `init`, `list`, `push`, `pull`, `status`, `remote`.
- Built-in presets for Claude Code, Cursor, OpenCode, and GitHub Copilot.
- SHA256-based drift detection with CRLF-tolerant hashing.
- Interactive conflict resolver with line-LCS diff.
- Cross-platform: Linux, macOS, Windows.

[Unreleased]: https://github.com/zhivko-kocev/friday/compare/v0.0.4...HEAD
[0.0.4]: https://github.com/zhivko-kocev/friday/compare/v0.0.3...v0.0.4
[0.0.3]: https://github.com/zhivko-kocev/friday/compare/v0.0.2...v0.0.3
[0.0.2]: https://github.com/zhivko-kocev/friday/compare/v0.0.1...v0.0.2
[0.0.1]: https://github.com/zhivko-kocev/friday/releases/tag/v0.0.1
