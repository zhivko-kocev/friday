# friday — use cases & command guide

A task-oriented manual for `friday` v1.0. Every command is explained with what
it does, when to reach for it, the full flow (including what happens under the
hood), its flags, and worked examples. If you just want the elevator pitch, read
the [README](../README.md); this document is the deep end.

---

## Table of contents

- [Core concepts](#core-concepts) — read this first
- [The everyday seven](#the-everyday-seven)
  - [`init`](#init--create-or-clone-your-store) · [`claim`](#claim--adopt-an-agents-existing-config) · [`sync`](#sync--the-daily-driver) · [`status`](#status--what-would-change-the-two-axis-grid) · [`setup`](#setup--put-knowledge-into-a-project) · [`sessions`](#sessions--the-cross-agent-session-index) · [`share`](#share--propose-changes-to-your-team)
- [Advanced commands](#advanced-commands)
  - [`push`](#push--one-way-store--agents) · [`pull`](#pull--one-way-agents--store) · [`promote`](#promote--project-config-back-into-the-store) · [`transfer`](#transfer--move-one-agents-config-to-another) · [`source`](#source--additional-content-sources) · [`session`](#session--inspect-or-export-one-session) · [`rollback`](#rollback--undo-a-write) · [`remote`](#remote--the-git-bridge) · [`doctor`](#doctor--health-check--best-practice-advisor) · [`upgrade`](#upgrade--update-friday-itself) · [`migrate`](#migrate--modernize-a-manifest-from-an-older-release) · [`eject`](#eject--stop-managing-agents-entirely) · [`completion`](#completion--shell-tab-completion) · [`version`](#version) · [`help`](#help)
- [End-to-end workflows](#end-to-end-workflows)
- [The best-practice advisor](#the-best-practice-advisor-in-depth)
- [Safety model](#safety-model)
- [Exit codes (for CI)](#exit-codes-for-ci)

---

## Core concepts

**One store, many agents.** Everything friday knows lives in a single directory,
`~/.friday/`, as plain Markdown. friday *renders* that store into each AI agent's
native config format and location. You edit one source; friday keeps Claude Code,
Codex, OpenCode, Copilot, and the rest from drifting apart.

**The store layout (the contract):**

```
~/.friday/
  core.md            entry file — heads CLAUDE.md / AGENTS.md / copilot-instructions.md
                     (also matched at core/core.md; legacy identity.md still works)
  rules/*.md         behaviour rules — concatenated after core.md into each agent's
                     one instruction file
  agents/*.md        subagent definitions (frontmatter adapted per agent dialect;
                     Codex receives rendered TOML)
  commands/*.md      slash commands / prompts (Gemini receives rendered TOML)
  skills/<name>/     agent skills, mirrored verbatim (frontmatter trimmed per agent)
  mcp.yaml           canonical MCP server list — rendered into each agent's own
                     MCP config
  permissions.yaml   canonical allow/ask/deny policy — rendered into each agent's
                     own permissions shape
  defaults.yaml      canonical model / effort / theme / statusline / telemetry
                     opt-out — rendered into each agent's own settings
  native/<adapter>/**/*
                     verbatim mirror into that adapter's own config dir — the
                     escape hatch for anything the schemas above don't model
  settings/<adapter>.{json,toml}
                     extra keys/tables merged into that adapter's own settings file
  hooks/**           per-agent hook sources — hooks/hooks.json merges into
                     ~/.claude/settings.json, hooks/<agent>/... serves the rest;
                     scripts run from the store in place (confirm-first)
  friday.yaml        adapter manifest, plus the `sources:` list (optional;
                     auto-seeded by `friday init`)
  .friday-doctor.yaml  optional lint config (see the advisor)
  .sources/<name>/   checkout of each git remote listed under `sources:` —
                     managed by `friday source`; read-only to friday
```

`standards/` and `connectors/` no longer have preset rules. A store may still
keep them, but nothing copies them out — a rule's own content reaches them by
path, since every built-in rule rewrites `${CLAUDE_PLUGIN_ROOT}` to
`~/.friday`.

**Rules & rendering.** Each adapter in `friday.yaml` (or a built-in preset) is
a list of *rules*. A rule names one or more store files (`from`), one target
(`to`), and a strategy:

- **copy** (the default when `strategy` is omitted) — mirror a file or a tree
  (e.g. `skills/**/*` → `~/.claude/skills/`). Reversible, so `pull` can
  capture edits back.
- **concatenate** — join several store files into one target (e.g. `core.md` +
  `rules/*.md` → `CLAUDE.md`), joined by `separator` (default `\n\n---\n\n`).
  Not reversible, so `pull` skips these.
- **merge-json** — merge the top-level keys of one store JSON file into a
  target JSON file friday co-owns with you (e.g. `settings/claude.json` →
  `~/.claude/settings.json`). Only the keys present in the source are touched;
  the rest of the target survives.
- **merge-toml** — the TOML analog: it splices the tables present in the
  source into the target (e.g. `settings/codex.toml` → `~/.codex/config.toml`)
  and unions nested keys, so unmanaged sibling keys survive. Owned entries may
  be re-ordered on write and their comments are not preserved.
- **render** — project a canonical schema, or transform one file, into a single
  adapter's native shape, then land the result the way `merge` says. Both
  `surface` and `merge` are required, and only on a render rule.

A render rule's `surface` names what is being projected:

| `surface` | source | produces |
|-----------|--------|----------|
| `mcp` | `mcp.yaml` | that adapter's MCP server config |
| `permissions` | `permissions.yaml` | that adapter's permissions / policy shape |
| `defaults` | `defaults.yaml` | that adapter's model / theme / statusline settings |
| `codex-agent` | one `agents/*.md` | a Codex TOML subagent |
| `gemini-command` | one `commands/*.md` | a Gemini TOML custom command |

`merge` names how the projection lands: `copy` (friday owns the whole target
file), `merge-json`, or `merge-toml` (friday owns only its own keys or tables
in a file you also edit).

Other per-rule fields: `separator`, `frontmatter_strip` (drop frontmatter keys
the target dialect has no equivalent for), `replace` (literal string
substitutions), `max_bytes` (friday still writes the target, but every push and
status flags it as larger than the agent will consume), and `confirm_hook`
(this copy installs a file the agent auto-executes, so ask before writing it).
`merge-json`/`merge-toml` accept none of `separator`, `frontmatter_strip`, or
`max_bytes` — each splices one non-glob source into one literal target.

`md-to-toml` and `md-to-json` are **retired**. Manifests seeded by a pre-v1.0
`friday init` still name them, so friday recognizes them: such a rule is
dropped with a warning rather than failing the whole manifest, and
[`friday migrate`](#migrate--modernize-a-manifest-from-an-older-release)
rewrites the file to the `render` equivalents (`surface: codex-agent` and
`surface: gemini-command`).

**What can be pulled.** `concatenate`, `frontmatter_strip`, a plain
`merge-json`/`merge-toml` rule, a `render` rule with `surface:
defaults`/`codex-agent`/`gemini-command`, and a `render` rule with `surface:
permissions` and `merge: copy` are all one-way (no pull) — each target either
co-owns content friday does not otherwise track, or was narrowed/stripped on
the way out in a way friday cannot reconstruct. `pull` reports those as
unsupported instead of guessing. Only `render` rules with `surface: mcp`, or
`surface: permissions` merged as `merge-json`/`merge-toml`, have a real pull
inverse.

Retraction of a `merge-json`/`merge-toml` write is content-based: a key or
table friday previously wrote is removed only when the target's current value
still equals what friday wrote, so an edit you made by hand is never silently
reverted.

Every rule rewrites the Claude-plugin path variable `${CLAUDE_PLUGIN_ROOT}` to
`~/.friday` on push (and back on pull), so plugin-shaped knowledge repos (like
developer-os) work unmodified.

**Presets & the manifest.** friday ships **six built-in presets** (claude,
codex, copilot, opencode, gemini, oh-my-pi) plus a shared `agents` adapter
that owns the one `~/.agents/skills` tree Codex, Copilot, Gemini, and OpenCode
all discover natively. If `~/.friday/friday.yaml` exists it is
*authoritative* — friday uses exactly those adapters. If it's absent, friday
falls back to the built-in presets. To customize, add or edit adapters in
`friday.yaml` (it versions with your store).

**Multiple sources.** `sources:` in `friday.yaml` composes additional
git-backed content roots underneath the primary store, lowest precedence
first — a team repo and your own store, without merging them. Each entry is a
`name` (a lowercase slug, never `local`), a `url`, and an optional `ref`:

```yaml
sources:
  - name: team
    url: git@github.com:acme/ai-config.git
  - name: vendor
    url: https://github.com/example/rules.git
    ref: v2.1.0
```

Sources clone to `~/.friday/.sources/<name>` and are managed by
[`friday source`](#source--additional-content-sources). A source is
**read-only** to friday: it never writes into `.sources/<name>`, and a capture
whose canonical file is owned by one is refused with both escape routes named
— edit it in that source's own repo, or copy the file into the primary store,
which outranks every configured source.

**Drift & baselines (the safety core).** Every time friday writes a target file
it records that file's SHA256. Before overwriting, it re-checks the hash:
- match → friday wrote it last, safe to update;
- mismatch → *you* edited it directly (drift) — friday refuses to clobber it and
  either prompts (interactive) or reports a conflict (non-interactive).

Writes are **atomic** (temp file + rename), so Ctrl-C mid-write never leaves a
half-written file. Line-ending differences (CRLF vs LF) are normalized, so a
Windows checkout of LF-authored files isn't flagged as drift.

**Interactive vs. non-interactive.** There is no full-screen mode: every
command is one-shot and plain-text. Running bare `friday` (no arguments)
prints usage plus the single concrete next step for wherever this install
stands — `friday init`/`friday claim` when there is no store, `friday sync`
when an installed agent has pending changes, `friday status` then `friday
sync` or `friday push --force` when it has drift, and nothing extra when
everything is already in sync — and exits 1. The hint is computed from the
same dry-run plan `sync` would use, filtered to installed adapters, so a
preset for an agent you don't have never nags. Individual commands prompt on
stdin where a write needs consent (agent/knowledge pickers, conflict
resolution, hook wiring). When stdout/stdin isn't a TTY — pipes, CI — or you
pass `--no-interactive`/`--json`, output is plain and byte-stable and
conflicts are reported rather than prompted. Color obeys `NO_COLOR` /
`FRIDAY_NO_COLOR` / `--no-color`, and `FORCE_COLOR` forces it back on.

**The two-axis status model.** `friday status` shows each managed file on two
independent axes (borrowed from chezmoi):
- **column 1 — local drift:** you edited the *target* since friday last wrote it
  (an edit `pull`/`sync` would capture).
- **column 2 — pending render:** the *store* changed and a `push`/`sync` would
  update the target.

A file can be dirty on either, both (a conflict), or neither (in sync).

---

## The everyday seven

### `init` — create or clone your store

**Purpose:** get `~/.friday` onto this machine.

**When:** first time on a new laptop/desktop, or bootstrapping from a team repo.

**Flow:**
1. If you pass a remote, friday shallow-clones it into `~/.friday`.
2. Otherwise it prompts for a URL (interactive) or scaffolds an empty store.
3. On scaffold it seeds `friday.yaml` with all built-in presets; a cloned repo is
   left as-is (it runs on built-in presets if it has no manifest).

**Flags:**
| Flag | Meaning |
|------|---------|
| `--from-git URL` | Clone URL into `~/.friday` without prompting (scripts). |
| `--scaffold` | Create an empty store, no prompt, no clone. |

(`--remote` is the deprecated earlier name for `--from-git` — it still works but prints a nudge.)

**Examples:**
```bash
friday init                                             # interactive: prompts for the URL
friday init --from-git git@github.com:me/ai-config.git    # clone a dotfiles/team repo
friday init --from-git git@github.com:me/developer-os.git  # a Claude-plugin-shaped repo works too
friday init --scaffold                                  # start fresh, author from scratch
```

**Notes:** `~/.friday` must not already exist for a clone. `--scaffold` never
prompts. If you already have agents configured but no store,
[`friday claim`](#claim--adopt-an-agents-existing-config) does the scaffold and
the capture in one step.

---

### `claim` — adopt an agent's existing config

**Purpose:** turn config an agent already has on disk into a store, without
authoring anything by hand.

**When:** first run on a machine where Claude Code, Codex, or any other
supported agent was configured directly. Also the way to *enrich* an existing
store with an agent friday has never seen.

**Flow:**
1. If there is no store yet, `claim` scaffolds one (`friday.yaml` seeded with
   every built-in preset) — so this is a single command from nothing.
2. It walks each installed agent's config dir, exactly like
   [`pull --discover`](#pull--one-way-agents--store), and captures what it
   finds into the store's canonical layout.
3. Files an earlier, more specific rule already models are left to that rule;
   the `native/<adapter>/**/*` mirror is an escape hatch, not a second claim on
   the same file, and it never captures the target of a rule that has no pull
   inverse (a concatenated `CLAUDE.md`, a composed `settings.json`).

**Flags:**
| Flag | Meaning |
|------|---------|
| `--to NAME` | Route newly captured files into this configured source instead of the primary store. |
| `--dry-run` | Show what would be captured without writing. |
| `--no-interactive` | Don't prompt on conflicts; treat them as skip. |

**Examples:**
```bash
friday claim                    # every installed agent, into the primary store
friday claim claude codex       # only these adapters
friday claim --dry-run          # see what it would take
friday claim --to team          # newly captured files land in the "team" source
```

**Notes:** `claim` reads agents and writes only the store, so it is safe to run
against a machine you don't want reconfigured. Follow it with
[`friday status`](#status--what-would-change-the-two-axis-grid) before the first
`sync`.

---

### `sync` — the daily driver

**Purpose:** reconcile the store with every installed agent, both directions.

**When:** your default. Run it after editing store files *or* after tweaking an
agent's config directly — it sorts out which is newer.

**Flow (it's `pull` then `push`, like `git pull` = fetch + merge):**
1. **pull phase** — capture edits you made in agent dirs back into the store;
   both baselines update so the store is now the source of truth.
2. **push phase** — fan the (now-current) store out to every *other* installed
   agent. Files just pulled read as in-sync, so only genuinely new content moves.
3. Conflicts (both sides changed) surface in the interactive resolver, or are
   reported/skipped non-interactively.

**Flags:**
| Flag | Meaning |
|------|---------|
| `--dry-run` | Show both phases, write nothing. |
| `--force` | Resolve every conflict in favor of the incoming side (unattended). |
| `--no-interactive` | Skip prompts; conflicts are skipped (CI). |

**Examples:**
```bash
friday sync                 # reconcile everything
friday sync claude          # scope to one agent
friday sync --dry-run       # preview both phases
friday sync --force         # CI/unattended: take incoming on every conflict
```

**Notes:** the dry-run push plan is computed *before* the pull applies, so a
dry-run preview may differ slightly from what a real run does after capturing
edits — the header says so.

---

### `status` — what would change (the two-axis grid)

**Purpose:** a read-only, at-a-glance picture of every managed file. Never writes.

**When:** before a sync, in CI gates, or to answer "is anything out of step?"

**Flow:** friday plans a dry-run push (column 2) and reads the drift store
(column 1), then prints a grid. Rows that need a decision — a hand edit
(column 1) or a conflict — always show per file; a large group of plain
pending renders folds into one count line per adapter, and uninstalled agents
collapse to a single "would create N files" line, so the view stays scannable.
A trailing count reports the in-sync remainder.

**Reading the grid:**
```
changes:
  col 1: local edit to capture   col 2: pending render   ! conflict
  M   claude    CLAUDE.md               # you edited the target; store unchanged
  M!  claude    rules/x.md              # both changed — a conflict
   M  codex     4 files (agents/×2, skills/×2)   # store changed; push will update
   A  opencode (12 files — not installed; `friday sync` sets it up)
  92 file(s) in sync
```

**Flags:**
| Flag | Meaning |
|------|---------|
| `--diff` | Also print the content diff for each pending render. |
| `--origin` | Also show where each adapter is defined (friday.yaml / built-in), plus each adapter's rule mappings (`strategy: from → to`). |
| `--check` | CI: exit 2 if anything is out of sync, 0 when clean. |
| `--json` | Machine-readable; exit 2 on conflict (body/exit unchanged across versions). |

**Examples:**
```bash
friday status                       # the grid
friday status --diff                # grid + diffs
friday status --origin              # grid + adapter origins
friday status --check && echo clean # CI gate
friday status claude                # one agent
```

---

### `setup` — put knowledge into a project

**Purpose:** drop a chosen subset of your store into a *project's* own config.

**When:** starting a repo that should carry specific rules/agents/skills for a
given agent — committed to *that project's* git, not to `~/.friday`.

**Flow:**
1. Pick an agent (interactive picker, or `--agent`).
2. Check the knowledge to include — core, rules, agents, commands, skills — with
   sensible items pre-selected.
3. friday writes it into the project's native layout (`CLAUDE.md`, `.claude/…`,
   `.github/…`, `.opencode/…`), which the project's git then versions.

**Flags:**
| Flag | Meaning |
|------|---------|
| `--agent NAME` | Skip the agent prompt. |
| `--dry-run` | Show what would be written. |
| `--force` | Overwrite without prompting on drift. |
| `--no-interactive` | No prompts; conflicts treated as skip (CI). |

**Examples:**
```bash
cd my-project
friday setup                        # interactive
friday setup --agent claude --dry-run
```

**Notes:** friday deliberately does **not** create a `.friday` folder inside the
project — the project's own agent config *is* the artifact, versioned by its git.
An adapter in your `friday.yaml` that carries no `project_rules` falls back to
the built-in preset's project shape, so `setup` works against a store scaffolded
by `friday init`. Every project-scope destination is clamped to the project
directory: a rule cannot write outside it.

---

### `sessions` — the cross-agent session index

**Purpose:** one list of your coding-agent conversations, across every agent
installed on this machine.

**When:** "where did I do that work?", or before handing a task from one agent
to another.

**Flow:** each agent has a read-only reader over its own session store —
claude, codex, copilot, gemini, opencode, oh-my-pi. friday never writes into
any of them. A reader reports itself unavailable when that agent's *session*
directory is absent (e.g. `~/.copilot/session-state`, `~/.gemini/tmp`), not
merely when its config dir exists — friday pushes config into those config
dirs, so probing them would claim availability and then list nothing.

**Flags:**
| Flag | Meaning |
|------|---------|
| `--agent NAME` | Only this agent (`claude`, `codex`, `copilot`, `gemini`, `opencode`, `oh-my-pi`). |
| `--project SUBSTR` | Only sessions whose project path contains SUBSTR. |
| `--since D` | Only sessions started within this long ago (`24h`, `7d`, `2w`). |
| `--limit N` | Show at most N sessions (0 = no limit). |
| `--json` | Machine-readable. |
| `--stats` | Print token/cost rollups instead of the session list. |
| `--group G` | Rollup granularity with `--stats`: `session`, `daily`, `weekly`, `monthly` (default `session`). |

**Examples:**
```bash
friday sessions                                # everything, newest first
friday sessions --agent claude --since 7d
friday sessions --project my-repo --limit 20
friday sessions --stats --group weekly         # token and cost rollups
friday sessions --json | jq '.sessions[0]'
```

**Notes:** the per-session detail views are
[`session show` and `session export`](#session--inspect-or-export-one-session).
The OpenCode reader shells out to the `opencode` binary; empty output there
means zero sessions, not a failure.

---

### `share` — propose changes to your team

**Purpose:** publish store changes for review instead of pushing to everyone.

**When:** you improved shared rules and want an MR/PR, not a force-push.

**Flow:** friday makes an ephemeral commit, pushes it to a *new* remote branch,
and opens an MR (GitLab push-options) or prints the PR link (other forges). Your
local store is untouched until the MR merges.

**Flags:**
| Flag | Meaning |
|------|---------|
| `-m`, `--message` | Commit message (**required**). |
| `--branch NAME` | Remote branch name (default `friday/propose-<timestamp>`). |
| `--target BRANCH` | MR target branch (default: the remote's HEAD branch). |

**Examples:**
```bash
friday share -m "tighten the code-review rules"
friday share -m "add release skill" --branch friday/release-skill --target main
```

**Notes:** `share` is the friendly porcelain for [`remote propose`](#remote--the-git-bridge).

---

### seeing configured agents

There is no separate `list` command — `status` and `doctor` already show every
configured adapter, its target, and whether it's installed. For the full
per-adapter rule mappings (`strategy: from → to`), use:

```bash
friday status --origin
```

---

## Advanced commands

### `push` — one-way store → agents

**Purpose:** fan the store out without capturing anything back.

**When:** you only authored in the store and want it applied; or you want a single
agent updated.

**Flags:**
| Flag | Meaning |
|------|---------|
| `--only GLOB` | Push only changes sourced from store files matching GLOB. |
| `--diff` | Show a line diff for each change. |
| `--dry-run` | Show what would change, write nothing. |
| `--force` | Overwrite drift without prompting. |
| `--no-interactive` | Don't prompt; treat conflicts as skip. |

**Examples:**
```bash
friday push                     # store → all installed agents
friday push claude              # one agent
friday push --only 'rules/*'    # only rule-sourced changes
friday push --diff --dry-run    # preview with diffs
```

**Notes:** `push` exits **2** when any change is a conflict (drift friday
refuses to clobber), which is the exit code a CI job should treat as "someone
edited an agent dir directly" rather than as an unclassified failure. See
[exit codes](#exit-codes-for-ci). Only installed adapters are pushed unless you
name one explicitly.

---

### `pull` — one-way agents → store

**Purpose:** capture edits made directly in an agent dir back into the store.

**When:** you tuned `~/.claude/CLAUDE.md` in the moment and want it preserved; or
you're bootstrapping the store from an existing install.

**Two modes:**
- **plain** — updates store files the store *already knows about*.
- **`--discover`** — walks the whole agent dir and captures files a normal pull
  can't see (e.g. a brand-new skill you authored in `~/.claude/skills/`). This is
  how you *enrich* or *bootstrap* a store.

**Flags:**
| Flag | Meaning |
|------|---------|
| `--discover` | Walk the agent dir; capture new files too. |
| `--dry-run` | Show what would change. |
| `--all` | Auto-apply every adapter (skip the prompt). (`--force` is the deprecated alias.) |
| `--no-interactive` | Skip prompts; legacy batch flow. |

**Examples:**
```bash
friday pull                 # capture known files from all agents
friday pull claude          # one agent
friday pull --discover      # bootstrap/enrich from an existing install
```

**Notes:** not every rule has an inverse. `concatenate`, `frontmatter_strip`, a
plain `merge-json`/`merge-toml` rule, a `render` rule with `surface:
defaults`/`codex-agent`/`gemini-command`, and a `render` rule with `surface:
permissions` and `merge: copy` are all one-way (no pull) — each target either
co-owns content friday does not otherwise track, or was narrowed/stripped on
the way out in a way friday cannot reconstruct. `pull` reports those as
unsupported instead of guessing. Only `render` rules with `surface: mcp`, or
`surface: permissions` merged as `merge-json`/`merge-toml`, have a real pull
inverse. A pull also never writes into a configured source: `.sources/<name>`
is read-only, and a capture whose canonical file one of them owns is refused
with both escape routes named.

---

### `promote` — project config back into the store

**Purpose:** the inverse of `setup` — lift knowledge authored in a *project* up
into `~/.friday`, optionally straight into an MR.

**When:** a skill/rule you wrote for one project deserves to be global (and shared).

**Flags:**
| Flag | Meaning |
|------|---------|
| `--agent NAME` | Agent preset the project uses (skips the prompt). |
| `--propose` | After capturing, push a branch + open an MR (requires `-m`). |
| `-m` | MR commit message (with `--propose`). |
| `--branch` / `--target` | MR branch / target (with `--propose`). |
| `--dry-run` | Show what would be captured. |
| `--force` | Overwrite store files without prompting. |
| `--no-interactive` | Don't prompt on conflicts; treat as skip. |

**Examples:**
```bash
friday promote .claude/skills/release-notes
friday promote .claude/skills/release-notes --propose -m "add release-notes skill"
```

**Notes:** closes the loop — knowledge born in a session flows up to the user
store and out to the team. Like `setup`, an adapter with no `project_rules` in
your manifest falls back to the built-in preset's project shape.

---

### `transfer` — move one agent's config to another

**Purpose:** carry one agent's on-disk config over to another agent, through
the store.

**When:** you're switching daily drivers, or standing a second agent up to
match the one you already tuned.

**Flow:** two halves, in order — capture `<from>`'s targets into the store
(the `claim`/`pull --discover` path), then push the store to `<to>`. Both
halves prompt on a collision, so a scripted transfer needs
`--no-interactive`.

**Flags:**
| Flag | Meaning |
|------|---------|
| `--dry-run` | Show what would be captured and pushed, write nothing. |
| `--no-interactive` | Don't prompt on conflicts; treat them as skip. |

**Examples:**
```bash
friday transfer claude codex              # apply both halves
friday transfer claude codex --dry-run    # preview both halves
```

**Notes:** what actually survives the trip is what both adapters have a rule
for. A surface only `<from>` models stays in the store; nothing is invented for
`<to>`.

---

### `source` — additional content sources

**Purpose:** manage the extra git-backed content roots declared under
`sources:` in `friday.yaml` (see
[Multiple sources](#core-concepts)). Distinct from
[`remote`](#remote--the-git-bridge), which operates on the primary store's own
git remote.

**When:** you want a team or vendor rules repo composed under your own store
without merging the two histories.

**Subcommands:**
| Command | Meaning |
|---------|---------|
| `source add <name> <url> [--ref REF]` | Clone a source into `.sources/<name>` and record it in `friday.yaml`. `--ref` pins a branch, tag, or sha. |
| `source list` | List configured sources. |
| `source update [name...]` | Clone missing sources, then fetch and fast-forward the ref-less ones. |
| `source rm <name> [--force]` | Remove a configured source. `--force` skips the prompt. |

**Examples:**
```bash
friday source add team git@github.com:acme/ai-config.git
friday source add vendor https://github.com/example/rules.git --ref v2.1.0
friday source list
friday source update
friday source rm vendor
```

**Notes:** the name is validated *before* anything is cloned — a lowercase
slug, unique, and never `local` — so a name can neither escape `.sources/` nor
leave behind a manifest friday can no longer open. `add` and `rm` edit
`friday.yaml` through a YAML-node editor, so your comments and key order
survive. A pinned (`ref`) source is never fast-forwarded by `update`. Lowest
precedence first: the primary store outranks every configured source, and each
source outranks the ones listed before it.

---

### `session` — inspect or export one session

**Purpose:** the per-session detail views behind
[`friday sessions`](#sessions--the-cross-agent-session-index).

**Subcommands:**
| Command | Meaning |
|---------|---------|
| `session show <id>` | Normalized summary and turn list for one session. |
| `session export <id>` | Write a deterministic Markdown handoff brief. |

**Flags:**
| Flag | Applies to | Meaning |
|------|-----------|---------|
| `--agent NAME` | both | Resolve the id against only this agent — skips rebuilding the whole cross-agent index. |
| `--json` | `show` | Machine-readable. |
| `--out PATH` | `export` | Output path (default `./friday-handoff-<agent>-<id8>.md`). |
| `--force` | `export` | Overwrite an existing output file without prompting. |
| `--no-interactive` | `export` | Don't prompt; refuse rather than overwrite. |

**Examples:**
```bash
friday session show 3f9c1a2b --agent claude
friday session show 3f9c1a2b --json | jq '.turns | length'
friday session export 3f9c1a2b
friday session export 3f9c1a2b --out handoff.md --force
```

**Notes:** a brief embeds raw transcript text — prompts, pasted file contents,
anything a credential was pasted into — so it is written `0o600` and never
world-readable, and an existing file is overwritten only with explicit consent.
friday performs **no redaction**: read a brief before committing or sharing it.

---

### `rollback` — undo a write

**Purpose:** restore files from the snapshot friday takes before every write.

**When:** a push/pull/sync/setup/promote overwrote something you want back.

**Flow:** every write-capable command records a content-addressed snapshot first.
`rollback` restores the most recent one, or the snapshot named by an optional
`<id>` argument — which is what `--list`'s ids are for.

**Arguments:**
| Argument | Meaning |
|------|---------|
| `[id]` | Snapshot to restore (from `--list`). Omitted, the latest is used. |

**Flags:**
| Flag | Meaning |
|------|---------|
| `--list` | List recorded snapshots. |
| `--dry-run` | Show what would be restored. |

**Examples:**
```bash
friday rollback --list
friday rollback            # restore the latest snapshot
friday rollback 3f9c1a2b   # restore a specific snapshot (id from --list)
friday undo                # alias
```

---

### `remote` — the git bridge

**Purpose:** run the store's git operations without `cd`-ing into `~/.friday`.

**Subcommands:**
| Command | Meaning |
|---------|---------|
| `remote init <url>` | Set the store's origin. |
| `remote status` | Short status of the store repo. |
| `remote pull` | Fetch + fast-forward the store. |
| `remote push -m "..."` | add + commit + push the store. |
| `remote propose -m "..."` | Review-first MR (what `share` calls). |

**Examples:**
```bash
friday remote init git@github.com:me/ai-config.git
friday remote push -m "update rules"
friday remote status
```

---

### `doctor` — health check + best-practice advisor

**Purpose:** diagnose the install, lint the store, and explain mappings.

**When:** something's off, before sharing, or as a CI quality gate.

**Three modes:**
1. **`friday doctor`** — a full health check: store presence, git status, entry
   file, manifest validity (including a warning for any adapter name friday has
   no built-in preset for, and one naming `friday migrate` for a key an older
   release seeded), per-adapter installed/missing, drift across installed
   agents, hooks-wiring state (whether the target carries the store's hooks and
   is current), the drift cache location, a delegated **agnix** run when that
   binary is on `PATH`, and the **store checks** (the best-practice advisor —
   see below).
2. **`friday doctor <file>`** — explain which adapter + rule produced a target
   file, from which sources (the folded-in `explain`). Handy for "why does
   `~/.claude/CLAUDE.md` contain this?"
3. **`friday doctor --json`** — the machine-readable form for CI. The object
   carries `findings`, `summary`, `adapters`, `drift`, `agnix`, and `warnings`,
   and exits 1 only on an error-severity finding.

**Examples:**
```bash
friday doctor
friday doctor ~/.claude/CLAUDE.md
friday doctor --json
```

**Notes:** `dest-collision` is merge-aware — several rules composing one merge
target (`permissions.yaml` and `defaults.yaml` both owning
`~/.claude/settings.json`, say) is the design, not a collision — so a healthy
default store exits 0. `source-shadow` is derived from the real dry-run plan,
so entry-file variants and store files no rule references are not flagged.

See [the advisor section](#the-best-practice-advisor-in-depth) for the rules.

---

### `upgrade` — update friday itself

**Purpose:** replace the running binary with the latest release.

**When:** you installed from a release asset or `install.sh`. If a package
manager appears to own the binary, `upgrade` says so and defers to it rather
than fighting it (`--force` overrides).

**Flow:** query the release metadata, pick the asset for this OS/arch, verify
it against that release's own `checksums.txt` *before* anything is written,
then replace the binary. Every URL — metadata and asset alike — must be https
on an official release host, redirects included, or the download is refused;
the install dir is checked for writability first; and on Windows the
renamed-aside old binary is restored if the replace fails. There are no
background or implicit version checks — it runs only when you run it.

**Flags:**
| Flag | Meaning |
|------|---------|
| `--check` | Report what would be installed; download and write nothing. |
| `--force` | Install even when the release isn't newer, or when a package manager appears to own this binary. |
| `--version TAG` | Install this exact release tag instead of the latest. |

**Examples:**
```bash
friday upgrade --check
friday upgrade
friday upgrade --version v1.0.0
```

**Notes:** the checksum proves the asset matches the release that published
it — integrity, not authenticity. The checksums file ships from that same
release, so a compromised release host defeats it; there is no signature or
attestation today.

---

### `migrate` — modernize a manifest from an older release

**Purpose:** rewrite a `friday.yaml` seeded by a pre-v1.0 `friday init` into
one the current friday understands.

**When:** friday prints a manifest warning on every run — a renamed adapter key
(`antigravity` → `gemini`, `pi` → `oh-my-pi`), or a rule naming the retired
`md-to-toml`/`md-to-json` strategies.

**Flow:**
1. Each legacy adapter key is renamed to its current name, and that adapter's
   target and rules are refreshed from the built-in preset. The two halves are
   inseparable: the key changed *because* the surface moved, so the old rules no
   longer describe anything real. Keys you added yourself (`project_target`,
   `project_rules`) and the adapter's position in the file are left alone.
2. Every rule naming a retired strategy is dropped.
3. The rewrite works on the YAML node tree, so comments, key order, and every
   untouched adapter stay byte-identical.

**Flags:**
| Flag | Meaning |
|------|---------|
| `--dry-run` | Show what would change without writing. |

**Examples:**
```bash
friday migrate --dry-run
friday migrate
```

**Notes:** this is not a rescue command. A stale manifest still loads — a
retired-strategy rule is dropped with a warning instead of failing the whole
file, and a renamed key warns rather than erroring — so `migrate` is how you
stop the per-run warnings and get a renamed adapter rendering again. It is
idempotent: run it twice and the second run changes nothing.

---

### `eject` — stop managing agents entirely

**Purpose:** capture every installed agent's current targets into the store,
then drop friday's bookkeeping, leaving each agent's config standalone.

Deliberately all-or-nothing. It removes `friday.yaml`, the drift cache, and
every `friday rollback` snapshot — whole-install state with no per-agent slice
to take away — so it takes no arguments and rejects an agent name rather than
quietly ignoring it. Your store content and the agents' own config files are
left exactly where they are; to stop managing just one agent, delete its entry
from `friday.yaml`.

**Flags:**
| Flag | Meaning |
|------|---------|
| `--yes` | Skip the confirmation prompt. |

**Examples:**
```bash
friday eject
friday eject --yes
```

---

### `completion` — shell tab-completion

Generated by the CLI framework from the live command graph, so it can't drift
from the real commands; dynamic values (adapter names) complete too.

```bash
friday completion bash > /etc/bash_completion.d/friday
friday completion zsh  > "${fpath[1]}/_friday"
friday completion fish > ~/.config/fish/completions/friday.fish
```

---

### `version`

```bash
friday version
```

### `help`

```bash
friday help          # the seven everyday commands
friday help --all    # the full toolbox + flag details + examples
friday <cmd> --help  # one command's flags
```

---

## End-to-end workflows

### 1. Solo developer, new machine
```bash
friday init --from-git git@github.com:me/ai-config.git   # clone your store
friday sync                                             # apply to every installed agent
friday status                                           # confirm everything's in sync
```

### 2. Bootstrap the store from an existing install
*You've been using Claude Code for months; nothing is in a store yet.*
```bash
friday claim                    # scaffold, then capture every installed agent
friday status                   # review
friday remote init git@github.com:me/ai-config.git
friday remote push -m "seed store from this machine"
```

### 3. Team standards
*A shared team-config repo; every developer points at it.*
```bash
friday init --from-git git@github.com:acme/ai-config.git
friday sync
# later, you improve a rule:
friday share -m "require conventional-commit messages"   # opens an MR for review
```

### 4. Author a skill in a project, promote it, share it
```bash
# in the project, you wrote .claude/skills/release-notes/
friday promote .claude/skills/release-notes              # lift it into ~/.friday
friday sync                                              # fan it to your other agents
friday share -m "add release-notes skill"                # propose it to the team
```

### 5. CI gate on drift
```bash
friday status --check      # exit 2 if any agent is out of sync with the store
# or, machine-readable:
friday status --json | jq '.summary'
friday doctor --json       # lint the store; exit 1 on error-severity findings
# applying in CI: exit 2 means someone edited an agent dir by hand
friday push --no-interactive
```

### 6. Layer a team repo under your own store
```bash
friday source add team git@github.com:acme/ai-config.git   # clones to .sources/team
friday source list
friday status --origin      # see which root each mapping resolves from
friday sync
# your own store outranks the source; edit team content in its own repo,
# or copy the file into ~/.friday to override it locally
```

### 7. Move to a different agent
```bash
friday transfer claude codex --dry-run   # preview both halves
friday transfer claude codex
```

### 8. Upgrading from a pre-v1.0 install
```bash
friday migrate --dry-run   # what a stale friday.yaml would become
friday migrate             # rename legacy adapter keys, drop retired rules
friday doctor              # confirm the manifest is clean
```

---

## The best-practice advisor (in depth)

`friday doctor` includes a **one-shot linter** for how you author your agent
config. It reads your store on disk, reports where you diverge from good
practice, and exits. It is **not** a resident process and never observes runtime
behavior — that boundary is deliberate (see below).

**Severity:**
- **error** — a structural problem that will misbehave. Fails `friday doctor`
  (non-zero exit).
- **warn** — an advisory best-practice nudge. Printed, but does **not** fail the
  check.

**The rules:**

| Rule id | Severity | Flags when… |
|---------|----------|-------------|
| `frontmatter` | error | A `.md` file has malformed YAML frontmatter. |
| `dest-collision` | error | Two rules write the same destination and at least one replaces the whole file, so it discards what the others wrote. Rules that all *compose* one target (`permissions.yaml` and `defaults.yaml` merging into the same `settings.json`) are the documented layout and are not reported. |
| `mcp-secret` | error | A value in any store `.yaml`/`.yml`/`.json`/`.toml` config file looks like a real credential rather than an env reference — use `${VAR}`. Covers the canonical schemas (`mcp.yaml`, `permissions.yaml`, `defaults.yaml`) and both passthrough escape hatches (`settings/<adapter>.*`, `native/<adapter>/**`), because a credential is no safer for sitting in a file friday does not model. |
| `plan` | error | The store's render plan could not be computed at all, so the plan-derived checks below could not run. |
| `oversized` | warn | A file exceeds 64 KiB (bloats any context window). |
| `broken-ref` | warn | A relative Markdown link resolves to nothing. |
| `long-instructions` | warn | An entry file (`core.md`) or a `rules/*.md` exceeds 200 lines — split focused topics so agents load only what they need. |
| `skill-description` | warn | A `skills/*/SKILL.md` has no description, or one under 20 chars — the description is the trigger the agent matches on; without it the skill never fires. |
| `source-shadow` | warn | A file in a configured source is shadowed by a higher-precedence copy of the same path, so it never contributes to any push. |
| `source-manifest` | warn | A configured source carries its own `friday.yaml`, which is ignored — only the primary store's manifest is read. |
| `agnix/<RuleID>` | as agnix reports it | Delegated from the external [agnix](#delegated-content-linting-agnix) content linter, when it is installed. |

`mcp-secret` is **error**-severity, so it fails `friday doctor` and
`doctor --json` in CI. Every finding carries a **self-contained fix hint**.

**Silencing a rule** (per store — versions alongside your content) in
`~/.friday/.friday-doctor.yaml`. Delegated ids are silenced exactly like
first-party ones:
```yaml
disable:
  - broken-ref
  - long-instructions
  - agnix/CC-SK-007
```

**CI form:**
```bash
friday doctor --json
```
```json
{
  "findings": [
    { "rule": "skill-description", "severity": "warn",
      "path": "skills/foo/SKILL.md", "message": "no description in frontmatter — ..." }
  ],
  "summary": { "error": 0, "warn": 1 },
  "adapters": [],
  "drift": [],
  "agnix": { "status": "not-found" },
  "warnings": []
}
```
Exits 1 only when there's an error-severity finding; warnings alone exit 0.
`adapters` and `drift` mirror the human health check, `agnix` reports the
delegated run, and `warnings` carries any manifest warning (a dropped
retired-strategy rule, a renamed adapter key) friday printed this run.

**Why it stops here (the scope boundary).** The advisor checks *static config
state*. It deliberately does **not** check *runtime operating judgment* —
context-budget discipline, when to `/compact` vs `/clear`, per-turn choices —
because a one-shot tool that reads files once and exits can't observe those, and
building something that could would mean a resident daemon, which friday rejects
by design. Curated prose guidance (e.g. community best-practice repos) is
*inspiration for authoring rules*, not something friday consumes at check time.
Git-sourced community rule packs are a possible future addition; today the rule
set is first-party.

### Delegated content linting (agnix)

`friday doctor` does not reimplement general content linting. If an `agnix`
binary is on `PATH`, doctor runs it over each source root with
`--format sarif` and folds its results in under an `agnix/` rule prefix. The
delegation is bounded: the child process has a timeout, its stdout is
size-capped, arguments are terminated with `--`, and the SARIF `version` must
be 2.x — output friday cannot read is reported as a failed run, never as
"clean". A missing binary is not a failure: it is an opt-in tool, and friday's
own checks still ran.

---

## Safety model

- **Atomic writes** — temp file + `fsync` + rename; Ctrl-C never leaves a
  half-written file.
- **Drift detection** — SHA256 baselines; friday refuses to overwrite a target
  you edited directly. Resolve interactively, or with `--force`.
- **Bidirectional** — `pull` checks the target's own baseline first, so it won't
  overwrite a newer store with stale target content; both-sides-changed files
  become conflicts, not silent losses.
- **Snapshots** — every write-capable command records a content-addressed
  snapshot; [`rollback`](#rollback--undo-a-write) restores it.
- **Hook code is never installed unattended** — a write that carries hook
  content is confirmed first, and the decision is keyed on the *content*, not
  on which rule produced it, so a `native/<adapter>/` mirror cannot smuggle an
  executable hook past the prompt. `--no-interactive` (no confirmer) skips such
  a write; `--force` bypasses the prompt.
- **Configured sources are read-only** — friday never writes into
  `.sources/<name>`, so a `pull` can't rewrite someone else's repo behind your
  back.
- **Session reads never write** — every session reader is read-only, and
  `session export` writes only the brief you asked for: `0o600`, refusing to
  overwrite an existing file without `--force`, and rollback-journaled like any
  other write.
- **CRLF tolerance** — line-ending differences never register as drift.
- **No secrets** — the scaffolded `.gitignore` filters `.env`, `*.key`, `*.pem`,
  and runtime state dirs; secrets belong in env vars or a secret manager, not the
  store.

---

## Exit codes (for CI)

| Command | Exit 0 | Exit 1 | Exit 2 |
|---------|--------|--------|--------|
| `status` (default) | no conflicts | error | a drift conflict exists |
| `status --json` | no conflicts | error | a drift conflict exists |
| `status --check` | fully in sync | error | anything out of sync (drift *or* pending render) |
| `push` | applied, no conflicts | error (bad manifest, unknown adapter, unreadable store) | a change is a conflict — friday refused to clobber a target you edited |
| `pull` | applied, no conflicts | error | a change is a conflict, or stdin closed before anything was applied |
| `sync` | both phases clean | error in either phase | a conflict in either phase (the pull phase's code short-circuits the push) |
| `claim` / `transfer` | applied, no conflicts | usage or load error | a change is a conflict |
| `doctor` | healthy | a problem / error-severity finding | — |
| `doctor --json` | no error-severity findings | error-severity finding present | — |

`--check` is the strict gate (any divergence fails); the default/`--json`
`status` exit keys only on true conflicts, preserving the historical contract.

Exit **2** always means the same thing across the write commands: friday found
a target you had edited directly and refused to overwrite it. That is the code
a CI job should special-case — treat it as "someone changed an agent dir by
hand", not as an unclassified failure. `friday status --check` is the right
gate when you want *any* divergence to fail, including pending renders that
are not conflicts at all.
