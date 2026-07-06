package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/zhivko-kocev/friday/internal/config"
)

// CompileResult reports what a compile extracted, what it emitted, and what
// fell on the floor between the two agent formats.
type CompileResult struct {
	Extracted []Change // from-agent target → temp store
	Emitted   []Change // temp store → to-agent target (dry-run when requested)
	Lossy     []string // extracted-but-unconsumed files and unsupported-rule notes
}

// Compile converts one agent's installed config into another's format
// without touching ~/.friday. It imports the from-adapter's target into a
// throwaway store, checks nothing gets lost, then pushes that store through
// the to-adapter's rules. A temp dir instead of an in-memory store keeps the
// whole engine (planning, drift, atomic writes) reusable as-is.
//
// When the conversion is lossy and allowLossy is false, Compile returns the
// result with Lossy populated and Emitted nil — nothing is written.
func Compile(cfg *config.Config, from, to string, allowLossy bool, opts Options) (*CompileResult, error) {
	if from == to {
		return nil, fmt.Errorf("--from and --to are both %q", from)
	}
	for _, name := range []string{from, to} {
		if _, ok := cfg.Adapters[name]; !ok {
			return nil, fmt.Errorf("unknown adapter %q (defined: %v)", name, cfg.AdapterNames())
		}
	}

	tmp, err := os.MkdirTemp("", "friday-compile-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	res := &CompileResult{}
	// Every phase runs against a throwaway drift state. Compile reads the
	// from-adapter's real target dir, and recording those files in the real
	// drift store would re-baseline them to their current content — silently
	// disarming the conflict prompt on the next real push.
	driftPath := filepath.Join(tmp, "drift-state.json")

	// Phase 1 — extract: materialize the from-adapter's target into the temp
	// store. The store is empty, so no conflicts are possible.
	cfgFrom := config.NewDefault(cfg.Scope, tmp, cfg.TargetRoot, map[string]*config.Adapter{from: cfg.Adapters[from]})
	res.Extracted, err = Import(cfgFrom, Options{driftPath: driftPath})
	if err != nil {
		return nil, fmt.Errorf("extract %s: %w", from, err)
	}
	extracted := map[string]bool{}
	for _, ch := range res.Extracted {
		switch ch.Action {
		case ActionCreate, ActionUpdate, ActionInSync:
			extracted[ch.DestRel] = true
		case ActionUnsupported:
			res.Lossy = append(res.Lossy, fmt.Sprintf("%s: %s", ch.DestRel, ch.Reason))
		}
	}

	// Phase 2 — lossiness check: anything extracted that the to-adapter's
	// push plan doesn't consume would silently vanish in the conversion.
	cfgTo := config.NewDefault(cfg.Scope, tmp, cfg.TargetRoot, map[string]*config.Adapter{to: cfg.Adapters[to]})
	planned, err := Push(cfgTo, Options{DryRun: true, driftPath: driftPath})
	if err != nil {
		return nil, fmt.Errorf("plan %s: %w", to, err)
	}
	consumed := map[string]bool{}
	for _, ch := range planned {
		if ch.Action == ActionMissingSource {
			continue
		}
		for _, src := range ch.Sources {
			consumed[src] = true
		}
	}
	for rel := range extracted {
		if !consumed[rel] {
			res.Lossy = append(res.Lossy, fmt.Sprintf("%s: no %s rule consumes it", rel, to))
		}
	}
	sort.Strings(res.Lossy)
	if len(res.Lossy) > 0 && !allowLossy {
		return res, nil
	}

	// Phase 3 — emit for real (or dry-run). Still on the throwaway drift
	// state: compiled output deliberately gets NO real baselines, so a later
	// `friday push` sees it as foreign and prompts instead of silently
	// clobbering it. Existing target files likewise read as drifted here,
	// which routes every overwrite through the conflict prompt (or --force).
	res.Emitted, err = Push(cfgTo, Options{
		DryRun:     opts.DryRun,
		Force:      opts.Force,
		OnConflict: opts.OnConflict,
		driftPath:  driftPath,
	})
	if err != nil {
		return nil, fmt.Errorf("emit %s: %w", to, err)
	}
	return res, nil
}
