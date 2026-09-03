# Changelog

All notable changes to friday are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- **`friday sessions --project` matched nothing off Windows.** `normalizeProject` folded separators with `filepath.ToSlash`, which is a no-op on Linux and macOS, so a session whose project path was recorded on Windows (`D:\repo\MyProject`) could not be reached by any filter spelling from another machine. The store is a dotfile meant to sync across machines, so this is the ordinary cross-machine case rather than an edge one. The backslash rewrite is now unconditional.
- **The gemini reader reported a lowercased project path on macOS.** `normalizeProjectPath` leaned on `filepath.EvalSymlinks` to recover on-disk casing, which it does on Windows but not on Darwin: gemini-cli lowercases the whole path before writing its `.project_root` marker, and on a case-insensitive APFS volume that lowercased path resolves and comes back exactly as handed in. One directory then wore two spellings in the `Project` column, and `--project` (a case-sensitive `Contains`) silently omitted the gemini rows. Casing is now recovered by walking the path against the filesystem's own directory entries, with no per-OS branch to drift, and an exact match always beating a case-insensitive one.
- **`InstallMethod` did not recognise a scoop path evaluated off Windows** — the same `filepath.ToSlash` no-op. Latent rather than user-facing: the only path it is asked about in production is the running binary's own, so a Windows-shaped path never reaches it on another OS. Corrected for the same reason as the other two.

### Changed

- `actions/setup-go` v6 → v7 in both workflows.
- `github.com/mattn/go-isatty` 0.0.20 → 0.0.24 and `github.com/charmbracelet/x/ansi` 0.11.6 → 0.11.8, pulling `go-runewidth` 0.0.19 → 0.0.24, `displaywidth` 0.9.0 → 0.11.0, `uax29/v2` 2.5.0 → 2.7.0 and `go-colorful` 1.3.0 → 1.4.0.

## [1.0.0] — 2026-09-03

**friday renders an agent's whole configuration, not just its Markdown knowledge.** Three canonical, adapter-agnostic YAML schemas — `mcp.yaml`, `permissions.yaml`, `defaults.yaml` — project onto each agent's own native JSON or TOML shape through a new `render` strategy, and two passthrough directories cover whatever a schema doesn't model. The interactive control room v0.5.0 introduced is deleted: friday is a one-shot CLI again, rebuilt on `urfave/cli/v3`.

### Breaking

Every manifest-level break below is handled by **`friday migrate`** (`--dry-run` to preview; idempotent), which renames legacy adapter keys, refreshes those adapters' target and rules from the current preset, and drops rules naming a retired strategy — preserving your comments, key order, and every other adapter. Nothing bricks in the meantime: a manifest written by an older release now loads with a warning per problem (printed once per run, on stderr) instead of failing every command.

- **The `antigravity` preset is now `gemini`.** The registry key changed, and the old preset's `md-to-json` subagent rule names a strategy that no longer exists. On v0.5.0 this combination made every command exit 1; now the rule is dropped with a warning and the adapter is reported as renamed. Migration: `friday migrate` renames the key and re-seeds the adapter from the `gemini` preset (`~/.gemini/GEMINI.md`, `settings.json` hooks, `mcp.yaml`/`permissions.yaml`/`defaults.yaml`, plus a `gemini-command` surface for `commands/*.md`).
- **The `pi` preset is now `oh-my-pi`, and its target moved from `~/.pi/agent` to `~/.omp/agent`** (relocatable via oh-my-pi's own `PI_CODING_AGENT_DIR`). This is the quieter half of the rename and the more dangerous one: the old `pi` rules were plain `copy`/`concatenate`, so a v0.5.0 manifest keeps parsing and keeps writing to the dead pi-mono path, while `friday sessions --agent oh-my-pi`, `friday transfer`, and every `oh-my-pi` rule stay unreachable. friday now warns on the stale key on every run. Migration: `friday migrate`. Files already written under `~/.pi/agent` are left alone — remove them by hand once you have confirmed the new target.
- **The `md-to-toml` and `md-to-json` rule strategies are retired.** They are replaced by `strategy: render` with `surface: codex-agent` (Codex subagent TOML) and `surface: gemini-command` (Gemini command TOML). A rule naming either is dropped at load time with a warning that names the adapter, the rule index, and the replacement surface. Migration: `friday migrate`, or hand-edit the rule to the `render` form.
- **Every `standards/*.md` and `connectors/*.md` rule is gone from the built-in presets.** Those directories were being copied as inert reference trees into six agent config homes, which no agent discovers or reads. The store directories themselves are untouched and still reachable by reference from your prose. If you were relying on the copies: an existing `friday.yaml` keeps its own `standards`/`connectors` copy rules (they are ordinary `copy` rules and still valid), so nothing changes until you re-run `friday init`; a store with no manifest falls back to the presets and stops writing them immediately. To keep them, declare the rule in `friday.yaml` yourself or mirror the files through `native/<adapter>/`. `friday migrate` does not touch these rules.
- **The interactive control room is deleted** — the whole `internal/ui/tui` package and its `bubbletea` frontend. Bare `friday` prints usage plus a state-aware next-step hint and exits 1, whether stdout is a terminal or a pipe. There is no replacement and no flag to bring it back; every capability it exposed was already a command (`sync`, `setup`, `status`, `share`, `pull --discover`). Migration: use the commands directly.
- **Minimum Go is 1.26** (was 1.24.2). A `go 1.26` directive in `go.mod` is a hard floor — an older toolchain cannot build friday from source. Migration: upgrade the toolchain, or install a prebuilt release asset.
- **Shell-completion scripts must be regenerated.** A script installed from v0.5.0 delegates to a hidden `friday __complete` command that no longer exists, so completion silently produces nothing. Completion is now `urfave/cli/v3`'s own mechanism (`--generate-shell-completion`). Migration: re-run `friday completion bash|zsh|fish` and reinstall the output.

### Added

- **Canonical config schemas.** Three adapter-agnostic YAML files in the store, each projected onto every adapter that has a place for it — friday's own schema in, the agent's native shape out, never a literal copy:
  - **`mcp.yaml`** — Model Context Protocol servers (stdio/http/sse, `env`, per-server `agents:` scoping). Renders to `~/.claude.json` (claude), `config.toml` (codex), `mcp-config.json` (copilot), `opencode.json` (opencode), `settings.json` (gemini), `mcp.json` (oh-my-pi).
  - **`permissions.yaml`** — a five-axis (read/write/exec/net/mcp) allow/ask/deny policy, resolved deny → ask → allow, first match wins. Renders to `settings.json` (claude, copilot), `config.toml` (codex), `opencode.json` (opencode), and `policies/friday.toml` (gemini, whose `policies/` dir is a set of independent files gemini-cli combines itself, so friday owns its own file outright instead of merging into yours). No oh-my-pi rule: its policy lives in `config.yml`, and friday has no YAML co-owned-merge strategy yet.
  - **`defaults.yaml`** — model, reasoning effort, theme, statusline, telemetry opt-out. Renders to `settings.json` (claude, copilot, gemini), `config.toml` (codex), `opencode.json` + `tui.json` (opencode). Same oh-my-pi gap.
- **New `render` rule strategy** — `surface:` selects the schema or transform (`mcp` | `permissions` | `codex-agent` | `gemini-command` | `defaults`) and `merge:` selects how the projection lands (`copy` | `merge-json` | `merge-toml`). A reasoning-effort value outside an adapter's own enum is clamped into range and reported rather than written as a token the agent would reject; anything friday cannot map onto an adapter is named in the push output instead of dropped silently.
- **Two passthrough directories** for what the schemas deliberately don't canonicalize. `native/<adapter>/**/*` mirrors a file verbatim into that adapter's config dir — the escape hatch for a surface friday doesn't model. `settings/<adapter>.{json,toml}` merges arbitrary extra keys into the same native settings file the schemas write, so an unmodelled key composes instead of replacing the whole file. Every `native/` mirror is confirm-gated: a mirrored file lands in a directory the agent auto-executes from, so friday cannot tell hook wiring from a theme palette and treats the whole tree as hook-bearing.
- **New `merge-toml` rule strategy** — merge-json's TOML analog. Splices the leaves friday owns into a co-owned TOML file (Codex's `config.toml`), leaving unmanaged tables and keys in place, and unions nested keys rather than claiming a whole table.
- **Codex subagent conversion.** The `codex-agent` render surface projects Claude-shaped `agents/*.md` into Codex's own TOML subagent format (`~/.codex/agents/<name>.toml`) — `name`/`description`/`developer_instructions` from the frontmatter and body. Codex requires all three, so `description` is always emitted. Like `concatenate`, a render rule does not pull back.
- **`gemini` preset**, replacing `antigravity`: `~/.gemini/GEMINI.md`, hooks merged into `settings.json`'s own `hooks` key, all three canonical schemas (`permissions.yaml` lands as `policies/friday.toml`), and a `gemini-command` surface projecting `commands/*.md` into Gemini's TOML command format.
- **Shared `agents` adapter** (`~/.agents`) — codex, copilot, gemini, and opencode all discover Agent-Skills-standard skills from one `~/.agents/skills` tree instead of four duplicate copies. It counts as installed when any of those four is. claude and oh-my-pi keep their own native `skills/` rules, which outrank the shared path in their own discovery order.
- **Multi-root stores.** `sources:` in `friday.yaml` composes additional git remotes (cloned under `.sources/<name>`) beneath the primary store, lowest precedence first — a shared org-wide rule pack layered under your personal store, expanded through the same glob engine. `friday source add|list|update|rm` manages them; `status --origin` and `doctor` both report which root a file came from. A configured source is read-only: friday never writes a capture into `.sources/`.
- **`friday sessions`** — a read-only, cross-agent session index over Claude Code, Codex, Copilot, Gemini CLI, OpenCode, and oh-my-pi's own on-disk transcript stores (`--agent`, `--project`, `--since`, `--json`, `--stats` rollups by agent/model/period). `friday session show <id>` prints a normalized transcript; `friday session export <id>` writes a deterministic markdown handoff brief. friday never writes into any agent's session store — every reader only reads what that agent already produced.
- **`friday claim`** — capture one or more agents' existing on-disk config into the store in one step (init's per-adapter discovery, promoted to a first-class command).
- **`friday transfer <from> <to>`** — move one agent's config to another *through* the store: capture the source agent's config in, push it back out under the destination agent's rules.
- **`friday upgrade`** — chezmoi-style self-update. Detects the install method (brew/scoop print their own upgrade command instead of downloading), otherwise downloads the matching release asset and verifies its published sha256 before any write. Explicit invocation only — no background version checks.
- **`friday migrate`** — rewrites a `friday.yaml` written by an older release. See Breaking.
- **`friday doctor` delegates content linting to `agnix`.** friday's own checks stay strictly render-graph (destination collisions, drift, hooks-wiring staleness, source-shadow, source-manifest, `mcp-secret`, store structure); when an [`agnix`](https://github.com/agent-sh/agnix) binary is on PATH, `doctor` runs it over every source root and folds its findings in under an `agnix/`-prefixed rule id and an `agnix` origin — otherwise it prints one line naming where to get it. An unusable agnix response is never a reason to fail `doctor`; it degrades to the same hint.
- **`mcp-secret` doctor check** — flags a literal credential in a store config file instead of a required `${VAR}` reference. Error severity, so it fails `doctor` and `doctor --json` in CI. friday resolves no variable references, so a `${VAR}` reaches the agent's config verbatim, which is the form all six document.
- **Hooks for every agent, confirm-first.** An agent activates only the hooks its own config declares, so friday merges the store's hook sources into that config file instead of dropping an inert copy. The four agents with a declarative hook file — claude (`~/.claude/settings.json`), codex (`~/.codex/hooks.json`), copilot (`~/.copilot/hooks/friday-git-guard.json`), gemini (`~/.gemini/settings.json`'s `hooks` key) — are merged in; each has a native-dialect source in the store and all invoke one shared guard whose deny signal is selected per agent (`exit 2` for codex; stdout JSON for copilot and gemini, whose event names and deny shapes differ from claude's). opencode and oh-my-pi expose hooks only as imperative TypeScript, so friday drops a small guard plugin/extension that shells out to the same script and blocks by throwing / returning `{block:true}`. The hook scripts run from the store in place. `friday setup` wires project-scope hooks into the repo's `.claude/settings.json`, copying the scripts in-repo and referencing them via `${CLAUDE_PROJECT_DIR}`. Hook commands run arbitrary shell and a store may be a clone of someone else's repo, so friday shows the exact commands and prompts before writing: a nil confirmer (`--no-interactive`) skips the write, `--force` bypasses the prompt.
- **Local ownership tracking.** friday records the entries and leaves it wrote in a machine-local cache beside the drift store (never synced) so a later push can retract its own now-stale entry after you change the source. Cleared cache degrades gracefully to exact-content dedup — still idempotent, it just cannot retract a since-changed entry until the next clean push.
- **`friday doctor` reports hooks-wiring state** — whether the agent's config carries the store's hooks and is current — replacing the old "wire it by hand" hint, and warns when a hook command references an unquoted path (`$HOME`, `${CLAUDE_PROJECT_DIR}`, …) that would break on a path containing a space. The check parses the command with a real shell parser rather than clearing on any unrelated quote.
- **`friday doctor --json`** now carries `adapters`, `agnix`, `drift`, `findings`, `summary`, and `warnings`.
- **Project scope for custom adapters.** A `friday.yaml` adapter can carry `project_target`/`project_rules` alongside its user-scope `target`/`rules`, so `friday setup` and `friday promote` work for an adapter you defined yourself, not only the built-ins.
- **DX baseline.** `FRIDAY_HOME` overrides the store root (following `RUSTUP_HOME`/`CARGO_HOME` — the value is the store dir itself); `--quiet` suppresses all non-error output without changing exit codes; `--verbose` shows the resolved source behind every rendered file; `FORCE_COLOR` forces color even off a terminal, alongside the existing `NO_COLOR`/`FRIDAY_NO_COLOR`; and the `status`/`sessions`/`doctor` tables compute column widths from the widest cell actually present instead of a hardcoded printf width.

### Changed

- **Command framework migrated to `urfave/cli/v3`.** The hand-rolled dispatcher, flag parser, and completion generator are gone; every command, alias, flag name, and exit code stayed identical. Gained: real shell completion with dynamic adapter candidates, and a two-tier `help`/`help --all` split (the everyday seven versus the full toolbox).
- **Bare `friday` distinguishes drift from pending work.** It filters its dry run to installed adapters, and reports an unclearable conflict — "Drift detected", pointing at `friday status`, then `friday sync` per file or `friday push --force` — separately from ordinary pending renders. It used to print "Pending changes" forever on a store with a conflict.
- **`merge-toml` is not byte-for-byte outside the leaves friday owns.** Untouched tables and keys survive, but the file is re-emitted through go-toml's encoder: quoting can normalize (`"dark"` becomes `'dark'`), an owned entry can move, and comments attached directly above a retracted leaf go with it. `merge-json` does round-trip untouched input exactly, comments included.
- **Which rules pull back, spelled out.** Only `render` with `surface: mcp`, and `surface: permissions` landed as `merge-json`/`merge-toml`, have a real pull inverse — a hand edit to those targets is captured back into `mcp.yaml`/`permissions.yaml`. Where a safe destination exists (an unrecognized MCP field, or a claude permission that is none of the five shapes friday renders) the leftover is folded into that adapter's `settings/<adapter>.json` fragment so a later push re-contributes it losslessly; where one does not, it is named in the output rather than dropped or guessed at. Everything else is push-only and reports the edit instead: `concatenate`, `frontmatter_strip`, plain `merge-json`/`merge-toml`, and `render` with `surface: defaults`, `codex-agent`, `gemini-command`, or `permissions` merged as `copy`.
- **A configured source is read-only.** A pull whose canonical file is owned by a `.sources/<name>` checkout is refused with a message naming both escape routes — change it in that source's own repository, or copy it into the primary store, which outranks every configured source. Writing there would be discarded by the next `friday source update`, which needs a clean tree to fast-forward.
- **`native/<adapter>/**/*` is an escape hatch, not a net.** On import (`friday claim`, `pull --discover`) the mirror no longer captures a file an earlier, more specific rule already models, and never captures the target of a rule that cannot be imported (concatenate's `CLAUDE.md`, the composed `settings.json`). A whole-file mirror of a composed target would discard the merge composition, friday's own confirmed hook wiring included.
- **A disabled MCP server is written with the adapter's own off switch** rather than omitted, on every adapter that documents one. Omission left removal to the merge step's retraction, which cannot retract a scalar leaf — leaving a half-entry the agent still loads (a bare `npx` with no args). Where no native flag exists the entry is omitted *and reported*, so the user learns the disable did not reach that agent.
- **`friday source add`/`rm` edit `friday.yaml` through a YAML node editor**, so unrelated comments and key order survive the write.
- **`friday init --scaffold` is genuinely non-interactive** — it used to print the remote-URL prompt anyway.
- **`doctor`'s `dest-collision` check is merge-aware.** Two rules that each splice their own keys into one file compose; they are no longer reported as an error. `doctor` exits 0 on a healthy default store.
- **`doctor`'s `source-shadow` check is derived from the real dry-run plan**, so the entry-file variant exception and files no rule references are no longer falsely flagged.
- **A manifest naming an adapter friday has no preset for now warns** and points at the current spelling, or at `friday migrate` when the name is a known rename. A hand-written adapter is still a supported thing to have — silence was the problem.
- **`friday status --origin` reports the source root** behind each adapter alongside its rule mappings.

### Removed

- **The interactive control room.** See Breaking.
- **`antigravity` preset** — replaced by `gemini`. See Breaking.
- **`md-to-toml` / `md-to-json` strategies** — replaced by `render`. See Breaking.
- **`standards/*.md` and `connectors/*.md` preset rules.** See Breaking.
- **`windsurf` preset.** Cognition is folding Windsurf/Cascade into Devin Desktop (`docs.windsurf.com` now 307-redirects to `docs.devin.ai`) and the legacy Cascade line reached end-of-life on 2026-07-01, so its config paths are a moving target. friday drops the preset rather than track a renaming product; it can return as a `devin` preset once Devin Desktop's config surface settles. Existing `~/.codeium/windsurf` targets stop syncing — re-add a hand-written adapter in `friday.yaml` if you still need them.

### Fixed

- **`friday setup` and `friday promote` were dead for anyone with a manifest.** Both resolved project scope from `friday.yaml` only, and `friday init` seeds user-scope rules only — so every store scaffolded by `init` had no project mapping and both commands failed. They now fall back to the built-in preset's project rules when the manifest adapter carries none. Project-scope destinations are also clamped to the project directory.
- **Claude permission rendering widened a deny.** A canonical `deny`/`ask` now renders in Claude's command-*prefix* form `Bash(cmd:*)` so it actually blocks argument variants; a canonical `allow` keeps the *exact* form `Bash(cmd)` so an allow is never widened into permission the user never granted. The pull inverse mirrors the asymmetry: a `Bash(cmd)` found under deny/ask is not friday's shape and is preserved verbatim instead of being recovered and re-widened.
- **A deny-only network policy turned Codex's network access on.** `network.enabled` is the master switch for command network access, so it now derives from the allow tier alone, and the resulting fail-closed narrowing is reported.
- **Gemini permission rendering emitted constructs Gemini cannot evaluate.** `commandPrefix` has no glob dialect, so a pattern carrying `*` or `?` matched only those characters and the rule never fired; such patterns now route through an `argsPattern` regex. `mcpName` receives the bare server name, and a non-`*` tool component is reported as unmapped rather than silently mangled.
- **Codex's `model_reasoning_effort` could receive a value outside its own enum.** It is clamped to `minimal..xhigh` (Claude and Copilot to `low..xhigh`) and the clamp is reported. Statusline `refreshInterval`/`padding` are omitted when unset — both adapters document a minimum of 1, and a written zero costs the whole `statusLine` object.
- **A commented JSONC target no longer round-trips as malformed.** Pull-side and lint-side JSON both parse through `hujson`, the same way the merge path already read them, so a Copilot-style commented `settings.json` is read rather than written off.
- **`friday doctor`'s unquoted-expansion check** no longer clears on any unrelated quote elsewhere in the command.
- **agnix delegation is bounded and validated.** It runs under `exec.CommandContext` with a timeout, caps buffered stdout, passes `--` so a store dir starting with a dash is never read as a flag, and validates the reported SARIF version — non-2.x output is reported as failed rather than read as a clean bill of health.
- **Session readers no longer claim availability they don't have.** `Available()` probes each agent's session-state directory (`~/.copilot/session-state`, `~/.gemini/tmp`, …) rather than friday's own push target — pushing config into `~/.copilot` used to make the reader report available and then find zero sessions. Empty stdout from `opencode session list` means zero sessions, not a parse error. An oversized JSONL line yields a warned partial record instead of a silently truncated one.
- **`friday upgrade` on Windows** restores the renamed-aside binary if the replace fails, preflights install-dir writability before downloading, and tidies a leftover `.old` from a previous upgrade on every invocation.
- **A stale owned-state record could delete another rule's live wiring.** Four merge-json rules share claude's `settings.json` and three merge-toml rules share codex's `config.toml`, but a pre-v1.0 ownership record did not name the rule that wrote it — so on the first push after upgrading, every one of those rules claimed the same record and retracted whatever still matched it. Merge writes are drift-exempt, so nothing prompted first. A record that cannot be attributed to one rule is now retired instead of read; the cost is one missed cleanup on the upgrade push, which the next push completes.
- **A merge source could write a literal `null` into a co-owned file.** An explicit `null` — the ordinary way a hand-authored `hooks.json` says "unset this" — was written straight through as `"key": null`, or left an empty `{}` shell once friday's own entries under it were stripped. It now drops the key, keeping any entries the user has at the same position.
- **A merge into a CRLF-authored file mixed line endings.** The splice preserves untouched bytes exactly, but the fragment it inserts came from an LF-only encoder, so patching a Windows-authored `settings.json` left one file carrying both conventions. The target's own convention is restored.
- **`friday eject <agent>` ejected everything.** The command accepted and silently discarded an agent name while removing `friday.yaml`, the drift cache, and every rollback snapshot for the whole install — with `--yes`, unprompted. It is deliberately all-or-nothing, so it now rejects the argument and says so; to stop managing one agent, delete its entry from `friday.yaml`.
- **`friday doctor` printed its manifest diagnostic to stdout, and `--quiet` silenced it.** The one command whose job is explaining a degraded manifest was the one that hid it. Warnings now go through the same stderr path every other command uses.
- **`--quiet` did not cover the bare-invocation banner or the deprecated-flag nudge.** Both print outside the usual output helpers — the nudge before flag parsing had even happened — so a scripted `friday --quiet` still wrote to stdout. Exit codes were and remain unaffected.
- **`--verbose` could not name the winning root for most files.** It tagged a source with `@root` only when one change carried two of them, which only a concatenate entry file ever does — so merge-json, merge-toml, copy, and render files read exactly like a single-source store. The engine now records the winning root per change and `--verbose` renders it.
- **A shadowed source was reported once and then never again.** The advisory was surfaced only for a create or update, so it vanished the moment the write stabilized — which is every push after the first, while the shadowing persists.
- **`friday status` disagreed with `push` about the shared `agents` adapter.** status used a bare directory check, so it showed `[missing]` and folded pending files under "`friday sync` sets it up" while sync was already writing there.
- **`friday setup` and `friday promote` ignored `sources:`.** Project scope built its config without them, so source-only content was invisible to setup's picker and the refusal to capture into a read-only `.sources/` checkout could never fire — promote silently duplicated the file into the primary store instead.
- **`friday claim` could not capture native MCP servers or permission policy.** Every render rule was reported unsupported, though `friday pull` already inverts the `mcp` and co-owned `permissions` surfaces from the identical rule shape. The genuinely one-way surfaces stay refused, each naming its own obstacle.
- **A push with an unfetched source wrote a partial store.** A source with no directory on disk contributes no matches, which is indistinguishable from an empty one — and `.sources/` is gitignored, so that is the default state on a second machine. Writes now refuse and name `friday source update`; dry runs still plan.
- **Two roots' copies of one file could collide silently.** The cross-root union compared paths case-sensitively, so `rules/Team.md` and `rules/team.md` were treated as two files on the filesystems most developers use, and the second write overwrote the first with nothing reported as shadowed.
- **`rules.Matches` and `Expand` disagreed on a dot segment after a wildcard.** A pattern like `**/.hidden/*.md` matched a path that the store walk would never find again, so `pull --discover` and `claim` could capture content a later push silently orphaned.
- **An adapter whose every rule was retired went inert in silence.** Each dropped rule was reported, but not that the adapter now syncs nothing at all.
- **A reserved Windows device name passed source validation.** `sources: [{name: nul}]` validated, then could never be materialized — `mkdir` reports success and creates nothing — leaving a source permanently indistinguishable from an empty one.

### Security

- **Hook consent was keyed on the wrong thing.** The confirmer fired on a rule's `drift_exempt` flag rather than on whether the write installs executable hook content, so a `native/<adapter>/**/*` mirror could land `settings.json` with a `SessionStart` command — or `plugins/anything.ts` — with no prompt at all. Consent is now keyed on hook *content*: a merge whose store source lives under `hooks/`, and every `confirm_hook` copy including all six `native/` mirrors. A nil confirmer (`--no-interactive`) skips such a write; only `--force` bypasses the prompt.
- **Merge retraction destroyed unmanaged keys.** Retraction deleted by path membership, so a payload that stopped defining a table took the user's own sibling keys with it. Retraction is now content-based on both sides: a key, leaf, or entry friday previously wrote is removed only when the target still holds exactly what friday recorded writing there. Anything hand-edited since is the user's and stays.
- **`friday session export` clobbered files.** It overwrote an existing output path without asking; a brief embeds raw transcript text, so the file it silently truncated could have been anything. It now refuses without `--force` (and refuses outright under `--no-interactive`), writes through the atomic-write path at `0o600`, and journals the previous content so `friday rollback` can restore it.
- **`friday source add` accepted a path-traversing name.** `friday source add ../../pwned <url>` exited 0 and cloned two levels above the store; `Team_Rules` wrote a manifest that friday could then no longer open, bricking every later command. The name is validated before anything is cloned or persisted, and `Config.Save()` validates too, so no writer can persist a name the loader rejects.
- **A pull could write into a read-only source checkout,** producing an edit the next `friday source update` would discard and `friday remote push` could never ship. Refused, with both escape routes named.
- **`friday upgrade` pinned to https and an official host.** Every release URL — metadata and asset alike — must be https and resolve to an allowlisted official host, checked before any bytes move and re-checked on every redirect, so a 302 out of a valid asset URL cannot walk the download elsewhere. The published sha256 is verified before any write. Note the limit: the checksums file ships from the same release as the asset, so this is integrity, not authenticity — a compromised release host defeats it.
- **A git URL could execute a command.** `--` stops git's flag parsing but not its transport layer, and `ext::<command>` runs its argument as the way it "connects". A `sources:` URL is untrusted input — a manifest can arrive from someone else's clone — and reached `git clone` with only a leading-dash check in front of it. `ValidateURL` now rejects the `transport::address` form and any scheme outside https/http/ssh/git/file, and every git invocation pins `GIT_ALLOW_PROTOCOL` to that same list. The environment variable is the load-bearing half: it overrides `protocol.*.allow` config, so a machine already exporting a relaxed list for unrelated tooling defeated the config form outright — measured, with the command going on to execute.
- **Hook consent could be bypassed through the settings passthrough.** Consent keyed on the rule's own source path, so a `"hooks"` key placed in `settings/<adapter>.json` instead of `hooks/` merged into the very same `settings.json` with no prompt at all, including under `--no-interactive`. It is now keyed on what the payload actually installs as well as where it came from.
- **`EditManifest` could persist a manifest the loader rejects.** `Config.Save()` documented the guarantee that no writer leaves a manifest friday cannot open, but the node-editing writer — the one `friday source add` actually uses — validated nothing. Both writers now validate, and `Save()` checks the adapter invariants it claimed to.

### Dependencies

- The yaml module moved from `gopkg.in/yaml.v3` to its maintained home `go.yaml.in/yaml/v3`.
- Added direct: `github.com/urfave/cli/v3` (the command surface), `github.com/pelletier/go-toml/v2` (merge-toml and the codex/gemini renders), `github.com/tailscale/hujson` (JSONC targets), `mvdan.cc/sh/v3` (real shell parsing in doctor's unquoted-expansion check), `github.com/muesli/termenv`.
- Dropped `github.com/charmbracelet/x/exp/teatest` (test-only), added in v0.5.0 for golden-file tests of the deleted TUI.

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

[Unreleased]: https://github.com/zhivko-kocev/friday/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/zhivko-kocev/friday/compare/v0.5.0...v1.0.0
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
