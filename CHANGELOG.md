# Changelog

All notable changes to friday are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- **Hooks are now wired for you (confirm-first).** Claude Code activates only the hooks declared in `settings.json` — a loose `~/.claude/hooks/hooks.json` is never loaded — so the `claude` preset now merges the store's `hooks/hooks.json` into the `hooks` key of `~/.claude/settings.json` instead of dropping an inert copy you had to wire by hand. The hook scripts run from the store in place.
  - **New `merge-json` rule strategy.** Deep-merges one source JSON file into a co-owned target at the entry level: objects union their keys, and arrays keep the target's own elements while refreshing friday's. Your other keys (`model`, `permissions`, …), unmanaged hook events, and hooks you added by hand are all preserved. It canonicalizes deterministically (sorted keys, preserved arrays, exact numbers, no HTML-escaping), is idempotent, and is push-only + drift-exempt so unrelated edits never read as drift or conflict. A non-empty but unparseable target is an error — friday writes nothing rather than clobber it.
  - **Local ownership tracking.** friday records the entries it wrote in a machine-local `hooks-owned.json` (beside the drift store, never synced) so a later push can remove its own now-stale entry after you change a hook in the store. If that cache is cleared it degrades gracefully to exact-content dedup — still idempotent, it just can't drop a since-changed entry until the next clean push.
  - **Never automatic.** Hook commands run arbitrary shell and a store may be a clone of someone else's repo, so friday shows the exact commands and prompts before writing — `friday push` and `friday setup` on stdin, the control room's sync flow via a confirmation modal. `--no-interactive` (no confirmer) skips wiring; `--force` bypasses the prompt.
  - **Project scope too.** `friday setup` wires hooks into the repo's `.claude/settings.json`, copying the hook scripts in-repo and referencing them via `${CLAUDE_PROJECT_DIR}` so a teammate who clones the repo gets working hooks.
- **`friday doctor` reports hooks-wiring state** — whether `settings.json` carries the store's hooks and is current — replacing the old "wire it by hand" hint, and warns when a hook command references an unquoted path (`$HOME`, `${CLAUDE_PROJECT_DIR}`, …) that would break on a path containing a space.

## [0.5.0] — 2026-07-09

**The control room.** Running bare `friday` on a real terminal now opens a full-screen interactive TUI — pick and drive the everyday commands without remembering flags. It's a new frontend over the same engine and verbs: no new commands, no daemon, nothing running in the background. The moment you pass a flag or subcommand, or pipe/redirect the output, you get the exact one-shot CLI you had before — byte-for-byte, same exit codes.

### Added
- **Interactive control room** — bare `friday` on a real terminal launches a full-screen TUI built on the Charm stack (bubbletea / bubbles / lipgloss). The menu is the maintain-loop — **sync, setup, status, share, discover** — with `init` handled by cold-start:
  - **Cold start.** On a fresh machine (no `~/.friday` yet) the control room opens on a single prompt: paste a store repo to clone, or leave it blank to scaffold a fresh store — a UI over `friday init`. A failed clone or scaffold is cleaned up so a retry starts fresh.
  - **Sync.** Choose which installed agents to sync from a checklist, preview the capture + fan-out plan, then apply. Local edits are captured (pull) before the store fans out (push), grouped by direction in the preview.
  - **Setup.** Pick an agent, then a checklist of store knowledge (core + rules pre-checked on a fresh project, whatever is already applied on a re-run), preview, and apply into the project's own config.
  - **Status.** The pending-render plan — the store-side changes `sync`/`push` would apply — in a scrollable view, where a hand-edited target shows up as a conflict row. (The CLI's `friday status` additionally breaks out a separate local-drift column and install-state.)
  - **Share.** Propose your store changes for team review — enter a message, confirm the push to your remote, and friday opens the MR. It always confirms first (the one outward-facing action) and only appears when the store is git-backed.
  - **Discover.** Scan installed agents for files not yet in your store and multiselect which to import — a UI over `pull --discover`; each pick is scoped to its own agent.
  - **Interactive conflict resolution.** When an apply would clobber a hand edit, a modal shows the diff and offers keep / take / skip — for both push and pull. Ctrl-C cancels an in-flight apply: the engine stops before the next file and unwinds cleanly (writes are atomic, so nothing is ever half-written), then reports that it was cancelled.
  - **UX.** Preview-before-apply on every write; a positive `✓ applied` confirmation; a `?` help overlay listing every key; consistent keybindings (esc = back, q = quit, ctrl+c = cancel/quit); `d` toggles per-file diffs on the changes screen (windowed); long lists scroll and narrow terminals truncate cleanly; animated progress on slow operations.

### Changed
- **Bare `friday` on a real terminal now launches the control room** instead of printing usage and exiting 1 — the headline change in this release. There is no opt-out flag; `friday help` and `friday -h` still print usage. Any piped, redirected, CI, or `--no-interactive` invocation keeps the exact plain-text output and exit codes it had before, byte-for-byte, and every flag and subcommand is unchanged.
- **`friday status --origin` now lists each adapter's rule mappings** (`strategy: from → to`) in addition to where each adapter is defined — the per-adapter rule view that `friday list` used to provide.

### Removed
- **`friday list` (and its `ls` alias)** — redundant with `status` and `doctor`, which already show adapters, targets, and install state. Its one unique view (per-adapter rule mappings) moved into `friday status --origin`.

### Dependencies
- Added `github.com/charmbracelet/x/exp/teatest` (test-only) for golden-file tests of the TUI; this raised `golang.org/x/text` from 0.23 to 0.28.

## [0.3.2] — 2026-07-08

An output & experience polish pass — no new capabilities, just making what's there friendlier to read and less surprising to use.

### Changed
- **Change output collapses to folders + counts.** `push` / `pull` / `sync` / `setup` / `promote` no longer print a row per file. Each adapter folds into one line — a count plus a folder breakdown (`claude    5 updated  (CLAUDE.md, agents/×2, skills/×2)`) — files already in sync collapse to a single tally, and only conflicts and skips are named individually. The `status` grid gets the same treatment: hand-edits and conflicts stay per-file (they need a decision), while a large group of pending renders folds into one count line.
- **`--diff` is now windowed.** `status --diff` and `push --diff` show a few lines of context around each edit with elided regions marked `…`, a per-file cap with a `+N more` tail, and a `(+A −B, H hunks)` footer — instead of dumping the entire file. The interactive conflict resolver still shows the full diff, since resolving a conflict needs complete context.
- **`friday pull --force` is now `friday pull --all`.** On `pull`, the flag means "auto-apply every adapter" — a different meaning than `--force` ("overwrite on drift") carries everywhere else, which was a footgun. `--force` still works on `pull` but prints a deprecation note; use `--all`.
- **`friday init --from-git` is the documented clone flag.** `--remote` was an exact alias; it still works but prints a deprecation note.

### Fixed
- `friday doctor --json` and the `friday remote` subcommand flags (`-m` / `--message` / `--branch` / `--target`) now appear in `--help` and shell completion — they were defined but invisible.
- `friday list` no longer accepts the ignored `adapters` / `all` positional (dead back-compat); it takes no arguments.

## [0.3.1] — 2026-07-08

### Removed
- **The plugins feature** — `friday plugin` (`add`/`upgrade`/`remove`/`list`/`validate`), the `~/.friday/plugins/` overlay, the `friday.lock` pin file, and the plugin loader. friday now resolves adapters from the built-in presets plus your `friday.yaml` manifest only. Declarative-YAML plugins added a distribution and loader surface for little gain over editing `friday.yaml` directly, so removing them keeps the tool's moving parts minimal. (Unaffected: `${CLAUDE_PLUGIN_ROOT}` rewriting and support for Claude-Code-plugin-shaped knowledge repos like developer-os — that's a separate feature. `friday status --origin` now reports `friday.yaml` or `built-in`.)

## [0.3.0] — 2026-07-08

A UX + capability pass: a slimmer, more memorable command surface with an interactive terminal UI, a two-axis `status` that makes drift legible, a best-practice advisor in `doctor`, git-distributed plugins, and a pull data-loss fix.

### Added
- **Interactive TUI** (built on the Charm stack) for `setup` (agent picker + a checkbox list of knowledge, with sensible items pre-checked), `pull`, the drift/conflict resolver, and the `init` URL prompt; animated spinners on network git operations. It activates only on a real terminal — pipes, CI, `--no-interactive`, and tests keep the exact plain, line-based output as before.
- **Two-axis `friday status`** — a chezmoi-style two-column grid: column 1 flags a target you hand-edited since friday last wrote it (an edit to capture), column 2 flags a pending render (canonical differs from the target). Not-installed agents collapse to one summary line so the view stays scannable. `status --diff` prints the content diff for each pending render; `status --origin` shows where each adapter is defined (friday.yaml / built-in / plugin); `status --check` adds a terraform-style exit code (2 when anything is out of sync) for CI. The `--json` body and default exit code are unchanged.
- **Best-practice advisor** in `friday doctor` — static-config lint rules with severities (`error` fails the check, `warn` is advisory), stable grep-able rule ids, and a self-contained fix hint per finding. New rules: `long-instructions` (an entry/rule file over 200 lines) and `skill-description` (a skill missing a usable trigger description). Silence a rule per store in `.friday-doctor.yaml` (`disable: [...]`). `friday doctor --json` emits the findings for CI.
- **Git-distributed plugins** — `friday plugin add <name> <git-url>` fetches a declarative YAML preset from a repo (no code ever runs), pins the resolved commit in `plugins/friday.lock` for reproducible team renders, and installs it as `plugins/<name>.yaml`. `friday plugin upgrade [name|--all]` re-fetches and re-pins; `friday plugin remove <name>` uninstalls; `friday plugin list --urls` shows provenance.
- `friday share` — propose your store changes for team review (opens an MR); the everyday name for `remote propose`.
- `friday pull --discover` — walk an agent's dir and capture files a normal pull can't see, to bootstrap or enrich the store (replaces `friday import`).
- `friday doctor <file>` — explain which adapter + rule produces a file (replaces `friday explain`); `friday doctor` with no args now also runs the store checks that were `friday lint`.
- **Tiered help** — `friday help` lists the five everyday commands; `friday help --all` shows the full toolbox; `friday <command> --help` shows one command's flags. `rollback` gains the `undo` alias.

### Changed
- Command surface consolidated around a five-verb spine — `init`, `sync`, `setup`, `share`, `status` — with `push`/`pull` and the rest demoted to an advanced tier (all still dispatch and complete exactly as before).
- `friday doctor` no longer fails on advisory findings — only error-severity store problems make it exit non-zero; best-practice warnings are informational.
- Colors are centralized on a single theme; non-color output is unchanged byte-for-byte.

### Removed
- `friday import`, `friday explain`, and `friday lint` as standalone commands — folded into `friday pull --discover` and `friday doctor` (capability unchanged).
- `friday compile` — removed as too niche; convert by pushing into the store and out to the other agent.

### Fixed
- **Pull rendered a just-captured edit as a spurious removal** on a second agent that maps the same store file (and `sync --force` / `pull --no-interactive --force` could silently revert it). Pull now recognizes a store file another agent updated earlier in the same run — reporting the trailing agent in-sync when it's merely behind, or a conflict when it carries its own divergent edit — instead of overwriting the fresh store content.

## [0.2.1] — 2026-07-06

Hardening release: an adversarial review of v0.1.0/v0.2.0 surfaced 21 confirmed defects — most of them silent-data-loss paths in the drift/baseline machinery — all fixed here with regression tests.

### Fixed
- **Pull conflict prompt acted inverted.** The menu was hardcoded for the push direction, so on pull "[k] keep canonical" wrote the incoming target over the store and "[t] use target" kept the store. The menu is now worded per direction (`[k] keep target   [t] use store` on pull).
- **Pull could overwrite a newer store with stale target content** (e.g. after editing `~/.friday` and running a project-scope `friday setup`, or pushing to one agent then pulling another). Pull now checks the target's own baseline first: a target unchanged since friday last wrote it has nothing to capture and is reported in-sync — even under `--force`.
- **A push-direction 3-way merge was silently reverted by the next push.** Merges on copy rules are now written back to the store file too, so both sides converge; on concatenate/frontmatter-strip rules the old baseline is kept so the next push re-prompts instead of reverting, plus a warning.
- **`friday compile` disarmed drift protection.** Its import phase recorded the from-adapter's current target files (hand edits included) as baselines in the real drift state — even under `--dry-run` — so the next push overwrote hand edits without prompting. Compile now runs entirely on a throwaway drift state.
- **`friday import` / `friday eject` corrupted stores containing a replace value naturally.** Import compared in store-space, so a literal `~/.friday` in store prose read as a phantom edit that a forced import rewrote to `${CLAUDE_PLUGIN_ROOT}`. Import now compares in target-space, like pull.
- **Pull updates no longer rewrite natural replace-value occurrences.** The textual inverse now applies only to lines the target actually changed; unchanged lines keep their original store form (LCS alignment against the pushed rendering).
- **`friday pull --force` / `friday sync --force` still prompted interactively** — the force flag was never forwarded to the apply-phase engine call. In scripts (piped stdin) every conflict was silently skipped while the run claimed success.
- **In-sync stores never acquired baselines**, so a store upgraded from v0.0.4 hit an unresolvable conflict on every non-interactive pull after the first target edit. In-sync runs now record missing baselines (steady-state runs still skip the state write).
- **The legacy `identity.md` beat `core.md`** when a store carried both: literal-template copy rules planned every variant and the last write won. The from-list is most-preferred first; the first variant now wins, on push and on pull.
- **`friday promote <path> --propose -m "..."` (the documented invocation) silently skipped the MR** — stdlib flag parsing stops at the first positional, turning trailing flags into path filters. All commands taking positionals (promote, push, pull, sync, import) now parse flags anywhere on the line (`--` still terminates flags).
- **The `max_bytes` warning was lost whenever the oversized file had also drifted** — conflict resolution overwrote the same field. Warnings now ride a dedicated field, survive resolution, and appear in `--json` output.
- **Plugin presets were invisible to `friday setup` / `friday promote`**, and a plugin's `project_target`/`project_rules` were parsed but unreachable. Both commands now resolve plugins exactly like push/pull (plugins may shadow built-ins).
- **A typo'd from-pattern was silently absorbed** when any sibling pattern matched. Tokenized templates now report `missing-source` per pattern; literal templates (alternative-spelling lists) still report per rule.
- **Import inverted every file under the first from-pattern's anchor** and imported files no pattern accepts, creating store orphans push never consumes. Inversion now runs per pattern, and unmapped files are reported as skipped.
- Plugin validation errors on `project_rules` were labeled with a merged `rule[N]` index pointing at a nonexistent entry; they now read `project_rule[N]`.

### Changed
- `friday rollback` now covers every write-capable command — pull, sync, setup, promote, import, and compile record snapshots, not just push.
- `friday pull <adapter>` with closed/piped stdin exits 2 with a hint (`--no-interactive` / `--force`) instead of exiting 0 having applied nothing.
- `friday compile` prompts (or requires `--force`) when overwriting existing files in the to-adapter's target, and compiled output deliberately carries no baselines so a later real `friday push` prompts instead of silently clobbering it.
- Internal: the entry-file variant list is defined once (`presets.EntryFiles`) and consumed by setup and doctor; the `${CLAUDE_PLUGIN_ROOT}` rewrite is stamped onto every built-in rule centrally instead of repeated per rule.

## [0.2.0] — 2026-07-06

The capability-matrix release: every store directory maps into every agent that has a documented place for it.

### Added
- `max_bytes` rule field — flags concatenated outputs larger than the agent can consume; the warning rides every push/status line. The windsurf preset sets it to Windsurf's documented 6000-char `global_rules.md` cap.
- **Full capability-matrix presets** — every store directory now maps into every agent with a documented place for it (all paths re-verified against current harness docs): codex gains `skills/` (Agent Skills standard) and `prompts/` (from `commands/`); copilot gains `skills/`, custom agents as `agents/*.agent.md`, and project-scope `.github/skills/` + `.github/agents/`; opencode gains `commands/` and adapted `agents/`; windsurf gains `global_workflows/` (target moved from `~/.codeium/windsurf/memories` up to `~/.codeium/windsurf`); antigravity gains `antigravity/global_workflows/`; pi gains `prompts/`. `standards/` and `connectors/` land as reference copies in every agent's config home; claude re-gains the `hooks/` reference copy. Agent frontmatter is adapted per dialect (opencode/copilot strip Claude's `tools` comma-list and `model` sentinels; copilot keeps `name`, opencode derives it from the filename).
- `friday setup` / `friday promote` catalogs now include `standards`, `connectors`, and `hooks` as selectable items.

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

[0.5.0]: https://github.com/zhivko-kocev/friday/compare/v0.3.2...v0.5.0
[0.3.2]: https://github.com/zhivko-kocev/friday/compare/v0.3.1...v0.3.2
[0.3.1]: https://github.com/zhivko-kocev/friday/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/zhivko-kocev/friday/compare/v0.2.1...v0.3.0
[0.2.1]: https://github.com/zhivko-kocev/friday/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/zhivko-kocev/friday/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/zhivko-kocev/friday/compare/v0.0.4...v0.1.0
[0.0.4]: https://github.com/zhivko-kocev/friday/compare/v0.0.3...v0.0.4
[0.0.3]: https://github.com/zhivko-kocev/friday/compare/v0.0.2...v0.0.3
[0.0.2]: https://github.com/zhivko-kocev/friday/compare/v0.0.1...v0.0.2
[0.0.1]: https://github.com/zhivko-kocev/friday/releases/tag/v0.0.1
