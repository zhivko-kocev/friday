package tui

import (
	"fmt"
	"strings"

	"github.com/zhivko-kocev/friday/internal/engine"
)

// renderConfirm draws the modal that asks before friday installs co-owned hook
// wiring (merge-json) into a settings file. Those commands run arbitrary shell
// on tool use and a synced store may be someone else's repo, so the write is
// never silent — the user sees the exact commands, that friday's entries are
// merged in (their own hooks kept), and answers y/N.
func renderConfirm(info engine.WriteConfirmInfo, st styles) string {
	verb := "update the hooks in"
	if info.Creating {
		verb = "create"
	}

	var b strings.Builder
	b.WriteString(st.warn.Render("install hooks?") + "\n\n")
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
	b.WriteString("\n")
	b.WriteString(st.ok.Render("  [y] install") + st.footer.Render("   [n] skip") + "\n")
	return b.String()
}
