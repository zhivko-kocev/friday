package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/zhivko-kocev/friday/internal/conflict"
	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/output"
	"github.com/zhivko-kocev/friday/internal/snapshot"
	"github.com/zhivko-kocev/friday/internal/ui"
)

// interactiveResolver bridges engine's resolver contract to the conflict
// package's stdin prompt. Returned only when the caller wants prompts —
// CI mode passes nil so engine surfaces drift as conflicts and skips.
func interactiveResolver() engine.ConflictResolver {
	return func(info engine.ConflictInfo) engine.Resolution {
		output.Warn("CONFLICT: %s", info.DestRel)
		output.Dim("  source: %v", info.Sources)
		output.Dim("  dest:   %s", info.DestPath)
		labelCanonical, labelDest := labelsFor(info.Direction)
		choice, merged := conflict.Prompt(labelCanonical, labelDest, info.NewContent, info.OldContent, info.BaseContent)
		switch choice {
		case conflict.ChoiceKeep:
			return engine.Resolution{Choice: engine.ConflictKeepCanonical}
		case conflict.ChoiceTake:
			return engine.Resolution{Choice: engine.ConflictTakeTarget}
		case conflict.ChoiceMerge:
			return engine.Resolution{Choice: engine.ConflictUseMerged, Content: merged}
		default:
			return engine.Resolution{Choice: engine.ConflictSkip}
		}
	}
}

// baseLookup resolves drift baseline hashes to snapshot blobs so conflict
// prompts can offer a 3-way merge. Nil when the snapshot dir is unavailable.
func baseLookup() func(string) ([]byte, bool) {
	return snapshot.BaseLookup()
}

// hookWriteConfirmer prompts before friday installs co-owned hook wiring
// (merge-json) into a settings file. Those commands run arbitrary shell on tool
// use, and a synced store may be someone else's repo, so the write is never
// silent. Returned only when prompts are wanted; non-interactive callers pass
// nil so the engine skips wiring rather than installing it unattended.
func hookWriteConfirmer() engine.ConfirmWriter {
	return func(info engine.WriteConfirmInfo) bool {
		verb := "update the hooks in"
		if info.Creating {
			verb = "create"
		}
		output.Warn("%s wants to %s %s:", info.Adapter, verb, info.DestRel)
		cmds := engine.HookCommands(info.Source)
		if len(cmds) == 0 {
			output.Dim("  (%d bytes of hook config)", len(info.Source))
		} else {
			for _, c := range cmds {
				output.Dim("  %s", c)
			}
		}
		output.Dim("  these run arbitrary shell on tool use")
		if !info.Creating {
			output.Dim("  merged into %s — your own hooks there are kept", info.DestRel)
		}
		return confirmYesNo("install these hooks?")
	}
}

// confirmYesNo asks a yes/no question, defaulting to no. It uses the rich TTY
// prompt when a terminal is attached and a plain stdin read otherwise (piped
// input, tests) — the same fallback the conflict prompt uses.
func confirmYesNo(q string) bool {
	if ui.Interactive() {
		return ui.Confirm(q)
	}
	fmt.Fprintf(os.Stdout, "%s [y/N] > ", q)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

func labelsFor(dir engine.Direction) (canonical, dest string) {
	switch dir {
	case engine.DirPush:
		return "canonical (store)", "target"
	case engine.DirPull:
		return "target", "store"
	}
	return "", ""
}
