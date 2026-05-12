# Security Policy

## Supported versions

friday is pre-1.0. Security fixes ship on the latest release tag — please upgrade before reporting issues against older binaries.

| Version    | Supported |
| ---------- | --------- |
| latest     | ✅        |
| < latest   | ❌        |

## What we treat as a security issue

friday is a file-management CLI. The threat model is narrow:

- **Path traversal.** A malicious `friday.yaml` (e.g. cloned from a hostile remote) using `..` to write outside the configured target dir.
- **Argument injection into git.** A URL that escapes `git clone` to run additional flags (we mitigate this with `ValidateURL` + the `--` separator).
- **Secrets leaking into commits.** The scaffolded `.gitignore` filters obvious secrets, but a thoughtful report on what else should be filtered is welcome.
- **Atomic-write races.** Anything that could leave a target file half-written or world-readable when it shouldn't be.

Things that are *not* security issues:

- friday refusing to overwrite drift without `--force` (that's the intended UX)
- Errors from running `friday` against a missing/empty store (that's a config bug, not a vuln)

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

- Vulnerabilities in upstream Go stdlib or `gopkg.in/yaml.v3` — report those to their respective projects.
- Social engineering of users (e.g. tricking someone into running `friday init --remote <bad-url>`). Friday clones what you tell it to; treat untrusted repos accordingly.
