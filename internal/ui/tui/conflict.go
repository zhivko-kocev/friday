package tui

import (
	"fmt"
	"strings"

	"github.com/zhivko-kocev/friday/internal/engine"
)

// renderConflict draws the resolution modal for one drifted file. It names both
// sides in the terms of the direction (push: canonical store vs. target agent
// file), shows their sizes and whether a last-synced base is recoverable, and
// lists the choices. Merge is intentionally absent in v0.5.0 (see the plan);
// keep/take/skip cover the safe outcomes.
func renderConflict(info engine.ConflictInfo, base []byte, st styles) string {
	canonical, target := "canonical (store)", "target"
	if info.Direction == engine.DirPull {
		canonical, target = "target", "store"
	}

	var b strings.Builder
	b.WriteString(st.errText.Render("conflict — both sides changed") + "\n\n")
	b.WriteString("  " + st.changeHd.Render(info.DestRel) + "\n")
	if len(info.Sources) > 0 {
		b.WriteString(st.footer.Render("  from: "+strings.Join(info.Sources, ", ")) + "\n")
	}
	if info.Warning != "" {
		b.WriteString(st.warn.Render("  ! warning: "+info.Warning) + "\n")
	}
	// Diff: what's on disk now (target) → what keep would write (canonical).
	b.WriteString(st.footer.Render(fmt.Sprintf("  %s → %s:", target, canonical)))
	b.WriteString(renderDiff(info.OldContent, info.NewContent, st) + "\n")
	if len(base) > 0 {
		b.WriteString(st.footer.Render(fmt.Sprintf("  (a last-synced base of %d bytes is available)", len(base))) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(st.ok.Render("  [k] keep "+canonical) + st.footer.Render("  (overwrite "+target+")") + "\n")
	b.WriteString(st.warn.Render("  [t] take "+target) + st.footer.Render("  (adopt the on-disk edit)") + "\n")
	b.WriteString(st.footer.Render("  [s] skip  (leave both untouched, report it)") + "\n")
	return b.String()
}
