package cli

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	ucli "github.com/urfave/cli/v3"

	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/output"
	"github.com/zhivko-kocev/friday/internal/textdiff"
)

// verbose is set by the --verbose root flag (cli.go); report() reads it
// directly rather than threading a parameter through its 8+ call sites,
// the same reasoning newRootCommand's version var is package-level state
// instead of a parameter on every function that ever prints it.
var verbose bool

// report prints a per-adapter summary of the changes the engine produced,
// collapsed so the common case fits on a screen: created/updated files fold
// into one count-plus-folder-breakdown line per adapter, in-sync files never
// list individually (they'd be pure noise on a re-run), missing optional
// sources fold into a count, and only the things that need a decision —
// conflicts and unsupported skips — are named one per line. A final
// grand-total line closes it out. --diff appends a separate windowed section.
// --verbose replaces both collapsed lines with one line per file, each
// created/updated entry naming the source(s) that rendered it — the detail
// the collapsed form deliberately hides on the common, quiet path.
func report(changes []engine.Change, showDiff, dryRun bool) {
	if len(changes) == 0 {
		output.Dim("no changes")
		return
	}
	byAdapter := map[string][]engine.Change{}
	order := []string{}
	for _, ch := range changes {
		if _, seen := byAdapter[ch.Adapter]; !seen {
			order = append(order, ch.Adapter)
		}
		byAdapter[ch.Adapter] = append(byAdapter[ch.Adapter], ch)
	}

	width := 0
	for _, name := range order {
		if len(name) > width {
			width = len(name)
		}
	}

	header := "changes:"
	if dryRun {
		header = "changes (dry-run):"
	}
	output.Header(header)

	inSync, collapsedMissing := 0, false
	for _, name := range order {
		var created, updated []engine.Change
		var conflicts, missing, skips, warned []engine.Change
		for _, ch := range byAdapter[name] {
			switch ch.Action {
			case engine.ActionInSync:
				inSync++
			case engine.ActionCreate:
				created = append(created, ch)
			case engine.ActionUpdate:
				updated = append(updated, ch)
			case engine.ActionConflict:
				conflicts = append(conflicts, ch)
			case engine.ActionMissingSource:
				missing = append(missing, ch)
			case engine.ActionUnsupported:
				skips = append(skips, ch)
			}
			// A rendered file can still carry an advisory (over max_bytes, a
			// shadowed lower-precedence source); the collapsed count would hide
			// it, so surface it on its own line. In-sync included: a shadowed
			// source stays shadowed once the write stabilizes, which is every
			// push after the first, and dropping the advisory there is exactly
			// the "silently dropped" outcome the multi-root contract rules out.
			if ch.Warning != "" && ch.Action != engine.ActionConflict {
				warned = append(warned, ch)
			}
		}
		if verbose {
			for _, ch := range created {
				output.Line(output.LevelInfo, "%-*s  + %s  <- %s", width, name, ch.DestRel, sourceList(ch))
			}
			for _, ch := range updated {
				output.Line(output.LevelInfo, "%-*s  ~ %s  <- %s", width, name, ch.DestRel, sourceList(ch))
			}
		} else if counts := changeCounts(len(created), len(updated), dryRun); counts != "" {
			dests := make([]string, 0, len(created)+len(updated))
			for _, ch := range created {
				dests = append(dests, ch.DestRel)
			}
			for _, ch := range updated {
				dests = append(dests, ch.DestRel)
			}
			output.Line(output.LevelInfo, "%-*s  %s  %s", width, name, counts, folderBreakdown(dests))
		}
		for _, ch := range warned {
			output.Line(output.LevelWarn, "%-*s  ! %s (warning: %s)", width, name, ch.DestRel, ch.Warning)
		}
		for _, ch := range conflicts {
			output.Line(output.LevelWarn, "%-*s  ! conflict  %s%s", width, name, ch.DestRel, annotate(ch.Reason, ch.Warning))
		}
		// The presets deliberately map optional store files (mcp.yaml,
		// permissions.yaml, native/<a>/**), so a store that hasn't grown into
		// them yet reports ~5 missing sources per adapter — dozens of lines
		// burying the handful of real writes. It is the absence of an optional
		// file, not a failure, so it collapses to a count.
		if len(missing) > 0 {
			if verbose {
				for _, ch := range missing {
					output.Line(output.LevelSkip, "%-*s  – %s%s", width, name, ch.DestRel, annotate(ch.Reason, ch.Warning))
				}
			} else {
				dests := make([]string, len(missing))
				for i, ch := range missing {
					dests[i] = ch.DestRel
				}
				output.Line(output.LevelSkip, "%-*s  – %d missing source(s)  %s", width, name, len(missing), folderBreakdown(dests))
				collapsedMissing = true
			}
		}
		// Unsupported skips stay per file: "hook wiring skipped — run
		// `friday push` to review" is a decision waiting on the user.
		for _, ch := range skips {
			output.Line(output.LevelSkip, "%-*s  – %s%s", width, name, ch.DestRel, annotate(ch.Reason, ch.Warning))
		}
	}
	if collapsedMissing {
		output.Dim("run with --verbose to name each missing source")
	}
	if inSync > 0 {
		output.Dim("%d file(s) in sync", inSync)
	}
	if showDiff {
		printDiffs(changes)
	}
	printSummary(changes, dryRun)
}

// sourceList renders one change's store-relative sources, tagged with the root
// each came from whenever a multi-root store makes that ambiguous.
//
// Two shapes need tagging, for different reasons. A concatenate entry file
// genuinely contributes from several roots at once, so its Sources list can
// read "core.md, core.md" — useless in precisely the case --verbose exists
// for; SourceAbs carries each entry's absolute path positionally, so the
// owning root is that path minus the store-relative tail, and its last
// segment names the source (a configured source is cloned to a directory
// named after it, and the primary's root is the store directory itself).
//
// Every single-winner strategy — merge-json, merge-toml, copy, render —
// instead emits exactly one source per change, so no amount of comparing
// SourceAbs entries can ever reveal which of several configured sources won.
// The engine records that in Change.Root, set only when more than one root is
// configured. Without reading it, --verbose silently rendered the majority of
// a multi-source store's files exactly like a single-source one, which is the
// opposite of what the flag promises.
func sourceList(ch engine.Change) string {
	roots := make([]string, len(ch.Sources))
	distinct := 0
	for i, rel := range ch.Sources {
		// MissingSource changes carry "" placeholders — no file on disk, so
		// there is no root to attribute the entry to.
		if i >= len(ch.SourceAbs) || ch.SourceAbs[i] == "" {
			continue
		}
		roots[i] = filepath.Clean(strings.TrimSuffix(ch.SourceAbs[i], filepath.FromSlash(rel)))
		if !slices.Contains(roots[:i], roots[i]) {
			distinct++
		}
	}
	if distinct < 2 {
		if len(ch.Sources) == 1 && ch.Root != "" {
			return ch.Sources[0] + "@" + ch.Root
		}
		return strings.Join(ch.Sources, ", ")
	}
	tagged := make([]string, len(ch.Sources))
	for i, rel := range ch.Sources {
		tagged[i] = rel
		if roots[i] != "" {
			tagged[i] += "@" + filepath.Base(roots[i])
		}
	}
	return strings.Join(tagged, ", ")
}

// annotate renders the trailing "(reason)" and "(warning: …)" suffixes a
// change may carry, either or both, so a conflict or skip still explains itself.
func annotate(reason, warning string) string {
	var s string
	if reason != "" {
		s += " (" + reason + ")"
	}
	if warning != "" {
		s += " (warning: " + warning + ")"
	}
	return s
}

// tally is the per-action count over a set of changes.
type tally struct {
	created, updated, inSync, conflict, skipped int
}

func (t *tally) add(ch engine.Change) {
	switch ch.Action {
	case engine.ActionCreate:
		t.created++
	case engine.ActionUpdate:
		t.updated++
	case engine.ActionInSync:
		t.inSync++
	case engine.ActionConflict:
		t.conflict++
	case engine.ActionMissingSource, engine.ActionUnsupported:
		t.skipped++
	}
}

// format renders the tally, omitting any zero bucket so the line stays terse.
func (t tally) format() string {
	var parts []string
	if t.created > 0 {
		parts = append(parts, fmt.Sprintf("%d created", t.created))
	}
	if t.updated > 0 {
		parts = append(parts, fmt.Sprintf("%d updated", t.updated))
	}
	if t.conflict > 0 {
		parts = append(parts, fmt.Sprintf("%d conflict(s)", t.conflict))
	}
	if t.skipped > 0 {
		parts = append(parts, fmt.Sprintf("%d skipped", t.skipped))
	}
	if t.inSync > 0 {
		parts = append(parts, fmt.Sprintf("%d in-sync", t.inSync))
	}
	if len(parts) == 0 {
		return "no changes"
	}
	return strings.Join(parts, ", ")
}

// printSummary closes the report with a single grand-total line. The per-adapter
// counts already appear in the body above, so the summary stays one line.
func printSummary(changes []engine.Change, dryRun bool) {
	var grand tally
	for _, ch := range changes {
		grand.add(ch)
	}
	output.Dim("summary: %s", grand.format())
}

// printDiffs prints the content diff for each pending render, under a shared
// "diffs:" header. Used by push --diff and status --diff so both render the same.
func printDiffs(changes []engine.Change) {
	any := false
	for _, ch := range changes {
		switch ch.Action {
		case engine.ActionCreate, engine.ActionUpdate, engine.ActionConflict:
			if !any {
				output.Header("diffs:")
				any = true
			}
			output.Dim("%s → %s", ch.Adapter, ch.DestRel)
			printDiff(ch.OldContent, ch.NewContent)
		}
	}
}

// diffContext is how many unchanged lines surround each change run in the
// windowed --diff view; maxDiffLines caps one file's output so a large render
// can't flood the terminal.
const (
	diffContext  = 3
	maxDiffLines = 60
)

// printDiff renders a windowed view of the change: only the edited regions plus
// diffContext lines of surrounding context, elided runs shown as "…", capped at
// maxDiffLines with a "+N more" tail, and a "(+A −B, H hunks)" footer. The full
// untruncated diff still lives in the interactive conflict resolver
// (conflict.renderDiffColored), which needs complete context to resolve.
func printDiff(old, newC []byte) {
	windowed, added, removed, hunks, overflow := windowDiff(textdiff.LineDiff(old, newC))
	for _, line := range windowed {
		output.DiffLine(line)
	}
	if overflow > 0 {
		output.Dim("… +%d more line(s)", overflow)
	}
	if added+removed > 0 {
		output.Dim("(+%d −%d, %d hunk%s)", added, removed, hunks, plural(hunks))
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// windowDiff trims a full LineDiff to its changed regions plus diffContext
// lines of context, capped at maxDiffLines. The windowing itself is pure line
// algebra, so it lives in textdiff beside LineDiff and Merge — this view only
// supplies the presentation constants. It is textdiff.Window's only caller
// today; the split keeps the algorithm testable without a CLI harness.
func windowDiff(lines []string) (out []string, added, removed, hunks, overflow int) {
	return textdiff.Window(lines, diffContext, maxDiffLines)
}

func exitCode(changes []engine.Change) int {
	for _, ch := range changes {
		if ch.Action == engine.ActionConflict {
			return 2
		}
	}
	return 0
}

// exitErr bridges the change-list exit-code contract onto urfave's
// error-returning Action signature: 0 becomes a nil error so urfave takes no
// further action, any other code becomes cli.Exit("", code) — an empty
// message because the command has already printed its own report, and the
// non-zero code is the only thing left for the framework to carry upward.
func exitErr(changes []engine.Change) error {
	if code := exitCode(changes); code != 0 {
		return ucli.Exit("", code)
	}
	return nil
}
