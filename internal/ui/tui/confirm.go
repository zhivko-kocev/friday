package tui

import (
	"fmt"
	"strings"

	"github.com/zhivko-kocev/friday/internal/engine"
)

// renderConfirm draws the modal that asks before friday installs hook code:
// co-owned wiring merged into a settings file (merge-json), or a wholesale
// executable hook plugin (an OpenCode plugin / pi extension). Either runs
// unattended on tool use and a synced store may be someone else's repo, so the
// write is never silent — for a merge the user sees the exact commands (their
// own hooks kept); for a plugin they see the code that will be installed, and
// answers y/N.
func renderConfirm(info engine.WriteConfirmInfo, st styles) string {
	var b strings.Builder
	b.WriteString(st.warn.Render("install hooks?") + "\n\n")

	if info.Merge {
		verb := "update the hooks in"
		if info.Creating {
			verb = "create"
		}
		b.WriteString("  " + st.changeHd.Render(fmt.Sprintf("%s wants to %s %s", info.Adapter, verb, info.DestRel)) + "\n")
		cmds := engine.HookCommands(info.Source)
		if len(cmds) == 0 {
			b.WriteString(st.footer.Render(fmt.Sprintf("  (%d bytes of hook config)", len(info.Source))) + "\n")
		} else {
			for _, c := range cmds {
				b.WriteString(st.footer.Render("  "+c) + "\n")
			}
		}
		b.WriteString(st.warn.Render("  these run arbitrary shell on tool use") + "\n")
		if !info.Creating {
			b.WriteString(st.footer.Render(fmt.Sprintf("  merged into %s — your own hooks there are kept", info.DestRel)) + "\n")
		}
	} else {
		b.WriteString("  " + st.changeHd.Render(fmt.Sprintf("%s wants to install %s", info.Adapter, info.DestRel)) + "\n")
		for _, line := range hookBodyPreview(info.Source) {
			b.WriteString(st.footer.Render("  "+line) + "\n")
		}
		b.WriteString(st.warn.Render(fmt.Sprintf("  this runs as a plugin on tool use; overwrites any existing %s", info.DestRel)) + "\n")
	}

	b.WriteString("\n")
	b.WriteString(st.ok.Render("  [y] install") + st.footer.Render("   [n] skip") + "\n")
	return b.String()
}

// hookBodyPreview returns the hook plugin body as display lines, capped so a
// long file doesn't overflow the modal.
func hookBodyPreview(src []byte) []string {
	const max = 30
	lines := strings.Split(strings.TrimRight(string(src), "\n"), "\n")
	if len(lines) <= max {
		return lines
	}
	return append(lines[:max:max], fmt.Sprintf("… (%d more lines)", len(lines)-max))
}
