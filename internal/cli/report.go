package cli

import (
	"fmt"
	"strings"

	"github.com/zhivko-kocev/friday/internal/conflict"
	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/output"
)

// report prints a per-adapter summary of changes the engine produced,
// then a one-line totals tally so the user doesn't have to scan the body.
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
	for _, name := range order {
		output.Header("adapter: " + name)
		for _, ch := range byAdapter[name] {
			line := formatLine(ch, dryRun)
			switch ch.Action {
			case engine.ActionInSync:
				output.OK("%s", line)
			case engine.ActionCreate, engine.ActionUpdate:
				output.Info("%s", line)
				if showDiff {
					printDiff(ch.OldContent, ch.NewContent)
				}
			case engine.ActionMissingSource, engine.ActionUnsupported:
				output.Skip("%s", line)
			case engine.ActionConflict:
				output.Warn("%s", line)
			}
		}
	}
	printSummary(changes, dryRun)
}

// tally is the per-action count for one adapter (or the grand total).
type tally struct {
	created, updated, inSync, conflict, skipped int
}

func (t tally) add(other tally) tally {
	return tally{
		created:  t.created + other.created,
		updated:  t.updated + other.updated,
		inSync:   t.inSync + other.inSync,
		conflict: t.conflict + other.conflict,
		skipped:  t.skipped + other.skipped,
	}
}

func (t tally) format() string {
	parts := []string{
		fmt.Sprintf("%d created", t.created),
		fmt.Sprintf("%d updated", t.updated),
		fmt.Sprintf("%d in-sync", t.inSync),
	}
	if t.conflict > 0 {
		parts = append(parts, fmt.Sprintf("%d conflict(s)", t.conflict))
	}
	if t.skipped > 0 {
		parts = append(parts, fmt.Sprintf("%d skipped", t.skipped))
	}
	return strings.Join(parts, ", ")
}

// printSummary lists the adapters touched, the per-action tally for each, and
// a grand total. Conflict/skipped buckets are omitted from rows where the
// count is zero so the common path stays terse.
func printSummary(changes []engine.Change, dryRun bool) {
	byAdapter := map[string]tally{}
	order := []string{}
	for _, ch := range changes {
		if _, seen := byAdapter[ch.Adapter]; !seen {
			order = append(order, ch.Adapter)
		}
		t := byAdapter[ch.Adapter]
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
		byAdapter[ch.Adapter] = t
	}

	prefix := "summary:"
	if dryRun {
		prefix = "summary (dry-run):"
	}
	output.Header(prefix)
	output.Dim("  adapters: %s", strings.Join(order, ", "))

	width := 0
	for _, name := range order {
		if len(name) > width {
			width = len(name)
		}
	}
	var grand tally
	for _, name := range order {
		t := byAdapter[name]
		output.Dim("  %-*s  %s", width, name, t.format())
		grand = grand.add(t)
	}
	output.Dim("  %-*s  %s", width, "total", grand.format())
}

func formatLine(ch engine.Change, dryRun bool) string {
	verb := ch.Action.String()
	if dryRun && (ch.Action == engine.ActionCreate || ch.Action == engine.ActionUpdate) {
		verb = "would-" + verb
	}
	parts := []string{fmt.Sprintf("%-15s", verb)}
	if len(ch.Sources) > 0 && ch.Sources[0] != "" {
		parts = append(parts, strings.Join(ch.Sources, "+"))
	}
	if ch.DestRel != "" {
		parts = append(parts, "→ "+ch.DestRel)
	}
	if ch.Reason != "" {
		parts = append(parts, "("+ch.Reason+")")
	}
	if ch.Warning != "" {
		parts = append(parts, "(warning: "+ch.Warning+")")
	}
	return strings.Join(parts, "  ")
}

func printDiff(old, newC []byte) {
	for _, line := range conflict.LineDiff(old, newC) {
		output.DiffLine(line)
	}
}

func exitCode(changes []engine.Change) int {
	for _, ch := range changes {
		if ch.Action == engine.ActionConflict {
			return 2
		}
	}
	return 0
}
