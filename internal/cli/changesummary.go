package cli

import (
	"fmt"
	"path/filepath"
	"strings"
)

// maxBreakdownGroups caps how many folder groups a breakdown lists before the
// rest fold into a "+N more" tail, so one adapter with many touched
// directories still renders on a single line.
const maxBreakdownGroups = 4

// folderBreakdown summarizes a set of destination-relative paths as a compact,
// parenthesized list grouped by top-level folder: a root-level file is shown by
// name (CLAUDE.md), files inside a directory collapse to "<dir>/" with a "×N"
// suffix when more than one landed there (agents/×2). Group order follows
// first appearance so output is deterministic. Returns "" for no paths.
//
// Shared by the push/pull report and the status grid so both render change
// locations the same way.
func folderBreakdown(dests []string) string {
	if len(dests) == 0 {
		return ""
	}
	var order []string
	count := map[string]int{}
	for _, d := range dests {
		key := filepath.ToSlash(d)
		if i := strings.IndexByte(key, '/'); i >= 0 {
			key = key[:i] + "/"
		}
		if _, seen := count[key]; !seen {
			order = append(order, key)
		}
		count[key]++
	}
	parts := make([]string, 0, len(order))
	for _, key := range order {
		if len(parts) == maxBreakdownGroups && len(order) > maxBreakdownGroups {
			parts = append(parts, fmt.Sprintf("+%d more", len(order)-len(parts)))
			break
		}
		if strings.HasSuffix(key, "/") && count[key] > 1 {
			parts = append(parts, fmt.Sprintf("%s×%d", key, count[key]))
		} else {
			parts = append(parts, key)
		}
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// changeCounts renders the created/updated headline for one adapter, omitting a
// zero bucket. Under a dry run it uses the infinitive ("to create") since
// nothing was written yet; otherwise the past tense ("created"). Returns "" when
// both counts are zero.
func changeCounts(created, updated int, dryRun bool) string {
	phrase := func(n int, past, future string) string {
		if dryRun {
			return fmt.Sprintf("%d to %s", n, future)
		}
		return fmt.Sprintf("%d %s", n, past)
	}
	var parts []string
	if created > 0 {
		parts = append(parts, phrase(created, "created", "create"))
	}
	if updated > 0 {
		parts = append(parts, phrase(updated, "updated", "update"))
	}
	return strings.Join(parts, ", ")
}
