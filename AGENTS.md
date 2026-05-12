# friday

One CLI to manage AI agent configs (Claude Code, Cursor, OpenCode, GitHub Copilot)
from a single canonical store. Push to every agent, pull edits back, sync across
machines via git. Store: `~/.friday/`. Default agent targets: `~/.claude`,
`~/.cursor`, `~/.config/opencode`, `~/.github` (all configurable via
`friday.yaml`).

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
identity.md           who you are (concatenated into CLAUDE.md / AGENTS.md / copilot-instructions.md)
rules/*.md            behaviour rules
agents/*.md           agent definitions (claude only)
commands/*.md         slash-commands (claude only)
skills/<name>/*       skills (recursively mirrored)
friday.yaml           adapter manifest — auto-seeded with all four presets at init
.gitignore            scaffolded with secret + runtime-state patterns
```

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
internal/drift/       SHA256 store at $UserCacheDir/friday/state.json — flags external edits
internal/frontmatter/ parse/strip YAML frontmatter in .md files (CRLF-tolerant)
internal/git/         shells out to `git` for clone/pull/push/status
internal/presets/     built-in adapter rule sets (claude/cursor/opencode/copilot)
internal/initcmd/     `friday init` — prompts for a URL, clones or scaffolds
internal/output/      all console output (colored, TTY-aware)
internal/atomicio/    WriteFile via temp + fsync + rename — used for every file write
internal/textnorm/    one home for CRLF→LF normalization (used by engine, drift, frontmatter)
```

## Key design rules

**Single store, many agents.** `~/.friday` is the one source of truth. `friday push`
writes each adapter's rules into the agent's expected on-disk layout. `pull` is the
inverse where supported. `remote push` ships the store via git — think of it like a
package manager where your dotfiles are the package.

**One engine, one scope.** `engine.Push` / `engine.Pull` operate on a
`*config.Config` with a `TargetRoot` and `StoreDir`. Currently user-scope only
(`$HOME`); project-scope is reserved for a future release.

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

## friday.yaml schema

```yaml
version: 1
adapters:
  <name>:
    target: <path>                         # see "Path resolution" below
    rules:
      - from: <pattern> | [<pattern>, ...]
        to: <template>
        strategy: copy | concatenate       # default: copy
        separator: <string>                # concatenate-only; default "\n\n---\n\n"
        frontmatter_strip: [<key>, ...]    # strip listed YAML frontmatter keys
```

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

```
friday init [flags]               prompt: blank → scaffold + git init; URL → git clone into ~/.friday
friday list [adapters]            adapters in friday.yaml + whether each is installed on this machine
friday push [adapters...]         write ~/.friday into installed agent dirs (no args = every installed)
friday pull [adapters...]         no args = per-agent diff + apply prompt; with args = legacy file-level
friday status [adapters...]       diff store vs targets (no writes)
friday doctor                     read-only health check (store, manifest, drift)
friday remote pull                git pull in ~/.friday
friday remote push -m "msg"       git add -A && commit && push (scaffolded .gitignore filters secrets)
friday remote status              git status
```

`init` supports both interactive and non-interactive flows:
`friday init --scaffold` (empty scaffold) and `friday init --remote URL` (clone).
Stdin-piping still works (`echo "URL" | friday init`) for tooling that prefers it.
Refuses to overwrite a non-empty `~/.friday` — remove it yourself to re-init.

`push` exits `2` if any change is `ActionConflict` (drift detected in CI, etc.).

## Adding a preset

1. Edit [internal/presets/presets.go](internal/presets/presets.go), add an
   entry to the `registry` map.
2. Existing users won't see the new preset until they re-init or hand-edit
   their `friday.yaml`. There's no auto-merge command — explicit beats clever.

## Limitations

- Concatenate rules and `frontmatter_strip` rules are not reversible (no pull).
- Pull only walks files that already exist in the store. New files added
  directly to a target dir are not auto-discovered — copy them into the
  store first, then pull keeps them in sync.
- `friday remote push` requires `git remote add origin ...` to have been
  run inside the user store at some point.
- No permissions translation across agents.
