package cli

import (
	"flag"
	"os"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/drift"
	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/output"
	"github.com/zhivko-kocev/friday/internal/presets"
)

type statusOpts struct {
	asJSON bool
	diff   bool
	check  bool
	origin bool
}

func statusFlags(o *statusOpts) *flag.FlagSet {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.BoolVar(&o.asJSON, "json", false, "machine-readable output")
	fs.BoolVar(&o.diff, "diff", false, "also print the content diff for each pending render")
	fs.BoolVar(&o.check, "check", false, "exit 2 if anything is out of sync (for CI); 0 when clean")
	fs.BoolVar(&o.origin, "origin", false, "also show where each adapter is defined (friday.yaml / built-in / plugin)")
	return fs
}

// cmdStatus — a two-axis view of every managed file (no writes): column 1 is
// local drift (the target edited since friday last wrote it — a hand edit to
// capture), column 2 is the pending render (canonical differs from the
// target). Mirrors chezmoi's two-column `status`.
//
// The --json body and the default exit code are computed from the
// push-direction plan exactly as before — the drift column is display-only, so
// existing CI parsers and the exit-2-on-conflict contract don't move. --check
// opts into terraform-style detailed exit codes (2 = anything pending).
func cmdStatus(args []string) int {
	var o statusOpts
	fs := statusFlags(&o)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	cfg, err := loadUserOrDefault()
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	if len(fs.Args()) > 0 {
		if _, err := cfg.SelectAdapters(fs.Args()); err != nil {
			output.Err("%v", err)
			return 1
		}
	}
	changes, err := engine.Push(cfg, engine.Options{
		Adapters: fs.Args(),
		DryRun:   true,
	})
	if err != nil {
		output.Err("%v", err)
		return 1
	}
	if o.asJSON {
		if err := printStatusJSON(cfg, changes); err != nil {
			output.Err("%v", err)
			return 1
		}
		// Default JSON exit stays byte-identical to before (push-only, no drift
		// read). --check opts into the drift-aware gate — the exact CI use case
		// it exists for; the extra read is read-only, so status still writes
		// nothing.
		if o.check {
			return statusExit(changes, buildStatusRows(changes, handEditedLookup()), true)
		}
		return exitCode(changes)
	}

	output.Header("Friday Status (user)")
	output.Dim("store: %s", cfg.StoreDir)
	installed := map[string]bool{}
	for _, name := range cfg.AdapterNames() {
		abs, _ := cfg.AdapterTargetAbs(name)
		if dirExists(abs) {
			installed[name] = true
			output.OK("%-10s [installed]  %s", name, abs)
		} else {
			output.Skip("%-10s [missing]    %s", name, abs)
		}
	}

	rows := buildStatusRows(changes, handEditedLookup())
	printStatusGrid(rows, installed)
	if o.diff {
		printStatusDiffs(changes)
	}
	if o.origin {
		printStatusOrigin(cfg)
	}
	return statusExit(changes, rows, o.check)
}

// statusRow is the two-axis reconcile state of one managed file.
type statusRow struct {
	handEdit bool          // column 1: target drifted from the baseline friday wrote
	render   engine.Action // column 2: what a push would do
	adapter  string
	dest     string
}

// clean reports whether the file needs no attention on either axis.
func (r statusRow) clean() bool {
	return !r.handEdit && r.render == engine.ActionInSync
}

func (r statusRow) col1() string {
	if r.handEdit {
		return "M"
	}
	return " "
}

func (r statusRow) col2() string {
	switch r.render {
	case engine.ActionCreate:
		return "A"
	case engine.ActionUpdate:
		return "M"
	case engine.ActionConflict:
		return "!"
	case engine.ActionMissingSource, engine.ActionUnsupported:
		return "-"
	default: // ActionInSync
		return " "
	}
}

func (r statusRow) level() output.Level {
	switch {
	case r.render == engine.ActionConflict || r.handEdit:
		return output.LevelWarn
	case r.render == engine.ActionMissingSource || r.render == engine.ActionUnsupported:
		return output.LevelSkip
	default:
		return output.LevelInfo
	}
}

// buildStatusRows derives one row per planned change, tagging column 1 from a
// read-only drift lookup. Pure over its inputs so tests assert on the rows.
func buildStatusRows(changes []engine.Change, handEdited func(adapter, destPath string) bool) []statusRow {
	rows := make([]statusRow, 0, len(changes))
	for _, ch := range changes {
		rows = append(rows, statusRow{
			handEdit: handEdited(ch.Adapter, ch.DestPath),
			render:   ch.Action,
			adapter:  ch.Adapter,
			dest:     ch.DestRel,
		})
	}
	return rows
}

// handEditedLookup loads the drift store read-only and reports, per target,
// whether it changed since friday last wrote it. A target friday never wrote
// (no baseline) but that exists reads as drifted — the conservative stance,
// matching the engine. Any load failure degrades to "no drift" rather than
// blocking a status read.
func handEditedLookup() func(adapter, destPath string) bool {
	none := func(string, string) bool { return false }
	path, err := drift.DefaultPath()
	if err != nil {
		return none
	}
	store, err := drift.Load(path)
	if err != nil {
		return none
	}
	return func(adapter, destPath string) bool {
		drifted, exists := store.Check(adapter, destPath)
		return drifted && exists
	}
}

// printStatusGrid renders the two-column grid for installed adapters. To stay
// scannable, files for a not-yet-installed adapter (all pending creates) are
// collapsed into one summary line, and rows for a rule that matched nothing
// (no destination — a store/config issue `doctor` reports) are dropped.
func printStatusGrid(rows []statusRow, installed map[string]bool) {
	inSync, width := 0, 0
	var shown []statusRow
	pending := map[string]int{}
	var pendingOrder []string
	for _, r := range rows {
		if r.clean() {
			inSync++
			continue
		}
		if r.dest == "" {
			continue
		}
		if !installed[r.adapter] {
			if _, seen := pending[r.adapter]; !seen {
				pendingOrder = append(pendingOrder, r.adapter)
			}
			pending[r.adapter]++
			continue
		}
		shown = append(shown, r)
		if len(r.adapter) > width {
			width = len(r.adapter)
		}
	}

	output.Header("changes:")
	if len(shown) == 0 && len(pendingOrder) == 0 {
		output.OK("everything in sync (%d files)", inSync)
		return
	}
	if len(shown) > 0 {
		output.Dim("col 1: local edit to capture   col 2: pending render   ! conflict")
		for _, r := range shown {
			output.Line(r.level(), "%s%s  %-*s  %s", r.col1(), r.col2(), width, r.adapter, r.dest)
		}
	}
	for _, name := range pendingOrder {
		output.Line(output.LevelSkip, " A  %s (%d files — not installed; `friday sync` sets it up)", name, pending[name])
	}
	if inSync > 0 {
		output.Dim("%d file(s) in sync", inSync)
	}
}

// printStatusDiffs prints the content diff for each pending render, under a
// header, when --diff is set. Reuses report.go's printDiff.
func printStatusDiffs(changes []engine.Change) {
	any := false
	for _, ch := range changes {
		if ch.Action == engine.ActionCreate || ch.Action == engine.ActionUpdate || ch.Action == engine.ActionConflict {
			if !any {
				output.Header("diffs:")
				any = true
			}
			output.Dim("%s → %s", ch.Adapter, ch.DestRel)
			printDiff(ch.OldContent, ch.NewContent)
		}
	}
}

// printStatusOrigin shows where each adapter's definition comes from. A
// friday.yaml manifest is authoritative when present — that's where you edit
// an adapter. With no manifest, friday falls back to the built-in presets
// overlaid with any ~/.friday/plugins, so each adapter is tagged built-in or
// plugin. (The loader is either/or, not a merge, so this is exact.)
func printStatusOrigin(cfg *config.Config) {
	output.Header("origin:")
	manifest := false
	if fi, err := os.Stat(cfg.ManifestPath); err == nil && !fi.IsDir() {
		manifest = true
	}
	var pluginNames map[string]bool
	if !manifest {
		pluginNames = map[string]bool{}
		plugins, _ := presets.LoadPlugins(cfg.StoreDir)
		for name := range plugins {
			pluginNames[name] = true
		}
	}
	for _, name := range cfg.AdapterNames() {
		origin := "built-in"
		switch {
		case manifest:
			origin = "friday.yaml"
		case pluginNames[name]:
			origin = "plugin"
		}
		target, _ := cfg.AdapterTargetAbs(name)
		output.Line(output.LevelInfo, "%-12s %-11s %s", name, origin, target)
	}
	if manifest {
		output.Dim("defined in %s — edit or delete an entry there", cfg.ManifestPath)
	} else {
		output.Dim("no friday.yaml — using built-in presets + ~/.friday/plugins")
	}
}

// statusExit maps the run to a process exit code. Without --check it preserves
// the historical contract (exit 2 only on a conflict). With --check it adopts
// terraform's detailed-exitcode semantics: 2 when anything is out of sync.
func statusExit(changes []engine.Change, rows []statusRow, check bool) int {
	if !check {
		return exitCode(changes)
	}
	for _, r := range rows {
		if !r.clean() {
			return 2
		}
	}
	return 0
}
