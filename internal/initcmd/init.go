// Package initcmd scaffolds the user-level Friday store at $UserConfigDir/friday/
// (and optionally clones it from a git remote first).
package initcmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/git"
	"github.com/zhivko-kocev/friday/internal/output"
	"github.com/zhivko-kocev/friday/internal/presets"
)

// Options controls a `friday init` invocation.
type Options struct {
	FromGit     string   // optional repo URL — clone instead of scaffold
	Remote      string   // optional `origin` URL to register after scaffold-init
	Adapters    []string // preset names to seed friday.yaml with (scaffold path)
	NoGit       bool     // skip the `git init` step on scaffold
	Force       bool     // overwrite an existing user store
	ReallyForce bool     // permit Force to wipe a store containing .git/
}

// Run scaffolds (or clones into) the user-level store.
func Run(opts Options) error {
	if opts.ReallyForce && !opts.Force {
		return errors.New("--really-force has no effect without --force")
	}
	storeDir, err := config.UserStoreDir()
	if err != nil {
		return err
	}
	if exists, err := dirNonEmpty(storeDir); err != nil {
		return err
	} else if exists && !opts.Force {
		return fmt.Errorf("%s already exists (use --force to overwrite)", storeDir)
	}
	if opts.Force {
		if err := safeWipe(storeDir, opts.ReallyForce); err != nil {
			return err
		}
	}

	if opts.FromGit != "" {
		return cloneInto(storeDir, opts.FromGit)
	}
	return scaffoldEmpty(storeDir, opts)
}

// safeWipe removes storeDir, but refuses if it looks like an active git
// working tree (.git/ present) unless reallyForce was passed. Avoids the
// "I pointed --force at the wrong directory" footgun.
func safeWipe(storeDir string, reallyForce bool) error {
	if _, err := os.Stat(storeDir); os.IsNotExist(err) {
		return nil
	}
	gitDir := filepath.Join(storeDir, ".git")
	if _, err := os.Stat(gitDir); err == nil && !reallyForce {
		return fmt.Errorf("%s contains a .git/ directory — refusing --force without --really-force", storeDir)
	}
	return os.RemoveAll(storeDir)
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
	manifestPath := filepath.Join(storeDir, config.ManifestName)
	if _, err := os.Stat(manifestPath); err != nil {
		if os.IsNotExist(err) {
			output.Warn("repo has no %s — pushes will fail until one is added", config.ManifestName)
			output.Dim("hint: `friday add <preset>` to seed it from a built-in preset")
		} else {
			return err
		}
	}
	output.OK("user store ready at %s", storeDir)
	return nil
}

func scaffoldEmpty(storeDir string, opts Options) error {
	if err := scaffoldStore(storeDir); err != nil {
		return err
	}

	// Only persist friday.yaml if the user explicitly chose adapters —
	// without it, push falls back to the full preset set.
	if len(opts.Adapters) > 0 {
		cfg := &config.Config{
			Version:      1,
			Adapters:     map[string]*config.Adapter{},
			Scope:        config.ScopeUser,
			ManifestPath: filepath.Join(storeDir, config.ManifestName),
			StoreDir:     storeDir,
		}
		for _, name := range opts.Adapters {
			p, err := presets.Resolve(name)
			if err != nil {
				return err
			}
			cfg.Adapters[name] = p.Adapter()
		}
		if err := cfg.Save(); err != nil {
			return err
		}
		output.OK("wrote %s", config.ManifestName)
		for _, name := range cfg.AdapterNames() {
			if p, ok := presets.Get(name); ok {
				output.Info("adapter %s: %s", name, p.Comment)
			}
		}
	}

	output.OK("user store scaffolded at %s", storeDir)

	if !opts.NoGit {
		if !git.Available() {
			output.Warn("git not in PATH — skipping `git init` (use --no-git to silence)")
		} else if err := git.Init(storeDir); err != nil {
			output.Warn("git init failed: %v", err)
		} else {
			output.OK("initialized git repo")
			if opts.Remote != "" {
				if err := git.AddRemote(storeDir, "origin", opts.Remote); err != nil {
					output.Warn("git remote add origin failed: %v", err)
				} else {
					output.OK("added remote origin → %s", opts.Remote)
				}
			}
		}
	}

	if len(opts.Adapters) == 0 {
		output.Dim("no adapters configured — push will use built-in presets")
		output.Dim("available presets: %v", presets.Names())
	}
	return nil
}

// AddAdapter appends a preset (or refreshes an existing entry) to the config.
func AddAdapter(cfg *config.Config, name string, target string, force bool) error {
	p, err := presets.Resolve(name)
	if err != nil {
		return err
	}
	if existing, ok := cfg.Adapters[name]; ok && !force {
		return fmt.Errorf("adapter %q already defined (target: %s) — use --force to replace", name, existing.Target)
	}
	ad := p.Adapter()
	if target != "" {
		ad.Target = target
	}
	cfg.Adapters[name] = ad
	if err := cfg.Save(); err != nil {
		return err
	}
	output.OK("added adapter %s → %s", name, ad.Target)
	output.Dim("%s", p.Comment)
	return nil
}

// RemoveAdapter deletes a named adapter from the config and persists. Does
// not touch the on-disk target dir — undoing a push is the user's call.
func RemoveAdapter(cfg *config.Config, name string) error {
	if _, ok := cfg.Adapters[name]; !ok {
		return fmt.Errorf("adapter %q not in friday.yaml", name)
	}
	delete(cfg.Adapters, name)
	if err := cfg.Save(); err != nil {
		return err
	}
	output.OK("removed adapter %s", name)
	output.Dim("note: the on-disk target dir was not touched")
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
	if err := writeIfMissing(filepath.Join(storeAbs, "identity.md"), placeholderIdentity); err != nil {
		return err
	}
	if err := writeIfMissing(filepath.Join(storeAbs, "rules", "general.md"), placeholderRule); err != nil {
		return err
	}
	// Ship a .gitignore so `friday remote push` (which uses `git add -A`) won't
	// pick up secrets or runtime state if the user drops them in the store.
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

const placeholderIdentity = `# Identity

You are an AI assistant helping the user with their work.

Edit this file to describe yourself, your role, and your operating principles.
This file is concatenated into agent-specific instructions (e.g. CLAUDE.md, AGENTS.md).
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
