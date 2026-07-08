package cli

import (
	"fmt"
	"strings"

	"github.com/zhivko-kocev/friday/internal/conflict"
	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/output"
)

// report prints a per-adapter summary of the changes the engine produced,
// collapsed so the common case fits on a screen: created/updated files fold
// into one count-plus-folder-breakdown line per adapter, in-sync files never
// list individually (they'd be pure noise on a re-run), and only the things
// that need attention — conflicts and skips — are named one per line. A final
// grand-total line closes it out. --diff appends a separate windowed section.
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

	inSync := 0
	for _, name := range order {
		var created, updated []string
		var conflicts, skips, warned []engine.Change
		for _, ch := range byAdapter[name] {
			switch ch.Action {
			case engine.ActionInSync:
				inSync++
			case engine.ActionCreate:
				created = append(created, ch.DestRel)
			case engine.ActionUpdate:
				updated = append(updated, ch.DestRel)
			case engine.ActionConflict:
				conflicts = append(conflicts, ch)
			case engine.ActionMissingSource, engine.ActionUnsupported:
				skips = append(skips, ch)
			}
			// A rendered file can still carry an advisory (e.g. over max_bytes);
			// the collapsed count would hide it, so surface it on its own line.
			if ch.Warning != "" && (ch.Action == engine.ActionCreate || ch.Action == engine.ActionUpdate) {
				warned = append(warned, ch)
			}
		}
		if counts := changeCounts(len(created), len(updated), dryRun); counts != "" {
			dests := append(append([]string{}, created...), updated...)
			output.Line(output.LevelInfo, "%-*s  %s  %s", width, name, counts, folderBreakdown(dests))
		}
		for _, ch := range warned {
			output.Line(output.LevelWarn, "%-*s  ! %s (warning: %s)", width, name, ch.DestRel, ch.Warning)
		}
		for _, ch := range conflicts {
			output.Line(output.LevelWarn, "%-*s  ! conflict  %s%s", width, name, ch.DestRel, annotate(ch.Reason, ch.Warning))
		}
		for _, ch := range skips {
			output.Line(output.LevelSkip, "%-*s  – %s%s", width, name, ch.DestRel, annotate(ch.Reason, ch.Warning))
		}
	}
	if inSync > 0 {
		output.Dim("%d file(s) in sync", inSync)
	}
	if showDiff {
		printDiffs(changes)
	}
	printSummary(changes, dryRun)
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
	windowed, added, removed, hunks, overflow := windowDiff(conflict.LineDiff(old, newC))
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

// windowDiff trims a full LineDiff — lines prefixed "  " (context), "- ", or
// "+ " — to its changed regions plus diffContext lines of context, replacing
// each elided run (including leading/trailing file body) with a single "…"
// marker. It returns the windowed lines, the added/removed line counts, the
// number of change runs (hunks), and how many kept lines were dropped past the
// maxDiffLines cap (0 if none).
func windowDiff(lines []string) (out []string, added, removed, hunks, overflow int) {
	isChange := func(s string) bool {
		return strings.HasPrefix(s, "+") || strings.HasPrefix(s, "-")
	}
	n := len(lines)
	keep := make([]bool, n)
	inRun := false
	for i, l := range lines {
		if isChange(l) {
			keep[i] = true
			if strings.HasPrefix(l, "+") {
				added++
			} else {
				removed++
			}
			if !inRun {
				hunks++
				inRun = true
			}
			for d := 1; d <= diffContext; d++ {
				if i-d >= 0 {
					keep[i-d] = true
				}
				if i+d < n {
					keep[i+d] = true
				}
			}
		} else {
			inRun = false
		}
	}

	pendingEllipsis := false
	for i := 0; i < n; i++ {
		if !keep[i] {
			pendingEllipsis = true
			continue
		}
		if len(out) >= maxDiffLines {
			for j := i; j < n; j++ {
				if keep[j] {
					overflow++
				}
			}
			return out, added, removed, hunks, overflow
		}
		if pendingEllipsis {
			out = append(out, "…")
			pendingEllipsis = false
		}
		out = append(out, lines[i])
	}
	if pendingEllipsis && len(out) > 0 {
		out = append(out, "…")
	}
	return out, added, removed, hunks, overflow
}

func exitCode(changes []engine.Change) int {
	for _, ch := range changes {
		if ch.Action == engine.ActionConflict {
			return 2
		}
	}
	return 0
}
