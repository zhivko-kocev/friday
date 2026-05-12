package cli

import (
	"fmt"
	"os"

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

	// 3. manifest
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

	// 4. adapters
	output.Header("adapters")
	for _, name := range cfg.AdapterNames() {
		abs, _ := cfg.AdapterTargetAbs(name)
		if dirExists(abs) {
			output.OK("%-10s installed   %s", name, abs)
		} else {
			output.Skip("%-10s missing     %s", name, abs)
		}
	}

	// 5. drift across installed adapters
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

	// 6. drift store file
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
