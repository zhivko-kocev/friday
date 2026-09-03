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

// stdin is the one buffered reader every plain-text prompt in this package
// reads from. bufio reads ahead up to 4 KiB, so a reader allocated per
// prompt swallows the rest of a piped answer stream into a buffer it then
// discards: `printf 'y\ny\ny\n' | friday push` would wire the first hook and
// report every later one as declined. One reader keeps scripted answers
// working across the whole run, conflict prompts included.
var stdin = bufio.NewReader(os.Stdin)

// interactiveResolver bridges engine's resolver contract to the conflict
// package's stdin prompt. Returned only when the caller wants prompts —
// CI mode passes nil so engine surfaces drift as conflicts and skips.
func interactiveResolver() engine.ConflictResolver {
	return func(info engine.ConflictInfo) engine.Resolution {
		labelCanonical, labelDest := labelsFor(info.Direction)
		var choice conflict.Choice
		var merged []byte
		// The prompt cannot be suppressed, so neither can the file it is
		// about or the diff behind [d] — answering "keep canonical" blind
		// discards the target's hand edits.
		output.WithPromptContext(func() {
			output.Warn("CONFLICT: %s", info.DestRel)
			output.Dim("  source: %v", info.Sources)
			output.Dim("  dest:   %s", info.DestPath)
			choice, merged = conflict.Prompt(stdin, info.DestRel, labelCanonical, labelDest, info.NewContent, info.OldContent, info.BaseContent)
		})
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

// hookWriteConfirmer prompts before friday installs hook code: co-owned wiring
// merged into a settings file (merge-json), or a wholesale executable hook
// plugin (an OpenCode plugin / an oh-my-pi extension). Either runs
// unattended on tool use, and a synced store may be someone else's repo, so
// the write is never silent. Returned only when prompts are wanted;
// non-interactive callers pass nil so the engine skips wiring rather than
// installing it unattended.
func hookWriteConfirmer() engine.ConfirmWriter {
	return func(info engine.WriteConfirmInfo) bool {
		verb := "install"
		if info.Merge {
			verb = "update the hooks in"
			if info.Creating {
				verb = "create"
			}
		}
		var ok bool
		// --quiet drops progress chatter, not the evidence a consent prompt
		// is asking about: "install this hook?" with the adapter, the target
		// file, and the shell commands stripped out is not informed consent.
		output.WithPromptContext(func() {
			output.Warn("%s wants to %s %s:", info.Adapter, verb, info.DestRel)
			if info.Merge {
				// merge-json: the source is a hooks JSON blob — show its commands.
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
			} else {
				// A plugin install: the source IS the code — show it so consent is
				// informed, and be clear it replaces any existing file.
				for _, line := range hookBodyPreview(info.Source) {
					output.Dim("  %s", line)
				}
				output.Dim("  this runs as a plugin on tool use; overwrites any existing %s", info.DestRel)
			}
			ok = confirmYesNo("install this hook?")
		})
		return ok
	}
}

// hookBodyPreview returns the hook plugin body as display lines, capped so a
// long file doesn't flood the prompt.
func hookBodyPreview(src []byte) []string {
	const max = 40
	lines := strings.Split(strings.TrimRight(string(src), "\n"), "\n")
	if len(lines) <= max {
		return lines
	}
	out := append(lines[:max:max], fmt.Sprintf("… (%d more lines)", len(lines)-max))
	return out
}

// confirmYesNo asks a yes/no question, defaulting to no. It uses the rich TTY
// prompt when a terminal is attached and a plain stdin read otherwise (piped
// input, tests) — the same fallback the conflict prompt uses.
func confirmYesNo(q string) bool {
	if ui.Interactive() {
		return ui.Confirm(q)
	}
	fmt.Fprintf(os.Stdout, "%s [y/N] > ", q)
	line, err := stdin.ReadString('\n')
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
