package cli

import (
	"github.com/zhivko-kocev/friday/internal/conflict"
	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/output"
	"github.com/zhivko-kocev/friday/internal/snapshot"
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
	dir, err := snapshot.Dir()
	if err != nil {
		return nil
	}
	return func(h string) ([]byte, bool) { return snapshot.ReadBlob(dir, h) }
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
