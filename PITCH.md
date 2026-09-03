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
friday init --from-git git@github.com:me/ai-config.git
# edit ~/.friday/core.md, rules/*.md, skills/*/, ...
friday push
# writes ~/.claude/CLAUDE.md, ~/.codex/AGENTS.md, ~/.config/opencode/AGENTS.md, ~/.copilot/copilot-instructions.md
friday remote push -m "tweak rules"
# git add -A && commit && push, all from one command
```

That's it. No proprietary format. No hosted service. No daemon.

---

## What you get

- **One canonical store**, version-controlled in a git repo of your choice —
  and composable: `sources:` in `friday.yaml` layers additional git remotes
  under it, so a team repo and your personal store coexist without merging.
- **Structured agent config from one source, not six.** `mcp.yaml`,
  `permissions.yaml`, and `defaults.yaml` are friday's own canonical schemas;
  it renders each one into whatever shape the agent actually reads — JSON, TOML,
  a co-owned `settings.json` key, or a policy file — so an MCP server or a
  deny rule is written once.
- **Six built-in presets** — Claude Code, Codex, OpenCode, GitHub Copilot,
  Gemini CLI, and oh-my-pi. A preset is layout data: a new one that maps
  Markdown into an agent's own dirs is a short block in
  `internal/presets/presets.go`. Wiring it into the canonical MCP/permissions/
  defaults renderers means adding it to `internal/render` too, where each
  adapter's native shape is implemented.
- **Markdown for knowledge, YAML for config.** Your rules, identity, agents,
  and skills stay plain Markdown. Only the structured settings that were never
  prose — servers, permissions, model defaults — are YAML, and both are
  human-readable and diffable. No manifest is required: friday seeds one on
  init with every built-in preset, and you can hand-edit or delete entries.
- **Your sessions, in one place.** `friday sessions` indexes conversations
  across every installed agent, read-only; `friday session export` writes a
  deterministic handoff brief so work can move from one agent to another.
- **Round-trip with conflict detection.** Edit a target file directly?
  `friday pull` brings it back, for every rule that has an inverse — a copy, or
  a rendered MCP/permissions merge. Pushed-then-edited? friday detects drift and
  prompts before overwriting. The one-way rules are listed in
  [docs/USE_CASES.md](docs/USE_CASES.md); friday reports them rather than
  guessing.
- **Atomic writes.** Every target file is written via temp file + rename.
  Ctrl-C mid-write leaves the previous version intact.
- **Git management built in.** `friday remote pull / push -m "..." / status`.
  No need to `cd` anywhere.

---

## Standard layout (the contract)

```
core.md               the entry file (becomes the head of CLAUDE.md / AGENTS.md)
rules/*.md            behaviour rules — concatenated after core.md, one file per agent
agents/*.md           sub-agent definitions (frontmatter adapted per agent)
commands/*.md         slash-commands / prompts
skills/<name>/*       skills, mirrored verbatim (frontmatter trimmed per agent)
mcp.yaml              canonical MCP server list
permissions.yaml      canonical allow/ask/deny policy
defaults.yaml         canonical model / effort / theme / statusline / telemetry
hooks/**              per-agent hook sources — wired confirm-first, never unattended
native/<adapter>/**   verbatim mirror into that adapter's own config dir
settings/<adapter>.{json,toml}
                      extra keys the canonical schemas don't own
friday.yaml           adapter manifest (and `sources:`) — auto-seeded by init
```

Repositories that follow this layout work with `friday push` out of the box.
Customize per-machine by editing `friday.yaml`; the manifest version-controls
your overrides alongside the content.

---

## Use cases

**Solo developer with multiple machines.** Sync your AI tooling identity
across laptops and desktops. `friday init --from-git <url>` on a fresh machine.

**Team standards.** A team-config repo with shared rules, agent personas, and
skills. Every developer points `friday init --from-git` at the team repo.
Updates land everywhere with one push.

**Onboarding new agents.** A new tool releases tomorrow. Add its layout to
`internal/presets/presets.go`; if it should also receive the canonical MCP,
permissions, and defaults schemas, add its native shape to `internal/render`.
The next `friday push` includes it.

---

## Why not a framework, a daemon, a VS Code extension?

- **No daemon** — config is text files, friday is a CLI. Your editor of
  choice, your shell of choice.
- **No proprietary format** — Markdown for knowledge, YAML for structured
  config. Read by humans, versioned by git, searchable by grep.
- **No magic** — every change is reported in a per-adapter section of stdout.
  Conflicts surface on stderr with an interactive resolver.
- **No lock-in** — `rm -rf ~/.friday` and you're back to the agents'
  native configs.

---

## The architecture in one slide

```
┌───────────────────────────────────────────────────────────────┐
│ ~/.friday/   one repo: markdown knowledge + canonical YAML    │
│   core.md  rules/*.md  agents/*.md  commands/*.md             │
│   skills/<name>/*                                             │
│   mcp.yaml  permissions.yaml  defaults.yaml                   │
│   hooks/**  native/<adapter>/**  settings/<adapter>.*         │
└─────────────────────┬─────────────────────────────────────────┘
                      │  friday push  (presets + rule engine + renderers)
                      ▼
┌─────────────────────┬───────────────────┬─────────────────────┐
│ ~/.claude/          │ ~/.codex/         │ ~/.copilot/         │
│ CLAUDE.md           │ AGENTS.md         │ copilot-instr..md   │
│ agents/ commands/   │ agents/*.toml     │ agents/*.agent.md   │
│ skills/             │ prompts/          │ mcp-config.json     │
│ settings.json       │ config.toml       │ settings.json       │
│ ~/.claude.json      │ hooks.json        │ hooks/*.json        │
├─────────────────────┼───────────────────┼─────────────────────┤
│ ~/.config/opencode/ │ ~/.gemini/        │ ~/.omp/agent/       │
│ AGENTS.md           │ GEMINI.md         │ AGENTS.md           │
│ agents/ commands/   │ agents/           │ agents/ commands/   │
│ opencode.json       │ commands/*.toml   │ skills/             │
│ tui.json            │ settings.json     │ mcp.json            │
│ plugins/*.ts        │ policies/*.toml   │ extensions/*.ts     │
└─────────────────────┴───────────────────┴─────────────────────┘
```

Codex, Copilot, Gemini, and OpenCode all discover Agent-Skills-standard
skills under one shared `~/.agents/skills/` tree, so friday writes it once
instead of four times; Claude and oh-my-pi read their own `skills/` dir at a
higher discovery priority and get their own copy above.

Same source, every agent. One sync.

*Stop maintaining four config files for the same idea. Maintain one.*
