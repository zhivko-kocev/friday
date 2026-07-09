package tui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/zhivko-kocev/friday/internal/conflict"
	"github.com/zhivko-kocev/friday/internal/engine"
)

// diffContext is how many unchanged lines surround each change run, and
// maxDiffLines caps one file's diff so a large render doesn't bury the plan
// (both mirror the CLI's --diff view — see conflict.Window).
const (
	diffContext  = 3
	maxDiffLines = 60
)

// renderChanges turns a plan into the viewport body. It renders into a string —
// never through internal/output, which writes the real stdout the alt-screen
// owns. Sync produces both directions, so changes are split into capture (pull)
// and fan-out (push) sections (shown only when both are present); in-sync files
// fold into a count, and pull rows the engine can't reverse (concatenate →
// unsupported, absent sources → missing-source) are dropped as noise.
func renderChanges(changes []engine.Change, st styles, showDiff bool) string {
	var b strings.Builder
	b.WriteString(st.changeHd.Render("changes:"))

	inSync := 0
	var pull, push []engine.Change
	for _, ch := range changes {
		if ch.Action == engine.ActionInSync {
			inSync++
			continue
		}
		if ch.Direction == engine.DirPull &&
			(ch.Action == engine.ActionMissingSource || ch.Action == engine.ActionUnsupported) {
			continue
		}
		if ch.Direction == engine.DirPull {
			pull = append(pull, ch)
		} else {
			push = append(push, ch)
		}
	}

	mixed := len(pull) > 0 && len(push) > 0
	section := func(label string, rows []engine.Change) {
		if len(rows) == 0 {
			return
		}
		if mixed {
			b.WriteString("\n" + st.footer.Render(label))
		}
		for _, ch := range rows {
			line := fmt.Sprintf("  %-8s %-10s %s", ch.Action, ch.Adapter, ch.DestRel)
			if ch.Reason != "" {
				line += "  (" + ch.Reason + ")"
			}
			b.WriteString("\n" + st.action(ch.Action).Render(line))
			// A rendered file can carry an advisory (e.g. over max_bytes) the
			// collapsed row would otherwise hide — surface it like the CLI does.
			if ch.Warning != "" {
				b.WriteString("\n" + st.warn.Render("    ! warning: "+ch.Warning))
			}
			if showDiff && (ch.Action == engine.ActionCreate || ch.Action == engine.ActionUpdate) {
				b.WriteString(renderDiff(ch.OldContent, ch.NewContent, st))
			}
		}
	}
	section("capture (pull):", pull)
	section("fan out (push):", push)

	if len(pull) == 0 && len(push) == 0 && inSync == 0 {
		b.WriteString("\n  " + st.action(engine.ActionInSync).Render("no changes"))
	}
	if inSync > 0 {
		b.WriteString("\n" + st.action(engine.ActionInSync).Render(fmt.Sprintf("  %d file(s) in sync", inSync)))
	}
	return b.String()
}

// renderDiff returns an indented, colored, windowed unified diff of old→new
// (conflict.LineDiff windowed by conflict.Window), for the changes screen's `d`
// toggle and the conflict modal. Lines are prefixed "  " (context) / "- " /
// "+ ", distant context collapses to "…", and the whole thing caps at
// maxDiffLines with a "+N more" tail — so an edit deep in a large file is shown
// with context instead of buried behind its unchanged prefix.
func renderDiff(old, newC []byte, st styles) string {
	windowed, _, _, _, overflow := conflict.Window(conflict.LineDiff(old, newC), diffContext, maxDiffLines)
	var b strings.Builder
	for _, ln := range windowed {
		style := st.footer // context (and the "…" elision marker)
		switch {
		case strings.HasPrefix(ln, "+"):
			style = st.ok
		case strings.HasPrefix(ln, "-"):
			style = st.errText
		}
		b.WriteString("\n    " + style.Render(ln))
	}
	if overflow > 0 {
		b.WriteString("\n    " + st.footer.Render(fmt.Sprintf("… +%d more diff lines", overflow)))
	}
	return b.String()
}

// hasDirection reports whether any change moves in dir — used to detect a
// combined sync plan (both directions present).
func hasDirection(changes []engine.Change, dir engine.Direction) bool {
	return slices.ContainsFunc(changes, func(ch engine.Change) bool { return ch.Direction == dir })
}
