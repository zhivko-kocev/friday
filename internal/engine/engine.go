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

// reasonAcceptedDrift labels an InSync change where the user kept their
// target version. Display only — apply keys on Change.acceptedDrift.
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
	driftPath := opts.driftPath
	if driftPath == "" {
		driftPath, err = drift.DefaultPath()
		if err != nil {
			return nil, fmt.Errorf("resolve drift path: %w", err)
		}
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

		aborted := false
		for i := range changes {
			// Cancellation checkpoint: stop before touching the next change so a
			// long apply can be halted (not just conflicts fast-skipped). Only
			// the changes processed so far are reported/persisted.
			if aborted = canceled(opts.Abort); aborted {
				changes = changes[:i]
				break
			}
			ch := &changes[i]
			resolveConflict(ch, store, opts)
			if ch.mergedPush {
				prepareMergeWriteBack(ch, ad.Rules[ch.RuleIndex])
			}
			if !opts.DryRun {
				didWrite, err := apply(ch, store, dir)
				if err != nil {
					return nil, fmt.Errorf("apply %s: %w", ch.DestPath, err)
				}
				wrote = wrote || didWrite
				// Remember store files this pull actually captured, so a later
				// adapter mapping the same file isn't planned against the
				// content we just wrote. Nil map (non-pull, or a caller that
				// didn't opt in) skips the bookkeeping.
				if didWrite && dir == DirPull && opts.PulledStorePaths != nil &&
					(ch.Action == ActionCreate || ch.Action == ActionUpdate) {
					opts.PulledStorePaths[ch.DestPath] = true
				}
			}
		}
		all = append(all, changes...)
		if aborted {
			break // don't start the next adapter
		}
	}

	// Only persist the drift store if a write actually happened. Avoids
	// touching the on-disk file (and its mtime) for pure status / dry-run
	// flows that hit run() with DryRun=false but produce no changes.
	if !opts.DryRun && wrote {
		warnf := opts.Warnf
		if warnf == nil {
			warnf = output.Warn // CLI default; the control room sets a sink
		}
		if err := store.Save(); err != nil {
			warnf("failed to save drift store: %v", err)
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

// canceled reports whether an abort channel has been closed or signaled. A nil
// channel never cancels (the CLI's behavior).
func canceled(abort <-chan struct{}) bool {
	if abort == nil {
		return false
	}
	select {
	case <-abort:
		return true
	default:
		return false
	}
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
		// A target that still matches its own baseline was never edited —
		// there is nothing to capture. The planned Update means the STORE
		// side is newer; writing the stale target over it is exactly the
		// loss the baselines exist to prevent. Not a conflict, so --force
		// doesn't resurrect the overwrite either.
		if ch.SrcAbs != "" {
			if tDrifted, tExists := store.Check(ch.Adapter, ch.SrcAbs); tExists && !tDrifted {
				ch.Action = ActionInSync
				ch.Reason = "target unchanged since last push — store is newer, push to update"
				ch.staleTarget = true
				return
			}
		}
		// Another agent already captured this same store file earlier in this
		// run (the shared store moved under us). This target is either merely
		// behind that update or carries its own divergent edit — the target
		// baseline tells them apart. Only fires when a pull command opted in by
		// threading PulledStorePaths; nil map => never taken.
		if opts.PulledStorePaths[ch.DestPath] {
			if store.BaselineHash(ch.Adapter, ch.SrcAbs) == "" {
				// No baseline: never pushed, so "stale and behind" is
				// indistinguishable from a real edit. The store was just
				// updated from another agent — treat this one as behind and
				// fan out with a push rather than reverting the store.
				ch.Action = ActionInSync
				ch.Reason = "store file just updated from another agent this run — run `friday push` to update this one"
				ch.staleTarget = true
				return
			}
			// A baseline exists and the clean-stale guard above did not fire, so
			// this target genuinely drifted: this agent edited its own copy
			// differently. Surface it instead of silently dropping either edit.
			ch.Action = ActionConflict
			ch.Reason = "another agent updated this store file this run and this agent's copy also changed — pull this adapter on its own to capture its edits"
			return
		}
		// Missing canonical baseline reads as drifted (exists && drifted) —
		// the conservative stance for stores that predate canonical
		// tracking. It self-heals: in-sync runs and writes both record it.
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
		Warning:    ch.Warning,
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
		// incoming target version — the prompt words it per direction.)
	case ConflictUseMerged:
		// Write the merged content instead of the planned version; on push
		// the merge must also reach the store — see prepareMergeWriteBack.
		ch.NewContent = res.Content
		ch.mergedPush = ch.Direction == DirPush
	case ConflictTakeTarget:
		// Don't overwrite. Adopt the current dest as the new baseline so
		// future runs treat it as canonical until the next real edit.
		ch.Action = ActionInSync
		ch.Reason = reasonAcceptedDrift
		ch.acceptedDrift = true
	case ConflictSkip:
		ch.Action = ActionConflict
		ch.Reason = "skipped"
	}
}

// prepareMergeWriteBack routes a push-direction merge into the store. The
// target-side edits live only in the merged content; leaving the store file
// untouched would make the very next push plan from it and silently revert
// the merge. Invertible rules write the merge back to the store file;
// non-invertible ones (concatenate, frontmatter_strip) keep the old target
// baseline so the next push flags the file again instead of clobbering it.
func prepareMergeWriteBack(ch *Change, r *rules.Rule) {
	invertible := ch.SrcAbs != "" && r.Strategy == rules.StrategyCopy && len(r.FrontmatterStrip) == 0
	if invertible {
		ch.storeWriteBack = pullContent(r, ch.SrcContent, ch.NewContent)
		return
	}
	ch.Warning = "merge written to the target only — fold it into the store or the next push will flag this file again"
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
			switch {
			case ch.storeWriteBack != nil:
				// Merge resolution on an invertible rule: the store file
				// gets the merge too, so both sides converge and the next
				// push plans exactly what is already on disk.
				if err := atomicio.WriteFile(ch.SrcAbs, ch.storeWriteBack, mode); err != nil {
					return false, err
				}
				store.Set(ch.Adapter, ch.DestPath, ch.NewContent)
				store.SetCanonical(ch.SrcAbs, ch.storeWriteBack)
			case ch.mergedPush:
				// Merge on a non-invertible rule: the store still holds the
				// old content. Keep the old target baseline so the next push
				// re-prompts instead of silently reverting the merge.
			default:
				// Target baseline: what friday wrote. Canonical baseline: the
				// store file it was rendered from — pull consults it later.
				store.Set(ch.Adapter, ch.DestPath, ch.NewContent)
				if ch.SrcAbs != "" {
					store.SetCanonical(ch.SrcAbs, ch.SrcContent)
				}
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
		if ch.acceptedDrift {
			if dir == DirPush {
				store.Set(ch.Adapter, ch.DestPath, ch.OldContent)
			} else {
				store.SetCanonical(ch.DestPath, ch.OldContent)
			}
			return true, nil
		}
		return healBaselines(ch, store, dir), nil
	}
	return false, nil
}

// healBaselines records missing baselines for a change both sides already
// agree on. Stores that predate baseline tracking (or targets last written
// by an older friday) otherwise never acquire baselines while in sync — and
// then every later edit reads as an unresolvable conflict in non-interactive
// runs. Only absent entries are recorded, so a steady-state run stays
// save-free. Downgraded stale-target changes are excluded: their store side
// carries unsynced edits that must NOT be declared synced.
func healBaselines(ch *Change, store *drift.Store, dir Direction) (recorded bool) {
	if ch.staleTarget {
		return false
	}
	switch dir {
	case DirPush:
		if ch.OldContent != nil && store.BaselineHash(ch.Adapter, ch.DestPath) == "" {
			store.Set(ch.Adapter, ch.DestPath, ch.OldContent)
			recorded = true
		}
		if ch.SrcAbs != "" && store.CanonicalBaselineHash(ch.SrcAbs) == "" {
			store.SetCanonical(ch.SrcAbs, ch.SrcContent)
			recorded = true
		}
	case DirPull:
		if ch.NewContent != nil && store.CanonicalBaselineHash(ch.DestPath) == "" {
			store.SetCanonical(ch.DestPath, ch.NewContent)
			recorded = true
		}
		if ch.SrcAbs != "" && store.BaselineHash(ch.Adapter, ch.SrcAbs) == "" {
			store.Set(ch.Adapter, ch.SrcAbs, ch.SrcContent)
			recorded = true
		}
	}
	return recorded
}
