# Security Policy

## Supported versions

Security fixes ship on the latest release tag only — there are no backports to
older tags, so please upgrade before reporting an issue against an older binary.
`friday upgrade` is the supported remediation path (see the self-update trust
model below); an install a package manager owns is upgraded through that manager.

| Version    | Supported |
| ---------- | --------- |
| latest     | yes       |
| < latest   | no        |

## What we treat as a security issue

friday is a file-management CLI, but it is no longer only that. It self-updates
over the network, executes an external linter found on `PATH`, clones arbitrary
git remotes and renders their content into agent configuration, writes hook code
that an agent runs on tool use, and reads conversation transcripts. Each of those
is a trust boundary, described in the next section.

- **Path traversal.** A malicious `friday.yaml` (e.g. cloned from a hostile remote) using `..` to write outside the configured target dir. A `sources:` name that escapes `.sources/` is the same class: `config.ValidateSourceName` restricts a name to `^[a-z0-9][a-z0-9-]*$` (and never `local`), and both the manifest loader and `friday source add` apply it — a name that still escapes is in scope.
- **Argument injection into git.** A URL or ref that escapes `git clone` / `git checkout` to run additional flags. Mitigated by `ValidateURL` / `ValidateRef` (both reject a leading `-`) plus the `--` separator where the subcommand has one; `git checkout` does not have a usable one, so ref validation is the only control there.
- **Command execution through a git transport.** `--` stops flag parsing but not git's transport layer, and `ext::<command>` / `fd::<n>` execute rather than speak a wire protocol. Two controls, because either alone leaks: `ValidateURL` rejects the `transport::address` form and any scheme outside https/http/ssh/git/file, and every invocation friday makes pins `GIT_ALLOW_PROTOCOL` to that same list. It has to be the environment variable — `GIT_ALLOW_PROTOCOL` overrides `protocol.*.allow` config, so a machine already exporting a relaxed list for unrelated tooling defeats the config form outright. A URL that reaches a transport outside that list is in scope.
- **Secrets leaking into commits.** The scaffolded `.gitignore` filters obvious secrets, but a thoughtful report on what else should be filtered is welcome. `friday doctor` separately reports a literal credential in any store `.yaml`/`.yml`/`.json`/`.toml` config file — the canonical schemas and both passthrough escape hatches — where a `${VAR}` reference belongs (rule `mcp-secret`, error severity, so it fails `doctor` in CI). friday resolves no variable references, so a `${VAR}` reaches the agent's config verbatim. A bypass of that check is in scope.
- **Atomic-write races.** Anything that could leave a target file half-written or world-readable when it shouldn't be.
- **Escaping a documented trust boundary.** Installing hook code without the confirmation described below, installing a release asset that was not checksum-verified or did not come from an official host, or writing into a configured source checkout as if it were the primary store.

## Trust boundaries

### Self-update (`friday upgrade`)

Self-update runs only when you invoke it — friday performs no background or
implicit version checks. What the code enforces, in the order it enforces it:

- If a package manager (brew/snap/scoop) appears to own the binary, friday refuses to install over it and prints that manager's own upgrade command instead. `--force` overrides.
- Releases come from friday's own GitHub repository (`zhivko-kocev/friday`) through `https://api.github.com`. The archive URL and the `checksums.txt` URL both arrive verbatim in the release JSON, so both are validated before any bytes move: https only, and the host must be on the allowlist (`api.github.com`, `github.com`, `objects.githubusercontent.com`, `release-assets.githubusercontent.com`). Redirects are re-validated per hop, so a 302 out of a valid asset URL cannot walk the download off https or off an official host.
- Downloads are size-capped (64 MiB) and time-bounded (5 minutes).
- The archive's SHA-256 must match its entry in that same release's `checksums.txt`. This is the last gate before any write: a mismatch aborts before the binary is replaced, and the install directory's writability is proven *before* the download, so an unwritable location is a refusal rather than a wasted download.
- The replacement is atomic. On Windows the running binary is renamed aside first and restored if the write then fails, so a failed upgrade never leaves you with no binary at all.

What this does **not** give you is authenticity. `checksums.txt` is published by
the same release as the asset it describes, so verification proves the bytes are
the ones that release advertises — not who produced them. There is no signature
or build attestation today. Anyone who can publish a release under this
repository, or who compromises the release host, defeats the checksum gate.
Reports of an attack that works *within* those constraints are in scope: a bypass
of the host or scheme pinning, a path where the checksum is skipped, a silent
downgrade.

### Configured sources (`sources:`)

A `sources:` entry is a git remote friday shallow-clones into
`~/.friday/.sources/<name>` and renders into your agents' configuration. **Its
content is arbitrary third-party content with a path into your agent's shell.** A
source offers every store surface the primary store does, including `hooks/`
fragments, the OpenCode/oh-my-pi plugin files, and `native/<adapter>/**/*`
mirrors that land verbatim in a directory the agent auto-executes from. friday
validates the name, URL and ref, and treats the checkout as read-only (nothing
pushes or pulls back into it, and a capture whose winning file lives there is
refused rather than written) — but it does not review the content. Treat adding a
source like adding a dependency: read it first, pin `ref:` to something you have
reviewed, and remember that `friday source update` moves that pin forward.

### Hook execution

A hook is a shell command an agent runs on its own tool use, so every write that
installs hook code is confirmed first. The gate keys on the content being
written: a merge whose store source lives under `hooks/`, and any `copy` rule
marked `confirm_hook` — the OpenCode/oh-my-pi guard plugins plus every
`native/<adapter>/**/*` mirror. `friday push` and `friday setup` print the exact
commands and prompt on stdin.

- The default is **not** to write: a declined prompt, or no confirmer at all, downgrades the change to a skip.
- `--no-interactive` supplies no confirmer, so it **skips** hook writes rather than accepting them.
- `--force` bypasses the prompt and writes. Use it only against a store you control.
- A dry run (`friday status`) plans the real action but never prompts and never writes.

In scope: any path that installs hook code without passing that gate. Not in
scope: hook code you confirmed, or a store you chose to `--force`.

### External binary execution (`agnix`)

`friday doctor` delegates content linting to an `agnix` binary when one is on
`PATH`, running it once per source root. friday resolves it with a plain `PATH`
lookup, so **a hostile `PATH` entry named `agnix` gets executed.** That is the
same exposure any tool shelling out to a helper has; it is stated here because it
is not obvious from the command name. The child process is bounded: a 30-second
timeout, an 8 MiB stdout cap, a `--` terminator before the directory argument,
and a declared SARIF version that must be `2.x` — anything else is reported as a
failed run rather than a clean bill of health. A path that escapes those bounds,
or that reports health friday did not actually verify, is in scope.

### Session transcripts

`friday sessions` / `session show` / `session export` read the local conversation
stores of the installed agents. Those readers are read-only — `internal/session`
contains no writer, and friday never writes into an agent's own session store.
friday performs **no redaction**: a transcript routinely contains tokens,
credentials, and proprietary source. `session export` therefore refuses to
overwrite an existing file without consent (`--force`, or an interactive yes;
`--no-interactive` refuses instead of prompting), writes through the atomic
writer at mode `0600`, and journals the previous content so `friday rollback` can
restore it. A path that widens those permissions, writes outside the requested
output path, or silently overwrites an existing file is in scope.

## What is not a security issue

- friday refusing to overwrite drift without `--force` (that's the intended UX)
- Errors from running `friday` against a missing/empty store (that's a config bug, not a vuln)
- `--force` doing what it documents — bypassing the hook prompt, or installing over a package-manager-owned binary
- `friday upgrade` deferring to brew/snap/scoop instead of replacing the binary itself

## Reporting a vulnerability

**Do not open a public GitHub issue for vulnerabilities.**

Email the maintainer at the address listed on the [GitHub profile](https://github.com/zhivko-kocev), or use GitHub's [private vulnerability reporting](https://github.com/zhivko-kocev/friday/security/advisories/new) feature.

Please include:

- friday version (`friday version`)
- OS / Go version
- Reproduction steps — minimal `friday.yaml` + commands
- Impact (what an attacker can do)

You'll get an acknowledgment within 7 days. Fixes target the next patch release; we'll coordinate disclosure timing with you.

## Out-of-scope

- Vulnerabilities in the Go stdlib or in friday's dependencies — report those to their respective projects. The ones that parse untrusted input are `go.yaml.in/yaml/v3`, `github.com/pelletier/go-toml/v2`, `github.com/tailscale/hujson`, `mvdan.cc/sh/v3`, and `github.com/urfave/cli/v3`. A report that friday fed one of them input it should have rejected first *is* in scope.
- Social engineering of users into cloning a repo they shouldn't (`friday init --from-git <bad-url>`). friday clones what you tell it to; treat untrusted repos accordingly. This does **not** extend to what a cloned manifest can then do: a hostile `friday.yaml` is in scope for both of the classes above, because the user consented to cloning a repo, not to its `sources:` entries executing commands or writing outside `.sources/` on the next `friday source update`.
