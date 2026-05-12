# Changelog

All notable changes to friday are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- `friday doctor` — read-only health check that surfaces store presence, git status, manifest validity, per-adapter installation state, and any detected drift.
- `friday init --remote URL` flag — clone a remote without piping into stdin.
- `friday init --scaffold` flag — empty-store scaffold without prompting.
- Push/pull summary line — `n created, m updated, k in-sync, … skipped` after every change report.

### Changed
- **Target file writes are now atomic** (temp file + fsync + rename). A Ctrl-C mid-write leaves the previous version of the file intact instead of corrupting it.
- **`opencode` preset target** moved from `~/.opencode` to `~/.config/opencode` to match the XDG convention OpenCode itself uses. Existing users with `~/.opencode` should either rename their directory or edit `friday.yaml` to point at the old path.

### Fixed
- `output.Dim` / `output.Header` no longer re-interpret literal `%` in pre-formatted strings as `fmt` verbs.

## [0.1.0] — initial release

- `init` / `list` / `push` / `pull` / `status` / `remote` commands.
- Built-in presets for Claude Code, Cursor, OpenCode, and GitHub Copilot.
- SHA256-based drift detection.
- Interactive conflict resolver with line-LCS diff.
- Cross-platform: Linux, macOS, Windows.

[Unreleased]: https://github.com/zhivko-kocev/friday/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/zhivko-kocev/friday/releases/tag/v0.1.0
