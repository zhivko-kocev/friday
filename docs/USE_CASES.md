# friday — use cases & command guide

A task-oriented manual for `friday` v0.5.0. Every command is explained with what
it does, when to reach for it, the full flow (including what happens under the
hood), its flags, and worked examples. If you just want the elevator pitch, read
the [README](../README.md); this document is the deep end.

---

## Table of contents

- [Core concepts](#core-concepts) — read this first
- [The everyday five](#the-everyday-five)
  - [`init`](#init--create-or-clone-your-store) · [`sync`](#sync--the-daily-driver) · [`status`](#status--what-would-change-the-two-axis-grid) · [`setup`](#setup--put-knowledge-into-a-project) · [`share`](#share--propose-changes-to-your-team)
- [Advanced commands](#advanced-commands)
  - [`push`](#push--one-way-store--agents) · [`pull`](#pull--one-way-agents--store) · [`promote`](#promote--project-config-back-into-the-store) · [`rollback`](#rollback--undo-a-write) · [`remote`](#remote--the-git-bridge) · [`doctor`](#doctor--health-check--best-practice-advisor) · [`eject`](#eject--stop-managing-an-agent) · [`completion`](#completion--shell-tab-completion) · [`version`](#version) · [`help`](#help)
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
  rules/*.md         behaviour rules — concatenated for Claude/Codex/Copilot, split for OpenCode
  agents/*.md        subagent definitions (frontmatter adapted per agent dialect)
  commands/*.md      slash commands / prompts
  skills/<name>/     agent skills, mirrored verbatim (frontmatter trimmed per agent)
  standards/*.md     per-language baselines — stay in the store, reached by reference
  hooks/**           hooks.json merged into ~/.claude/settings.json (confirm-first); scripts run from the store
  connectors/*.md    connector docs — reference copies
  friday.yaml        adapter manifest (optional; auto-seeded on `init --scaffold`)
  .friday-doctor.yaml  optional lint config (see the advisor)
```

**Rules & rendering.** Each adapter in `friday.yaml` (or a built-in preset) is a
list of *rules*, each with a strategy:
- **concatenate** — join several store files into one target (e.g. `core.md` +
  `rules/*.md` → `CLAUDE.md`). Not reversible, so `pull` skips these.
- **copy** — mirror a file or a tree (e.g. `skills/` → `~/.claude/skills/`).
  Reversible, so `pull` can capture edits back.

Every rule rewrites the Claude-plugin path variable `${CLAUDE_PLUGIN_ROOT}` to
`~/.friday` on push (and back on pull), so plugin-shaped knowledge repos (like
developer-os) work unmodified.

**Presets & the manifest.** friday ships **seven built-in presets** (claude,
codex, copilot, opencode, windsurf, antigravity, pi). If `~/.friday/friday.yaml`
exists it is *authoritative* — friday uses exactly those adapters. If it's
absent, friday falls back to the built-in presets. To customize, add or edit
adapters in `friday.yaml` (it versions with your store).

**Drift & baselines (the safety core).** Every time friday writes a target file
it records that file's SHA256. Before overwriting, it re-checks the hash:
- match → friday wrote it last, safe to update;
- mismatch → *you* edited it directly (drift) — friday refuses to clobber it and
  either prompts (interactive) or reports a conflict (non-interactive).

Writes are **atomic** (temp file + rename), so Ctrl-C mid-write never leaves a
half-written file. Line-ending differences (CRLF vs LF) are normalized, so a
Windows checkout of LF-authored files isn't flagged as drift.

**Interactive vs. non-interactive.** On a real terminal, running bare `friday`
(no arguments) opens the full-screen **control room** — a menu-driven TUI over
`sync` / `setup` / `status` / `share` / `discover`, with cold-start for `init` on
a fresh machine — that previews every change before applying and resolves
conflicts in a diff modal (keep / take / skip). Individual commands show rich
interactive bits too (pickers, a conflict resolver, spinners). When stdout/stdin
isn't a TTY — pipes, CI — or you pass `--no-interactive`/`--json`, output is plain
and byte-stable, conflicts are reported rather than prompted, and the control
room never opens. Color obeys `NO_COLOR` / `FRIDAY_NO_COLOR` / `--no-color`.

**The two-axis status model.** `friday status` shows each managed file on two
independent axes (borrowed from chezmoi):
- **column 1 — local drift:** you edited the *target* since friday last wrote it
  (an edit `pull`/`sync` would capture).
- **column 2 — pending render:** the *store* changed and a `push`/`sync` would
  update the target.

A file can be dirty on either, both (a conflict), or neither (in sync).

---

## The everyday five

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

**Notes:** `~/.friday` must not already exist for a clone. If you already have
agents configured but no store, scaffold then [`pull --discover`](#pull--one-way-agents--store) to seed from disk.

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

**Notes:** concatenate targets (like `CLAUDE.md`) can't be pulled — many store
files map into one, which isn't reversible; friday reports those as unsupported.

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
store and out to the team.

---

### `rollback` — undo a write

**Purpose:** restore files from the snapshot friday takes before every write.

**When:** a push/pull/sync/setup/promote overwrote something you want back.

**Flow:** every write-capable command records a content-addressed snapshot first.
`rollback` restores the most recent one.

**Flags:**
| Flag | Meaning |
|------|---------|
| `--list` | List recorded snapshots. |
| `--dry-run` | Show what would be restored. |

**Examples:**
```bash
friday rollback --list
friday rollback            # restore the latest snapshot
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
   file, manifest validity, per-adapter installed/missing, drift across
   installed agents, hooks-wiring state (whether `settings.json` carries the
   store's hooks and is current), the drift cache location, and the **store
   checks** (the best-practice advisor — see below).
2. **`friday doctor <file>`** — explain which adapter + rule produced a target
   file, from which sources (the folded-in `explain`). Handy for "why does
   `~/.claude/CLAUDE.md` contain this?"
3. **`friday doctor --json`** — emit the store-check findings as diagnostics for
   CI; exits 1 only on an error-severity finding.

**Examples:**
```bash
friday doctor
friday doctor ~/.claude/CLAUDE.md
friday doctor --json
```

See [the advisor section](#the-best-practice-advisor-in-depth) for the rules.

---

### `eject` — stop managing an agent

**Purpose:** capture an agent's current targets into the store, then drop friday's
bookkeeping for it, leaving the agent's config standalone.

**Flags:**
| Flag | Meaning |
|------|---------|
| `--yes` | Skip the confirmation prompt. |

**Examples:**
```bash
friday eject claude
friday eject claude --yes
```

---

### `completion` — shell tab-completion

Generated from the command registry, so it can't drift from the real commands.

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
friday help          # the five everyday commands
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
friday init --scaffold          # empty store
friday pull --discover          # walk ~/.claude etc. and capture what's there
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

**The rules (v0.3.0):**

| Rule id | Severity | Flags when… |
|---------|----------|-------------|
| `frontmatter` | error | A `.md` file has malformed YAML frontmatter. |
| `dest-collision` | error | Two rules write the same destination (last one silently wins). |
| `oversized` | warn | A file exceeds 64 KiB (bloats any context window). |
| `broken-ref` | warn | A relative Markdown link resolves to nothing. |
| `long-instructions` | warn | An entry file (`core.md`) or a `rules/*.md` exceeds 200 lines — split focused topics so agents load only what they need. |
| `skill-description` | warn | A `skills/*/SKILL.md` has no description, or one under 20 chars — the description is the trigger the agent matches on; without it the skill never fires. |

Every finding carries a **self-contained fix hint**.

**Silencing a rule** (per store — versions alongside your content) in
`~/.friday/.friday-doctor.yaml`:
```yaml
disable:
  - broken-ref
  - long-instructions
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
  "summary": { "error": 0, "warn": 1 }
}
```
Exits 1 only when there's an error-severity finding; warnings alone exit 0.

**Why it stops here (the scope boundary).** The advisor checks *static config
state*. It deliberately does **not** check *runtime operating judgment* —
context-budget discipline, when to `/compact` vs `/clear`, per-turn choices —
because a one-shot tool that reads files once and exits can't observe those, and
building something that could would mean a resident daemon, which friday rejects
by design. Curated prose guidance (e.g. community best-practice repos) is
*inspiration for authoring rules*, not something friday consumes at check time.
Git-sourced community rule packs are a possible future addition; today the rule
set is first-party.

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
| `doctor` | healthy | a problem / error-severity finding | — |
| `doctor --json` | no error-severity findings | error-severity finding present | — |

`--check` is the strict gate (any divergence fails); the default/`--json` exit
keys only on true conflicts, preserving the historical contract.
