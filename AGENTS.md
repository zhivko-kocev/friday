# friday

One CLI to manage AI agent configs from a single canonical store. Push to every
agent, pull edits back, sync across machines via git. Store: `~/.friday/`. Six
built-in presets, with default agent targets: `claude` (`~/.claude`), `codex`
(`~/.codex`), `copilot` (`~/.copilot`), `opencode` (`~/.config/opencode`),
`antigravity` (`~/.gemini`), and `pi` (`~/.pi/agent`) — all configurable via
`friday.yaml`.

Bare `friday` on a real terminal opens a full-screen interactive control room
(TUI) over the same engine and verbs — a frontend, not new commands. Any
flag/subcommand, or any piped / CI / `--no-interactive` run, keeps the exact
byte-identical plain-text CLI.

Cursor is intentionally not a built-in preset: its global rules are stored in
Cursor's settings UI, not on the filesystem, so there's nothing for friday to
write at user scope. See README for details.

## Build & test

```bash
make build          # produces ./friday (Unix) or ./friday.exe (Windows)
make test           # go test ./...
make lint           # go vet ./...
make tidy           # go mod tidy
```

## Store layout

The user store lives at `$HOME/.friday` on every platform — same place,
same layout, sharable as a dotfile. The manifest at `$HOME/.friday/friday.yaml`
controls which adapter rules apply and where. The store dir IS that directory —
flat, no nested `store/`.

## Standard repo layout

```
core.md               the entry file (concatenated into CLAUDE.md / AGENTS.md / copilot-instructions.md);
                      also matched at core/core.md — legacy identity.md still accepted
rules/*.md            behaviour rules
standards/*.md        per-language baselines (not copied — reached via ~/.friday refs)
agents/*.md           agent/subagent definitions (claude, copilot, opencode; codex/antigravity native formats being wired)
commands/*.md         slash-commands / prompts / workflows (claude, codex, opencode, antigravity, pi)
skills/<name>/*       skills (recursively mirrored)
hooks/hooks.json      merged into ~/.claude/settings.json's `hooks` key (merge-json, confirm-first); scripts under hooks/ run from the store in place. Codex/Copilot/Antigravity hook surfaces being wired — see ROADMAP
friday.yaml           adapter manifest — auto-seeded with all built-in presets (six) at init
.gitignore            scaffolded with secret + runtime-state patterns
```

Presets copy only what agents discover on disk (instructions file, agents/,
commands/, skills/); other store content is reached by reference — each rule
rewrites `${CLAUDE_PLUGIN_ROOT}` to `~/.friday` via `replace`.

`friday init` always writes `friday.yaml` with every built-in preset on
the scaffold path. To disable an adapter, delete its entry. To customize a
target dir or rule, edit it. The presets only seed the manifest at init
time — they don't run again.

Empty subdirs are fine — globs that match nothing are reported as
`missing-source` and skipped. Dotfiles (`.gitkeep`, `.hidden.md`) are
filtered from `*` and `**` matches by convention.

## Package map

```
cmd/friday/           entry point — wires os.Args into cli.Run, sets version via ldflags
internal/cli/         command dispatcher — one cmdX function per subcommand
internal/config/      parses friday.yaml; LoadUser / NewDefault
internal/rules/       Rule type, FromSpec (string|[]string), glob expansion, token engine
internal/engine/      plans + applies push/pull; resolves drift via the conflict UI; atomic writes
internal/conflict/    interactive [k/t/d/s] prompt with line-LCS diff (LineDiff is reused by report)
internal/drift/       SHA256 store at $UserCacheDir/friday/state.json — flags external edits; owned.go tracks merge-json's own hook entries (hooks-owned.json)
internal/frontmatter/ parse/strip YAML frontmatter in .md files (CRLF-tolerant)
internal/git/         shells out to `git` for clone/pull/push/status
internal/presets/     built-in adapter rule sets (claude/codex/copilot/opencode/antigravity/pi)
internal/initcmd/     `friday init` — prompts for a URL, clones or scaffolds
internal/setupcmd/    `friday setup` / `promote` — apply store knowledge into a project's own config
internal/output/      all console output (colored, TTY-aware)
internal/lint/        store checks + best-practice advisor (backs `friday doctor`)
internal/snapshot/    content-addressed pre-write snapshots (backs `friday rollback`)
internal/atomicio/    WriteFile via temp + fsync + rename — used for every file write
internal/textnorm/    one home for CRLF→LF normalization (used by engine, drift, frontmatter)
internal/ui/          TTY detection + huh-backed prompts (the plain-path interactive bits)
internal/ui/theme/    the shared color theme
internal/ui/tui/      the control room — the full-screen bubbletea TUI bare `friday` launches
```

## Key design rules

**Single store, many agents.** `~/.friday` is the one source of truth. `friday push`
writes each adapter's rules into the agent's expected on-disk layout. `pull` is the
inverse where supported. `remote push` ships the store via git — think of it like a
package manager where your dotfiles are the package.

**One engine, one scope per run.** `engine.Push` / `engine.Pull` operate on a
`*config.Config` with a `TargetRoot` and `StoreDir` — one scope per invocation.
User scope (`$HOME`) is the default; project scope is live via `friday setup` /
`promote`, which point the engine at a project dir and write into the project's
own git-tracked config (presets carry `ProjectTarget` / `ProjectRules`).

**Push targets installed agents only.** Bare `friday push` filters to
adapters whose target dir already exists. Explicit `friday push <name>`
bypasses the filter — that's the bootstrap path for first-time agent setup.

**Pull is per-agent interactive by default.** Bare `friday pull` walks
each installed agent: plan → show diff → prompt apply / skip / quit.
Naming an adapter (`friday pull claude`) bypasses the loop and uses the
file-level conflict resolver. `--no-interactive` opts out entirely.

**Drift only applies to push.** On pull, every change is intentional. The
drift store at `$UserCacheDir/friday/state.json` is keyed by
`(adapter, absPath)` and refreshed on every successful push write.

**Concatenate is one-way.** Multi-source → single-target rules (`CLAUDE.md`,
`AGENTS.md`, `copilot-instructions.md`) are not pullable. Same for rules
with `frontmatter_strip` — pulling would re-introduce stripped fields.

**merge-json is push-only, drift-exempt, and confirm-first.** It deep-merges one
source JSON file into a co-owned target JSON file at the entry level (the `claude`
preset wires `hooks/hooks.json` into `settings.json`). Objects union their keys
and arrays keep the target's own elements while refreshing friday's — so your
`model`, unmanaged hook events, and hooks you added by hand all survive. friday
records the entries it wrote in a machine-local owned-state file (`hooks-owned.json`,
a sibling of the drift store) so a later push can drop its own now-stale entry
after a store edit; with that cache gone it degrades to exact-content dedup (still
idempotent, just can't remove a since-changed entry). It needs no whole-file drift
baseline and never conflicts. Because hook commands run arbitrary shell and a
store may be a clone, `friday push` and `friday setup` show the exact commands and
prompt before writing (the control room via a modal); `--no-interactive` (no
confirmer) skips it and `--force` bypasses the prompt. Not pullable.

## friday.yaml schema

```yaml
version: 1
adapters:
  <name>:
    target: <path>                         # see "Path resolution" below
    rules:
      - from: <pattern> | [<pattern>, ...]
        to: <template>
        strategy: copy | concatenate | merge-json  # default: copy
        separator: <string>                # concatenate-only; default "\n\n---\n\n"
        frontmatter_strip: [<key>, ...]    # strip listed YAML frontmatter keys
        replace: {<literal>: <literal>}    # rewrite on push, inverted on pull
        max_bytes: <int>                   # warn when the output exceeds the agent's limit
```

`replace` substitutes literal strings in file content: keys → values on push,
values → keys on pull. Presets use it to rewrite `${CLAUDE_PLUGIN_ROOT}` —
the path variable Claude Code plugins use for sibling-file references — to
`~/.friday`. The map must be invertible: no empty or duplicate values, no
key == value. Pull compares in target-space (target vs the store's forward
transform), so unedited files never phantom-update; the textual inverse only
runs on edited files. Choose replacement values that cannot occur naturally
in content (`~/.friday` is safe in friday-free repos; `~/.claude` is not),
or a natural occurrence inside an edited file will turn into the key.

### Path resolution

| Form         | Resolved to                                        |
|--------------|----------------------------------------------------|
| `/abs/path`  | as-is                                              |
| `~/foo`      | `$HOME/foo`                                        |
| `.claude`    | `$HOME/.claude`                                     |

### Tokens (only with `strategy: copy`)

| Token       | Value                                           |
|-------------|-------------------------------------------------|
| `{filename}`| basename with extension (`researcher.md`)       |
| `{stem}`    | basename without extension (`researcher`)       |
| `{ext}`     | extension with dot (`.md`)                      |
| `{relpath}` | path relative to the rule's anchor              |
| `{dir}`     | directory portion of `{relpath}` (`""` if root) |

The **anchor** is the longest literal directory prefix of the `from`
pattern. For `agents/*.md` it's `agents`; for `skills/**/*.md` it's
`skills`; for the literal `rules/general.md` it's `rules` (parent dir).

## CLI

Porcelain — the everyday five (`friday help`):

```
friday init                       clone or scaffold your ~/.friday store
friday setup                      add friday knowledge to the current project
friday sync                       capture local edits, then fan them to every agent
friday status                     show what would change (no writes)
friday share -m "msg"             propose your store changes for team review (opens an MR)
```

Advanced (`friday help --all`): `push` (store → installed agents), `pull`
(agent edits → store; `--discover` finds new files), `promote` (project config →
store), `rollback`/`undo`, `eject`, `remote` (git ops on the store:
`init`/`pull`/`push`/`propose`/`status`), `doctor` (health-check + store lint;
`doctor <file>` explains a mapping), `completion`, `version`, `help`.

`init` supports both interactive and non-interactive flows:
`friday init --scaffold` (empty scaffold) and `friday init --from-git URL`
(clone; `--remote` is a soft-deprecated alias). Stdin-piping still works
(`echo "URL" | friday init`) for tooling that prefers it. Refuses to overwrite a
non-empty `~/.friday` — remove it yourself to re-init.

`push` exits `2` if any change is `ActionConflict` (drift detected in CI, etc.).

## Adding a preset

1. Edit [internal/presets/presets.go](internal/presets/presets.go), add an
   entry to the `registry` map.
2. Existing users won't see the new preset until they re-init or hand-edit
   their `friday.yaml`. There's no auto-merge command — explicit beats clever.

## Limitations

- Concatenate, `frontmatter_strip`, and `merge-json` rules are not reversible (no pull).
- `merge-json` hook wiring is confirmed before every write — `friday push` and
  `friday setup` prompt on stdin, the control room via a modal. A nil confirmer
  (`--no-interactive`) skips wiring; `--force` bypasses the prompt.
- A plain `pull` only walks files the store already knows about. A file created
  directly in a target dir is caught by `friday pull --discover`, which walks the
  whole agent dir and captures new files into the store.
- `friday remote push` requires `git remote add origin ...` to have been
  run inside the user store at some point.
- No permissions translation across agents.
