package cli

import (
	"context"
	"os"
	"strings"

	ucli "github.com/urfave/cli/v3"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/drift"
	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/output"
	"github.com/zhivko-kocev/friday/internal/rules"
	"github.com/zhivko-kocev/friday/internal/source"
)

// newStatusCommand is a two-axis view of every managed file (no writes):
// column 1 is local drift (the target edited since friday last wrote it — a
// hand edit to capture), column 2 is the pending render (canonical differs
// from the target). Mirrors chezmoi's two-column `status`.
//
// The --json body and the default exit code are computed from the
// push-direction plan exactly as before — the drift column is display-only,
// so existing CI parsers and the exit-2-on-conflict contract don't move.
// --check opts into terraform-style detailed exit codes (2 = anything
// pending).
func newStatusCommand() *ucli.Command {
	return &ucli.Command{
		Name:          "status",
		Category:      "porcelain",
		Usage:         "show what would change (no writes)",
		ArgsUsage:     "[adapter...]",
		ShellComplete: completeAdapters,
		Flags: []ucli.Flag{
			&ucli.BoolFlag{Name: "json", Usage: "machine-readable output"},
			&ucli.BoolFlag{Name: "diff", Usage: "also print the content diff for each pending render"},
			&ucli.BoolFlag{Name: "check", Usage: "exit 2 if anything is out of sync (for CI); 0 when clean"},
			&ucli.BoolFlag{Name: "origin", Usage: "also show where each adapter is defined (friday.yaml / built-in)"},
		},
		Action: runStatus,
	}
}

func runStatus(_ context.Context, cmd *ucli.Command) error {
	adapters := cmd.Args().Slice()
	asJSON := cmd.Bool("json")
	check := cmd.Bool("check")

	cfg, err := loadUserOrDefault()
	if err != nil {
		output.Err("%v", err)
		return ucli.Exit("", 1)
	}
	if len(adapters) > 0 {
		if _, err := cfg.SelectAdapters(adapters); err != nil {
			output.Err("%v", err)
			return ucli.Exit("", 1)
		}
	}
	changes, err := engine.Push(cfg, engine.Options{
		Adapters: adapters,
		DryRun:   true,
	})
	if err != nil {
		output.Err("%v", err)
		return ucli.Exit("", 1)
	}
	if asJSON {
		if err := printStatusJSON(cfg, changes); err != nil {
			output.Err("%v", err)
			return ucli.Exit("", 1)
		}
		// Default JSON exit stays byte-identical to before (push-only, no drift
		// read). --check opts into the drift-aware gate — the exact CI use case
		// it exists for; the extra read is read-only, so status still writes
		// nothing.
		if check {
			return exitFromCode(statusExit(changes, buildStatusRows(changes, handEditedLookup()), true))
		}
		return exitErr(changes)
	}

	output.Header("Friday Status (user)")
	output.Dim("store: %s", cfg.StoreDir)
	installed := map[string]bool{}
	names := cfg.AdapterNames()
	gridRows := make([][]string, len(names))
	installedAt := make([]bool, len(names))
	for i, name := range names {
		abs, _ := cfg.AdapterTargetAbs(name)
		// The same definition push and sync use. A bare dirExists here made
		// status report the shared "agents" adapter as missing, and fold its
		// pending files under "`friday sync` sets it up", while sync was
		// already writing there — status has to describe what sync will do.
		installedAt[i] = adapterInstalled(cfg, name)
		status := "[missing]"
		if installedAt[i] {
			installed[name] = true
			status = "[installed]"
		}
		gridRows[i] = []string{name, status, abs}
	}
	for i, line := range alignTable(nil, gridRows) {
		if installedAt[i] {
			output.OK("%s", line)
		} else {
			output.Skip("%s", line)
		}
	}

	rows := buildStatusRows(changes, handEditedLookup())
	printStatusGrid(rows, installed)
	if cmd.Bool("diff") {
		printDiffs(changes)
	}
	if cmd.Bool("origin") {
		printStatusOrigin(cfg)
	}
	return exitFromCode(statusExit(changes, rows, check))
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
		printGridRows(shown, width)
	}
	for _, name := range pendingOrder {
		output.Line(output.LevelSkip, " A  %s (%d files — not installed; `friday sync` sets it up)", name, pending[name])
	}
	if inSync > 0 {
		output.Dim("%d file(s) in sync", inSync)
	}
}

// statusCollapseMin is how many same-action pending renders one adapter must
// have before they fold into a single count line instead of listing each file.
// Below it, individual paths stay visible (status is the drill-down view);
// above it, a flood collapses like the push report.
const statusCollapseMin = 3

// printGridRows renders the two-column grid body. Rows that need per-file
// attention — a hand edit (column 1) or a conflict — always print individually.
// Pure pending renders (column 2 only) fold per adapter and action into a
// count-plus-breakdown line once a group reaches statusCollapseMin, so a large
// render doesn't bury the rows that actually need a decision.
func printGridRows(shown []statusRow, width int) {
	var adapterOrder []string
	seen := map[string]bool{}
	individual := map[string][]statusRow{}
	pendingDests := map[string]map[string][]string{} // adapter -> col2 marker -> dests
	pendingOrder := map[string][]string{}            // adapter -> marker order
	markerLevel := map[string]output.Level{}         // col2 marker -> level
	for _, r := range shown {
		if !seen[r.adapter] {
			seen[r.adapter] = true
			adapterOrder = append(adapterOrder, r.adapter)
		}
		if r.handEdit || r.render == engine.ActionConflict {
			individual[r.adapter] = append(individual[r.adapter], r)
			continue
		}
		m := r.col2()
		if pendingDests[r.adapter] == nil {
			pendingDests[r.adapter] = map[string][]string{}
		}
		if _, ok := pendingDests[r.adapter][m]; !ok {
			pendingOrder[r.adapter] = append(pendingOrder[r.adapter], m)
		}
		pendingDests[r.adapter][m] = append(pendingDests[r.adapter][m], r.dest)
		markerLevel[m] = r.level()
	}
	for _, a := range adapterOrder {
		for _, r := range individual[a] {
			output.Line(r.level(), "%s%s  %-*s  %s", r.col1(), r.col2(), width, r.adapter, r.dest)
		}
		for _, m := range pendingOrder[a] {
			dests := pendingDests[a][m]
			if len(dests) < statusCollapseMin {
				for _, d := range dests {
					output.Line(markerLevel[m], " %s  %-*s  %s", m, width, a, d)
				}
				continue
			}
			output.Line(markerLevel[m], " %s  %-*s  %d files %s", m, width, a, len(dests), folderBreakdown(dests))
		}
	}
}

// printStatusOrigin shows where each adapter's definition comes from. A
// friday.yaml manifest is authoritative when present — that's where you edit
// an adapter; with no manifest, friday falls back to the built-in presets.
// (The loader is either/or, not a merge, so this is exact.)
//
// A multi-source store (Phase 2) additionally gets a per-rule Source column
// naming which configured sources currently hold a matching file — a plain
// single-source store (today's common case) shows nothing extra here, since
// there is only ever one place a file could come from (D10: unified by
// default; --origin's per-rule detail is the one opt-in exception).
func printStatusOrigin(cfg *config.Config) {
	output.Header("origin:")
	manifest := false
	if fi, err := os.Stat(cfg.ManifestPath); err == nil && !fi.IsDir() {
		manifest = true
	}
	origin := "built-in"
	if manifest {
		origin = "friday.yaml"
	}
	var roots []rules.Root
	if len(cfg.Sources) > 0 {
		if set, err := source.Load(cfg); err == nil {
			roots = set.Roots()
			names := make([]string, len(roots))
			for i, r := range roots {
				names[i] = r.Name
			}
			output.Dim("sources: %s", strings.Join(names, ", "))
			// A source in the manifest but absent from disk contributes no
			// files, so every rule quietly renders from whatever is left —
			// the natural state between `friday source add` and the first
			// `friday source update`. Listing it above as if it were present
			// is how an incomplete render reaches every agent unnoticed.
			for _, missing := range set.Uncloned() {
				output.Warn("%s: not cloned — run `friday source update`", missing)
			}
		}
	}
	names := cfg.AdapterNames()
	gridRows := make([][]string, len(names))
	for i, name := range names {
		target, _ := cfg.AdapterTargetAbs(name)
		gridRows[i] = []string{name, origin, target}
	}
	for i, line := range alignTable(nil, gridRows) {
		name := names[i]
		output.Line(output.LevelInfo, "%s", line)
		// Each adapter's rule mappings (strategy: from → to) — the one view the
		// removed `list` command uniquely offered, folded in here.
		for _, r := range cfg.Adapters[name].Rules {
			strat := r.Strategy
			if strat == "" {
				strat = "copy"
			}
			if len(roots) > 0 {
				output.Dim("  %s  %v → %s  [%s]", strat, []string(r.From), r.To, strings.Join(ruleSources(roots, r.From), ", "))
			} else {
				output.Dim("  %s  %v → %s", strat, []string(r.From), r.To)
			}
		}
	}
	if manifest {
		output.Dim("defined in %s — edit or delete an entry there", cfg.ManifestPath)
	} else {
		output.Dim("no friday.yaml — using built-in presets")
	}
}

// ruleSources returns the distinct source names, in roots' own precedence
// order (lowest first), that currently hold at least one file matching any
// of the rule's from-patterns.
func ruleSources(roots []rules.Root, from rules.FromSpec) []string {
	seen := map[string]bool{}
	for _, pat := range from {
		matched, _, err := rules.ExpandAll(roots, pat)
		if err != nil {
			continue
		}
		for _, m := range matched {
			seen[m.Source] = true
		}
	}
	var ordered []string
	for _, r := range roots {
		if seen[r.Name] {
			ordered = append(ordered, r.Name)
		}
	}
	if len(ordered) == 0 {
		return []string{"none"}
	}
	return ordered
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
