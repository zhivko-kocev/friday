package cli

import (
	"fmt"
	"strings"

	"github.com/zhivko-kocev/friday/internal/conflict"
	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/output"
)

// report prints a per-adapter summary of changes the engine produced.
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
