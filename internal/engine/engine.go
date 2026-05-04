// Package engine executes the rule sets defined in friday.yaml. It plans
// changes (without writing), resolves drift via a caller-supplied
// resolver, and applies the survivors to disk.
package engine

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/drift"
	"github.com/zhivko-kocev/friday/internal/output"
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

// Status reports the diff between store and targets without writing.
func Status(cfg *config.Config) ([]Change, error) {
	return run(cfg, Options{DryRun: true}, DirPush)
}

func run(cfg *config.Config, opts Options, dir Direction) ([]Change, error) {
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

		var changes []Change
		switch dir {
		case DirPush:
			changes, err = planPush(name, ad, storeAbs, targetAbs)
		case DirPull:
			changes, err = planPull(name, ad, storeAbs, targetAbs)
		}
		if err != nil {
			return nil, fmt.Errorf("adapter %s: %w", name, err)
		}

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

// resolveConflict downgrades Update actions when the destination has drifted,
// invoking the caller's resolver (or, if absent, marking the change as a
// Conflict so it gets skipped).
//
// Drift detection only applies to push: pull's whole purpose is to capture
// changes, so an Update there is always intentional.
func resolveConflict(ch *Change, store *drift.Store, opts Options) {
	if ch.Direction != DirPush || ch.Action != ActionUpdate {
		return
	}
	drifted, exists := store.Check(ch.Adapter, ch.DestPath)
	if !exists || !drifted {
		return
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

	choice := opts.OnConflict(ConflictInfo{
		Adapter:    ch.Adapter,
		Direction:  ch.Direction,
		Sources:    ch.Sources,
		DestPath:   ch.DestPath,
		DestRel:    ch.DestRel,
		OldContent: ch.OldContent,
		NewContent: ch.NewContent,
	})
	switch choice {
	case ConflictKeepCanonical:
		// Proceed with the planned write.
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
		if err := os.WriteFile(ch.DestPath, ch.NewContent, mode); err != nil {
			return false, err
		}
		// WriteFile only honors the perm bits on create; chmod ensures
		// pre-existing targets pick up the executable bit too.
		if err := os.Chmod(ch.DestPath, mode); err != nil {
			return false, err
		}
		// Drift store only tracks adapter targets (push direction). On pull
		// we're writing into the store itself — no tracking needed.
		if dir == DirPush {
			store.Set(ch.Adapter, ch.DestPath, ch.NewContent)
		}
		return true, nil
	case ActionInSync:
		// On push, if the user chose "take target", record the current dest
		// hash so future pushes don't keep flagging it.
		if dir == DirPush && ch.Reason == reasonAcceptedDrift {
			store.Set(ch.Adapter, ch.DestPath, ch.OldContent)
			return true, nil
		}
	}
	return false, nil
}
