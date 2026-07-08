package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/drift"
	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/git"
	"github.com/zhivko-kocev/friday/internal/lint"
	"github.com/zhivko-kocev/friday/internal/output"
	"github.com/zhivko-kocev/friday/internal/presets"
)

// cmdDoctor runs a read-only health check on the local install. Surfaces:
//   - whether the user store exists and is a git repo
//   - manifest validity (or fallback-to-presets mode)
//   - per-adapter installed-vs-missing status
//   - drift state across every installed adapter
//
// Exits non-zero only when something is actually broken; "no agents installed"
// is informational, not an error.
func doctorFlags(asJSON *bool) *flag.FlagSet {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.BoolVar(asJSON, "json", false, "machine-readable store-check findings (for CI)")
	return fs
}

func cmdDoctor(args []string) int {
	var asJSON bool
	fs := doctorFlags(&asJSON)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return 1
	}
	// `friday doctor <file>` explains which adapter + rule produces that file
	// (the folded-in `explain`); with no args it runs the full health check.
	if len(pos) == 1 {
		return explainFile(pos[0])
	}
	if len(pos) > 1 {
		output.Err("usage: friday doctor [target-file] [--json]")
		return 1
	}
	if asJSON {
		return doctorJSON()
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

	// 8. store checks (the folded-in `lint` plus best-practice advice):
	// structural errors fail the check; warnings are advisory.
	output.Header("store checks")
	if findings, err := lint.Run(cfg); err != nil {
		output.Err("lint: %v", err)
		bad++
	} else if len(findings) == 0 {
		output.OK("no issues")
	} else {
		warns := 0
		for _, f := range findings {
			if f.Severity == lint.Error {
				output.Err("%-17s %s — %s", f.Rule, f.Path, f.Msg)
				bad++
			} else {
				output.Warn("%-17s %s — %s", f.Rule, f.Path, f.Msg)
				warns++
			}
		}
		if warns > 0 {
			output.Dim("%d advisory warning(s) — best practices, not failures; silence a rule in %s", warns, lint.ConfigName)
		}
	}

	if bad > 0 {
		fmt.Println()
		output.Err("%d problem(s) detected", bad)
		return 1
	}
	return 0
}

// findingJSON is the machine-readable shape of one store-check finding.
type findingJSON struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Path     string `json:"path"`
	Message  string `json:"message"`
}

// doctorJSON emits the store-check findings as diagnostics for CI and exits 1
// if any error-severity finding is present (warnings alone exit 0). The human
// health check is skipped — this is the advisor's scriptable surface.
func doctorJSON() int {
	cfg, err := loadUserOrDefault()
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	findings, err := lint.Run(cfg)
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	out := struct {
		Findings []findingJSON  `json:"findings"`
		Summary  map[string]int `json:"summary"`
	}{Findings: []findingJSON{}, Summary: map[string]int{"error": 0, "warn": 0}}
	for _, f := range findings {
		out.Findings = append(out.Findings, findingJSON{f.Rule, f.Severity.String(), f.Path, f.Msg})
		out.Summary[f.Severity.String()]++
	}
	blob, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	fmt.Println(string(blob))
	if out.Summary["error"] > 0 {
		return 1
	}
	return 0
}

// explainFile answers "which adapter + rule produced this file?" by planning
// a dry-run push over every adapter — the same code path that writes, so there
// is one source of truth for who writes what. Multiple matches all print: two
// rules writing one destination is a misconfiguration this exists to expose.
// Exposed as `friday doctor <file>`.
func explainFile(arg string) int {
	abs, err := filepath.Abs(arg)
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	cfg, err := loadUserOrDefault()
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	changes, err := engine.Push(cfg, engine.Options{DryRun: true})
	if err != nil {
		output.Err("%v", err)
		return 1
	}

	found := 0
	for _, ch := range changes {
		if !samePath(ch.DestPath, abs) {
			continue
		}
		found++
		r := cfg.Adapters[ch.Adapter].Rules[ch.RuleIndex]
		output.Header(ch.DestRel)
		output.Info("adapter:   %s", ch.Adapter)
		output.Info("rule:      from %v → %s  (strategy: %s)", []string(r.From), r.To, r.Strategy)
		if len(r.FrontmatterStrip) > 0 {
			output.Info("           frontmatter_strip: %v", r.FrontmatterStrip)
		}
		for k, v := range r.Replace {
			output.Info("           replace: %q → %q", k, v)
		}
		output.Info("sources:   %s", strings.Join(ch.Sources, ", "))
		output.Info("state:     %s", ch.Action)
		if ch.Reason != "" {
			output.Dim("           %s", ch.Reason)
		}
	}
	if found == 0 {
		output.Warn("no rule produces %s", abs)
		suggestNearest(changes, arg)
		return 1
	}
	if found > 1 {
		output.Warn("%d rules write this file — later rules win; consider removing the overlap", found)
	}
	return 0
}

// suggestNearest points at planned destinations whose relative path ends in
// the same basename, for the common "right file, wrong directory" miss.
func suggestNearest(changes []engine.Change, arg string) {
	base := filepath.Base(arg)
	for _, ch := range changes {
		if filepath.Base(ch.DestRel) == base {
			output.Dim("did you mean %s (%s)?", ch.DestPath, ch.Adapter)
		}
	}
}

// checkEntryFiles reports on the store's entry file. Concatenate rules match
// core.md, core/core.md, and legacy identity.md — a store carrying more than
// one would concatenate them all, which is almost never intended.
func checkEntryFiles(storeDir string) {
	var present []string
	for _, rel := range presets.EntryFiles {
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
