package cli

import (
	"github.com/zhivko-kocev/friday/internal/conflict"
	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/output"
)

// interactiveResolver bridges engine's resolver contract to the conflict
// package's stdin prompt. Returned only when the caller wants prompts —
// CI mode passes nil so engine surfaces drift as conflicts and skips.
func interactiveResolver() engine.ConflictResolver {
	return func(info engine.ConflictInfo) engine.ConflictChoice {
		output.Warn("CONFLICT: %s", info.DestRel)
		output.Dim("  source: %v", info.Sources)
		output.Dim("  dest:   %s", info.DestPath)
		labelCanonical, labelDest := labelsFor(info.Direction)
		switch conflict.Prompt(labelCanonical, labelDest, info.NewContent, info.OldContent) {
		case conflict.ChoiceKeep:
			return engine.ConflictKeepCanonical
		case conflict.ChoiceTake:
			return engine.ConflictTakeTarget
		default:
			return engine.ConflictSkip
		}
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
