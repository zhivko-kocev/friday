# friday

Go CLI that manages a single canonical agent-config store at user level
(`$UserConfigDir/friday/`) and applies it to multiple AI tools — Claude
Code, Cursor, OpenCode, GitHub Copilot. Project-specific configs are
push-only and transient: clone a content repo, write into the project's
agent dirs, discard the clone.

## Build & test

```bash
make build          # produces ./friday (Unix) or ./friday.exe (Windows)
make test           # go test ./...
make lint           # go vet ./...
make tidy           # go mod tidy
```

## Two scopes

| Scope    | Manifest location                          | Targets resolved against |
|----------|--------------------------------------------|--------------------------|
| user     | `$UserConfigDir/friday/friday.yaml`        | `$HOME`                  |
| project  | (cloned repo, transient)                   | `$CWD`                   |

`$UserConfigDir` is `~/.config` on Linux, `~/Library/Application Support`
on macOS, `%APPDATA%` on Windows. The store dir IS that directory — flat,
no nested `store/`.

## Standard repo layout (the contract)

A "config repo" — whether cloned into the user store or used transiently
in a project — is **pure content**. No `friday.yaml` required; if absent,
friday falls back to the built-in presets.

```
identity.md           who you are (concatenated into CLAUDE.md / AGENTS.md)
rules/*.md            behaviour rules
agents/*.md           agent definitions (claude only)
commands/*.md         slash-commands (claude only)
skills/<name>/*       skills (recursively mirrored)
friday.yaml           OPTIONAL — overrides preset behaviour
```

No preset currently reads `memory/` or `tasks/`. The scaffold doesn't create
them; if you ship them in a config repo they'll just sit there unused.

Empty subdirs are fine — globs that match nothing are reported as
`missing-source` and skipped. Dotfiles (`.gitkeep`, `.hidden.md`) are
filtered from `*` and `**` matches by convention.

## Package map

```
cmd/friday/           entry point — wires os.Args into cli.Run, sets version via ldflags
internal/cli/         command dispatcher — one cmdX function per subcommand
internal/config/      parses friday.yaml; LoadUser / LoadProject / NewDefault
internal/rules/       Rule type, FromSpec (string|[]string), glob expansion, token engine
internal/engine/      plans + applies push/pull; resolves drift via the conflict UI
internal/conflict/    interactive [k/t/d/s] prompt with line-LCS diff (LineDiff is reused by report)
internal/drift/       SHA256 store at $UserCacheDir/friday/state.json — flags external edits
internal/frontmatter/ parse/strip YAML frontmatter in .md files (CRLF-tolerant)
internal/git/         shells out to `git` for clone/pull/push/status
internal/presets/     built-in adapter rule sets (claude/cursor/opencode/copilot)
internal/initcmd/     `friday init` (scaffold or clone) and `friday add` (append preset)
internal/output/      all console output (colored, TTY-aware)
internal/atomicio/    WriteFile via temp + fsync + rename — used by config.Save and drift.Save
internal/textnorm/    one home for CRLF→LF normalization (used by engine, drift, frontmatter)
```

## Key design rules

**Repos are pure content.** `friday.yaml` is friday's *local customization*
file, not part of the standard layout. A repo can ship one to override
preset rules, but the common case is no manifest — friday uses presets.

**Two scopes, one engine.** `engine.Push` / `engine.Pull` operate on a
`*config.Config` that knows its `Scope` and `TargetRoot`. The same code
handles user-level (`~/.claude`) and project-transient (`./.claude`).

**Drift only applies to push.** On pull, every change is intentional. The
drift store at `$UserCacheDir/friday/state.json` is keyed by
`(adapter, absPath)` and refreshed on every successful push write. Project
pushes share the same drift store — they target absolute paths the user
might re-push later.

**Concatenate is one-way.** Multi-source → single-target rules (`CLAUDE.md`,
`AGENTS.md`, `copilot-instructions.md`) are not pullable. Same for rules
with `frontmatter_strip` — pulling would re-introduce stripped fields.

**Defaults resolved at use-time, not save-time.** The default
`Separator` is never written into `friday.yaml` — yaml.v3's block-scalar
emission for multi-line strings produces output it can't re-parse at
deeply-nested indentation.

## friday.yaml schema (optional)

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

| Form         | Resolved to                                  |
|--------------|----------------------------------------------|
| `/abs/path`  | as-is                                        |
| `~/foo`      | `$HOME/foo`                                  |
| `.claude`    | `$HOME/.claude` (user) or `$CWD/.claude` (project) |

### Tokens (only with `strategy: copy`)

| Token       | Value                                          |
|-------------|------------------------------------------------|
| `{filename}`| basename with extension (`researcher.md`)      |
| `{stem}`    | basename without extension (`researcher`)      |
| `{ext}`     | extension with dot (`.md`)                     |
| `{relpath}` | path relative to the rule's anchor             |
| `{dir}`     | directory portion of `{relpath}` (`""` if root)|

The **anchor** is the longest literal directory prefix of the `from`
pattern. For `agents/*.md` it's `agents`; for `skills/**/*.md` it's
`skills`; for the literal `rules/general.md` it's `rules` (parent dir).

## CLI

```
friday init                       # empty user store
friday init --adapters claude,cursor
friday init --from-git <url>      # clone a content repo into the user store
friday add <preset> [--target dir] [--force] [--list]
friday push [adapters...]         # user → ~/.claude, ~/.cursor, ...
friday push --from-git <url|path> [adapters...]   # transient: repo → ./.claude, ...
friday pull [adapters...]         # user-level reverse
friday status [adapters...]
friday remote pull                # git pull in the user store
friday remote push -m "msg"       # git add -A && commit && push (scaffolded .gitignore filters secrets)
friday remote status              # git status
```

`push` exits `2` if any change is `ActionConflict` (drift in CI, etc.).
Project pull is unsupported — there's no local store to pull into.

## Adding a preset

1. Edit [internal/presets/presets.go](internal/presets/presets.go), add an
   entry to the `registry` map.
2. The preset auto-appears in `friday add --list`,
   `friday init --adapters`, and the `loadUserOrDefault` fallback (push/pull
   without a `friday.yaml`).
3. No Go interface to implement — just describe the rule set.

## Limitations (v1)

- Concatenate rules and `frontmatter_strip` rules are not reversible (no
  pull). For frontmatter_strip there's a clean fix (merge-on-pull) — open
  if you want it.
- Pull only walks files that already exist in the store. New files added
  directly to a target dir are not auto-discovered — copy them into the
  store first, then pull keeps them in sync.
- `friday remote push` requires `git remote add origin ...` to have been
  run inside the user store at some point.
- No permissions translation across agents (deferred from the older
  registry-based design).
