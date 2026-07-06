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
	RuleIndex  int      // index into the adapter's Rules that produced this change
	Sources    []string // store-relative paths for push; target-relative for pull
	SrcAbs     string   // absolute path of the single source file ("" for concat)
	SrcContent []byte   // raw on-disk bytes of SrcAbs, pre-transform — baseline material
	DestPath   string   // absolute path that would be written (or was)
	DestRel    string   // human-friendly relative path for display
	Action     Action
	OldContent []byte
	NewContent []byte
	Mode       fs.FileMode // mode of the source file; 0 means "use default"
	Reason     string      // for skip/conflict/unsupported
	Warning    string      // advisory that survives resolution (e.g. max_bytes)

	// Engine-internal resolution state — never rendered.
	acceptedDrift  bool   // ConflictTakeTarget: adopt the dest as the new baseline
	mergedPush     bool   // ConflictUseMerged on push: NewContent is the merge result
	storeWriteBack []byte // push-merge on an invertible rule: content for SrcAbs
	staleTarget    bool   // pull downgraded because the target never drifted
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
	// ConflictUseMerged writes the resolver-supplied merged content instead
	// of either side. Resolution.Content carries it.
	ConflictUseMerged
)

// Resolution is what a resolver returns: the choice, plus merged content
// when the choice is ConflictUseMerged.
type Resolution struct {
	Choice  ConflictChoice
	Content []byte
}

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
	// BaseContent is the last-synced content both sides diverged from —
	// recovered from the snapshot blob store via the drift baseline hash.
	// Nil when unknown (no snapshot yet); the prompt then omits merge.
	BaseContent []byte
}

// ConflictResolver is invoked when a write would clobber edits on the other
// side. A nil resolver (non-interactive mode) surfaces drift as Conflict.
type ConflictResolver func(ConflictInfo) Resolution

// Options controls a single Push/Pull/Status invocation.
type Options struct {
	Adapters   []string // empty = all adapters in config
	DryRun     bool
	Force      bool             // overwrite without prompting
	OnConflict ConflictResolver // nil = treat drift as Conflict (skip)
	ShowDiff   bool             // print a diff for each Update/Create — caller's responsibility
	Only       []string         // store-relative source globs; non-empty drops changes with no matching source
	// BaseLookup resolves a drift baseline hash to its content (snapshot
	// blob store). Nil disables merge-base recovery.
	BaseLookup func(hash string) ([]byte, bool)
	// driftPath overrides where baselines are read and written. Compile sets
	// it to a throwaway file so temp-store runs never touch the user's real
	// drift state. Empty means drift.DefaultPath().
	driftPath string
}
