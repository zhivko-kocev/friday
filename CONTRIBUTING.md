# Contributing to friday

Thanks for considering a contribution. This doc covers the minimum you need to know to land a useful change.

## Ground rules

- Be the maintainer: every change is for the next person to read. Code that needs a comment to explain *how* it works should be rewritten until the *how* is obvious.
- KISS / YAGNI / DRY. No speculative abstractions, no half-finished implementations.
- Public surface area is precious. New flags, new commands, new YAML keys — all are forever once shipped. Justify them in the PR description.
- Code style: standard `gofmt`, `go vet` clean, `goimports` ordering.

## Project layout

See [AGENTS.md](AGENTS.md) — it's the canonical map of the repo.

```
cmd/friday/         main entry
internal/cli/       command dispatch (one file per subcommand)
internal/engine/    core push/pull logic
internal/config/    friday.yaml loader
internal/presets/   built-in adapter rule sets
internal/...        smaller building blocks
```

`internal/` packages aren't importable from outside this repo. That's intentional — friday is a CLI, not a library.

## Local development

```bash
git clone https://github.com/zhivko-kocev/friday
cd friday
make build             # produces ./friday (or .exe on Windows)
make test              # go test ./...
make lint              # go vet ./...
make tidy              # go mod tidy
```

Go 1.26 is the minimum (`go.mod`'s `go` directive is a hard floor — an older
toolchain refuses to build). The command surface is built on
`github.com/urfave/cli/v3`; parsing and rendering use `go.yaml.in/yaml/v3`
(every YAML file friday reads or writes), `github.com/pelletier/go-toml/v2`
(merge-toml and the codex/gemini renders), `github.com/tailscale/hujson`
(commented JSONC targets), and `mvdan.cc/sh/v3` (real shell parsing in
doctor's unquoted-expansion check). The interactive bits — source selection,
conflict resolution on a real terminal, spinners — are the Charm stack
(bubbletea / bubbles / huh / lipgloss, plus `mattn/go-isatty` and
`muesli/termenv` for TTY and color detection); every one of them has a plain
line-based fallback for a pipe. See `go.mod` for the full list, and note that
anything marked `// indirect` there is not ours to depend on directly.
Running the race detector needs cgo (a C compiler on PATH):
`CGO_ENABLED=1 go test -race ./...`.

For end-to-end testing against your own machine, install into a scratch dir:

```bash
go build -o /tmp/friday-dev ./cmd/friday
HOME=$(mktemp -d) /tmp/friday-dev init --scaffold
```

## Submitting changes

1. **Fork and branch.** One branch per change.
2. **Write a test.** Every behavior change needs one. The existing tests use `t.TempDir()` and `t.Setenv("HOME", ...)` to scope file effects — copy that pattern.
3. **Run the full suite locally**: `go test ./... -race -count=1`. CI runs the same on Linux, macOS, and Windows.
4. **Update docs**. If you change CLI surface, update `README.md`, `internal/cli/help.go` (the two-tier `renderUsage`, plus the static `introText`/`globalFlagsText`/`examplesText` blocks — `printCommandList` introspects `root.Commands`, but those blocks do not), and the relevant sections of `AGENTS.md`.
5. **Commit cleanly.** Use [Conventional Commits](https://www.conventionalcommits.org/) prefixes (`feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`). Goreleaser uses these for changelog generation.
6. **Open the PR.** Fill in the template. Small focused PRs land faster than sprawling ones.

## Adding a preset for a new agent

Most contributions will be new agent presets.

1. Edit [`internal/presets/presets.go`](internal/presets/presets.go), add an entry to the `registry` map. Mirror an existing preset (`claude` is the most full-featured).
2. If any rule uses `strategy: render`, register the adapter in `internal/render` as well: `mcpAdapters` (`mcp.go`), the `Permissions` switch (`permissions.go`), `defaultsAdapters` (`defaults.go`), and the `MCPInverse`/`PermissionsInverse` switches for whatever should pull back. Each is a closed set that returns `render.<Surface>: unknown adapter %q`, so a preset added without this step passes tests that only exercise the registry and then fails at push time. Add the name to `internal/spec`'s `Adapters` too, or `mcp.yaml`'s `agents:` and `defaults.yaml`'s `overrides:` will reject it. A preset limited to `copy`/`concatenate`/`merge-json`/`merge-toml` needs none of this.
3. Add the preset name to the `TestNamesIsSortedAndComplete` test in `presets_test.go`.
4. Update the preset table in `README.md`.
5. If the agent expects a non-XDG path on Windows (e.g. `%APPDATA%\foo`), prefer encoding it as an absolute-looking template (`~/AppData/Roaming/foo`) — friday's path resolution handles `~` but does not auto-translate XDG vs Windows conventions.

Existing users of friday won't auto-pick-up new presets — they're seeded at `friday init` time only. That's intentional: explicit beats clever. New presets ship in the next release, and existing users opt in by editing `friday.yaml`.

## Reporting bugs / requesting features

Use the [issue templates](.github/ISSUE_TEMPLATE). For bugs, include:

- friday version (`friday version`)
- OS + Go version
- Minimal `friday.yaml` that reproduces it
- What you ran, what happened, what you expected

## Security

Report security issues privately — see [SECURITY.md](SECURITY.md).

## License

By contributing you agree that your contributions are licensed under the [MIT License](LICENSE).
