package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/drift"
	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/git"
	"github.com/zhivko-kocev/friday/internal/output"
)

// cmdDoctor runs a read-only health check on the local install. Surfaces:
//   - whether the user store exists and is a git repo
//   - manifest validity (or fallback-to-presets mode)
//   - per-adapter installed-vs-missing status
//   - drift state across every installed adapter
//
// Exits non-zero only when something is actually broken; "no agents installed"
// is informational, not an error.
func cmdDoctor(args []string) int {
	if len(args) > 0 {
		output.Err("friday doctor takes no arguments")
		return 1
	}

	output.Header("friday doctor")
	bad := 0

	// 1. store directory
	storeDir, err := config.UserStoreDir()
	if err != nil {
		output.Err("resolve store dir: %v", err)
		return 1
	}
	if _, err := os.Stat(storeDir); err != nil {
		output.Err("store missing: %s", storeDir)
		output.Dim("hint: run `friday init`")
		return 1
	}
	output.OK("store: %s", storeDir)

	// 2. git status
	if git.Available() {
		if git.IsRepo(storeDir) {
			output.OK("git: store is a git repo")
			if dirty, _ := git.HasUncommitted(storeDir); dirty {
				output.Warn("git: uncommitted changes in store (run `friday remote push -m ...`)")
			}
		} else {
			output.Warn("git: store is not a git repo (run `git init` inside %s to enable remote sync)", storeDir)
		}
	} else {
		output.Warn("git: not in PATH — `friday remote` will not work")
	}

	// 3. entry file (core.md / core/core.md / legacy identity.md) + hooks
	checkEntryFiles(storeDir)
	checkHooks(storeDir)

	// 4. manifest
	cfg, err := config.LoadUser()
	switch {
	case err == nil:
		output.OK("manifest: %s (%d adapter(s))", cfg.ManifestPath, len(cfg.Adapters))
	case err == config.ErrNoManifest:
		output.Warn("manifest: no friday.yaml — falling back to all built-in presets")
		cfg, err = loadUserOrDefault()
		if err != nil {
			output.Err("preset fallback failed: %v", err)
			return 1
		}
	default:
		output.Err("manifest invalid: %v", err)
		return 1
	}

	// 5. adapters
	output.Header("adapters")
	for _, name := range cfg.AdapterNames() {
		abs, _ := cfg.AdapterTargetAbs(name)
		if dirExists(abs) {
			output.OK("%-10s installed   %s", name, abs)
		} else {
			output.Skip("%-10s missing     %s", name, abs)
		}
	}

	// 6. drift across installed adapters
	output.Header("drift")
	installed := installedAdapters(cfg)
	if len(installed) == 0 {
		output.Dim("(no installed adapters — nothing to check)")
	} else {
		changes, err := engine.Push(cfg, engine.Options{
			Adapters: installed,
			DryRun:   true,
		})
		if err != nil {
			output.Err("dry-run push: %v", err)
			bad++
		} else {
			conflicts := 0
			for _, ch := range changes {
				if ch.Action == engine.ActionConflict {
					conflicts++
					output.Warn("drift in %s: %s", ch.Adapter, ch.DestRel)
				}
			}
			if conflicts == 0 {
				output.OK("no drift detected")
			} else {
				output.Dim("hint: `friday pull <adapter>` to capture edits, or `friday push --force` to overwrite")
			}
		}
	}

	// 7. drift store file
	driftPath, err := drift.DefaultPath()
	if err == nil {
		if _, err := os.Stat(driftPath); err == nil {
			output.OK("drift cache: %s", driftPath)
		} else {
			output.Dim("drift cache: %s (not created yet — first push writes it)", driftPath)
		}
	}

	if bad > 0 {
		fmt.Println()
		output.Err("%d problem(s) detected", bad)
		return 1
	}
	return 0
}

// checkEntryFiles reports on the store's entry file. Concatenate rules match
// core.md, core/core.md, and legacy identity.md — a store carrying more than
// one would concatenate them all, which is almost never intended.
func checkEntryFiles(storeDir string) {
	var present []string
	for _, rel := range []string{"core.md", "core/core.md", "identity.md"} {
		if _, err := os.Stat(filepath.Join(storeDir, filepath.FromSlash(rel))); err == nil {
			present = append(present, rel)
		}
	}
	switch {
	case len(present) == 0:
		output.Dim("entry file: none (core.md) — generated instructions will start with your rules")
	case len(present) > 1:
		output.Warn("entry file: %d variants present (%s) — concatenate rules will include all of them; keep one", len(present), strings.Join(present, ", "))
	case present[0] == "identity.md":
		output.Dim("entry file: identity.md (legacy name — rename to core.md; both work)")
	default:
		output.OK("entry file: %s", present[0])
	}
}

// checkHooks flags a limitation pushes can't fix: Claude Code auto-loads
// hooks.json only from plugins. The store's hooks stay in ~/.friday/hooks/;
// wiring them up means adding entries to ~/.claude/settings.json by hand.
func checkHooks(storeDir string) {
	if _, err := os.Stat(filepath.Join(storeDir, "hooks", "hooks.json")); err == nil {
		output.Dim("hooks: store ships hooks/hooks.json — Claude Code only auto-loads plugin hooks; add entries to ~/.claude/settings.json pointing at %s manually", filepath.Join(storeDir, "hooks"))
	}
}
