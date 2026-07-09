// Package initcmd scaffolds the user-level Friday store at $HOME/.friday/
// (or clones a remote repo into that path). Driven by an interactive prompt:
// the user supplies a remote URL, or leaves it blank to scaffold an empty
// skeleton with `git init`.
package initcmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/git"
	"github.com/zhivko-kocev/friday/internal/output"
	"github.com/zhivko-kocev/friday/internal/presets"
	"github.com/zhivko-kocev/friday/internal/ui"
)

// Run scaffolds (or clones into) the user-level store. Prompts on `prompt`
// for a remote URL: blank input → empty scaffold + git init; non-blank →
// `git clone --depth=1 URL`. The reader is injected so tests can drive the
// prompt; production callers pass os.Stdin.
func Run(prompt io.Reader) error {
	storeDir, err := config.UserStoreDir()
	if err != nil {
		return err
	}
	if initable, err := NeedsInit(storeDir); err != nil {
		return err
	} else if !initable {
		return fmt.Errorf("%s already exists — remove it yourself to re-init", storeDir)
	}

	url, err := readRemoteURL(prompt)
	if err != nil {
		return err
	}

	if url == "" {
		return scaffoldEmpty(storeDir)
	}
	return cloneInto(storeDir, url)
}

// readRemoteURL asks for the store's git remote and returns the trimmed URL.
// On a real terminal it uses the rich text input; otherwise it reads one line
// (EOF — nothing piped — is treated as blank, same as pressing Enter to start
// fresh).
func readRemoteURL(in io.Reader) (string, error) {
	if ui.Interactive() {
		url, err := ui.Input("Remote repository URL (blank to start fresh)", "git@github.com:you/ai-config.git")
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(url), nil
	}
	output.Info("Remote repository URL (blank to start fresh):")
	fmt.Print("  > ")
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// prepClone runs the pre-flight checks and makes the parent dir. Pure — shared
// by the CLI's cloneInto (which keeps them before its spinner, so the plain-path
// error output is unchanged) and the Clone seam.
func prepClone(storeDir, url string) error {
	if !git.Available() {
		return errors.New("git not found in PATH")
	}
	if err := git.ValidateURL(url); err != nil {
		return err
	}
	return os.MkdirAll(filepath.Dir(storeDir), 0o755)
}

// Clone fetches url into storeDir. Pure — no printing or spinner; the caller
// owns progress UI. It returns a non-fatal advisory (empty when all is well) so
// the control room can surface the same "no friday.yaml → presets" notice
// cloneInto prints, rather than silently dropping it. cloneInto wraps it with
// the CLI spinner + summary; the control room runs it inside its own tea.Cmd.
func Clone(storeDir, url string) (presetAdvisory string, err error) {
	if err := prepClone(storeDir, url); err != nil {
		return "", err
	}
	if err := git.Clone(url, storeDir); err != nil {
		return "", err
	}
	return presetFallbackAdvisory(storeDir)
}

// presetFallbackAdvisory returns the "repo has no friday.yaml — push will use
// built-in presets" notice when a freshly-cloned store carries no manifest
// (empty when it does). Shared so the CLI and the control room word it the same.
func presetFallbackAdvisory(storeDir string) (string, error) {
	if _, err := os.Stat(filepath.Join(storeDir, config.ManifestName)); err != nil {
		if !os.IsNotExist(err) {
			return "", err
		}
		return fmt.Sprintf("repo has no %s — push will use built-in presets", config.ManifestName), nil
	}
	return "", nil
}

func cloneInto(storeDir, url string) error {
	if err := prepClone(storeDir, url); err != nil {
		return err
	}
	if err := ui.WithSpinner("cloning "+url, func() error { return git.Clone(url, storeDir) }); err != nil {
		return err
	}
	advisory, err := presetFallbackAdvisory(storeDir)
	if err != nil {
		return err
	}
	if advisory != "" {
		output.Dim("%s", advisory)
	}
	output.OK("user store ready at %s", storeDir)
	return nil
}

// scaffoldEmpty writes the skeleton, seeds friday.yaml with every built-in
// preset, then runs `git init`. Empty-input flow assumes the user wants a
// fresh store ready to push to all four agents out of the box.
func scaffoldEmpty(storeDir string) error {
	if err := scaffoldStore(storeDir); err != nil {
		return err
	}
	if err := writeDefaultManifest(storeDir); err != nil {
		return err
	}
	output.OK("user store scaffolded at %s", storeDir)

	if !git.Available() {
		output.Warn("git not in PATH — skipping `git init`")
		return nil
	}
	if err := git.Init(storeDir); err != nil {
		output.Warn("git init failed: %v", err)
		return nil
	}
	output.OK("initialized git repo")
	return nil
}

// Scaffold writes the empty-store skeleton, seeds friday.yaml with every preset,
// and best-effort `git init`s. Pure — no printing; the caller owns progress UI.
// It returns a non-fatal git advisory (empty when all is well) so the control
// room can surface the same "no git" / "git init failed" notice scaffoldEmpty
// prints, rather than silently producing a non-repo store. Safe on an
// existing-empty dir (MkdirAll + writeIfMissing are idempotent).
func Scaffold(storeDir string) (gitAdvisory string, err error) {
	if err := scaffoldStore(storeDir); err != nil {
		return "", err
	}
	if _, err := writeManifest(storeDir); err != nil {
		return "", err
	}
	if !git.Available() {
		return "git not in PATH — skipped `git init`; `friday remote`/`share` need a git repo", nil
	}
	if err := git.Init(storeDir); err != nil {
		return fmt.Sprintf("git init failed: %v — `friday remote`/`share` need a git repo", err), nil
	}
	return "", nil
}

// writeManifest persists every built-in preset into friday.yaml and returns how
// many it wrote. Pure — no printing; shared by Scaffold and writeDefaultManifest.
func writeManifest(storeDir string) (int, error) {
	cfg := &config.Config{
		Version:      1,
		Adapters:     map[string]*config.Adapter{},
		Scope:        config.ScopeUser,
		ManifestPath: filepath.Join(storeDir, config.ManifestName),
		StoreDir:     storeDir,
	}
	for _, name := range presets.Names() {
		p, _ := presets.Get(name)
		cfg.Adapters[name] = p.Adapter()
	}
	if err := cfg.Save(); err != nil {
		return 0, err
	}
	return len(cfg.Adapters), nil
}

// writeDefaultManifest persists every built-in preset into friday.yaml so
// the scaffold is push-ready without an extra `friday add` step.
func writeDefaultManifest(storeDir string) error {
	n, err := writeManifest(storeDir)
	if err != nil {
		return err
	}
	output.OK("wrote %s with %d presets", config.ManifestName, n)
	return nil
}

func scaffoldStore(storeAbs string) error {
	if err := os.MkdirAll(storeAbs, 0o755); err != nil {
		return err
	}
	for _, sub := range []string{"rules", "agents", "commands", "skills"} {
		if err := os.MkdirAll(filepath.Join(storeAbs, sub), 0o755); err != nil {
			return err
		}
		keep := filepath.Join(storeAbs, sub, ".gitkeep")
		if _, err := os.Stat(keep); errors.Is(err, os.ErrNotExist) {
			if err := os.WriteFile(keep, nil, 0o644); err != nil {
				return err
			}
		}
	}
	if err := writeIfMissing(filepath.Join(storeAbs, "core.md"), placeholderCore); err != nil {
		return err
	}
	if err := writeIfMissing(filepath.Join(storeAbs, "rules", "general.md"), placeholderRule); err != nil {
		return err
	}
	return writeIfMissing(filepath.Join(storeAbs, ".gitignore"), placeholderGitignore)
}

// NeedsInit reports whether dir is init-able — absent or empty. It is the single
// definition of "empty enough to scaffold or clone into", shared by `friday
// init`'s overwrite guard and the control room's cold-start gate so the two
// can't drift on what counts as an existing store.
func NeedsInit(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}
	return len(entries) == 0, nil
}

func writeIfMissing(path, content string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

const placeholderCore = `# Core

You are an AI assistant helping the user with their work.

Edit this file to describe the assistant's role and operating principles.
It leads every agent's generated instructions (CLAUDE.md, AGENTS.md,
copilot-instructions.md).
`

const placeholderRule = `---
description: General rules that apply to all interactions.
---

# General

- Be concise. Lead with the answer.
- Cite sources for non-obvious claims.
- Edit existing files instead of creating new ones when reasonable.
`

const placeholderGitignore = `# Secrets — never commit
.env
.env.*
*.key
*.pem
*.secret
*.keystore
.credentials.json

# OS / editor
.DS_Store
Thumbs.db
*.swp
*~
.vscode/
.idea/

# Claude Code runtime state (in case the dir doubles as ~/.claude)
sessions/
projects/
backups/
cache/
file-history/
ide/
session-env/
shell-snapshots/
telemetry/
history.jsonl
mcp-needs-auth-cache.json
stats-cache.json
`
