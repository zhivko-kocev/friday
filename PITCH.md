---
title: "friday"
subtitle: "One config, every AI agent."
author: "kocew"
geometry: margin=1in
fontsize: 11pt
---

# friday

**One config. Every AI agent.**

You use Claude Code at home, Codex at work, OpenCode on your laptop, and your
team standardized on Copilot last quarter. They each want their config in a
different place, in a different format, with subtly different conventions.
Updating "be terse, lead with the answer" means editing four files in four
folders. You stop bothering. Your agents drift apart.

`friday` is a Go CLI that fixes this. Write your rules, identity, agents, and
skills once — in plain Markdown — and `friday push` writes them to every tool
the right way.

---

## The pitch in 30 seconds

```bash
friday init --remote git@github.com:me/ai-config.git
# edit ~/.friday/core.md, rules/*.md, skills/*/, ...
friday push
# writes ~/.claude/CLAUDE.md, ~/.codex/AGENTS.md, ~/.config/opencode/AGENTS.md, ~/.copilot/copilot-instructions.md
friday remote push -m "tweak rules"
# git add -A && commit && push, all from one command
```

That's it. No proprietary format. No hosted service. No daemon.

---

## What you get

- **One canonical store**, version-controlled in a git repo of your choice.
- **Four built-in presets** for Claude Code, Codex, OpenCode, and GitHub Copilot.
  Add a fifth in 30 lines of Go.
- **Plain Markdown content.** Your config repo is just `core.md`, `rules/*.md`,
  `skills/foo/*`. No yaml manifest required — friday seeds one on init with
  every built-in preset, but you can hand-edit or delete entries freely.
- **Round-trip with conflict detection.** Edit a target file directly?
  `friday pull` brings it back. Pushed-then-edited? Friday detects drift and
  prompts before overwriting.
- **Atomic writes.** Every target file is written via temp file + rename.
  Ctrl-C mid-write leaves the previous version intact.
- **Git management built in.** `friday remote pull / push -m "..." / status`.
  No need to `cd` anywhere.

---

## Standard layout (the contract)

```
core.md               the entry file (becomes the head of CLAUDE.md / AGENTS.md)
rules/*.md            behaviour rules — concatenated into one file per agent
agents/*.md           Claude sub-agent definitions
commands/*.md         Claude slash-commands
skills/<name>/*       skills, mirrored verbatim (frontmatter trimmed per agent)
friday.yaml           adapter manifest — auto-seeded by init
```

Repositories that follow this layout work with `friday push` out of the box.
Customize per-machine by editing `friday.yaml`; the manifest version-controls
your overrides alongside the content.

---

## Use cases

**Solo developer with multiple machines.** Sync your AI tooling identity
across laptops and desktops. `friday init --remote <url>` on a fresh machine.

**Team standards.** A team-config repo with shared rules, agent personas, and
skills. Every developer points `friday init --remote` at the team repo.
Updates land everywhere with one push.

**Onboarding new agents.** A new tool releases tomorrow. Add a 30-line preset
to `internal/presets/presets.go`. The next `friday push` includes it.

---

## Why not a framework, a daemon, a VS Code extension?

- **No daemon** — config is text files, friday is a CLI. Your editor of
  choice, your shell of choice.
- **No proprietary format** — Markdown in, Markdown out. Read by humans,
  versioned by git, searchable by grep.
- **No magic** — every change is reported in a per-adapter section of stdout.
  Conflicts surface on stderr with an interactive resolver.
- **No lock-in** — `rm -rf ~/.friday` and you're back to the agents'
  native configs.

---

## The architecture in one slide

```
┌─────────────────────────────────────────────────────────────┐
│  ~/.friday/    ← one repo, plain markdown                   │
│    core.md                                                  │
│    rules/*.md                                               │
│    agents/*.md                                              │
│    skills/*/                                                │
└──────────────────┬──────────────────────────────────────────┘
                   │  friday push  (presets + rule engine)
                   ▼
┌──────────────┬──────────────┬────────────────────┬───────────────┐
│  ~/.claude/  │  ~/.codex/   │ ~/.config/opencode │  ~/.copilot/  │
│  CLAUDE.md   │  AGENTS.md   │  AGENTS.md         │  copilot-     │
│  agents/*    │              │  rules/*.md        │  instructions │
│  commands/*  │              │  skills/*/         │  .md          │
│  skills/*/   │              │                    │               │
└──────────────┴──────────────┴────────────────────┴───────────────┘
```

Same source, four flavours. One push.

*Stop maintaining four config files for the same idea. Maintain one.*
