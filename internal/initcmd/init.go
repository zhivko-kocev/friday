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
	if exists, err := dirNonEmpty(storeDir); err != nil {
		return err
	} else if exists {
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

// readRemoteURL prints the prompt and returns the trimmed first line. EOF
// (no input piped, no terminal) is treated as blank — same as pressing Enter.
func readRemoteURL(in io.Reader) (string, error) {
	output.Info("Remote repository URL (blank to start fresh):")
	fmt.Print("  > ")
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func cloneInto(storeDir, url string) error {
	if !git.Available() {
		return errors.New("git not found in PATH")
	}
	if err := git.ValidateURL(url); err != nil {
		return err
	}
	output.Info("cloning %s", url)
	if err := os.MkdirAll(filepath.Dir(storeDir), 0o755); err != nil {
		return err
	}
	if err := git.Clone(url, storeDir); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(storeDir, config.ManifestName)); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		output.Dim("repo has no %s — push will use built-in presets", config.ManifestName)
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

// writeDefaultManifest persists every built-in preset into friday.yaml so
// the scaffold is push-ready without an extra `friday add` step.
func writeDefaultManifest(storeDir string) error {
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
		return err
	}
	output.OK("wrote %s with %d presets", config.ManifestName, len(cfg.Adapters))
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

func dirNonEmpty(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return len(entries) > 0, nil
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
