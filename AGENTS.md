# friday

One CLI to manage AI agent configs from a single canonical store. Push to every
agent, pull edits back, sync across machines via git. Store: `~/.friday/`. Six
built-in presets, with default agent targets: `claude` (`~/.claude`), `codex`
(`~/.codex`), `copilot` (`~/.copilot`), `opencode` (`~/.config/opencode`),
`gemini` (`~/.gemini`), and `oh-my-pi` (`~/.omp/agent`) — all configurable via
`friday.yaml`. friday manages an agent's **full** configuration, not just its
Markdown knowledge: MCP servers, permissions, and settings defaults each
render from one canonical YAML source onto an adapter's native shape (JSON
or TOML) — MCP reaches all six adapters, permissions and defaults reach the five
with a native surface friday can write (oh-my-pi keeps both in its own
config.yml, which is YAML, and friday has no merge-yaml strategy yet). No
TUI, no daemon — friday is a one-shot CLI whether stdout is a terminal or a
pipe; bare `friday` prints usage plus a state-aware next-step hint.

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
flat, no nested `store/`. `sources:` in `friday.yaml` can compose one or more
additional git remotes underneath it (cloned to `.sources/<name>`, lowest
precedence first) — see internal/source and the `friday.yaml` schema below.

## Standard repo layout

```
core.md                      the entry file (concatenated into CLAUDE.md / AGENTS.md / copilot-instructions.md);
                             also matched at core/core.md — legacy identity.md still accepted
rules/*.md                   behaviour rules
agents/*.md                  agent/subagent definitions (claude, copilot, opencode, gemini, and oh-my-pi natively — gemini strips the unsupported `tools` field; codex renders TOML subagents via the render strategy)
commands/*.md                slash-commands / prompts / workflows (claude, codex, opencode, gemini, oh-my-pi)
skills/<name>/*              skills (recursively mirrored; codex/copilot/gemini/opencode share one ~/.agents/skills tree; claude and oh-my-pi each read their own skills dir instead)
mcp.yaml                     canonical MCP server list — renders onto every adapter's native MCP config (internal/spec, internal/render)
permissions.yaml             canonical five-axis allow/ask/deny policy — renders onto claude/codex/copilot/opencode/gemini's native permissions shape (oh-my-pi's lives in its YAML config.yml, which friday cannot merge into yet)
defaults.yaml                canonical model/theme/statusline/telemetry-opt-out — renders onto the same five adapters' own settings, for the same reason
native/<adapter>/**/*        verbatim mirror into that adapter's own target dir — the escape hatch for anything a schema above doesn't model
settings/<adapter>.{json,toml} merged into that adapter's own settings file (merge-json/merge-toml) — extra keys the canonical schemas don't own
hooks/hooks.json             merged into ~/.claude/settings.json's `hooks` key (merge-json, confirm-first); scripts under hooks/ run from the store in place. Codex/Copilot/Gemini each have their own hooks/<agent>/hooks.json source, in that agent's own dialect: Codex matches Claude's (top-level `hooks`; a matched PreToolUse command that exits 2 blocks the call), Copilot needs `"version": 1` with camelCase event names and denies by printing a `permissionDecision` JSON on stdout (a non-zero exit there is only a warning) into its own hooks/friday-git-guard.json rather than a co-owned file, and Gemini's live inside settings.json's own `hooks` key. OpenCode/oh-my-pi get a guard plugin instead (they have no declarative hook file)
friday.yaml                  adapter manifest — auto-seeded with all built-in presets (six agents + the shared "agents" adapter) at init
.gitignore                   scaffolded with secret + runtime-state patterns
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
internal/cli/         command dispatcher (urfave/cli/v3) — one newXCommand func per subcommand
internal/config/      parses friday.yaml; LoadUser / NewDefault; FRIDAY_HOME store-root override; degrades a stale manifest into Config.Warnings; edit.go rewrites the manifest through a yaml.Node editor so comments and key order survive
internal/rules/       Rule type, FromSpec (string|[]string), glob expansion, token engine
internal/source/      composes the primary store + configured `sources:` into one ordered Set of Source; Set.Roots() projects it to []rules.Root for glob expansion
internal/spec/        canonical schemas: MCP (mcp.yaml), Permissions (permissions.yaml), Defaults (defaults.yaml)
internal/render/      projects a parsed spec (or an agents/commands/*.md record) onto one adapter's native shape
internal/engine/      plans + applies push/pull over every rule strategy (copy/concatenate/merge-json/merge-toml/render); resolves drift via the conflict UI; atomic writes
internal/conflict/    interactive [k/t/d/s] prompt; 3-way merge and line diffs both delegate to internal/textdiff
internal/textdiff/    pure line-diff + 3-way merge + windowed rendering — no UI dependency, shared by --diff, the conflict prompt, and internal/engine's pull-side unchanged-line detection
internal/drift/       SHA256 store at $UserCacheDir/friday/state.json — flags external edits; owned.go tracks merge-json's own hook entries (hooks-owned.json)
internal/secret/      flags a literal credential in mcp.yaml instead of a required ${VAR} reference
internal/frontmatter/ parse/strip YAML frontmatter in .md files (CRLF-tolerant)
internal/git/         shells out to `git` for clone/pull/push/status, plus fetch/checkout-ref/current-ref for cloning and updating configured sources
internal/presets/     built-in adapter rule sets (claude/codex/copilot/opencode/gemini/oh-my-pi + the shared "agents" adapter)
internal/initcmd/     `friday init` — prompts for a URL, clones or scaffolds
internal/setupcmd/    `friday setup` / `promote` — apply store knowledge into a project's own config
internal/session/     read-only Reader per coding-agent CLI (claude/codex/copilot/gemini/opencode/oh-my-pi) — backs `friday sessions` / `session show` / `session export`; friday never writes into an agent's own session store
internal/upgrade/     `friday upgrade` — version check, https/official-host-pinned download, mandatory checksum verification, atomic binary replace
internal/output/      all console output (colored, TTY-aware; --quiet/--verbose/FORCE_COLOR/NO_COLOR)
internal/lint/        store checks + best-practice advisor (backs `friday doctor`); agnix.go delegates content linting to an external `agnix` binary when one is on PATH
internal/snapshot/    content-addressed pre-write snapshots (backs `friday rollback`)
internal/atomicio/    WriteFile via temp + fsync + rename — used for every file write
internal/textnorm/    one home for CRLF→LF normalization (used by engine, drift, frontmatter)
internal/ui/          TTY detection + huh-backed prompts (the plain-path interactive bits)
internal/ui/theme/    the shared color theme — backs internal/output and internal/cli's lipgloss/table renders
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
a sibling of the drift store) so a later push can retract its own now-stale entry
after a store edit. Retraction is content-based: a key friday recorded writing is
removed only while the target still holds exactly that value — anything else was
hand-edited since, is the user's now, and stays. With that cache gone the merge
degrades to exact-content dedup (still idempotent, just can't remove a
since-changed entry). It needs no whole-file drift baseline and never conflicts.
Not pullable.

**Hook consent is keyed on hook content.** Hook commands run arbitrary shell and a
store may be a clone of someone else's repo, so friday confirms before any write
that installs hook code: a merge whose store source lives under `hooks/`, and a
`copy` rule carrying `confirm_hook` — the OpenCode/oh-my-pi guard plugins, plus
every `native/<adapter>/**/*` mirror, which lands verbatim in a directory the
agent auto-executes from. `friday push` and `friday setup` show the exact commands
and prompt on stdin; a nil confirmer (`--no-interactive`) skips the write, `--force`
bypasses the prompt, and a dry run plans the real action without ever prompting.
The gate deliberately does not key on drift-exemption: that both over- and
under-fired — prompting for every MCP/permissions/defaults merge, so
`--no-interactive` silently dropped a whole policy, while letting a plain copy rule
install an auto-activating plugin unannounced.

**merge-toml owns leaves, not tables.** It is merge-json's counterpart for a TOML
target (e.g. Codex's `config.toml`), spliced through `go-toml/edit`. Ownership is
per-leaf: friday owns the scalar and array positions its payload actually renders,
bounded by the fragment's own top-level keys (`Tables()`), so a user's sibling key
inside a table friday also writes — `[tui] notifications` next to friday's
`[tui] theme` — is never touched. Everything friday does not own survives byte for
byte: unowned keys and tables, comments, blank lines, hand formatting, plus the
position and attached comments of an owned leaf that already exists. Three things
do change inside the owned set — a brand-new leaf is appended at the end of its
section rather than in payload order, an owned value is re-rendered by go-toml's
encoder so its quoting may change (`"dark"` becomes `'dark'`), and a retracted leaf
takes the comments attached to it with it. Retraction follows the same
content-based rule as merge-json, and a table it empties loses its now-orphaned
header. Not pullable.

**A multi-source store has one winner per literal file, every source's copy of
a glob.** `sources:` composes the primary store with 0+ additional git remotes,
lowest precedence first (see internal/source). A `concatenate` rule over a
literal alternative-name pattern (the entry file: `core.md`/`core/core.md`/
`identity.md`) includes every root's own copy, so an upstream identity and the
local one both contribute. Everything else that names one exact file —
`merge-json`'s/`merge-toml`'s source, a `render` rule's `mcp.yaml`/
`permissions.yaml`/`defaults.yaml` — instead resolves to a single winner, the
highest-precedence root that has it; a lower-precedence root's own copy is
reported as a Warning rather than silently dropped. A glob rule (`agents/*.md`,
`skills/**/*`) unions distinct files across every root and keeps only the
highest-precedence copy of a colliding relative path.

## friday.yaml schema

```yaml
version: 1
sources:                                    # optional; additional git-backed content, lowest precedence first
  - name: <name>                            # cloned to $HOME/.friday/.sources/<name>
    url: <git-url>
    ref: <branch|tag|sha>                    # omit for the remote's default branch
adapters:
  <name>:
    target: <path>                          # see "Path resolution" below
    project_target: <path>                  # optional; enables `friday setup`/`promote` for this adapter
    rules: [<rule>, ...]
    project_rules: [<rule>, ...]            # optional; rendered by `friday setup`/`promote` instead of push/pull
```

A `<rule>` is:

```yaml
- from: <pattern> | [<pattern>, ...]
  to: <template>
  strategy: copy | concatenate | merge-json | merge-toml | render  # default: copy
  separator: <string>                # concatenate-only; default "\n\n---\n\n"
  frontmatter_strip: [<key>, ...]    # strip listed YAML frontmatter keys
  replace: {<literal>: <literal>}    # rewrite on push, inverted on pull
  max_bytes: <int>                   # warn when the output exceeds the agent's limit
  confirm_hook: <bool>               # copy-only; confirm before installing an auto-activating hook
                                     # (an OpenCode plugin, an oh-my-pi extension, a native/ mirror)
  surface: mcp | permissions | defaults | codex-agent | gemini-command  # render-only; required
  merge: copy | merge-json | merge-toml                                # render-only; required
                                     # must be copy when surface is codex-agent or gemini-command
                                     # (a whole markdown record renders one whole file)
```

The strategy enum is closed. `md-to-toml` and `md-to-json` are retired,
replaced by `strategy: render` with `surface: codex-agent` and
`surface: gemini-command`. Every manifest seeded by a pre-v1.0 `friday init`
still names them, so friday no longer refuses such a file: `config.load`
drops each retired rule and records why in `Config.Warnings`, which the CLI
prints once per run on stderr — never stdout, which `--json` output and the
shell-completion stream both parse. The same list carries a rename notice for
any adapter key in `config.LegacyAdapterNames` (`antigravity` → `gemini`,
`pi` → `oh-my-pi`), whose rules still load but describe surfaces that moved.
`friday migrate` rewrites the manifest so the warnings stop and a renamed
adapter renders again. Any other unknown strategy is still a hard load error.

`replace` substitutes literal strings in file content: keys → values on push,
values → keys on pull. Presets use it to rewrite `${CLAUDE_PLUGIN_ROOT}` —
the path variable Claude Code plugins use for sibling-file references — to
`~/.friday`. The map must be invertible: no empty or duplicate values, no
key == value. Pull compares in target-space (target vs the store's forward
transform), so unedited files never phantom-update; the textual inverse only
runs on edited files. Choose replacement values that cannot occur naturally
in content (`~/.friday` is safe in friday-free repos; `~/.claude` is not),
or a natural occurrence inside an edited file will turn into the key.

`render` projects one of friday's three canonical schemas — `mcp.yaml`
(`surface: mcp`), `permissions.yaml` (`surface: permissions`), or
`defaults.yaml` (`surface: defaults`) — onto one adapter's native shape
(internal/spec parses and validates the source, internal/render does the
projection), or transforms a single markdown record (`surface: codex-agent`
for `agents/*.md` → a Codex TOML subagent, `surface: gemini-command` for
`commands/*.md` → a Gemini custom command). `merge` names how the projected
result lands: `copy` overwrites the whole target, `merge-json`/`merge-toml`
splice it into a co-owned file the way the strategies of the same name do
for a plain source file.

Pull support varies by surface. `mcp` always has a real inverse: it reads
the adapter's native file back, recovers the fields that round-trip, and
merges the result into the owning source's `mcp.yaml` — a field the native
form can't express (or that's scoped to a different adapter) is left
untouched, never deleted. `permissions` inverts the same way, but only
when `merge` isn't `copy` — a `copy`-merged target (e.g. Gemini's
whole-file policy) is friday-owned outright, so `friday push` is how you
reconcile it instead of a pull. `defaults` and the two markdown-record
surfaces (`codex-agent`, `gemini-command`) never pull: `defaults`' native
enums are narrower than the canonical union they were clamped from, and
the markdown surfaces drop frontmatter on the way out — both are one-way
by construction, same as a plain `merge-json`/`merge-toml` rule.

### Canonical schemas

`mcp.yaml`, `permissions.yaml`, and `defaults.yaml` are a user-facing contract
frozen at v1.0. `internal/spec` parses each with strict decoding — an unknown
field, an unknown enum value, or a second YAML document in the same file is an
error, not a silent drop — and `internal/render` projects the validated value per
adapter. `version` defaults to `1` when absent, and `1` is the only accepted
value.

```yaml
# mcp.yaml
version: 1
servers:
  <name>:                          # must not be empty — every adapter keys its native config by it
    transport: stdio | http | sse  # required
    command: <string>              # stdio only; required there
    args: [<string>, ...]          # stdio
    env: {<KEY>: <value>}          # stdio
    url: <string>                  # http/sse only; required there
    headers: {<name>: <value>}     # http/sse
    tools: [<name>, ...]           # Copilot needs tool names spelled out; empty means every tool ("*")
    enabled: <bool>                # default true
    agents: [<adapter>, ...]       # restrict to a subset; empty means every adapter
```

Each `agents` entry is checked against the closed adapter set (`claude`, `codex`,
`copilot`, `gemini`, `oh-my-pi`, `opencode`). `enabled: false` renders the
adapter's own off switch on the three that have one (codex, opencode, oh-my-pi) —
never omission, which would leave a previously pushed server half-retracted and
still loadable; on the other three the entry is omitted and reported, so the user
learns the disable did not reach that agent. A server whose `agents` excludes the
adapter is omitted silently — "not for this agent" means the entry has no business
in that file at all. `enabled`, `agents`, and `tools` outside Copilot are
store-only: the pull inverse cannot recover them, so it merges into the existing
`mcp.yaml` entry instead of replacing it and they survive untouched.

```yaml
# permissions.yaml — five fixed axes, three action buckets each
version: 1
read:                              # also: write, exec, net, mcp — same shape
  allow: [<pattern>, ...]
  ask:   [<pattern>, ...]
  deny:  [<pattern>, ...]
```

Every renderer resolves an axis deny → ask → allow, first match wins (Claude
Code's model, the strictest of the six). What a pattern matches is the axis's own
business — a path for `read`/`write`, a command for `exec`, a host for `net`, a
server or tool for `mcp` — and a whitespace-only pattern is rejected rather than
rendered as a rule that can never match. Rendering is deliberately asymmetric per
tier on Claude: a canonical `deny`/`ask` becomes the command-prefix form
`Bash(cmd:*)` so it also blocks argument variants, while an `allow` keeps the
exact form `Bash(cmd)` so an allow is never widened into permission the user never
wrote. The pull inverse mirrors that split. Anything friday cannot express for a
given adapter is reported as unmapped, never dropped silently — including every
pattern for oh-my-pi, which has no reachable permission surface at all.

```yaml
# defaults.yaml
version: 1
model:
  provider: <string>               # optional except on OpenCode, which keys a model by provider/name
  name: <string>                   # required when model is set
  effort: off | minimal | low | medium | high | xhigh | max
theme: default | dark | light | github | dim | high-contrast | colorblind
statusline:
  command: <string>                # required when statusline is set
  padding: <int>                   # >= 0; omitted from the render when unset
  refresh_interval: <int>          # >= 0; omitted from the render when unset
telemetry: <bool>                  # opt-out only: false renders each adapter's disable knob,
                                   # true or unset renders nothing
overrides:
  <adapter>:                       # per-adapter escape hatch for a vendor-only field
    <native-key>: <value>          # merged last, so it wins over a canonical field on the same key
```

`effort` and `theme` are unions each adapter's narrower enum is clamped or mapped
from: codex accepts `minimal..xhigh`, claude and copilot `low..xhigh`, and a clamp
that actually changed the value is reported. `statusline` reaches only the two
adapters with a native one (claude, copilot); `padding` and `refresh_interval`
are omitted from the render when unset rather than written as `0`, which both
adapters reject for `refreshInterval` — at the cost of the whole `statusLine`
object. An `overrides` key that is not an adapter name is a
parse error pointing at `friday migrate`; an adapter that is real but has no
defaults surface is reported instead.

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

Porcelain — the everyday seven (`friday help`):

```
friday init                       clone or scaffold your ~/.friday store
friday claim                      capture an agent's existing config into the store
friday setup                      add friday knowledge to the current project
friday sync                       capture local edits, then fan them to every agent
friday status                     show what would change (no writes)
friday sessions                   list coding-agent sessions across every installed agent
friday share -m "msg"             propose your store changes for team review (opens an MR)
```

Advanced (`friday help --all`): `push` (store → installed agents), `pull`
(agent edits → store; `--discover` finds new files), `promote` (project config →
store), `transfer <from> <to>` (move one agent's config to another through the
store), `source` (manage additional `sources:` — `add`/`list`/`update`/`rm`),
`session` (inspect/export one session — `show`/`export`), `rollback`/`undo`,
`eject`, `remote` (git ops on the store: `init`/`pull`/`push`/`propose`/`status`),
`doctor` (health-check + store lint + agnix delegation; `doctor <file>` explains
a mapping), `upgrade` (self-update; `--check`/`--force`/`--version <tag>`),
`migrate` (rewrite a friday.yaml written by an older release; `--dry-run`),
`completion`, `version`, `help`.

`init` supports both interactive and non-interactive flows:
`friday init --scaffold` (empty scaffold) and `friday init --from-git URL`
(clone; `--remote` is a soft-deprecated alias). Stdin-piping still works
(`echo "URL" | friday init`) for tooling that prefers it. Refuses to overwrite a
non-empty `~/.friday` — remove it yourself to re-init.

`push` exits `2` if any change is `ActionConflict` (drift detected in CI, etc.).

Global flags (work with any command): `--no-color`, `--quiet` (suppress all
non-error output; exit codes unchanged), `--verbose` (show per-file decisions
and each rendered path's resolved source). `FRIDAY_HOME` overrides the store
root; `FORCE_COLOR` forces color even off a terminal, alongside the existing
`NO_COLOR`/`FRIDAY_NO_COLOR`.

## Adding a preset

1. Edit [internal/presets/presets.go](internal/presets/presets.go), add an
   entry to the `registry` map.
2. If any of its rules uses `strategy: render`, register the adapter in
   [internal/render](internal/render) too: `mcpAdapters` (`mcp.go`),
   `Permissions`' switch (`permissions.go`), `defaultsAdapters` (`defaults.go`),
   and the `MCPInverse`/`PermissionsInverse` switches for anything pullable.
   Each is a closed set that hard-errors on an unknown name
   (`render.MCP: unknown adapter "x"`), so a preset added without this step
   plans fine and fails at push time. A preset limited to
   `copy`/`concatenate`/`merge-json`/`merge-toml` needs nothing here.
3. Existing users won't see the new preset until they re-init or hand-edit
   their `friday.yaml`. There's no auto-merge command — explicit beats clever.

## Limitations

- Concatenate, `frontmatter_strip`, a plain `merge-json`/`merge-toml` rule, a
  `render` rule with `surface: defaults`/`codex-agent`/`gemini-command`, and a
  `render` rule with `surface: permissions` and `merge: copy` are all one-way
  (no pull) — each target either co-owns content friday does not otherwise
  track, or was narrowed/stripped on the way out in a way friday cannot
  reconstruct. Only `render` rules with `surface: mcp`, or `surface:
  permissions` merged as `merge-json`/`merge-toml`, have a real pull inverse.
- Every write that installs hook code is confirmed first: a merge whose store
  source lives under `hooks/`, and a `copy` rule with `confirm_hook` (the
  OpenCode/oh-my-pi guard plugins and the six `native/<adapter>/**/*` mirrors).
  `friday push` and `friday setup` prompt on stdin. A nil confirmer
  (`--no-interactive`) skips the write; `--force` bypasses the prompt.
- A plain `pull` only walks files the store already knows about. A file created
  directly in a target dir is caught by `friday pull --discover`, which walks the
  whole agent dir and captures new files into the store. On import the
  `native/<adapter>/**/*` mirror is last in line: rules are ordered
  most-specific-first, so it captures only what nothing above it already models,
  and never the literal target of a rule that cannot be imported at all
  (concatenate's `CLAUDE.md`, a composed `settings.json`) — a whole-file copy
  there would clobber the composition on the next push.
- `friday remote push` requires `git remote add origin ...` to have been
  run inside the user store at some point.
- A configured source (`sources:`) is read-only from friday's perspective —
  `friday source update` fetches and checks it out, but nothing ever pushes
  or pulls back into a source's own git remote. A pull or `claim` whose winning
  canonical file lives in a source checkout is refused rather than written,
  naming both escape routes: edit it in that source's own repo, or copy it into
  the primary store, which outranks every configured source.
