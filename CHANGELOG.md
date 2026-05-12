# Changelog

All notable changes to friday are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

_Nothing yet._

## [0.0.3] — 2026-05-12

First open-source-ready cut. Bug fixes, new commands, and a full set of community files.

### Added
- `friday doctor` — read-only health check that surfaces store presence, git status, manifest validity, per-adapter installation state, and any detected drift.
- `friday init --remote URL` flag — clone a remote without piping into stdin.
- `friday init --scaffold` flag — empty-store scaffold without prompting.
- Push/pull summary line — `n created, m updated, k in-sync, … skipped` after every change report.
- Community files: `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md`, `ROADMAP.md`, `CHANGELOG.md`.
- GitHub issue + PR templates, dependabot config, `.editorconfig`.

### Changed
- **Target file writes are now atomic** (temp file + fsync + rename). A Ctrl-C mid-write leaves the previous version of the file intact instead of corrupting it.
- **`opencode` preset target** moved from `~/.opencode` to `~/.config/opencode` to match the XDG convention OpenCode itself uses. Users with `~/.opencode` from earlier versions should either rename the directory or edit `friday.yaml` to keep the old path.
- Removed unused `friday add` / `friday remove` commands and the `presets.Resolve` helper they relied on. `friday init` already seeds every preset; adapters are toggled by editing `friday.yaml` directly.
- README rewritten with badges, full doctor/init flag docs, and a safety section.

### Fixed
- `output.Dim` / `output.Header` no longer re-interpret literal `%` in pre-formatted strings as `fmt` verbs (would render as `%!(NOVERB)`).

## [0.0.2] — Windows installer

- Added `install.ps1` for PowerShell installation on Windows.
- goreleaser configured for `windows/amd64` archives.

## [0.0.1] — initial tag

- First tagged build. Core commands: `init`, `list`, `push`, `pull`, `status`, `remote`.
- Built-in presets for Claude Code, Cursor, OpenCode, and GitHub Copilot.
- SHA256-based drift detection with CRLF-tolerant hashing.
- Interactive conflict resolver with line-LCS diff.
- Cross-platform: Linux, macOS, Windows.

[Unreleased]: https://github.com/zhivko-kocev/friday/compare/v0.0.3...HEAD
[0.0.3]: https://github.com/zhivko-kocev/friday/compare/v0.0.2...v0.0.3
[0.0.2]: https://github.com/zhivko-kocev/friday/compare/v0.0.1...v0.0.2
[0.0.1]: https://github.com/zhivko-kocev/friday/releases/tag/v0.0.1
