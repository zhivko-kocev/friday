// Package engine executes the rule sets defined in friday.yaml. It plans
// changes (without writing), resolves drift via a caller-supplied
// resolver, and applies the survivors to disk.
package engine

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/zhivko-kocev/friday/internal/atomicio"
	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/drift"
	"github.com/zhivko-kocev/friday/internal/output"
	"github.com/zhivko-kocev/friday/internal/rules"
)

const (
	defaultFileMode fs.FileMode = 0o644
	defaultDirMode  fs.FileMode = 0o755
)

// reasonAcceptedDrift is the marker on an InSync change that flags the
// "user kept their target version" outcome. apply uses it to update the
// drift baseline.
const reasonAcceptedDrift = "kept dest version (drift accepted)"

// Push applies friday.yaml rules from store → adapter targets.
func Push(cfg *config.Config, opts Options) ([]Change, error) {
	return run(cfg, opts, DirPush)
}

// Pull reverses friday.yaml rules: adapter targets → store. Concatenate
// rules and rules with frontmatter_strip are skipped as unsupported.
func Pull(cfg *config.Config, opts Options) ([]Change, error) {
	return run(cfg, opts, DirPull)
}

// planner produces the change set for one adapter. planPush, planPull, and
// planImport all satisfy it; runWith supplies the shared resolve/apply loop.
type planner func(adapterName string, ad *config.Adapter, storeAbs, targetAbs string) ([]Change, error)

func run(cfg *config.Config, opts Options, dir Direction) ([]Change, error) {
	if dir == DirPull {
		return runWith(cfg, opts, dir, planPull)
	}
	return runWith(cfg, opts, dir, planPush)
}

func runWith(cfg *config.Config, opts Options, dir Direction, plan planner) ([]Change, error) {
	adapters, err := cfg.SelectAdapters(opts.Adapters)
	if err != nil {
		return nil, err
	}

	storeAbs := cfg.StoreDir
	driftPath, err := drift.DefaultPath()
	if err != nil {
		return nil, fmt.Errorf("resolve drift path: %w", err)
	}
	store, err := drift.Load(driftPath)
	if err != nil {
		return nil, fmt.Errorf("load drift store: %w", err)
	}

	wrote := false
	var all []Change
	for _, name := range adapters {
		ad := cfg.Adapters[name]
		targetAbs, err := cfg.AdapterTargetAbs(name)
		if err != nil {
			return nil, err
		}

		changes, err := plan(name, ad, storeAbs, targetAbs)
		if err != nil {
			return nil, fmt.Errorf("adapter %s: %w", name, err)
		}
		changes = filterOnly(changes, opts.Only)

		for i := range changes {
			ch := &changes[i]
			resolveConflict(ch, store, opts)
			if !opts.DryRun {
				didWrite, err := apply(ch, store, dir)
				if err != nil {
					return nil, fmt.Errorf("apply %s: %w", ch.DestPath, err)
				}
				wrote = wrote || didWrite
			}
		}
		all = append(all, changes...)
	}

	// Only persist the drift store if a write actually happened. Avoids
	// touching the on-disk file (and its mtime) for pure status / dry-run
	// flows that hit run() with DryRun=false but produce no changes.
	if !opts.DryRun && wrote {
		if err := store.Save(); err != nil {
			output.Warn("failed to save drift store: %v", err)
		}
	}
	return all, nil
}

// filterOnly drops planned changes none of whose sources match a glob in
// only. An empty filter keeps everything. Concatenate changes carry every
// member file in Sources, so one matching member keeps the whole output —
// the dest is regenerated as a unit regardless.
func filterOnly(changes []Change, only []string) []Change {
	if len(only) == 0 {
		return changes
	}
	out := changes[:0:0]
	for _, ch := range changes {
		for _, src := range ch.Sources {
			if matchesAny(src, only) {
				out = append(out, ch)
				break
			}
		}
	}
	return out
}

func matchesAny(path string, globs []string) bool {
	for _, g := range globs {
		if rules.Match(g, path) {
			return true
		}
	}
	return false
}

// resolveConflict downgrades Update actions when the write would clobber
// edits on the other side, invoking the caller's resolver (or, if absent,
// marking the change as a Conflict so it gets skipped).
//
// Push checks the target-side baseline (did the user edit the target since
// friday last wrote it?). Pull checks the canonical-side baseline (did the
// user edit the store since the last sync?) — without it, pull silently
// eats canonical edits when both sides have moved.
func resolveConflict(ch *Change, store *drift.Store, opts Options) {
	if ch.Action != ActionUpdate {
		return
	}
	var drifted, exists bool
	switch ch.Direction {
	case DirPush:
		drifted, exists = store.Check(ch.Adapter, ch.DestPath)
		if !exists || !drifted {
			return
		}
	case DirPull:
		// Missing baseline reads as drifted (exists && drifted) — the
		// conservative stance for stores that predate canonical tracking.
		// It self-heals: the first push or pull records the baseline.
		drifted, _ = store.CheckCanonical(ch.DestPath)
		if !drifted {
			return
		}
	}
	if opts.Force {
		return
	}
	// No resolver, or we're in dry-run: surface as conflict, never prompt.
	if opts.OnConflict == nil || opts.DryRun {
		ch.Action = ActionConflict
		ch.Reason = "drift detected — use --force to overwrite or run without --dry-run"
		return
	}

	info := ConflictInfo{
		Adapter:    ch.Adapter,
		Direction:  ch.Direction,
		Sources:    ch.Sources,
		DestPath:   ch.DestPath,
		DestRel:    ch.DestRel,
		OldContent: ch.OldContent,
		NewContent: ch.NewContent,
	}
	if opts.BaseLookup != nil {
		var baseHash string
		if ch.Direction == DirPush {
			baseHash = store.BaselineHash(ch.Adapter, ch.DestPath)
		} else {
			baseHash = store.CanonicalBaselineHash(ch.DestPath)
		}
		if baseHash != "" {
			if base, ok := opts.BaseLookup(baseHash); ok {
				info.BaseContent = base
			}
		}
	}

	res := opts.OnConflict(info)
	switch res.Choice {
	case ConflictKeepCanonical:
		// Proceed with the planned write. (On pull "canonical" is the
		// incoming target version — labels flip in the prompt.)
	case ConflictUseMerged:
		// Write the merged content instead of the planned version; the
		// normal apply path records it as the new baseline.
		ch.NewContent = res.Content
	case ConflictTakeTarget:
		// Don't overwrite. Adopt the current dest as the new baseline so
		// future runs treat it as canonical until the next real edit.
		ch.Action = ActionInSync
		ch.Reason = reasonAcceptedDrift
	case ConflictSkip:
		ch.Action = ActionConflict
		ch.Reason = "skipped"
	}
}

// apply executes a single Change against disk. Returns true if a write
// (file create/update or drift-store mutation) actually happened.
func apply(ch *Change, store *drift.Store, dir Direction) (bool, error) {
	switch ch.Action {
	case ActionCreate, ActionUpdate:
		if err := os.MkdirAll(filepath.Dir(ch.DestPath), defaultDirMode); err != nil {
			return false, err
		}
		mode := ch.Mode
		if mode == 0 {
			mode = defaultFileMode
		}
		// Atomic write — a Ctrl-C mid-write leaves the previous file intact
		// rather than a half-written target.
		if err := atomicio.WriteFile(ch.DestPath, ch.NewContent, mode); err != nil {
			return false, err
		}
		switch dir {
		case DirPush:
			// Target baseline: what friday wrote. Canonical baseline: the
			// store file it was rendered from — pull consults it later.
			store.Set(ch.Adapter, ch.DestPath, ch.NewContent)
			if ch.SrcAbs != "" {
				store.SetCanonical(ch.SrcAbs, ch.SrcContent)
			}
		case DirPull:
			// After a pull both sides agree: record the store file as the
			// canonical baseline and the (unmodified) target file as the
			// target baseline, so the next push sees neither as drifted.
			store.SetCanonical(ch.DestPath, ch.NewContent)
			if ch.SrcAbs != "" {
				store.Set(ch.Adapter, ch.SrcAbs, ch.SrcContent)
			}
		}
		return true, nil
	case ActionInSync:
		// "Take target" on a conflict: adopt the current dest as the new
		// baseline so future runs stop flagging it.
		if ch.Reason == reasonAcceptedDrift {
			if dir == DirPush {
				store.Set(ch.Adapter, ch.DestPath, ch.OldContent)
			} else {
				store.SetCanonical(ch.DestPath, ch.OldContent)
			}
			return true, nil
		}
	}
	return false, nil
}
