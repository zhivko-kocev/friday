package engine

import "io/fs"

// Direction is push (store → target) or pull (target → store).
type Direction int

const (
	DirPush Direction = iota
	DirPull
)

func (d Direction) String() string {
	switch d {
	case DirPush:
		return "push"
	case DirPull:
		return "pull"
	}
	return "?"
}

// Action describes the disk effect of a single change.
type Action int

const (
	ActionInSync Action = iota
	ActionCreate
	ActionUpdate
	ActionConflict
	ActionMissingSource
	ActionUnsupported // e.g. pulling a concatenate rule
)

func (a Action) String() string {
	switch a {
	case ActionInSync:
		return "in-sync"
	case ActionCreate:
		return "create"
	case ActionUpdate:
		return "update"
	case ActionConflict:
		return "conflict"
	case ActionMissingSource:
		return "missing-source"
	case ActionUnsupported:
		return "unsupported"
	}
	return "?"
}

// Change is a single planned (or applied) write — or a no-op explaining why.
type Change struct {
	Adapter    string
	Direction  Direction
	Sources    []string // store-relative paths for push; target-relative for pull
	DestPath   string   // absolute path that would be written (or was)
	DestRel    string   // human-friendly relative path for display
	Action     Action
	OldContent []byte
	NewContent []byte
	Mode       fs.FileMode // mode of the source file; 0 means "use default"
	Reason     string      // for skip/conflict/unsupported
}

// ConflictChoice is the engine-level outcome of a drift resolution prompt.
type ConflictChoice int

const (
	// ConflictKeepCanonical writes the canonical (source) version, overwriting
	// the drifted destination.
	ConflictKeepCanonical ConflictChoice = iota
	// ConflictTakeTarget keeps the destination as-is and updates the drift
	// baseline so future runs treat it as the new canonical hash.
	ConflictTakeTarget
	// ConflictSkip leaves both sides untouched and reports the change as a
	// conflict in the summary.
	ConflictSkip
)

// ConflictInfo is the snapshot the resolver receives. It carries everything
// the prompt needs without leaking the engine's internal Change type.
type ConflictInfo struct {
	Adapter    string
	Direction  Direction
	Sources    []string
	DestPath   string
	DestRel    string
	OldContent []byte // current target content (the drifted version)
	NewContent []byte // canonical content the engine would write
}

// ConflictResolver is invoked when a push would overwrite a file the user
// has edited since friday last wrote it. Returning ConflictSkip with a nil
// resolver is the default in non-interactive mode.
type ConflictResolver func(ConflictInfo) ConflictChoice

// Options controls a single Push/Pull/Status invocation.
type Options struct {
	Adapters   []string // empty = all adapters in config
	DryRun     bool
	Force      bool             // overwrite without prompting
	OnConflict ConflictResolver // nil = treat drift as Conflict (skip)
	ShowDiff   bool             // print a diff for each Update/Create — caller's responsibility
}
